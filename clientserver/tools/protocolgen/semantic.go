package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strconv"

	"golang.org/x/tools/go/packages"
)

// sourceRouteIR binds the producer registration, handler, and client request
// construction before code generation. It is deliberately intolerant: a source
// shape we do not understand is a generation failure rather than a fallback.
func sourceRouteIR(m module, routes []route, operations []operation) (routeIR, error) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps, Dir: m.Dir}
	loaded, err := packages.Load(cfg, "./internal/server", "./internal/client", "./internal/proto")
	if err != nil || packages.PrintErrors(loaded) != 0 {
		return routeIR{}, fmt.Errorf("load producer route semantics: %w", err)
	}
	byPath := map[string]*packages.Package{}
	for _, pkg := range loaded {
		byPath[pkg.PkgPath] = pkg
	}
	server, client := byPath[m.Path+"/internal/server"], byPath[m.Path+"/internal/client"]
	if server == nil || client == nil {
		return routeIR{}, fmt.Errorf("load producer route semantics packages")
	}
	field, err := errorJSONField(byPath[m.Path+"/internal/proto"])
	if err != nil {
		return routeIR{}, err
	}
	ir := routeIR{
		decodeError:         errorIR{status: 400, message: "", field: field, contentType: ""},
		responseHeaders:     make(map[string][]headerParam),
		clientIDValidations: make(map[string]clientIDValidationIR),
	}
	for _, r := range routes {
		var mapped *operation
		for i := range operations {
			op := &operations[i]
			if op.Method == r.Method && normalizeRoutePath(op.Path) == normalizeRoutePath(r.Path) {
				mapped = op
				break
			}
		}
		if mapped == nil {
			continue
		}
		handler := findNamedHandler(server, r.Handler)
		if handler == nil {
			return routeIR{}, fmt.Errorf("%s %s: registered handler %s not found", r.Method, r.Path, r.Handler)
		}
		headers, err := extractResponseHeaders(server, handler)
		if err != nil {
			return routeIR{}, err
		}
		for _, candidate := range operations {
			if candidate.Method == r.Method && normalizeRoutePath(candidate.Path) == normalizeRoutePath(r.Path) {
				ir.responseHeaders[candidate.Name] = headers
			}
		}
		validation, hasValidation, err := extractClientIDValidation(server, handler, field)
		if err != nil {
			return routeIR{}, err
		}
		if hasValidation {
			ir.clientIDValidations[mapped.Name] = *validation
		}
		if spec, ok, err := extractStreamIR(server, handler, r, validation); err != nil {
			return routeIR{}, err
		} else if ok {
			if ir.stream != nil {
				return routeIR{}, fmt.Errorf("multiple producer SSE handlers")
			}
			if mapped.Name == "" || mapped.Classification != "http" {
				return routeIR{}, fmt.Errorf("%s %s: SSE handler has no typed client operation", r.Method, r.Path)
			}
			spec.method = mapped.Name
			spec.requestType = operationTypeName(mapped.Name)
			spec.producerType = spec.requestType + "Producer"
			spec.pathParams = append([]string(nil), mapped.PathParams...)
			spec.queryRequired = append([]string(nil), mapped.QueryParams...)
			spec.requestHeader = append([]headerParam(nil), mapped.HeaderParams...)
			ir.stream = spec
		}
		controls, err := extractControlIR(server, client, handler, r, operations)
		if err != nil {
			return routeIR{}, err
		}
		if len(controls) != 0 {
			if len(ir.control) != 0 {
				return routeIR{}, fmt.Errorf("multiple producer discriminated handlers")
			}
			ir.control = controls
			decode, unknown, err := handlerJSONErrors(server, handler, field)
			if err != nil {
				return routeIR{}, err
			}
			ir.decodeError = decode
			ir.unknownError = unknown
			if unknown.message == "" {
				return routeIR{}, fmt.Errorf("%s: discriminated handler lacks default json error", handler.Name.Name)
			}
		}
	}
	if len(ir.control) == 0 || ir.stream == nil || ir.decodeError.message == "" {
		return routeIR{}, fmt.Errorf("producer lacks required control/SSE/decode semantic evidence")
	}
	return ir, nil
}

type clientIDValidationIR struct {
	query       string
	empty       errorIR
	invalid     errorIR
	parserPath  string
	parserAlias string
	parserFunc  string
}

func errorJSONField(proto *packages.Package) (string, error) {
	if proto == nil {
		return "", fmt.Errorf("load internal/proto")
	}
	obj, ok := proto.Types.Scope().Lookup("Error").(*types.TypeName)
	if !ok {
		return "", fmt.Errorf("proto.Error missing")
	}
	st, ok := obj.Type().Underlying().(*types.Struct)
	if !ok {
		return "", fmt.Errorf("proto.Error is not a struct")
	}
	var field string
	for i := 0; i < st.NumFields(); i++ {
		candidate := jsonTag(st.Tag(i))
		if candidate == "" || candidate == "-" {
			continue
		}
		if field != "" {
			return "", fmt.Errorf("proto.Error has multiple JSON fields")
		}
		field = st.Field(i).Name()
	}
	if field == "" {
		return "", fmt.Errorf("proto.Error has no JSON error field")
	}
	return field, nil
}
func jsonTag(tag string) string { // the source type/tag is the authority; this only splits Go's tag syntax.
	for len(tag) > 0 {
		var k, v string
		n, err := fmt.Sscanf(tag, `%s`, &k)
		_ = n
		_ = err
		_ = v
		break
	}
	// json tags in source are conventional and go/types already exposes the raw tag.
	const marker = `json:"`
	for i := 0; i+len(marker) <= len(tag); i++ {
		if tag[i:i+len(marker)] == marker {
			rest := tag[i+len(marker):]
			for j, c := range rest {
				if c == '"' {
					if comma := indexComma(rest[:j]); comma >= 0 {
						return rest[:comma]
					}
					return rest[:j]
				}
			}
		}
	}
	return ""
}
func indexComma(s string) int {
	for i := range s {
		if s[i] == ',' {
			return i
		}
	}
	return -1
}
func findNamedHandler(pkg *packages.Package, name string) *ast.FuncDecl {
	for _, f := range pkg.Syntax {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
				return fn
			}
		}
	}
	return nil
}
func extractResponseHeaders(pkg *packages.Package, handler *ast.FuncDecl) ([]headerParam, error) {
	if pkg == nil || handler == nil {
		return nil, fmt.Errorf("response header extraction needs a source handler")
	}
	headerFns := []*ast.FuncDecl{handler}
	usesJSONEncode := false
	ast.Inspect(handler.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "jsonEncode" {
			usesJSONEncode = true
		}
		return true
	})
	if usesJSONEncode {
		helper := findNamedHandler(pkg, "jsonEncode")
		if helper == nil {
			return nil, fmt.Errorf("%s: jsonEncode helper not found", handler.Name.Name)
		}
		headerFns = append(headerFns, helper)
	}
	byName := map[string]headerParam{}
	for _, fn := range headerFns {
		var extractErr error
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if extractErr != nil {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok || !isResponseHeaderMutation(pkg, call) {
				return true
			}
			name, nameOK := literalString(call.Args[0])
			value, valueOK := literalString(call.Args[1])
			if !nameOK || !valueOK {
				extractErr = fmt.Errorf("%s: response header %s must use literal name and value", fn.Name.Name, call.Fun.(*ast.SelectorExpr).Sel.Name)
				return false
			}
			byName[name] = headerParam{Name: name, Values: []string{value}}
			return true
		})
		if extractErr != nil {
			return nil, extractErr
		}
	}
	headers := make([]headerParam, 0, len(byName))
	for _, header := range byName {
		headers = append(headers, header)
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].Name < headers[j].Name })
	return headers, nil
}

func isResponseHeaderMutation(pkg *packages.Package, call *ast.CallExpr) bool {
	if len(call.Args) != 2 {
		return false
	}
	set, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (set.Sel.Name != "Set" && set.Sel.Name != "Add") {
		return false
	}
	header, ok := set.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	headerSelector, ok := header.Fun.(*ast.SelectorExpr)
	if !ok || headerSelector.Sel.Name != "Header" {
		return false
	}
	return types.TypeString(pkg.TypesInfo.TypeOf(headerSelector.X), func(p *types.Package) string { return p.Path() }) == "net/http.ResponseWriter"
}

func constString(pkg *packages.Package, expr ast.Expr) (string, error) {
	c, ok := pkg.TypesInfo.Uses[selectorName(expr)].(*types.Const)
	if !ok {
		return "", fmt.Errorf("nonconstant value")
	}
	return strconv.Unquote(c.Val().ExactString())
}
func constInt(pkg *packages.Package, expr ast.Expr) (int, error) {
	c, ok := pkg.TypesInfo.Uses[selectorName(expr)].(*types.Const)
	if !ok {
		return 0, fmt.Errorf("nonconstant status")
	}
	return strconv.Atoi(c.Val().ExactString())
}

func handlerJSONErrors(pkg *packages.Package, fn *ast.FuncDecl, field string) (errorIR, errorIR, error) {
	var values []errorIR
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "jsonError" {
			return true
		}
		status, err := constInt(pkg, call.Args[1])
		if err != nil {
			return true
		}
		message, ok := literalString(call.Args[2])
		if !ok {
			return true
		}
		values = append(values, errorIR{status: status, message: message, field: field, contentType: "application/json"})
		return true
	})
	if len(values) < 2 {
		return errorIR{}, errorIR{}, fmt.Errorf("%s: expected decode and default jsonError evidence", fn.Name.Name)
	}
	return values[0], values[len(values)-1], nil
}
func extractControlIR(server, client *packages.Package, handler *ast.FuncDecl, route route, operations []operation) ([]controlIR, error) {
	var sw *ast.SwitchStmt
	ast.Inspect(handler.Body, func(n ast.Node) bool {
		if x, ok := n.(*ast.SwitchStmt); ok {
			sw = x
			return false
		}
		return true
	})
	if sw == nil {
		return nil, nil
	}
	fieldExpr, ok := sw.Tag.(*ast.SelectorExpr)
	if !ok {
		return nil, fmt.Errorf("%s: unsupported discriminator", handler.Name.Name)
	}
	field := fieldExpr.Sel.Name
	byValue := map[string]string{}
	allowed := map[string]bool{}
	for _, op := range operations {
		if op.Method == route.Method && normalizeRoutePath(op.Path) == normalizeRoutePath(route.Path) {
			allowed[op.Name] = true
		}
	}
	for _, file := range client.Syntax {
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !allowed[fn.Name.Name] {
				continue
			}
			gotField, value, err := extractClientDiscriminator(fn, client)
			if err != nil {
				return nil, err
			}
			if gotField == field && value != "" {
				byValue[value] = fn.Name.Name
			}
		}
	}
	var out []controlIR
	for _, s := range sw.Body.List {
		clause := s.(*ast.CaseClause)
		if len(clause.List) == 0 {
			continue
		}
		for _, expr := range clause.List {
			value, err := constString(server, expr)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", handler.Name.Name, err)
			}
			method := byValue[value]
			if method == "" {
				return nil, fmt.Errorf("%s: no client request construction for discriminator %q", handler.Name.Name, value)
			}
			out = append(out, controlIR{method: method, field: field, value: value})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no discriminator variants", handler.Name.Name)
	}
	return out, nil
}
func extractStreamIR(pkg *packages.Package, fn *ast.FuncDecl, r route, validation *clientIDValidationIR) (*streamIR, bool, error) {
	var headers []headerParam
	var status int
	var frame string
	flushes := 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Set" && len(call.Args) == 2 {
			k, kok := literalString(call.Args[0])
			v, vok := literalString(call.Args[1])
			if kok && vok {
				headers = append(headers, headerParam{Name: k, Values: []string{v}})
			}
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WriteHeader" && len(call.Args) == 1 {
			if x, e := constInt(pkg, call.Args[0]); e == nil {
				status = x
			}
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Flush" {
			flushes++
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Fprintf" && len(call.Args) >= 2 {
			if x, ok := literalString(call.Args[1]); ok {
				frame = x
			}
		}
		return true
	})
	if len(headers) == 0 || status == 0 || frame == "" || flushes < 2 {
		return nil, false, nil
	}
	if validation == nil {
		return nil, false, fmt.Errorf("%s: SSE handler lacks source query validation", fn.Name.Name)
	}
	return &streamIR{route: r, headers: headers, status: status, frame: frame, initialFlush: true, eventFlush: true, queryRequired: []string{validation.query}}, true, nil
}

func extractClientIDValidation(pkg *packages.Package, handler *ast.FuncDecl, field string) (*clientIDValidationIR, bool, error) {
	var helpers []*ast.FuncDecl
	ast.Inspect(handler.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 || !isIdentifier(call.Args[0], "w") || !isIdentifier(call.Args[1], "r") {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if candidate := findNamedHandler(pkg, selector.Sel.Name); candidate != nil {
			helpers = append(helpers, candidate)
		}
		return true
	})
	if len(helpers) == 0 {
		return nil, false, nil
	}
	if len(helpers) != 1 {
		return nil, false, fmt.Errorf("%s: ambiguous source request validator helpers", handler.Name.Name)
	}
	validation, err := parseClientIDValidation(pkg, helpers[0], field)
	if err != nil {
		return nil, false, err
	}
	return &validation, true, nil
}

func parseClientIDValidation(pkg *packages.Package, helper *ast.FuncDecl, field string) (clientIDValidationIR, error) {
	var validation clientIDValidationIR
	var clientValue string
	ast.Inspect(helper.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		name, ok := assignment.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if query, ok := sourceQueryGet(assignment.Rhs[0]); ok {
			clientValue, validation.query = name.Name, query
		}
		return true
	})
	if validation.query == "" || clientValue == "" {
		return clientIDValidationIR{}, fmt.Errorf("%s: validator lacks source query binding", helper.Name.Name)
	}
	ast.Inspect(helper.Body, func(node ast.Node) bool {
		ifStmt, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		if message, status, ok := sourceJSONError(pkg, ifStmt.Body); ok {
			switch {
			case isEmptyComparison(ifStmt.Cond, clientValue):
				validation.empty = errorIR{status: status, message: message, field: field, contentType: "application/json"}
			case hasUUIDParse(pkg, ifStmt, clientValue):
				validation.invalid = errorIR{status: status, message: message, field: field, contentType: "application/json"}
				ast.Inspect(ifStmt, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Parse" {
						if fn, ok := pkg.TypesInfo.Uses[selector.Sel].(*types.Func); ok && fn.Pkg() != nil {
							validation.parserPath = fn.Pkg().Path()
							validation.parserAlias = fn.Pkg().Name()
							validation.parserFunc = fn.Name()
						}
					}
					return true
				})
			}
		}
		return true
	})
	if validation.empty.message == "" || validation.invalid.message == "" || validation.parserPath == "" {
		return clientIDValidationIR{}, fmt.Errorf("%s: incomplete source UUID validator", helper.Name.Name)
	}
	return validation, nil
}

func sourceQueryGet(expression ast.Expr) (string, bool) {
	get, ok := expression.(*ast.CallExpr)
	if !ok || len(get.Args) != 1 {
		return "", false
	}
	selector, ok := get.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Get" {
		return "", false
	}
	return literalString(get.Args[0])
}

func sourceJSONError(pkg *packages.Package, body *ast.BlockStmt) (string, int, bool) {
	var message string
	var status int
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok || name.Name != "jsonError" {
			return true
		}
		var err error
		status, err = constInt(pkg, call.Args[1])
		if err != nil {
			status = 0
			return false
		}
		message, _ = literalString(call.Args[2])
		return false
	})
	return message, status, message != "" && status != 0
}

func isIdentifier(expression ast.Expr, name string) bool {
	ident, ok := expression.(*ast.Ident)
	return ok && ident.Name == name
}

func isEmptyComparison(expression ast.Expr, name string) bool {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op.String() != "==" {
		return false
	}
	left, leftName := binary.X.(*ast.Ident)
	right, rightName := binary.Y.(*ast.Ident)
	leftEmpty, leftOK := literalString(binary.X)
	rightEmpty, rightOK := literalString(binary.Y)
	return (leftName && left.Name == name && rightOK && rightEmpty == "") || (rightName && right.Name == name && leftOK && leftEmpty == "")
}

func hasUUIDParse(pkg *packages.Package, node ast.Node, clientValue string) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || !isIdentifier(call.Args[0], clientValue) {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Parse" {
			return true
		}
		fn, ok := pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
		if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/google/uuid" {
			return true
		}
		found = true
		return false
	})
	return found
}

func controlDispatchDecls(operations []operation, semantics routeIR) []byte {
	if len(semantics.control) == 0 {
		return nil
	}
	byName := map[string]operation{}
	for _, op := range operations {
		byName[op.Name] = op
	}
	first, ok := byName[semantics.control[0].method]
	if !ok {
		return nil
	}
	var out bytes.Buffer
	key := first.Method + " " + first.Path
	inputType := operationTypeName(first.Name)
	fmt.Fprintf(&out, "private.HandleFunc(%q,func(w http.ResponseWriter,req *http.Request){ var input %sRequest; input.Header=req.Header.Clone(); if !decodeRequestJSON(w,req,&input.Body,maxBodyBytes,%d,%q){return}; switch input.Body.%s {", key, inputType, semantics.decodeError.status, semantics.decodeError.message, semantics.control[0].field)
	for _, variant := range semantics.control {
		op, ok := byName[variant.method]
		if !ok {
			continue
		}
		reqType := operationTypeName(op.Name)
		fmt.Fprintf(&out, "case %q: output,err:=impl.%s(req.Context(),%sRequest{Header:input.Header,Body:input.Body});if err!=nil{if !WriteHTTPError(w,err){writeSourceError(w,http.StatusInternalServerError,err.Error())};return};", variant.value, op.Name, reqType)
		if op.HasResult {
			fmt.Fprintf(&out, "writeJSON(w,output.Status,output.Metadata,%s,output.Result);", intSlice(op.AcceptedStatuses))
		} else {
			fmt.Fprintf(&out, "writeEmpty(w,output.Status,output.Metadata,%s);", intSlice(op.AcceptedStatuses))
		}
	}
	fmt.Fprintf(&out, "default: writeSourceError(w,%d,%q)} })\n", semantics.unknownError.status, semantics.unknownError.message)
	return out.Bytes()
}
func writeClientIDValidation(out *bytes.Buffer, value string, validation clientIDValidationIR) {
	fmt.Fprintf(out, "if %s==\"\"{writeSourceError(w,%d,%q);return};", value, validation.empty.status, validation.empty.message)
	fmt.Fprintf(out, "if _,err:=%s.%s(%s);err!=nil{writeSourceError(w,%d,%q);return};", validation.parserAlias, validation.parserFunc, value, validation.invalid.status, validation.invalid.message)
}

func streamDispatchDecls(operations []operation, semantics routeIR) []byte {
	if semantics.stream == nil {
		return nil
	}
	var op *operation
	for i := range operations {
		if operations[i].Method == semantics.stream.route.Method && normalizeRoutePath(operations[i].Path) == normalizeRoutePath(semantics.stream.route.Path) {
			op = &operations[i]
			break
		}
	}
	if op == nil {
		return nil
	}
	var out bytes.Buffer
	stream := semantics.stream
	fmt.Fprintf(&out, "private.HandleFunc(%q,func(w http.ResponseWriter,req *http.Request){if !requireEmptyRequestBody(w,req,maxBodyBytes,%d,%q){return};", stream.route.Method+" "+stream.route.Path, semantics.decodeError.status, semantics.decodeError.message)
	fmt.Fprintf(&out, "input:=%sRequest{Header:req.Header.Clone()};", stream.requestType)
	for _, p := range stream.pathParams {
		fmt.Fprintf(&out, "input.%s=req.PathValue(%q);", exportedField(p), p)
	}
	for _, q := range stream.queryRequired {
		fmt.Fprintf(&out, "input.Query.%s=req.URL.Query().Get(%q);", exportedField(q), q)
	}
	validation, ok := semantics.clientIDValidations[stream.method]
	if !ok {
		return nil
	}
	writeClientIDValidation(&out, "input.Query."+exportedField(validation.query), validation)
	fmt.Fprintf(&out, "producer,err:=impl.%s(req.Context(),input);if err!=nil{if !WriteHTTPError(w,err){writeSourceError(w,http.StatusInternalServerError,err.Error())};return};if producer==nil{writeSourceError(w,http.StatusInternalServerError,\"nil event producer\");return};", stream.method)
	for _, h := range stream.headers {
		for _, v := range h.Values {
			fmt.Fprintf(&out, "w.Header().Set(%q,%q);", h.Name, v)
		}
	}
	fmt.Fprintf(&out, "w.WriteHeader(%d);f:=http.NewResponseController(w);", stream.status)
	if stream.initialFlush {
		fmt.Fprint(&out, "_ = f.Flush();")
	}
	fmt.Fprintf(&out, "sink:=&sseSink{ctx:req.Context(),w:w,f:f,frame:%q};err=producer.Serve(req.Context(),sink);if err!=nil&&req.Context().Err()==nil{_=sink.Close(err);return};_=sink.Close(nil) })\n", stream.frame)
	return out.Bytes()
}

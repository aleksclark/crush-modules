package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

func serverOnlyOperations(m module, routes []route, clientOps []operation) ([]operation, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps,
		Dir:  m.Dir,
	}
	loaded, err := packages.Load(cfg, "./internal/server", "./internal/backend", "./internal/config")
	if err != nil || packages.PrintErrors(loaded) > 0 {
		return nil, fmt.Errorf("load server packages for server-only operations: %w", err)
	}
	byPath := map[string]*packages.Package{}
	for _, pkg := range loaded {
		byPath[pkg.PkgPath] = pkg
	}
	server := byPath[m.Path+"/internal/server"]
	if server == nil {
		return nil, fmt.Errorf("load internal/server for server-only operations")
	}
	var out []operation
	for _, route := range routes {
		if routeClassification(route, clientOps) != "typed_server_only" {
			continue
		}
		op, err := deriveServerOnlyOperation(m, server, byPath, route)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, nil
}

func deriveServerOnlyOperation(m module, server *packages.Package, byPath map[string]*packages.Package, route route) (operation, error) {
	handler, file, err := findRouteHandler(server, route)
	if err != nil {
		return operation{}, err
	}
	name := strings.TrimPrefix(handler.Name.Name, "handle")
	if name == handler.Name.Name {
		return operation{}, fmt.Errorf("%s: server-only handler %s has no handle prefix", pos(server, handler.Pos()), handler.Name.Name)
	}
	result, err := handlerJSONResult(m, server, byPath, handler)
	if err != nil {
		return operation{}, err
	}
	acceptedStatuses, statusSemantics, err := extractServerStatusSemantics(server, handler)
	if err != nil {
		return operation{}, err
	}
	op := operation{
		clientMethod: clientMethod{
			Name:           name,
			Method:         route.Method,
			Path:           route.Path,
			Classification: "typed_server_only",
		},
		HasResult:        true,
		ResultWire:       result,
		PathParams:       templateParams(route.Path),
		Source:           pos(server, handler.Pos()),
		StatusSemantics:  statusSemantics,
		AcceptedStatuses: acceptedStatuses,
	}
	_ = file
	return op, nil
}

// extractServerStatusSemantics is the server-only counterpart to client status
// extraction. jsonEncode proves its source-default 200; other status writes
// must resolve through the producer type checker.
func extractServerStatusSemantics(pkg *packages.Package, handler *ast.FuncDecl) ([]int, string, error) {
	statuses := map[int]bool{}
	var walkErr error
	ast.Inspect(handler.Body, func(node ast.Node) bool {
		if walkErr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "jsonEncode" {
			statuses[200] = true
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "WriteHeader" || len(call.Args) != 1 {
			return true
		}
		status, err := constInt(pkg, call.Args[0])
		if err != nil {
			walkErr = fmt.Errorf("%s: unsupported server response status: %w", handler.Name.Name, err)
			return false
		}
		statuses[status] = true
		return true
	})
	if walkErr != nil {
		return nil, "", walkErr
	}
	if len(statuses) == 0 {
		return nil, "source_unspecified", nil
	}
	values := make([]int, 0, len(statuses))
	for status := range statuses {
		values = append(values, status)
	}
	return sortedInts(values), "guarded", nil
}

func findRouteHandler(server *packages.Package, route route) (*ast.FuncDecl, *ast.File, error) {
	want := route.Method + " " + route.Path
	if route.Method == "" {
		want = route.Path
	}
	for _, file := range server.Syntax {
		var handlerName string
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			pattern, err := strconvUnquote(lit.Value)
			if err != nil || pattern != want {
				return true
			}
			handlerName = handlerIdent(call.Args[1])
			return false
		})
		if handlerName == "" {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == handlerName {
				return fn, file, nil
			}
		}
		for _, other := range server.Syntax {
			for _, decl := range other.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Name.Name == handlerName {
					return fn, other, nil
				}
			}
		}
	}
	return nil, nil, fmt.Errorf("%s %s: server-only route handler not found", route.Method, route.Path)
}

func handlerIdent(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.Ident:
		return v.Name
	}
	return ""
}

func handlerJSONResult(m module, server *packages.Package, byPath map[string]*packages.Package, handler *ast.FuncDecl) (types.Type, error) {
	var encoded ast.Expr
	ast.Inspect(handler.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Name == "jsonEncode" {
				encoded = call.Args[1]
			}
		case *ast.SelectorExpr:
			if fun.Sel.Name == "jsonEncode" {
				encoded = call.Args[1]
			}
		}
		return true
	})
	if encoded == nil {
		return nil, fmt.Errorf("%s: server handler %s has no jsonEncode result", pos(server, handler.Pos()), handler.Name.Name)
	}
	t := server.TypesInfo.TypeOf(encoded)
	if t == nil {
		return nil, fmt.Errorf("%s: unresolved jsonEncode argument", pos(server, encoded.Pos()))
	}
	if !isUntypedAny(t) {
		return t, nil
	}
	ident, ok := encoded.(*ast.Ident)
	if !ok {
		return nil, fmt.Errorf("%s: server handler encodes untyped value without a resolvable source", pos(server, encoded.Pos()))
	}
	return resolveAssignedResult(m, server, byPath, handler, ident)
}

func isUntypedAny(t types.Type) bool {
	iface, ok := t.Underlying().(*types.Interface)
	return ok && iface.Empty()
}

func resolveAssignedResult(m module, server *packages.Package, byPath map[string]*packages.Package, handler *ast.FuncDecl, ident *ast.Ident) (types.Type, error) {
	var rhs ast.Expr
	ast.Inspect(handler.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, left := range assign.Lhs {
			name, ok := left.(*ast.Ident)
			if !ok || name.Name != ident.Name || i >= len(assign.Rhs) {
				continue
			}
			rhs = assign.Rhs[i]
		}
		return true
	})
	call, ok := rhs.(*ast.CallExpr)
	if !ok {
		return nil, fmt.Errorf("%s: cannot resolve encoded %s", pos(server, ident.Pos()), ident.Name)
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, fmt.Errorf("%s: encoded value is not a backend call", pos(server, call.Pos()))
	}
	backend := byPath[m.Path+"/internal/backend"]
	if backend == nil {
		return nil, fmt.Errorf("load internal/backend")
	}
	fn := findFunc(backend, sel.Sel.Name)
	if fn == nil {
		return nil, fmt.Errorf("%s: backend method %s not found", pos(backend, sel.Pos()), sel.Sel.Name)
	}
	return backendConcreteReturn(m, backend, byPath, fn)
}

func backendConcreteReturn(m module, backend *packages.Package, byPath map[string]*packages.Package, fn *ast.FuncDecl) (types.Type, error) {
	var returned ast.Expr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		ret, ok := node.(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 {
			return true
		}
		if ident, ok := ret.Results[0].(*ast.Ident); ok && ident.Name == "nil" {
			return true
		}
		returned = ret.Results[0]
		return true
	})
	if returned == nil {
		return nil, fmt.Errorf("%s: backend %s has no concrete return", pos(backend, fn.Pos()), fn.Name.Name)
	}
	if ident, ok := returned.(*ast.Ident); ok {
		var rhs ast.Expr
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, left := range assign.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok || name.Name != ident.Name || i >= len(assign.Rhs) {
					continue
				}
				rhs = assign.Rhs[i]
			}
			return true
		})
		if call, ok := rhs.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				pkgName, ok := sel.X.(*ast.Ident)
				if ok {
					if pkgName.Name == "config" {
						cfg := byPath[m.Path+"/internal/config"]
						if cfg == nil {
							return nil, fmt.Errorf("load internal/config")
						}
						obj := cfg.Types.Scope().Lookup(sel.Sel.Name)
						sig, ok := obj.Type().(*types.Signature)
						if !ok || sig.Results().Len() == 0 {
							return nil, fmt.Errorf("%s: config.%s has no result", pos(cfg, sel.Pos()), sel.Sel.Name)
						}
						return sig.Results().At(0).Type(), nil
					}
				}
			}
		}
	}
	t := backend.TypesInfo.TypeOf(returned)
	if t == nil || isUntypedAny(t) {
		return nil, fmt.Errorf("%s: backend %s return is not a concrete wire type", pos(backend, fn.Pos()), fn.Name.Name)
	}
	return t, nil
}

func findFunc(pkg *packages.Package, name string) *ast.FuncDecl {
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == name {
				return fn
			}
		}
	}
	return nil
}

func pos(pkg *packages.Package, p token.Pos) string {
	if pkg == nil || pkg.Fset == nil {
		return "unknown"
	}
	return pkg.Fset.Position(p).String()
}

func strconvUnquote(v string) (string, error) {
	if strings.HasPrefix(v, "`") {
		return strings.Trim(v, "`"), nil
	}
	return strconvUnquoteStd(v)
}

func strconvUnquoteStd(v string) (string, error) {
	return parserUnquote(v)
}

func parserUnquote(v string) (string, error) {
	return unquoteString(v)
}

func unquoteString(v string) (string, error) {
	if len(v) >= 2 && v[0] == '"' {
		return astString(v)
	}
	return "", fmt.Errorf("not a string")
}

func astString(v string) (string, error) {
	return strconv.Unquote(v)
}

func sourceAuthAndErrorMetadata(m module) (authConfigured, authOptional, errorField string, mappings map[string]string, err error) {
	fset := token.NewFileSet()
	serverFile := filepath.Join(m.Dir, "internal", "server", "server.go")
	parsed, err := parser.ParseFile(fset, serverFile, nil, 0)
	if err != nil {
		return "", "", "", nil, err
	}
	authConfigured = extractAuthConfigured(parsed)
	if authConfigured == "" {
		return "", "", "", nil, fmt.Errorf("%s: configured token 401 WWW-Authenticate behavior not found", serverFile)
	}
	clientFile := filepath.Join(m.Dir, "internal", "client", "client.go")
	clientParsed, err := parser.ParseFile(fset, clientFile, nil, 0)
	if err != nil {
		return "", "", "", nil, err
	}
	authOptional = extractAuthOptional(clientParsed)
	if authOptional == "" {
		return "", "", "", nil, fmt.Errorf("%s: optional/empty token behavior not found", clientFile)
	}
	errorField, err = extractErrorJSONField(filepath.Join(m.Dir, "internal", "proto", "proto.go"))
	if err != nil {
		return "", "", "", nil, err
	}
	mappings, err = extractErrorMappings(m)
	return authConfigured, authOptional, errorField, mappings, err
}

func extractAuthConfigured(file *ast.File) string {
	foundHeader, foundBearer := false, false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Set" || len(call.Args) != 2 {
			return true
		}
		key, _ := literalString(call.Args[0])
		val, _ := literalString(call.Args[1])
		if key == "WWW-Authenticate" && val == "Bearer" {
			foundHeader = true
			foundBearer = true
		}
		return true
	})
	if foundHeader && foundBearer {
		return "configured token: missing or invalid Authorization is 401 with WWW-Authenticate Bearer"
	}
	return ""
}

func extractAuthOptional(file *ast.File) string {
	var found bool
	ast.Inspect(file, func(node ast.Node) bool {
		ifStmt, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		bin, ok := ifStmt.Cond.(*ast.BinaryExpr)
		if !ok || bin.Op != token.NEQ {
			return true
		}
		sel, ok := bin.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "authToken" {
			return true
		}
		found = true
		return false
	})
	if found {
		return "optional/empty token: omit Authorization and allow unauthenticated requests"
	}
	return ""
}

func extractErrorJSONField(path string) (string, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return "", err
	}
	for _, decl := range parsed.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Error" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			var jsonField string
			for _, field := range st.Fields.List {
				if field.Tag == nil {
					continue
				}
				candidate := jsonTag(field.Tag.Value)
				if candidate == "" || candidate == "-" {
					continue
				}
				if jsonField != "" {
					return "", fmt.Errorf("%s: proto.Error has multiple JSON fields", path)
				}
				jsonField = candidate
			}
			if jsonField == "" {
				return "", fmt.Errorf("%s: proto.Error has no JSON error field", path)
			}
			return jsonField, nil
		}
	}
	return "", fmt.Errorf("%s: proto.Error message JSON field not found", path)
}

func extractErrorMappings(m module) (map[string]string, error) {
	out := map[string]string{}
	fset := token.NewFileSet()
	server, err := parser.ParseFile(fset, filepath.Join(m.Dir, "internal", "server", "proto.go"), nil, 0)
	if err != nil {
		return nil, err
	}
	ast.Inspect(server, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		left, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || left.Name != "status" {
			return true
		}
		sel, ok := assign.Rhs[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "StatusNotFound":
			out["ErrorMappingNotFound"] = "404"
		case "StatusConflict":
			out["ErrorMappingBusy"] = "409"
		case "StatusServiceUnavailable":
			out["ErrorMappingShuttingDown"] = "503"
		case "StatusBadRequest":
			out["ErrorMappingUnsupported"] = "400"
		}
		return true
	})
	client, err := parser.ParseFile(fset, filepath.Join(m.Dir, "internal", "client", "client.go"), nil, 0)
	if err != nil {
		return nil, err
	}
	ast.Inspect(client, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			sel, ok := expr.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if sel.Sel.Name == "StatusConflict" {
				out["ErrorMappingBusy"] = "409 ShutdownServerIfIdle busy"
			}
			if sel.Sel.Name == "StatusBadRequest" {
				out["ErrorMappingUnsupported"] = "400 unknown/unsupported control command"
			}
		}
		return true
	})
	required := []string{"ErrorMappingNotFound", "ErrorMappingBusy", "ErrorMappingShuttingDown", "ErrorMappingUnsupported"}
	for _, name := range required {
		if out[name] == "" {
			return nil, fmt.Errorf("source error mapping %s not found", name)
		}
	}
	return out, nil
}

func literalString(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconvUnquote(lit.Value)
	return v, err == nil
}

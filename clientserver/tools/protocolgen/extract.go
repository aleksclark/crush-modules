package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

type headerParam struct {
	Name   string
	Values []string
}

func classifyClientFunc(fn *ast.FuncDecl) (*ast.CallExpr, string, error) {
	if fn == nil || fn.Body == nil {
		return nil, "", fmt.Errorf("client method has no body")
	}
	calls := httpTransportCalls(fn)
	switch len(calls) {
	case 1:
		return calls[0], "http", nil
	case 0:
		if positivelyNonHTTP(fn) {
			return nil, "non_http", nil
		}
		return nil, "", fmt.Errorf("%s: unsupported exported client method: no HTTP transport call", fn.Name.Name)
	default:
		return nil, "", fmt.Errorf("%s: multiple HTTP transport calls", fn.Name.Name)
	}
}

func httpTransportCalls(fn *ast.FuncDecl) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "get", "post", "put", "delete":
			calls = append(calls, call)
		}
		return true
	})
	return calls
}

func positivelyNonHTTP(fn *ast.FuncDecl) bool {
	if trivialAccessor(fn) {
		return true
	}
	if fn.Type != nil && fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			if namedTypeEndsWith(field.Type, "Conn") {
				return true
			}
		}
	}
	return false
}

func trivialAccessor(fn *ast.FuncDecl) bool {
	for _, stmt := range fn.Body.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			return false
		}
		for _, expr := range ret.Results {
			if callExpr(expr) != nil {
				return false
			}
		}
	}
	return len(fn.Body.List) > 0
}

func callExpr(expr ast.Expr) *ast.CallExpr {
	call, _ := expr.(*ast.CallExpr)
	return call
}

func namedTypeEndsWith(expr ast.Expr, suffix string) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == suffix
	case *ast.SelectorExpr:
		return t.Sel.Name == suffix
	case *ast.StarExpr:
		return namedTypeEndsWith(t.X, suffix)
	}
	return false
}

func extractQueryParams(fn *ast.FuncDecl, call *ast.CallExpr) ([]string, error) {
	if call == nil || len(call.Args) < 3 {
		return nil, nil
	}
	arg := call.Args[2]
	if ident, ok := arg.(*ast.Ident); ok && ident.Name == "nil" {
		return nil, nil
	}
	values, err := queryComposite(fn, arg)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, elt := range values.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, fmt.Errorf("%s: unsupported query form", fn.Name.Name)
		}
		key, ok := kv.Key.(*ast.BasicLit)
		if !ok || key.Kind != token.STRING {
			return nil, fmt.Errorf("%s: unsupported query form", fn.Name.Name)
		}
		name, err := strconv.Unquote(key.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: unsupported query form: %w", fn.Name.Name, err)
		}
		out = append(out, name)
	}
	return out, nil
}

func queryComposite(fn *ast.FuncDecl, expr ast.Expr) (*ast.CompositeLit, error) {
	if lit, ok := expr.(*ast.CompositeLit); ok {
		return lit, nil
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return nil, fmt.Errorf("%s: unsupported query form", fn.Name.Name)
	}
	var found *ast.CompositeLit
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, left := range assign.Lhs {
			name, ok := left.(*ast.Ident)
			if !ok || name.Name != ident.Name || i >= len(assign.Rhs) {
				continue
			}
			if lit, ok := assign.Rhs[i].(*ast.CompositeLit); ok {
				found = lit
			}
		}
		return true
	})
	if found == nil {
		return nil, fmt.Errorf("%s: unsupported query form", fn.Name.Name)
	}
	return found, nil
}

func extractHeaderParams(fn *ast.FuncDecl, call *ast.CallExpr) ([]headerParam, error) {
	if call == nil {
		return nil, nil
	}
	var arg ast.Expr
	switch call.Fun.(*ast.SelectorExpr).Sel.Name {
	case "get":
		if len(call.Args) < 4 {
			return nil, nil
		}
		arg = call.Args[3]
	case "delete":
		if len(call.Args) < 4 {
			return nil, nil
		}
		arg = call.Args[3]
	case "post", "put":
		if len(call.Args) < 5 {
			return nil, nil
		}
		arg = call.Args[4]
	}
	if arg == nil {
		return nil, nil
	}
	if ident, ok := arg.(*ast.Ident); ok && ident.Name == "nil" {
		return nil, nil
	}
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("%s: unsupported header form", fn.Name.Name)
	}
	var out []headerParam
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, fmt.Errorf("%s: unsupported header form", fn.Name.Name)
		}
		name, err := headerKey(kv.Key)
		if err != nil {
			return nil, fmt.Errorf("%s: unsupported header form: %w", fn.Name.Name, err)
		}
		values, err := headerValues(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: unsupported header form: %w", fn.Name.Name, err)
		}
		out = append(out, headerParam{Name: name, Values: values})
	}
	return out, nil
}

func headerKey(expr ast.Expr) (string, error) {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return strconv.Unquote(lit.Value)
	}
	return "", fmt.Errorf("nonliteral header key")
}

func headerValues(expr ast.Expr) ([]string, error) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("nonliteral header values")
	}
	var out []string
	for _, elt := range lit.Elts {
		item, ok := elt.(*ast.BasicLit)
		if !ok || item.Kind != token.STRING {
			return nil, fmt.Errorf("nonliteral header value")
		}
		value, err := strconv.Unquote(item.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func extractBody(fn *ast.FuncDecl, call *ast.CallExpr, pkg *packages.Package) (types.Type, bool, error) {
	if call == nil {
		return nil, false, nil
	}
	sel := call.Fun.(*ast.SelectorExpr).Sel.Name
	if sel != "post" && sel != "put" {
		return nil, false, nil
	}
	if len(call.Args) < 4 {
		return nil, false, nil
	}
	candidate := call.Args[3]
	if ident, ok := candidate.(*ast.Ident); ok && ident.Name == "nil" {
		return nil, false, nil
	}
	body, err := resolveJSONBodyCall(fn, candidate, pkg, map[types.Object]bool{})
	if err != nil {
		return nil, false, err
	}
	t := pkg.TypesInfo.TypeOf(body.Args[0])
	if t == nil {
		return nil, false, bodyExtractionError(pkg, fn, body.Args[0], "unresolved jsonBody payload type")
	}
	return t, true, nil
}

// resolveJSONBodyCall follows a transport body expression through a single local
// declaration or assignment chain until it reaches jsonBody(payload). Ambiguous
// chains are rejected rather than guessing a wire type.
func resolveJSONBodyCall(fn *ast.FuncDecl, expr ast.Expr, pkg *packages.Package, seen map[types.Object]bool) (*ast.CallExpr, error) {
	if call, ok := expr.(*ast.CallExpr); ok {
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "jsonBody" || len(call.Args) != 1 {
			return nil, bodyExtractionError(pkg, fn, expr, "expected jsonBody(payload)")
		}
		return call, nil
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return nil, bodyExtractionError(pkg, fn, expr, "expected jsonBody(payload) or a local variable assigned from it")
	}
	object := pkg.TypesInfo.ObjectOf(ident)
	if object == nil {
		return nil, bodyExtractionError(pkg, fn, ident, "unresolved local body variable")
	}
	if seen[object] {
		return nil, bodyExtractionError(pkg, fn, ident, "cyclic local body assignment")
	}
	seen[object] = true
	assignments := localAssignments(fn, pkg, object)
	if len(assignments) != 1 {
		return nil, bodyExtractionError(pkg, fn, ident, fmt.Sprintf("local body variable has %d assignments; require exactly one", len(assignments)))
	}
	return resolveJSONBodyCall(fn, assignments[0], pkg, seen)
}

func localAssignments(fn *ast.FuncDecl, pkg *packages.Package, object types.Object) []ast.Expr {
	var assignments []ast.Expr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			for index, left := range statement.Lhs {
				if index < len(statement.Rhs) && sameLocalObject(pkg, left, object) {
					assignments = append(assignments, statement.Rhs[index])
				}
			}
		case *ast.ValueSpec:
			for index, name := range statement.Names {
				if index < len(statement.Values) && sameLocalObject(pkg, name, object) {
					assignments = append(assignments, statement.Values[index])
				}
			}
		}
		return true
	})
	return assignments
}

func sameLocalObject(pkg *packages.Package, expr ast.Expr, object types.Object) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && pkg.TypesInfo.ObjectOf(ident) == object
}

func bodyExtractionError(pkg *packages.Package, fn *ast.FuncDecl, expr ast.Expr, detail string) error {
	return fmt.Errorf("%s: unsupported body extraction at %s: %s", fn.Name.Name, pkg.Fset.Position(expr.Pos()), detail)
}

func extractDecoder(pkg *packages.Package, fn *ast.FuncDecl, required bool) (types.Type, bool, error) {
	var target ast.Expr
	var decoders int
	var unrecognized bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "Decode" {
			if !isJSONDecoder(sel.X) {
				unrecognized = true
				return true
			}
			if len(call.Args) == 1 {
				if unary, ok := call.Args[0].(*ast.UnaryExpr); ok && unary.Op == token.AND {
					target = unary.X
					decoders++
				} else {
					unrecognized = true
				}
			}
		}
		return true
	})
	if unrecognized {
		return nil, false, fmt.Errorf("%s: unsupported decoder form", fn.Name.Name)
	}
	if target == nil {
		if required {
			return nil, false, fmt.Errorf("%s: missing decoder", fn.Name.Name)
		}
		return nil, false, nil
	}
	t := pkg.TypesInfo.TypeOf(target)
	if t == nil {
		return nil, false, fmt.Errorf("%s: unsupported decoder: unresolved Decode target", fn.Name.Name)
	}
	return t, true, nil
}

func isJSONDecoder(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "NewDecoder" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "json"
}

func extractStatusSemantics(pkg *packages.Package, fn *ast.FuncDecl) ([]int, string, error) {
	statuses := map[int]bool{}
	guarded := false
	var walkErr error
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if walkErr != nil {
			return false
		}
		switch n := node.(type) {
		case *ast.CallExpr:
			ident, ok := n.Fun.(*ast.Ident)
			if !ok || ident.Name != "checkStatus" {
				return true
			}
			guarded = true
			if len(n.Args) == 1 {
				statuses[200] = true
			}
			for _, arg := range n.Args[1:] {
				status, err := resolveHTTPStatus(pkg, arg)
				if err != nil {
					walkErr = fmt.Errorf("%s: unsupported status form: %w", fn.Name.Name, err)
					return false
				}
				statuses[status] = true
			}
		case *ast.BinaryExpr:
			if n.Op != token.EQL && n.Op != token.NEQ {
				return true
			}
			var statusExpr ast.Expr
			if isResponseStatusCode(n.X) {
				statusExpr = n.Y
			} else if isResponseStatusCode(n.Y) {
				statusExpr = n.X
			} else {
				return true
			}
			guarded = true
			status, err := resolveHTTPStatus(pkg, statusExpr)
			if err != nil {
				walkErr = fmt.Errorf("%s: unsupported status form: %w", fn.Name.Name, err)
				return false
			}
			statuses[status] = true
		}
		return true
	})
	if walkErr != nil {
		return nil, "", walkErr
	}
	if !guarded {
		return nil, "source_unspecified", nil
	}
	values := make([]int, 0, len(statuses))
	for value := range statuses {
		values = append(values, value)
	}
	return sortedInts(values), "guarded", nil
}

func resolveHTTPStatus(pkg *packages.Package, expr ast.Expr) (int, error) {
	if pkg != nil && pkg.TypesInfo != nil {
		switch obj := pkg.TypesInfo.Uses[selectorName(expr)].(type) {
		case *types.Const:
			if n, ok := constantInt(obj); ok {
				return n, nil
			}
		}
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return 0, fmt.Errorf("non-selector status")
	}
	if !strings.HasPrefix(sel.Sel.Name, "Status") {
		return 0, fmt.Errorf("unsupported status %s", sel.Sel.Name)
	}
	return 0, fmt.Errorf("unresolved status %s", sel.Sel.Name)
}

func constantInt(obj *types.Const) (int, bool) {
	if obj == nil || obj.Val() == nil {
		return 0, false
	}
	n, err := strconv.Atoi(obj.Val().ExactString())
	return n, err == nil
}

func sortedInts(values []int) []int {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
	return values
}

func extractClientDiscriminator(fn *ast.FuncDecl, pkg *packages.Package) (field, value string, err error) {
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Command" {
				continue
			}
			cons, ok := pkg.TypesInfo.Uses[selectorName(kv.Value)].(*types.Const)
			if !ok {
				err = fmt.Errorf("%s: unsupported control discriminator", fn.Name.Name)
				return false
			}
			field = "Command"
			value, err = strconv.Unquote(cons.Val().ExactString())
			if err != nil {
				value = strings.Trim(cons.Val().ExactString(), `"`)
				err = nil
			}
			return false
		}
		return true
	})
	return field, value, err
}

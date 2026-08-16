package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestBuildWirePlanCollectsGenericNamedOrigins(t *testing.T) {
	m := resolvedModule()
	ops, _, err := clientOperations(m)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildWirePlan(m, ops)
	if err != nil {
		t.Fatal(err)
	}
	event := plan.packageType(m.Path+"/internal/pubsub", "Event")
	if event == nil {
		t.Fatal("missing pubsub.Event from package index")
	}
	if _, ok := plan.names[event]; !ok {
		t.Fatal("generic pubsub.Event origin was not collected")
	}
}

func TestClientOperationsUseJSONDecoderTargetAsWireResult(t *testing.T) {
	m := resolvedModule()
	ops, _, err := clientOperations(m)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"GetMCPPrompt":               `struct{Prompt string "json:\"prompt\""}`,
		"GetInitializePrompt":        `struct{Prompt string "json:\"prompt\""}`,
		"ProjectNeedsInitialization": `struct{NeedsInit bool "json:\"needs_init\""}`,
		"MCPAuthURL":                 "github.com/charmbracelet/crush/internal/proto.MCPAuthResponse",
		"ImportCopilot":              `struct{Token *github.com/charmbracelet/crush/internal/oauth.Token "json:\"token\""; Success bool "json:\"success\""}`,
		"GrantPermission":            "github.com/charmbracelet/crush/internal/proto.PermissionGrantResponse",
		"AnswerQuestionBatch":        "github.com/charmbracelet/crush/internal/proto.QuestionAnswerResponse",
		"CancelQuestionBatch":        "github.com/charmbracelet/crush/internal/proto.QuestionAnswerResponse",
	}
	got := map[string]string{}
	for _, op := range ops {
		if _, ok := want[op.Name]; ok {
			got[op.Name] = op.ResultWire.String()
		}
	}
	for name, expected := range want {
		if got[name] != expected {
			t.Errorf("%s wire result = %q, want JSON decoder target %q", name, got[name], expected)
		}
	}
}

func TestClientOperationsResolveLocalJSONBodyVariable(t *testing.T) {
	m := resolvedModule()
	ops, _, err := clientOperations(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Name != "InitiateAgentProcessing" {
			continue
		}
		if !op.HasBody {
			t.Fatal("InitiateAgentProcessing local jsonBody variable was not extracted as a request body")
		}
		if got, want := op.BodyWire.String(), "github.com/charmbracelet/crush/internal/proto.AgentInitRequest"; got != want {
			t.Fatalf("InitiateAgentProcessing body = %q, want %q", got, want)
		}
		return
	}
	t.Fatal("InitiateAgentProcessing operation was not collected")
}

func TestEveryNonNilTransportBodyHasGeneratedRequestBody(t *testing.T) {
	m := resolvedModule()
	ops, _, err := clientOperations(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ops); got != 73 {
		t.Fatalf("client operations = %d, want exact exported source set of 73", got)
	}
	byName := make(map[string]operation, len(ops))
	for _, op := range ops {
		byName[op.Name] = op
	}
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo, Dir: m.Dir}
	loaded, err := packages.Load(cfg, "./internal/client")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(loaded) > 0 || len(loaded) != 1 {
		t.Fatal("load client package: type errors")
	}
	var httpOperations, mismatches int
	for _, file := range loaded[0].Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !ast.IsExported(fn.Name.Name) {
				continue
			}
			call, classification, err := classifyClientFunc(fn)
			if err != nil {
				t.Fatal(err)
			}
			if classification != "http" {
				continue
			}
			httpOperations++
			if !hasNonNilTransportBody(call) {
				continue
			}
			op, ok := byName[fn.Name.Name]
			if !ok || !op.HasBody {
				t.Errorf("%s at %s has a non-nil transport body but generated HasBody=%v", fn.Name.Name, loaded[0].Fset.Position(call.Pos()), ok && op.HasBody)
				mismatches++
			}
		}
	}
	if httpOperations != 70 {
		t.Fatalf("HTTP operations = %d, want exact client source set of 70", httpOperations)
	}
	if mismatches != 0 {
		t.Fatalf("non-nil transport body / HasBody mismatches = %d, want 0", mismatches)
	}
}

func hasNonNilTransportBody(call *ast.CallExpr) bool {
	if call == nil || len(call.Args) < 4 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "post" && selector.Sel.Name != "put") {
		return false
	}
	ident, isIdent := call.Args[3].(*ast.Ident)
	return !isIdent || ident.Name != "nil"
}

func TestExtractBodyRejectsAmbiguousLocalAssignmentsWithSourceLocation(t *testing.T) {
	fn, call, pkg := typedTestTransport(t, `
package fixture
type AgentInitRequest struct { Interactive bool }
type Client struct{}
func jsonBody(value any) any { return value }
func (Client) post(any, string, any, any, any) {}
func (c Client) Initiate() {
	body := jsonBody(AgentInitRequest{Interactive: true})
	body = jsonBody(AgentInitRequest{Interactive: false})
	c.post(nil, "/init", nil, body, nil)
}`)
	_, _, err := extractBody(fn, call, pkg)
	if err == nil {
		t.Fatal("ambiguous local body assignments were accepted")
	}
	if !strings.Contains(err.Error(), "source.go:") || !strings.Contains(err.Error(), "has 2 assignments") {
		t.Fatalf("error = %q, want source location and explicit ambiguity", err)
	}
}

func typedTestTransport(t *testing.T, source string) (*ast.FuncDecl, *ast.CallExpr, *packages.Package) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "source.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}}
	pkgTypes, err := (&types.Config{}).Check("fixture", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	var call *ast.CallExpr
	for _, decl := range file.Decls {
		candidate, ok := decl.(*ast.FuncDecl)
		if !ok || candidate.Name.Name != "Initiate" {
			continue
		}
		fn = candidate
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			candidate, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			if selector, ok := candidate.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "post" {
				call = candidate
			}
			return true
		})
	}
	if fn == nil || call == nil {
		t.Fatal("fixture transport call was not found")
	}
	return fn, call, &packages.Package{Fset: fset, Types: pkgTypes, TypesInfo: info}
}

func TestServerRoutesRejectNonliteralProductionRegistration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.go")
	source := `package server
import "net/http"
func (s *Server) installHandler() {
  mux := http.NewServeMux()
  pattern := "GET /v1/health"
  mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
}
type Server struct{}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := serverRoutes(path); err == nil {
		t.Fatal("nonliteral registration was silently skipped")
	}
}

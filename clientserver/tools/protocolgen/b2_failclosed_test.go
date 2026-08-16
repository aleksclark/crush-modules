package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func parseFunc(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", "package client\n"+src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			return fn
		}
	}
	t.Fatal("fixture has no function")
	return nil
}

func TestTransportCallErrorsOnMultipleUnclassifiedCalls(t *testing.T) {
	fn := parseFunc(t, `
func (c *Client) Ambiguous(ctx context.Context) {
	c.get(ctx, "/workspaces", nil, nil)
	c.post(ctx, "/control", nil, nil, nil)
}
`)
	if transportCall(fn) != nil {
		t.Fatal("multiple transport calls were silently classified as the first call")
	}
}

func TestTransportCallErrorsOnZeroUnclassifiedCalls(t *testing.T) {
	fn := parseFunc(t, `
func (c *Client) Mystery(ctx context.Context) error {
	return c.other(ctx)
}
`)
	if transportCall(fn) != nil {
		t.Fatal("unrelated call was treated as a transport")
	}
}

func TestClientOperationsFailClosedOnUnknownExportedMethod(t *testing.T) {
	dir := t.TempDir()
	src := `package client
import (
	"context"
	"net/http"
	"net/url"
)
type Client struct{}
func (c *Client) Mystery(ctx context.Context) error { return c.other(ctx) }
func (c *Client) other(ctx context.Context) error { return nil }
func (c *Client) get(ctx context.Context, path string, query url.Values, headers http.Header) (*http.Response, error) {
	return nil, nil
}
`
	if err := os.WriteFile(filepath.Join(dir, "client.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := clientMethods(dir)
	if err == nil {
		// clientMethods currently classifies Mystery as non_http_or_dynamic without fail-closed.
		methods, methodsErr := clientMethods(dir)
		if methodsErr != nil {
			t.Fatal(methodsErr)
		}
		for _, method := range methods {
			if method.Name == "Mystery" && method.Classification == "non_http_or_dynamic" {
				t.Fatal("unknown exported method was silently classified instead of a location error")
			}
		}
	}
}

func TestUnsupportedQueryHeaderDecoderAreLocationErrors(t *testing.T) {
	fn := parseFunc(t, `
func (c *Client) WeirdQuery(ctx context.Context) error {
	_, err := c.get(ctx, "/health", buildQuery(), nil)
	return err
}
`)
	params := queryParams(fn, transportCall(fn))
	if params != nil {
		t.Fatalf("unsupported query form produced params %v instead of failing closed", params)
	}
}

func TestProvidersRouteExtractedAsTypedServerOnly(t *testing.T) {
	m := resolvedModule()
	routes, err := serverRoutes(m.Dir)
	if err != nil {
		t.Fatal(err)
	}
	ops, _, err := clientOperations(m)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, route := range routes {
		if route.Path != "/v1/workspaces/{id}/providers" {
			continue
		}
		found = true
		if routeClassification(route, ops) != "typed_server_only" {
			t.Fatalf("providers classification = %q, want typed_server_only", routeClassification(route, ops))
		}
	}
	if !found {
		t.Fatal("providers route missing")
	}
}

func TestLiteralStringDecodesGoEscapesForWireFrames(t *testing.T) {
	got, ok := literalString(&ast.BasicLit{Kind: token.STRING, Value: `"data: %s\n\n"`})
	if !ok {
		t.Fatal("literal did not decode")
	}
	if got != "data: %s\n\n" {
		t.Fatalf("decoded wire frame = %q", got)
	}
}

package clientserver_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFreshGeneratedProtocolCompiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "-C", "tools/protocolgen", "run", ".", "-out", dir)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate protocol: %v\n%s", err, out)
	}
	cmd = exec.Command("go", "test", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fresh generated protocol does not compile: %v\n%s", err, out)
	}
}

func TestGeneratorWritesDeterministicPublicProtocol(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd := exec.Command("go", "-C", "tools/protocolgen", "run", ".", "-out", dir)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate protocol: %v\n%s", err, out)
	}
	first, err := os.ReadFile(filepath.Join(dir, "protocol_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("go", "-C", "tools/protocolgen", "run", ".", "-out", dir)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerate protocol: %v\n%s", err, out)
	}
	second, err := os.ReadFile(filepath.Join(dir, "protocol_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("generator output is not byte-identical")
	}
	if bytes.Contains(first, []byte("\"github.com/charmbracelet/crush/internal/")) {
		t.Fatal("public generated source imports an internal package")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "protocol_gen.go", first, 0)
	if err != nil {
		t.Fatalf("parse generated source: %v", err)
	}
	assertUniqueStructFields(t, parsed)
	assertSaveSessionPathFields(t, parsed)
}

func assertUniqueStructFields(t *testing.T, file *ast.File) {
	t.Helper()
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			seen := map[string]bool{}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if seen[name.Name] {
						t.Fatalf("generated %s repeats field %s", ts.Name.Name, name.Name)
					}
					seen[name.Name] = true
				}
			}
		}
	}
}

func assertSaveSessionPathFields(t *testing.T, file *ast.File) {
	t.Helper()
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "OperationSaveSessionRequest" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				t.Fatal("OperationSaveSessionRequest is not a struct")
			}
			var names []string
			for _, field := range st.Fields.List {
				if field.Tag != nil && bytes.Contains([]byte(field.Tag.Value), []byte("path:")) {
					names = append(names, field.Names[0].Name)
				}
			}
			if len(names) != 2 || names[0] == names[1] {
				t.Fatalf("SaveSession path fields = %v, want two distinct identities", names)
			}
			return
		}
	}
	t.Fatal("OperationSaveSessionRequest not generated")
}

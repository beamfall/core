package plugin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// TestDormantLifecycleContractIsAbsent prevents public deactivate/reactivate/
// purge promises from returning as types-only surface. A future lifecycle must
// replace this assertion with an end-to-end persistence/query/index/purge test.
func TestDormantLifecycleContractIsAbsent(t *testing.T) {
	path := filepath.Join("lifecycle.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && ast.IsExported(function.Name.Name) {
			t.Fatalf("dormant lifecycle surface exported %s without production consumers", function.Name.Name)
		}
		gen, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if ok && ast.IsExported(typeSpec.Name.Name) {
				t.Fatalf("dormant lifecycle surface exported %s without production consumers", typeSpec.Name.Name)
			}
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range valueSpec.Names {
				if ast.IsExported(name.Name) {
					t.Fatalf("dormant lifecycle surface exported %s without production consumers", name.Name)
				}
			}
		}
	}
}

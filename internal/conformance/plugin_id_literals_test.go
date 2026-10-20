package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCoreHasNoFirstPartyPluginIDStringLiterals(t *testing.T) {
	forbidden := map[string]bool{
		"filmtv":        true,
		"music":         true,
		"podcasts":      true,
		"photos":        true,
		"audiobook":     true,
		"familyarchive": true,
		"enrichment":    true,
	}
	roots := []string{"cmd", "internal"}
	scanned := 0
	for _, root := range roots {
		err := filepath.WalkDir(requireRoot(t, root), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel := vocabRelPath(t, path)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			scanned++
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil || !forbidden[value] || allowedPluginIDLiteral(rel, value) {
					return true
				}
				pos := fset.Position(lit.Pos())
				t.Fatalf("%s:%d contains first-party plugin ID literal %q", rel, pos.Line, value)
				return false
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned == 0 {
		t.Fatal("plugin ID literal gate scanned zero files")
	}
}

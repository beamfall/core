package conformance

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageImportsStayInsideStore(t *testing.T) {
	root := repoRoot(t)
	scanned := 0
	for _, scanRoot := range []string{"cmd", "internal"} {
		if err := filepath.WalkDir(requireRoot(t, scanRoot), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			scanned++
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			for _, imported := range file.Imports {
				value := strings.Trim(imported.Path.Value, `"`)
				if value == "database/sql" || value == "modernc.org/sqlite" {
					if !strings.HasPrefix(relative, "internal/store/") {
						t.Fatalf("%s imports %s outside internal/store", relative, value)
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if scanned == 0 {
		t.Fatal("storage import gate scanned zero files")
	}
}

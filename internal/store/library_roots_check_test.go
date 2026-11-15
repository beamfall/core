package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLibraryRoots(t *testing.T) {
	dir := t.TempDir()
	if err := CheckLibraryRoots([]string{dir, "  " + dir + "  "}); err != nil {
		t.Fatalf("readable folder refused: %v", err)
	}

	missing := filepath.Join(dir, "missing")
	file := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		root string
		want string
	}{
		{"missing", missing, "The folder " + missing + " doesn't exist on the server."},
		{"file", file, file + " is a file, not a folder."},
		{"blank", "  ", "library root is required"},
	}
	if os.Geteuid() != 0 {
		unreadable := filepath.Join(dir, "locked")
		if err := os.Mkdir(unreadable, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(unreadable, 0o700) }) // #nosec G302 -- a directory needs its execute bit so the temp dir can be removed
		cases = append(cases, struct {
			name string
			root string
			want string
		}{"unreadable", unreadable, "The server doesn't have permission to read " + unreadable + "."})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckLibraryRoots([]string{dir, tc.root})
			var invalid ValidationError
			if !errors.As(err, &invalid) || invalid.Error() != tc.want {
				t.Fatalf("CheckLibraryRoots(%q) = %v, want %q", tc.root, err, tc.want)
			}
			if strings.Contains(err.Error(), "scanLibrary") {
				t.Fatalf("refusal leaks a field name: %v", err)
			}
		})
	}
}

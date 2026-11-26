package jsonstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSaveRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "state.json")

	var empty []string
	found, err := Load(path, &empty)
	if err != nil || found {
		t.Fatalf("Load(missing) = (%v, %v), want (false, nil)", found, err)
	}

	want := []string{"a", "b"}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 0600", info.Mode().Perm())
	}

	var got []string
	found, err = Load(path, &got)
	if err != nil || !found {
		t.Fatalf("Load(saved) = (%v, %v), want (true, nil)", found, err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got = %v, want %v", got, want)
	}

	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt: %v", err)
	}
	var corrupt []string
	found, err = Load(path, &corrupt)
	if err == nil || !found {
		t.Fatalf("Load(corrupt) = (%v, %v), want (true, non-nil)", found, err)
	}
}

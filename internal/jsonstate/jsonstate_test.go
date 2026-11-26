package jsonstate

import (
	"errors"
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

func TestSaveDistinguishesPreRenameAndPostRenameSyncFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Save(path, []string{"old"}); err != nil {
		t.Fatal(err)
	}

	// Pre-rename failure (path itself is a directory so os.Rename fails).
	dirTarget := filepath.Join(dir, "as-dir")
	if err := os.Mkdir(dirTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	err := Save(dirTarget, []string{"new"})
	var postSync *PostRenameSyncError
	if err == nil || errors.As(err, &postSync) {
		t.Fatalf("pre-rename Save err = %v, must not be PostRenameSyncError", err)
	}

	// Post-rename failure leaves new file installed and returns PostRenameSyncError.
	injected := errors.New("dir fsync EIO")
	defer WithSyncDirForTest(func(string) error { return injected })()

	err = Save(path, []string{"new"})
	if !errors.As(err, &postSync) || !errors.Is(err, injected) {
		t.Fatalf("post-rename Save err = %v, want *PostRenameSyncError wrapping injected", err)
	}
	var loaded []string
	found, loadErr := Load(path, &loaded)
	if loadErr != nil || !found || len(loaded) != 1 || loaded[0] != "new" {
		t.Fatalf("file after post-rename sync failure = (%+v, %v, %v), want [new]", loaded, found, loadErr)
	}
}

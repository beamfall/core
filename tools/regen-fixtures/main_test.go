package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixturePath(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "probe.json")
	if err := os.WriteFile(fixture, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fixturePath(root, "probe.json"); err != nil || got != want {
		t.Fatalf("fixturePath(valid) = %q, %v", got, err)
	}
	for _, name := range []string{"", "../probe.json", "nested/probe.json", "/tmp/probe.json"} {
		if _, err := fixturePath(root, name); err == nil {
			t.Errorf("fixturePath(%q) accepted unsafe path", name)
		}
	}

	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := fixturePath(root, "linked.json"); err == nil {
		t.Fatal("fixturePath accepted a basename symlink outside the fixture root")
	}
}

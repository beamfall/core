package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyExclusiveNeverOverwritesAClaimant(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "stage"), filepath.Join(dir, "Track.mp3")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("claimant"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyExclusive(src, dst); !os.IsExist(err) {
		t.Fatalf("copy over a claimant = %v, want exist error", err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "claimant" { // #nosec G304 -- test temp dir
		t.Fatalf("claimant = %q, %v", data, err)
	}
	fresh := filepath.Join(dir, "Track-2.mp3")
	if err := copyExclusive(src, fresh); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(fresh); err != nil || string(data) != "payload" { // #nosec G304 -- test temp dir
		t.Fatalf("copy = %q, %v", data, err)
	}
}

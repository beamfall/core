package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBundledCredentialsFallBackOnlyWithoutOwnerSecret: a bundled default
// answers an AuthRef the owner never saved; the owner's secret overrides it,
// clearing that secret restores the default, and a broken owner secret stays
// an error rather than being masked (CORE-1129).
func TestBundledCredentialsFallBackOnlyWithoutOwnerSecret(t *testing.T) {
	dir := t.TempDir()
	creds := withBundledCredentials(secretFiles{dir: dir}, "project-key=bundled-value")

	if got, err := creds.Secret("project-key"); err != nil || got != "bundled-value" {
		t.Fatalf("Secret with no owner file = %q, %v; want bundled-value", got, err)
	}
	if _, err := creds.Secret("other-key"); err == nil {
		t.Fatal("Secret for an unbundled, unsaved ref succeeded; want an error")
	}

	if err := creds.SetSecret("project-key", "owner-value"); err != nil {
		t.Fatal(err)
	}
	if got, err := creds.Secret("project-key"); err != nil || got != "owner-value" {
		t.Fatalf("Secret with owner file = %q, %v; want owner-value", got, err)
	}

	if err := creds.SetSecret("project-key", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := creds.Secret("project-key"); err != nil || got != "bundled-value" {
		t.Fatalf("Secret after clearing = %q, %v; want bundled-value", got, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "project-key"), []byte("loose"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := creds.Secret("project-key"); err == nil {
		t.Fatal("Secret with a group-readable owner file fell back to the bundled value; want an error")
	}
}

// TestParseBundledCredentials keeps well-formed pairs and drops blank,
// malformed and path-like entries.
func TestParseBundledCredentials(t *testing.T) {
	got := parseBundledCredentials(" a = one ,b=two=2,,noequals,c=,=x,../d=no,e/f=no,.g=no")
	want := map[string]string{"a": "one", "b": "two=2"}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for ref, value := range want {
		if got[ref] != value {
			t.Errorf("parsed[%q] = %q, want %q", ref, got[ref], value)
		}
	}
	if len(parseBundledCredentials("")) != 0 {
		t.Error("empty BundledCredentials parsed to a non-empty map")
	}
}

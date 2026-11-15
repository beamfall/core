package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
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

	if err := os.WriteFile(filepath.Join(dir, "project-key"), []byte("loose"), 0o644); err != nil { //nolint:gosec // G306: the test needs a deliberately loose key file.
		t.Fatal(err)
	}
	if _, err := creds.Secret("project-key"); err == nil {
		t.Fatal("Secret with a group-readable owner file fell back to the bundled value; want an error")
	}
}

// TestDisablingBundledCredentialStopsTheScraper (CORE-1540): disabling an
// AuthRef that has a bundled default stops the scraper instead of falling
// back to the built-in key, survives a restart, and is lifted only by saving
// a new owner key. Disabling an unbundled ref just clears the owner's secret.
func TestDisablingBundledCredentialStopsTheScraper(t *testing.T) {
	var _ graph.ScraperCredentialDisabler = bundledSecrets{}

	var requests atomic.Int32
	var lastKey atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		lastKey.Store(r.URL.Query().Get("key"))
		fmt.Fprint(w, `{"name": "Sample"}`)
	}))
	defer server.Close()
	spec := plugin.ScraperSpec{
		ID: "keyed", Kind: "http-json", Endpoint: server.URL,
		LookupTemplate: "/{id}", AuthRef: "project-key", AuthStyle: "query:key",
		FieldMap: map[string]string{"title": "name"},
	}
	dir := t.TempDir()
	creds := withBundledCredentials(secretFiles{dir: dir}, "project-key=bundled-value")
	lookup := func() error {
		_, err := scrape.New(scrape.WithInsecureLoopback(), scrape.WithCredentials(creds)).Lookup(context.Background(), spec, "1")
		return err
	}

	if err := lookup(); err != nil || lastKey.Load() != "bundled-value" {
		t.Fatalf("lookup with bundled key = %v, key %v; want bundled-value", err, lastKey.Load())
	}
	if err := creds.SetSecret("project-key", "owner-value"); err != nil {
		t.Fatal(err)
	}
	if err := creds.DisableSecret("project-key"); err != nil {
		t.Fatal(err)
	}
	if !creds.SecretDisabled("project-key") {
		t.Fatal("SecretDisabled = false after DisableSecret")
	}
	if _, err := creds.Secret("project-key"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Secret after disable = %v; want an absent credential", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "project-key")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("owner secret survived disable: %v", err)
	}
	before := requests.Load()
	restarted := withBundledCredentials(secretFiles{dir: dir}, "project-key=bundled-value")
	creds = restarted
	var missing *scrape.CredentialMissingError
	if err := lookup(); !errors.As(err, &missing) {
		t.Fatalf("lookup after disable = %v; want CredentialMissingError", err)
	}
	if got := requests.Load(); got != before {
		t.Fatalf("disabled scraper made %d request(s)", got-before)
	}
	if info, err := os.Lstat(filepath.Join(dir, ".disabled-project-key")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("opt-out marker = %v, %v; want a 0600 file", info, err)
	}

	// Clearing the (absent) owner secret again keeps the opt-out.
	if err := creds.SetSecret("project-key", ""); err != nil {
		t.Fatal(err)
	}
	if !creds.SecretDisabled("project-key") {
		t.Fatal("clearing the owner secret lifted the opt-out")
	}

	// Saving a new owner key lifts the opt-out; clearing it then restores the
	// bundled default as before.
	if err := creds.SetSecret("project-key", "new-owner-value"); err != nil {
		t.Fatal(err)
	}
	if creds.SecretDisabled("project-key") {
		t.Fatal("SecretDisabled = true after saving an owner key")
	}
	if err := lookup(); err != nil || lastKey.Load() != "new-owner-value" {
		t.Fatalf("lookup with new owner key = %v, key %v", err, lastKey.Load())
	}
	if err := creds.SetSecret("project-key", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := creds.Secret("project-key"); err != nil || got != "bundled-value" {
		t.Fatalf("Secret after clearing the new owner key = %q, %v; want bundled-value", got, err)
	}

	// An unbundled ref never gets a marker: disabling only clears the secret.
	if err := creds.SetSecret("other-key", "owner-value"); err != nil {
		t.Fatal(err)
	}
	if err := creds.DisableSecret("other-key"); err != nil {
		t.Fatal(err)
	}
	if creds.SecretDisabled("other-key") {
		t.Fatal("SecretDisabled = true for an unbundled ref")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".disabled-other-key")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unbundled ref got an opt-out marker: %v", err)
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

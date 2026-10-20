package conformance

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/playback"
)

const quirkBaselinePath = "../playback/quirks/baseline.json"

// networkingPackages are the import paths that would let the quirks database
// reach the network.
var networkingPackages = []string{"net", "net/http", "net/url", "crypto/tls", "os/exec"}

// TestPlaybackQuirksAreAirGapped proves ADR-0161 §5's offline requirement on the
// path that actually loads the database: the baseline is embedded in the release
// artifact and parsed in-process, so an air-gapped server gets the full overlay
// with no fetch, no feed, and no default endpoint.
//
// It deliberately does NOT assert a transitive import ban. The overlay's veto
// signatures take store.Media/store.MediaFileStream, so internal/playback
// legitimately imports internal/store, which itself reaches net/url for
// unrelated reasons (sqlite_boot.go) — banning that would fail on a dependency
// this contract has no reason to constrain, and would prove nothing about the
// database. The provable, sufficient claims are the two below: the package that
// parses and resolves the database declares no networking import of its own, and
// the shipped baseline names no endpoint.
func TestPlaybackQuirksAreAirGapped(t *testing.T) {
	root := requireRoot(t, "internal")
	dir := filepath.Join(root, "playback")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		for _, spec := range file.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			for _, banned := range networkingPackages {
				if path == banned {
					t.Fatalf("internal/playback/%s imports %q — the quirks overlay must resolve with no network dependency (ADR-0161 §5)", name, path)
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatal("air-gap gate scanned zero files")
	}

	baseline, err := os.ReadFile(quirkBaselinePath)
	if err != nil {
		t.Fatalf("read %s: %v", quirkBaselinePath, err)
	}
	if _, err := playback.ParseQuirkDB(baseline); err != nil {
		t.Fatalf("the shipped quirks baseline does not load: %v", err)
	}
	for _, marker := range []string{"http://", "https://", "://", "www.", "endpoint", "\"url\"", "feed"} {
		if strings.Contains(strings.ToLower(string(baseline)), marker) {
			t.Fatalf("quirks baseline names %q — the database must carry no fetch target (ADR-0161 §5)", marker)
		}
	}
}

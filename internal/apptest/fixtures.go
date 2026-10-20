package apptest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fixturesSubdir is the repo-relative home of the generated media fixtures.
const fixturesSubdir = "testing/io-fixtures/generated"

// Fixtures accesses the generated media fixtures and their manifest (logical
// name → file + external sha256), produced by `make fixtures`. Accessors fail
// loud (never silent) when fixtures are missing.
type Fixtures struct {
	t testing.TB

	once    sync.Once
	dir     string
	entries map[string]fixtureEntry
}

// NewFixtures returns a standalone Fixtures accessor, for I/O tests that need
// the generated media fixtures without booting a full app (e.g. direct ffmpeg
// *Runner journeys). Harness.Fixtures is the same type, wired by Boot.
func NewFixtures(t testing.TB) *Fixtures {
	t.Helper()
	return &Fixtures{t: t}
}

type fixtureEntry struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type fixtureManifest struct {
	Fixtures []fixtureEntry `json:"fixtures"`
}

// Path returns the absolute path to the fixture file named name.
func (f *Fixtures) Path(name string) string {
	f.t.Helper()
	f.load()
	e, ok := f.entries[name]
	if !ok {
		f.t.Fatalf("apptest: no fixture named %q (run `make fixtures`)", name)
	}
	return filepath.Join(f.dir, e.File)
}

// Digest returns the external (manifest-recorded) sha256 of fixture name.
func (f *Fixtures) Digest(name string) string {
	f.t.Helper()
	f.load()
	e, ok := f.entries[name]
	if !ok {
		f.t.Fatalf("apptest: no fixture named %q (run `make fixtures`)", name)
	}
	return e.SHA256
}

func (f *Fixtures) load() {
	f.t.Helper()
	f.once.Do(func() {
		root := repoRoot(f.t)
		f.dir = filepath.Join(root, fixturesSubdir)
		manifestPath := filepath.Join(f.dir, "manifest.json")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			f.t.Fatalf("apptest: read fixtures manifest %s (run `make fixtures`): %v", manifestPath, err)
		}
		var m fixtureManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			f.t.Fatalf("apptest: decode fixtures manifest %s: %v", manifestPath, err)
		}
		f.entries = make(map[string]fixtureEntry, len(m.Fixtures))
		for _, e := range m.Fixtures {
			f.entries[e.Name] = e
		}
	})
}

// repoRoot walks up from the working directory to the module root (the dir
// holding go.mod).
func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("apptest: getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("apptest: could not locate repo root (go.mod) from working directory")
		}
		dir = parent
	}
}

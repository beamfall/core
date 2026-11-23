package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// offlineConfigDataDir returns a fresh data directory with the store's schema
// created and closed again, as an install script would see it before the
// server's first start.
func offlineConfigDataDir(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func applyOffline(t *testing.T, dataDir, doc string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := applyConfig(context.Background(), []string{"--data-dir", dataDir}, strings.NewReader(doc), &out)
	return out.String(), err
}

func exportOffline(t *testing.T, dataDir string) configDocument {
	t.Helper()
	var out bytes.Buffer
	if err := exportConfig(context.Background(), []string{"--data-dir", dataDir}, &out); err != nil {
		t.Fatal(err)
	}
	doc, err := parseConfigDocument(out.Bytes())
	if err != nil {
		t.Fatalf("export is not a valid document: %v\n%s", err, out.String())
	}
	return doc
}

func liveLibraries(t *testing.T, dataDir string) []store.Library {
	t.Helper()
	st, err := store.Open(context.Background(), dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libs, err := st.Libraries(context.Background(), store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	return libs
}

func libraryDoc(workers string, libraries ...string) string {
	return `{"version":1,"backgroundWorkers":` + workers + `,"plugins":[],"libraries":[` + strings.Join(libraries, ",") + `]}`
}

func libraryEntry(title, plugin string, roots ...string) string {
	encoded, _ := json.Marshal(configLibraryDocItem{Title: title, Plugin: plugin, Roots: append([]string{}, roots...)})
	return string(encoded)
}

func TestApplyConfigOfflineCreatesLibrariesIdempotently(t *testing.T) {
	dataDir := offlineConfigDataDir(t)
	doc := libraryDoc("2", libraryEntry("Music", "music", filepath.Join(t.TempDir(), "music")))

	out, err := applyOffline(t, dataDir, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "applied") {
		t.Fatalf("first apply output = %q, want applied changes", out)
	}
	libs := liveLibraries(t, dataDir)
	if len(libs) != 1 || libs[0].Title != "Music" || libs[0].PluginID == nil || *libs[0].PluginID != "music" {
		t.Fatalf("libraries after apply = %#v, want one Music/music library", libs)
	}

	// Re-applying the same document writes nothing and creates no duplicate.
	out, err = applyOffline(t, dataDir, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already up to date") {
		t.Fatalf("re-apply output = %q, want no-op", out)
	}
	if got := len(liveLibraries(t, dataDir)); got != 1 {
		t.Fatalf("re-apply left %d libraries, want 1", got)
	}
}

func TestApplyConfigOfflineAddsRootsWithoutRemoving(t *testing.T) {
	dataDir := offlineConfigDataDir(t)
	base := t.TempDir()
	first, second := filepath.Join(base, "a"), filepath.Join(base, "b")
	if _, err := applyOffline(t, dataDir, libraryDoc("2", libraryEntry("Films", "filmtv", first))); err != nil {
		t.Fatal(err)
	}
	// A document naming only the second root adds it and keeps the first.
	out, err := applyOffline(t, dataDir, libraryDoc("2", libraryEntry("Films", "filmtv", second)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "applied 1 config change") {
		t.Fatalf("root addition output = %q, want one change", out)
	}
	doc := exportOffline(t, dataDir)
	if len(doc.Libraries) != 1 || !reflect.DeepEqual(doc.Libraries[0].Roots, []string{first, second}) {
		t.Fatalf("libraries = %#v, want Films with both roots", doc.Libraries)
	}
	// A document that omits the library entirely removes nothing.
	if _, err := applyOffline(t, dataDir, `{"version":1,"backgroundWorkers":2,"plugins":[]}`); err != nil {
		t.Fatal(err)
	}
	if got := len(liveLibraries(t, dataDir)); got != 1 {
		t.Fatalf("omitting libraries left %d, want 1 (additive)", got)
	}
}

func TestApplyConfigOfflineMatchesTitleCaseSensitively(t *testing.T) {
	dataDir := offlineConfigDataDir(t)
	for _, title := range []string{"Music", "music"} {
		root := t.TempDir()
		if _, err := applyOffline(t, dataDir, libraryDoc("2", libraryEntry(title, "music", root))); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(liveLibraries(t, dataDir)); got != 2 {
		t.Fatalf("got %d libraries, want 2 (titles differ by case)", got)
	}
}

func TestApplyConfigOfflineRejectsUnknownPluginWithoutHalfApplying(t *testing.T) {
	dataDir := offlineConfigDataDir(t)
	_, err := applyOffline(t, dataDir, libraryDoc("7", libraryEntry("Ghosts", "ghost", "/media/ghosts")))
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v, want unknown plugin error naming ghost", err)
	}
	doc := exportOffline(t, dataDir)
	if doc.BackgroundWorkers == 7 || len(doc.Libraries) != 0 {
		t.Fatalf("invalid document half-applied: %#v", doc)
	}
}

func TestApplyConfigOfflineRefusesRunningServer(t *testing.T) {
	dataDir := offlineConfigDataDir(t)
	holdServerLockForTest(t, dataDir)
	if _, err := applyOffline(t, dataDir, `{"version":1,"backgroundWorkers":2,"plugins":[]}`); err == nil {
		t.Fatal("offline apply ran while the server lock was held")
	}
}

func TestExportConfigOfflineRoundTripIsStable(t *testing.T) {
	dataDir := offlineConfigDataDir(t)
	base := t.TempDir()
	doc := `{"version":1,"backgroundWorkers":3,"plugins":[{"id":"karaoke","active":false}],"libraries":[` +
		libraryEntry("Podcasts", "podcasts", filepath.Join(base, "p2"), filepath.Join(base, "p1")) + "," +
		libraryEntry("Films", "filmtv") + "," +
		libraryEntry("Home", "", filepath.Join(base, "home")) + `]}`
	if _, err := applyOffline(t, dataDir, doc); err != nil {
		t.Fatal(err)
	}
	first := exportOffline(t, dataDir)
	if first.BackgroundWorkers != 3 || len(first.Libraries) != 3 || first.Libraries[0].Title != "Films" {
		t.Fatalf("export = %#v, want workers 3 and libraries sorted by title", first)
	}
	// A pluginless Library exports with an empty plugin and re-applies as a no-op.
	if home := first.Libraries[1]; home.Title != "Home" || home.Plugin != "" {
		t.Fatalf("export library[1] = %#v, want the pluginless Home library", home)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	out, err := applyOffline(t, dataDir, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already up to date") {
		t.Fatalf("applying the export output = %q, want no-op", out)
	}
	if second := exportOffline(t, dataDir); !reflect.DeepEqual(first, second) {
		t.Fatalf("round trip changed the export:\nfirst  %#v\nsecond %#v", first, second)
	}
}

func TestApplyConfigOfflineGrantsLibraryToOwner(t *testing.T) {
	dataDir := offlineConfigDataDir(t)
	ctx := context.Background()
	st, err := store.Open(ctx, dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.CreateProfile(ctx, "Owner", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileOwner(ctx, profile.ID); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if _, err := applyOffline(t, dataDir, libraryDoc("2", libraryEntry("Music", "music", "/media/music"))); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(ctx, dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, err := st.Profile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(owner.Library) != 1 {
		t.Fatalf("owner grants = %v, want the new library", owner.Library)
	}
}

func TestParseConfigDocumentRejectsInvalidLibraries(t *testing.T) {
	cases := map[string]string{
		"relative root":     libraryDoc("2", libraryEntry("M", "music", "media/music")),
		"unclean root":      libraryDoc("2", libraryEntry("M", "music", "/media/../music")),
		"trailing slash":    libraryDoc("2", libraryEntry("M", "music", "/media/music/")),
		"padded root":       libraryDoc("2", libraryEntry("M", "music", "/media/music ")),
		"duplicate root":    libraryDoc("2", libraryEntry("M", "music", "/m", "/m")),
		"duplicate library": libraryDoc("2", libraryEntry("M", "music"), libraryEntry("M", "music")),
		"empty title":       libraryDoc("2", libraryEntry(" ", "music")),
		"unknown field":     libraryDoc("2", `{"title":"M","plugin":"music","roots":[],"id":"x"}`),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfigDocument([]byte(doc)); err == nil {
				t.Fatalf("parseConfigDocument accepted invalid document: %s", doc)
			}
		})
	}
}

func TestApplyConfigOfflineRefusesOverlappingLibraryRoots(t *testing.T) {
	dataDir := offlineConfigDataDir(t)
	before := exportOffline(t, dataDir).BackgroundWorkers
	shared := filepath.Join(t.TempDir(), "media")
	doc := libraryDoc(strconv.Itoa(before+3),
		libraryEntry("Music", "music", shared),
		libraryEntry("Films", "music", filepath.Join(shared, "films")))

	if _, err := applyOffline(t, dataDir, doc); err == nil {
		t.Fatal("apply-config accepted two libraries with overlapping folders")
	}
	if got := len(liveLibraries(t, dataDir)); got != 0 {
		t.Fatalf("overlapping apply wrote %d libraries before refusing, want 0", got)
	}
	if got := exportOffline(t, dataDir).BackgroundWorkers; got != before {
		t.Fatalf("overlapping apply changed background workers %d -> %d before refusing", before, got)
	}
}

// recordingConfigBackend stands in for a remote server: it reports one live
// Library and records the writes apply issues.
type recordingConfigBackend struct {
	live   configLiveLibrary
	writes []string
}

func (b *recordingConfigBackend) snapshot(context.Context) (configSnapshot, error) {
	return configSnapshot{Plugins: []configPluginDocItem{{ID: "music", Active: true}}, Libraries: []configLiveLibrary{b.live}}, nil
}

func (b *recordingConfigBackend) setBackgroundWorkers(context.Context, int) error { return nil }

func (b *recordingConfigBackend) setPluginActive(context.Context, string, bool) error { return nil }

func (b *recordingConfigBackend) createLibrary(_ context.Context, lib configLibraryDocItem) error {
	b.writes = append(b.writes, "create "+lib.Title)
	return nil
}

func (b *recordingConfigBackend) setLibraryRoots(context.Context, string, []string) error {
	return nil
}

func TestApplyConfigRemoteBackendLeavesRootOverlapToTheServer(t *testing.T) {
	backend := &recordingConfigBackend{live: configLiveLibrary{
		configLibraryDocItem: configLibraryDocItem{Title: "Music", Plugin: "music", Roots: []string{"/srv/remote/media"}},
		ID:                   "library-music",
	}}
	desired := configDocument{Version: configDocumentVersion, Libraries: []configLibraryDocItem{
		{Title: "Music", Plugin: "music", Roots: []string{"/srv/remote/media"}},
		{Title: "Films", Plugin: "music", Roots: []string{"/srv/remote/media/films"}},
	}}
	if _, err := applyConfigDocument(context.Background(), backend, desired); err != nil {
		t.Fatalf("remote apply resolved folders on the CLI host: %v", err)
	}
	if !reflect.DeepEqual(backend.writes, []string{"create Films"}) {
		t.Fatalf("remote apply wrote %v, want the create left to the server's own check", backend.writes)
	}
}

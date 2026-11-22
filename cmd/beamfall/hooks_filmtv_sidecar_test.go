//go:build !beamfall_appstore

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	filmtv "github.com/beamfall/plugin-filmtv"
)

func writeSidecarFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// CORE-0361: a film's sidecar parses to values the bundle declares, signed
// with the signature the mapper reports for it, and states every relation the
// format owns.
func TestFilmTVHydrationCarriesFilmSidecar(t *testing.T) {
	mapper := firstPartyHooks().HydrationMappers[filmtv.PluginID]
	sidecars, ok := mapper.(app.HydrationSidecarMapper)
	if !ok || sidecars.SidecarSignatureField() != filmtv.NFOSignatureField {
		t.Fatalf("Film/TV mapper sidecar support = %v", ok)
	}
	dir := filepath.Join(t.TempDir(), "Lantern (2019)")
	path := filepath.Join(dir, "Lantern (2019).mkv")
	writeSidecarFixture(t, path, "")
	if sidecars.SidecarSignature(path) != "" || sidecars.MapSidecar(path) != nil {
		t.Fatal("film without a sidecar carries local values")
	}
	writeSidecarFixture(t, filepath.Join(dir, "Lantern (2019).nfo"), `<?xml version="1.0" encoding="UTF-8"?>
<movie><title>The Lantern</title><sorttitle>Lantern, The</sorttitle><plot>A keeper stays.</plot>
<tagline>Light stays.</tagline><year>2019</year><genre>Drama</genre><studio>North Pier</studio>
<set><name>Lantern Collection</name></set><director>Mara Ellison</director>
<actor><name>Iris Calder</name><role>Wren</role><order>0</order></actor><playcount>1</playcount></movie>`)
	signature := sidecars.SidecarSignature(path)
	if signature == "" {
		t.Fatal("sidecar signature empty with a sidecar present")
	}
	if plan := mapper.MapHydration(nil, path); !plan.MarkComplete {
		t.Fatalf("film plan = %+v, want marked complete", plan)
	}
	local := sidecars.MapSidecar(path)
	if local == nil || local.Unreadable || local.Signature != signature {
		t.Fatalf("film sidecar = %+v, want signed %q", local, signature)
	}
	if len(local.OwnedRelations) != len(filmtv.NFOOwnedRelations()) {
		t.Fatalf("owned relations = %+v, want every NFO relation", local.OwnedRelations)
	}
	if local.Title != "The Lantern" || local.SortTitle != "Lantern, The" || local.Summary != "A keeper stays." || local.Year != 2019 {
		t.Fatalf("local built-ins = %+v", local)
	}
	declared := filmtv.Bundle().MediaFieldTypes()
	tagline := false
	for _, field := range local.MediaFields {
		if declared[field.Name] != string(field.Type) {
			t.Fatalf("local field %s type %q, declared %q", field.Name, field.Type, declared[field.Name])
		}
		tagline = tagline || (field.Name == "tagline" && field.Text == "Light stays.")
	}
	if !tagline {
		t.Fatalf("local fields = %+v, want the tagline", local.MediaFields)
	}
	assertFilmTVPlanDeclared(t, app.HydrationPlan{Relations: local.Relations})
	names := map[string]int{}
	for _, relation := range local.Relations {
		if relation.ToEntity != nil {
			t.Fatalf("local relation %+v targets an Entity", relation)
		}
		names[relation.Name]++
	}
	if names["credited-in"] != 2 || names["genre-of"] != 1 || names["produced"] != 1 || names["film-series-film"] != 1 {
		t.Fatalf("local relations = %v", names)
	}
}

// CORE-0361: container sidecars are not imported. An episode's own sidecar
// still parses, while its show and season sidecars put no field on the
// container Entities the plan writes.
func TestFilmTVHydrationIgnoresContainerSidecars(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Tidewater")
	path := filepath.Join(root, "Season 1", "Tidewater S01E01.mkv")
	writeSidecarFixture(t, path, "")
	writeSidecarFixture(t, filepath.Join(root, "tvshow.nfo"), `<tvshow><title>Tidewater</title><plot>A harbour town.</plot><studio>Coast TV</studio></tvshow>`)
	writeSidecarFixture(t, filepath.Join(root, "Season 1", "season.nfo"), `<season><plot>The first tide.</plot></season>`)
	writeSidecarFixture(t, filepath.Join(root, "Season 1", "Tidewater S01E01.nfo"), `<episodedetails><title>Flood</title><plot>Water rises.</plot><season>1</season><episode>1</episode></episodedetails>`)

	mapper := firstPartyHooks().HydrationMappers[filmtv.PluginID]
	plan := mapper.MapHydration(nil, path)
	assertFilmTVPlanDeclared(t, plan)
	var season, show []store.EntityField
	for _, relation := range plan.Relations {
		if relation.Name == filmtv.SeasonOfRelation {
			season, show = relation.From.Fields, relation.ToEntity.Fields
		}
	}
	if !hasNumberField(season, "number", 1) || hasTextField(season, "overview", "The first tide.") {
		t.Fatalf("season fields = %+v, want the number and no sidecar value", season)
	}
	if len(show) != 0 {
		t.Fatalf("show fields = %+v, want none", show)
	}
	local := mapper.(app.HydrationSidecarMapper).MapSidecar(path)
	if local == nil || local.Summary != "Water rises." {
		t.Fatalf("episode sidecar = %+v, want its own plot", local)
	}
}

// CORE-0361: a sidecar that exists but cannot be read parses as Unreadable,
// not as no sidecar, so Core keeps what it imported before.
func TestFilmTVSidecarUnreadableIsNotRemoval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Lantern (2019).mkv")
	nfo := filepath.Join(dir, "Lantern (2019).nfo")
	writeSidecarFixture(t, path, "")
	writeSidecarFixture(t, nfo, `<movie><title>The Lantern</title></movie>`)
	if err := os.Chmod(nfo, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(nfo, 0o600) })
	if f, err := os.Open(nfo); err == nil { // #nosec G304 -- nfo is under this test's t.TempDir()
		_ = f.Close()
		t.Skip("permissions do not stop this user reading the sidecar")
	}
	sidecars := firstPartyHooks().HydrationMappers[filmtv.PluginID].(app.HydrationSidecarMapper)
	if sidecars.SidecarSignature(path) == "" {
		t.Fatal("unreadable sidecar has no signature, so it reads as removed")
	}
	if local := sidecars.MapSidecar(path); local == nil || !local.Unreadable {
		t.Fatalf("unreadable sidecar = %+v, want Unreadable", local)
	}
}

func hasTextField(fields []store.EntityField, name, text string) bool {
	for _, field := range fields {
		if field.Name == name && field.Type == store.EntityFieldText && field.Text == text {
			return true
		}
	}
	return false
}

// CORE-0361: an NFO play count reaches engagement through the migration path,
// as watched for the importing Profile and no other.
func TestFilmTVNFOImportMarksWatchedForTheImportingProfileOnly(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	path := filepath.Join(root, "Lantern (2019)", "Lantern (2019).mkv")
	writeSidecarFixture(t, path, "bytes")
	writeSidecarFixture(t, filepath.Join(root, "Lantern (2019)", "Lantern (2019).nfo"), `<movie><title>The Lantern</title><playcount>1</playcount></movie>`)
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: "Lantern", Container: "mkv", Codec: "h264",
		Class: store.MediaClassVideo, ContentFingerprint: "fp-nfo-playcount-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProfile(ctx, "Other", store.ProfileAdult, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}

	key := app.MigrationSourceKey{PluginID: filmtv.PluginID, ImporterID: "nfo"}
	factory := firstPartyHooks().MigrationSourceFactories[key]
	if factory == nil {
		t.Fatalf("nfo importer factory missing: %+v", key)
	}
	categories := []string{migrate.CategoryWatchState}
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, staticSourceResolver{source: factory(categories, root)}))
	payload, err := json.Marshal(migrate.Payload{
		Version: 1, PluginID: filmtv.PluginID, ImporterID: "nfo", Categories: categories,
		ProfileID: "profile-default", SourcePath: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	enqueued, err := st.EnqueueJob(ctx, store.Job{Kind: migrate.JobKind, Class: store.JobClassBackground, DedupeKey: "filmtv:nfo", Payload: payload, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	done, err := st.Job(ctx, enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d: %q", done.Progress.Current, done.Progress.Total, done.Progress.Message)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok || !progress.Watched {
		t.Fatalf("importing profile progress = %+v ok=%v err=%v, want watched", progress, ok, err)
	}
	if progress, ok, err := st.PlaybackProgressFor(ctx, other.ID, media.ID); err != nil || (ok && progress.Watched) {
		t.Fatalf("other profile progress = %+v ok=%v err=%v, want untouched", progress, ok, err)
	}
}

// CORE-0361: the NFO importer honours the run's categories, so a run that
// does not select watch state writes none.
func TestFilmTVNFOImporterHonoursCategories(t *testing.T) {
	factory := firstPartyHooks().MigrationSourceFactories[app.MigrationSourceKey{PluginID: filmtv.PluginID, ImporterID: "nfo"}]
	if factory == nil {
		t.Fatal("nfo importer factory missing")
	}
	root := t.TempDir()
	if got := factory([]string{migrate.CategoryRatings}, root).Categories(); slices.Contains(got, migrate.CategoryWatchState) {
		t.Fatalf("categories without watch state selected = %v, want no watch state", got)
	}
	if got := factory([]string{migrate.CategoryWatchState}, root).Categories(); !slices.Contains(got, migrate.CategoryWatchState) {
		t.Fatalf("categories with watch state selected = %v, want watch state", got)
	}
}

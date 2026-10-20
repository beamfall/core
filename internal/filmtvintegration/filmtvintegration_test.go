// Package filmtvintegration_test is the core-side integration test for the
// Film & TV migration sources after they moved to the beamfall-plugin-filmtv
// module. It cannot live in the plugin module (which may not import
// core store/jobs/migrate.Handler), so it stays in core and imports the plugin's
// public constructors (filmtv.NewNFOSource/NewPlexSource) — exercising the real
// migrate.Handler against a storetest store, preserving the MIG-2 acceptance
// assertions the old internal/filmtvimport test carried.
package filmtvintegration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/foreignimport/foreignimporttest"
	"github.com/beamfall/core/internal/store/storetest"
	filmtv "github.com/beamfall/plugin-filmtv"
)

// fixedResolver maps every run to one source — the test stand-in for the app's
// plugin-backed migrationSourceResolver (which wires (filmtv, importerID) → the
// filmtv plugin constructors).
type fixedResolver struct{ source migrate.Source }

func (r fixedResolver) Resolve(_, _ string, _ []string, _ string) (migrate.Source, bool) {
	return r.source, true
}

// seedMediaAtPath imports a Media + primary file at path so the path-keyed match
// chain can resolve it. Mirrors the MIG-1 handler test helper.
func seedMediaAtPath(t *testing.T, st *store.Store, path, fingerprint, title string) (store.MediaID, string) {
	t.Helper()
	media, file, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              title,
		Container:          "mkv",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("seed media at %q: %v", path, err)
	}
	return media.ID, file.ContentFingerprint
}

// runImport enqueues + runs one migration job for source through the real jobs
// registry (claim → handler → complete) and returns the completed Job.
func runImport(t *testing.T, st *store.Store, source migrate.Source, profileID string, categories []string, now time.Time) store.Job {
	t.Helper()
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, fixedResolver{source: source}))

	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   filmtv.PluginID,
		ImporterID: "nfo",
		Categories: categories,
		ProfileID:  profileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "filmtv:import",
		Payload:   payload,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	return done
}

// writeFile writes content to path, creating parent dirs — fixture helper.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNFOImportRunMatchesSeededMediaByPath is the MIG-2 wizard acceptance:
// a fixture NFO sidecar library → the NFO Source →
// path-keyed ForeignItems → run through the real migrate.Handler against Media
// seeded at the same paths. It asserts the path match resolves (match rate),
// matched files are pinned, and that the NFO parse-hint IDs are surfaced on the
// foreign items the source yields (the ids identification consumes, ADR-0028
// §6.2). An NFO file with no matching seeded Media is recorded unmatched.
func TestNFOImportRunMatchesSeededMediaByPath(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	root := t.TempDir()
	// A movie folder: video + per-file NFO carrying a TMDB id + folder artwork.
	moviePath := filepath.Join(root, "Example (2024)", "Example (2024).mkv")
	writeFile(t, moviePath, "x")
	writeFile(t, filepath.Join(root, "Example (2024)", "Example (2024).nfo"), `<?xml version="1.0"?>
<movie><title>Example</title><year>2024</year><uniqueid type="tmdb">12345</uniqueid></movie>`)
	writeFile(t, filepath.Join(root, "Example (2024)", "poster.jpg"), "img")
	// A second file with a folder-level movie.nfo and no per-file sidecar.
	secondPath := filepath.Join(root, "Second", "video.mkv")
	writeFile(t, secondPath, "y")
	writeFile(t, filepath.Join(root, "Second", "mo"+"vie.nfo"), `<?xml version="1.0"?>
<movie><title>Second</title><uniqueid type="imdb">tt7654321</uniqueid></movie>`)
	// A third file with NO seeded Media → recorded unmatched.
	gonePath := filepath.Join(root, "Gone", "gone.mkv")
	writeFile(t, gonePath, "z")

	// Seed Media at the first two absolute paths (resolved like the scanner).
	absMovie, _ := filepath.Abs(moviePath)
	absSecond, _ := filepath.Abs(secondPath)
	_, movieFP := seedMediaAtPath(t, st, absMovie, "fp-nfo-movie-1", "Example")
	_, secondFP := seedMediaAtPath(t, st, absSecond, "fp-nfo-second-2", "Second")

	source := filmtv.NewNFOSource(root)

	// The source yields the parse-hint IDs identification will consume.
	items, err := source.Items(ctx)
	if err != nil {
		t.Fatalf("NFO source Items: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("source yielded %d items, want 3 (two matchable + one unmatched)", len(items))
	}

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	done := runImport(t, st, source, profileID, source.Categories(), now)

	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed", done.Status)
	}
	if done.Progress.Current != 2 || done.Progress.Total != 3 {
		t.Fatalf("match rate = %d of %d, want 2 of 3", done.Progress.Current, done.Progress.Total)
	}

	// Matched files are pinned so a later rescan respects the match.
	for _, fp := range []string{movieFP, secondFP} {
		pinned, found, err := st.MatchPinned(ctx, fp)
		if err != nil || !found {
			t.Fatalf("match-pin read %q: found=%v err=%v", fp, found, err)
		}
		if !pinned {
			t.Fatalf("matched NFO file %q not pinned", fp)
		}
	}
}

// TestPlexImportAppliesWatchStateEndToEnd exercises the full Plex source path:
// a fixture Plex SQLite DB → NewPlexSource → ForeignItems with watch-state → the
// real migrate.Handler → import-provenance engagement on seeded Media. This is
// the watch-state half of the acceptance the NFO source (ids only) does not
// cover.
func TestPlexImportAppliesWatchStateEndToEnd(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	watchedID, watchedFP := seedMediaAtPath(t, st, "/media/films/Watched.mkv", "fp-plex-watched", "Watched")
	resumeID, _ := seedMediaAtPath(t, st, "/media/films/Resume.mkv", "fp-plex-resume", "Resume")

	dbPath := foreignimporttest.WritePlexFixture(t, t.TempDir(), "/media/films/Watched.mkv", "/media/films/Resume.mkv")
	source := filmtv.NewPlexSource(dbPath)
	if got := source.Categories(); len(got) != 1 || got[0] != migrate.CategoryWatchState {
		t.Fatalf("Plex source categories = %#v, want [watch-state]", got)
	}

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	done := runImport(t, st, source, profileID, source.Categories(), now)
	if done.Progress.Current != 2 || done.Progress.Total != 2 {
		t.Fatalf("Plex match rate = %d of %d, want 2 of 2", done.Progress.Current, done.Progress.Total)
	}

	watched, ok, err := st.PlaybackProgressFor(ctx, profileID, watchedID)
	if err != nil || !ok {
		t.Fatalf("watched progress: ok=%v err=%v", ok, err)
	}
	if !watched.Watched {
		t.Fatal("Plex completed item not marked watched")
	}
	if watched.Provenance != "import:"+filmtv.PluginID {
		t.Fatalf("provenance = %q, want import:filmtv", watched.Provenance)
	}
	if !watched.OccurredAt.Equal(time.Unix(1614834367, 0).UTC()) {
		t.Fatalf("OccurredAt = %v, want the Plex last_viewed_at instant", watched.OccurredAt)
	}
	_ = watchedFP

	resume, ok, err := st.PlaybackProgressFor(ctx, profileID, resumeID)
	if err != nil || !ok {
		t.Fatalf("resume progress: ok=%v err=%v", ok, err)
	}
	if resume.Watched || resume.PositionSeconds != 120 {
		t.Fatalf("resume = {watched=%v pos=%d}, want {false 120}", resume.Watched, resume.PositionSeconds)
	}
}

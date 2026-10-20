package migrate_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/foreignimport/foreignimporttest"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeSource is a test ImporterSpec parser (MIG-2 ships the real Plex/Emby/etc.
// parsers; MIG-1 builds the generic handler this fake exercises). It yields a
// fixed set of foreign items over the declared categories.
type fakeSource struct {
	categories []string
	items      []migrate.ForeignItem
}

func (f fakeSource) Categories() []string { return f.categories }
func (f fakeSource) Items(context.Context) ([]migrate.ForeignItem, error) {
	return f.items, nil
}

type sourceUserItemsSource struct {
	fakeSource
	itemsBySourceUser map[string][]migrate.ForeignItem
}

func (s sourceUserItemsSource) ItemsBySourceUser(context.Context) (map[string][]migrate.ForeignItem, error) {
	return s.itemsBySourceUser, nil
}

type cancelingSource struct {
	fakeSource
	cancel context.CancelFunc
}

func (f cancelingSource) Items(context.Context) ([]migrate.ForeignItem, error) {
	f.cancel()
	return f.items, nil
}

type trackingSource struct {
	called *bool
}

func (s trackingSource) Categories() []string { return []string{migrate.CategoryWatchState} }
func (s trackingSource) Items(context.Context) ([]migrate.ForeignItem, error) {
	*s.called = true
	return []migrate.ForeignItem{{Path: "/media/films/Watched.mkv", WatchState: migrate.WatchState{Present: true, Watched: true}}}, nil
}

// fakeResolver maps every (plugin, importer) to one fixed source — the test
// stand-in for the app's plugin-backed resolver.
type fakeResolver struct{ source migrate.Source }

func (r fakeResolver) Resolve(pluginID, importerID string, categories []string, sourcePath string) (migrate.Source, bool) {
	return r.source, true
}

type fakeResumePolicyResolver struct {
	policy store.ResumePolicy
}

func (r fakeResumePolicyResolver) ResumePolicy(context.Context, store.MediaID) (store.ResumePolicy, error) {
	return r.policy, nil
}

type checkpointStore struct {
	*store.Store
	checkpointCalls int
}

func (s *checkpointStore) UpdateJobCheckpoint(ctx context.Context, id store.JobID, leaseOwner string, checkpoint []byte, progress store.JobProgress, now time.Time) error {
	s.checkpointCalls++
	return s.Store.UpdateJobCheckpoint(ctx, id, leaseOwner, checkpoint, progress, now)
}

// seedMediaAtPath imports a Media + primary file at path with a synthetic
// content fingerprint so the path-based match chain can resolve it. Returns the
// Media ID and the file's content fingerprint (the match-pin key).
func seedMediaAtPath(t *testing.T, st *store.Store, path, fingerprint, title string) (store.MediaID, string) {
	return seedMediaAtPathWithClass(t, st, path, fingerprint, title, store.MediaClassVideo)
}

func seedMediaAtPathWithClass(t *testing.T, st *store.Store, path, fingerprint, title string, class store.MediaClass) (store.MediaID, string) {
	t.Helper()
	media, file, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              title,
		Container:          "mkv",
		Codec:              "h264",
		Class:              class,
		ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("seed media at %q: %v", path, err)
	}
	return media.ID, file.ContentFingerprint
}

func addExternalID(t *testing.T, st *store.Store, mediaID store.MediaID, scheme, value string) {
	t.Helper()
	if err := st.AddMediaExternalID(context.Background(), mediaID, scheme, value); err != nil {
		t.Fatalf("add external id %s/%s to %s: %v", scheme, value, mediaID, err)
	}
}

func TestMigrationRejectsUnsupportedPayloadVersion(t *testing.T) {
	st := storetest.Open(t)
	payload := migrate.Payload{
		Version:    2,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState},
		ProfileID:  "profile-default",
	}
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: fakeSource{categories: []string{migrate.CategoryWatchState}}}))
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "filmtv:plex:v2",
		Payload:   encoded,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RunOne(context.Background(), nil, time.Now().UTC()); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusFailed || done.Error != "migrate: unsupported payload version 2 (want 1)" {
		t.Fatalf("job = status %q error %q, want unsupported payload version failure", done.Status, done.Error)
	}
}

func TestMigrationRejectsUncertifiedForeignDBSchemaBeforeReadingItems(t *testing.T) {
	st := storetest.Open(t)
	called := false
	sourcePath := foreignimporttest.WriteVersionedSQLiteFixture(t, t.TempDir(), "plex.db", 999)
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState},
		ProfileID:  "profile-default",
		SourcePath: sourcePath,
	}
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: trackingSource{called: &called}}))
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "filmtv:plex:uncertified",
		Payload:   encoded,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = registry.RunOne(context.Background(), []store.JobClass{store.JobClassBackground}, time.Now().UTC())
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", done.Status)
	}
	if !strings.Contains(done.Error, "uncertified plex database schema version user_version:999") {
		t.Fatalf("job error = %q, want uncertified schema report", done.Error)
	}
	if called {
		t.Fatal("source Items was called after uncertified schema")
	}
}

func TestMigrationStopsWhenContextCanceledBeforeItem(t *testing.T) {
	st := storetest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	source := cancelingSource{
		fakeSource: fakeSource{
			categories: []string{migrate.CategoryWatchState},
			items:      []migrate.ForeignItem{{Path: "/media/local/one.mkv"}},
		},
		cancel: cancel,
	}
	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState},
		ProfileID:  "profile-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := migrate.Handler(st, fakeResolver{source: source})
	if _, err := handler(ctx, store.Job{ID: "job-cancel", Payload: payload}); !errors.Is(err, context.Canceled) {
		t.Fatalf("handler err = %v, want context canceled", err)
	}
}

func TestMigrationCheckpointsLongRuns(t *testing.T) {
	base := storetest.Open(t)
	st := &checkpointStore{Store: base}
	items := make([]migrate.ForeignItem, 100)
	for i := range items {
		items[i] = migrate.ForeignItem{Path: "/missing/item.mkv"}
	}
	source := fakeSource{categories: []string{migrate.CategoryWatchState}, items: items}

	registry := jobs.NewRegistry(st, "test-worker-checkpoints")
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: source}))
	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState},
		ProfileID:  "profile-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := base.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "filmtv:plex:checkpoints",
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RunOne(context.Background(), nil, time.Now().UTC()); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	done, err := base.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Progress.Current != 0 || done.Progress.Total != 100 {
		t.Fatalf("match-rate = %d of %d, want 0 of 100", done.Progress.Current, done.Progress.Total)
	}
	if st.checkpointCalls != 2 {
		t.Fatalf("checkpoint calls = %d, want running + final", st.checkpointCalls)
	}
}

// enqueueAndRun enqueues a migration run for the given source and runs it once
// through the real jobs registry (the production execution path: claim →
// handler → complete). It returns the completed Job so the test can read the
// match-rate report off its progress row.
func enqueueAndRun(t *testing.T, st *store.Store, source migrate.Source, profileID string, categories []string, now time.Time) store.Job {
	return enqueueAndRunWithOptions(t, st, source, profileID, categories, now)
}

func TestMigrationWatchStateImportMatchesByExternalID(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"
	mediaID, _ := seedMediaAtPath(t, st, "/media/films/history.mkv", "fp-trakt-history", "History")
	addExternalID(t, st, mediaID, "tmdb", "12345")
	historical := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)

	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			ExternalIDs: []migrate.ExternalID{{Scheme: "tmdb", Value: "12345"}},
			WatchState:  migrate.WatchState{Present: true, Watched: true, OccurredAt: historical},
		}},
	}
	done := enqueueAndRun(t, st, source, profileID, []string{migrate.CategoryWatchState}, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, profileID, mediaID)
	if err != nil || !ok {
		t.Fatalf("progress: ok=%v err=%v", ok, err)
	}
	if !progress.Watched || progress.PositionSeconds != 0 {
		t.Fatalf("external-ID watch-state = {watched=%v pos=%d}, want watched with no resume", progress.Watched, progress.PositionSeconds)
	}
	if progress.Provenance != "import:filmtv" {
		t.Fatalf("provenance = %q, want import:filmtv", progress.Provenance)
	}
	if !progress.OccurredAt.Equal(historical) {
		t.Fatalf("OccurredAt = %v, want %v", progress.OccurredAt, historical)
	}
	// Run-scoped import provenance: a watch-state item resolved by the
	// external-ID fallback is stamped "external-id" (not "path"), with the run's
	// job ID and importer id.
	if progress.ImportRunID != string(done.ID) || progress.ImportImporterID != "plex" || progress.ImportMatchMethod != "external-id" {
		t.Fatalf("import provenance = {run=%q importer=%q method=%q}, want {%q plex external-id}",
			progress.ImportRunID, progress.ImportImporterID, progress.ImportMatchMethod, done.ID)
	}
}

// TestMigrationExternalIDPrecedesPath proves the ADR-0037 §2.3 resolution order:
// when an item carries BOTH an external ID and a path that each resolve to a
// distinct Media, external-ID wins (the durable key), and MatchMethod records the
// branch that actually resolved identity ("external-id", not "path").
func TestMigrationExternalIDPrecedesPath(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"
	// The path resolves to one Media; the external ID to a different one. If the
	// chain still tried path first it would resolve (and stamp "path") the wrong
	// Media — the external-ID Media must win.
	pathMedia, _ := seedMediaAtPath(t, st, "/media/films/moved.mkv", "fp-path-media", "PathMedia")
	idMedia, _ := seedMediaAtPath(t, st, "/media/films/canonical.mkv", "fp-id-media", "IDMedia")
	addExternalID(t, st, idMedia, "tmdb", "778899")
	historical := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)

	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			Path:        "/media/films/moved.mkv",
			ExternalIDs: []migrate.ExternalID{{Scheme: "tmdb", Value: "778899"}},
			WatchState:  migrate.WatchState{Present: true, Watched: true, OccurredAt: historical},
		}},
	}
	done := enqueueAndRun(t, st, source, profileID, []string{migrate.CategoryWatchState}, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	// The external-ID Media carries the imported watch-state, stamped "external-id".
	idProgress, ok, err := st.PlaybackProgressFor(ctx, profileID, idMedia)
	if err != nil || !ok {
		t.Fatalf("external-ID media progress: ok=%v err=%v", ok, err)
	}
	if !idProgress.Watched {
		t.Fatalf("external-ID media watched = %v, want true", idProgress.Watched)
	}
	if idProgress.ImportMatchMethod != "external-id" {
		t.Fatalf("MatchMethod = %q, want external-id (external-ID precedes path per ADR-0037 §2.3)", idProgress.ImportMatchMethod)
	}
	// The path Media must NOT have received the watch-state — the chain resolved by
	// external ID and never fell through to the path branch.
	if _, pathApplied, err := st.PlaybackProgressFor(ctx, profileID, pathMedia); err != nil || pathApplied {
		t.Fatalf("path media progress present=%v err=%v, want no progress (external-ID won)", pathApplied, err)
	}
}

func TestMigrationExternalIDDuplicateIsUnmatched(t *testing.T) {
	st := storetest.Open(t)
	const profileID = "profile-default"
	first, _ := seedMediaAtPath(t, st, "/media/films/one.mkv", "fp-trakt-one", "One")
	second, _ := seedMediaAtPath(t, st, "/media/films/two.mkv", "fp-trakt-two", "Two")
	addExternalID(t, st, first, "imdb", "tt1234567")
	addExternalID(t, st, second, "imdb", "tt1234567")

	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			ExternalIDs: []migrate.ExternalID{{Scheme: "imdb", Value: "tt1234567"}},
			WatchState:  migrate.WatchState{Present: true, Watched: true, OccurredAt: time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)},
		}},
	}
	done := enqueueAndRun(t, st, source, profileID, []string{migrate.CategoryWatchState}, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 0 || done.Progress.Total != 1 {
		t.Fatalf("duplicate external-ID match-rate = %d of %d, want 0 of 1", done.Progress.Current, done.Progress.Total)
	}
}

func TestMigrationExternalIDProviderPrecedenceResolvesConflict(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"
	tmdbMedia, _ := seedMediaAtPath(t, st, "/media/films/tmdb.mkv", "fp-tmdb", "TMDB")
	imdbMedia, _ := seedMediaAtPath(t, st, "/media/films/imdb.mkv", "fp-imdb", "IMDB")
	addExternalID(t, st, tmdbMedia, "tmdb", "12345")
	addExternalID(t, st, imdbMedia, "imdb", "tt12345")

	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			ExternalIDs: []migrate.ExternalID{{Scheme: "imdb", Value: "tt12345"}, {Scheme: "tmdb", Value: "12345"}},
			WatchState:  migrate.WatchState{Present: true, Watched: true, OccurredAt: time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)},
		}},
	}
	done := enqueueAndRun(t, st, source, profileID, []string{migrate.CategoryWatchState}, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("provider-precedence match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	if progress, ok, err := st.PlaybackProgressFor(ctx, profileID, tmdbMedia); err != nil || !ok || !progress.Watched {
		t.Fatalf("tmdb media progress: ok=%v progress=%+v err=%v, want watched", ok, progress, err)
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, profileID, imdbMedia); err != nil || ok {
		t.Fatalf("imdb media progress present=%v err=%v, want none", ok, err)
	}
}

func TestMigrationExternalIDEqualPrecedenceConflictIsUnmatched(t *testing.T) {
	st := storetest.Open(t)
	const profileID = "profile-default"
	first, _ := seedMediaAtPath(t, st, "/media/films/one.mkv", "fp-equal-one", "One")
	second, _ := seedMediaAtPath(t, st, "/media/films/two.mkv", "fp-equal-two", "Two")
	addExternalID(t, st, first, "fixture-a", "one")
	addExternalID(t, st, second, "fixture-b", "two")

	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			ExternalIDs: []migrate.ExternalID{{Scheme: "fixture-a", Value: "one"}, {Scheme: "fixture-b", Value: "two"}},
			WatchState:  migrate.WatchState{Present: true, Watched: true, OccurredAt: time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)},
		}},
	}
	done := enqueueAndRun(t, st, source, profileID, []string{migrate.CategoryWatchState}, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 0 || done.Progress.Total != 1 {
		t.Fatalf("equal-precedence conflict match-rate = %d of %d, want 0 of 1", done.Progress.Current, done.Progress.Total)
	}
}

func enqueueAndRunWithOptions(t *testing.T, st *store.Store, source migrate.Source, profileID string, categories []string, now time.Time, opts ...migrate.HandlerOption) store.Job {
	t.Helper()
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: source}, opts...))

	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: categories,
		ProfileID:  profileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "filmtv:plex",
		Payload:   payload,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("enqueue migration job: %v", err)
	}
	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("read job back: %v", err)
	}
	return done
}

func enqueuePayloadAndRun(t *testing.T, st *store.Store, source migrate.Source, payload migrate.Payload, dedupeKey string, now time.Time, opts ...migrate.HandlerOption) store.Job {
	t.Helper()
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: source}, opts...))

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: dedupeKey,
		Payload:   encoded,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("enqueue migration job: %v", err)
	}
	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("read job back: %v", err)
	}
	return done
}

// TestMigrationImportRunMatchesAppliesAndIsRerunnable is the MIG-1 vertical
// slice: the generic import job handler + path-based match chain. It
// seeds two Media at known paths, enqueues an import whose source yields three
// foreign watch-state items — two matching the seeded paths, one at an unknown
// path — and runs it through the real jobs registry. It asserts:
//   - matched items get import-provenance, historically-timestamped engagement
//     written through UpsertPlaybackProgressWithProvenance;
//   - matched files are match-pinned;
//   - the unmatched item is recorded in the run's match-rate report;
//   - a second run does NOT double-apply (re-runnable / strictly additive,
//     ADR-0037 §2.4): an already-complete item is left untouched and an
//     in-progress item only advances on a strictly-ahead position.
func TestMigrationImportRunMatchesAppliesAndIsRerunnable(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	watchedID, watchedFP := seedMediaAtPath(t, st, "/media/films/Watched.mkv", "fp-watched-0001", "Watched")
	resumeID, resumeFP := seedMediaAtPath(t, st, "/media/films/Resume.mkv", "fp-resume-0002", "Resume")

	historical := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{
			// Matches Watched.mkv: a completed item, backdated.
			{Path: "/media/films/Watched.mkv", WatchState: migrate.WatchState{
				Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical,
			}},
			// Matches Resume.mkv (via a path that filepath.Clean normalizes —
			// exercises the path-normalized chain step): an in-progress item.
			{Path: "/media/films/../films/Resume.mkv", WatchState: migrate.WatchState{
				Present: true, PositionSeconds: 120, DurationSeconds: 600, OccurredAt: historical,
			}},
			// No Media at this path: recorded unmatched, never applied.
			{Path: "/media/films/Gone.mkv", WatchState: migrate.WatchState{
				Present: true, Watched: true, DurationSeconds: 600,
			}},
		},
	}

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	done := enqueueAndRun(t, st, source, profileID, []string{migrate.CategoryWatchState}, now)

	// Match-rate report rides on the job progress row.
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed", done.Status)
	}
	if done.Progress.Current != 2 || done.Progress.Total != 3 {
		t.Fatalf("match-rate = %d of %d, want 2 of 3", done.Progress.Current, done.Progress.Total)
	}

	// Matched, completed item: import provenance + historical timestamp.
	watched, ok, err := st.PlaybackProgressFor(ctx, profileID, watchedID)
	if err != nil || !ok {
		t.Fatalf("watched progress: ok=%v err=%v", ok, err)
	}
	if !watched.Watched {
		t.Fatal("matched completed item not marked watched")
	}
	if watched.Provenance != "import:filmtv" {
		t.Fatalf("provenance = %q, want import:filmtv", watched.Provenance)
	}
	if !watched.OccurredAt.Equal(historical) {
		t.Fatalf("OccurredAt = %v, want historical %v", watched.OccurredAt, historical)
	}
	// Run-scoped import provenance: a path-matched item is stamped
	// "path" with the run's job ID + importer id, so a per-run audit / re-run
	// skip-explanation can attribute this engagement row.
	if watched.ImportRunID != string(done.ID) || watched.ImportImporterID != "plex" || watched.ImportMatchMethod != "path" {
		t.Fatalf("import provenance = {run=%q importer=%q method=%q}, want {%q plex path}",
			watched.ImportRunID, watched.ImportImporterID, watched.ImportMatchMethod, done.ID)
	}
	// The historical import must NOT masquerade as a fresh play on the recency
	// shelves: updated_at_ms (UpdatedAt) is the import instant, not the
	// historical time.
	if watched.UpdatedAt.Before(now) {
		t.Fatalf("UpdatedAt = %v, want >= import instant %v (recency key not backdated)", watched.UpdatedAt, now)
	}

	// Matched, in-progress item: resume position applied (video resumes).
	resume, ok, err := st.PlaybackProgressFor(ctx, profileID, resumeID)
	if err != nil || !ok {
		t.Fatalf("resume progress: ok=%v err=%v", ok, err)
	}
	if resume.Watched || resume.PositionSeconds != 120 {
		t.Fatalf("resume progress = {watched=%v pos=%d}, want {false 120}", resume.Watched, resume.PositionSeconds)
	}

	// Matched files are pinned.
	for _, fp := range []string{watchedFP, resumeFP} {
		pinned, found, err := st.MatchPinned(ctx, fp)
		if err != nil || !found {
			t.Fatalf("match-pin read %q: found=%v err=%v", fp, found, err)
		}
		if !pinned {
			t.Fatalf("matched file %q not pinned", fp)
		}
	}

	// Re-run with the SAME source: strictly additive, no double-apply. The
	// completed item stays complete (play count not re-incremented); the
	// in-progress item is unchanged because Beamfall has advanced beyond the
	// source, and both skips are named by item in this run's report.
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID:       profileID,
		MediaID:         resumeID,
		PositionSeconds: 240,
		DurationSeconds: 600,
	}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatalf("advance Beamfall resume position: %v", err)
	}
	watchedPlaysBefore := watched.PlayCount
	done2 := enqueueAndRun(t, st, source, profileID, []string{migrate.CategoryWatchState}, now.Add(time.Hour))
	if done2.Progress.Current != 2 || done2.Progress.Total != 3 {
		t.Fatalf("re-run match-rate = %d of %d, want 2 of 3", done2.Progress.Current, done2.Progress.Total)
	}
	if !strings.Contains(done2.Progress.Message, "skipped-beamfall-newer: #1, #2") {
		t.Fatalf("re-run report = %q, want itemized skipped-newer disposition", done2.Progress.Message)
	}
	watchedAfter, _, err := st.PlaybackProgressFor(ctx, profileID, watchedID)
	if err != nil {
		t.Fatal(err)
	}
	if watchedAfter.PlayCount != watchedPlaysBefore {
		t.Fatalf("re-run double-applied: play count %d -> %d", watchedPlaysBefore, watchedAfter.PlayCount)
	}
	resumeAfter, _, err := st.PlaybackProgressFor(ctx, profileID, resumeID)
	if err != nil {
		t.Fatal(err)
	}
	if resumeAfter.PositionSeconds != 240 {
		t.Fatalf("re-run moved resume position to %d, want Beamfall-newer 240", resumeAfter.PositionSeconds)
	}
	records, err := st.UnmatchedRecords(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("re-run duplicated unmatched records: got %d, want 1", len(records))
	}

	// A re-run that IS strictly ahead advances the in-progress item (the
	// switch-day delta ADR-0037 §2.4 describes).
	ahead := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{
			{Path: "/media/films/Resume.mkv", WatchState: migrate.WatchState{
				Present: true, PositionSeconds: 300, DurationSeconds: 600, OccurredAt: historical,
			}},
		},
	}
	enqueueAndRun(t, st, ahead, profileID, []string{migrate.CategoryWatchState}, now.Add(2*time.Hour))
	resumeAdvanced, _, err := st.PlaybackProgressFor(ctx, profileID, resumeID)
	if err != nil {
		t.Fatal(err)
	}
	if resumeAdvanced.PositionSeconds != 300 {
		t.Fatalf("strictly-ahead re-run position = %d, want 300 (advanced)", resumeAdvanced.PositionSeconds)
	}
}

func TestMigrationDryRunMatchesWithoutApplying(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"
	mediaID, fp := seedMediaAtPath(t, st, "/media/films/Preview.mkv", "fp-preview-0001", "Preview")
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState, migrate.CategoryRatings, migrate.CategoryCollections},
		items: []migrate.ForeignItem{{
			Path:       "/media/films/Preview.mkv",
			WatchState: migrate.WatchState{Present: true, Watched: true, OccurredAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
		}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState, migrate.CategoryRatings, migrate.CategoryCollections},
		DryRun:     true,
		ProfileID:  profileID,
		LibraryID:  "library-local",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "dry-run", time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("dry-run match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	for _, want := range []string{"watch-state=1", "ratings=not yet importable", "collections=not yet importable"} {
		if !strings.Contains(done.Progress.Message, want) {
			t.Fatalf("dry-run message = %q, want %q", done.Progress.Message, want)
		}
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, profileID, mediaID); err != nil || ok {
		t.Fatalf("dry-run progress write: ok=%v err=%v, want no write", ok, err)
	}
	if pinned, _, err := st.MatchPinned(ctx, fp); err != nil || pinned {
		t.Fatalf("dry-run match pin: pinned=%v err=%v, want not pinned", pinned, err)
	}
}

func TestMigrationSkipsUnresolvedUserInsteadOfDefaultingProfile(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	mediaID, _ := seedMediaAtPath(t, st, "/media/films/Unmapped.mkv", "fp-unmapped-user", "Unmapped")
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			Path:       "/media/films/Unmapped.mkv",
			WatchState: migrate.WatchState{Present: true, Watched: true, OccurredAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
		}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState},
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "unresolved-user", time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed skip: %s", done.Status, done.Error)
	}
	if done.Progress.Current != 0 || done.Progress.Total != 1 {
		t.Fatalf("unresolved-user match-rate = %d of %d, want 0 of 1", done.Progress.Current, done.Progress.Total)
	}
	if !strings.Contains(done.Progress.Message, "skipped-unresolved-user=1") {
		t.Fatalf("message = %q, want unresolved-user skip", done.Progress.Message)
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-default", mediaID); err != nil || ok {
		t.Fatalf("unresolved user default-profile write: ok=%v err=%v, want no write", ok, err)
	}
}

func TestMigrationMapsSourceUsersIndependentlyAndSkipsUnmappedUser(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	firstMediaID, _ := seedMediaAtPath(t, st, "/media/films/First.mkv", "fp-source-user-first", "First")
	secondMediaID, _ := seedMediaAtPath(t, st, "/media/films/Second.mkv", "fp-source-user-second", "Second")
	unmappedMediaID, _ := seedMediaAtPath(t, st, "/media/films/Unmapped-Source-User.mkv", "fp-source-user-unmapped", "Unmapped Source User")
	watched := migrate.WatchState{
		Present:    true,
		Watched:    true,
		OccurredAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	source := sourceUserItemsSource{
		fakeSource: fakeSource{categories: []string{migrate.CategoryWatchState}},
		itemsBySourceUser: map[string][]migrate.ForeignItem{
			"source-user-a": {{Path: "/media/films/First.mkv", WatchState: watched}},
			"source-user-b": {{Path: "/media/films/Second.mkv", WatchState: watched}},
			"source-user-c": {{Path: "/media/films/Unmapped-Source-User.mkv", WatchState: watched}},
		},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "fixture",
		Categories: []string{migrate.CategoryWatchState},
		ProfileID:  "profile-default",
		SourceUserProfileIDs: map[string]string{
			"source-user-a": "profile-a",
			"source-user-b": "profile-b",
		},
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "per-source-user", time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed: %s", done.Status, done.Error)
	}
	if done.Progress.Current != 2 || done.Progress.Total != 3 {
		t.Fatalf("per-source-user match-rate = %d of %d, want 2 of 3", done.Progress.Current, done.Progress.Total)
	}
	for _, want := range []string{
		"source-user-a=mapped(items=1)",
		"source-user-b=mapped(items=1)",
		"source-user-c=unmapped(items=1)",
		"skipped-unresolved-user=1",
	} {
		if !strings.Contains(done.Progress.Message, want) {
			t.Fatalf("message = %q, want %q", done.Progress.Message, want)
		}
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-a", firstMediaID); err != nil || !ok {
		t.Fatalf("first source user progress: ok=%v err=%v, want profile-a write", ok, err)
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-b", secondMediaID); err != nil || !ok {
		t.Fatalf("second source user progress: ok=%v err=%v, want profile-b write", ok, err)
	}
	for _, check := range []struct {
		profileID store.ProfileID
		mediaID   store.MediaID
	}{
		{profileID: "profile-default", mediaID: firstMediaID},
		{profileID: "profile-a", mediaID: secondMediaID},
		{profileID: "profile-b", mediaID: firstMediaID},
		{profileID: "profile-default", mediaID: unmappedMediaID},
	} {
		if _, ok, err := st.PlaybackProgressFor(ctx, check.profileID, check.mediaID); err != nil || ok {
			t.Fatalf("unexpected progress for profile %q media %q: ok=%v err=%v", check.profileID, check.mediaID, ok, err)
		}
	}
}

func TestMigrationSubscriptionImportCreatesFollowsAndIsRerunnable(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	source := fakeSource{
		categories: []string{migrate.CategorySubscriptions},
		items: []migrate.ForeignItem{{
			Subscription: migrate.Subscription{
				Present: true,
				FeedURL: "https://feeds.example.com/daily.xml",
				Title:   "Daily Show",
			},
		}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "podcasts",
		ImporterID: "opml",
		Categories: []string{migrate.CategorySubscriptions},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Root:       "/media/local/podcasts",
	}
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	done := enqueuePayloadAndRun(t, st, source, payload, "podcasts:opml:library-local", now)
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed: %s", done.Status, done.Error)
	}
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}
	subs, err := st.Subscriptions(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(subs))
	}
	if subs[0].FeedURL != "https://feeds.example.com/daily.xml" || subs[0].Root != "/media/local/podcasts" || subs[0].Title != "Daily Show" {
		t.Fatalf("subscription = %+v", subs[0])
	}
	followed, err := st.SubscriptionFollowed(ctx, "profile-default", subs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !followed {
		t.Fatal("subscription was created but not followed by importing profile")
	}

	done = enqueuePayloadAndRun(t, st, source, payload, "podcasts:opml:library-local:rerun", now.Add(time.Hour))
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("rerun status = %q, want completed: %s", done.Status, done.Error)
	}
	subs, err = st.Subscriptions(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("rerun subscriptions = %d, want still one idempotent row", len(subs))
	}
	followed, err = st.SubscriptionFollowed(ctx, "profile-default", subs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !followed {
		t.Fatal("rerun unfollowed subscription")
	}
}

// TestMigrationSmartCollectionImportDrivesStaticWarning proves the production
// increment site (METAPARITY-10-SEAM): a source that yields SmartCollection items
// with the collections category admitted drives result.SmartCollectionsStatic and
// surfaces the distinct smart-collection→static warning line end-to-end on the job
// progress report (ADR-0028 §E / ADR-0037 §2.2), separate from the match-rate
// number. Smart collections are counted on their own line, never folded into
// Matched/Unmatched, so the run's match-rate stays 0 of 0 with no match-rate items.
func TestMigrationSmartCollectionImportDrivesStaticWarning(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	source := fakeSource{
		categories: []string{migrate.CategoryCollections},
		items: []migrate.ForeignItem{
			{SmartCollection: migrate.SmartCollection{
				Present: true, Identity: "recently-added", Name: "Recently Added",
				CurrentMembersPresent: true, CurrentMembers: []migrate.ForeignItem{}, MemberCount: 0,
			}},
			{SmartCollection: migrate.SmartCollection{
				Present: true, Identity: "top-rated", Name: "Top Rated",
				CurrentMembersPresent: true, CurrentMembers: []migrate.ForeignItem{}, MemberCount: 0,
			}},
		},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryCollections},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
	}
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	done := enqueuePayloadAndRun(t, st, source, payload, "filmtv:plex:smartcoll", now)
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed: %s", done.Status, done.Error)
	}
	// Smart collections never count toward the match-rate number.
	if done.Progress.Current != 0 || done.Progress.Total != 0 {
		t.Fatalf("match-rate = %d of %d, want 0 of 0 (smart collections are not match-rate items)", done.Progress.Current, done.Progress.Total)
	}
	const wantWarning = "; 2 smart collections imported as static — they will not auto-update"
	if !strings.HasSuffix(done.Progress.Message, wantWarning) {
		t.Fatalf("report message = %q, want distinct trailing warning %q", done.Progress.Message, wantWarning)
	}
	// The warning count must not have been absorbed into the match-rate segment.
	if strings.Contains(strings.SplitN(done.Progress.Message, ";", 2)[0], "smart") {
		t.Fatalf("smart-collection warning folded into the match-rate segment: %q", done.Progress.Message)
	}
	collections, err := st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 2 {
		t.Fatalf("persisted static collections = %d, want 2", len(collections))
	}
	got := map[string]int{}
	for _, collection := range collections {
		got[collection.Name] = collection.MemberCount
	}
	for _, name := range []string{"Recently Added", "Top Rated"} {
		if memberCount, ok := got[name]; !ok || memberCount != 0 {
			t.Fatalf("persisted static collections = %#v, want named empty baselines", got)
		}
	}
}

func TestMigrationSmartCollectionPersistsExactOrderedMembersAndReplaysIdempotently(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	first, _ := seedMediaAtPath(t, st, "/media/local/first.mkv", "fp-smart-first", "First")
	second, _ := seedMediaAtPath(t, st, "/media/local/second.mkv", "fp-smart-second", "Second")
	third, _ := seedMediaAtPath(t, st, "/media/local/third.mkv", "fp-smart-third", "Third")
	source := fakeSource{
		categories: []string{migrate.CategoryCollections},
		items: []migrate.ForeignItem{{SmartCollection: migrate.SmartCollection{
			Present:               true,
			Identity:              "source-smart-42",
			Name:                  "Source Order",
			CurrentMembersPresent: true,
			CurrentMembers: []migrate.ForeignItem{
				{Path: "/media/local/third.mkv"},
				{Path: "/media/local/first.mkv"},
				{Path: "/media/local/second.mkv"},
			},
			MemberCount: 3,
		}}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "fixture",
		ImporterID: "ordered",
		Categories: []string{migrate.CategoryCollections},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	done := enqueuePayloadAndRun(t, st, source, payload, "fixture:ordered:first", now)
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed: %s", done.Status, done.Error)
	}
	if done.Progress.Current != 3 || done.Progress.Total != 3 {
		t.Fatalf("member match-rate = %d of %d, want 3 of 3", done.Progress.Current, done.Progress.Total)
	}
	for _, want := range []string{"collections=3", "1 smart collections imported as static — they will not auto-update"} {
		if !strings.Contains(done.Progress.Message, want) {
			t.Fatalf("message = %q, want %q", done.Progress.Message, want)
		}
	}

	collections, err := st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || collections[0].Name != "Source Order" || collections[0].MemberCount != 3 {
		t.Fatalf("collections = %#v, want one Source Order collection with 3 members", collections)
	}
	firstCollection := collections[0]
	members, err := st.ResolveCollectionMembers(ctx, collections[0].ID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []store.MediaID{third, first, second}
	for i, member := range members {
		if member.MediaID != wantOrder[i] || member.SortOrder != i {
			t.Fatalf("member %d = %#v, want media %q at order %d", i, member, wantOrder[i], i)
		}
	}
	firstMembers := append([]store.CollectionMember(nil), members...)

	// Stable source identity, not mutable display name, is the replay key.
	source.items[0].SmartCollection.Name = "Renamed at Source"
	replayed := enqueuePayloadAndRun(t, st, source, payload, "fixture:ordered:replay", now.Add(time.Hour))
	if replayed.Status != store.JobStatusCompleted {
		t.Fatalf("replay status = %q, want completed: %s", replayed.Status, replayed.Error)
	}
	collections, err = st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || collections[0] != firstCollection {
		t.Fatalf("replay collections = %#v, want unchanged %#v", collections, firstCollection)
	}
	replayedMembers, err := st.ResolveCollectionMembers(ctx, collections[0].ID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(replayedMembers) != len(firstMembers) {
		t.Fatalf("replay members = %#v, want unchanged %#v", replayedMembers, firstMembers)
	}
	for i := range firstMembers {
		if replayedMembers[i] != firstMembers[i] {
			t.Fatalf("replay member %d = %#v, want unchanged %#v", i, replayedMembers[i], firstMembers[i])
		}
	}
}

func TestMigrationSmartCollectionMemberUsesMetadataMatchWithoutApplyingPlay(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	track := seedTrack(t, st, "/music/metadata/member.flac", "fp-smart-metadata-member", "Member Track", "Member Artist", "Member Album")
	source := fakeSource{
		categories: []string{migrate.CategoryCollections},
		items: []migrate.ForeignItem{{SmartCollection: migrate.SmartCollection{
			Present:               true,
			Identity:              "source-smart-metadata",
			Name:                  "Metadata Members",
			CurrentMembersPresent: true,
			CurrentMembers: []migrate.ForeignItem{{Scrobble: migrate.Scrobble{
				Present: true,
				Artist:  "Member Artist",
				Track:   "Member Track",
				Album:   "Member Album",
			}}},
			MemberCount: 1,
		}}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "music",
		ImporterID: "metadata-collection",
		Categories: []string{migrate.CategoryCollections},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
	}
	selectors := migrate.WithScrobbleRelationSelectors(migrate.ScrobbleRelationSelectors{
		ArtistConfirm: []string{"appears-on"},
		AlbumTieBreak: []string{"appears-on"},
	})

	done := enqueuePayloadAndRun(t, st, source, payload, "music:metadata-collection", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC), selectors)
	if done.Status != store.JobStatusCompleted || done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("metadata member import = status %q, %d/%d, error %q", done.Status, done.Progress.Current, done.Progress.Total, done.Error)
	}
	if !strings.Contains(done.Progress.Message, "scrobble=1") {
		t.Fatalf("message = %q, want metadata match strategy", done.Progress.Message)
	}
	collections, err := st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 {
		t.Fatalf("collections = %#v, want one", collections)
	}
	members, err := st.ResolveCollectionMembers(ctx, collections[0].ID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].MediaID != track {
		t.Fatalf("members = %#v, want metadata-matched %q", members, track)
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-default", track); err != nil || ok {
		t.Fatalf("metadata identity applied a play: progress present=%v err=%v", ok, err)
	}
}

func TestMigrationSmartCollectionUnmatchedMemberSkipsWholeCollectionAndCountsIt(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	seedMediaAtPath(t, st, "/media/local/present.mkv", "fp-smart-present", "Present")
	source := fakeSource{
		categories: []string{migrate.CategoryCollections},
		items: []migrate.ForeignItem{{SmartCollection: migrate.SmartCollection{
			Present:               true,
			Identity:              "source-smart-incomplete",
			Name:                  "Incomplete",
			CurrentMembersPresent: true,
			CurrentMembers: []migrate.ForeignItem{
				{Path: "/media/local/present.mkv"},
				{Path: "/media/local/missing.mkv"},
				{},
			},
			MemberCount: 3,
		}}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "fixture",
		ImporterID: "incomplete",
		Categories: []string{migrate.CategoryCollections},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "fixture:incomplete", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed: %s", done.Status, done.Error)
	}
	if done.Progress.Current != 1 || done.Progress.Total != 3 {
		t.Fatalf("member match-rate = %d of %d, want 1 of 3", done.Progress.Current, done.Progress.Total)
	}
	for _, want := range []string{"collections=0", "unmatched=2"} {
		if !strings.Contains(done.Progress.Message, want) {
			t.Fatalf("message = %q, want truthful skipped-collection count %q", done.Progress.Message, want)
		}
	}
	if !strings.Contains(done.Progress.Message, "1 smart collections skipped — one or more current members were unmatched") {
		t.Fatalf("message = %q, want incomplete-collection policy", done.Progress.Message)
	}
	collections, err := st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 0 {
		t.Fatalf("partial collection persisted: %#v", collections)
	}
	records, err := st.UnmatchedRecords(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("unmatched records = %#v, want missing-path and identity-less members", records)
	}
	handles := map[string]bool{}
	for _, record := range records {
		handles[record.SourceHandle] = true
	}
	if !handles["/media/local/missing.mkv"] || !handles["collection:source-smart-incomplete member:2"] {
		t.Fatalf("unmatched handles = %#v, want missing path and explicit empty identity", handles)
	}
}

func TestMigrationSmartCollectionDryRunMatchesApplyReportAndWritesNothing(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	seedMediaAtPath(t, st, "/media/local/preview.mkv", "fp-smart-preview", "Preview")
	source := fakeSource{
		categories: []string{migrate.CategoryCollections},
		items: []migrate.ForeignItem{
			{SmartCollection: migrate.SmartCollection{
				Present:               true,
				Identity:              "source-smart-preview",
				Name:                  "Preview",
				CurrentMembersPresent: true,
				CurrentMembers:        []migrate.ForeignItem{{Path: "/media/local/preview.mkv"}},
				MemberCount:           1,
			}},
			{SmartCollection: migrate.SmartCollection{
				Present:               true,
				Identity:              "source-smart-incomplete-preview",
				Name:                  "Incomplete Preview",
				CurrentMembersPresent: true,
				CurrentMembers: []migrate.ForeignItem{
					{Path: "/media/local/preview.mkv"},
					{Path: "/media/local/missing-preview.mkv"},
				},
				MemberCount: 2,
			}},
		},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "fixture",
		ImporterID: "preview",
		Categories: []string{migrate.CategoryCollections},
		DryRun:     true,
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "fixture:preview", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if done.Status != store.JobStatusCompleted || done.Progress.Current != 2 || done.Progress.Total != 3 {
		t.Fatalf("dry-run = status %q, %d/%d, error %q", done.Status, done.Progress.Current, done.Progress.Total, done.Error)
	}
	for _, want := range []string{
		"collections=1",
		"unmatched=1",
		"1 smart collections imported as static — they will not auto-update",
		"1 smart collections skipped — one or more current members were unmatched",
	} {
		if !strings.Contains(done.Progress.Message, want) {
			t.Fatalf("dry-run message = %q, want %q", done.Progress.Message, want)
		}
	}
	collections, err := st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 0 {
		t.Fatalf("dry-run collections = %#v, want none", collections)
	}
	records, err := st.UnmatchedRecords(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("dry-run unmatched records = %#v, want none", records)
	}

	applyPayload := payload
	applyPayload.DryRun = false
	applied := enqueuePayloadAndRun(t, st, source, applyPayload, "fixture:preview:apply", time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC))
	if applied.Status != store.JobStatusCompleted {
		t.Fatalf("apply status = %q, want completed: %s", applied.Status, applied.Error)
	}
	if applied.Progress.Current != done.Progress.Current || applied.Progress.Total != done.Progress.Total {
		t.Fatalf("apply match-rate = %d/%d, want dry-run parity %d/%d", applied.Progress.Current, applied.Progress.Total, done.Progress.Current, done.Progress.Total)
	}
	previewReport := strings.SplitN(done.Progress.Message, "; items:", 2)[0]
	if applied.Progress.Message != previewReport {
		t.Fatalf("apply report = %q, want dry-run report without preview disposition %q", applied.Progress.Message, previewReport)
	}
	collections, err = st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || collections[0].Name != "Preview" || collections[0].MemberCount != 1 {
		t.Fatalf("apply collections = %#v, want only the complete one-member snapshot", collections)
	}
	records, err = st.UnmatchedRecords(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].SourceHandle != "/media/local/missing-preview.mkv" {
		t.Fatalf("apply unmatched records = %#v, want the one missing member", records)
	}
}

func TestMigrationSmartCollectionCountOnlyInputCannotSucceed(t *testing.T) {
	st := storetest.Open(t)
	source := fakeSource{
		categories: []string{migrate.CategoryCollections},
		items: []migrate.ForeignItem{{SmartCollection: migrate.SmartCollection{
			Present: true, Identity: "source-smart-count-only", Name: "Count Only", MemberCount: 2,
		}}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "fixture",
		ImporterID: "count-only",
		Categories: []string{migrate.CategoryCollections},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "fixture:count-only", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if done.Status != store.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", done.Status)
	}
	if !strings.Contains(done.Error, "ordered current-member identities are required") {
		t.Fatalf("job error = %q, want count-only rejection", done.Error)
	}
}

// TestMigrationSmartCollectionSkippedWhenCollectionsNotAdmitted proves a smart
// collection is skipped entirely — no increment, no warning line — when the run
// does not admit the collections category, mirroring the other category-gated
// item shapes.
func TestMigrationSmartCollectionSkippedWhenCollectionsNotAdmitted(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{
			{SmartCollection: migrate.SmartCollection{Present: true, Name: "Recently Added", MemberCount: 42}},
		},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
	}
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	done := enqueuePayloadAndRun(t, st, source, payload, "filmtv:plex:smartcoll-skip", now)
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed: %s", done.Status, done.Error)
	}
	if strings.Contains(done.Progress.Message, "smart collections imported as static") {
		t.Fatalf("report message = %q, want no smart-collection warning (collections not admitted)", done.Progress.Message)
	}
	collections, err := st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 0 {
		t.Fatalf("category-filtered smart collection reached store: %#v", collections)
	}
}

func TestMigrationDryRunSubscriptionDoesNotWrite(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	source := fakeSource{
		categories: []string{migrate.CategorySubscriptions},
		items: []migrate.ForeignItem{{
			Subscription: migrate.Subscription{
				Present: true,
				FeedURL: "https://feeds.example.com/preview.xml",
				Title:   "Preview Feed",
			},
		}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "podcasts",
		ImporterID: "opml",
		Categories: []string{migrate.CategorySubscriptions},
		DryRun:     true,
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Root:       "/media/local/podcasts",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "podcasts:opml:dry-run", time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC))
	if done.Progress.Current != 1 || done.Progress.Total != 1 || !strings.Contains(done.Progress.Message, "subscriptions=1") {
		t.Fatalf("dry-run subscription report = %d/%d %q, want preview count", done.Progress.Current, done.Progress.Total, done.Progress.Message)
	}
	subs, err := st.Subscriptions(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Fatalf("dry-run subscriptions = %d, want none", len(subs))
	}
}

func TestMigrationItemStateImportReplaysFeedScopedState(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/media/local/podcasts/state.mp3",
		Title:               "Episode",
		Container:           "mp3",
		Codec:               "mp3",
		Class:               store.MediaClassAudio,
		ContentFingerprint:  "fp-podcast-state-0001",
		DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatalf("seed podcast media: %v", err)
	}
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/state.xml",
		Root:      "/media/local/podcasts",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "guid-state", "/media/local/podcasts/state.mp3", now); err != nil {
		t.Fatal(err)
	}

	source := fakeSource{
		categories: []string{migrate.CategoryItemState},
		items: []migrate.ForeignItem{{
			ItemState: migrate.ItemState{
				Present: true, FeedURL: "https://feeds.example.com/state.xml", GUID: "guid-state",
				HasProgress: true, Progress: 0.5, Starred: true,
			},
		}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "podcasts",
		ImporterID: "overcast",
		Categories: []string{migrate.CategoryItemState},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Root:       "/media/local/podcasts",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "podcasts:state:library-local", now)
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed: %s", done.Status, done.Error)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("PlaybackProgressFor: ok=%v err=%v", ok, err)
	}
	if progress.PositionSeconds != 60 || progress.DurationSeconds != 120 || progress.Provenance != "import:podcasts" {
		t.Fatalf("progress = %+v, want 60/120 import:podcasts", progress)
	}
	// Run-scoped import provenance: item-state is stamped with the
	// run's job ID + importer id + the "feed-guid" match method, so a
	// per-run audit / re-run skip-explanation can attribute this row the same
	// way MIGREV-7 does for watch-state.
	if progress.ImportRunID != string(done.ID) || progress.ImportImporterID != "overcast" || progress.ImportMatchMethod != "feed-guid" {
		t.Fatalf("import provenance = {run=%q importer=%q method=%q}, want {%q overcast feed-guid}",
			progress.ImportRunID, progress.ImportImporterID, progress.ImportMatchMethod, done.ID)
	}
	favorite, err := st.MediaFavorited(ctx, "profile-default", media.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !favorite {
		t.Fatal("starred item-state was not applied as favorite")
	}
}

func TestMigrationItemStateImportPendingReplayAfterSeenEntry(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/media/local/podcasts/pending.mp3",
		Title:               "Pending Episode",
		Container:           "mp3",
		Codec:               "mp3",
		Class:               store.MediaClassAudio,
		ContentFingerprint:  "fp-podcast-pending-0001",
		DurationSecondsHint: 200,
	})
	if err != nil {
		t.Fatalf("seed pending podcast media: %v", err)
	}

	source := fakeSource{
		categories: []string{migrate.CategoryItemState},
		items: []migrate.ForeignItem{{
			ItemState: migrate.ItemState{
				Present: true, FeedURL: "https://feeds.example.com/pending.xml", GUID: "guid-pending",
				HasProgress: true, Progress: 0.25,
			},
		}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "podcasts",
		ImporterID: "overcast",
		Categories: []string{migrate.CategoryItemState},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Root:       "/media/local/podcasts",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "podcasts:state:pending:library-local", now)
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed: %s", done.Status, done.Error)
	}
	if progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", media.ID); err != nil || ok {
		t.Fatalf("pending item-state applied before seen-entry reconciliation: progress=%+v ok=%v err=%v", progress, ok, err)
	}
	subs, err := st.Subscriptions(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(subs))
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, subs[0].ID, "guid-pending", "/media/local/podcasts/pending.mp3", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplayItemStateImports(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok {
		t.Fatalf("PlaybackProgressFor after replay: ok=%v err=%v", ok, err)
	}
	if progress.PositionSeconds != 50 || progress.DurationSeconds != 200 || progress.Provenance != "import:podcasts" {
		t.Fatalf("progress = %+v, want 50/200 import:podcasts", progress)
	}
}

func TestMigrationItemStateRequiresTargetLibrary(t *testing.T) {
	st := storetest.Open(t)
	source := fakeSource{
		categories: []string{migrate.CategoryItemState},
		items: []migrate.ForeignItem{{
			ItemState: migrate.ItemState{Present: true, FeedURL: "https://feeds.example.com/state.xml", GUID: "guid-state"},
		}},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "podcasts",
		ImporterID: "overcast",
		Categories: []string{migrate.CategoryItemState},
		ProfileID:  "profile-default",
	}
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: source}))
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "podcasts:state:missing-library",
		Payload:   encoded,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RunOne(context.Background(), nil, time.Now().UTC()); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusFailed || done.Error != "migrate: payload is missing the target library" {
		t.Fatalf("job = status %q error %q, want missing target library failure", done.Status, done.Error)
	}
}

func TestMigrationCollectionsRequireTargetLibraryBeforeDurableWrite(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	mediaID, _ := seedMediaAtPath(t, st, "/media/films/Before-Collection.mkv", "fp-before-collection", "Before Collection")
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState, migrate.CategoryCollections},
		items: []migrate.ForeignItem{
			{
				Path: "/media/films/Before-Collection.mkv",
				WatchState: migrate.WatchState{
					Present: true,
					Watched: true,
				},
			},
			{
				SmartCollection: migrate.SmartCollection{
					Present:               true,
					Identity:              "missing-library",
					Name:                  "Missing Library",
					CurrentMembersPresent: true,
				},
			},
		},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "fixture",
		Categories: []string{migrate.CategoryWatchState, migrate.CategoryCollections},
		ProfileID:  "profile-default",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "collections:missing-library", time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
	if done.Status != store.JobStatusFailed || done.Error != "migrate: payload is missing the target library" {
		t.Fatalf("job = status %q error %q, want missing target library failure", done.Status, done.Error)
	}
	if progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", mediaID); err != nil || ok {
		t.Fatalf("playback progress written before target-library validation: progress=%+v ok=%v err=%v", progress, ok, err)
	}
}

func TestMigrationImportUsesMatchedMediaClassForResumePolicy(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	audioID, audioFP := seedMediaAtPathWithClass(t, st, "/media/music/Track.flac", "fp-audio-0001", "Track", store.MediaClassAudio)

	historical := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			Path: "/media/music/Track.flac",
			WatchState: migrate.WatchState{
				Present: true, PositionSeconds: 120, DurationSeconds: 600, OccurredAt: historical,
			},
		}},
	}

	done := enqueueAndRun(t, st, source, profileID, []string{migrate.CategoryWatchState}, time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC))
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed", done.Status)
	}
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}

	progress, ok, err := st.PlaybackProgressFor(ctx, profileID, audioID)
	if err != nil || !ok {
		t.Fatalf("audio progress: ok=%v err=%v", ok, err)
	}
	if progress.PositionSeconds != 0 {
		t.Fatalf("audio import stored resume position %d, want 0", progress.PositionSeconds)
	}
	if progress.Watched {
		t.Fatal("audio import below played threshold was marked watched")
	}
	if progress.Provenance != "import:filmtv" {
		t.Fatalf("provenance = %q, want import:filmtv", progress.Provenance)
	}
	if !progress.OccurredAt.Equal(historical) {
		t.Fatalf("OccurredAt = %v, want historical %v", progress.OccurredAt, historical)
	}
	pinned, found, err := st.MatchPinned(ctx, audioFP)
	if err != nil || !found {
		t.Fatalf("match-pin read %q: found=%v err=%v", audioFP, found, err)
	}
	if !pinned {
		t.Fatalf("matched audio file %q not pinned", audioFP)
	}
}

func TestMigrationImportResumePolicyResolverCanOverrideAudioDefault(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	audioID, _ := seedMediaAtPathWithClass(t, st, "/media/podcasts/Episode.mp3", "fp-audio-override-0001", "Episode", store.MediaClassAudio)

	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			Path: "/media/podcasts/Episode.mp3",
			WatchState: migrate.WatchState{
				Present: true, PositionSeconds: 120, DurationSeconds: 600, OccurredAt: time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC),
			},
		}},
	}
	resume := true

	enqueueAndRunWithOptions(t, st, source, profileID, []string{migrate.CategoryWatchState}, time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		migrate.WithResumePolicyResolver(fakeResumePolicyResolver{policy: store.ResumePolicy{Class: store.MediaClassAudio, Override: &resume}}),
	)

	progress, ok, err := st.PlaybackProgressFor(ctx, profileID, audioID)
	if err != nil || !ok {
		t.Fatalf("audio progress: ok=%v err=%v", ok, err)
	}
	if progress.PositionSeconds != 120 {
		t.Fatalf("audio override import stored resume position %d, want 120", progress.PositionSeconds)
	}
	if progress.Watched {
		t.Fatal("audio override import below played threshold was marked watched")
	}
}

// TestMigrationUnmatchedRoutesToReviewInbox: an
// import item the match chain cannot resolve is no longer only counted — it is
// persisted as a Media-independent Unmatched Record so the operator can Fix Match
// it. A dry-run preview persists nothing.
func TestMigrationUnmatchedRoutesToReviewInbox(t *testing.T) {
	ctx := context.Background()
	opsFilter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	unmatchedItem := migrate.ForeignItem{
		Path:       "/media/films/Unknown.mkv",
		WatchState: migrate.WatchState{Present: true, Watched: true, OccurredAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
	}

	// Live run: the unmatched item lands in the review inbox with import origin,
	// its source handle, and a no-candidates failure reason.
	st := storetest.Open(t)
	source := fakeSource{categories: []string{migrate.CategoryWatchState}, items: []migrate.ForeignItem{unmatchedItem}}
	done := enqueueAndRun(t, st, source, "profile-default", []string{migrate.CategoryWatchState}, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 0 || done.Progress.Total != 1 {
		t.Fatalf("unmatched match-rate = %d of %d, want 0 of 1", done.Progress.Current, done.Progress.Total)
	}
	records, err := st.UnmatchedRecords(ctx, opsFilter)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("UnmatchedRecords = %d, want 1 (the unmatched import routed to the inbox)", len(records))
	}
	rec := records[0]
	if rec.Origin != store.UnmatchedOriginImport || rec.SourceHandle != "/media/films/Unknown.mkv" {
		t.Fatalf("import unmatched record = %#v, want import origin + source path", rec)
	}
	if rec.FailureReason != store.UnmatchedReasonNoCandidates || rec.ImporterID != "plex" {
		t.Fatalf("import unmatched record lost reason/importer: %#v", rec)
	}

	// Dry-run: an unmatched preview must persist no record.
	dryStore := storetest.Open(t)
	drySource := fakeSource{categories: []string{migrate.CategoryWatchState}, items: []migrate.ForeignItem{unmatchedItem}}
	dryPayload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState},
		DryRun:     true,
		ProfileID:  "profile-default",
	}
	enqueuePayloadAndRun(t, dryStore, drySource, dryPayload, "dry-unmatched", time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	dryRecords, err := dryStore.UnmatchedRecords(ctx, opsFilter)
	if err != nil {
		t.Fatal(err)
	}
	if len(dryRecords) != 0 {
		t.Fatalf("dry-run persisted %d unmatched records, want 0", len(dryRecords))
	}
}

package migrate_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// seedTrack imports an audio Media (a music Track) titled track and links the
// given artist (and album) as Entities related to it under the music
// `appears-on` relation, so the scrobble metadata match chain can resolve it.
// Returns the Media ID.
func seedTrack(t *testing.T, st *store.Store, path, fingerprint, track, artist, album string) store.MediaID {
	t.Helper()
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              track,
		Container:          "flac",
		Codec:              "flac",
		Class:              store.MediaClassAudio,
		ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("seed track %q: %v", track, err)
	}
	linkEntity(t, st, "entity-"+fingerprint+"-artist", artist, media.ID)
	if album != "" {
		linkEntity(t, st, "entity-"+fingerprint+"-album", album, media.ID)
	}
	return media.ID
}

// linkEntity creates an Entity titled title and relates it to mediaID under the
// music `appears-on` relation (the relation name the scrobble match chain
// confirms artist/album against).
func linkEntity(t *testing.T, st *store.Store, id, title string, mediaID store.MediaID) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, store.Entity{ID: store.EntityID(id), LibraryID: "library-local", Kind: "artist", Title: title}); err != nil {
		t.Fatalf("seed entity %q: %v", title, err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: store.EntityID(id),
		Name:         "appears-on",
		ToMediaID:    mediaID,
	}); err != nil {
		t.Fatalf("relate entity %q: %v", title, err)
	}
}

// enqueueAndRunScrobble enqueues a music (Last.fm) import run for source and
// runs it once through the real jobs registry, returning the completed Job. It
// uses the music plugin id so the apply path stamps "import:music" provenance.
func enqueueAndRunScrobble(t *testing.T, st *store.Store, source migrate.Source, profileID string, now time.Time) store.Job {
	t.Helper()
	registry := jobs.NewRegistry(st, "test-worker")
	// The scrobble match chain reads plugin-owned relation slugs (ADR-0098 area
	// 1). The composition root supplies the Music plugin's; the test mirrors it
	// with the bare relation names the seeded entities are linked under.
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: source},
		migrate.WithScrobbleRelationSelectors(migrate.ScrobbleRelationSelectors{
			ArtistConfirm: []string{"appears-on"},
			AlbumTieBreak: []string{"appears-on"},
		})))

	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   "music",
		ImporterID: "lastfm",
		Categories: []string{migrate.CategoryWatchState},
		ProfileID:  profileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "music:lastfm",
		Payload:   payload,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("enqueue scrobble job: %v", err)
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

// TestMigrationScrobbleImportMatchesByMetadataAppliesPlaysAndIsRerunnable is the
// MIG-4 vertical slice: the Last.fm scrobble importer. It seeds
// two music Tracks, runs an import whose source yields five scrobbles — three
// for one track, one for another, one for an unknown track — and asserts:
//   - matched scrobbles resolve by metadata (track title + artist), not path;
//   - the play count is the per-Media running total (3 and 1), last-played is
//     the latest scrobble instant, written with import provenance;
//   - the unknown-track scrobble is reported unmatched (no Media created);
//   - a re-run does NOT double-apply (strictly additive, ADR-0037 §2.4): the
//     play count stays at the source total.
func TestMigrationScrobbleImportMatchesByMetadataAppliesPlaysAndIsRerunnable(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	roygbiv := seedTrack(t, st, "/music/boc/roygbiv.flac", "fp-roygbiv-0001", "Roygbiv", "Boards of Canada", "Music Has the Right to Children")
	xtal := seedTrack(t, st, "/music/aphex/xtal.flac", "fp-xtal-0002", "Xtal", "Aphex Twin", "Selected Ambient Works 85-92")

	t1 := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t1.Add(2 * time.Hour) // latest play of Roygbiv
	source := scrobbleSource{
		scrobbles: []migrate.Scrobble{
			{Present: true, Artist: "Boards of Canada", Track: "Roygbiv", Album: "Music Has the Right to Children", PlayedAt: t1},
			{Present: true, Artist: "Boards of Canada", Track: "Roygbiv", PlayedAt: t3},
			{Present: true, Artist: "Boards of Canada", Track: "Roygbiv", PlayedAt: t2},
			{Present: true, Artist: "Aphex Twin", Track: "Xtal", PlayedAt: t1},
			// No Media for this artist/track: reported unmatched, never created.
			{Present: true, Artist: "Someone Else", Track: "Unknown Song", PlayedAt: t1},
		},
	}

	// now is the run instant the job carries; it is in the past relative to the
	// real wall clock the store stamps updated_at_ms with, mirroring the sibling
	// watch-state test, so the recency-key assertion (UpdatedAt >= now) is sound.
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	done := enqueueAndRunScrobble(t, st, source, profileID, now)

	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed", done.Status)
	}
	// Match-rate report: 4 of 5 scrobbles matched (one unknown track).
	if done.Progress.Current != 4 || done.Progress.Total != 5 {
		t.Fatalf("match-rate = %d of %d, want 4 of 5", done.Progress.Current, done.Progress.Total)
	}

	// Roygbiv: three plays, last-played the latest scrobble (t3), import
	// provenance, recency key NOT backdated.
	roy, ok, err := st.PlaybackProgressFor(ctx, profileID, roygbiv)
	if err != nil || !ok {
		t.Fatalf("roygbiv progress: ok=%v err=%v", ok, err)
	}
	if roy.PlayCount != 3 {
		t.Fatalf("roygbiv play count = %d, want 3", roy.PlayCount)
	}
	if roy.Provenance != "import:music" {
		t.Fatalf("roygbiv provenance = %q, want import:music", roy.Provenance)
	}
	// Run-scoped import provenance: a metadata-resolved scrobble is
	// stamped "scrobble" with the run's job ID + importer id.
	if roy.ImportRunID != string(done.ID) || roy.ImportImporterID != "lastfm" || roy.ImportMatchMethod != "scrobble" {
		t.Fatalf("roygbiv import provenance = {run=%q importer=%q method=%q}, want {%q lastfm scrobble}",
			roy.ImportRunID, roy.ImportImporterID, roy.ImportMatchMethod, done.ID)
	}
	if !roy.OccurredAt.Equal(t3) {
		t.Fatalf("roygbiv last-played OccurredAt = %v, want latest scrobble %v", roy.OccurredAt, t3)
	}
	if roy.UpdatedAt.Before(now) {
		t.Fatalf("roygbiv UpdatedAt = %v, want >= import instant %v (recency key not backdated)", roy.UpdatedAt, now)
	}
	// A scrobble is a play event, not a resume — no resume position, not watched.
	if roy.PositionSeconds != 0 || roy.Watched {
		t.Fatalf("roygbiv = {pos=%d watched=%v}, want {0 false} (play event, not resume)", roy.PositionSeconds, roy.Watched)
	}

	// Xtal: one play.
	xt, ok, err := st.PlaybackProgressFor(ctx, profileID, xtal)
	if err != nil || !ok {
		t.Fatalf("xtal progress: ok=%v err=%v", ok, err)
	}
	if xt.PlayCount != 1 {
		t.Fatalf("xtal play count = %d, want 1", xt.PlayCount)
	}

	// Re-run the same export: strictly additive, no double-apply.
	done2 := enqueueAndRunScrobble(t, st, source, profileID, now.Add(time.Hour))
	if done2.Progress.Current != 4 || done2.Progress.Total != 5 {
		t.Fatalf("re-run match-rate = %d of %d, want 4 of 5", done2.Progress.Current, done2.Progress.Total)
	}
	royAfter, _, err := st.PlaybackProgressFor(ctx, profileID, roygbiv)
	if err != nil {
		t.Fatal(err)
	}
	if royAfter.PlayCount != 3 {
		t.Fatalf("re-run double-applied: roygbiv play count %d, want 3 (additive)", royAfter.PlayCount)
	}
}

// TestMigrationScrobbleAmbiguousArtistIsUnmatched confirms the match chain does
// not guess: a scrobble whose track title hits two Media of DIFFERENT artists,
// with no album tiebreaker, is reported unmatched rather than applied to the
// wrong Media (ADR-0037 §2.3 step 4 — the handler never guesses).
func TestMigrationScrobbleAmbiguousArtistIsUnmatched(t *testing.T) {
	st := storetest.Open(t)
	const profileID = "profile-default"

	// Two different artists, same track title, no album to disambiguate.
	seedTrack(t, st, "/music/a/intro.flac", "fp-intro-a", "Intro", "Artist A", "")
	seedTrack(t, st, "/music/b/intro.flac", "fp-intro-b", "Intro", "Artist B", "")

	source := scrobbleSource{scrobbles: []migrate.Scrobble{
		{Present: true, Artist: "Artist A", Track: "Intro", PlayedAt: time.Now()},
	}}
	// Only Artist A's Media should match (artist confirms exactly one), so this
	// is matched, not ambiguous — assert it resolves to exactly one.
	done := enqueueAndRunScrobble(t, st, source, profileID, time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 1 || done.Progress.Total != 1 {
		t.Fatalf("artist-disambiguated match-rate = %d of %d, want 1 of 1", done.Progress.Current, done.Progress.Total)
	}

	// Now a scrobble whose artist matches BOTH (seed a second Media under the
	// same artist + same title, no album) must stay unmatched — ambiguous.
	seedTrack(t, st, "/music/a/intro-alt.flac", "fp-intro-a2", "Intro", "Artist A", "")
	ambiguous := scrobbleSource{scrobbles: []migrate.Scrobble{
		{Present: true, Artist: "Artist A", Track: "Intro", PlayedAt: time.Now()},
	}}
	done2 := enqueueAndRunScrobble(t, st, ambiguous, profileID, time.Date(2026, 6, 16, 1, 0, 0, 0, time.UTC))
	if done2.Progress.Current != 0 || done2.Progress.Total != 1 {
		t.Fatalf("ambiguous match-rate = %d of %d, want 0 of 1 (no guess)", done2.Progress.Current, done2.Progress.Total)
	}
}

// TestMigrationScrobbleSelectorsReadIndependentLists proves the artist-confirm
// and album-tiebreak chains read their OWN plugin-owned relation lists (ADR-0098
// area 1) — a regression guard for the prior bug where the album tiebreaker
// reused the artist relation slice. Two tracks share a title + artist (both
// confirm by `appears-on`); only the album disambiguates, and the album entity
// is linked under a DIFFERENT relation (`album-track`). The tiebreak resolves
// ONLY because AlbumTieBreak names `album-track`, not `appears-on`.
func TestMigrationScrobbleSelectorsReadIndependentLists(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	// Two tracks, same title + artist (artist alone is ambiguous).
	a := seedTrack(t, st, "/m/a/song.flac", "fp-sel-a", "Song", "The Band", "")
	seedTrack(t, st, "/m/b/song.flac", "fp-sel-b", "Song", "The Band", "")

	// Link an Album entity to track A under the album-track relation only (NOT
	// appears-on), titled the scrobble's album.
	if err := st.UpsertEntity(ctx, store.Entity{ID: "entity-sel-album", LibraryID: "library-local", Kind: "album", Title: "The Album"}); err != nil {
		t.Fatalf("seed album: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "entity-sel-album", Name: "album-track", ToMediaID: a, Cardinality: "one"}); err != nil {
		t.Fatalf("relate album: %v", err)
	}

	registry := jobs.NewRegistry(st, "test-worker")
	source := scrobbleSource{scrobbles: []migrate.Scrobble{
		{Present: true, Artist: "The Band", Track: "Song", Album: "The Album", PlayedAt: time.Now()},
	}}
	// ArtistConfirm = appears-on; AlbumTieBreak = album-track (distinct lists).
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: source},
		migrate.WithScrobbleRelationSelectors(migrate.ScrobbleRelationSelectors{
			ArtistConfirm: []string{"appears-on"},
			AlbumTieBreak: []string{"album-track"},
		})))
	payload, _ := json.Marshal(migrate.Payload{Version: 1, PluginID: "music", ImporterID: "lastfm", Categories: []string{migrate.CategoryWatchState}, ProfileID: profileID})
	now := time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)
	if _, err := st.EnqueueJob(ctx, store.Job{Kind: migrate.JobKind, Class: store.JobClassBackground, DedupeKey: "music:lastfm", Payload: payload, CreatedAt: now}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	// Track A (the one carrying the album-track edge) got the play; the album
	// tiebreak resolved via the AlbumTieBreak list, proving it is read separately.
	progress, ok, err := st.PlaybackProgressFor(ctx, profileID, a)
	if err != nil || !ok || progress.PlayCount != 1 {
		t.Fatalf("track A progress = ok %v play %d err %v, want one play (album tiebreak via album-track)", ok, progress.PlayCount, err)
	}
}

func TestMigrationScrobbleImportMatchesByExternalID(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	mediaID := seedTrack(t, st, "/music/boc/roygbiv.flac", "fp-roygbiv-mbid", "Roygbiv", "Boards of Canada", "")
	if err := st.AddMediaExternalID(ctx, mediaID, "musicbrainz.recording", "mbid-roygbiv"); err != nil {
		t.Fatalf("seed recording id: %v", err)
	}
	t1 := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	source := foreignItemSource{items: []migrate.ForeignItem{
		{ExternalIDs: []migrate.ExternalID{{Scheme: "musicbrainz.recording", Value: "mbid-roygbiv"}}, Scrobble: migrate.Scrobble{Present: true, Artist: "Other", Track: "Unhelpful", PlayedAt: t1}},
		{ExternalIDs: []migrate.ExternalID{{Scheme: "musicbrainz.recording", Value: "mbid-roygbiv"}}, Scrobble: migrate.Scrobble{Present: true, Artist: "Other", Track: "Unhelpful", PlayedAt: t2}},
	}}
	done := enqueueAndRunScrobble(t, st, source, profileID, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 2 || done.Progress.Total != 2 {
		t.Fatalf("external-ID scrobble match-rate = %d of %d, want 2 of 2", done.Progress.Current, done.Progress.Total)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, profileID, mediaID)
	if err != nil || !ok {
		t.Fatalf("progress: ok=%v err=%v", ok, err)
	}
	if progress.PlayCount != 2 || !progress.OccurredAt.Equal(t2) {
		t.Fatalf("progress = play_count %d occurred %v, want 2/%v", progress.PlayCount, progress.OccurredAt, t2)
	}
	// Run-scoped import provenance: a scrobble item resolved
	// by the delegated external-ID branch is stamped "external-id", NOT "scrobble"
	// — the persisted match method reflects the real producing branch.
	if progress.ImportImporterID != "lastfm" || progress.ImportMatchMethod != "external-id" {
		t.Fatalf("import provenance = {importer=%q method=%q}, want {lastfm external-id}",
			progress.ImportImporterID, progress.ImportMatchMethod)
	}
}

func TestMigrationScrobbleDuplicateExternalIDDoesNotFallbackToMetadata(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"
	first := seedTrack(t, st, "/music/a/roygbiv.flac", "fp-roygbiv-a", "Roygbiv", "Boards of Canada", "")
	second := seedTrack(t, st, "/music/b/roygbiv.flac", "fp-roygbiv-b", "Roygbiv", "Boards of Canada", "")
	if err := st.AddMediaExternalID(ctx, first, "musicbrainz.recording", "duplicate-mbid"); err != nil {
		t.Fatalf("seed first recording id: %v", err)
	}
	if err := st.AddMediaExternalID(ctx, second, "musicbrainz.recording", "duplicate-mbid"); err != nil {
		t.Fatalf("seed second recording id: %v", err)
	}
	source := foreignItemSource{items: []migrate.ForeignItem{{
		ExternalIDs: []migrate.ExternalID{{Scheme: "musicbrainz.recording", Value: "duplicate-mbid"}},
		Scrobble:    migrate.Scrobble{Present: true, Artist: "Boards of Canada", Track: "Roygbiv", PlayedAt: time.Now()},
	}}}
	done := enqueueAndRunScrobble(t, st, source, profileID, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	if done.Progress.Current != 0 || done.Progress.Total != 1 {
		t.Fatalf("duplicate external-ID scrobble match-rate = %d of %d, want 0 of 1", done.Progress.Current, done.Progress.Total)
	}
}

// scrobbleSource is a test migrate.Source yielding metadata-keyed scrobbles
// (the real internal/musicimport Last.fm parser produces the same shape).
type scrobbleSource struct {
	scrobbles []migrate.Scrobble
}

func (scrobbleSource) Categories() []string { return []string{migrate.CategoryWatchState} }
func (s scrobbleSource) Items(context.Context) ([]migrate.ForeignItem, error) {
	items := make([]migrate.ForeignItem, 0, len(s.scrobbles))
	for _, sc := range s.scrobbles {
		items = append(items, migrate.ForeignItem{Scrobble: sc})
	}
	return items, nil
}

type foreignItemSource struct {
	items []migrate.ForeignItem
}

func (foreignItemSource) Categories() []string { return []string{migrate.CategoryWatchState} }
func (s foreignItemSource) Items(context.Context) ([]migrate.ForeignItem, error) {
	return s.items, nil
}

package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAudioMediaNeverStoresResumePosition(t *testing.T) {
	st := openSeeded(t)
	progress := PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 45,
		DurationSeconds: 120,
	}

	// Audio class: declarative no-resume — position never lands in the row.
	if err := st.UpsertPlaybackProgress(context.Background(), progress, false, ResumePolicy{Class: MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if stored.PositionSeconds != 0 {
		t.Fatalf("audio position = %d, want 0 (never resumes)", stored.PositionSeconds)
	}
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	items, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "continue-watching", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("continue-watching shows audio media: %#v", items)
	}

	// Video class with the same numbers resumes normally.
	if err := st.UpsertPlaybackProgress(context.Background(), progress, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	stored, _, err = st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if stored.PositionSeconds != 45 {
		t.Fatalf("video position = %d, want 45", stored.PositionSeconds)
	}
}

// TestPlaybackEventTimeGuardsPositionAgainstStaleReplay is ABPARITY-1:
// a late-arriving report whose EventTimeMS predates the row's
// last-applied event-time must not rewind position_seconds — the cross-device
// resume regression (offline device flushes stale progress over a newer
// position from another device).
func TestPlaybackEventTimeGuardsPositionAgainstStaleReplay(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	policy := ResumePolicy{Class: MediaClassVideo}

	// Device A reports position 100 at event-time 1000 (newest so far).
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha",
		PositionSeconds: 100, DurationSeconds: 1000, EventTimeMS: 1000,
	}, false, policy); err != nil {
		t.Fatal(err)
	}

	// Device B, offline since event-time 400, flushes late: position 30 at
	// event-time 400 — OLDER than the row's stored event-time. Must be a no-op.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha",
		PositionSeconds: 30, DurationSeconds: 1000, EventTimeMS: 400,
	}, false, policy); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if stored.PositionSeconds != 100 {
		t.Fatalf("stale replay rewound position to %d, want 100 (guard failed)", stored.PositionSeconds)
	}
	if stored.EventTimeMS != 1000 {
		t.Fatalf("stale replay overwrote event_time_ms to %d, want 1000", stored.EventTimeMS)
	}

	// A genuinely newer report (even an intentional rewind to an earlier
	// position) at a LATER event-time must still advance.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha",
		PositionSeconds: 50, DurationSeconds: 1000, EventTimeMS: 2000,
	}, false, policy); err != nil {
		t.Fatal(err)
	}
	stored, ok, err = st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if stored.PositionSeconds != 50 {
		t.Fatalf("newer event-time report position = %d, want 50 (deliberate rewind must advance)", stored.PositionSeconds)
	}
	if stored.EventTimeMS != 2000 {
		t.Fatalf("stored event_time_ms = %d, want 2000", stored.EventTimeMS)
	}

	// A legacy write with no event-time (EventTimeMS zero) falls back to the
	// pre-ABPARITY-1 unconditional-overwrite behavior.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha",
		PositionSeconds: 5, DurationSeconds: 1000,
	}, false, policy); err != nil {
		t.Fatal(err)
	}
	stored, ok, err = st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if stored.PositionSeconds != 5 {
		t.Fatalf("legacy no-event-time write position = %d, want 5 (legacy overwrite fallback)", stored.PositionSeconds)
	}
}

func TestResumeOverrideMakesAudioResumable(t *testing.T) {
	st := openSeeded(t)
	progress := PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 45,
		DurationSeconds: 120,
	}

	// A plugin override (podcasts: ResumeMedia=true) beats the audio class
	// default — the position lands and Continue Watching surfaces it.
	resume := true
	policy := ResumePolicy{Class: MediaClassAudio, Override: &resume}
	if err := st.UpsertPlaybackProgress(context.Background(), progress, false, policy); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if stored.PositionSeconds != 45 {
		t.Fatalf("overridden audio position = %d, want 45", stored.PositionSeconds)
	}
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	items, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "continue-watching", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("continue-watching items = %d, want 1", len(items))
	}

	// The inverse override pins resumable video to no-resume.
	noResume := false
	if err := st.UpsertPlaybackProgress(context.Background(), progress, false, ResumePolicy{Class: MediaClassVideo, Override: &noResume}); err != nil {
		t.Fatal(err)
	}
	stored, _, err = st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if stored.PositionSeconds != 0 {
		t.Fatalf("overridden video position = %d, want 0", stored.PositionSeconds)
	}
}

// TestBackdatedImportEngagementKeepsHistoricalTimestampAndProvenance proves the
// ADR-0009 §7 import-readiness reservation: a backdated engagement
// event with an import provenance persists with its REAL historical occurred_at
// and a distinguishable provenance, while the recency key (updated_at_ms) stays
// "now" so the years-old import never masquerades as a fresh play.
func TestBackdatedImportEngagementKeepsHistoricalTimestampAndProvenance(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	before := time.Now().UTC().Truncate(time.Millisecond)     // updated_at_ms is ms-precision
	historical := time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC) // "watched 3 years ago"
	progress := PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 120,
		DurationSeconds: 120,
	}
	prov := EngagementProvenance{Source: "import", OccurredAt: historical}
	if err := st.UpsertPlaybackProgressWithProvenance(ctx, progress, true, ResumePolicy{Class: MediaClassVideo}, prov); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()

	stored, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if stored.Provenance != "import" {
		t.Fatalf("provenance = %q, want %q", stored.Provenance, "import")
	}
	if !stored.OccurredAt.Equal(historical) {
		t.Fatalf("occurred-at = %v, want historical %v", stored.OccurredAt, historical)
	}
	if !stored.Watched {
		t.Fatalf("imported finished play not marked watched")
	}
	if stored.PlayCount != 1 {
		t.Fatalf("imported play count = %d, want 1", stored.PlayCount)
	}
	// The recency key (UpdatedAt) is "now", NOT the backdated time — so the
	// import does not surface as a fresh play on the recency shelves.
	if stored.UpdatedAt.Before(before) || stored.UpdatedAt.After(after) {
		t.Fatalf("updated-at = %v, want within [%v, %v] (recency key, not backdated)", stored.UpdatedAt, before, after)
	}
	if !stored.UpdatedAt.After(historical) {
		t.Fatalf("recency key %v should be far after backdated occurred-at %v", stored.UpdatedAt, historical)
	}
}

func TestImportedWatchStateCarriesSourcePlayCount(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.UpsertPlaybackProgressWithProvenance(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 30,
		DurationSeconds: 120,
		PlayCount:       4,
	}, false, ResumePolicy{Class: MediaClassVideo}, EngagementProvenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if stored.PlayCount != 4 || stored.Watched {
		t.Fatalf("imported watch-state = {plays=%d watched=%v}, want {4 false}", stored.PlayCount, stored.Watched)
	}

	if err := st.UpsertPlaybackProgressWithProvenance(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 60,
		DurationSeconds: 120,
		PlayCount:       2,
	}, false, ResumePolicy{Class: MediaClassVideo}, EngagementProvenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}
	stored, _, err = st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if stored.PlayCount != 4 {
		t.Fatalf("smaller imported watch-state play count lowered to %d, want 4", stored.PlayCount)
	}

	if err := st.UpsertPlaybackProgressWithProvenance(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		DurationSeconds: 120,
		PlayCount:       2,
	}, true, ResumePolicy{Class: MediaClassVideo}, EngagementProvenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}
	stored, _, err = st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if stored.PlayCount != 4 || !stored.Watched {
		t.Fatalf("smaller completed import = {plays=%d watched=%v}, want {4 true}", stored.PlayCount, stored.Watched)
	}
}

// TestNativeEngagementInsertUnchangedByProvenanceReservation proves a plain
// native UpsertPlaybackProgress is byte-for-byte unchanged by the FREEZE-3
// columns: provenance reads back empty and occurred_at stays zero (the column
// is NULL), so existing callers and the recency shelves behave exactly as before.
func TestNativeEngagementInsertUnchangedByProvenanceReservation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	before := time.Now().UTC().Truncate(time.Millisecond) // updated_at_ms is ms-precision
	progress := PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 30,
		DurationSeconds: 120,
	}
	if err := st.UpsertPlaybackProgress(ctx, progress, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()

	stored, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if stored.Provenance != "" {
		t.Fatalf("native provenance = %q, want empty", stored.Provenance)
	}
	if !stored.OccurredAt.IsZero() {
		t.Fatalf("native occurred-at = %v, want zero (column NULL)", stored.OccurredAt)
	}
	if stored.PositionSeconds != 30 {
		t.Fatalf("native position = %d, want 30", stored.PositionSeconds)
	}
	if stored.UpdatedAt.Before(before) || stored.UpdatedAt.After(after) {
		t.Fatalf("native updated-at = %v, want within [%v, %v]", stored.UpdatedAt, before, after)
	}
}

// TestApplyImportedPlaysAdditiveCountAndForwardLastPlayed covers the MIG-4
// scrobble apply path: play_count rises to the source total and
// only ever forward, last-played advances only forward, the row carries import
// provenance, the recency key (updated_at_ms) stays "now" (not backdated), and
// no resume position / watched flag is written (a play event, not a resume).
func TestApplyImportedPlaysAdditiveCountAndForwardLastPlayed(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	before := time.Now().UTC().Truncate(time.Millisecond)
	t1 := time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC) // later play

	// First import: 3 plays, last-played t2.
	if err := st.ApplyImportedPlays(ctx, "profile-default", "media-alpha", 3, t2, EngagementProvenance{Source: "import:music", OccurredAt: t2}); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()

	got, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	if got.PlayCount != 3 {
		t.Fatalf("play count = %d, want 3", got.PlayCount)
	}
	if got.Provenance != "import:music" {
		t.Fatalf("provenance = %q, want import:music", got.Provenance)
	}
	if !got.OccurredAt.Equal(t2) {
		t.Fatalf("last-played OccurredAt = %v, want %v", got.OccurredAt, t2)
	}
	if got.PositionSeconds != 0 || got.Watched {
		t.Fatalf("scrobble apply wrote resume/watched: {pos=%d watched=%v}", got.PositionSeconds, got.Watched)
	}
	if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
		t.Fatalf("recency key %v outside [%v,%v] (must be now, not backdated)", got.UpdatedAt, before, after)
	}

	// Re-apply the SAME running total with an EARLIER timestamp: additive MAX —
	// count unchanged, last-played not moved backward.
	if err := st.ApplyImportedPlays(ctx, "profile-default", "media-alpha", 3, t1, EngagementProvenance{Source: "import:music", OccurredAt: t1}); err != nil {
		t.Fatal(err)
	}
	reGot, _, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if reGot.PlayCount != 3 {
		t.Fatalf("re-apply changed count: %d, want 3 (MAX, additive)", reGot.PlayCount)
	}
	if !reGot.OccurredAt.Equal(t2) {
		t.Fatalf("re-apply moved last-played backward: %v, want %v (forward-only)", reGot.OccurredAt, t2)
	}

	// A LARGER total adds the delta (the switch-day re-import picks up new plays).
	if err := st.ApplyImportedPlays(ctx, "profile-default", "media-alpha", 5, t2, EngagementProvenance{Source: "import:music", OccurredAt: t2}); err != nil {
		t.Fatal(err)
	}
	grown, _, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if grown.PlayCount != 5 {
		t.Fatalf("larger total: play count = %d, want 5", grown.PlayCount)
	}
}

func TestAudioPlayedAtEightyPercentCountsPlay(t *testing.T) {
	st := openSeeded(t)

	// 96/120 = 80%: audio counts a play; the same report leaves video unwatched (95% threshold).
	report := PlaybackProgress{ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 96, DurationSeconds: 120}
	if err := st.UpsertPlaybackProgress(context.Background(), report, false, ResumePolicy{Class: MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	stored, _, err := st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Watched || stored.PlayCount != 1 {
		t.Fatalf("audio at 80%% = watched %v plays %d, want watched with 1 play", stored.Watched, stored.PlayCount)
	}

	// Repeated watched reports do not double count.
	if err := st.UpsertPlaybackProgress(context.Background(), report, false, ResumePolicy{Class: MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	stored, _, _ = st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if stored.PlayCount != 1 {
		t.Fatalf("plays after repeat report = %d, want 1", stored.PlayCount)
	}

	// A fresh listen (resets to unwatched) then completing again counts a second play.
	if err := st.UpsertPlaybackProgress(context.Background(), PlaybackProgress{ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 10, DurationSeconds: 120}, false, ResumePolicy{Class: MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(context.Background(), report, false, ResumePolicy{Class: MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	stored, _, _ = st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if stored.PlayCount != 2 {
		t.Fatalf("plays after second listen = %d, want 2", stored.PlayCount)
	}

	video := PlaybackProgress{ProfileID: "profile-default", MediaID: "media-archive", PositionSeconds: 96, DurationSeconds: 120}
	if err := st.UpsertPlaybackProgress(context.Background(), video, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	stored, _, _ = st.PlaybackProgressFor(context.Background(), "profile-default", "media-archive")
	if stored.Watched {
		t.Fatal("video at 80% marked watched; threshold is 95%")
	}
}

func TestPlayedSourceKinds(t *testing.T) {
	st := openSeeded(t)
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	finish := PlaybackProgress{ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 120, DurationSeconds: 120}
	for i := 0; i < 2; i++ {
		if err := st.UpsertPlaybackProgress(context.Background(), PlaybackProgress{ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 10, DurationSeconds: 120}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPlaybackProgress(context.Background(), finish, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertPlaybackProgress(context.Background(), PlaybackProgress{ProfileID: "profile-default", MediaID: "media-archive", PositionSeconds: 120, DurationSeconds: 120}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(context.Background(), PlaybackProgress{ProfileID: "profile-managed", MediaID: "media-archive", PositionSeconds: 120, DurationSeconds: 120}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}

	most, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "most-played", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(most) != 2 || most[0].ID != "media-alpha" || most[0].Summary != "2 plays" {
		t.Fatalf("most-played = %#v, want media-alpha first with 2 plays", most)
	}
	managedMost, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "most-played", ProfileID: "profile-managed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(managedMost) != 1 || managedMost[0].ID != "media-archive" || managedMost[0].Summary != "1 play" {
		t.Fatalf("managed most-played = %#v, want only profile-managed play counts", managedMost)
	}

	recent, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "recently-played", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	// Timestamps tie at millisecond resolution in-test, so assert membership.
	if len(recent) != 2 {
		t.Fatalf("recently-played = %#v, want both played items", recent)
	}

	// Reveal filtering: restricted plays stay invisible to unrevealed scopes.
	hidden, err := st.SourceItems(context.Background(), LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "most-played", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range hidden {
		if item.ID == "media-archive" {
			t.Fatalf("unrevealed most-played names restricted media: %#v", hidden)
		}
	}
}

func TestListeningStatsProfileVisibilityAndEntityRollups(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	openLib, err := st.CreateLibrary(ctx, "Open Music", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	sealedLib, err := st.CreateLibrary(ctx, "Sealed Music", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	importTrack := func(lib LibraryID, path, title, fp string) MediaID {
		m, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: lib, Path: path, Title: title, Container: "flac", Codec: "flac",
			Class: MediaClassAudio, ContentFingerprint: fp, DurationSecondsHint: 180,
		})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	visibleA := importTrack(openLib.ID, "/music/a.flac", "Visible A", "stats-a")
	visibleB := importTrack(openLib.ID, "/music/b.flac", "Visible B", "stats-b")
	sealed := importTrack(sealedLib.ID, "/music/hidden.flac", "Sealed", "stats-hidden")
	for _, entity := range []Entity{
		{ID: "stats-artist", LibraryID: openLib.ID, Kind: "artist", Title: "Visible Artist"},
		{ID: "stats-album", LibraryID: openLib.ID, Kind: "album", Title: "Visible Album"},
		{ID: "stats-hidden-artist", LibraryID: sealedLib.ID, Kind: "artist", Title: "Hidden Artist"},
		{ID: "stats-hidden-album", LibraryID: sealedLib.ID, Kind: "album", Title: "Hidden Album"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []EntityRelation{
		{FromEntityID: "stats-artist", Name: "appears-on", ToMediaID: visibleA},
		{FromEntityID: "stats-artist", Name: "appears-on", ToMediaID: visibleB},
		{FromEntityID: "stats-album", Name: "contains", ToMediaID: visibleA},
		{FromEntityID: "stats-hidden-artist", Name: "appears-on", ToMediaID: sealed},
		{FromEntityID: "stats-hidden-album", Name: "contains", ToMediaID: sealed},
	} {
		if err := st.UpsertEntityRelation(ctx, rel); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ApplyImportedPlays(ctx, "profile-default", visibleA, 2, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), EngagementProvenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyImportedPlays(ctx, "profile-default", visibleB, 1, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), EngagementProvenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyImportedPlays(ctx, "profile-managed", visibleB, 9, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), EngagementProvenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyImportedPlays(ctx, "profile-default", sealed, 99, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), EngagementProvenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}

	stats, err := st.ListeningStatsFor(ctx, "profile-default", LibraryFilter{AllLibraries: true, SealedIDs: []LibraryID{sealedLib.ID}}, "2026", []string{"artist"}, []string{"album"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Summary.Plays != 3 || stats.Summary.TrackCount != 2 || stats.Summary.ArtistCount != 1 || stats.Summary.AlbumCount != 1 {
		t.Fatalf("summary = %#v, want visible own-profile counts only", stats.Summary)
	}
	if len(stats.TopTracks) != 2 || stats.TopTracks[0].ID != string(visibleA) || stats.TopTracks[0].PlayCount != 2 {
		t.Fatalf("top tracks = %#v, want visibleA first", stats.TopTracks)
	}
	if len(stats.TopArtists) != 1 || stats.TopArtists[0].ID != "stats-artist" || stats.TopArtists[0].PlayCount != 3 {
		t.Fatalf("top artists = %#v, want visible artist only", stats.TopArtists)
	}
	if len(stats.TopAlbums) != 1 || stats.TopAlbums[0].ID != "stats-album" || stats.TopAlbums[0].PlayCount != 2 {
		t.Fatalf("top albums = %#v, want visible album only", stats.TopAlbums)
	}

	artists, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true, SealedIDs: []LibraryID{sealedLib.ID}}, SourceQuery{Kind: "top-artists", ProfileID: "profile-default", Value: "2026"})
	if err != nil {
		t.Fatal(err)
	}
	if len(artists) != 1 || artists[0].ID != "stats-artist" || artists[0].Summary != "3 plays" {
		t.Fatalf("top-artists source = %#v, want visible artist rollup", artists)
	}
	summary, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true, SealedIDs: []LibraryID{sealedLib.ID}}, SourceQuery{Kind: "listening-summary", ProfileID: "profile-default", Value: "2026"})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != 1 || summary[0].Summary != "3 plays across 2 tracks" {
		t.Fatalf("listening-summary source = %#v, want visible own-profile summary", summary)
	}
}

// TestResumeProgressInProgressBand proves the "resume-progress" Source
// predicate surfaces only Media the Profile has partial
// (started past the resume floor, not finished, not zero) progress on, and
// reveal-filters restricted Media exactly like continue-watching.
func TestResumeProgressInProgressBand(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	// media-alpha: partial progress past the floor — IN-PROGRESS.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 45, DurationSeconds: 120}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	// media-archive: finished (watched) — NOT in-progress. It is also restricted,
	// so it doubles as the reveal-filter probe below.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: "media-archive", PositionSeconds: 90, DurationSeconds: 90}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	// A second open Media barely started (under the resume floor) — NOT in-progress.
	zero := importQueueMedia(t, st, "barely-started")
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: zero, PositionSeconds: 3, DurationSeconds: 600}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}

	items := mustSourceItems(t, st, filter, SourceQuery{Kind: "resume-progress", Value: "in-progress", ProfileID: "profile-default"})
	if len(items) != 1 || items[0].ID != "media-alpha" {
		t.Fatalf("resume-progress in-progress = %#v, want only media-alpha (partial, not finished, not zero)", items)
	}

	// Same definition as continue-watching: the two never disagree.
	cw := mustSourceItems(t, st, filter, SourceQuery{Kind: "continue-watching", ProfileID: "profile-default"})
	if len(cw) != 1 || cw[0].ID != "media-alpha" {
		t.Fatalf("continue-watching = %#v, want it to match resume-progress in-progress", cw)
	}

	// An unrecognized band yields an empty feed, never an error.
	if other := mustSourceItems(t, st, filter, SourceQuery{Kind: "resume-progress", Value: "played", ProfileID: "profile-default"}); len(other) != 0 {
		t.Fatalf("resume-progress value=played = %#v, want empty (band not server-evaluated)", other)
	}

	// Reveal filtering: an unrevealed scope never names restricted Media even if
	// it has progress. media-archive is finished here, but assert it never leaks
	// under an unrevealed filter regardless of band membership.
	hidden := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "resume-progress", Value: "in-progress", ProfileID: "profile-default"})
	for _, item := range hidden {
		if item.ID == "media-archive" {
			t.Fatalf("unrevealed resume-progress names restricted media: %#v", hidden)
		}
	}
}

// TestWatchedByAnyProfileSourcePredicate proves ADR-0051's household aggregate
// watched predicate: the Source exposes only any/no boolean bands over the
// Profile-scoped playback projection, never which Profile watched.
func TestWatchedByAnyProfileSourcePredicate(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	unwatched := importQueueMedia(t, st, "household-unwatched")

	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-managed", MediaID: "media-alpha", PositionSeconds: 120, DurationSeconds: 120}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: "media-archive", PositionSeconds: 90, DurationSeconds: 90}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: unwatched, PositionSeconds: 30, DurationSeconds: 120}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}

	any := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "watched-by-any-profile", Value: "any"}))
	if !any["media-alpha"] {
		t.Fatalf("watched-by-any-profile any omitted media watched by another Profile: %v", any)
	}
	if any[unwatched] {
		t.Fatalf("watched-by-any-profile any included in-progress but unwatched media %q: %v", unwatched, any)
	}

	none := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "watched-by-any-profile", Value: "none"}))
	if !none[unwatched] {
		t.Fatalf("watched-by-any-profile none omitted household-unwatched media %q: %v", unwatched, none)
	}
	if none["media-alpha"] {
		t.Fatalf("watched-by-any-profile none included watched media: %v", none)
	}

	hidden := mediaIDSet(mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "watched-by-any-profile", Value: "any"}))
	if hidden["media-archive"] {
		t.Fatalf("unrevealed watched-by-any-profile leaked restricted media: %v", hidden)
	}
	if other := mustSourceItems(t, st, filter, SourceQuery{Kind: "watched-by-any-profile", Value: "profile-default"}); len(other) != 0 {
		t.Fatalf("watched-by-any-profile profile-like value = %#v, want empty", other)
	}
}

func TestPlayQueueRenumbersAndMovesWithinProfile(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	extraA := importQueueMedia(t, st, "queue-a")
	extraB := importQueueMedia(t, st, "queue-b")

	if err := st.QueueAdd(ctx, "profile-default", "media-alpha", nil, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-default", extraA, nil, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-default", extraB, queueIntPtr(1), filter); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, filter, "profile-default", []MediaID{"media-alpha", extraB, extraA})

	if err := st.QueueMove(ctx, "profile-default", extraB, 1, filter); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, filter, "profile-default", []MediaID{"media-alpha", extraB, extraA})

	if err := st.QueueAdd(ctx, "profile-default", "media-alpha", nil, filter); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, filter, "profile-default", []MediaID{extraB, extraA, "media-alpha"})

	if err := st.QueueRemove(ctx, "profile-default", extraA, filter); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, filter, "profile-default", []MediaID{extraB, "media-alpha"})

	if err := st.QueueClear(ctx, "profile-default", filter); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, filter, "profile-default", nil)
}

func TestPlayQueueIsPerProfileAndRevealFilteredWithoutMutation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	hidden := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}

	if err := st.QueueAdd(ctx, "profile-default", "media-alpha", nil, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-default", "media-archive", nil, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-managed", "media-alpha", nil, revealed); err != nil {
		t.Fatal(err)
	}

	assertQueueIDs(t, st, hidden, "profile-default", []MediaID{"media-alpha"})
	assertQueueIDs(t, st, revealed, "profile-default", []MediaID{"media-alpha", "media-archive"})
	assertQueueIDs(t, st, hidden, "profile-default", []MediaID{"media-alpha"})
	assertQueueIDs(t, st, revealed, "profile-default", []MediaID{"media-alpha", "media-archive"})
	assertQueueIDs(t, st, revealed, "profile-managed", []MediaID{"media-alpha"})
}

func TestPlayQueueAddValidatesMediaVisibility(t *testing.T) {
	st := openSeeded(t)
	err := st.QueueAdd(context.Background(), "profile-default", "media-archive", nil, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
	})
	if err == nil {
		t.Fatal("QueueAdd accepted hidden media")
	}
	assertQueueIDs(t, st, LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}, "profile-default", nil)
}

func TestPlayQueueMutationsCannotTouchHiddenEntries(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	hidden := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}

	if err := st.QueueAdd(ctx, "profile-default", "media-archive", nil, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-default", "media-alpha", nil, revealed); err != nil {
		t.Fatal(err)
	}

	// Removing a hidden-but-queued id is a silent no-op: hidden == missing,
	// so there is nothing to remove — and the entry survives for reveal.
	if err := st.QueueRemove(ctx, "profile-default", "media-archive", hidden); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, revealed, "profile-default", []MediaID{"media-archive", "media-alpha"})

	// Moving a hidden-but-queued id fails with byte-identical shape to an
	// id that was never queued — no existence oracle.
	errHidden := st.QueueMove(ctx, "profile-default", "media-archive", 0, hidden)
	errAbsent := st.QueueMove(ctx, "profile-default", "media-never-queued", 0, hidden)
	if errHidden == nil || errAbsent == nil || errHidden.Error() != errAbsent.Error() {
		t.Fatalf("hidden move = %v, absent move = %v - must be indistinguishable", errHidden, errAbsent)
	}
	assertQueueIDs(t, st, revealed, "profile-default", []MediaID{"media-archive", "media-alpha"})

	// Clear removes only what the caller can see; the hidden entry stays
	// and reappears on reveal.
	if err := st.QueueClear(ctx, "profile-default", hidden); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, hidden, "profile-default", nil)
	assertQueueIDs(t, st, revealed, "profile-default", []MediaID{"media-archive"})
}

func TestPlayQueuePositionsCountVisibleEntries(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	hidden := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}
	extraA := importQueueMedia(t, st, "queue-pos-a")
	extraB := importQueueMedia(t, st, "queue-pos-b")

	for _, id := range []MediaID{"media-archive", extraA, extraB} {
		if err := st.QueueAdd(ctx, "profile-default", id, nil, revealed); err != nil {
			t.Fatal(err)
		}
	}

	// A relocked caller reads [extraA, extraB]; moving extraB to position 0
	// lands it before extraA while the hidden entry keeps the front slot.
	if err := st.QueueMove(ctx, "profile-default", extraB, 0, hidden); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, hidden, "profile-default", []MediaID{extraB, extraA})
	assertQueueIDs(t, st, revealed, "profile-default", []MediaID{"media-archive", extraB, extraA})

	// Add-at-position counts visible slots the same way.
	extraC := importQueueMedia(t, st, "queue-pos-c")
	if err := st.QueueAdd(ctx, "profile-default", extraC, queueIntPtr(1), hidden); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, hidden, "profile-default", []MediaID{extraB, extraC, extraA})
	assertQueueIDs(t, st, revealed, "profile-default", []MediaID{"media-archive", extraB, extraC, extraA})
}

func TestPlayQueueWritesStayBehindQueueAPI(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	allowed := map[string]bool{
		filepath.Join(root, "internal", "store", "queue.go"):                            true,
		filepath.Join(root, "internal", "store", "engagement_prune.go"):                 true,
		filepath.Join(root, "internal", "store", "engagement_prune_test.go"):            true,
		filepath.Join(root, "internal", "store", "engagement_test.go"):                  true,
		filepath.Join(root, "internal", "store", "migrate", "events", "0008_queue.sql"): true,
	}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".sql")) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "play_queue") && !allowed[filepath.Clean(path)] {
			t.Fatalf("play_queue referenced outside queue API: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMediaFavoriteRequiresVisibleMediaAndPreservesHiddenRows(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	hidden := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}

	if err := st.SetMediaFavorite(ctx, "profile-default", "media-archive", true, revealed); err != nil {
		t.Fatal(err)
	}
	favorited, err := st.MediaFavorited(ctx, "profile-default", "media-archive")
	if err != nil {
		t.Fatal(err)
	}
	if !favorited {
		t.Fatal("MediaFavorited = false, want true")
	}

	errHiddenFavorite := st.SetMediaFavorite(ctx, "profile-default", "media-archive", true, hidden)
	errMissingFavorite := st.SetMediaFavorite(ctx, "profile-default", "media-missing", true, hidden)
	if errHiddenFavorite == nil || errMissingFavorite == nil || errHiddenFavorite.Error() != errMissingFavorite.Error() {
		t.Fatalf("hidden favorite = %v, missing favorite = %v; want identical errors", errHiddenFavorite, errMissingFavorite)
	}

	errHiddenUnfavorite := st.SetMediaFavorite(ctx, "profile-default", "media-archive", false, hidden)
	errMissingUnfavorite := st.SetMediaFavorite(ctx, "profile-default", "media-missing", false, hidden)
	if errHiddenUnfavorite == nil || errMissingUnfavorite == nil || errHiddenUnfavorite.Error() != errMissingUnfavorite.Error() {
		t.Fatalf("hidden unfavorite = %v, missing unfavorite = %v; want identical errors", errHiddenUnfavorite, errMissingUnfavorite)
	}
	favorited, err = st.MediaFavorited(ctx, "profile-default", "media-archive")
	if err != nil {
		t.Fatal(err)
	}
	if !favorited {
		t.Fatal("hidden unfavorite deleted the row")
	}

	if err := st.SetMediaFavorite(ctx, "profile-default", "media-archive", false, revealed); err != nil {
		t.Fatal(err)
	}
	favorited, err = st.MediaFavorited(ctx, "profile-default", "media-archive")
	if err != nil {
		t.Fatal(err)
	}
	if favorited {
		t.Fatal("revealed unfavorite left the row")
	}
}

func importQueueMedia(t *testing.T, st *Store, slug string) MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/" + slug + ".mp4",
		Title:              slug,
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:" + slug,
	})
	if err != nil {
		t.Fatal(err)
	}
	return media.ID
}

func queueIntPtr(value int) *int {
	return &value
}

func assertQueueIDs(t *testing.T, st *Store, filter LibraryFilter, profile ProfileID, want []MediaID) {
	t.Helper()
	items, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "queue", ProfileID: profile})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]MediaID, len(items))
	for i, item := range items {
		got[i] = item.ID
	}
	if !equalMediaIDs(got, want) {
		t.Fatalf("queue IDs = %v, want %v", got, want)
	}
}

// nextUpFixture builds an ordered container in library-local with three
// children whose titles invert the play order (so title ordering would
// betray a sort_order bug), plus an unordered fan-out entity over the same
// children (which must never produce a next-up row).
func nextUpFixture(t *testing.T, st *Store, container EntityID, prefix string) []MediaID {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, widgetEntity(container, "library-local")); err != nil {
		t.Fatal(err)
	}
	titles := []string{"Zeta", "Yankee", "Xray"}
	ids := make([]MediaID, len(titles))
	for i, title := range titles {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               "/media/" + prefix + title + ".mp4",
			Title:              prefix + title,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: "sha256:" + prefix + title,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = media.ID
		order := i + 1
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: container, Name: "linked", ToMediaID: media.ID, SortOrder: &order,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func mustReport(t *testing.T, st *Store, profile ProfileID, media MediaID, position, duration int, finished bool) {
	t.Helper()
	err := st.UpsertPlaybackProgress(context.Background(), PlaybackProgress{
		ProfileID: profile, MediaID: media,
		PositionSeconds: position, DurationSeconds: duration,
	}, finished, ResumePolicy{Class: MediaClassVideo})
	if err != nil {
		t.Fatal(err)
	}
}

func nextUp(t *testing.T, st *Store, filter LibraryFilter, profile ProfileID) []SourceItem {
	t.Helper()
	items, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "next-up", ProfileID: profile})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestNextUpFollowsPlayNextOrder(t *testing.T) {
	st := openSeeded(t)
	filter := LibraryFilter{AllLibraries: true}
	ids := nextUpFixture(t, st, "entity-season", "s1-")

	// No engagement in the container: no next-up row.
	if items := nextUp(t, st, filter, "profile-default"); len(items) != 0 {
		t.Fatalf("next-up before engagement = %#v, want empty", items)
	}

	// Finishing the first child surfaces the second — sort_order, not
	// title, decides (titles are in reverse play order).
	mustReport(t, st, "profile-default", ids[0], 120, 120, true)
	items := nextUp(t, st, filter, "profile-default")
	if len(items) != 1 || items[0].ID != ids[1] {
		t.Fatalf("next-up = %#v, want exactly %q", items, ids[1])
	}

	// Finishing out of order: with child 2 also watched the frontier is
	// child 3 — one item per container, never several.
	mustReport(t, st, "profile-default", ids[1], 120, 120, true)
	items = nextUp(t, st, filter, "profile-default")
	if len(items) != 1 || items[0].ID != ids[2] {
		t.Fatalf("next-up = %#v, want exactly %q", items, ids[2])
	}

	// Everything watched: the container retires from the shelf.
	mustReport(t, st, "profile-default", ids[2], 120, 120, true)
	if items = nextUp(t, st, filter, "profile-default"); len(items) != 0 {
		t.Fatalf("next-up after finishing all = %#v, want empty", items)
	}
}

func TestNextUpYieldsNothingWhileFrontierIsResumable(t *testing.T) {
	st := openSeeded(t)
	filter := LibraryFilter{AllLibraries: true}
	ids := nextUpFixture(t, st, "entity-season", "s1-")
	mustReport(t, st, "profile-default", ids[0], 120, 120, true)

	// The frontier child goes in progress past the resume floor: it now
	// lives on continue-watching, and next-up must not double-list it nor
	// skip ahead — the container yields nothing (ADR-0028 §5's separate
	// shelves).
	mustReport(t, st, "profile-default", ids[1], 45, 120, false)
	if items := nextUp(t, st, filter, "profile-default"); len(items) != 0 {
		t.Fatalf("next-up with resumable frontier = %#v, want empty", items)
	}
	cw, err := st.SourceItems(context.Background(), filter, SourceQuery{Kind: "continue-watching", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cw) != 1 || cw[0].ID != ids[1] {
		t.Fatalf("continue-watching = %#v, want %q", cw, ids[1])
	}

	// Under the resume floor the child is not resumable (continue-watching
	// hides it), so next-up keeps it: nothing falls between the shelves.
	mustReport(t, st, "profile-default", ids[1], resumeFloorSeconds-1, 120, false)
	items := nextUp(t, st, filter, "profile-default")
	if len(items) != 1 || items[0].ID != ids[1] {
		t.Fatalf("next-up with barely-started frontier = %#v, want %q", items, ids[1])
	}
}

func TestNextUpOrdersContainersByEngagementRecency(t *testing.T) {
	st := openSeeded(t)
	filter := LibraryFilter{AllLibraries: true}
	first := nextUpFixture(t, st, "entity-season-one", "s1-")
	second := nextUpFixture(t, st, "entity-season-two", "s2-")

	mustReport(t, st, "profile-default", first[0], 120, 120, true)
	time.Sleep(5 * time.Millisecond) // distinct updated_at_ms for recency
	mustReport(t, st, "profile-default", second[0], 120, 120, true)

	items := nextUp(t, st, filter, "profile-default")
	if len(items) != 2 || items[0].ID != second[1] || items[1].ID != first[1] {
		t.Fatalf("next-up = %#v, want most recently engaged container first (%q, %q)", items, second[1], first[1])
	}

	// Re-engaging the first container moves it back to the front.
	time.Sleep(5 * time.Millisecond)
	mustReport(t, st, "profile-default", first[1], 120, 120, true)
	items = nextUp(t, st, filter, "profile-default")
	if len(items) != 2 || items[0].ID != first[2] || items[1].ID != second[1] {
		t.Fatalf("next-up after re-engaging = %#v, want %q then %q", items, first[2], second[1])
	}
}

func TestNextUpIgnoresUnorderedContainers(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	// A category-style fan-out entity relates to seeded media without
	// sort_order: engagement there must never produce a next-up row.
	if err := st.UpsertEntity(ctx, widgetEntity("entity-category", "library-local")); err != nil {
		t.Fatal(err)
	}
	other, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/other.mp4",
		Title:              "Other",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:other",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, media := range []MediaID{"media-alpha", other.ID} {
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: "entity-category", Name: "linked", ToMediaID: media,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mustReport(t, st, "profile-default", "media-alpha", 120, 120, true)
	if items := nextUp(t, st, LibraryFilter{AllLibraries: true}, "profile-default"); len(items) != 0 {
		t.Fatalf("next-up over unordered container = %#v, want empty", items)
	}
}

func TestNextUpRevealAndProfileIsolation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	ids := nextUpFixture(t, st, "entity-season", "s1-")
	mustReport(t, st, "profile-default", ids[0], 120, 120, true)

	// An ordered container in the restricted Library: invisible unrevealed,
	// present when the Reveal contract opens it (the standard LibraryFilter
	// funnel via mediaForEntitySQL).
	if err := st.UpsertEntity(ctx, widgetEntity("entity-vault", "library-archive")); err != nil {
		t.Fatal(err)
	}
	one := 1
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-vault", Name: "linked", ToMediaID: "media-archive", SortOrder: &one,
	}); err != nil {
		t.Fatal(err)
	}
	restricted, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-archive",
		Path:               "/archive/next.mp4",
		Title:              "Archive Next",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:archive-next",
	})
	if err != nil {
		t.Fatal(err)
	}
	two := 2
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-vault", Name: "linked", ToMediaID: restricted.ID, SortOrder: &two,
	}); err != nil {
		t.Fatal(err)
	}
	mustReport(t, st, "profile-default", "media-archive", 120, 120, true)

	hidden := nextUp(t, st, LibraryFilter{AllLibraries: true}, "profile-default")
	if len(hidden) != 1 || hidden[0].ID != ids[1] {
		t.Fatalf("unrevealed next-up = %#v, want only %q", hidden, ids[1])
	}
	revealed := nextUp(t, st, LibraryFilter{Revealed: true, AllLibraries: true}, "profile-default")
	found := false
	for _, item := range revealed {
		if item.ID == restricted.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("revealed next-up = %#v, want it to include %q", revealed, restricted.ID)
	}

	// Another Profile's engagement is its own: a fresh profile sees nothing,
	// and a missing profile ID yields nothing rather than leaking.
	if items := nextUp(t, st, LibraryFilter{Revealed: true, AllLibraries: true}, "profile-other"); len(items) != 0 {
		t.Fatalf("next-up for unengaged profile = %#v, want empty", items)
	}
	if items := nextUp(t, st, LibraryFilter{Revealed: true, AllLibraries: true}, ""); len(items) != 0 {
		t.Fatalf("next-up without profile = %#v, want empty", items)
	}
}

func TestNextUpReturnsMediaForEntityErrors(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	ids := nextUpFixture(t, st, "entity-season", "s1-")
	mustReport(t, st, "profile-default", ids[0], 120, 120, true)
	originalCatalogR := st.catalogR
	st.catalogR = st.catalogW
	t.Cleanup(func() { st.catalogR = originalCatalogR })
	if _, err := st.catalogW.ExecContext(ctx, `ALTER TABLE media RENAME TO media_broken`); err != nil {
		t.Fatal(err)
	}
	_, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "next-up", ProfileID: "profile-default"})
	if err == nil || !strings.Contains(err.Error(), "no such table: media") {
		t.Fatalf("next-up err = %v, want media query error", err)
	}
}

// TestImportProvenanceColumnsWrittenAndReadBack proves the MIGREV-7 (ADR-0037
// §2.4) run-scoped import provenance persists on both import write paths — the
// watch-state upsert and the scrobble play-count apply — and reads back the
// run id, importer id, and match method that resolved identity.
func TestImportProvenanceColumnsWrittenAndReadBack(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Watch-state path (UpsertPlaybackProgressWithProvenance).
	prov := EngagementProvenance{
		Source:      "import:filmtv",
		OccurredAt:  time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC),
		RunID:       "job-run-1",
		ImporterID:  "plex",
		MatchMethod: "path",
	}
	if err := st.UpsertPlaybackProgressWithProvenance(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 60, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}, prov); err != nil {
		t.Fatal(err)
	}
	ws, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("watch-state read: ok=%v err=%v", ok, err)
	}
	if ws.ImportRunID != "job-run-1" || ws.ImportImporterID != "plex" || ws.ImportMatchMethod != "path" {
		t.Fatalf("watch-state provenance = {run=%q importer=%q method=%q}, want {job-run-1 plex path}",
			ws.ImportRunID, ws.ImportImporterID, ws.ImportMatchMethod)
	}

	// Scrobble path (ApplyImportedPlays), external-id method.
	if err := st.ApplyImportedPlays(ctx, "profile-default", "media-beta", 4,
		time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC), EngagementProvenance{
			Source: "import:music", OccurredAt: time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC),
			RunID: "job-run-2", ImporterID: "lastfm", MatchMethod: "external-id",
		}); err != nil {
		t.Fatal(err)
	}
	pl, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-beta")
	if err != nil || !ok {
		t.Fatalf("scrobble read: ok=%v err=%v", ok, err)
	}
	if pl.ImportRunID != "job-run-2" || pl.ImportImporterID != "lastfm" || pl.ImportMatchMethod != "external-id" {
		t.Fatalf("scrobble provenance = {run=%q importer=%q method=%q}, want {job-run-2 lastfm external-id}",
			pl.ImportRunID, pl.ImportImporterID, pl.ImportMatchMethod)
	}
}

// TestNativeEngagementLeavesImportProvenanceColumnsEmpty proves a plain native
// write leaves the MIGREV-7 import provenance columns NULL (read back empty), so
// native rows stay byte-for-byte pre-MIGREV-7 and are distinguishable from
// imported rows.
func TestNativeEngagementLeavesImportProvenanceColumnsEmpty(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("read: ok=%v err=%v", ok, err)
	}
	if got.ImportRunID != "" || got.ImportImporterID != "" || got.ImportMatchMethod != "" {
		t.Fatalf("native import provenance = {run=%q importer=%q method=%q}, want all empty",
			got.ImportRunID, got.ImportImporterID, got.ImportMatchMethod)
	}
}

// TestNoOpScrobbleRerunPreservesAppliedRunProvenance proves the MED-2 rule: a
// pure no-op re-run (same running total, no later play) must NOT relabel the row
// as freshly applied by the new run — the originally-applied run's provenance
// columns stay intact, so a per-run audit / skip-explanation attributes the row
// to the run that actually applied it.
func TestNoOpScrobbleRerunPreservesAppliedRunProvenance(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	t1 := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)

	// Run A applies 3 plays.
	if err := st.ApplyImportedPlays(ctx, "profile-default", "media-alpha", 3, t1, EngagementProvenance{
		Source: "import:music", OccurredAt: t1, RunID: "run-A", ImporterID: "lastfm", MatchMethod: "scrobble",
	}); err != nil {
		t.Fatal(err)
	}
	// Run B re-imports the SAME export (same total, same/earlier time): a no-op.
	if err := st.ApplyImportedPlays(ctx, "profile-default", "media-alpha", 3, t1, EngagementProvenance{
		Source: "import:music", OccurredAt: t1, RunID: "run-B", ImporterID: "lastfm", MatchMethod: "scrobble",
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok {
		t.Fatalf("read: ok=%v err=%v", ok, err)
	}
	if got.PlayCount != 3 {
		t.Fatalf("no-op re-run changed count: %d, want 3", got.PlayCount)
	}
	if got.ImportRunID != "run-A" {
		t.Fatalf("no-op re-run relabeled run: import_run_id = %q, want run-A (the run that applied)", got.ImportRunID)
	}

	// A REAL advance (larger total) DOES adopt the new run's provenance.
	t2 := time.Date(2024, 6, 7, 8, 9, 10, 0, time.UTC)
	if err := st.ApplyImportedPlays(ctx, "profile-default", "media-alpha", 5, t2, EngagementProvenance{
		Source: "import:music", OccurredAt: t2, RunID: "run-C", ImporterID: "lastfm", MatchMethod: "scrobble",
	}); err != nil {
		t.Fatal(err)
	}
	advanced, _, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if advanced.PlayCount != 5 || advanced.ImportRunID != "run-C" {
		t.Fatalf("advancing re-run = {count=%d run=%q}, want {5 run-C}", advanced.PlayCount, advanced.ImportRunID)
	}
}

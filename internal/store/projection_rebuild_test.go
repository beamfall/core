package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

type projectionSnapshot struct {
	name string
	data []byte
}

func TestProjectionRebuildDeterministicFromFrozenBase(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 45,
		DurationSeconds: 120,
		EventTimeMS:     200,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID:       "profile-managed",
		MediaID:         "media-alpha",
		PositionSeconds: 120,
		DurationSeconds: 120,
		EventTimeMS:     300,
		ChannelContext:  true,
	}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyImportedPlays(ctx, "profile-default", "media-archive", 7,
		time.Date(2021, 2, 3, 4, 5, 6, 0, time.UTC), EngagementProvenance{
			Source:      "import",
			OccurredAt:  time.Date(2021, 2, 3, 4, 5, 6, 0, time.UTC),
			RunID:       "run-1",
			ImporterID:  "importer-1",
			MatchMethod: "external-id",
		}); err != nil {
		t.Fatal(err)
	}

	live := snapshotEngagementProjections(t, st.eventsR)
	if err := st.rebuildEngagementProjections(ctx); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	first := snapshotEngagementProjections(t, st.eventsR)
	if err := st.rebuildEngagementProjections(ctx); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	second := snapshotEngagementProjections(t, st.eventsR)

	if name := firstDifferingProjection(live, first); name != "" {
		t.Fatalf("first rebuild differs from live projection %q", name)
	}
	if name := firstDifferingProjection(first, second); name != "" {
		t.Fatalf("projection rebuild is not byte-identical; first difference: %q", name)
	}
}

func snapshotEngagementProjections(t *testing.T, db *sql.DB) []projectionSnapshot {
	t.Helper()
	return []projectionSnapshot{{
		name: "playback_progress",
		data: snapshotPlaybackProgress(t, db),
	}}
}

func snapshotPlaybackProgress(t *testing.T, db *sql.DB) []byte {
	t.Helper()
	rows, err := db.Query(`
		SELECT profile_id, media_id, position_seconds, duration_seconds, watched,
		       updated_at_ms, play_count, last_played_at_ms, provenance,
		       occurred_at_ms, import_run_id, import_importer_id,
		       import_match_method, event_time_ms, channel_context, continue_hidden
		FROM playback_progress
		ORDER BY profile_id, media_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var snapshot bytes.Buffer
	for rows.Next() {
		var (
			profileID, mediaID, provenance                         string
			position, duration, watched, playCount, channelContext int
			continueHidden                                         int
			updatedAt                                              int64
			lastPlayedAt, occurredAt, eventTime                    sql.NullInt64
			runID, importerID, matchMethod                         sql.NullString
		)
		if err := rows.Scan(&profileID, &mediaID, &position, &duration, &watched,
			&updatedAt, &playCount, &lastPlayedAt, &provenance, &occurredAt,
			&runID, &importerID, &matchMethod, &eventTime, &channelContext, &continueHidden); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&snapshot, "%q\t%q\t%d\t%d\t%d\t%d\t%d\t%v\t%q\t%v\t%v\t%v\t%v\t%v\t%d\t%d\n",
			profileID, mediaID, position, duration, watched, updatedAt, playCount,
			lastPlayedAt, provenance, occurredAt, runID, importerID, matchMethod,
			eventTime, channelContext, continueHidden)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot.Bytes()
}

func firstDifferingProjection(left, right []projectionSnapshot) string {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i].name != right[i].name {
			return left[i].name
		}
		if !bytes.Equal(left[i].data, right[i].data) {
			return left[i].name
		}
	}
	if len(left) > limit {
		return left[limit].name
	}
	if len(right) > limit {
		return right[limit].name
	}
	return ""
}

func TestProjectionRebuildRefusesUnloggedRows(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if _, err := st.eventsW.ExecContext(ctx, `
		INSERT INTO playback_progress(profile_id, media_id, updated_at_ms)
		VALUES ('profile-unlogged', 'media-unlogged', 1)
	`); err != nil {
		t.Fatal(err)
	}
	if err := st.rebuildEngagementProjections(ctx); err == nil {
		t.Fatal("rebuild accepted a projection row without a canonical event")
	}
}

// Rebuild replays the log and refuses when the replay would change any
// existing row on any column, leaving the table untouched. Legacy rows the
// log cannot reproduce exercise the refusal; a fully logged history must
// rebuild unchanged.
func TestProjectionRebuildVerifiesReplayAgainstExistingRows(t *testing.T) {
	const profile, media = "profile-default", "media-alpha"
	legacyRow := func(t *testing.T, st *Store) {
		t.Helper()
		// A pre-log row at 30 s with no engagement event behind it.
		if _, err := st.eventsW.ExecContext(t.Context(), `
			INSERT INTO playback_progress(profile_id, media_id, position_seconds, duration_seconds, play_count, updated_at_ms)
			VALUES (?, ?, 30, 120, 2, 1)
		`, profile, media); err != nil {
			t.Fatal(err)
		}
	}
	heartbeat := func(t *testing.T, st *Store) {
		t.Helper()
		if err := st.UpsertPlaybackProgress(t.Context(), PlaybackProgress{
			ProfileID: profile, MediaID: media, PositionSeconds: 30, DurationSeconds: 120,
		}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
	}
	hide := func(t *testing.T, st *Store) {
		t.Helper()
		if _, err := st.SetContinueWatchingHidden(t.Context(), profile, media, true, itemOpenFilter); err != nil {
			t.Fatal(err)
		}
	}
	markUnwatched := func(t *testing.T, st *Store) {
		t.Helper()
		if _, err := st.SetItemWatched(t.Context(), profile, ItemTarget{MediaID: media}, false, itemOpenFilter); err != nil {
			t.Fatal(err)
		}
	}
	importPlays := func(t *testing.T, st *Store) {
		t.Helper()
		at := time.Date(2021, 2, 3, 4, 5, 6, 0, time.UTC)
		if err := st.ApplyImportedPlays(t.Context(), profile, media, 3, at, EngagementProvenance{
			Source: "import", OccurredAt: at, RunID: "run-1", ImporterID: "importer-1", MatchMethod: "external-id",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name     string
		steps    []func(*testing.T, *Store)
		rebuilds bool
	}{
		{name: "only hide-continue", steps: []func(*testing.T, *Store){legacyRow, hide}},
		// The heartbeat is logged but comes after the hide: replay would
		// apply the hide to nothing, then recreate the row visible.
		{name: "hide then heartbeat", steps: []func(*testing.T, *Store){legacyRow, hide, heartbeat}},
		// An unwatched mark replays as an UPDATE only, recreating nothing.
		{name: "mark watched=false, then hide", steps: []func(*testing.T, *Store){legacyRow, markUnwatched, hide}},
		// The live import keeps the 30 s position on conflict; replay into
		// an empty table inserts the row at 0.
		{name: "import (legacy row at 30 s), then hide", steps: []func(*testing.T, *Store){legacyRow, importPlays, hide}},
		{name: "fully logged history rebuilds unchanged", rebuilds: true,
			steps: []func(*testing.T, *Store){heartbeat, importPlays, hide, heartbeat}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openSeeded(t)
			for _, step := range tc.steps {
				step(t, st)
			}
			if !continueHidden(t, st, profile, media) {
				t.Fatal("setup left the row visible")
			}
			before := snapshotPlaybackProgress(t, st.eventsR)
			err := st.rebuildEngagementProjections(t.Context())
			if tc.rebuilds && err != nil {
				t.Fatalf("rebuild of a fully logged history refused: %v", err)
			}
			if !tc.rebuilds && err == nil {
				t.Fatal("rebuild accepted a row the log cannot reproduce")
			}
			if after := snapshotPlaybackProgress(t, st.eventsR); !bytes.Equal(before, after) {
				t.Fatalf("rebuild changed playback_progress:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

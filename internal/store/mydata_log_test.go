package store

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"
)

func TestMyEngagementLogHighCardinalityUsesBoundedChunks(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const profile ProfileID = "profile-default"
	const mediaCount = 901
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	catalogTx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	eventsTx, err := st.eventsW.BeginTx(ctx, nil)
	if err != nil {
		_ = catalogTx.Rollback()
		t.Fatal(err)
	}
	expected := make([]myEngagementRankedEvent, 0, mediaCount*3)
	queueRows := make([]queueEntry, 0, mediaCount)
	for i := 0; i < mediaCount; i++ {
		id := MediaID(fmt.Sprintf("media-log-%04d", i))
		if _, err := catalogTx.ExecContext(ctx, `
			INSERT INTO media(id, library_id, title, duration_seconds, container, codec)
			VALUES (?, 'library-local', ?, 60, 'mp4', 'h264')
		`, id, id); err != nil {
			_ = catalogTx.Rollback()
			_ = eventsTx.Rollback()
			t.Fatal(err)
		}
		at := base.Add(time.Duration((i*37)%mediaCount) * time.Second)
		atMS := timeToMillis(at)
		if _, err := eventsTx.ExecContext(ctx, `
			INSERT INTO playback_progress(profile_id, media_id, updated_at_ms)
			VALUES (?, ?, ?)
		`, profile, id, atMS); err != nil {
			_ = catalogTx.Rollback()
			_ = eventsTx.Rollback()
			t.Fatal(err)
		}
		if _, err := eventsTx.ExecContext(ctx, `
			INSERT INTO media_favorite(profile_id, media_id, favorited_at_ms)
			VALUES (?, ?, ?)
		`, profile, id, atMS); err != nil {
			_ = catalogTx.Rollback()
			_ = eventsTx.Rollback()
			t.Fatal(err)
		}
		queueRows = append(queueRows, queueEntry{mediaID: id, addedAtMS: atMS})
		for rank, kind := range []MyEngagementEventKind{MyEventPlay, MyEventFavorite, MyEventQueue} {
			expected = append(expected, myEngagementRankedEvent{
				rank: rank,
				event: MyEngagementEvent{
					Kind: kind, MediaID: id, LibraryID: "library-local", OccurredAt: at,
				},
			})
		}
	}
	if err := replaceQueue(ctx, eventsTx, profile, queueRows); err != nil {
		_ = catalogTx.Rollback()
		_ = eventsTx.Rollback()
		t.Fatal(err)
	}
	if err := catalogTx.Commit(); err != nil {
		_ = eventsTx.Rollback()
		t.Fatal(err)
	}
	if err := eventsTx.Commit(); err != nil {
		t.Fatal(err)
	}

	var after MediaID
	chunks := 0
	for {
		ids, events, err := st.myEngagementChunkLocked(ctx, profile, after, MyEngagementLogQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			break
		}
		if len(ids) > myEngagementLogIDChunk || len(events) > myEngagementLogIDChunk*3 {
			t.Fatalf("chunk %d materialized %d ids / %d events", chunks, len(ids), len(events))
		}
		after = ids[len(ids)-1]
		chunks++
	}
	if chunks != 3 {
		t.Fatalf("chunks = %d, want 3", chunks)
	}

	sort.Slice(expected, func(i, j int) bool { return myEngagementBefore(expected[i], expected[j]) })
	query := MyEngagementLogQuery{Limit: 37, Offset: 511}
	page, err := st.MyEngagementLog(ctx, profile, LibraryFilter{AllLibraries: true}, query)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != len(expected) || len(page.Events) != query.Limit {
		t.Fatalf("page = total %d / %d events, want %d / %d", page.Total, len(page.Events), len(expected), query.Limit)
	}
	for i, got := range page.Events {
		want := expected[query.Offset+i].event
		if got.Kind != want.Kind || got.MediaID != want.MediaID || got.LibraryID != want.LibraryID || !got.OccurredAt.Equal(want.OccurredAt) {
			t.Fatalf("event %d = %#v, want %#v", i, got, want)
		}
	}
}

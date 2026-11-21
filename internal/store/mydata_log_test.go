package store

import (
	"context"
	"database/sql"
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
		queueRows = append(queueRows, queueEntry{id: string(id), mediaID: id, addedAtMS: atMS})
		for rank, kind := range []MyEngagementEventKind{MyEventPlay, MyEventFavorite, MyEventQueue} {
			var entryID string
			if kind == MyEventQueue {
				entryID = string(id)
			}
			expected = append(expected, myEngagementRankedEvent{
				rank: rank, entryID: entryID,
				event: MyEngagementEvent{
					Kind: kind, MediaID: id, LibraryID: "library-local", OccurredAt: at,
				},
			})
		}
	}
	// More than one chunk for a single Media, including entries with the
	// same timestamp, requires the keyset to advance by entry ID.
	for i := range myEngagementLogChunkSize + 1 {
		duplicate := queueRows[0]
		duplicate.id += fmt.Sprintf("-%04d", i)
		duplicate.addedAtMS -= int64(i / 2)
		queueRows = append(queueRows, duplicate)
		expected = append(expected, myEngagementRankedEvent{
			rank: 2, entryID: duplicate.id,
			event: MyEngagementEvent{
				Kind: MyEventQueue, MediaID: duplicate.mediaID, LibraryID: "library-local",
				OccurredAt: millisToTime(duplicate.addedAtMS),
			},
		})
	}
	if err := saveQueue(ctx, eventsTx, profile, queueSnapshot{entries: queueRows, repeat: QueueRepeatOff}, nil); err != nil {
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

	readTx, err := st.eventsR.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer readTx.Rollback()
	var after myEngagementRankedEvent
	seen := make(map[string]bool, len(queueRows))
	chunks, total := 0, 0
	for {
		ids, events, err := myEngagementChunk(ctx, readTx, profile, after, MyEngagementLogQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			break
		}
		if len(ids) > myEngagementLogChunkSize || len(events) > myEngagementLogChunkSize {
			t.Fatalf("chunk %d materialized %d ids / %d events", chunks, len(ids), len(events))
		}
		for _, event := range events {
			if event.event.Kind != MyEventQueue {
				continue
			}
			if event.entryID == "" || seen[event.entryID] {
				t.Fatalf("missing or repeated cursor entry ID %q", event.entryID)
			}
			seen[event.entryID] = true
		}
		after = events[len(events)-1]
		chunks++
		total += len(events)
	}
	if want := (len(expected) + myEngagementLogChunkSize - 1) / myEngagementLogChunkSize; chunks != want || total != len(expected) || len(seen) != len(queueRows) {
		t.Fatalf("scanned %d chunks, %d events, %d queue entries; want %d, %d, %d", chunks, total, len(seen), want, len(expected), len(queueRows))
	}
	if err := readTx.Rollback(); err != nil {
		t.Fatal(err)
	}

	sort.Slice(expected, func(i, j int) bool { return myEngagementBefore(expected[i], expected[j]) })
	query := MyEngagementLogQuery{Limit: 37, Offset: len(expected) - 463}
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

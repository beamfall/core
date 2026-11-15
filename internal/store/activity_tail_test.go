package store

import (
	"context"
	"testing"
	"time"
)

func TestActivityEntriesAfterTailsUnscopedActions(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	latest, err := st.LatestActivityID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(100, 0).UTC()
	appendActivity(t, st, ActivityEntry{OccurredAt: at, Action: ActivityProfileSwitch, ProfileID: "profile-default"})
	appendActivity(t, st, ActivityEntry{OccurredAt: at, Action: ActivityRelock, ProfileID: "profile-default"})
	appendActivity(t, st, ActivityEntry{OccurredAt: at, Action: ActivityProfileSwitch, ProfileID: "profile-default", LibraryID: "library-default"})
	appendActivity(t, st, ActivityEntry{OccurredAt: at, Action: ActivityProfileSwitch, ProfileID: "profile-default", DeviceID: "device-2"})

	entries, err := st.ActivityEntriesAfter(ctx, latest, []string{ActivityProfileSwitch}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID >= entries[1].ID || entries[1].DeviceID != "device-2" {
		t.Fatalf("entries = %+v, want the two unscoped profile switches oldest first", entries)
	}
	for _, entry := range entries {
		if entry.LibraryID != "" || entry.Action != ActivityProfileSwitch {
			t.Fatalf("unexpected entry %+v", entry)
		}
	}
	if page, _ := st.ActivityEntriesAfter(ctx, latest, []string{ActivityProfileSwitch}, 1); len(page) != 1 || page[0].ID != entries[0].ID {
		t.Fatalf("limit must page from the oldest: %+v", page)
	}
	if none, _ := st.ActivityEntriesAfter(ctx, latest, nil, 10); len(none) != 0 {
		t.Fatal("no actions selects nothing")
	}
	newest, err := st.LatestActivityID(ctx)
	if err != nil || newest != entries[1].ID {
		t.Fatalf("LatestActivityID = %d, %v; want %d", newest, err, entries[1].ID)
	}
}

func TestLatestEventSeqMatchesOutboxTail(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	seq, err := st.LatestEventSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.ReadEventOutbox(ctx, seq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("nothing may follow the latest seq, got %d events", len(events))
	}
	if seq > 0 {
		before, err := st.ReadEventOutbox(ctx, seq-1, 10)
		if err != nil || len(before) != 1 || before[0].Seq != seq {
			t.Fatalf("latest seq %d is not the outbox tail: %+v, %v", seq, before, err)
		}
	}
}

package store

import (
	"context"
	"errors"
	"testing"
)

// TestQueueReplaceWithStationConfirmationAndHiddenPreservation pins the
// materialization contract (ADR-0098 area 5): replacing a non-empty VISIBLE
// queue needs confirmation; hidden-but-queued entries are PRESERVED through a
// replace (a relocked session must never destroy or learn about queue state it
// cannot read); and a confirmed replace installs exactly the generated window in
// order.
func TestQueueReplaceWithStationConfirmationAndHiddenPreservation(t *testing.T) {
	// feature:music-stations
	st := openSeeded(t)
	ctx := context.Background()
	revealed := profileDefaultFilter(true)
	relocked := profileDefaultFilter(false)

	// Seed a queue with a VISIBLE item (media-alpha, library-local) and a HIDDEN
	// item (media-archive, library-archive — only visible when revealed).
	if err := st.QueueAdd(ctx, "profile-default", "media-archive", nil, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-default", "media-alpha", nil, revealed); err != nil {
		t.Fatal(err)
	}

	// From a RELOCKED session, media-archive is hidden. Its only visible item is
	// media-alpha, so a replace without confirmation is refused.
	err := st.QueueReplaceWithStation(ctx, "profile-default", []MediaID{"media-alpha"}, false, relocked)
	if !errors.Is(err, ErrQueueReplaceNeedsConfirm) {
		t.Fatalf("replace without confirm = %v, want ErrQueueReplaceNeedsConfirm", err)
	}

	// Confirm the replace from the relocked session. The hidden media-archive
	// must SURVIVE; the visible window is media-alpha (the only visible candidate
	// the relocked filter admits). Order: hidden entries kept at front.
	if err := st.QueueReplaceWithStation(ctx, "profile-default", []MediaID{"media-alpha"}, true, relocked); err != nil {
		t.Fatal(err)
	}
	// A revealed read sees both: the preserved hidden archive AND the new alpha.
	items, err := st.SourceItems(ctx, revealed, SourceQuery{Kind: "queue", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	ids := sourceIDs(items)
	if !containsID(ids, "media-archive") {
		t.Fatalf("hidden media-archive was destroyed by replace: %v", ids)
	}
	if !containsID(ids, "media-alpha") {
		t.Fatalf("station window missing media-alpha: %v", ids)
	}

	// An EMPTY visible queue needs no confirmation. Clear the visible queue, then
	// a replace with confirmReplace=false succeeds.
	if err := st.QueueClear(ctx, "profile-default", relocked); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueReplaceWithStation(ctx, "profile-default", []MediaID{"media-alpha"}, false, relocked); err != nil {
		t.Fatalf("replace of empty visible queue = %v, want nil", err)
	}
}

// TestQueueReplaceWithStationDropsInvisibleAndDedupes pins the defensive filter:
// a stale window can never inject an item the caller cannot itself see, and
// duplicate ids collapse.
func TestQueueReplaceWithStationDropsInvisibleAndDedupes(t *testing.T) {
	// feature:music-stations
	st := openSeeded(t)
	ctx := context.Background()
	relocked := profileDefaultFilter(false)

	// media-archive is invisible to a relocked session; media-alpha is visible and
	// appears twice. The installed queue must be exactly [media-alpha].
	err := st.QueueReplaceWithStation(ctx, "profile-default",
		[]MediaID{"media-alpha", "media-archive", "media-alpha"}, true, relocked)
	if err != nil {
		t.Fatal(err)
	}
	items, err := st.SourceItems(ctx, relocked, SourceQuery{Kind: "queue", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	ids := sourceIDs(items)
	if len(ids) != 1 || ids[0] != "media-alpha" {
		t.Fatalf("queue = %v, want [media-alpha] (invisible dropped, dupes collapsed)", ids)
	}
}

func sourceIDs(items []SourceItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = string(it.ID)
	}
	return out
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

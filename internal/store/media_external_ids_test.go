package store

import (
	"context"
	"testing"
)

// TestAddMediaExternalIDAnnouncesNewIdentifierOnce pins CORE-1698: attaching an
// identifier the Media did not carry emits one media.updated, so an integration
// watching for it sees hydration's late attach; re-attaching it emits nothing.
func TestAddMediaExternalIDAnnouncesNewIdentifierOnce(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	before := mediaUpdatedEvents(t, st, "media-alpha")

	for range 2 {
		if err := st.AddMediaExternalID(ctx, "media-alpha", "example.db", "item-1"); err != nil {
			t.Fatalf("AddMediaExternalID: %v", err)
		}
	}
	if got := mediaUpdatedEvents(t, st, "media-alpha") - before; got != 1 {
		t.Fatalf("media.updated events after attaching one identifier twice = %d, want 1", got)
	}
}

func TestMediaExternalIDsBatchGroupsByMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, add := range []struct {
		media         MediaID
		scheme, value string
	}{
		{"media-alpha", "example.db", "item-2"},
		{"media-alpha", "another.db", "a-9"},
		{"media-archive", "example.db", "item-3"},
	} {
		if err := st.AddMediaExternalID(ctx, add.media, add.scheme, add.value); err != nil {
			t.Fatalf("AddMediaExternalID: %v", err)
		}
	}
	got, err := st.MediaExternalIDsBatch(ctx, []MediaID{"media-alpha", "media-archive", "media-missing"})
	if err != nil {
		t.Fatalf("MediaExternalIDsBatch: %v", err)
	}
	alpha := got["media-alpha"]
	if len(alpha) != 2 || alpha[0] != (MediaExternalID{Scheme: "another.db", Value: "a-9"}) || alpha[1] != (MediaExternalID{Scheme: "example.db", Value: "item-2"}) {
		t.Fatalf("media-alpha ids = %#v, want scheme/value order", alpha)
	}
	if len(got["media-archive"]) != 1 {
		t.Fatalf("media-archive ids = %#v, want one", got["media-archive"])
	}
	if _, ok := got["media-missing"]; ok {
		t.Fatalf("media without identifiers must be absent, got %#v", got["media-missing"])
	}
	if empty, err := st.MediaExternalIDsBatch(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty batch = %#v, %v; want empty map", empty, err)
	}
}

func mediaUpdatedEvents(t *testing.T, st *Store, mediaID MediaID) int {
	t.Helper()
	events, err := st.ReadEventOutbox(context.Background(), 0, 10000)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Kind == MediaEventUpdated && event.AggregateID == string(mediaID) {
			count++
		}
	}
	return count
}

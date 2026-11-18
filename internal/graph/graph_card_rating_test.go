package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestCardsAndMediaCarryRating covers CORE-2398: cards and the media(id) read
// carry the display certification (certification-display over certification)
// and the 0-10 community score, omit both keys for an unrated item, and drop
// a score off the 0-10 scale.
func TestCardsAndMediaCarryRating(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.UpsertMediaFields(ctx, "media-alpha", []store.MediaField{
		{Name: "certification", Type: store.EntityFieldText, Text: "TV-14"},
		{Name: "certification-display", Type: store.EntityFieldText, Text: "TV-MA"},
		{Name: "community-rating", Type: store.EntityFieldNumber, Number: 7.3},
	}); err != nil {
		t.Fatalf("UpsertMediaFields alpha: %v", err)
	}
	if err := st.UpsertMediaFields(ctx, "media-archive", []store.MediaField{
		{Name: "certification", Type: store.EntityFieldText, Text: "PG-13"},
		{Name: "community-rating", Type: store.EntityFieldNumber, Number: 73},
	}); err != nil {
		t.Fatalf("UpsertMediaFields archive: %v", err)
	}
	handler := newTestHandlerWithStore(st)

	data, errs := post(t, handler, `{"query":"query { sourceItems { id certification audienceScore } }","variables":{"kind":"recently-added"}}`, authorizeRevealed)
	if len(errs) != 0 {
		t.Fatalf("sourceItems errors = %#v", errs)
	}
	cards := cardsByID(t, data["sourceItems"])
	assertRating(t, "card media-alpha", cards["media-alpha"], `"TV-MA"`, "7.3")
	assertRating(t, "card media-beta", cards["media-archive"], `"PG-13"`, "")
	for id, card := range cards {
		if id == "media-alpha" || id == "media-archive" {
			continue
		}
		assertRating(t, "card "+id, card, "", "")
	}

	data, errs = post(t, handler, `{"query":"query { media(id: \"media-alpha\") { id certification audienceScore } }","variables":{"id":"media-alpha"}}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("media errors = %#v", errs)
	}
	var media map[string]json.RawMessage
	mustUnmarshal(t, data["media"], &media)
	assertRating(t, "media media-alpha", media, `"TV-MA"`, "7.3")
}

func assertRating(t *testing.T, label string, item map[string]json.RawMessage, certification, score string) {
	t.Helper()
	if item == nil {
		t.Fatalf("%s missing", label)
	}
	for key, want := range map[string]string{"certification": certification, "audienceScore": score} {
		got, ok := item[key]
		if want == "" {
			if ok {
				t.Fatalf("%s %s = %s, want the key omitted", label, key, got)
			}
			continue
		}
		if string(got) != want {
			t.Fatalf("%s %s = %s, want %s", label, key, got, want)
		}
	}
}

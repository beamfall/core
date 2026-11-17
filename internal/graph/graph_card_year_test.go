package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// cardsByID decodes a card list into raw objects keyed by id, so a test can
// tell an omitted key from a zero value.
func cardsByID(t *testing.T, raw json.RawMessage) map[string]map[string]json.RawMessage {
	t.Helper()
	var cards []map[string]json.RawMessage
	mustUnmarshal(t, raw, &cards)
	out := make(map[string]map[string]json.RawMessage, len(cards))
	for _, card := range cards {
		var id string
		mustUnmarshal(t, card["id"], &id)
		out[id] = card
	}
	return out
}

func setMediaYear(t *testing.T, st *store.Store, id store.MediaID, year int) {
	t.Helper()
	if err := st.UpdateMediaMetadata(context.Background(), id, store.MediaMetadata{Year: &year}); err != nil {
		t.Fatalf("UpdateMediaMetadata %s: %v", id, err)
	}
}

// TestCardsCarryYear covers CORE-2269: a card states its item's year, a card
// whose item has none omits the key, and the restricted rules are untouched.
func TestCardsCarryYear(t *testing.T) {
	st := storetest.Open(t)
	// media-alpha (visible library) gets a year; media-archive (restricted
	// library) has none.
	setMediaYear(t, st, "media-alpha", 2019)
	handler := newTestHandlerWithStore(st)

	const sourceItemsQuery = `{"query":"query { sourceItems { id year restricted locked } }","variables":{"kind":"recently-added"}}`

	data, errs := post(t, handler, sourceItemsQuery, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("sourceItems errors = %#v", errs)
	}
	cards := cardsByID(t, data["sourceItems"])
	if got := string(cards["media-alpha"]["year"]); got != "2019" {
		t.Fatalf("media-alpha year = %q, want 2019; card = %v", got, cards["media-alpha"])
	}
	if _, ok := cards["media-archive"]; ok {
		t.Fatal("unrevealed sourceItems returned the restricted media-archive")
	}
	for id, card := range cards {
		if _, ok := card["restricted"]; ok {
			t.Fatalf("unrevealed card %s carries a restricted key: %v", id, card)
		}
	}

	data, errs = post(t, handler, sourceItemsQuery, authorizeRevealed)
	if len(errs) != 0 {
		t.Fatalf("revealed sourceItems errors = %#v", errs)
	}
	cards = cardsByID(t, data["sourceItems"])
	if got := string(cards["media-alpha"]["year"]); got != "2019" {
		t.Fatalf("revealed media-alpha year = %q, want 2019", got)
	}
	archive, ok := cards["media-archive"]
	if !ok {
		t.Fatal("revealed sourceItems omitted media-archive")
	}
	if year, ok := archive["year"]; ok {
		t.Fatalf("media-archive year = %s, want the key omitted for an item with no year", year)
	}
	if got := string(archive["restricted"]); got != "true" {
		t.Fatalf("revealed media-archive restricted = %q, want true", got)
	}

	data, errs = post(t, handler,
		`{"query":"query($query: String!) { search(query: $query) { id year } }","variables":{"query":"Sample"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("search errors = %#v", errs)
	}
	if got := string(cardsByID(t, data["search"])["media-alpha"]["year"]); got != "2019" {
		t.Fatalf("search media-alpha year = %q, want 2019", got)
	}

	data, errs = post(t, handler,
		`{"query":"query($libraryID: String!) { libraryItems(libraryID: $libraryID) { items { id year } } }","variables":{"libraryID":"library-local"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("libraryItems errors = %#v", errs)
	}
	var browse struct {
		Items json.RawMessage `json:"items"`
	}
	mustUnmarshal(t, data["libraryItems"], &browse)
	if got := string(cardsByID(t, browse.Items)["media-alpha"]["year"]); got != "2019" {
		t.Fatalf("libraryItems media-alpha year = %q, want 2019", got)
	}
}

// TestSemanticSourceItemsCarryYearAndClass covers the one card path that does
// not run the per-card media read: sourceItems in semantic mode.
func TestSemanticSourceItemsCarryYearAndClass(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	vec := []float32{1, 0, 0}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/m/semantic.mp4", Title: "Semantic Hit",
		Container: "mp4", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: "sha256:semantic",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, "sha256:semantic", "text-v1", vec); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}
	setMediaYear(t, st, media.ID, 1987)
	handler := newTestHandlerWithStore(st)
	handler.SetQueryEmbedder(stubEmbedder{kind: "text-v1", available: true, vector: vec})

	data, errs := post(t, handler,
		`{"query":"query { sourceItems { id year mediaClass } }","variables":{"kind":"all-items","query":"anything","mode":"semantic"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("semantic sourceItems errors = %#v", errs)
	}
	card, ok := cardsByID(t, data["sourceItems"])[string(media.ID)]
	if !ok {
		t.Fatalf("semantic sourceItems omitted the hit: %s", data["sourceItems"])
	}
	if got := string(card["year"]); got != "1987" {
		t.Errorf("semantic card year = %q, want 1987", got)
	}
	if got := string(card["mediaClass"]); got != `"video"` {
		t.Errorf("semantic card mediaClass = %s, want \"video\"", got)
	}
}

// TestSourceItemYearLeavesRestrictedAndLockedAlone pins the wire mapping: the
// year rides beside the privacy flags and changes none of them.
func TestSourceItemYearLeavesRestrictedAndLockedAlone(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	item := store.SourceItem{ID: "media-alpha", Kind: "media", Restricted: true, Locked: true, Year: 2019}

	encode := func(revealed bool) map[string]json.RawMessage {
		data, err := json.Marshal(h.sourceItem(item, revealed))
		if err != nil {
			t.Fatal(err)
		}
		var card map[string]json.RawMessage
		mustUnmarshal(t, data, &card)
		return card
	}

	unrevealed := encode(false)
	if _, ok := unrevealed["restricted"]; ok {
		t.Fatalf("unrevealed card carries restricted: %v", unrevealed)
	}
	if string(unrevealed["locked"]) != "true" || string(unrevealed["year"]) != "2019" {
		t.Fatalf("unrevealed card = %v, want locked true and year 2019", unrevealed)
	}
	revealed := encode(true)
	if string(revealed["restricted"]) != "true" || string(revealed["locked"]) != "true" || string(revealed["year"]) != "2019" {
		t.Fatalf("revealed card = %v, want restricted, locked and year 2019", revealed)
	}

	// An item with no year, and an Entity card, have no year key at all.
	for _, plain := range []store.SourceItem{{ID: "media-alpha", Kind: "media"}, {ID: "entity-maker", Kind: "entity"}} {
		data, err := json.Marshal(h.sourceItem(plain, true))
		if err != nil {
			t.Fatal(err)
		}
		var card map[string]json.RawMessage
		mustUnmarshal(t, data, &card)
		if year, ok := card["year"]; ok {
			t.Fatalf("%s card year = %s, want the key omitted", plain.ID, year)
		}
	}
}

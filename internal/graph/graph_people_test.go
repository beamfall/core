package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestMediaCreditsCarryAttributesArtworkAndBillingOrder covers the cast row
// read (CORE-2406): each credit carries its relation attributes and the
// credited Entity's artwork, and credits come in relation sort order (the
// provider's billing order) rather than by title.
func TestMediaCreditsCarryAttributesArtworkAndBillingOrder(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "reels",
		Version:     "1.0.0",
		DisplayName: plugin.L("Reels"),
		MediaLabel:  plugin.L("Reel"),
		EntityKinds: []plugin.EntityKind{{
			Name:        "person",
			DisplayName: plugin.L("Person"),
			Archetype:   "person",
			Relations: []plugin.RelationSpec{{
				Name:       "credited-in",
				ToKind:     plugin.MediaKindName,
				Attributes: []string{"character", "role"},
			}},
		}},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	handler.SetArtwork(fakeArtworkLocator{entities: map[store.EntityID]map[string]bool{
		"entity-lead": {artwork.SlotPoster: true},
	}})
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Reels Library", false, "reels", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          library.ID,
		Path:               "/srv/reels/one.mkv",
		Title:              "Reel One",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "fp-reel-one",
		Class:              store.MediaClassVideo,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.Entity{
		{ID: "entity-lead", LibraryID: library.ID, Kind: "person", Title: "Zed Lead"},
		{ID: "entity-support", LibraryID: library.ID, Kind: "person", Title: "Amy Support"},
		{ID: "entity-uncredited", LibraryID: library.ID, Kind: "person", Title: "Bob Extra"},
	} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	first, second := 0, 1
	for _, r := range []store.EntityRelation{
		{FromEntityID: "entity-support", Name: "credited-in", ToMediaID: media.ID, SortOrder: &second,
			Attributes: map[string]string{"character": "The Friend", "role": " "}},
		{FromEntityID: "entity-lead", Name: "credited-in", ToMediaID: media.ID, SortOrder: &first,
			Attributes: map[string]string{"character": "The Hero"}},
		{FromEntityID: "entity-uncredited", Name: "credited-in", ToMediaID: media.ID},
	} {
		if err := st.UpsertEntityRelation(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got := graphData[MediaInfo](t, handler, `query($id:String!) { media(id: $id) { id credits } }`, map[string]any{"id": string(media.ID)}, "media").Credits
	person := plugin.L("Person")
	want := []MediaCreditInfo{
		{EntityID: "entity-lead", Title: "Zed Lead", Kind: "person", KindDisplayName: person, Archetype: "person", Relation: "credited-in",
			Attributes:   map[string]string{"character": "The Hero"},
			ArtworkSlots: map[string]string{artwork.SlotPoster: "/api/entity/artwork/entity-lead/poster?v=" + fakeArtworkVersion}},
		{EntityID: "entity-support", Title: "Amy Support", Kind: "person", KindDisplayName: person, Archetype: "person", Relation: "credited-in",
			Attributes: map[string]string{"character": "The Friend"}},
		{EntityID: "entity-uncredited", Title: "Bob Extra", Kind: "person", KindDisplayName: person, Archetype: "person", Relation: "credited-in"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("credits = %#v\nwant %#v", got, want)
	}

	// The person page reads the headshot from entity(id).
	lead := graphData[EntityDetailInfo](t, handler, `query($id:String!) { entity(id: $id) { id artworkSlots } }`, map[string]any{"id": "entity-lead"}, "entity")
	if lead.ArtworkSlots[artwork.SlotPoster] != "/api/entity/artwork/entity-lead/poster?v="+fakeArtworkVersion {
		t.Fatalf("entity artworkSlots = %#v, want the poster slot", lead.ArtworkSlots)
	}
	bare := graphData[EntityDetailInfo](t, handler, `query($id:String!) { entity(id: $id) { id artworkSlots } }`, map[string]any{"id": "entity-support"}, "entity")
	if bare.ArtworkSlots != nil {
		t.Fatalf("artless entity artworkSlots = %#v, want omitted", bare.ArtworkSlots)
	}
}

// graphData runs one viewer query and decodes data[field].
func graphData[T any](t *testing.T, handler *Handler, query string, variables map[string]any, field string) T {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string]T `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Data[field]
}

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

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestEntityQueryListsRelatedEntityGroups pins the entity(id) related groups
// (CORE-1210): Entity-to-Entity relations in both directions, grouped by
// kind with the kind's declared display name, items as entity Source items,
// and no groups for an Entity linked only to Media.
func TestEntityQueryListsRelatedEntityGroups(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "tunes",
		Version:     "1.0.0",
		DisplayName: plugin.L("Tunes"),
		MediaLabel:  plugin.L("Cut"),
		EntityKinds: []plugin.EntityKind{{
			Name:              "record",
			DisplayName:       plugin.L("Record"),
			PluralDisplayName: plugin.L("Records"),
			Archetype:         "container",
			OrderedChildren:   true,
			Relations: []plugin.RelationSpec{
				{Name: "record-cut", ToKind: plugin.MediaKindName},
				{Name: "record-performer", ToKind: "performer"},
			},
		}, {
			Name:        "performer",
			DisplayName: plugin.L("Performer"),
			Archetype:   "person",
			Relations:   []plugin.RelationSpec{{Name: "plays-on", ToKind: plugin.MediaKindName}},
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
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Tunes Library", false, "tunes", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          library.ID,
		Path:               "/srv/tunes/one.mp3",
		Title:              "Cut One",
		Container:          "mp3",
		Codec:              "mp3",
		ContentFingerprint: "fp-cut-one",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.Entity{
		{ID: "entity-performer", LibraryID: library.ID, Kind: "performer", Title: "The Performer"},
		{ID: "entity-solo", LibraryID: library.ID, Kind: "performer", Title: "Solo"},
		{ID: "entity-record-b", LibraryID: library.ID, Kind: "record", Title: "Second Record"},
		{ID: "entity-record-a", LibraryID: library.ID, Kind: "record", Title: "First Record"},
	} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []store.EntityRelation{
		{FromEntityID: "entity-record-a", Name: "record-performer", ToEntityID: "entity-performer"},
		{FromEntityID: "entity-record-b", Name: "record-performer", ToEntityID: "entity-performer"},
		{FromEntityID: "entity-record-a", Name: "record-cut", ToMediaID: media.ID},
		{FromEntityID: "entity-solo", Name: "plays-on", ToMediaID: media.ID},
	} {
		if err := st.UpsertEntityRelation(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	read := func(id string) EntityDetailInfo {
		t.Helper()
		body := []byte(`{"query":"query($id:String!) { entity(id: $id) { id kind title relatedEntities { kind kindDisplayName kindPluralDisplayName items { id kind title libraryID } } } }","variables":{"id":"` + id + `"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("entity status = %d: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data   map[string]EntityDetailInfo `json:"data"`
			Errors []json.RawMessage           `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) > 0 {
			t.Fatalf("entity errors: %s", rec.Body.String())
		}
		return resp.Data["entity"]
	}

	got := read("entity-performer").RelatedEntities
	want := []RelatedEntityGroupInfo{{
		Kind:                  "record",
		KindDisplayName:       plugin.L("Record"),
		KindPluralDisplayName: plugin.L("Records"),
		Archetype:             "container",
		// First Record's only Media is video, so its card carries that class.
		Items: []SourceItem{
			{ID: "entity-record-a", LibraryID: string(library.ID), Kind: "entity", Title: "First Record", MediaClass: "video"},
			{ID: "entity-record-b", LibraryID: string(library.ID), Kind: "entity", Title: "Second Record"},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("performer relatedEntities = %#v, want %#v", got, want)
	}

	got = read("entity-record-a").RelatedEntities
	want = []RelatedEntityGroupInfo{{
		Kind:            "performer",
		KindDisplayName: plugin.L("Performer"),
		Archetype:       "person",
		// The Performer has no Media of its own, so it takes the class its
		// linked Records' Media share (CORE-2889).
		Items: []SourceItem{{ID: "entity-performer", LibraryID: string(library.ID), Kind: "entity", Title: "The Performer", MediaClass: "video"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record relatedEntities = %#v, want %#v", got, want)
	}

	if record := read("entity-record-a"); record.Archetype != "container" {
		t.Fatalf("record archetype = %q, want container", record.Archetype)
	}
	if solo := read("entity-solo"); solo.ID != "entity-solo" || solo.RelatedEntities != nil {
		t.Fatalf("media-only entity = %#v, want no related groups", solo)
	}
}

func TestUnruledRelatedEntityCardsPreserveOrderAndOwningPluginArchetype(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	var bundles []plugin.Bundle
	var libraries []store.LibraryID
	for i, archetype := range []string{"container", "category"} {
		bundles = append(bundles, plugin.Bundle{
			ID: plugin.ID(archetype), Version: "1.0.0", DisplayName: plugin.L(archetype),
			MediaLabel:  plugin.L("Item"),
			EntityKinds: []plugin.EntityKind{{Name: "record", Archetype: archetype}},
			Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		})
		library, err := st.CreateLibrary(ctx, archetype, false, archetype, "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		libraries = append(libraries, library.ID)
		if i == 0 {
			if err := st.UpsertEntity(ctx, store.Entity{ID: "context", LibraryID: library.ID, Kind: "record", Title: "Context"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := []struct {
		id    store.EntityID
		owner int
	}{{"first", 0}, {"second", 1}, {"third", 0}}
	for i, card := range want {
		if err := st.UpsertEntity(ctx, store.Entity{ID: card.id, LibraryID: libraries[card.owner], Kind: "record", Title: string(card.id)}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: card.id, Name: "related", ToEntityID: "context", SortOrder: &i}); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := newTestPluginRegistry(bundles)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(manifest.NewService(st, time.Now, registry), auth.NewService(st), st, registry)
	detail, err := handler.entityQuery(ctx, auth.Scope{ProfileID: "profile-default", LibraryIDs: libraries},
		request{Variables: map[string]any{"id": "context"}})
	if err != nil {
		t.Fatal(err)
	}
	groups := detail.RelatedEntities
	if len(groups) != len(want) {
		t.Fatalf("related groups = %+v, want three consecutive owner runs", groups)
	}
	for i, group := range groups {
		archetype := bundles[want[i].owner].EntityKinds[0].Archetype
		if len(group.Items) != 1 || group.Items[0].ID != string(want[i].id) || group.Archetype != archetype || len(group.Sections) != 0 {
			t.Fatalf("group %d = %+v, want card %q with archetype %q and no sections", i, group, want[i].id, archetype)
		}
	}
}

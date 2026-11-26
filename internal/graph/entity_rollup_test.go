package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const (
	rollUpEntityQuery = "query Entity($id: String!) { entity(id: $id) { id kind title archetype relatedEntities { kind archetype items { id kind title mediaClass year byline engagement { totalCount remainingCount } } } } }"
	rollUpMediaQuery  = "query MediaForEntity($entityID: String!, $rollUp: Boolean) { mediaForEntity(entityID: $entityID, rollUp: $rollUp) { id kind title mediaClass year subtitle engagement { positionSeconds durationSeconds completed totalCount remainingCount } } }"
)

type rollUpCard struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Year       int    `json:"year"`
	Engagement *struct {
		TotalCount     int `json:"totalCount"`
		RemainingCount int `json:"remainingCount"`
	} `json:"engagement"`
}

func readMediaForEntity(t *testing.T, f rollUpFixture, entityID string, rollUp any) []rollUpCard {
	t.Helper()
	vars := map[string]any{"entityID": entityID}
	if rollUp != nil {
		vars["rollUp"] = rollUp
	}
	resp := postGraphJSON(t, f.handler, authorizeViewer, rollUpMediaQuery, vars)
	if len(resp.Errors) != 0 {
		t.Fatalf("mediaForEntity errors = %+v", resp.Errors)
	}
	var cards []rollUpCard
	if err := json.Unmarshal(resp.Data["mediaForEntity"], &cards); err != nil {
		t.Fatal(err)
	}
	return cards
}

func TestMediaForEntityRollUpListsOutermostContainers(t *testing.T) {
	f := newRollUpFixture(t, 3, 2, 2, 2)
	for _, entity := range []string{string(f.person), string(f.topic)} {
		cards := readMediaForEntity(t, f, entity, true)
		volumes := map[string]rollUpCard{}
		loose := map[string]bool{}
		for _, card := range cards {
			switch card.Kind {
			case "entity":
				if _, dup := volumes[card.ID]; dup {
					t.Fatalf("%s: volume %s listed twice", entity, card.ID)
				}
				volumes[card.ID] = card
			default:
				loose[card.ID] = true
			}
		}
		if len(volumes) != len(f.volumes) || len(loose) != len(f.loose) {
			t.Fatalf("%s: got %d container cards and %d Media, want %d and %d: %+v", entity, len(volumes), len(loose), len(f.volumes), len(f.loose), cards)
		}
		for _, volume := range f.volumes {
			card, ok := volumes[string(volume)]
			if !ok {
				t.Fatalf("%s: missing volume %s", entity, volume)
			}
			// Items are dated 2030 down to 2027 within each volume.
			if card.Year != 2027 {
				t.Errorf("%s: volume %s year = %d, want the earliest member year 2027", entity, volume, card.Year)
			}
			if card.Engagement == nil || card.Engagement.TotalCount != 4 || card.Engagement.RemainingCount != 4 {
				t.Errorf("%s: volume %s engagement = %+v, want 4 of 4 left", entity, volume, card.Engagement)
			}
		}
		for _, id := range f.loose {
			if !loose[string(id)] {
				t.Errorf("%s: missing standalone Media %s", entity, id)
			}
		}
	}
}

func TestMediaForEntityRollUpKeepsAContainersOwnChildren(t *testing.T) {
	f := newRollUpFixture(t, 1, 2, 3, 0)
	cards := readMediaForEntity(t, f, string(f.parts[0]), true)
	if len(cards) != 3 {
		t.Fatalf("part lists %d cards, want its 3 Media: %+v", len(cards), cards)
	}
	for _, card := range cards {
		if card.Kind == "entity" {
			t.Fatalf("part rolled its own children up: %+v", cards)
		}
	}
}

func TestMediaForEntityWithoutRollUpListsEveryItem(t *testing.T) {
	f := newRollUpFixture(t, 2, 2, 2, 1)
	for _, rollUp := range []any{nil, false} {
		cards := readMediaForEntity(t, f, string(f.person), rollUp)
		if len(cards) != len(f.held)+len(f.loose) {
			t.Fatalf("rollUp=%v: got %d cards, want every Media item (%d)", rollUp, len(cards), len(f.held)+len(f.loose))
		}
		for _, card := range cards {
			if card.Kind == "entity" {
				t.Fatalf("rollUp=%v: unexpected container card %+v", rollUp, card)
			}
		}
	}
}

// BenchmarkEntityPage times the two reads the web Entity page makes for a
// person credited on 2,400 held and 100 standalone Media (CORE-3058).
func BenchmarkEntityPage(b *testing.B) {
	f := newRollUpFixture(b, 40, 5, 12, 100)
	for _, read := range []struct {
		name, query string
		vars        map[string]any
	}{
		{"entity", rollUpEntityQuery, map[string]any{"id": string(f.person)}},
		{"mediaForEntity", rollUpMediaQuery, map[string]any{"entityID": string(f.person)}},
		{"mediaForEntityRollUp", rollUpMediaQuery, map[string]any{"entityID": string(f.person), "rollUp": true}},
	} {
		payload, err := json.Marshal(map[string]any{"query": read.query, "variables": read.vars})
		if err != nil {
			b.Fatal(err)
		}
		b.Run(read.name, func(b *testing.B) {
			var size int
			for b.Loop() {
				req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(payload))
				authorizeViewer(req)
				rec := httptest.NewRecorder()
				f.handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || bytes.Contains(rec.Body.Bytes(), []byte(`"errors"`)) {
					b.Fatalf("%s: status %d: %.300s", read.name, rec.Code, rec.Body.String())
				}
				size = rec.Body.Len()
			}
			b.ReportMetric(float64(size), "bytes/resp")
		})
	}
}

// TestMediaForEntityAudioCategoryRollsUpMultiItemContainers verifies that a
// category Entity page holding audio Media rolls multi-item direct containers
// (>= 2 visible children) up into Entity cards while keeping 1-item audio
// releases and person pages (track listings) unrolled.
func TestMediaForEntityAudioCategoryRollsUpMultiItemContainers(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	bundle := plugin.Bundle{
		ID:          "audiokit",
		Version:     "0.1.0",
		DisplayName: plugin.L("Audiokit"),
		MediaLabel:  plugin.L("Cut"),
		EntityKinds: []plugin.EntityKind{
			{Name: "holder", Archetype: "container", OrderedChildren: true, Relations: []plugin.RelationSpec{
				{Name: "holds-cut", ToKind: plugin.MediaKindName, Cardinality: "one"},
			}},
			{Name: "creator", Archetype: "person", Relations: []plugin.RelationSpec{
				{Name: "performed", ToKind: plugin.MediaKindName},
			}},
			{Name: "category-mood", Archetype: "category", Relations: []plugin.RelationSpec{
				{Name: "marks", ToKind: plugin.MediaKindName},
			}},
		},
		Layouts: []plugin.LayoutSpec{{
			ID:   "home",
			Role: "browse",
			Sections: []plugin.SectionSpec{
				{ID: "holders", Kind: "grid", Title: plugin.L("Holders"), Source: plugin.SourceSpec{Kind: "entity-items", EntityKind: "holder"}},
			},
		}},
	}
	registry, err := newTestPluginRegistry([]plugin.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	library, err := st.CreateLibrary(ctx, "Audio", false, "audiokit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	importCut := func(name string) store.MediaID {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/audio/" + name + ".flac", Title: name,
			Container: "flac", Codec: "flac", Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return item.ID
	}
	cut1 := importCut("Cut One")
	cut2 := importCut("Cut Two")
	solo := importCut("Solo Cut")

	for _, e := range []store.Entity{
		{ID: "entity-holder-multi", LibraryID: library.ID, Kind: "holder", Title: "Double Set"},
		{ID: "entity-holder-single", LibraryID: library.ID, Kind: "holder", Title: "Single Cut"},
		{ID: "entity-creator", LibraryID: library.ID, Kind: "creator", Title: "The Creator"},
		{ID: "entity-mood", LibraryID: library.ID, Kind: "category-mood", Title: "Chill"},
	} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []store.EntityRelation{
		{FromEntityID: "entity-holder-multi", Name: "holds-cut", ToMediaID: cut1},
		{FromEntityID: "entity-holder-multi", Name: "holds-cut", ToMediaID: cut2},
		{FromEntityID: "entity-holder-single", Name: "holds-cut", ToMediaID: solo},
		{FromEntityID: "entity-creator", Name: "performed", ToMediaID: cut1},
		{FromEntityID: "entity-creator", Name: "performed", ToMediaID: solo},
		{FromEntityID: "entity-mood", Name: "marks", ToMediaID: cut1},
		{FromEntityID: "entity-mood", Name: "marks", ToMediaID: solo},
	} {
		if err := st.UpsertEntityRelation(ctx, rel); err != nil {
			t.Fatal(err)
		}
	}

	readRolled := func(entityID string) map[string]string {
		resp := postGraphJSON(t, handler, authorizeViewer, rollUpMediaQuery, map[string]any{"entityID": entityID, "rollUp": true})
		if len(resp.Errors) != 0 {
			t.Fatalf("mediaForEntity(%s) errors = %#v", entityID, resp.Errors)
		}
		var cards []rollUpCard
		if err := json.Unmarshal(resp.Data["mediaForEntity"], &cards); err != nil {
			t.Fatal(err)
		}
		byID := make(map[string]string, len(cards))
		for _, c := range cards {
			byID[c.ID] = c.Kind
		}
		return byID
	}

	// Category page rolls multi-item holder up to entity while keeping 1-item solo as media.
	moodCards := readRolled("entity-mood")
	if len(moodCards) != 2 || moodCards["entity-holder-multi"] != "entity" || moodCards[string(solo)] != "media" {
		t.Fatalf("category rollUp = %v, want multi-holder as entity and 1-item solo as media", moodCards)
	}
	// Person page keeps individual audio cuts as media.
	personCards := readRolled("entity-creator")
	if len(personCards) != 2 || personCards[string(cut1)] != "media" || personCards[string(solo)] != "media" {
		t.Fatalf("person rollUp = %v, want individual cuts kept as media", personCards)
	}
}

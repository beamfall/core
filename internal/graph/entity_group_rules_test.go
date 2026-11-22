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

// TestEntityQuerySplitsRuledKindByField pins CORE-2769: a group rule keeps a
// related kind as one group whose sections split it by the field — Entities
// without a value first, then listed values in rule order with their
// headings, then unlisted values with none — each section newest first, and
// every card dated from its Media.
func TestEntityQuerySplitsRuledKindByField(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:           "tunes",
		Version:      "1.0.0",
		DisplayName:  plugin.L("Tunes"),
		MediaLabel:   plugin.L("Cut"),
		MediaClasses: []string{"audio"},
		EntityKinds: []plugin.EntityKind{{
			Name:              "record",
			DisplayName:       plugin.L("Record"),
			PluralDisplayName: plugin.L("Records"),
			Archetype:         "container",
			Fields:            []plugin.FieldSpec{{Name: "format", Type: "text"}, {Name: "release-date", Type: "date"}},
			Relations: []plugin.RelationSpec{
				{Name: "record-cut", ToKind: plugin.MediaKindName},
				{Name: "record-performer", ToKind: "performer"},
			},
		}, {
			Name:      "performer",
			Archetype: "person",
		}},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}, {
		ID: "plain", Version: "1.0.0", DisplayName: plugin.L("Plain"),
		MediaLabel: plugin.L("Item"), MediaClasses: []string{"audio"},
		EntityKinds: []plugin.EntityKind{{Name: "record", Archetype: "category", DisplayName: plugin.L("Plain Record")}},
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
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
	handler.entityGroupRules = map[plugin.ID]map[string]EntityGroupRule{"tunes": {"record": {
		Field: "format",
		Groups: []EntityGroupValue{
			{Value: "long", Label: plugin.L("Long Players")},
			{Value: "short", Label: plugin.L("Short Players")},
		},
	}}}
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Tunes Library", false, "tunes", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	// The page belongs to a different library with no grouping rule.
	pageLibrary, err := st.CreateLibrary(ctx, "Performers", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{ID: "entity-performer", LibraryID: pageLibrary.ID, Kind: "performer", Title: "The Performer"}); err != nil {
		t.Fatal(err)
	}
	records := []struct {
		id, title, format string
		year              int
	}{
		{"entity-a", "Alpha", "short", 2001},
		{"entity-b", "Beta", "long", 1999},
		{"entity-c", "Gamma", "long", 2010},
		{"entity-d", "Delta", "", 0},
		{"entity-e", "Epsilon", "odd", 2005},
		{"entity-f", "Zeta", "long", 0},
	}
	for _, r := range records {
		entity := store.Entity{ID: store.EntityID(r.id), LibraryID: library.ID, Kind: "record", Title: r.title}
		// The release date would reverse the order of the two dated long cards.
		switch r.id {
		case "entity-b":
			entity.Fields = append(entity.Fields, store.EntityField{Name: "release-date", Type: store.EntityFieldDate, Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})
		case "entity-c":
			entity.Fields = append(entity.Fields, store.EntityField{Name: "release-date", Type: store.EntityFieldDate, Date: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)})
		}
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
		relations := []store.EntityRelation{{FromEntityID: entity.ID, Name: "record-performer", ToEntityID: "entity-performer"}}
		if r.year != 0 || r.format != "" {
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: library.ID, Path: "/srv/tunes/" + r.id + ".flac", Title: r.title, Container: "flac", Codec: "flac",
				Class: store.MediaClassAudio, ContentFingerprint: "fp-" + r.id, Year: r.year,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertMediaFields(ctx, media.ID, []store.MediaField{{Name: "format", Type: store.EntityFieldText, Text: r.format}}); err != nil {
				t.Fatal(err)
			}
			relations = append(relations, store.EntityRelation{FromEntityID: entity.ID, Name: "record-cut", ToMediaID: media.ID})
		}
		for _, relation := range relations {
			if err := st.UpsertEntityRelation(ctx, relation); err != nil {
				t.Fatal(err)
			}
		}
	}

	body := []byte(`{"query":"query($id:String!) { entity(id: $id) { relatedEntities { kind sections { value displayName count } items { id year } } } }","variables":{"id":"entity-performer"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("entity status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Entity struct {
				RelatedEntities []struct {
					Kind     string                     `json:"kind"`
					Sections []RelatedEntitySectionInfo `json:"sections"`
					Items    []struct {
						ID   string `json:"id"`
						Year int    `json:"year"`
					} `json:"items"`
				} `json:"relatedEntities"`
			} `json:"entity"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Errors) > 0 {
		t.Fatalf("entity response %s (%v)", rec.Body.String(), err)
	}
	groups := resp.Data.Entity.RelatedEntities
	if len(groups) != 1 || groups[0].Kind != "record" {
		t.Fatalf("related groups = %+v, want one record group", groups)
	}
	var ids []string
	var years []int
	for _, item := range groups[0].Items {
		ids = append(ids, item.ID)
		years = append(years, item.Year)
	}
	wantIDs := []string{"entity-d", "entity-c", "entity-b", "entity-f", "entity-a", "entity-e"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("items = %v, want %v", ids, wantIDs)
	}
	if wantYears := []int{0, 2010, 1999, 0, 2001, 2005}; !reflect.DeepEqual(years, wantYears) {
		t.Fatalf("years = %v, want %v", years, wantYears)
	}
	wantSections := []RelatedEntitySectionInfo{
		{Value: "", Count: 1},
		{Value: "long", DisplayName: plugin.L("Long Players"), Count: 3},
		{Value: "short", DisplayName: plugin.L("Short Players"), Count: 1},
		{Value: "odd", Count: 1},
	}
	if !reflect.DeepEqual(groups[0].Sections, wantSections) {
		t.Fatalf("sections = %+v, want %+v", groups[0].Sections, wantSections)
	}

	// A second Library of the same plugin keeps its own sections, and the
	// unruled plugin retains its archetype and relation order.
	plain, err := st.CreateLibrary(ctx, "Plain Library", false, "plain", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateLibrary(ctx, "Other Tunes Library", false, "tunes", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	addRelated := func(libraryID store.LibraryID, id, title, value string, order, year int) {
		t.Helper()
		entity := store.Entity{ID: store.EntityID(id), LibraryID: libraryID, Kind: "record", Title: title}
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: entity.ID, Name: "record-performer", ToEntityID: "entity-performer", SortOrder: &order,
		}); err != nil {
			t.Fatal(err)
		}
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: libraryID, Path: "/srv/tunes/" + id + ".flac", Title: title, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + id, Year: year,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertMediaFields(ctx, media.ID, []store.MediaField{{Name: "format", Type: store.EntityFieldText, Text: value}}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: entity.ID, Name: "record-cut", ToMediaID: media.ID}); err != nil {
			t.Fatal(err)
		}
	}
	addRelated(plain.ID, "plain-first", "Z", "long", 1, 1990)
	addRelated(second.ID, "other-long", "Other Long", "long", 2, 2020)
	addRelated(plain.ID, "plain-last", "A", "short", 3, 2015)
	detail, err := handler.entityQuery(ctx, auth.Scope{ProfileID: "profile-default", LibraryIDs: []store.LibraryID{
		plain.ID, second.ID, library.ID, pageLibrary.ID,
	}}, request{Variables: map[string]any{"id": "entity-performer"}})
	if err != nil || len(detail.RelatedEntities) != 4 {
		t.Fatalf("mixed owners = %+v, err = %v; want four groups", detail.RelatedEntities, err)
	}
	mixed := detail.RelatedEntities
	if got := mixed[0]; got.Archetype != "category" || len(got.Sections) != 0 || len(got.Items) != 1 || got.Items[0].ID != "plain-first" {
		t.Fatalf("first unruled run = %+v, want plain-first with category archetype", got)
	}
	if got := mixed[1]; got.Archetype != "container" || len(got.Items) != 1 || got.Items[0].ID != "other-long" ||
		len(got.Sections) != 1 || got.Sections[0].Value != "long" {
		t.Fatalf("second Library group = %+v, want its own long section", got)
	}
	if got := mixed[2]; got.Archetype != "category" || len(got.Sections) != 0 || len(got.Items) != 1 || got.Items[0].ID != "plain-last" {
		t.Fatalf("second unruled run = %+v, want plain-last with category archetype", got)
	}
	if got := mixed[3]; !reflect.DeepEqual(got.Sections, wantSections) {
		t.Fatalf("first Library sections = %+v, want %+v", got.Sections, wantSections)
	}
}

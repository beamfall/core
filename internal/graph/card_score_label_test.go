package graph

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestScoredCardsNameTheScoreSourceAndVotes pins CORE-2415: a scored Media
// item in a plugin-bound Library carries the plugin's label for the score
// field and the vote count on its card, its search card and its media(id)
// read; an unscored item carries neither, and a missing vote count is omitted.
func TestScoredCardsNameTheScoreSourceAndVotes(t *testing.T) {
	scoreLabel := plugin.LocaleString{"en": "Critic rating", "fr": "Note critique"}
	bundle := plugin.Bundle{
		ID:          "scorekit",
		Version:     "0.1.0",
		DisplayName: plugin.L("Scorekit"),
		MediaLabel:  plugin.L("Item"),
		MediaFields: []plugin.FieldSpec{
			{Name: store.AudienceScoreField, Type: "number", Label: scoreLabel},
			{Name: "community-rating-votes", Type: "number", Label: plugin.L("Critic votes")},
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "all", Kind: "grid", Title: plugin.L("All"), ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "all-items"}},
		}}},
	}
	st := storetest.Open(t)
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
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Scored", false, "scorekit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string][]store.MediaField{
		"voted": {
			{Name: store.AudienceScoreField, Type: store.EntityFieldNumber, Number: 7.3},
			{Name: "community-rating-votes", Type: store.EntityFieldNumber, Number: 1200},
		},
		"unvoted":  {{Name: store.AudienceScoreField, Type: store.EntityFieldNumber, Number: 6.1}},
		"unscored": {{Name: "community-rating-votes", Type: store.EntityFieldNumber, Number: 40}},
	}
	media := map[string]store.MediaID{}
	for name, values := range fields {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/scored/" + name + ".mkv", Title: "Scored Title",
			Container: "mkv", Codec: "h264", ContentFingerprint: "fp-" + name, DurationSecondsHint: 60,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaFields(ctx, item.ID, values); err != nil {
			t.Fatal(err)
		}
		media[name] = item.ID
	}
	check := func(surface, name string, label plugin.LocaleString, votes int) {
		t.Helper()
		switch want := fields[name]; {
		case name == "unscored":
			if len(label) != 0 || votes != 0 {
				t.Fatalf("%s %s = %v, %d; want no label and no votes", surface, name, label, votes)
			}
		case label.Resolve("fr") != "Note critique" || label.Resolve("en") != "Critic rating":
			t.Fatalf("%s %s label = %v, want %v", surface, name, label, scoreLabel)
		case name == "voted" && votes != 1200, name == "unvoted" && votes != 0:
			t.Fatalf("%s %s votes = %d (fields %v)", surface, name, votes, want)
		}
	}

	var page LibraryItemsResponse
	execGraph(t, handler, authorizeViewer,
		`query($libraryID: String!) { libraryItems(libraryID: $libraryID, limit: 50) { items { id audienceScoreLabel audienceScoreVotes } } }`,
		map[string]any{"libraryID": string(library.ID)}, "libraryItems", &page)
	cards := map[string]SourceItem{}
	for _, item := range page.Items {
		cards[item.ID] = item
	}
	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id audienceScoreLabel audienceScoreVotes } } }`,
		map[string]any{"query": "Scored Title"}, "searchResult", &search)
	rows := map[string]SearchResultItem{}
	for _, item := range search.Items {
		rows[item.ID] = item
	}
	for name, id := range media {
		card, ok := cards[string(id)]
		if !ok {
			t.Fatalf("card %s missing from %+v", name, page.Items)
		}
		check("card", name, card.AudienceScoreLabel, card.AudienceScoreVotes)
		row, ok := rows[string(id)]
		if !ok {
			t.Fatalf("search row %s missing from %+v", name, search.Items)
		}
		check("search", name, row.AudienceScoreLabel, row.AudienceScoreVotes)
		var info MediaInfo
		execGraph(t, handler, authorizeViewer,
			`query($id: String!) { media(id: $id) { id audienceScoreLabel audienceScoreVotes } }`,
			map[string]any{"id": string(id)}, "media", &info)
		check("media", name, info.AudienceScoreLabel, info.AudienceScoreVotes)
	}
}

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

// TestMediaCardsCarryThePluginSubtitleAndOrdinals pins CORE-1226 and
// CORE-2386: a Media card carries its stored ordinals and the Library
// plugin's MediaSubtitle rendered per declared locale, dropping a segment
// whose value is missing; core never reads the template's words.
func TestMediaCardsCarryThePluginSubtitleAndOrdinals(t *testing.T) {
	bundle := plugin.Bundle{
		ID:          "volumekit",
		Version:     "0.1.0",
		DisplayName: plugin.L("Volumekit"),
		MediaLabel:  plugin.L("Part"),
		MediaFields: []plugin.FieldSpec{{Name: "part-title", Type: "text", Label: plugin.L("Part title")}},
		MediaSubtitle: []plugin.LocaleString{
			{"en": "Vol {sequence}", "fr": "Tome {sequence}"},
			plugin.L("No {index}"),
			plugin.L("{part-title}"),
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
	library, err := st.CreateLibrary(ctx, "Volumes", false, "volumekit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string][]store.MediaField{
		"full": {
			{Name: "sequence", Type: store.EntityFieldNumber, Number: 5},
			{Name: "index", Type: store.EntityFieldNumber, Number: 3},
			{Name: "part-title", Type: store.EntityFieldText, Text: "Reunion"},
		},
		"untitled": {
			{Name: "sequence", Type: store.EntityFieldNumber, Number: 5},
			{Name: "index", Type: store.EntityFieldNumber, Number: 4},
		},
		"bare": nil,
	}
	media := map[string]store.MediaID{}
	for name, values := range fields {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/volumes/" + name + ".mkv", Title: "Same Title",
			Container: "mkv", Codec: "h264", ContentFingerprint: "fp-" + name, DurationSecondsHint: 1800,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaFields(ctx, item.ID, values); err != nil {
			t.Fatal(err)
		}
		media[name] = item.ID
	}

	var page LibraryItemsResponse
	execGraph(t, handler, authorizeViewer,
		`query($libraryID: String!) { libraryItems(libraryID: $libraryID, limit: 50) { items { id subtitle sequence index durationSeconds } } }`,
		map[string]any{"libraryID": string(library.ID)}, "libraryItems", &page)
	cards := map[string]SourceItem{}
	for _, item := range page.Items {
		cards[item.ID] = item
	}
	full := cards[string(media["full"])]
	if full.Subtitle.Resolve("en") != "Vol 5 · No 3 · Reunion" || full.Subtitle.Resolve("fr") != "Tome 5 · No 3 · Reunion" {
		t.Fatalf("full subtitle = %v", full.Subtitle)
	}
	if full.Sequence != 5 || full.Index != 3 || full.DurationSeconds != 1800 {
		t.Fatalf("full ordinals = %v, %v", full.Sequence, full.Index)
	}
	untitled := cards[string(media["untitled"])]
	if untitled.Subtitle.Resolve("en") != "Vol 5 · No 4" || untitled.Index != 4 {
		t.Fatalf("untitled card = %+v, want the title segment dropped", untitled)
	}
	bare := cards[string(media["bare"])]
	if len(bare.Subtitle) != 0 || bare.Sequence != 0 || bare.Index != 0 {
		t.Fatalf("bare card = %+v, want no subtitle or ordinals", bare)
	}

	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id subtitle index } } }`,
		map[string]any{"query": "Same Title"}, "searchResult", &search)
	found := false
	for _, item := range search.Items {
		if item.ID == string(media["full"]) {
			found = item.Subtitle.Resolve("en") == "Vol 5 · No 3 · Reunion" && item.Index == 3
		}
	}
	if !found {
		t.Fatalf("search rows = %+v, want the full card with its subtitle", search.Items)
	}
}

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

// TestPlaceholderIconFollowsThePluginDeclaration pins CORE-3336: Media in a
// Library whose plugin declares LibraryDefaults.Icon carry it as
// placeholderIcon on their card, their search card and their media(id) read,
// and the Library defaults to that icon; Media in a Library whose plugin
// declares none, and in a plugin-less Library, omit it.
func TestPlaceholderIconFollowsThePluginDeclaration(t *testing.T) {
	bundleFor := func(id plugin.ID, icon string) plugin.Bundle {
		return plugin.Bundle{
			ID:              id,
			Version:         "0.1.0",
			DisplayName:     plugin.L(string(id)),
			MediaLabel:      plugin.L("Item"),
			MediaClasses:    []string{"audio"},
			LibraryDefaults: plugin.LibraryDefaults{Icon: icon},
			Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "all", Kind: "grid", Title: plugin.L("All"), ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "all-items"}},
			}}},
		}
	}
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{bundleFor("mickit", "mic"), bundleFor("notekit", "")})
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
	want := map[string]string{"mickit": "mic", "notekit": "", "": ""}
	wantLibraryIcon := map[string]string{"mickit": "mic", "notekit": "note"}
	media := map[string]store.MediaID{}
	libraries := map[string]store.LibraryID{}
	created := map[string]store.Library{}
	for pluginID := range want {
		library, err := st.CreateLibrary(ctx, "Lib "+pluginID, false, pluginID, "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/glyph/" + pluginID + "x.flac", Title: "Glyph Title",
			Container: "flac", Codec: "flac", ContentFingerprint: "fp-" + pluginID, DurationSecondsHint: 60,
		})
		if err != nil {
			t.Fatal(err)
		}
		media[pluginID], libraries[pluginID], created[pluginID] = item.ID, library.ID, library
	}

	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id placeholderIcon } } }`,
		map[string]any{"query": "Glyph Title"}, "searchResult", &search)
	rows := map[string]SearchResultItem{}
	for _, item := range search.Items {
		rows[item.ID] = item
	}
	for pluginID, id := range media {
		var page LibraryItemsResponse
		execGraph(t, handler, authorizeViewer,
			`query($libraryID: String!) { libraryItems(libraryID: $libraryID, limit: 50) { items { id placeholderIcon } } }`,
			map[string]any{"libraryID": string(libraries[pluginID])}, "libraryItems", &page)
		if len(page.Items) != 1 || page.Items[0].ID != string(id) {
			t.Fatalf("plugin %q libraryItems = %+v, want the one item %s", pluginID, page.Items, id)
		}
		if got := page.Items[0].PlaceholderIcon; got != want[pluginID] {
			t.Errorf("plugin %q card placeholderIcon = %q, want %q", pluginID, got, want[pluginID])
		}
		row, ok := rows[string(id)]
		if !ok {
			t.Fatalf("plugin %q search row missing from %+v", pluginID, search.Items)
		}
		if row.PlaceholderIcon != want[pluginID] {
			t.Errorf("plugin %q search placeholderIcon = %q, want %q", pluginID, row.PlaceholderIcon, want[pluginID])
		}
		var info MediaInfo
		execGraph(t, handler, authorizeViewer,
			`query($id: String!) { media(id: $id) { id placeholderIcon } }`,
			map[string]any{"id": string(id)}, "media", &info)
		if info.PlaceholderIcon != want[pluginID] {
			t.Errorf("plugin %q media(id) placeholderIcon = %q, want %q", pluginID, info.PlaceholderIcon, want[pluginID])
		}
		if wantIcon, ok := wantLibraryIcon[pluginID]; ok {
			got, err := handler.libraryInfo(ctx, created[pluginID])
			if err != nil {
				t.Fatal(err)
			}
			if got.Icon != wantIcon || got.DefaultIcon != wantIcon {
				t.Errorf("plugin %q library icon = %q default %q, want %q", pluginID, got.Icon, got.DefaultIcon, wantIcon)
			}
		}
	}
}

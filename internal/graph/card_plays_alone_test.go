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

// TestPlaysAloneFollowsThePluginDeclaration pins CORE-3147: Media in a Library
// whose plugin declares Engagement.PlaysAlone carry playsAlone on their card,
// their search card and their media(id) read; Media in a Library whose plugin
// does not declare it, and in a plugin-less Library, omit it.
func TestPlaysAloneFollowsThePluginDeclaration(t *testing.T) {
	bundleFor := func(id plugin.ID, playsAlone bool) plugin.Bundle {
		return plugin.Bundle{
			ID:          id,
			Version:     "0.1.0",
			DisplayName: plugin.L(string(id)),
			MediaLabel:  plugin.L("Item"),
			Engagement:  plugin.Engagement{PlaysAlone: playsAlone},
			Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "all", Kind: "grid", Title: plugin.L("All"), ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "all-items"}},
			}}},
		}
	}
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{bundleFor("alonekit", true), bundleFor("queuekit", false)})
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
	want := map[string]bool{"alonekit": true, "queuekit": false, "": false}
	media := map[string]store.MediaID{}
	libraries := map[string]store.LibraryID{}
	for pluginID := range want {
		library, err := st.CreateLibrary(ctx, "Lib "+pluginID, false, pluginID, "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/alone/" + pluginID + "x.mkv", Title: "Alone Title",
			Container: "mkv", Codec: "h264", ContentFingerprint: "fp-" + pluginID, DurationSecondsHint: 60,
		})
		if err != nil {
			t.Fatal(err)
		}
		media[pluginID], libraries[pluginID] = item.ID, library.ID
	}

	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id playsAlone } } }`,
		map[string]any{"query": "Alone Title"}, "searchResult", &search)
	rows := map[string]SearchResultItem{}
	for _, item := range search.Items {
		rows[item.ID] = item
	}
	for pluginID, id := range media {
		var page LibraryItemsResponse
		execGraph(t, handler, authorizeViewer,
			`query($libraryID: String!) { libraryItems(libraryID: $libraryID, limit: 50) { items { id playsAlone } } }`,
			map[string]any{"libraryID": string(libraries[pluginID])}, "libraryItems", &page)
		if len(page.Items) != 1 || page.Items[0].ID != string(id) {
			t.Fatalf("plugin %q libraryItems = %+v, want the one item %s", pluginID, page.Items, id)
		}
		if got := page.Items[0].PlaysAlone; got != want[pluginID] {
			t.Errorf("plugin %q card playsAlone = %v, want %v", pluginID, got, want[pluginID])
		}
		row, ok := rows[string(id)]
		if !ok {
			t.Fatalf("plugin %q search row missing from %+v", pluginID, search.Items)
		}
		if row.PlaysAlone != want[pluginID] {
			t.Errorf("plugin %q search playsAlone = %v, want %v", pluginID, row.PlaysAlone, want[pluginID])
		}
		var info MediaInfo
		execGraph(t, handler, authorizeViewer,
			`query($id: String!) { media(id: $id) { id playsAlone } }`,
			map[string]any{"id": string(id)}, "media", &info)
		if info.PlaysAlone != want[pluginID] {
			t.Errorf("plugin %q media(id) playsAlone = %v, want %v", pluginID, info.PlaysAlone, want[pluginID])
		}
	}
}

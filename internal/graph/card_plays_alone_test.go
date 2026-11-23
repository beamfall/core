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

// TestPlaysAloneFollowsThePluginDeclaration pins CORE-3147 and CORE-3307:
// Media in a Library whose plugin declares Engagement.PlaysAlone or
// Engagement.SelectPlays carry playsAlone or selectPlays on their card, their
// search card and their media(id) read; Media in a Library whose plugin does
// not declare them, and in a plugin-less Library, omit them.
func TestPlaysAloneFollowsThePluginDeclaration(t *testing.T) {
	bundleFor := func(id plugin.ID, engagement plugin.Engagement) plugin.Bundle {
		return plugin.Bundle{
			ID:          id,
			Version:     "0.1.0",
			DisplayName: plugin.L(string(id)),
			MediaLabel:  plugin.L("Item"),
			Engagement:  engagement,
			Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "all", Kind: "grid", Title: plugin.L("All"), ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "all-items"}},
			}}},
		}
	}
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{
		bundleFor("alonekit", plugin.Engagement{PlaysAlone: true}),
		bundleFor("selectkit", plugin.Engagement{SelectPlays: true}),
		bundleFor("queuekit", plugin.Engagement{}),
	})
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
	want := map[string]flags{"alonekit": {playsAlone: true}, "selectkit": {selectPlays: true}, "queuekit": {}, "": {}}
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

	var search struct{ Items []cardFlags }
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id playsAlone selectPlays } } }`,
		map[string]any{"query": "Alone Title"}, "searchResult", &search)
	rows := map[string]cardFlags{}
	for _, item := range search.Items {
		rows[item.ID] = item
	}
	for pluginID, id := range media {
		var page struct{ Items []cardFlags }
		execGraph(t, handler, authorizeViewer,
			`query($libraryID: String!) { libraryItems(libraryID: $libraryID, limit: 50) { items { id playsAlone selectPlays } } }`,
			map[string]any{"libraryID": string(libraries[pluginID])}, "libraryItems", &page)
		if len(page.Items) != 1 || page.Items[0].ID != string(id) {
			t.Fatalf("plugin %q libraryItems = %+v, want the one item %s", pluginID, page.Items, id)
		}
		if got := flagsOf(t, "card", page.Items[0]); got != want[pluginID] {
			t.Errorf("plugin %q card flags = %+v, want %+v", pluginID, got, want[pluginID])
		}
		row, ok := rows[string(id)]
		if !ok {
			t.Fatalf("plugin %q search row missing from %+v", pluginID, search.Items)
		}
		if got := flagsOf(t, "search", row); got != want[pluginID] {
			t.Errorf("plugin %q search flags = %+v, want %+v", pluginID, got, want[pluginID])
		}
		var info cardFlags
		execGraph(t, handler, authorizeViewer,
			`query($id: String!) { media(id: $id) { id playsAlone selectPlays } }`,
			map[string]any{"id": string(id)}, "media", &info)
		if got := flagsOf(t, "media(id)", info); got != want[pluginID] {
			t.Errorf("plugin %q media(id) flags = %+v, want %+v", pluginID, got, want[pluginID])
		}
	}
}

type flags struct{ playsAlone, selectPlays bool }

// cardFlags keeps whether each flag was on the wire at all, which a plain bool
// cannot tell from false.
type cardFlags struct {
	ID          string `json:"id"`
	PlaysAlone  *bool  `json:"playsAlone"`
	SelectPlays *bool  `json:"selectPlays"`
}

// flagsOf reads a card's flags and fails when one arrives as false: an
// undeclared flag must be absent, so a client keeps its own fallback rule
// (CORE-3307).
func flagsOf(t *testing.T, read string, card cardFlags) flags {
	t.Helper()
	for name, value := range map[string]*bool{"playsAlone": card.PlaysAlone, "selectPlays": card.SelectPlays} {
		if value != nil && !*value {
			t.Errorf("%s %s sends %s: false, want the key absent", read, card.ID, name)
		}
	}
	return flags{card.PlaysAlone != nil && *card.PlaysAlone, card.SelectPlays != nil && *card.SelectPlays}
}

// TestSemanticSourceItemsCarryPlaysAlone pins CORE-3147 on the one card path
// that skips card enrichment: an all-items Source read in semantic mode.
func TestSemanticSourceItemsCarryPlaysAlone(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID: "alonekit", Version: "0.1.0", DisplayName: plugin.L("alonekit"), MediaLabel: plugin.L("Item"),
		Engagement: plugin.Engagement{PlaysAlone: true},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "all", Kind: "grid", Title: plugin.L("All"), ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "all-items"}},
		}}},
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
	library, err := st.CreateLibrary(ctx, "Lib alonekit", false, "alonekit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	vec := []float32{1, 0, 0}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: library.ID, Path: "/alone/semantic.mkv", Title: "Semantic Alone",
		Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:alone-semantic", DurationSecondsHint: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEmbedding(ctx, "sha256:alone-semantic", "text-v1", vec); err != nil {
		t.Fatal(err)
	}
	handler.SetQueryEmbedder(stubEmbedder{kind: "text-v1", available: true, vector: vec})

	data, errs := post(t, handler,
		`{"query":"query { sourceItems { id playsAlone } }","variables":{"kind":"all-items","query":"anything","mode":"semantic"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("semantic sourceItems errors = %#v", errs)
	}
	card, ok := cardsByID(t, data["sourceItems"])[string(media.ID)]
	if !ok {
		t.Fatalf("semantic sourceItems omitted the hit: %s", data["sourceItems"])
	}
	if got := string(card["playsAlone"]); got != "true" {
		t.Errorf("semantic card playsAlone = %q, want true", got)
	}
}

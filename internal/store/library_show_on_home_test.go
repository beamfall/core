package store

import (
	"context"
	"slices"
	"testing"
)

func homeSourceIDs(t *testing.T, st *Store, filter LibraryFilter, query SourceQuery) []MediaID {
	t.Helper()
	items, err := st.SourceItems(context.Background(), filter, query)
	if err != nil {
		t.Fatalf("SourceItems(%+v) returned error: %v", query, err)
	}
	ids := make([]MediaID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestLibraryShowOnHomeDefaultsOnAndPersists(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{Revealed: true, AllLibraries: true}

	showOnHome := func() map[LibraryID]bool {
		libraries, err := st.Libraries(ctx, all)
		if err != nil {
			t.Fatal(err)
		}
		got := map[LibraryID]bool{}
		for _, library := range libraries {
			got[library.ID] = library.ShowOnHome
		}
		return got
	}
	if got := showOnHome(); !got["library-local"] || !got["library-archive"] {
		t.Fatalf("ShowOnHome defaults = %v, want every Library shown", got)
	}

	if err := st.SetLibraryShowOnHome(ctx, "library-local", false); err != nil {
		t.Fatalf("SetLibraryShowOnHome returned error: %v", err)
	}
	if got := showOnHome(); got["library-local"] || !got["library-archive"] {
		t.Fatalf("ShowOnHome after hiding library-local = %v", got)
	}
	if err := st.SetLibraryShowOnHome(ctx, "library-local", true); err != nil {
		t.Fatal(err)
	}
	if got := showOnHome(); !got["library-local"] {
		t.Fatalf("ShowOnHome after re-showing library-local = %v", got)
	}

	if err := st.SetLibraryShowOnHome(ctx, "library-missing", false); err == nil {
		t.Fatal("SetLibraryShowOnHome accepted an unknown Library")
	}
	if err := st.SetLibraryShowOnHome(ctx, "", false); err == nil {
		t.Fatal("SetLibraryShowOnHome accepted an empty Library ID")
	}
}

func TestSourceItemsHomeSkipsLibrariesHiddenFromHome(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{Revealed: true, AllLibraries: true}
	scoped := LibraryFilter{Revealed: true, AllowedIDs: []LibraryID{"library-local", "library-archive"}}
	recent := SourceQuery{Kind: "recently-added"}
	home := SourceQuery{Kind: "recently-added", Home: true}

	// With nothing hidden, a Home query matches the plain aggregate one.
	before := homeSourceIDs(t, st, all, recent)
	if !slices.Contains(before, "media-alpha") || !slices.Contains(before, "media-archive") {
		t.Fatalf("seeded recently-added = %v, want media-alpha and media-archive", before)
	}
	if got := homeSourceIDs(t, st, all, home); !slices.Equal(got, before) {
		t.Fatalf("Home recently-added with nothing hidden = %v, want %v", got, before)
	}

	if err := st.SetLibraryShowOnHome(ctx, "library-local", false); err != nil {
		t.Fatal(err)
	}
	for name, filter := range map[string]LibraryFilter{"all-libraries": all, "allow-list": scoped} {
		got := homeSourceIDs(t, st, filter, home)
		if slices.Contains(got, "media-alpha") || !slices.Contains(got, "media-archive") {
			t.Fatalf("%s Home recently-added = %v, want media-archive without media-alpha", name, got)
		}
		// Every non-Home aggregate feed is unaffected.
		if got := homeSourceIDs(t, st, filter, recent); !slices.Contains(got, "media-alpha") {
			t.Fatalf("%s aggregate recently-added = %v, want media-alpha kept", name, got)
		}
	}
	// The hidden Library's own Sections still list its Media.
	query := SourceQuery{Kind: "recently-added", LibraryID: "library-local"}
	if got := homeSourceIDs(t, st, all, query); !slices.Contains(got, "media-alpha") {
		t.Fatalf("library-scoped %+v = %v, want media-alpha", query, got)
	}

	// Hiding every Library a scope can see leaves Home empty, never widened.
	if err := st.SetLibraryShowOnHome(ctx, "library-archive", false); err != nil {
		t.Fatal(err)
	}
	for name, filter := range map[string]LibraryFilter{"all-libraries": all, "allow-list": scoped} {
		if got := homeSourceIDs(t, st, filter, home); len(got) != 0 {
			t.Fatalf("%s Home recently-added with every Library hidden = %v, want empty", name, got)
		}
	}
}

func TestSourceItemsScopedHomeRespectsLibraryVisibility(t *testing.T) {
	st := openSeeded(t)
	if err := st.SetLibraryShowOnHome(context.Background(), "library-local", false); err != nil {
		t.Fatal(err)
	}
	for name, filter := range map[string]LibraryFilter{
		"all-libraries": {Revealed: true, AllLibraries: true},
		"allow-list":    {Revealed: true, AllowedIDs: []LibraryID{"library-local", "library-archive"}},
	} {
		t.Run(name, func(t *testing.T) {
			query := SourceQuery{Kind: "recently-added", LibraryID: "library-local", Home: true}
			if got := homeSourceIDs(t, st, filter, query); len(got) != 0 {
				t.Errorf("hidden Library's Home feed = %v, want empty", got)
			}
			query.LibraryID = "library-archive"
			if got := homeSourceIDs(t, st, filter, query); !slices.Equal(got, []MediaID{"media-archive"}) {
				t.Errorf("visible Library's Home feed = %v, want only media-archive", got)
			}
		})
	}
}

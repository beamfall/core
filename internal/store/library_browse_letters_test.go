package store

import (
	"context"
	"slices"
	"testing"
)

func TestLibraryBrowseInitial(t *testing.T) {
	for title, want := range map[string]string{
		"apple":   "A",
		"Banana":  "B",
		"émile":   "É",
		"3 doors": "#",
		"(paren)": "#",
		" space":  "#",
		"":        "#",
		"\xff":    "#",
	} {
		if got := libraryBrowseInitial(title); got != want {
			t.Errorf("libraryBrowseInitial(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestLibraryBrowseLettersKeepFirstOffsetAndCountPerInitial(t *testing.T) {
	var candidates []libraryBrowseCandidate
	for _, title := range []string{"2 later", "alpha", "Apex", "bravo", "zulu", "~after z"} {
		candidates = append(candidates, libraryBrowseCandidate{item: SourceItem{Title: title}})
	}
	want := []LibraryBrowseLetter{{"#", 0, 2}, {"A", 1, 2}, {"B", 3, 1}, {"Z", 4, 1}}
	if got := libraryBrowseLetters(candidates, libraryBrowseCandidate.titleKey); !slices.Equal(got, want) {
		t.Fatalf("letters = %v, want %v", got, want)
	}
	if got := libraryBrowseLetters(nil, libraryBrowseCandidate.titleKey); got == nil || len(got) != 0 {
		t.Fatalf("letters of an empty set = %#v, want an empty non-nil list", got)
	}
}

// The letters a browse reports point at the first item of each initial in the
// same filtered, sorted set a client pages through, and count its items.
func TestLibraryBrowseLettersMatchTheTitleOrder(t *testing.T) {
	st := openDemo(t)
	check := func(name string, result LibraryBrowseResult) {
		t.Helper()
		if len(result.Letters) == 0 {
			t.Fatalf("%s: no letters for a non-empty title browse", name)
		}
		if len(result.Items) != result.Total {
			t.Fatalf("%s: page holds %d of %d items; the check needs the whole set", name, len(result.Items), result.Total)
		}
		seen := map[string]bool{}
		counts := map[string]int{}
		for _, item := range result.Items {
			counts[libraryBrowseInitial(item.Title)]++
		}
		next := 0
		for i, letter := range result.Letters {
			if letter.Count != counts[letter.Letter] {
				t.Fatalf("%s: letter %s counts %d items, want %d", name, letter.Letter, letter.Count, counts[letter.Letter])
			}
			if i > 0 && letter.Offset <= result.Letters[i-1].Offset {
				t.Fatalf("%s: offsets not increasing: %v", name, result.Letters)
			}
			for ; next < letter.Offset; next++ {
				seen[libraryBrowseInitial(result.Items[next].Title)] = true
			}
			if seen[letter.Letter] || libraryBrowseInitial(result.Items[letter.Offset].Title) != letter.Letter {
				t.Fatalf("%s: letter %s at %d is not the first item with that initial", name, letter.Letter, letter.Offset)
			}
		}
	}
	all := mustLibraryBrowse(t, st, LibraryBrowseQuery{Limit: 200})
	check("asc", all)
	check("desc", mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortTitle, Descending: true, Limit: 200}))

	tens := mustLibraryBrowse(t, st, LibraryBrowseQuery{YearFrom: 2010, YearTo: 2019, Limit: 200})
	check("filtered", tens)
	if tens.Total >= all.Total {
		t.Fatalf("year filter kept %d of %d items; the test needs it to narrow the set", tens.Total, all.Total)
	}

	// A page still reports the letters of the whole set, so a client can jump
	// from the first page it loads.
	page := mustLibraryBrowse(t, st, LibraryBrowseQuery{Limit: 1})
	if !slices.Equal(page.Letters, all.Letters) {
		t.Fatalf("one-item page letters = %v, want the whole set's %v", page.Letters, all.Letters)
	}

	if year := mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortYear}); year.Letters == nil || len(year.Letters) != 0 {
		t.Fatalf("year sort letters = %#v, want an empty list", year.Letters)
	}
}

// CORE-1768: a title sort and its letters both key on the sort title when an
// item has one and fall back to the title otherwise, so a rail jump lands on
// the order the grid shows.
func TestLibraryBrowseSortsAndLettersBySortTitle(t *testing.T) {
	st := openDemo(t)
	before := mustLibraryBrowse(t, st, LibraryBrowseQuery{Limit: 200})
	if len(before.Items) < 3 || len(before.Items) != before.Total {
		t.Fatalf("need the whole demo library, got %d of %d", len(before.Items), before.Total)
	}
	moved := before.Items[0]
	if libraryBrowseInitial(moved.Title) == "Z" {
		t.Fatalf("first title %q already buckets under Z", moved.Title)
	}
	if err := st.UpdateMediaMetadata(context.Background(), moved.ID, MediaMetadata{
		SortTitle: strPtr("Zzzz sort key"), Source: MutationSourceManual,
	}); err != nil {
		t.Fatal(err)
	}
	after := mustLibraryBrowse(t, st, LibraryBrowseQuery{Limit: 200})

	// The item with a sort title moves to its sort-title place; every other
	// item keeps its title order.
	var wantOrder []MediaID
	for _, item := range before.Items[1:] {
		wantOrder = append(wantOrder, item.ID)
	}
	wantOrder = append(wantOrder, moved.ID)
	var gotOrder []MediaID
	for _, item := range after.Items {
		gotOrder = append(gotOrder, item.ID)
	}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Fatalf("order = %v, want %v", gotOrder, wantOrder)
	}

	// The letters bucket it under its sort title's initial, at its offset.
	last := after.Letters[len(after.Letters)-1]
	if last.Letter != "Z" || last.Offset+last.Count != after.Total {
		t.Fatalf("last letter = %+v, want Z ending at %d", last, after.Total)
	}
	counts := map[string]int{}
	for _, letter := range after.Letters {
		counts[letter.Letter] = letter.Count
	}
	beforeCounts := map[string]int{}
	for _, letter := range before.Letters {
		beforeCounts[letter.Letter] = letter.Count
	}
	oldInitial := libraryBrowseInitial(moved.Title)
	if counts["Z"] != beforeCounts["Z"]+1 || counts[oldInitial] != beforeCounts[oldInitial]-1 {
		t.Fatalf("letter counts %v, before %v: the item must move from %s to Z", counts, beforeCounts, oldInitial)
	}
}

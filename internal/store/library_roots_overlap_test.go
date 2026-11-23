package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryRootsOverlap(t *testing.T) {
	base := t.TempDir()
	tv := filepath.Join(base, "tv")
	if err := os.MkdirAll(filepath.Join(tv, "season1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "tv2"), 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "tv-link")
	linked := os.Symlink(tv, link) == nil

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"equal", tv, tv, true},
		{"equal with dots", filepath.Join(tv, "season1", ".."), tv, true},
		{"parent", base, tv, true},
		{"child", filepath.Join(tv, "season1"), tv, true},
		{"missing child", filepath.Join(tv, "not-yet"), tv, true},
		{"sibling sharing a name prefix", filepath.Join(base, "tv2"), tv, false},
		{"sibling", filepath.Join(base, "films"), tv, false},
	}
	if linked {
		cases = append(cases,
			struct {
				name string
				a, b string
				want bool
			}{"symlink to the same folder", link, tv, true},
			struct {
				name string
				a, b string
				want bool
			}{"child through a symlink", filepath.Join(link, "season1"), tv, true})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LibraryRootsOverlap(tc.a, tc.b); got != tc.want {
				t.Fatalf("LibraryRootsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := LibraryRootsOverlap(tc.b, tc.a); got != tc.want {
				t.Fatalf("LibraryRootsOverlap(%q, %q) = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

func openOverlapStore(t *testing.T, base string) *Store {
	t.Helper()
	st, err := Open(context.Background(), migratedDataDir(t), Options{SeedDevelopmentData: true, AllowedRoots: []string{base}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestCheckLibraryRootsOverlapAgainstOtherLibraries(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	tv := filepath.Join(base, "tv")
	films := filepath.Join(base, "films")
	for _, dir := range []string{filepath.Join(tv, "season1"), films} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	st := openOverlapStore(t, base)
	if err := st.SetLibraryRoots(ctx, "library-local", []string{tv}); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateLibraryWithRoots(ctx, "Films", false, "", "profile-default", []string{films}, "")
	if err != nil {
		t.Fatal(err)
	}

	for name, root := range map[string]string{
		"equal":  tv,
		"parent": base,
		"child":  filepath.Join(tv, "season1"),
	} {
		t.Run(name, func(t *testing.T) {
			// A new Library and another existing Library both refuse it.
			for _, id := range []LibraryID{"", other.ID} {
				err := st.CheckLibraryRootsOverlap(ctx, id, nil, []string{root})
				var refusal *LibraryRootError
				if !errors.As(err, &refusal) || refusal.Reason != LibraryRootOverlaps || refusal.Path != root {
					t.Fatalf("CheckLibraryRootsOverlap(%q, %q) = %v, want overlaps refusal", id, root, err)
				}
			}
		})
	}

	free := filepath.Join(base, "music")
	if err := st.CheckLibraryRootsOverlap(ctx, "", nil, []string{free}); err != nil {
		t.Fatalf("non-overlapping folder refused: %v", err)
	}
	// A Library's own saved folder may be kept, and its own folders are not
	// "another Library" when it is replaced.
	if err := st.CheckLibraryRootsOverlap(ctx, "library-local", []string{tv}, []string{tv, free}); err != nil {
		t.Fatalf("keeping a saved folder and adding a free one refused: %v", err)
	}
	if err := st.CheckLibraryRootsOverlap(ctx, "library-local", []string{tv}, []string{free}); err != nil {
		t.Fatalf("replacing a folder with a free one refused: %v", err)
	}
}

func TestCheckLibraryRootsOverlapWithinOneRequest(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	parent := filepath.Join(base, "media")
	child := filepath.Join(parent, "tv")
	if err := os.MkdirAll(child, 0o750); err != nil {
		t.Fatal(err)
	}
	st := openOverlapStore(t, base)

	for _, roots := range [][]string{{parent, child}, {child, parent}} {
		var refusal *LibraryRootError
		if err := st.CheckLibraryRootsOverlap(ctx, "", nil, roots); !errors.As(err, &refusal) || refusal.Reason != LibraryRootOverlaps {
			t.Fatalf("CheckLibraryRootsOverlap(%q) = %v, want overlaps refusal", roots, err)
		}
	}
	// Two spellings of one folder are one root, not an overlap.
	if err := st.CheckLibraryRootsOverlap(ctx, "", nil, []string{child, child + "/"}); err != nil {
		t.Fatalf("duplicate spelling of one folder refused: %v", err)
	}
}

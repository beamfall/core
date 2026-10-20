package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLibraryRootsReplaceSetNormalizesAndSorts(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	first := filepath.Join(t.TempDir(), "media", "..", "media-a")
	second := filepath.Join(t.TempDir(), "media-b")
	if err := st.SetLibraryRoots(ctx, "library-local", []string{second, first, first}); err != nil {
		t.Fatalf("SetLibraryRoots returned error: %v", err)
	}

	roots, err := st.LibraryRoots(ctx, "library-local")
	if err != nil {
		t.Fatalf("LibraryRoots returned error: %v", err)
	}
	want := []string{filepath.Clean(first), filepath.Clean(second)}
	if len(roots) != len(want) || roots[0] != want[0] || roots[1] != want[1] {
		t.Fatalf("roots = %#v, want %#v", roots, want)
	}

	if err := st.SetLibraryRoots(ctx, "library-local", []string{second}); err != nil {
		t.Fatalf("replace SetLibraryRoots returned error: %v", err)
	}
	roots, err = st.LibraryRoots(ctx, "library-local")
	if err != nil {
		t.Fatalf("LibraryRoots after replace returned error: %v", err)
	}
	if len(roots) != 1 || roots[0] != filepath.Clean(second) {
		t.Fatalf("roots after replace = %#v, want only %q", roots, filepath.Clean(second))
	}
}

func TestAllLibraryRootsIncludesEveryLibrary(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	other, err := st.CreateLibrary(ctx, "Other", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary returned error: %v", err)
	}
	rootBase := t.TempDir()
	first := filepath.Join(rootBase, "a-root")
	second := filepath.Join(rootBase, "z-root")
	if err := st.SetLibraryRoots(ctx, "library-local", []string{first}); err != nil {
		t.Fatalf("SetLibraryRoots for library-local returned error: %v", err)
	}
	if err := st.SetLibraryRoots(ctx, other.ID, []string{second}); err != nil {
		t.Fatalf("SetLibraryRoots for other Library returned error: %v", err)
	}

	roots, err := st.AllLibraryRoots(ctx)
	if err != nil {
		t.Fatalf("AllLibraryRoots returned error: %v", err)
	}
	want := []string{first, second}
	if len(roots) != len(want) || roots[0] != want[0] || roots[1] != want[1] {
		t.Fatalf("AllLibraryRoots = %#v, want %#v", roots, want)
	}
}

// ADR-0028 §1: a family scope chosen at creation is persisted on the Library
// row and round-trips through both the focused read and the Libraries load;
// CreateLibrary without a scope leaves it unset.
func TestCreateLibraryRecordsFamilyScope(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	scoped, err := st.CreateLibraryWithRoots(ctx, "Movies", false, "filmtv", "profile-default", nil, "films")
	if err != nil {
		t.Fatalf("CreateLibraryWithRoots returned error: %v", err)
	}
	if scoped.FamilyScope != "films" {
		t.Fatalf("created library FamilyScope = %q, want %q", scoped.FamilyScope, "films")
	}
	got, err := st.LibraryFamilyScope(ctx, scoped.ID)
	if err != nil {
		t.Fatalf("LibraryFamilyScope returned error: %v", err)
	}
	if got != "films" {
		t.Fatalf("LibraryFamilyScope = %q, want %q", got, "films")
	}

	unset, err := st.CreateLibrary(ctx, "Mixed Bag", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary returned error: %v", err)
	}
	if unset.FamilyScope != "" {
		t.Fatalf("unscoped library FamilyScope = %q, want empty", unset.FamilyScope)
	}

	libraries, err := st.Libraries(ctx, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("Libraries returned error: %v", err)
	}
	for _, lib := range libraries {
		switch lib.ID {
		case scoped.ID:
			if lib.FamilyScope != "films" {
				t.Fatalf("loaded scoped library FamilyScope = %q, want films", lib.FamilyScope)
			}
		case unset.ID:
			if lib.FamilyScope != "" {
				t.Fatalf("loaded unscoped library FamilyScope = %q, want empty", lib.FamilyScope)
			}
		}
	}
}

func TestCreateLibrarySeedsScanSchedule(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	library, err := st.CreateLibrary(ctx, "Family", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary returned error: %v", err)
	}
	schedule, err := st.Schedule(ctx, ScheduleID("scan:"+string(library.ID)))
	if err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}
	if schedule.JobKind != "scan" || schedule.Trigger.Kind != ScheduleTriggerEvery ||
		schedule.Trigger.N != 12 || schedule.Trigger.Unit != ScheduleEveryHours ||
		!schedule.Enabled || schedule.Orphaned || schedule.LastCompletedEnqueue.IsZero() {
		t.Fatalf("created scan schedule = %#v", schedule)
	}
}

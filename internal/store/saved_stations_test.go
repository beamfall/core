package store

import (
	"context"
	"testing"
	"time"
)

// TestSavedStationCRUDStoresInputsNotOrder pins the core ADR-0098 area-5
// invariant for a saved Station: it persists the generation INPUTS
// (seed/rules/tuning) and NEVER a generated track order, and round-trips
// per-Profile per-Library.
func TestSavedStationCRUDStoresInputsNotOrder(t *testing.T) {
	// feature:music-stations
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()

	created, err := st.CreateSavedStation(ctx, SavedStation{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "  Morning Mix  ",
		SeedJSON:   `{"kind":"artist","id":"entity-1"}`,
		RulesJSON:  `{"minYear":2000}`,
		TuningJSON: `{"energy":0.7}`,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Morning Mix" {
		t.Fatalf("name not trimmed: %q", created.Name)
	}
	if created.ID == "" {
		t.Fatal("missing id")
	}
	// The struct has no field for a generated track order — the persisted shape is
	// inputs only. Assert the round-trip carries exactly the inputs back.
	got, err := st.SavedStations(ctx, "profile-default", "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("stations = %d, want 1", len(got))
	}
	if got[0].SeedJSON != `{"kind":"artist","id":"entity-1"}` ||
		got[0].RulesJSON != `{"minYear":2000}` ||
		got[0].TuningJSON != `{"energy":0.7}` {
		t.Fatalf("inputs not round-tripped: %+v", got[0])
	}

	// Empty inputs default to valid JSON.
	bare, err := st.CreateSavedStation(ctx, SavedStation{
		ProfileID: "profile-default", LibraryID: "library-local", Name: "Bare",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if bare.SeedJSON != "{}" || bare.RulesJSON != "{}" || bare.TuningJSON != "{}" {
		t.Fatalf("empty inputs not defaulted: %+v", bare)
	}

	// Delete returns the removed row and the listing shrinks.
	if _, err := st.DeleteSavedStation(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	after, err := st.SavedStations(ctx, "profile-default", "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].ID != bare.ID {
		t.Fatalf("after delete = %v, want only bare", after)
	}
}

// TestSavedStationValidation pins required-field rejection.
func TestSavedStationValidation(t *testing.T) {
	// feature:music-stations
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()
	for _, tc := range []struct {
		name string
		in   SavedStation
	}{
		{"no profile", SavedStation{LibraryID: "library-local", Name: "x"}},
		{"no library", SavedStation{ProfileID: "profile-default", Name: "x"}},
		{"no name", SavedStation{ProfileID: "profile-default", LibraryID: "library-local"}},
	} {
		if _, err := st.CreateSavedStation(ctx, tc.in, now); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}
	if _, err := st.SavedStationByID(ctx, "station-missing"); err != ErrSavedStationNotFound {
		t.Fatalf("missing station err = %v, want ErrSavedStationNotFound", err)
	}
}

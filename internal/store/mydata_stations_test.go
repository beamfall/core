package store

import (
	"context"
	"testing"
	"time"
)

// TestSavedStationsParticipateInMyData pins ADR-0098 area 5 / ADR-0036: a saved
// Station is exported under My Data (inputs only — never a generated order),
// reveal-filtered by its owning Library, and HARD-DELETED (not disowned) by the
// full-footprint delete.
func TestSavedStationsParticipateInMyData(t *testing.T) {
	// feature:music-stations
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(2000, 0).UTC()

	// A station on the visible local Library and one on the restricted Archive.
	if _, err := st.CreateSavedStation(ctx, SavedStation{
		ProfileID: "profile-default", LibraryID: "library-local", Name: "Local Station",
		SeedJSON: `{"kind":"genre","id":"g1"}`,
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSavedStation(ctx, SavedStation{
		ProfileID: "profile-default", LibraryID: "library-archive", Name: "Archive Station",
	}, now); err != nil {
		t.Fatal(err)
	}

	// RELOCKED export: the Archive-bound Station is dropped (reveal contract); the
	// local one survives and carries its inputs.
	relocked, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(relocked.Stations) != 1 || relocked.Stations[0].Name != "Local Station" {
		t.Fatalf("relocked stations = %+v, want only Local Station", relocked.Stations)
	}
	if relocked.Stations[0].SeedJSON != `{"kind":"genre","id":"g1"}` {
		t.Fatalf("station inputs not exported: %+v", relocked.Stations[0])
	}

	// REVEALED export: both Stations appear.
	revealed, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(revealed.Stations) != 2 {
		t.Fatalf("revealed stations = %d, want 2", len(revealed.Stations))
	}

	// FULL-FOOTPRINT DELETE hard-deletes the Profile's saved Stations.
	if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteAll, "", profileDefaultFilter(true)); err != nil {
		t.Fatal(err)
	}
	left, err := st.SavedStations(ctx, "profile-default", "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("stations after delete = %d, want 0 (hard-deleted)", len(left))
	}
}

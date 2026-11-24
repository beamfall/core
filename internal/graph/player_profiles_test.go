package graph

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestPlayerProfilesOwnerListing covers the Console's only listing of Player
// Profiles (ADR-0240 D1, D4, D7, CORE-3314): owner only, Player Profiles only,
// each with its walk-up setting and controllers list.
func TestPlayerProfilesOwnerListing(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	all := store.LibraryFilter{AllLibraries: true}
	player, err := st.CreateProfile(ctx, "Garage TV", store.ProfilePlayer, all)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProfile(ctx, "Sam", store.ProfileAdult, all); err != nil {
		t.Fatal(err)
	}

	const query = `{"query":"{ playerProfiles { id name kind walkUpEnabled playerControllers { profileID allAdults include exclude } } }"}`
	if _, errs := post(t, h, query, withPlayerControl(authorizeViewer)); len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("non-admin playerProfiles = %#v, want FORBIDDEN", errs)
	}

	data, errs := post(t, h, query, withPlayerControl(authorizeManager))
	if len(errs) != 0 {
		t.Fatalf("playerProfiles errors = %#v", errs)
	}
	var got []ProfileInfo
	mustUnmarshal(t, data["playerProfiles"], &got)
	if len(got) != 1 || got[0].ID != string(player.ID) || got[0].Kind != string(store.ProfilePlayer) {
		t.Fatalf("playerProfiles = %+v, want only %s", got, player.ID)
	}
	if got[0].PlayerControllers == nil || !got[0].PlayerControllers.AllAdults {
		t.Fatalf("playerControllers = %+v, want allAdults=true", got[0].PlayerControllers)
	}
}

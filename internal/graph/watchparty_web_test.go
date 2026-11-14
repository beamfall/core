package graph

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Watch Party server support for the web viewer (CORE-1677): sync reaches
// every member Profile, the timeline can be paused, the roster is broadcast,
// and forming or joining is home-network only (CORE-0531).

func pausedTransportBody(partyID, item string, positionMS int64, paused bool) string {
	pausedJSON := "false"
	if paused {
		pausedJSON = "true"
	}
	return `{"query":"mutation T($partyID: String!, $item: String!, $positionMS: Int!, $paused: Boolean){ watchPartyTransport(partyID:$partyID, item:$item, positionMS:$positionMS, paused:$paused){ groupID item positionMS presentAtMS } }",` +
		`"variables":{"partyID":"` + partyID + `","item":"` + item + `","positionMS":` + jsonNumber(positionMS) + `,"paused":` + pausedJSON + `}}`
}

// remote marks the request as arriving from outside the home network, the way
// the server's graphQL route does for a non-LAN peer.
func remote(authorize func(*http.Request)) func(*http.Request) {
	return func(req *http.Request) {
		authorize(req)
		*req = *req.WithContext(auth.WithRemoteOrigin(req.Context()))
	}
}

// formTwoProfileParty forms a party hosted by profile-default with
// profile-managed accepted, returning its id.
func formTwoProfileParty(t *testing.T, h *Handler) string {
	t.Helper()
	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	if _, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("accept errors = %#v", errs)
	}
	return party.PartyID
}

// feature:watch-party
func TestWatchPartyTransitionReachesEveryMemberProfile(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)
	partyID := formTwoProfileParty(t, h)

	cursor := lastSeq(t, st)
	if _, errs := post(t, h, pausedTransportBody(partyID, "media-alpha", 30000, true), authorizeViewer); len(errs) != 0 {
		t.Fatalf("transport errors = %#v", errs)
	}
	rows := partyTransitionRows(t, st, partyID, cursor)
	var profiles []string
	for _, p := range rows {
		profiles = append(profiles, string(p.ProfileID))
		if p.PositionMS != 30000 || !p.Paused {
			t.Errorf("row for %s = position %d paused %v, want 30000 paused", p.ProfileID, p.PositionMS, p.Paused)
		}
	}
	slices.Sort(profiles)
	if want := []string{"profile-default", "profile-managed"}; !slices.Equal(profiles, want) {
		t.Fatalf("transition rows addressed to %v, want one each for %v", profiles, want)
	}

	// Resuming clears the pause for everyone.
	cursor = lastSeq(t, st)
	if _, errs := post(t, h, pausedTransportBody(partyID, "media-alpha", 30000, false), authorizeViewer); len(errs) != 0 {
		t.Fatalf("resume errors = %#v", errs)
	}
	for _, p := range partyTransitionRows(t, st, partyID, cursor) {
		if p.Paused {
			t.Errorf("resume row for %s still paused", p.ProfileID)
		}
	}
}

// feature:watch-party
func TestWatchPartyRosterBroadcastOnAcceptAndLeave(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	cursor := lastSeq(t, st)
	partyID := formTwoProfileParty(t, h)

	roster := partySyncRows(t, st, partyID, cursor, store.GroupSignalRoster)
	if len(roster) == 0 {
		t.Fatal("no roster rows after create and accept")
	}
	last := map[store.ProfileID]store.GroupSyncPayload{}
	for _, p := range roster {
		last[p.ProfileID] = p
	}
	for _, profile := range []store.ProfileID{"profile-default", "profile-managed"} {
		p, ok := last[profile]
		if !ok {
			t.Fatalf("no roster row for %s; rows = %#v", profile, roster)
		}
		if want := []string{"Default", "Managed"}; !slices.Equal(p.Members, want) {
			t.Errorf("roster for %s = %v, want %v", profile, p.Members, want)
		}
		if !p.Paused {
			t.Errorf("roster for %s before playback paused=false, want true", profile)
		}
		if p.Item != "media-alpha" {
			t.Errorf("roster for %s item = %q, want media-alpha", profile, p.Item)
		}
	}

	cursor = lastSeq(t, st)
	if _, errs := post(t, h, leavePartyBody(partyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("leave errors = %#v", errs)
	}
	roster = partySyncRows(t, st, partyID, cursor, store.GroupSignalRoster)
	if len(roster) != 1 || roster[0].ProfileID != "profile-default" {
		t.Fatalf("roster after leave = %#v, want one row for profile-default", roster)
	}
	if want := []string{"Default"}; !slices.Equal(roster[0].Members, want) {
		t.Errorf("roster after leave = %v, want %v", roster[0].Members, want)
	}
}

// feature:watch-party
func TestWatchPartyRemoteFormAndJoinRefused(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	_, errs := post(t, h, createPartyBody("media-alpha", false), remote(authorizeViewer))
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "home network only") {
		t.Fatalf("remote createWatchParty errors = %#v, want the home-network refusal", errs)
	}

	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	_, errs = post(t, h, acceptBody(party.PartyID), remote(authorizeManaged))
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "home network only") {
		t.Fatalf("remote acceptWatchParty errors = %#v, want the home-network refusal", errs)
	}
	g, err := h.conductor.Party(party.PartyID)
	if err != nil {
		t.Fatalf("Party lookup: %v", err)
	}
	if n := len(g.Members()); n != 1 {
		t.Fatalf("members after refused remote accept = %d, want 1", n)
	}

	// The invite survives the refusal: joining from home still works.
	if _, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("home accept after remote refusal errors = %#v", errs)
	}
}

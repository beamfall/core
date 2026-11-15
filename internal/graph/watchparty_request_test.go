package graph

import (
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Watch Party requests (CORE-1765): a member's request in a host-only party is
// its own notification kind and a host-only player row, and the host approves
// it for everyone or declines it.

func decideRequestBody(partyID, requestID string, decline bool) string {
	d := "false"
	if decline {
		d = "true"
	}
	return `{"query":"mutation D($partyID: String!, $request: String, $decline: Boolean){ watchPartyTransport(partyID:$partyID, request:$request, decline:$decline){ groupID item positionMS presentAtMS } }",` +
		`"variables":{"partyID":"` + partyID + `","request":"` + requestID + `","decline":` + d + `}}`
}

// requestFromManaged sends a strict-party request from the managed member and
// returns the request row written to the host's Profile.
func requestFromManaged(t *testing.T, h *Handler, st *store.Store, partyID string, positionMS int64) store.GroupSyncPayload {
	t.Helper()
	cursor := lastSeq(t, st)
	if _, errs := post(t, h, requestTransportBody(partyID, "media-alpha", positionMS), authorizeManaged); len(errs) != 0 {
		t.Fatalf("request errors = %#v", errs)
	}
	rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalRequest)
	if len(rows) != 1 {
		t.Fatalf("request rows = %#v, want exactly one", rows)
	}
	return rows[0]
}

// feature:watch-party
func TestWatchPartyRequestHasOwnKindAndReachesHostOnly(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)

	row := requestFromManaged(t, h, st, partyID, 42000)
	if row.ProfileID != "profile-default" || row.Session != "session-default" {
		t.Fatalf("request row addressed to %q/%q, want the host profile-default/session-default", row.ProfileID, row.Session)
	}
	if row.Request == "" || row.From != "Managed" || row.Item != "media-alpha" || row.PositionMS != 42000 {
		t.Fatalf("request row = %#v, want an id, From Managed, media-alpha at 42000", row)
	}

	notes, err := st.ListNotifications(t.Context(), "profile-default", store.LibraryFilter{AllLibraries: true}, false, 50)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	var found bool
	for _, n := range notes {
		if n.Kind == store.NotificationWatchPartyInvite {
			t.Fatalf("request was filed as an invite: %#v", n)
		}
		if n.Kind == store.NotificationWatchPartyRequest {
			found = true
			if n.Ref != partyID+":"+row.Request || n.Body != "Managed" {
				t.Fatalf("request notification ref %q body %q, want %q and Managed", n.Ref, n.Body, partyID+":"+row.Request)
			}
		}
	}
	if !found {
		t.Fatalf("host notifications = %#v, want a watch-party-request", notes)
	}
}

// feature:watch-party
func TestWatchPartyHostApprovesRequestForEveryone(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)
	row := requestFromManaged(t, h, st, partyID, 42000)

	// Only the host decides: the requester cannot approve its own request.
	_, errs := post(t, h, decideRequestBody(partyID, row.Request, false), authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not the party host") {
		t.Fatalf("member approve errors = %#v, want not-the-party-host", errs)
	}

	cursor := lastSeq(t, st)
	if _, errs := post(t, h, decideRequestBody(partyID, row.Request, false), authorizeViewer); len(errs) != 0 {
		t.Fatalf("approve errors = %#v", errs)
	}
	got := map[store.ProfileID]int64{}
	for _, r := range partySyncRows(t, st, partyID, cursor, store.GroupSignalTransition) {
		got[r.ProfileID] = r.PositionMS
	}
	if len(got) != 2 || got["profile-default"] != 42000 || got["profile-managed"] != 42000 {
		t.Fatalf("approved transitions = %#v, want both Profiles at 42000", got)
	}

	// A decided request cannot be decided again.
	_, errs = post(t, h, decideRequestBody(partyID, row.Request, false), authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown request") {
		t.Fatalf("second approve errors = %#v, want unknown request", errs)
	}
}

// feature:watch-party
func TestWatchPartyHostDeclinesRequest(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)
	row := requestFromManaged(t, h, st, partyID, 42000)

	cursor := lastSeq(t, st)
	if _, errs := post(t, h, decideRequestBody(partyID, row.Request, true), authorizeViewer); len(errs) != 0 {
		t.Fatalf("decline errors = %#v", errs)
	}
	if rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalTransition); len(rows) != 0 {
		t.Fatalf("decline broadcast transitions %#v, want none", rows)
	}
	_, errs := post(t, h, decideRequestBody(partyID, "wp-unknown", true), authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown request") {
		t.Fatalf("unknown request errors = %#v, want unknown request", errs)
	}
}

// feature:watch-party
func TestWatchPartyRequestsDroppedOnHandoff(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)
	row := requestFromManaged(t, h, st, partyID, 42000)

	if _, errs := post(t, h, leavePartyBody(partyID), authorizeViewer); len(errs) != 0 {
		t.Fatalf("host leave errors = %#v", errs)
	}
	// The new host is the requester; the request was addressed to the old host.
	_, errs := post(t, h, decideRequestBody(partyID, row.Request, false), authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown request") {
		t.Fatalf("approve after hand-off errors = %#v, want unknown request", errs)
	}
}

package graph

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Watch Party lifecycle (CORE-1763): the host leaving hands the party to a
// remaining member or ends it, and a member whose event stream dropped is
// expired as a backstop to the web viewer's pagehide leave.

func endPartyBody(partyID string) string {
	return `{"query":"mutation L($partyID: String!, $end: Boolean){ leaveWatchParty(partyID:$partyID, end:$end){ partyID } }","variables":{"partyID":"` + partyID + `","end":true}}`
}

// feature:watch-party
func TestWatchPartyHostLeaveHandsOff(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)

	cursor := lastSeq(t, st)
	if _, errs := post(t, h, leavePartyBody(partyID), authorizeViewer); len(errs) != 0 {
		t.Fatalf("host leave errors = %#v", errs)
	}
	rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalRoster)
	if len(rows) != 1 || rows[0].ProfileID != "profile-managed" {
		t.Fatalf("roster after host leave = %#v, want one row for profile-managed", rows)
	}
	r := rows[0]
	if !r.Handoff || r.HostName != "Managed" || r.Host != "session-managed" {
		t.Fatalf("hand-off roster = handoff %v hostName %q host %q, want true Managed session-managed", r.Handoff, r.HostName, r.Host)
	}

	// The new host drives the party; the old host is no longer a member.
	if _, errs := post(t, h, transportBody(partyID, "media-alpha", 5000), authorizeManaged); len(errs) != 0 {
		t.Fatalf("new host transport errors = %#v", errs)
	}
	if _, errs := post(t, h, transportBody(partyID, "media-alpha", 0), authorizeViewer); len(errs) == 0 {
		t.Fatal("old host transport accepted after leaving")
	}
	if p, _ := h.partyHostProfile(partyID); p != "profile-managed" {
		t.Fatalf("party host profile = %q, want profile-managed", p)
	}
}

// feature:watch-party
func TestWatchPartyHostEndsForEveryone(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)

	// A member cannot end the party.
	if _, errs := post(t, h, endPartyBody(partyID), authorizeManaged); len(errs) == 0 {
		t.Fatal("member ended the party")
	}
	cursor := lastSeq(t, st)
	if _, errs := post(t, h, endPartyBody(partyID), authorizeViewer); len(errs) != 0 {
		t.Fatalf("end errors = %#v", errs)
	}
	rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalEnded)
	got := map[store.ProfileID]string{}
	for _, r := range rows {
		got[r.ProfileID] = r.HostName
	}
	if len(got) != 2 || got["profile-default"] != "Default" || got["profile-managed"] != "Default" {
		t.Fatalf("ended rows = %#v, want one per member Profile naming Default", rows)
	}
	if _, err := h.conductor.Party(partyID); err == nil {
		t.Fatal("party still exists after end")
	}
}

// feature:watch-party
func TestWatchPartyDroppedStreamExpires(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)

	h.EventStreamOpened("session-default")
	h.EventStreamOpened("session-managed")
	// The host's tab closes and its pagehide leave never arrives.
	h.conductor.StreamClosed("session-default")

	cursor := lastSeq(t, st)
	h.partyStreamsDropped(context.Background(), 0)
	rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalRoster)
	if len(rows) != 1 || !rows[0].Handoff || rows[0].Host != "session-managed" {
		t.Fatalf("roster after expiry = %#v, want a hand-off to session-managed", rows)
	}
	if h.conductor.SessionInParty("session-default") {
		t.Fatal("expired session still in the party")
	}
	if !h.conductor.SessionInParty("session-managed") {
		t.Fatal("connected member was expired")
	}
}

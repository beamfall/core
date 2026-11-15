package graph

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/beamfall/core/internal/conductor"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Watch Party drift reporting (CORE-1766): a member in another Profile reports
// its measured drift through reportDrift. A nudge is addressed to that member
// only and a resync never moves the host's timeline.

func reportPartyDrift(t *testing.T, h *Handler, partyID string, driftMS int64) ReportDriftResult {
	t.Helper()
	data, errs := post(t, h, reportDriftBody(partyID, driftMS), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("reportDrift(%d) errors = %#v", driftMS, errs)
	}
	var res ReportDriftResult
	if err := json.Unmarshal(data["reportDrift"], &res); err != nil {
		t.Fatalf("decode reportDrift: %v", err)
	}
	return res
}

// feature:watch-party
func TestWatchPartyMemberReportsDriftAcrossProfiles(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)

	if res := reportPartyDrift(t, h, partyID, 5); res.Action != "none" || res.GroupID != partyID {
		t.Fatalf("in-tolerance result = %#v, want action none for %s", res, partyID)
	}

	cursor := lastSeq(t, st)
	res := reportPartyDrift(t, h, partyID, 80)
	if res.Action != "nudge" || res.Rate != -conductor.NudgeRate {
		t.Fatalf("nudge result = %#v, want action nudge rate %v", res, -conductor.NudgeRate)
	}
	nudges := partySyncRows(t, st, partyID, cursor, store.GroupSignalNudge)
	if len(nudges) != 1 {
		t.Fatalf("nudge rows = %#v, want exactly one", nudges)
	}
	if n := nudges[0]; n.ProfileID != "profile-managed" || n.Session != "session-managed" || n.Rate != -conductor.NudgeRate {
		t.Fatalf("nudge row = %#v, want profile-managed/session-managed rate %v", n, -conductor.NudgeRate)
	}
}

// feature:watch-party
func TestWatchPartyMemberResyncDoesNotMoveTheParty(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)

	cursor := lastSeq(t, st)
	if res := reportPartyDrift(t, h, partyID, 900); res.Action != "resync" {
		t.Fatalf("resync result = %#v, want action resync", res)
	}
	if rows := partyTransitionRows(t, st, partyID, cursor); len(rows) != 0 {
		t.Fatalf("member resync wrote transitions %#v, want none", rows)
	}
	if rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalNudge); len(rows) != 0 {
		t.Fatalf("member resync wrote nudges %#v, want none", rows)
	}
}

// feature:watch-party
func TestWatchPartyDriftRefusesNonMember(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formTwoProfileParty(t, h)

	cursor := lastSeq(t, st)
	if _, errs := post(t, h, reportDriftBody(partyID, 80), authorizeRevealed); len(errs) == 0 {
		t.Fatal("non-member reportDrift succeeded, want refusal")
	}
	if rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalNudge); len(rows) != 0 {
		t.Fatalf("refused report wrote nudges %#v", rows)
	}
}

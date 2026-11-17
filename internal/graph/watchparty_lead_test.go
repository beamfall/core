package graph

import (
	"encoding/json"
	"github.com/beamfall/core/internal/conductor"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Lead parties (CORE-2243): the host session owns the audio and reports where
// it audibly is; every other member follows. These drive the mutations and
// read the real outbox rows.

func createLeadPartyBody(content string) string {
	return `{"query":"mutation C($content: String!, $lead: Boolean){ createWatchParty(content:$content, lead:$lead){ partyID } }",` +
		`"variables":{"content":"` + content + `","lead":true,"relaxed":true}}`
}

func leadReportBody(partyID string, positionMS, atMS int64, ratePermille int64) string {
	return `{"query":"mutation D($groupID: String!, $driftMS: Int!, $positionMS: Int, $atMS: Int, $ratePermille: Int){ reportDrift(groupID:$groupID, driftMS:$driftMS, positionMS:$positionMS, atMS:$atMS, ratePermille:$ratePermille){ groupID action rate } }",` +
		`"variables":{"groupID":"` + partyID + `","driftMS":0,"positionMS":` + jsonNumber(positionMS) +
		`,"atMS":` + jsonNumber(atMS) + `,"ratePermille":` + jsonNumber(ratePermille) + `}}`
}

// formLeadParty forms a lead party as the viewer (the lead), joins the managed
// Profile as a follower and starts playback at zero.
func formLeadParty(t *testing.T, h *Handler) string {
	t.Helper()
	data, errs := post(t, h, createLeadPartyBody("media-alpha"), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("createWatchParty(lead) errors = %#v", errs)
	}
	var info WatchPartyInfo
	if err := json.Unmarshal(data["createWatchParty"], &info); err != nil || info.PartyID == "" {
		t.Fatalf("decode createWatchParty: %v (%s)", err, data["createWatchParty"])
	}
	for _, step := range []struct {
		body      string
		authorize func(*http.Request)
	}{
		{inviteBody(info.PartyID, "Managed"), authorizeViewer},
		{acceptBody(info.PartyID), authorizeManaged},
		{pausedTransportBody(info.PartyID, "media-alpha", 0, false), authorizeViewer},
	} {
		if _, errs := post(t, h, step.body, step.authorize); len(errs) != 0 {
			t.Fatalf("lead party setup errors = %#v", errs)
		}
	}
	return info.PartyID
}

func leadReport(t *testing.T, h *Handler, partyID string, positionMS, atMS, rate int64) ReportDriftResult {
	t.Helper()
	data, errs := post(t, h, leadReportBody(partyID, positionMS, atMS, rate), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("lead report errors = %#v", errs)
	}
	var res ReportDriftResult
	if err := json.Unmarshal(data["reportDrift"], &res); err != nil {
		t.Fatalf("decode reportDrift: %v", err)
	}
	return res
}

// feature:watch-party
func TestLeadPartyStallReachesEveryFollowerProfile(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	h.SetConductor(conductor.NewRegistryWithClock(func() time.Time { return now }))
	partyID := formLeadParty(t, h)
	// A second into playback, clear of the floor between statements.
	now = now.Add(time.Second)

	// On time: nothing is sent.
	cursor := lastSeq(t, st)
	if res := leadReport(t, h, partyID, 1020, now.UnixMilli(), 0); res.Action != "none" {
		t.Fatalf("on-time report = %#v, want action none", res)
	}
	if rows := partyTransitionRows(t, st, partyID, cursor); len(rows) != 0 {
		t.Fatalf("on-time report wrote %d transitions, want none", len(rows))
	}

	// The lead is 2 s away from the timeline at a changed speed: one transition
	// per member Profile, stated now with no pre-buffer, marked lead.
	if res := leadReport(t, h, partyID, 3000, now.UnixMilli(), 1250); res.Action != "anchor" || res.GroupID != partyID {
		t.Fatalf("stall report = %#v, want action anchor", res)
	}
	rows := partyTransitionRows(t, st, partyID, cursor)
	var profiles []string
	for _, p := range rows {
		profiles = append(profiles, string(p.ProfileID))
		if p.PositionMS != 3000 || p.PresentAtMS != now.UnixMilli() || !p.Lead || p.RatePermille != 1250 || p.Paused {
			t.Errorf("row for %s = %+v, want 3000 presented now, lead, rate 1250", p.ProfileID, p)
		}
	}
	slices.Sort(profiles)
	if want := []string{"profile-default", "profile-managed"}; !slices.Equal(profiles, want) {
		t.Fatalf("lead transition addressed to %v, want %v", profiles, want)
	}
}

// feature:watch-party
func TestLeadPartyOnlyTheLeadMovesIt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)
	partyID := formLeadParty(t, h)

	cursor := lastSeq(t, st)
	// `relaxed` was passed at create: a lead party ignores it.
	if _, errs := post(t, h, requestTransportBody(partyID, "media-alpha", 9000), authorizeManaged); len(errs) == 0 || !strings.Contains(errs[0].Message, "not the party host") {
		t.Fatalf("follower transport errors = %#v, want a host-only refusal", errs)
	}
	if _, errs := post(t, h, leadReportBody(partyID, 9000, now.UnixMilli(), 0), authorizeManaged); len(errs) == 0 || !strings.Contains(errs[0].Message, "only the lead reports") {
		t.Fatalf("follower report errors = %#v, want a refusal", errs)
	}
	if rows := partyTransitionRows(t, st, partyID, cursor); len(rows) != 0 {
		t.Fatalf("a follower moved the lead timeline: %#v", rows)
	}
	if rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalRequest); len(rows) != 0 {
		t.Fatalf("a follower's transport reached the lead as a request: %#v", rows)
	}
}

// feature:watch-party
func TestLeadPartyEndsWhenTheLeadLeavesAndFollowersRejoin(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)
	partyID := formLeadParty(t, h)

	// A follower that left (a reload) accepts again on the same invite.
	if _, errs := post(t, h, leavePartyBody(partyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("follower leave errors = %#v", errs)
	}
	if _, errs := post(t, h, acceptBody(partyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("follower rejoin errors = %#v", errs)
	}

	cursor := lastSeq(t, st)
	if _, errs := post(t, h, leavePartyBody(partyID), authorizeViewer); len(errs) != 0 {
		t.Fatalf("lead leave errors = %#v", errs)
	}
	ended := partySyncRows(t, st, partyID, cursor, store.GroupSignalEnded)
	var told []string
	for _, p := range ended {
		told = append(told, string(p.ProfileID))
	}
	if !slices.Contains(told, "profile-managed") {
		t.Fatalf("ended rows went to %v, want the follower's Profile", told)
	}
	if rows := partySyncRows(t, st, partyID, cursor, store.GroupSignalRoster); len(rows) != 0 {
		t.Fatalf("the lead's leave handed the party off: %#v", rows)
	}
	if _, errs := post(t, h, acceptBody(partyID), authorizeManaged); len(errs) == 0 {
		t.Fatal("a follower joined a party whose lead had left")
	}
}

// feature:watch-party
func TestLeadPartyRosterTellsAJoiningScreenItFollows(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)
	partyID := formLeadParty(t, h)

	rows := partySyncRows(t, st, partyID, 0, store.GroupSignalRoster)
	if len(rows) == 0 {
		t.Fatal("no roster rows for the lead party")
	}
	for _, p := range rows {
		if !p.Lead {
			t.Errorf("roster for %s = %+v, want it marked lead", p.ProfileID, p)
		}
	}
}

// feature:watch-party
func TestLeadPartyRefusesAmbientMovesAndEmptyReports(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())
	partyID := formLeadParty(t, h)
	cursor := lastSeq(t, st)

	// A session in the lead's own Profile cannot move the party as an Ambient Group.
	if _, errs := post(t, h, transitionBody(partyID, "media-alpha", 60000), authorizeViewer); len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown group") {
		t.Fatalf("transitionGroup on a party errors = %#v, want unknown group", errs)
	}
	// A lead report with no position or sample instant is refused, not read as zero.
	if _, errs := post(t, h, reportDriftBody(partyID, 0), authorizeViewer); len(errs) == 0 || !strings.Contains(errs[0].Message, "positionMS is required") {
		t.Fatalf("empty lead report errors = %#v, want a refusal", errs)
	}
	if _, errs := post(t, h, leadReportBody(partyID, 9000, 0, 0), authorizeViewer); len(errs) == 0 || !strings.Contains(errs[0].Message, "atMS is required") {
		t.Fatalf("lead report without atMS errors = %#v, want a refusal", errs)
	}
	if rows := partyTransitionRows(t, st, partyID, cursor); len(rows) != 0 {
		t.Fatalf("a refused call moved the lead timeline: %#v", rows)
	}
}

// feature:watch-party
func TestLeadPartyIsHeldForEveryProfileWhenTheLeadFallsSilent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	h.SetConductor(conductor.NewRegistryWithClock(func() time.Time { return now }))
	partyID := formLeadParty(t, h)
	now = now.Add(time.Second)
	leadReport(t, h, partyID, 1000, now.UnixMilli(), 0)

	// The lead is still within its allowance: nothing is sent.
	cursor := lastSeq(t, st)
	now = now.Add(2 * time.Second)
	h.leadSilenceCheck(t.Context(), partyID)
	if rows := partyTransitionRows(t, st, partyID, cursor); len(rows) != 0 {
		t.Fatalf("check at 2 s wrote %d transitions, want none", len(rows))
	}

	// Three seconds of silence: every member Profile is paused where the lead
	// was last heard.
	now = now.Add(time.Second)
	h.leadSilenceCheck(t.Context(), partyID)
	rows := partyTransitionRows(t, st, partyID, cursor)
	if len(rows) != 2 {
		t.Fatalf("hold wrote %d transitions, want one per member Profile", len(rows))
	}
	for _, p := range rows {
		if !p.Paused || !p.Lead || p.PositionMS != 1000 {
			t.Errorf("hold row for %s = %+v, want paused lead transition at 1000", p.ProfileID, p)
		}
	}

	// The lead's next report resumes every Profile on that report.
	cursor = lastSeq(t, st)
	now = now.Add(4 * time.Second)
	if res := leadReport(t, h, partyID, 1500, now.UnixMilli(), 0); res.Action != "anchor" {
		t.Fatalf("report after hold = %#v, want action anchor", res)
	}
	rows = partyTransitionRows(t, st, partyID, cursor)
	if len(rows) != 2 {
		t.Fatalf("resume wrote %d transitions, want one per member Profile", len(rows))
	}
	for _, p := range rows {
		if p.Paused || p.PositionMS != 1500 {
			t.Errorf("resume row for %s = %+v, want playing at 1500", p.ProfileID, p)
		}
	}
}

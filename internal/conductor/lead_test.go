package conductor

import (
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// leadParty forms a lead party on a settable clock, joins the lead and the
// followers (each in its own invited Profile) and starts playback at zero.
func leadParty(t *testing.T, followers ...store.SessionID) (*Registry, *Group, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC)
	r := NewRegistryWithClock(func() time.Time { return now })
	g := r.FormLeadParty("profile-lead", "lead", "media-alpha")
	join := func(profile store.ProfileID, session store.SessionID) {
		if err := r.Invite(g.ID, "lead", profile); err != nil {
			t.Fatalf("Invite(%s): %v", profile, err)
		}
		if err := r.Accept(g.ID, profile, Member{SessionID: session}); err != nil {
			t.Fatalf("Accept(%s): %v", profile, err)
		}
	}
	join("profile-lead", "lead")
	for _, s := range followers {
		join(store.ProfileID("profile-"+string(s)), s)
	}
	if _, _, err := r.HostTransition(g.ID, "lead", "media-alpha", 0, false); err != nil {
		t.Fatalf("HostTransition: %v", err)
	}
	return r, g, &now
}

// TestLeadStallMovesEveryFollowerTarget covers the CORE-2243 stall criterion: a
// lead that stalls for 2 s reports a position 2 s behind the timeline, and that
// one report moves the target every follower reads.
func TestLeadStallMovesEveryFollowerTarget(t *testing.T) {
	r, g, now := leadParty(t, "screen-a", "screen-b")

	*now = now.Add(10 * time.Second)
	before, err := r.PartyAnchor(g.ID)
	if err != nil {
		t.Fatalf("PartyAnchor: %v", err)
	}
	if before.Position != 10*time.Second {
		t.Fatalf("target before the stall = %v, want 10s", before.Position)
	}

	// The lead stalled for 2 s: at 12 s of wall time it is audibly at 10 s.
	*now = now.Add(2 * time.Second)
	tr, members, restate, err := r.LeadReport(g.ID, "lead", 10*time.Second, *now, 0)
	if err != nil {
		t.Fatalf("LeadReport: %v", err)
	}
	if !restate {
		t.Fatal("a 2 s stall did not restate the timeline")
	}
	if len(members) != 3 {
		t.Fatalf("restated to %d members, want 3", len(members))
	}
	if tr.Position != 10*time.Second || !tr.PresentAt.Equal(*now) || !tr.Lead || tr.Paused {
		t.Fatalf("transition = %+v, want 10s presented now on a lead timeline", tr)
	}
	// Every follower reads the same anchor: one second on, the target is 11 s.
	*now = now.Add(time.Second)
	after, err := r.PartyAnchor(g.ID)
	if err != nil {
		t.Fatalf("PartyAnchor: %v", err)
	}
	if after.Position != 11*time.Second {
		t.Fatalf("target after the stall = %v, want 11s", after.Position)
	}
}

func TestLeadReportInsideThresholdLeavesTimeline(t *testing.T) {
	r, g, now := leadParty(t, "screen-a")
	*now = now.Add(3 * time.Second)
	_, _, restate, err := r.LeadReport(g.ID, "lead", 3*time.Second+LeadReanchorThreshold, *now, 0)
	if err != nil {
		t.Fatalf("LeadReport: %v", err)
	}
	if restate {
		t.Fatal("a report at the threshold restated the timeline")
	}
	// The sample instant, not the arrival instant, is what is compared: a report
	// that arrives 300 ms late about a position that was on time changes nothing.
	sampled := *now
	*now = now.Add(300 * time.Millisecond)
	if _, _, restate, _ = r.LeadReport(g.ID, "lead", 3*time.Second, sampled, 0); restate {
		t.Fatal("a late-arriving on-time report restated the timeline")
	}
}

func TestLeadReportHeartbeatRestatesWithoutMoving(t *testing.T) {
	r, g, now := leadParty(t, "screen-a")
	*now = now.Add(LeadHeartbeat)
	tr, _, restate, err := r.LeadReport(g.ID, "lead", LeadHeartbeat, *now, 0)
	if err != nil {
		t.Fatalf("LeadReport: %v", err)
	}
	if !restate || tr.Position != LeadHeartbeat {
		t.Fatalf("heartbeat = restate %v at %v, want a restatement at %v", restate, tr.Position, LeadHeartbeat)
	}
	*now = now.Add(time.Second)
	if _, _, restate, _ = r.LeadReport(g.ID, "lead", LeadHeartbeat+time.Second, *now, 0); restate {
		t.Fatal("restated again inside the heartbeat")
	}
}

// TestLeadRateChangeRunsTheTimelineAtThatSpeed covers tempo: the lead's speed
// is part of the shared timeline, so followers advance at it.
func TestLeadRateChangeRunsTheTimelineAtThatSpeed(t *testing.T) {
	r, g, now := leadParty(t, "screen-a")
	*now = now.Add(4 * time.Second)
	tr, _, restate, err := r.LeadReport(g.ID, "lead", 4*time.Second, *now, 1250)
	if err != nil {
		t.Fatalf("LeadReport: %v", err)
	}
	if !restate || tr.RatePermille != 1250 {
		t.Fatalf("rate change = restate %v rate %d, want a restatement at 1250", restate, tr.RatePermille)
	}
	*now = now.Add(4 * time.Second)
	anchor, _ := r.PartyAnchor(g.ID)
	if anchor.Position != 9*time.Second || anchor.RatePermille != 1250 {
		t.Fatalf("anchor = %v at %d, want 9s at 1250", anchor.Position, anchor.RatePermille)
	}
}

func TestLeadReportRefusals(t *testing.T) {
	r, g, now := leadParty(t, "screen-a")
	if _, _, _, err := r.LeadReport(g.ID, "screen-a", 0, *now, 0); !errors.Is(err, ErrNotHost) {
		t.Fatalf("follower report err = %v, want ErrNotHost", err)
	}
	if _, _, err := r.HostTransition(g.ID, "screen-a", "media-alpha", 0, false); !errors.Is(err, ErrNotHost) {
		t.Fatalf("follower transport err = %v, want ErrNotHost", err)
	}
	plain := r.FormParty("profile-host", "host", "media-alpha", false)
	if _, _, _, err := r.LeadReport(plain.ID, "host", 0, *now, 0); !errors.Is(err, ErrNotLead) {
		t.Fatalf("report on a plain party err = %v, want ErrNotLead", err)
	}
	// A paused lead timeline is not moved by a report.
	if _, _, err := r.HostTransition(g.ID, "lead", "media-alpha", 7*time.Second, true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	*now = now.Add(time.Minute)
	tr, _, restate, err := r.LeadReport(g.ID, "lead", 30*time.Second, *now, 0)
	if err != nil || restate || tr.Position != 7*time.Second || !tr.Paused {
		t.Fatalf("report while paused = %+v restate %v err %v, want the held 7s", tr, restate, err)
	}
}

func TestLeadPartyFollowerRejoinsAndPartyEndsWithLead(t *testing.T) {
	r, g, _ := leadParty(t, "screen-a", "screen-b")
	// A follower that reloads accepts again on the same invite.
	if _, err := r.LeaveParty(g.ID, "screen-a"); err != nil {
		t.Fatalf("LeaveParty(follower): %v", err)
	}
	if err := r.Accept(g.ID, "profile-screen-a", Member{SessionID: "screen-a-2"}); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	// Another screen in the lead's own Profile joins without a new invite.
	if err := r.Accept(g.ID, "profile-lead", Member{SessionID: "screen-c"}); err != nil {
		t.Fatalf("lead-Profile screen: %v", err)
	}
	leave, err := r.LeaveParty(g.ID, "lead")
	if err != nil {
		t.Fatalf("LeaveParty(lead): %v", err)
	}
	if !leave.Dissolved || leave.NewHost != "" || len(leave.Ended) != 3 {
		t.Fatalf("lead leave = %+v, want the party ended for 3 followers with no hand-off", leave)
	}
	if _, err := r.Party(g.ID); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("party after the lead left err = %v, want ErrNoGroup", err)
	}
}

// TestLeadReportDropsSamplesThatSayNothingAboutNow covers the reports a lead
// party ignores: a sample older than leadReportMaxAge, a sample taken before
// the lead last moved the timeline, and a report inside leadRestateFloor.
func TestLeadReportDropsSamplesThatSayNothingAboutNow(t *testing.T) {
	r, g, now := leadParty(t, "screen-a")
	*now = now.Add(20 * time.Second)

	// On time, but the report arrived 8 s late: the timeline does not move.
	if _, _, restate, err := r.LeadReport(g.ID, "lead", 12*time.Second, now.Add(-8*time.Second), 0); err != nil || restate {
		t.Fatalf("stale report restate = %v err %v, want it dropped", restate, err)
	}
	if a, _ := r.PartyAnchor(g.ID); a.Position != 20*time.Second {
		t.Fatalf("timeline after a stale report = %v, want 20s", a.Position)
	}

	// The lead seeks to 60 s; a report sampled 200 ms before the seek arrives after it.
	sampled := now.Add(-200 * time.Millisecond)
	if _, _, err := r.HostTransition(g.ID, "lead", "media-alpha", time.Minute, false); err != nil {
		t.Fatalf("seek: %v", err)
	}
	*now = now.Add(time.Second)
	if _, _, restate, err := r.LeadReport(g.ID, "lead", 19800*time.Millisecond, sampled, 0); err != nil || restate {
		t.Fatalf("pre-seek report restate = %v err %v, want it dropped", restate, err)
	}
	if a, _ := r.PartyAnchor(g.ID); a.Position != 61*time.Second {
		t.Fatalf("timeline after a pre-seek report = %v, want 61s", a.Position)
	}

	// Two reports that each move the timeline, 100 ms apart: only the first is stated.
	if _, _, restate, _ := r.LeadReport(g.ID, "lead", 50*time.Second, *now, 0); !restate {
		t.Fatal("a 11 s deviation did not restate")
	}
	*now = now.Add(100 * time.Millisecond)
	if _, _, restate, _ := r.LeadReport(g.ID, "lead", 40*time.Second, *now, 0); restate {
		t.Fatal("a second restatement inside the floor was sent")
	}
	*now = now.Add(leadRestateFloor)
	if _, _, restate, _ := r.LeadReport(g.ID, "lead", 40*time.Second, *now, 0); !restate {
		t.Fatal("a report after the floor did not restate")
	}

	// A negative position is read as zero.
	*now = now.Add(time.Second)
	if tr, _, _, _ := r.LeadReport(g.ID, "lead", -time.Hour, *now, 0); tr.Position != 0 {
		t.Fatalf("negative report position = %v, want 0", tr.Position)
	}
}

// TestWatchPartyIsClosedToAmbientGroupOperations covers the same-Profile
// follower: a second session in the lead's Profile cannot join, leave or move
// the party through the Ambient Group paths, which check the Profile only.
func TestWatchPartyIsClosedToAmbientGroupOperations(t *testing.T) {
	r, g, _ := leadParty(t, "screen-a")
	if err := r.Accept(g.ID, "profile-lead", Member{SessionID: "second-screen"}); err != nil {
		t.Fatalf("Accept(second screen): %v", err)
	}
	if _, _, err := r.Transition(g.ID, "profile-lead", "second-screen", "media-beta", time.Minute); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("Transition err = %v, want ErrNoGroup", err)
	}
	if err := r.Join(g.ID, "profile-lead", Member{SessionID: "uninvited"}); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("Join err = %v, want ErrNoGroup", err)
	}
	if err := r.Leave(g.ID, "profile-lead", "lead"); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("Leave err = %v, want ErrNoGroup", err)
	}
	if _, err := r.Group(g.ID, "profile-lead"); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("Group err = %v, want ErrNoGroup", err)
	}
	if _, _, err := r.ReportDrift(g.ID, "profile-lead", "second-screen", time.Second); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("ReportDrift err = %v, want ErrNoGroup", err)
	}
	a, _ := r.PartyAnchor(g.ID)
	if a.Item != "media-alpha" || len(g.Members()) != 3 {
		t.Fatalf("party changed: item %s, %d members", a.Item, len(g.Members()))
	}
}

// TestLeadPartyEndsWhenTheLeadsStreamExpires covers the backstop: a lead whose
// event stream stays closed past the grace window ends the party, and no
// follower becomes host.
func TestLeadPartyEndsWhenTheLeadsStreamExpires(t *testing.T) {
	r, g, now := leadParty(t, "screen-a", "screen-b")
	const grace = 20 * time.Second
	for _, s := range []store.SessionID{"lead", "screen-a", "screen-b"} {
		r.StreamOpened(s)
	}
	r.StreamClosed("lead")
	*now = now.Add(2 * grace)
	got := r.ExpireDisconnected(grace)
	if len(got) != 1 || got[0].Session != "lead" || !got[0].Leave.Dissolved || got[0].Leave.NewHost != "" {
		t.Fatalf("lead expiry = %+v, want the party dissolved with no new host", got)
	}
	if _, err := r.Party(g.ID); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("Party after the lead expired err = %v, want ErrNoGroup", err)
	}
}

// TestSilentLeadIsHeldAndResumesOnItsNextReport covers CORE-2266: a playing
// lead party whose lead stops reporting is held where the lead was last heard,
// and the lead's next report resumes it on that report.
func TestSilentLeadIsHeldAndResumesOnItsNextReport(t *testing.T) {
	r, g, now := leadParty(t, "screen-1")
	*now = now.Add(time.Second)
	if _, _, _, err := r.LeadReport(g.ID, "lead", time.Second, *now, 0); err != nil {
		t.Fatalf("LeadReport: %v", err)
	}

	// Two seconds after the last report the party is not yet due.
	*now = now.Add(2 * time.Second)
	if _, _, held, wait, err := r.HoldSilentLead(g.ID); err != nil || held || wait != time.Second {
		t.Fatalf("HoldSilentLead at 2 s = held %v wait %v err %v, want a 1 s wait", held, wait, err)
	}

	// At three seconds it is held at the position of the last report.
	*now = now.Add(time.Second)
	tr, members, held, _, err := r.HoldSilentLead(g.ID)
	if err != nil || !held {
		t.Fatalf("HoldSilentLead at 3 s = held %v err %v, want held", held, err)
	}
	if !tr.Paused || !tr.Lead || tr.Position != time.Second || len(members) != 2 {
		t.Fatalf("hold = %+v to %d members, want paused at 1 s to 2 members", tr, len(members))
	}
	// A held party needs no further check and does not advance.
	*now = now.Add(10 * time.Second)
	if _, _, held, wait, err := r.HoldSilentLead(g.ID); err != nil || held || wait != 0 {
		t.Fatalf("HoldSilentLead while held = held %v wait %v err %v, want nothing", held, wait, err)
	}
	if a, _ := r.PartyAnchor(g.ID); a.Position != time.Second || !a.Paused {
		t.Fatalf("held anchor = %+v, want paused at 1 s", a)
	}

	// The lead's next report resumes the timeline on that report.
	tr, _, restate, err := r.LeadReport(g.ID, "lead", 12*time.Second, *now, 0)
	if err != nil || !restate || tr.Paused || tr.Position != 12*time.Second {
		t.Fatalf("report after hold = %+v restate %v err %v, want playing at 12 s", tr, restate, err)
	}
	*now = now.Add(time.Second)
	if a, _ := r.PartyAnchor(g.ID); a.Position != 13*time.Second || a.Paused {
		t.Fatalf("anchor after resume = %+v, want playing at 13 s", a)
	}
}

// TestLeadPausedPartyIsNotHeldOrMovedByAReport covers the other half of
// CORE-2266: a pause the lead made is not a hold, so silence sends nothing and
// a later report does not move the timeline. A follower cannot hold a party.
func TestLeadPausedPartyIsNotHeldOrMovedByAReport(t *testing.T) {
	r, g, now := leadParty(t, "screen-1")
	if _, _, err := r.HostTransition(g.ID, "lead", "media-alpha", 5*time.Second, true); err != nil {
		t.Fatalf("HostTransition: %v", err)
	}
	*now = now.Add(10 * time.Second)
	if _, _, held, wait, err := r.HoldSilentLead(g.ID); err != nil || held || wait != 0 {
		t.Fatalf("HoldSilentLead on a paused party = held %v wait %v err %v, want nothing", held, wait, err)
	}
	if _, _, restate, err := r.LeadReport(g.ID, "lead", 30*time.Second, *now, 0); err != nil || restate {
		t.Fatalf("report on a lead-paused party = restate %v err %v, want dropped", restate, err)
	}
	if a, _ := r.PartyAnchor(g.ID); a.Position != 5*time.Second || !a.Paused {
		t.Fatalf("anchor = %+v, want paused at 5 s", a)
	}

	// The lead's own transport ends a hold too.
	if _, _, err := r.HostTransition(g.ID, "lead", "media-alpha", 5*time.Second, false); err != nil {
		t.Fatalf("HostTransition: %v", err)
	}
	*now = now.Add(LeadSilence)
	if _, _, held, _, _ := r.HoldSilentLead(g.ID); !held {
		t.Fatal("silent lead not held")
	}
	if tr, _, err := r.HostTransition(g.ID, "lead", "media-alpha", 7*time.Second, false); err != nil || tr.Paused {
		t.Fatalf("transport after hold = %+v err %v, want playing", tr, err)
	}
	if _, _, _, _, err := r.HoldSilentLead("no-such-party"); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("HoldSilentLead(unknown) = %v, want ErrNoGroup", err)
	}
	plain := r.FormParty("profile-lead", "lead", "media-alpha", false)
	if _, _, _, _, err := r.HoldSilentLead(plain.ID); !errors.Is(err, ErrNotLead) {
		t.Fatalf("HoldSilentLead(plain party) = %v, want ErrNotLead", err)
	}
}

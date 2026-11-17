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

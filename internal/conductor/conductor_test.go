package conductor

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestCalibrateComputesOffsetAndRoundTrip is the AV-4 NTP acceptance (a)
// (ADR-0035 §8 "Latency calibration"): the four-timestamp exchange computes the
// correct clock offset and round-trip from known timestamps. Construct a
// scenario with a KNOWN true offset and a KNOWN symmetric network delay, then
// confirm Calibrate recovers both — the whole point of the four-timestamp design
// is that symmetric path delay cancels out of the offset estimate.
func TestCalibrateComputesOffsetAndRoundTrip(t *testing.T) {
	// Ground truth: the member's clock is 200 ms AHEAD of the server's, and the
	// one-way network delay is 30 ms each direction (60 ms round-trip).
	const trueOffset = 200 * time.Millisecond
	const oneWay = 30 * time.Millisecond

	// Server clock baseline. The member clock is server + trueOffset.
	serverEpoch := time.Unix(1_700_000_000, 0).UTC()
	toMember := func(serverInstant time.Time) time.Time { return serverInstant.Add(trueOffset) }

	// t1: member sends at member-clock T0.
	t1 := toMember(serverEpoch)
	// The request arrives at the server oneWay later (server clock).
	t2 := serverEpoch.Add(oneWay)
	// The server replies essentially immediately (5 ms processing).
	t3 := t2.Add(5 * time.Millisecond)
	// The reply arrives at the member oneWay after t3, on the MEMBER clock.
	t4 := toMember(t3.Add(oneWay))

	cal := Calibrate(t1, t2, t3, t4)

	if cal.Offset != trueOffset {
		t.Errorf("offset = %v, want %v", cal.Offset, trueOffset)
	}
	// Round-trip is the two one-way delays (server processing is excluded by the
	// four-timestamp math): 2 * oneWay = 60 ms.
	if want := 2 * oneWay; cal.RoundTrip != want {
		t.Errorf("round-trip = %v, want %v", cal.RoundTrip, want)
	}
}

// TestCalibrateNegativeOffset confirms the sign convention: a member whose clock
// is BEHIND the server's yields a negative offset.
func TestCalibrateNegativeOffset(t *testing.T) {
	const trueOffset = -150 * time.Millisecond
	const oneWay = 20 * time.Millisecond
	serverEpoch := time.Unix(1_700_000_000, 0).UTC()
	toMember := func(s time.Time) time.Time { return s.Add(trueOffset) }

	t1 := toMember(serverEpoch)
	t2 := serverEpoch.Add(oneWay)
	t3 := t2
	t4 := toMember(t3.Add(oneWay))

	if got := Calibrate(t1, t2, t3, t4).Offset; got != trueOffset {
		t.Errorf("offset = %v, want %v", got, trueOffset)
	}
}

// TestReleaseAtConvertsViaOffsetAndLatency is the per-member conversion half of
// AV-4 acceptance (b) (ADR-0035 §8 "Transition protocol", step 3): a member
// converts the server present_at to its own clock via its offset, then backs off
// by its render latency, so the sample reaches its speaker at the scheduled
// instant. Two members with different offsets and latencies must derive
// different LOCAL release instants from the SAME server present_at.
func TestReleaseAtConvertsViaOffsetAndLatency(t *testing.T) {
	presentAt := time.Unix(1_700_000_000, 0).UTC() // a server-clock instant

	// Member A: clock +200 ms ahead, 50 ms render latency.
	a := Member{ClockOffset: 200 * time.Millisecond, RenderLatency: 50 * time.Millisecond}
	// Member B: clock -100 ms behind, 120 ms render latency.
	b := Member{ClockOffset: -100 * time.Millisecond, RenderLatency: 120 * time.Millisecond}

	wantA := presentAt.Add(200 * time.Millisecond).Add(-50 * time.Millisecond)
	wantB := presentAt.Add(-100 * time.Millisecond).Add(-120 * time.Millisecond)

	if got := a.ReleaseAt(presentAt); !got.Equal(wantA) {
		t.Errorf("member A release = %v, want %v", got, wantA)
	}
	if got := b.ReleaseAt(presentAt); !got.Equal(wantB) {
		t.Errorf("member B release = %v, want %v", got, wantB)
	}
	// The whole point: the same server instant maps to DIFFERENT local releases,
	// yet both sounds hit the air at the same wall-clock moment.
	if a.ReleaseAt(presentAt).Equal(b.ReleaseAt(presentAt)) {
		t.Error("two differently-calibrated members derived the same local release instant")
	}
}

// TestClassifyDriftThresholds is the AV-4 drift acceptance (c)
// (ADR-0035 §8 "Drift correction"): drift > 500 ms is a full re-sync; recoverable
// drift (>= 10 ms, <= 500 ms) is a ±0.1 % nudge whose sign opposes the drift;
// sub-threshold drift is no correction.
func TestClassifyDriftThresholds(t *testing.T) {
	cases := []struct {
		name   string
		drift  time.Duration
		action DriftAction
		rate   float64
	}{
		{"within window, no signal", 5 * time.Millisecond, DriftNone, 0},
		{"at nudge threshold, ahead → slow down", 10 * time.Millisecond, DriftNudge, -NudgeRate},
		{"behind → speed up", -200 * time.Millisecond, DriftNudge, NudgeRate},
		{"ahead, recoverable → slow down", 200 * time.Millisecond, DriftNudge, -NudgeRate},
		{"at resync boundary stays a nudge", 500 * time.Millisecond, DriftNudge, -NudgeRate},
		{"beyond recoverable → resync", 501 * time.Millisecond, DriftResync, 0},
		{"far behind → resync", -2 * time.Second, DriftResync, 0},
		{"minimum duration → resync", time.Duration(-1 << 63), DriftResync, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyDrift(tc.drift)
			if got.Action != tc.action {
				t.Errorf("action = %v, want %v", got.Action, tc.action)
			}
			if got.Rate != tc.rate {
				t.Errorf("rate = %v, want %v", got.Rate, tc.rate)
			}
		})
	}
}

// TestFreshRegistryIsEmpty is the AV-4 ephemerality acceptance (e):
// a fresh Registry holds no persisted state — there is
// nothing to load, no migration, no durable key. A brand-new Registry has zero
// groups, and a group dissolves the moment its last member leaves (so a restart,
// which starts from a fresh Registry, cannot resurrect a stale group).
func TestFreshRegistryIsEmpty(t *testing.T) {
	r := NewRegistry()
	if r.GroupCount() != 0 {
		t.Fatalf("fresh registry has %d groups, want 0 (nothing is persisted)", r.GroupCount())
	}

	g := r.Form("profile-default")
	if err := r.Join(g.ID, "profile-default", Member{SessionID: "s1"}); err != nil {
		t.Fatalf("join: %v", err)
	}
	if r.GroupCount() != 1 {
		t.Fatalf("after form+join, group count = %d, want 1", r.GroupCount())
	}

	// The last member leaving dissolves the group entirely — no orphaned state
	// lingers (ADR-0035 §8 "Leave": the group dissolves when the last member
	// leaves).
	if err := r.Leave(g.ID, "profile-default", "s1"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if r.GroupCount() != 0 {
		t.Fatalf("after last member left, group count = %d, want 0 (group dissolved)", r.GroupCount())
	}
	// The dissolved group is unreachable — a stale id resolves to nothing.
	if _, err := r.Group(g.ID, "profile-default"); err != ErrNoGroup {
		t.Fatalf("dissolved group lookup err = %v, want ErrNoGroup", err)
	}
}

// TestTransitionAnchorsAndSchedulesPresent confirms a transition anchors the
// group at the present position and schedules present_at one pre-buffer window
// out (ADR-0035 §8 "Transition protocol": present_at = now + 2 s).
func TestTransitionAnchorsAndSchedulesPresent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := NewRegistryWithClock(func() time.Time { return now })
	g := r.Form("profile-default")
	if err := r.Join(g.ID, "profile-default", Member{SessionID: "s1"}); err != nil {
		t.Fatalf("join: %v", err)
	}

	tr, members, err := r.Transition(g.ID, "profile-default", "s1", "media-alpha", 12*time.Second)
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if tr.Item != "media-alpha" || tr.Position != 12*time.Second {
		t.Errorf("transition item/pos = %q/%v, want media-alpha/12s", tr.Item, tr.Position)
	}
	if want := now.Add(PreBufferWindow); !tr.PresentAt.Equal(want) {
		t.Errorf("present_at = %v, want now+prebuffer %v", tr.PresentAt, want)
	}
	if len(members) != 1 || members[0].SessionID != "s1" {
		t.Errorf("transition members = %#v, want [s1]", members)
	}

	// The expected position advances with the wall clock from the anchor.
	if got := g.ExpectedPosition(now.Add(3 * time.Second)); got != 15*time.Second {
		t.Errorf("expected position 3s after anchor = %v, want 15s", got)
	}
}

func TestTransitionRejectsNonMemberBeforeMutation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := NewRegistryWithClock(func() time.Time { return now })
	g := r.Form("profile-default")
	if err := r.Join(g.ID, "profile-default", Member{SessionID: "member"}); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, _, err := r.Transition(g.ID, "profile-default", "member", "media-before", 7*time.Second); err != nil {
		t.Fatalf("initial transition: %v", err)
	}

	g.mu.Lock()
	wantItem, wantPosition, wantAnchor := g.item, g.position, g.anchor
	g.mu.Unlock()
	if _, _, err := r.Transition(g.ID, "profile-default", "intruder", "media-after", 99*time.Second); err != ErrNotMember {
		t.Fatalf("non-member transition error = %v, want ErrNotMember", err)
	}
	g.mu.Lock()
	gotItem, gotPosition, gotAnchor := g.item, g.position, g.anchor
	g.mu.Unlock()
	if gotItem != wantItem || gotPosition != wantPosition || !gotAnchor.Equal(wantAnchor) {
		t.Fatalf("rejected transition mutated group: item=%q position=%v anchor=%v; want %q/%v/%v", gotItem, gotPosition, gotAnchor, wantItem, wantPosition, wantAnchor)
	}
}

// TestReportDriftResyncReanchors confirms a > 500 ms drift triggers a full
// re-sync that re-anchors the group to its current expected position with a
// fresh present_at, while a small drift returns a nudge and no transition
// (ADR-0035 §8 "Drift correction").
func TestReportDriftResyncReanchors(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	clock := now
	r := NewRegistryWithClock(func() time.Time { return clock })
	g := r.Form("profile-default")
	if err := r.Join(g.ID, "profile-default", Member{SessionID: "s1"}); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, _, err := r.Transition(g.ID, "profile-default", "s1", "media-alpha", 0); err != nil {
		t.Fatalf("transition: %v", err)
	}

	// Advance the clock 10 s, then a member reports a 2 s drift (unrecoverable).
	clock = now.Add(10 * time.Second)
	decision, resync, err := r.ReportDrift(g.ID, "profile-default", "s1", 2*time.Second)
	if err != nil {
		t.Fatalf("report drift: %v", err)
	}
	if decision.Action != DriftResync {
		t.Fatalf("drift action = %v, want resync", decision.Action)
	}
	if resync == nil {
		t.Fatal("resync drift returned no transition")
	}
	// The re-sync re-anchors to the canonical position (0 + 10 s elapsed = 10 s)
	// with a fresh present_at one pre-buffer window from the new anchor.
	if resync.Position != 10*time.Second {
		t.Errorf("resync position = %v, want 10s (canonical timeline)", resync.Position)
	}
	if want := clock.Add(PreBufferWindow); !resync.PresentAt.Equal(want) {
		t.Errorf("resync present_at = %v, want %v", resync.PresentAt, want)
	}

	// A small drift returns a nudge and NO transition.
	decision, resync, err = r.ReportDrift(g.ID, "profile-default", "s1", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("small drift: %v", err)
	}
	if decision.Action != DriftNudge || resync != nil {
		t.Errorf("small drift = %v / resync=%v, want nudge / nil", decision.Action, resync)
	}
}

func TestReportDriftResyncCannotRacePastLeave(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := NewRegistryWithClock(func() time.Time { return now })
	g := r.Form("profile-default")
	for _, session := range []store.SessionID{"reporter", "peer"} {
		if err := r.Join(g.ID, "profile-default", Member{SessionID: session}); err != nil {
			t.Fatalf("join %s: %v", session, err)
		}
	}
	if _, _, err := r.Transition(g.ID, "profile-default", "reporter", "media-alpha", 0); err != nil {
		t.Fatalf("transition: %v", err)
	}

	checked := make(chan struct{})
	release := make(chan struct{})
	leaveAttempting := make(chan struct{})
	r.reportDriftChecked = func() {
		close(checked)
		<-release
	}
	r.leaveGroupLockAttempting = func() { close(leaveAttempting) }

	type driftResult struct {
		decision DriftDecision
		resync   *Transition
		err      error
	}
	driftDone := make(chan driftResult, 1)
	go func() {
		decision, resync, err := r.ReportDrift(g.ID, "profile-default", "reporter", time.Second)
		driftDone <- driftResult{decision: decision, resync: resync, err: err}
	}()
	<-checked

	leaveDone := make(chan error, 1)
	go func() { leaveDone <- r.Leave(g.ID, "profile-default", "reporter") }()
	<-leaveAttempting
	select {
	case err := <-leaveDone:
		t.Fatalf("Leave completed while ReportDrift held the membership/resync lock: %v", err)
	default:
	}
	close(release)

	result := <-driftDone
	if result.err != nil || result.decision.Action != DriftResync || result.resync == nil {
		t.Fatalf("ReportDrift = (%v, %#v, %v), want successful resync", result.decision, result.resync, result.err)
	}
	if err := <-leaveDone; err != nil {
		t.Fatalf("Leave after resync: %v", err)
	}
	r.reportDriftChecked = nil
	r.leaveGroupLockAttempting = nil
	if _, _, err := r.ReportDrift(g.ID, "profile-default", "reporter", time.Second); err != ErrNotMember {
		t.Fatalf("ReportDrift after Leave error = %v, want ErrNotMember", err)
	}
}

// TestCrossProfileRejected is the AV-4 same-Profile acceptance (d), Registry
// half (ADR-0035 §8, inheriting §7): a group is reachable only by its OWNING
// Profile. A different Profile's lookup/join/leave is rejected fail-closed,
// reported as no-such-group (the surface collapses ErrWrongProfile into the same
// "unknown group" response so it is never a cross-Profile existence oracle).
func TestCrossProfileRejected(t *testing.T) {
	r := NewRegistry()
	g := r.Form("profile-default")

	if _, err := r.Group(g.ID, "profile-managed"); err != ErrWrongProfile {
		t.Errorf("cross-Profile lookup err = %v, want ErrWrongProfile", err)
	}
	if err := r.Join(g.ID, "profile-managed", Member{SessionID: "intruder"}); err != ErrWrongProfile {
		t.Errorf("cross-Profile join err = %v, want ErrWrongProfile", err)
	}
	if err := r.Leave(g.ID, "profile-managed", "intruder"); err != ErrWrongProfile {
		t.Errorf("cross-Profile leave err = %v, want ErrWrongProfile", err)
	}
	// The owning Profile is unaffected.
	if _, err := r.Group(g.ID, "profile-default"); err != nil {
		t.Errorf("owning-Profile lookup err = %v, want nil", err)
	}
}

package conductor

import (
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// lifecycleParty forms a party hosted by host-session in profile-host and joins
// the guests in order, each in its own Profile.
func lifecycleParty(t *testing.T, r *Registry, guests ...store.SessionID) *Group {
	t.Helper()
	g := r.FormParty("profile-host", "host-session", "media-alpha", false)
	join := func(profile store.ProfileID, session store.SessionID) {
		if err := r.Invite(g.ID, mustHost(t, g), profile); err != nil {
			t.Fatalf("Invite(%s): %v", profile, err)
		}
		if err := r.Accept(g.ID, profile, Member{SessionID: session}); err != nil {
			t.Fatalf("Accept(%s): %v", profile, err)
		}
	}
	join("profile-host", "host-session")
	for _, s := range guests {
		join(store.ProfileID("profile-"+string(s)), s)
	}
	return g
}

func mustHost(t *testing.T, g *Group) store.SessionID {
	t.Helper()
	host, ok := g.Host()
	if !ok {
		t.Fatal("not a watch party")
	}
	return host
}

// TestLeavePartyHandsOffToEarliestMember covers CORE-1763: a host leaving a
// party with members left hands the host role to the member who joined first,
// and the new host's transport is authoritative while the old host's is not.
func TestLeavePartyHandsOffToEarliestMember(t *testing.T) {
	r := NewRegistry()
	g := lifecycleParty(t, r, "first", "second")

	leave, err := r.LeaveParty(g.ID, "host-session")
	if err != nil {
		t.Fatalf("LeaveParty(host): %v", err)
	}
	if leave.Dissolved || leave.NewHost != "first" {
		t.Fatalf("LeaveParty(host) = %+v, want hand-off to first", leave)
	}
	if host := mustHost(t, g); host != "first" {
		t.Fatalf("host after hand-off = %q, want first", host)
	}
	if p := g.HostProfile(); p != "profile-first" {
		t.Fatalf("host profile after hand-off = %q, want profile-first", p)
	}
	if g.ProfileID != "profile-host" {
		t.Fatalf("ProfileID changed on hand-off to %q", g.ProfileID)
	}
	if _, _, err := r.HostTransition(g.ID, "first", "media-alpha", time.Second, false); err != nil {
		t.Fatalf("new host transition: %v", err)
	}
	if _, _, err := r.HostTransition(g.ID, "host-session", "media-alpha", 0, false); !errors.Is(err, ErrNotHost) {
		t.Fatalf("old host transition err = %v, want ErrNotHost", err)
	}

	// The next hand-off goes to the next earliest member.
	if leave, err := r.LeaveParty(g.ID, "first"); err != nil || leave.NewHost != "second" {
		t.Fatalf("LeaveParty(first) = %+v, %v; want hand-off to second", leave, err)
	}
	// A non-host leaving never moves the host role.
	g2 := lifecycleParty(t, r, "a", "b")
	if leave, err := r.LeaveParty(g2.ID, "a"); err != nil || leave.NewHost != "" || mustHost(t, g2) != "host-session" {
		t.Fatalf("non-host LeaveParty = %+v, %v; host %q", leave, err, mustHost(t, g2))
	}
}

// TestEndParty covers CORE-1763: only the host can end a party, and ending it
// removes the party and reports every member so each can be told.
func TestEndParty(t *testing.T) {
	r := NewRegistry()
	g := lifecycleParty(t, r, "guest")

	if _, err := r.EndParty(g.ID, "guest"); !errors.Is(err, ErrNotHost) {
		t.Fatalf("member EndParty err = %v, want ErrNotHost", err)
	}
	members, err := r.EndParty(g.ID, "host-session")
	if err != nil {
		t.Fatalf("EndParty: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("EndParty members = %d, want 2", len(members))
	}
	if _, err := r.Party(g.ID); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("ended party err = %v, want ErrNoGroup", err)
	}
	if _, err := r.EndParty(g.ID, "host-session"); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("EndParty twice err = %v, want ErrNoGroup", err)
	}
	ag := r.Form("profile-amb")
	if _, err := r.EndParty(ag.ID, "s1"); !errors.Is(err, ErrNotParty) {
		t.Fatalf("ambient EndParty err = %v, want ErrNotParty", err)
	}
}

// TestExpireDisconnected covers CORE-1763's server-side backstop: a member whose
// last event stream closed and stayed closed past the grace window is removed,
// a reconnect inside the window cancels it, and a session that never held a
// stream is never expired.
func TestExpireDisconnected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	r := NewRegistryWithClock(func() time.Time { return now })
	g := lifecycleParty(t, r, "tab-closed", "reconnects", "no-stream")
	const grace = 20 * time.Second

	for _, s := range []store.SessionID{"host-session", "tab-closed", "reconnects"} {
		r.StreamOpened(s)
	}
	// Two streams on one session: closing one is not a drop.
	r.StreamOpened("host-session")
	if r.StreamClosed("host-session") {
		t.Fatal("closing one of two streams reported a drop")
	}
	if !r.StreamClosed("tab-closed") || !r.StreamClosed("reconnects") {
		t.Fatal("closing the last stream did not report a drop")
	}
	if r.StreamClosed("no-stream") {
		t.Fatal("a session with no stream reported a drop")
	}

	now = now.Add(grace / 2)
	if got := r.ExpireDisconnected(grace); len(got) != 0 {
		t.Fatalf("expired inside the grace window: %+v", got)
	}
	r.StreamOpened("reconnects")

	now = now.Add(grace)
	got := r.ExpireDisconnected(grace)
	if len(got) != 1 || got[0].Session != "tab-closed" || got[0].PartyID != g.ID || got[0].Leave.Dissolved {
		t.Fatalf("ExpireDisconnected = %+v, want only tab-closed", got)
	}
	if n := len(g.Members()); n != 3 {
		t.Fatalf("members after expiry = %d, want 3", n)
	}
	if r.SessionInParty("tab-closed") {
		t.Fatal("expired session still in the party")
	}
	// A swept session is not swept again.
	if got := r.ExpireDisconnected(grace); len(got) != 0 {
		t.Fatalf("second sweep = %+v, want none", got)
	}

	// The host's tab closing hands the party on.
	r.StreamClosed("host-session")
	now = now.Add(grace)
	got = r.ExpireDisconnected(grace)
	if len(got) != 1 || got[0].Session != "host-session" || got[0].Leave.NewHost != "reconnects" {
		t.Fatalf("host expiry = %+v, want hand-off to reconnects", got)
	}
}

// TestPartyRequests covers CORE-1765: a member holds at most one pending
// request, only the host takes one, and a hand-off drops them.
func TestPartyRequests(t *testing.T) {
	r := NewRegistry()
	g := lifecycleParty(t, r, "guest-a", "guest-b")

	if _, err := r.AddRequest(g.ID, PartyRequest{ID: "r0", From: "host-session"}); !errors.Is(err, ErrNotHost) {
		t.Fatalf("host AddRequest err = %v, want ErrNotHost", err)
	}
	if _, err := r.AddRequest(g.ID, PartyRequest{ID: "r0", From: "stranger"}); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member AddRequest err = %v, want ErrNotMember", err)
	}
	host, err := r.AddRequest(g.ID, PartyRequest{ID: "r1", From: "guest-a", Position: time.Second})
	if err != nil || host != "profile-host" {
		t.Fatalf("AddRequest = %q, %v, want profile-host", host, err)
	}
	// A newer request from the same member replaces the older one.
	if _, err := r.AddRequest(g.ID, PartyRequest{ID: "r2", From: "guest-a", Position: 2 * time.Second}); err != nil {
		t.Fatalf("AddRequest r2: %v", err)
	}
	if _, err := r.TakeRequest(g.ID, "host-session", "r1"); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("TakeRequest(replaced) err = %v, want ErrNoRequest", err)
	}
	if _, err := r.TakeRequest(g.ID, "guest-a", "r2"); !errors.Is(err, ErrNotHost) {
		t.Fatalf("member TakeRequest err = %v, want ErrNotHost", err)
	}
	req, err := r.TakeRequest(g.ID, "host-session", "r2")
	if err != nil || req.From != "guest-a" || req.Position != 2*time.Second {
		t.Fatalf("TakeRequest = %#v, %v", req, err)
	}
	if _, err := r.TakeRequest(g.ID, "host-session", "r2"); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("TakeRequest twice err = %v, want ErrNoRequest", err)
	}

	// A leaver's request goes with it, and a hand-off clears the rest.
	if _, err := r.AddRequest(g.ID, PartyRequest{ID: "r3", From: "guest-b"}); err != nil {
		t.Fatalf("AddRequest r3: %v", err)
	}
	if _, err := r.LeaveParty(g.ID, "host-session"); err != nil {
		t.Fatalf("LeaveParty: %v", err)
	}
	if _, err := r.TakeRequest(g.ID, "guest-a", "r3"); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("TakeRequest after hand-off err = %v, want ErrNoRequest", err)
	}
}

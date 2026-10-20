package conductor

import (
	"testing"
	"time"
)

// TestSessionInWatchPartyForMedia covers the PANEL-16 membership oracle:
// playbackInfo degrades a transcode for a real Watch-Party member at
// the cap instead of refusing, and membership is read here authoritatively.
func TestSessionInWatchPartyForMedia(t *testing.T) {
	r := NewRegistry()
	g := r.FormParty("profile-host", "host-session", "media-alpha", false)
	if err := r.Invite(g.ID, "host-session", "profile-guest"); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := r.Accept(g.ID, "profile-guest", Member{SessionID: "guest-session"}); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// A joined member, queried for the party's content, is in the party.
	if !r.SessionInWatchPartyForMedia("guest-session", "media-alpha") {
		t.Fatal("joined member not reported in the party for its content")
	}
	// Wrong content → not in THAT content's party.
	if r.SessionInWatchPartyForMedia("guest-session", "media-other") {
		t.Fatal("member reported for content the party is not watching")
	}
	// Unknown session → not a member.
	if r.SessionInWatchPartyForMedia("stranger", "media-alpha") {
		t.Fatal("non-member reported as a party member")
	}

	// An Ambient Group (non-party) on the same content never counts — only a
	// Watch Party degrades.
	ag := r.Form("profile-amb")
	if err := r.Join(ag.ID, "profile-amb", Member{SessionID: "amb-session"}); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if r.SessionInWatchPartyForMedia("amb-session", "media-alpha") {
		t.Fatal("ambient group member reported as a watch-party member")
	}
}

// TestLeaveParty covers the ADR-0111 §4 revocation propagation at the conductor
// layer: LeaveParty removes any member (cross-Profile, no same-Profile assertion),
// dissolves the party when the last member leaves, and the party's registered
// state is undetectable after dissolution.
func TestLeaveParty(t *testing.T) {
	r := NewRegistry()
	g := r.FormParty("profile-host", "host-session", "media-alpha", false)

	// Add the host as a member (the graph's createWatchParty pre-seeds + accepts
	// the host's own invite; here we replicate that manually for the unit test).
	if err := r.Invite(g.ID, "host-session", "profile-host"); err != nil {
		t.Fatalf("self-invite host: %v", err)
	}
	if err := r.Accept(g.ID, "profile-host", Member{SessionID: "host-session"}); err != nil {
		t.Fatalf("Accept host: %v", err)
	}

	// Invite and accept a guest.
	if err := r.Invite(g.ID, "host-session", "profile-guest"); err != nil {
		t.Fatalf("Invite guest: %v", err)
	}
	if err := r.Accept(g.ID, "profile-guest", Member{SessionID: "guest-session"}); err != nil {
		t.Fatalf("Accept guest: %v", err)
	}
	if n := len(g.Members()); n != 2 {
		t.Fatalf("members before leave = %d, want 2", n)
	}

	// Guest leaves: cross-Profile (different profile than host). Unlike Leave,
	// this does NOT require the caller to own the group's Profile.
	if err := r.LeaveParty(g.ID, "guest-session"); err != nil {
		t.Fatalf("LeaveParty(guest): %v", err)
	}
	if n := len(g.Members()); n != 1 {
		t.Fatalf("members after guest leave = %d, want 1 (host only)", n)
	}
	if r.GroupCount() != 1 {
		t.Fatalf("group count after guest leave = %d, want 1 (party still exists)", r.GroupCount())
	}

	// Leaving as a non-member is ErrNotMember.
	if err := r.LeaveParty(g.ID, "stranger"); err != ErrNotMember {
		t.Errorf("non-member LeaveParty err = %v, want ErrNotMember", err)
	}

	// Host leaves: party dissolves because it was the last member.
	if err := r.LeaveParty(g.ID, "host-session"); err != nil {
		t.Fatalf("LeaveParty(host): %v", err)
	}
	if r.GroupCount() != 0 {
		t.Fatalf("group count after last member leaves = %d, want 0 (party dissolved)", r.GroupCount())
	}

	// The party id is gone from the registry after dissolution.
	if _, err := r.Party(g.ID); err != ErrNoGroup {
		t.Errorf("dissolved party err = %v, want ErrNoGroup", err)
	}
}

// TestLeavePartyRejectsAmbientGroup confirms LeaveParty cannot be aimed at an
// Ambient Group — partyLookup enforces ErrNotParty.
func TestLeavePartyRejectsAmbientGroup(t *testing.T) {
	r := NewRegistry()
	ag := r.Form("profile-p")
	if err := r.Join(ag.ID, "profile-p", Member{SessionID: "s1"}); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := r.LeaveParty(ag.ID, "s1"); err != ErrNotParty {
		t.Errorf("ambient-group LeaveParty err = %v, want ErrNotParty", err)
	}
}

// TestPartyMemberProfileID confirms that Accept records the ProfileID on the
// Member so the graph layer can resolve the display name.
func TestPartyMemberProfileID(t *testing.T) {
	r := NewRegistryWithClock(func() time.Time { return time.Unix(1_700_000_000, 0) })
	g := r.FormParty("profile-host", "host-session", "media-alpha", false)
	// Host is accepted through the FormParty self-invite path; verify ProfileID set.
	members := g.Members()
	if len(members) != 0 {
		// FormParty doesn't auto-accept — the graph does. Here: manual path.
		_ = members
	}
	// Invite and accept a guest; verify the guest's ProfileID is recorded.
	if err := r.Invite(g.ID, "host-session", "profile-guest"); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	m := Member{SessionID: "guest-session", DeviceID: "device-g"}
	if err := r.Accept(g.ID, "profile-guest", m); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	for _, mem := range g.Members() {
		if mem.SessionID == "guest-session" {
			if mem.ProfileID != "profile-guest" {
				t.Errorf("guest ProfileID = %q, want %q (ADR-0111 §1 display-name lookup)", mem.ProfileID, "profile-guest")
			}
			return
		}
	}
	t.Fatal("guest session not found in members")
}

func TestFormPartyReapsAbandonedFormation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := NewRegistryWithClock(func() time.Time { return now })
	abandoned := r.FormParty("profile-host", "host-session", "media-alpha", false)

	now = now.Add(groupFormTTL + time.Second)
	r.FormParty("profile-next", "next-session", "media-beta", false)

	if _, err := r.Party(abandoned.ID); err != ErrNoGroup {
		t.Fatalf("abandoned party lookup err = %v, want ErrNoGroup", err)
	}
}

func TestFormPartyDoesNotReapAcceptedParty(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := NewRegistryWithClock(func() time.Time { return now })
	g := r.FormParty("profile-host", "host-session", "media-alpha", false)
	if err := r.Invite(g.ID, "host-session", "profile-guest"); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := r.Accept(g.ID, "profile-guest", Member{SessionID: "guest-session"}); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	now = now.Add(groupFormTTL + time.Hour)
	r.FormParty("profile-next", "next-session", "media-beta", false)
	if _, err := r.Party(g.ID); err != nil {
		t.Fatalf("accepted party was reaped: %v", err)
	}
}

func TestAcceptWinsQueuedAbandonedPartyReap(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := NewRegistryWithClock(func() time.Time { return now })
	g := r.FormParty("profile-host", "host-session", "media-alpha", false)
	if err := r.Invite(g.ID, "host-session", "profile-guest"); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	now = now.Add(groupFormTTL + time.Second)

	acceptLocked := make(chan struct{})
	r.acceptMapLocked = func() { close(acceptLocked) }
	g.mu.Lock()
	accepted := make(chan error, 1)
	go func() { accepted <- r.Accept(g.ID, "profile-guest", Member{SessionID: "guest-session"}) }()
	<-acceptLocked

	reapAttempting := make(chan struct{})
	r.reapMapLockAttempting = func() { close(reapAttempting) }
	reaped := make(chan struct{})
	go func() {
		r.reapAbandoned(now)
		close(reaped)
	}()
	<-reapAttempting
	g.mu.Unlock()

	if err := <-accepted; err != nil {
		t.Fatalf("Accept: %v", err)
	}
	<-reaped
	if _, err := r.Party(g.ID); err != nil {
		t.Fatalf("accepted party missing after queued reap: %v", err)
	}
	if len(g.Members()) != 1 {
		t.Fatalf("members after accepted race = %d, want 1", len(g.Members()))
	}
}

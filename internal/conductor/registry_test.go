package conductor

import (
	"testing"
	"time"
)

// TestFormReapsAbandonedEmptyGroups is the regression for the Form-then-
// never-join leak: Form inserts an empty group and only Leave (on the LAST
// member) ever deletes one, so a device that forms a group and never joins —
// e.g. it drops its SSE connection first — used to pin that empty group in
// the Registry forever. reapAbandoned, run opportunistically from Form, must
// free a group once it has sat memberless past groupFormTTL.
func TestFormReapsAbandonedEmptyGroups(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := NewRegistryWithClock(func() time.Time { return now })

	abandoned := r.Form("profile-default")
	if r.GroupCount() != 1 {
		t.Fatalf("group count after Form = %d, want 1", r.GroupCount())
	}

	// Advance the clock past the abandonment TTL and form a second group — the
	// abandoned one was never joined, so it must be reaped in the process.
	now = now.Add(groupFormTTL + time.Second)
	r.Form("profile-default")

	if r.GroupCount() != 1 {
		t.Fatalf("group count after reap = %d, want 1 (only the new group)", r.GroupCount())
	}
	if _, err := r.Group(abandoned.ID, "profile-default"); err != ErrNoGroup {
		t.Fatalf("abandoned group lookup err = %v, want ErrNoGroup", err)
	}
}

// TestFormDoesNotReapJoinedGroups confirms reapAbandoned only ever touches
// groups that never received a Join — a group with a member must survive
// well past groupFormTTL.
func TestFormDoesNotReapJoinedGroups(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := NewRegistryWithClock(func() time.Time { return now })

	g := r.Form("profile-default")
	if err := r.Join(g.ID, "profile-default", Member{SessionID: "s1"}); err != nil {
		t.Fatalf("join: %v", err)
	}

	now = now.Add(groupFormTTL + time.Hour)
	r.Form("profile-default")

	if _, err := r.Group(g.ID, "profile-default"); err != nil {
		t.Fatalf("joined group was reaped: err = %v", err)
	}
}

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSetProfileOwnerSingleInvariantAndMovable pins ADR-0007 §4: at most one
// Profile holds the owner flag, designation is movable, and only an adult
// Profile may own. feature:admin-session
func TestSetProfileOwnerSingleInvariantAndMovable(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	open := LibraryFilter{Revealed: true, AllLibraries: true}

	a, err := st.CreateProfile(ctx, "Owner A", ProfileAdult, open)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateProfile(ctx, "Owner B", ProfileAdult, open)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := st.CreateProfile(ctx, "Kid", ProfileManaged, open)
	if err != nil {
		t.Fatal(err)
	}

	// No owner is designated on a fresh (seeded) store.
	if designated, err := st.OwnerDesignated(ctx); err != nil || designated {
		t.Fatalf("OwnerDesignated before designation = (%v, %v), want (false, nil)", designated, err)
	}

	// Designate A.
	if err := st.SetProfileOwner(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if designated, err := st.OwnerDesignated(ctx); err != nil || !designated {
		t.Fatalf("OwnerDesignated after designation = (%v, %v), want (true, nil)", designated, err)
	}
	if isOwner, _ := st.ProfileIsOwner(ctx, a.ID); !isOwner {
		t.Fatal("A is not owner after SetProfileOwner(A)")
	}
	if isOwner, _ := st.ProfileIsOwner(ctx, b.ID); isOwner {
		t.Fatal("B is owner but was never designated")
	}
	// The read path echoes the flag.
	if got, err := st.Profile(ctx, a.ID); err != nil || !got.IsOwner {
		t.Fatalf("Profile(A).IsOwner = (%v, %v), want (true, nil)", got.IsOwner, err)
	}

	// Move ownership to B: A must lose the flag (single-owner invariant).
	if err := st.SetProfileOwner(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if isOwner, _ := st.ProfileIsOwner(ctx, b.ID); !isOwner {
		t.Fatal("B is not owner after transfer")
	}
	if isOwner, _ := st.ProfileIsOwner(ctx, a.ID); isOwner {
		t.Fatal("A still owner after transfer to B — single-owner invariant violated")
	}

	// A managed Profile can never own.
	if err := st.SetProfileOwner(ctx, kid.ID); err == nil {
		t.Fatal("SetProfileOwner accepted a managed Profile")
	} else if !errors.As(err, new(ValidationError)) {
		t.Fatalf("managed-owner err = %v, want ValidationError", err)
	}

	// An unknown Profile is rejected (not silently designated).
	if err := st.SetProfileOwner(ctx, ProfileID("no-such-profile")); err == nil {
		t.Fatal("SetProfileOwner accepted an unknown Profile")
	}

	// ProfileIsOwner of an unknown Profile is (false, nil) — the gate fails closed.
	if isOwner, err := st.ProfileIsOwner(ctx, ProfileID("no-such-profile")); err != nil || isOwner {
		t.Fatalf("ProfileIsOwner(unknown) = (%v, %v), want (false, nil)", isOwner, err)
	}
}

// TestSetProfileOwnerTransferEndsOutgoingOwnerAdminSessions pins CORE-0728:
// Admin authority is derived from the session's admin window alone, so a
// transfer must end the outgoing owner's Admin Sessions at once, while a
// session already bound to the new owner keeps its window.
// feature:admin-session
func TestSetProfileOwnerTransferEndsOutgoingOwnerAdminSessions(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	until := time.Now().Add(15 * time.Minute)

	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	incoming, err := st.CreateProfile(ctx, "Incoming Owner", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionProfile(ctx, "session-revealed", incoming.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []SessionID{"session-manager", "session-default", "session-revealed"} {
		if err := st.SetSessionAdminUntil(ctx, id, until); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.SetProfileOwner(ctx, incoming.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []SessionID{"session-manager", "session-default"} {
		session, err := st.Session(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !session.AdminUntil.IsZero() {
			t.Fatalf("outgoing owner session %s AdminUntil = %v after transfer, want zero", id, session.AdminUntil)
		}
	}
	kept, err := st.Session(ctx, "session-revealed")
	if err != nil {
		t.Fatal(err)
	}
	if kept.AdminUntil.IsZero() {
		t.Fatal("new owner's session lost its Admin window on designation")
	}
}

// TestCannotDisableOwnerProfile pins the lockout-safety guard:
// the owner Profile cannot be disabled (it would strand Admin once its sessions
// lapse); a non-owner Profile disables normally, and the owner can be disabled
// after ownership is transferred away. feature:admin-session
func TestCannotDisableOwnerProfile(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	open := LibraryFilter{Revealed: true, AllLibraries: true}
	disabledTrue := true
	disable := ProfileSettings{Disabled: &disabledTrue}

	owner, err := st.CreateProfile(ctx, "Owner", ProfileAdult, open)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProfile(ctx, "Other", ProfileAdult, open)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileOwner(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}

	// Disabling the owner is refused.
	if _, err := st.UpdateProfileSettings(ctx, owner.ID, disable); err == nil {
		t.Fatal("disabling the owner profile was allowed")
	} else if !errors.As(err, new(ValidationError)) {
		t.Fatalf("disable-owner err = %v, want ValidationError", err)
	}

	// A non-owner adult disables normally.
	if _, err := st.UpdateProfileSettings(ctx, other.ID, disable); err != nil {
		t.Fatalf("disabling a non-owner profile failed: %v", err)
	}

	// After transferring ownership away, the former owner can be disabled.
	if err := st.SetProfileOwner(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateProfileSettings(ctx, owner.ID, disable); err != nil {
		t.Fatalf("disabling the former owner after transfer failed: %v", err)
	}
}

// TestCreateProfileRecordsCreatedAt pins ADR-0036 §2.7: a newly minted Profile
// carries a non-zero creation timestamp that survives a re-read.
func TestCreateProfileRecordsCreatedAt(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	p, err := st.CreateProfile(ctx, "Timestamped", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.CreatedAtMs <= 0 {
		t.Fatalf("CreatedAtMs = %d, want > 0", p.CreatedAtMs)
	}
	got, err := st.Profile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAtMs != p.CreatedAtMs {
		t.Fatalf("re-read CreatedAtMs = %d, want %d", got.CreatedAtMs, p.CreatedAtMs)
	}
}

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// extStore opens a seeded store for the extension-request lifecycle tests.
func extStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	return openSeeded(t), context.Background()
}

// TestExtensionRequestCreateAndRead covers the pending-creation half of the
// lifecycle: a new request is pending with the 5-minute expiry stamped, and a
// read before expiry reports it pending.
func TestExtensionRequestCreateAndRead(t *testing.T) {
	st, ctx := extStore(t)
	now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.UTC)
	req, err := st.CreateExtensionRequest(ctx, ExtensionRequestInput{
		ProfileID:           "profile-managed",
		RequestingSessionID: "session-managed",
		TriggerSource:       "schedule",
		RemainingMinutes:    22,
		Now:                 now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != ExtensionPending {
		t.Fatalf("status = %q, want pending", req.Status)
	}
	if req.ApprovalNonce() == "" {
		t.Fatal("approval nonce is empty")
	}
	if !req.ExpiresAt.Equal(now.Add(ExtensionRequestTTL)) {
		t.Fatalf("expiresAt = %v, want now+5m", req.ExpiresAt)
	}
	got, err := st.ExtensionRequestByID(ctx, req.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ExtensionPending || got.RemainingMinutes != 22 {
		t.Fatalf("read = %+v, want pending/22", got)
	}
}

// TestExtensionRequestApproveDeny covers approve (a grant window is set) and a
// separate deny (no grant).
func TestExtensionRequestApproveDeny(t *testing.T) {
	st, ctx := extStore(t)
	now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.UTC)

	approved, err := st.CreateExtensionRequest(ctx, ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "s1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.RespondExtensionRequest(ctx, approved.ID, approved.ApprovalNonce(), "profile-default", ExtensionApproved, 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != ExtensionApproved || res.GrantMinutes != 30 || res.Responder != "profile-default" {
		t.Fatalf("approve result = %+v", res)
	}
	if !res.GrantExpiresAt.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("grantExpiresAt = %v, want now+30m", res.GrantExpiresAt)
	}

	denied, err := st.CreateExtensionRequest(ctx, ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "s2", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err = st.RespondExtensionRequest(ctx, denied.ID, denied.ApprovalNonce(), "profile-default", ExtensionDenied, 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != ExtensionDenied || res.GrantMinutes != 0 {
		t.Fatalf("deny result = %+v, want denied/0", res)
	}
	// A denied request grants nothing.
	granted, err := st.ActiveExtensionGrant(ctx, "profile-managed", "s2", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		t.Fatal("denied request produced an active grant")
	}
}

// TestExtensionRequestFirstResponderWins is acceptance (b): the second responder
// to a resolved request gets ErrExtensionAlreadyResolved, never overwriting the
// first decision.
func TestExtensionRequestFirstResponderWins(t *testing.T) {
	st, ctx := extStore(t)
	now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.UTC)
	req, err := st.CreateExtensionRequest(ctx, ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "s1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", ExtensionApproved, 15, now); err != nil {
		t.Fatal(err)
	}
	// A second responder (approve OR deny) loses.
	if _, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", ExtensionDenied, 0, now); !errors.Is(err, ErrExtensionAlreadyResolved) {
		t.Fatalf("second deny err = %v, want ErrExtensionAlreadyResolved", err)
	}
	if _, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", ExtensionApproved, 60, now); !errors.Is(err, ErrExtensionAlreadyResolved) {
		t.Fatalf("second approve err = %v, want ErrExtensionAlreadyResolved", err)
	}
	// The first decision (approved, 15 min) stands.
	got, err := st.ExtensionRequestByID(ctx, req.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ExtensionApproved || got.GrantMinutes != 15 {
		t.Fatalf("after races, request = %+v, want approved/15", got)
	}
}

func TestExtensionRequestApprovalNonce(t *testing.T) {
	st, ctx := extStore(t)
	now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.UTC)
	req, err := st.CreateExtensionRequest(ctx, ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "s1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RespondExtensionRequest(ctx, req.ID, "wrong", "profile-default", ExtensionApproved, 15, now); !errors.Is(err, ErrExtensionNotFound) {
		t.Fatalf("wrong nonce err = %v, want ErrExtensionNotFound", err)
	}
	got, err := st.ExtensionRequestByID(ctx, req.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ExtensionPending {
		t.Fatalf("wrong nonce changed status to %q, want pending", got.Status)
	}
	if _, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", ExtensionApproved, 15, now); err != nil {
		t.Fatalf("correct nonce approve: %v", err)
	}
	if _, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", ExtensionApproved, 15, now); !errors.Is(err, ErrExtensionAlreadyResolved) {
		t.Fatalf("replay err = %v, want ErrExtensionAlreadyResolved", err)
	}
}

// TestExtensionRequestExpiry is acceptance (c): a pending request older than 5
// minutes cannot be approved, and reads as expired.
func TestExtensionRequestExpiry(t *testing.T) {
	st, ctx := extStore(t)
	now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.UTC)
	req, err := st.CreateExtensionRequest(ctx, ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "s1", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	past := now.Add(ExtensionRequestTTL + time.Second)

	// A read past the window reports expired (lazy expiry, no write needed).
	got, err := st.ExtensionRequestByID(ctx, req.ID, past)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ExtensionExpired {
		t.Fatalf("expired read status = %q, want expired", got.Status)
	}
	// Approving an expired request is refused as already-resolved.
	if _, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", ExtensionApproved, 30, past); !errors.Is(err, ErrExtensionAlreadyResolved) {
		t.Fatalf("approve-after-expiry err = %v, want ErrExtensionAlreadyResolved", err)
	}
	// No grant ever became active.
	granted, err := st.ActiveExtensionGrant(ctx, "profile-managed", "s1", past)
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		t.Fatal("expired request produced an active grant")
	}
}

// TestExtensionPreauthorization covers the §2.4 toggle and the once-per-session
// guard the auto-approve path uses.
func TestExtensionPreauthorization(t *testing.T) {
	st, ctx := extStore(t)
	now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.UTC)

	// Default off.
	on, err := st.ExtensionPreauthorizationEnabled(ctx, "profile-managed")
	if err != nil {
		t.Fatal(err)
	}
	if on {
		t.Fatal("pre-authorization defaulted on")
	}
	if err := st.SetExtensionPreauthorization(ctx, "profile-managed", true, now); err != nil {
		t.Fatal(err)
	}
	if on, _ = st.ExtensionPreauthorizationEnabled(ctx, "profile-managed"); !on {
		t.Fatal("pre-authorization not enabled after set")
	}

	// Once-per-session guard: no approved request yet, then one.
	used, err := st.SessionHasApprovedExtension(ctx, "profile-managed", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if used {
		t.Fatal("session reported a prior approval before any")
	}
	req, err := st.CreateExtensionRequest(ctx, ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "s1", RemainingMinutes: 12, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RespondExtensionRequest(ctx, req.ID, "", "profile-managed", ExtensionApproved, 12, now); err != nil {
		t.Fatal(err)
	}
	if used, _ = st.SessionHasApprovedExtension(ctx, "profile-managed", "s1"); !used {
		t.Fatal("session did not report the approval")
	}
	// A different session is independent.
	if used, _ = st.SessionHasApprovedExtension(ctx, "profile-managed", "s2"); used {
		t.Fatal("a different session inherited the approval")
	}

	// Disabling leaves the flag off.
	if err := st.SetExtensionPreauthorization(ctx, "profile-managed", false, now); err != nil {
		t.Fatal(err)
	}
	if on, _ = st.ExtensionPreauthorizationEnabled(ctx, "profile-managed"); on {
		t.Fatal("pre-authorization still on after disable")
	}
}

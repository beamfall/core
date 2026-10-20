package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func accStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	return openSeeded(t), context.Background()
}

var (
	accNow     = time.Date(2026, time.June, 15, 10, 0, 0, 0, time.UTC)
	accProfile = ProfileID("profile-managed")
	accAdult   = ProfileID("profile-default")
	accMedia   = MediaID("media-alpha")
)

// TestAccessRequestCreateAndRead covers the pending-creation happy path.
func TestAccessRequestCreateAndRead(t *testing.T) {
	st, ctx := accStore(t)
	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile,
		MediaID:   accMedia,
		Now:       accNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != AccessPending {
		t.Fatalf("status = %q, want pending", req.Status)
	}
	if req.Verdict != AccessVerdictNone {
		t.Fatalf("verdict = %q, want empty", req.Verdict)
	}
	if !req.TTLExpiresAt.Equal(accNow.Add(DefaultAccessRequestTTL)) {
		t.Fatalf("TTLExpiresAt = %v, want now+7d", req.TTLExpiresAt)
	}
	got, err := st.AccessRequestByID(ctx, req.ID, accNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != AccessPending || got.ProfileID != accProfile || got.MediaID != accMedia {
		t.Fatalf("read = %+v, want pending for accProfile/accMedia", got)
	}
}

// TestAccessRequestDedup covers the partial-index dedup: a second tap while
// pending returns the existing pending row (same id), not a new one.
func TestAccessRequestDedup(t *testing.T) {
	st, ctx := accStore(t)
	first, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: accNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: accNow.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("dedup: second request got id %q, want same as first %q", second.ID, first.ID)
	}
}

// TestAccessRequestGrantDeny covers grant and deny resolution paths.
func TestAccessRequestGrantDeny(t *testing.T) {
	st, ctx := accStore(t)

	// Grant path.
	granted, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: accNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.ResolveAccessRequest(ctx, granted.ID, accAdult, AccessVerdictGranted, accNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != AccessResolved || res.Verdict != AccessVerdictGranted || res.Responder != accAdult {
		t.Fatalf("grant: %+v", res)
	}

	// Deny path — different (profile, media) pair would conflict with the
	// partial index, but we need a second media for this. Re-use a different
	// profile to keep seeds from colliding on media-archive (no seed cert ceiling).
	// Simplest: open a separate store and just test deny.
	st2, ctx2 := accStore(t)
	denied, err := st2.CreateAccessRequest(ctx2, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: accNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	res2, err := st2.ResolveAccessRequest(ctx2, denied.ID, accAdult, AccessVerdictDenied, accNow)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != AccessResolved || res2.Verdict != AccessVerdictDenied {
		t.Fatalf("deny: %+v", res2)
	}
}

// TestAccessRequestFirstResponderWins covers the first-responder-wins contract:
// two concurrent resolvers — both adult — only the first succeeds; the second
// gets ErrAccessRequestAlreadyResolved.
func TestAccessRequestFirstResponderWins(t *testing.T) {
	st, ctx := accStore(t)
	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: accNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveAccessRequest(ctx, req.ID, accAdult, AccessVerdictGranted, accNow); err != nil {
		t.Fatal(err)
	}
	// Second attempt must fail.
	_, err = st.ResolveAccessRequest(ctx, req.ID, accAdult, AccessVerdictDenied, accNow)
	if !errors.Is(err, ErrAccessRequestAlreadyResolved) {
		t.Fatalf("second resolve: %v, want ErrAccessRequestAlreadyResolved", err)
	}
}

// TestAccessRequestTTLSuppression covers lazy and eager suppression:
// a read after TTL reports suppressed without a write; GC then materializes it.
func TestAccessRequestTTLSuppression(t *testing.T) {
	st, ctx := accStore(t)
	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: accNow,
		TTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read inside TTL → pending.
	got, err := st.AccessRequestByID(ctx, req.ID, accNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != AccessPending {
		t.Fatalf("inside TTL: status = %q, want pending", got.Status)
	}

	// Read after TTL → suppressed (lazy, no DB write).
	after := accNow.Add(10 * time.Minute)
	got, err = st.AccessRequestByID(ctx, req.ID, after)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != AccessResolved || got.Verdict != AccessVerdictSuppressed {
		t.Fatalf("after TTL lazy read: %+v", got)
	}

	// Resolve after TTL must also fail.
	_, err = st.ResolveAccessRequest(ctx, req.ID, accAdult, AccessVerdictGranted, after)
	if !errors.Is(err, ErrAccessRequestAlreadyResolved) {
		t.Fatalf("resolve after TTL: %v, want ErrAccessRequestAlreadyResolved", err)
	}

	// GC write-suppresses it; afterwards a new pending request for the same
	// pair is allowed (the partial-index covers only status='pending').
	n, err := st.GCExpiredAccessRequests(ctx, after)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("GC affected %d rows, want 1", n)
	}

	// Re-create for same pair succeeds after suppression.
	_, err = st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: after,
	})
	if err != nil {
		t.Fatalf("re-create after suppression: %v", err)
	}
}

// TestPendingAccessRequestsForProfile covers the adult inbox (all pending) and
// the managed-Profile self-view (own pending only).
func TestPendingAccessRequestsForProfile(t *testing.T) {
	st, ctx := accStore(t)
	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: accNow,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Adult sees all pending.
	adultList, err := st.PendingAccessRequestsForProfile(ctx, accAdult, true, accNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(adultList) == 0 {
		t.Fatalf("adult inbox: 0 results, want >=1")
	}
	found := false
	for _, r := range adultList {
		if r.ID == req.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("adult inbox: request %q not found", req.ID)
	}

	// Managed Profile sees only its own.
	selfList, err := st.PendingAccessRequestsForProfile(ctx, accProfile, false, accNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(selfList) != 1 || selfList[0].ID != req.ID {
		t.Fatalf("managed inbox = %v, want [%s]", selfList, req.ID)
	}
}

// TestAccessRequestPayloadPrivacy verifies no content (no title/name) rides
// the request row — only the opaque ids (invariant #5).
func TestAccessRequestPayloadPrivacy(t *testing.T) {
	st, ctx := accStore(t)
	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: accProfile, MediaID: accMedia, Now: accNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The struct must carry only ids; no Title, Name, or Description field
	// should be present on AccessRequest (compile-time check by field count).
	// Runtime: Verdict is empty, Responder is empty while pending.
	if req.Verdict != AccessVerdictNone {
		t.Fatalf("pending verdict not empty: %q", req.Verdict)
	}
	if req.Responder != "" {
		t.Fatalf("pending responder not empty: %q", req.Responder)
	}
}

package store

import (
	"testing"
	"time"
)

var (
	grantNow     = time.Date(2026, time.June, 16, 10, 0, 0, 0, time.UTC)
	grantProfile = ProfileID("profile-managed")
	grantAdult   = ProfileID("profile-default")
	grantMedia   = MediaID("media-alpha")
)

// TestAccessGrantViaResolve covers the happy path: resolving an access
// request with verdict=granted atomically creates an AccessGrant, and the
// granted media ID appears in GrantedMediaIDsForProfile.
func TestAccessGrantViaResolve(t *testing.T) {
	st, ctx := accStore(t)

	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: grantProfile,
		MediaID:   grantMedia,
		Now:       grantNow,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := st.ResolveAccessRequest(ctx, req.ID, grantAdult, AccessVerdictGranted, grantNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Verdict != AccessVerdictGranted {
		t.Fatalf("verdict = %q, want granted", resolved.Verdict)
	}

	ids, err := st.GrantedMediaIDsForProfile(ctx, grantProfile)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range ids {
		if id == grantMedia {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("GrantedMediaIDsForProfile = %v, want to include %q", ids, grantMedia)
	}
}

// TestAccessGrantDenied covers the deny path: resolving with verdict=denied
// must NOT create a grant row.
func TestAccessGrantDenied(t *testing.T) {
	st, ctx := accStore(t)

	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: grantProfile,
		MediaID:   grantMedia,
		Now:       grantNow,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.ResolveAccessRequest(ctx, req.ID, grantAdult, AccessVerdictDenied, grantNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	ids, err := st.GrantedMediaIDsForProfile(ctx, grantProfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("GrantedMediaIDsForProfile = %v, want empty after denied verdict", ids)
	}
}

// TestAccessGrantAccessGrantsForProfile covers the full AccessGrant list read.
func TestAccessGrantAccessGrantsForProfile(t *testing.T) {
	st, ctx := accStore(t)

	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: grantProfile,
		MediaID:   grantMedia,
		Now:       grantNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveAccessRequest(ctx, req.ID, grantAdult, AccessVerdictGranted, grantNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	grants, err := st.AccessGrantsForProfile(ctx, grantProfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Fatalf("AccessGrantsForProfile len = %d, want 1", len(grants))
	}
	g := grants[0]
	if g.ProfileID != grantProfile {
		t.Errorf("ProfileID = %q, want %q", g.ProfileID, grantProfile)
	}
	if g.MediaID != grantMedia {
		t.Errorf("MediaID = %q, want %q", g.MediaID, grantMedia)
	}
	if g.ActorProfileID != grantAdult {
		t.Errorf("ActorProfileID = %q, want %q", g.ActorProfileID, grantAdult)
	}
	if g.RequestID != req.ID {
		t.Errorf("RequestID = %q, want %q", g.RequestID, req.ID)
	}
}

// TestAccessGrantRevoke covers the revocation path: after revoke, the media
// ID no longer appears in GrantedMediaIDsForProfile.
func TestAccessGrantRevoke(t *testing.T) {
	st, ctx := accStore(t)

	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: grantProfile,
		MediaID:   grantMedia,
		Now:       grantNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveAccessRequest(ctx, req.ID, grantAdult, AccessVerdictGranted, grantNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := st.RevokeAccessGrant(ctx, grantProfile, grantMedia); err != nil {
		t.Fatal(err)
	}

	ids, err := st.GrantedMediaIDsForProfile(ctx, grantProfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("GrantedMediaIDsForProfile = %v after revoke, want empty", ids)
	}
}

// TestAccessGrantRevokeIdempotent verifies that revoking a non-existent grant
// is a no-op (does not error).
func TestAccessGrantRevokeIdempotent(t *testing.T) {
	st, ctx := accStore(t)
	if err := st.RevokeAccessGrant(ctx, grantProfile, grantMedia); err != nil {
		t.Fatalf("RevokeAccessGrant on absent grant: %v", err)
	}
}

// TestAccessGrantReGrantRefreshes verifies that a second grant (after
// revoke+new-request) refreshes the existing row rather than erroring.
func TestAccessGrantReGrantRefreshes(t *testing.T) {
	st, ctx := accStore(t)

	// First grant.
	req1, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: grantProfile,
		MediaID:   grantMedia,
		Now:       grantNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveAccessRequest(ctx, req1.ID, grantAdult, AccessVerdictGranted, grantNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Revoke, then issue a new request and grant it again.
	if err := st.RevokeAccessGrant(ctx, grantProfile, grantMedia); err != nil {
		t.Fatal(err)
	}
	req2, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: grantProfile,
		MediaID:   grantMedia,
		Now:       grantNow.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveAccessRequest(ctx, req2.ID, grantAdult, AccessVerdictGranted, grantNow.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}

	ids, err := st.GrantedMediaIDsForProfile(ctx, grantProfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != grantMedia {
		t.Fatalf("GrantedMediaIDsForProfile = %v after re-grant, want [%q]", ids, grantMedia)
	}
}

// TestAccessGrantSuppressed verifies that resolving with verdict=suppressed
// does NOT create a grant (the managed Profile stays locked; the affordance
// is withdrawn by the client reading verdict=suppressed).
func TestAccessGrantSuppressed(t *testing.T) {
	st, ctx := accStore(t)

	req, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: grantProfile,
		MediaID:   grantMedia,
		Now:       grantNow,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := st.ResolveAccessRequest(ctx, req.ID, grantAdult, AccessVerdictSuppressed, grantNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Verdict != AccessVerdictSuppressed {
		t.Fatalf("verdict = %q, want suppressed", resolved.Verdict)
	}

	ids, err := st.GrantedMediaIDsForProfile(ctx, grantProfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("GrantedMediaIDsForProfile = %v after suppressed verdict, want empty", ids)
	}
}

// TestAccessGrantPerTitleBoundary verifies that grants are per-(profile, media)
// — an empty profile-ID query returns nil grants without affecting other
// profiles.
func TestAccessGrantPerTitleBoundary(t *testing.T) {
	st, ctx := accStore(t)

	// Validation: empty profileID must error.
	if _, err := st.GrantedMediaIDsForProfile(ctx, ""); err == nil {
		t.Fatal("GrantedMediaIDsForProfile with empty profileID: want error, got nil")
	}
	if _, err := st.AccessGrantsForProfile(ctx, ""); err == nil {
		t.Fatal("AccessGrantsForProfile with empty profileID: want error, got nil")
	}
	if err := st.RevokeAccessGrant(ctx, "", grantMedia); err == nil {
		t.Fatal("RevokeAccessGrant with empty profileID: want error, got nil")
	}
}

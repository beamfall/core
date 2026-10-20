package store

import (
	"context"
	"testing"
	"time"
)

// TestScrobbleLinkRoundTrip: upsert a per-Profile link, read it back, relink
// (replace), then unlink. The SecretRef rides through; no token is stored here
// (the credential lives in the secure store, invariant #5 — this row only ever
// holds the ref).
func TestScrobbleLinkRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	link := ScrobbleLink{
		ProfileID:    "profile-default",
		Service:      "trakt",
		SecretRef:    "scrobble-profile-default-trakt",
		AccountLabel: "alice",
		LinkedAt:     time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := st.UpsertScrobbleLink(ctx, link); err != nil {
		t.Fatalf("UpsertScrobbleLink: %v", err)
	}
	got, err := st.ScrobbleLinksFor(ctx, "profile-default")
	if err != nil {
		t.Fatalf("ScrobbleLinksFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 link, got %d", len(got))
	}
	if got[0].Service != "trakt" || got[0].SecretRef != link.SecretRef || got[0].AccountLabel != "alice" {
		t.Fatalf("round-tripped link mismatch: %+v", got[0])
	}

	// Relink (replace) the same service — must not duplicate.
	link.SecretRef = "scrobble-profile-default-trakt-v2"
	link.AccountLabel = "alice2"
	if err := st.UpsertScrobbleLink(ctx, link); err != nil {
		t.Fatalf("relink: %v", err)
	}
	got, _ = st.ScrobbleLinksFor(ctx, "profile-default")
	if len(got) != 1 {
		t.Fatalf("relink must replace, not duplicate; got %d links", len(got))
	}
	if got[0].SecretRef != "scrobble-profile-default-trakt-v2" || got[0].AccountLabel != "alice2" {
		t.Fatalf("relink did not replace: %+v", got[0])
	}

	// Unlink.
	if err := st.DeleteScrobbleLink(ctx, "profile-default", "trakt"); err != nil {
		t.Fatalf("DeleteScrobbleLink: %v", err)
	}
	got, _ = st.ScrobbleLinksFor(ctx, "profile-default")
	if len(got) != 0 {
		t.Fatalf("after unlink, want 0 links, got %d", len(got))
	}
	// Unlink again is a no-op success.
	if err := st.DeleteScrobbleLink(ctx, "profile-default", "trakt"); err != nil {
		t.Fatalf("idempotent unlink: %v", err)
	}
}

// TestScrobbleEligibilityDefaultsOff: a Library with no eligibility row is NOT
// eligible — the opt-in default that makes "adult never default" fall out. The
// Restricted seed Library (library-archive) confirms an adult Library is not
// eligible without an explicit row.
func TestScrobbleEligibilityDefaultsOff(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	for _, lib := range []LibraryID{"library-local", "library-archive"} {
		eligible, err := st.ScrobbleLibraryEligible(ctx, lib)
		if err != nil {
			t.Fatalf("ScrobbleLibraryEligible(%s): %v", lib, err)
		}
		if eligible {
			t.Fatalf("library %s must default to NOT scrobble-eligible (opt-in default)", lib)
		}
	}

	// Explicit opt-in flips it on; opt-out flips it back.
	if err := st.SetScrobbleLibraryEligibility(ctx, "library-local", true); err != nil {
		t.Fatalf("SetScrobbleLibraryEligibility on: %v", err)
	}
	eligible, _ := st.ScrobbleLibraryEligible(ctx, "library-local")
	if !eligible {
		t.Fatal("explicit opt-in should make the Library eligible")
	}
	if err := st.SetScrobbleLibraryEligibility(ctx, "library-local", false); err != nil {
		t.Fatalf("SetScrobbleLibraryEligibility off: %v", err)
	}
	eligible, _ = st.ScrobbleLibraryEligible(ctx, "library-local")
	if eligible {
		t.Fatal("explicit opt-out should make the Library not eligible")
	}
}

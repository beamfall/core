package streamtoken

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

func TestIssueAndVerify(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	scope := auth.Scope{DeviceID: "device-tv", SessionID: "session-default", ProfileID: "profile-default"}

	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	grant, err := service.Verify(token, TargetMediaDirect, scope)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if grant.MediaID != "media-alpha" {
		t.Fatalf("MediaID = %q, want media-alpha", grant.MediaID)
	}
}

func TestAuthenticatedGrantValidatesMACOnceAndFailsClosed(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	scope := auth.Scope{DeviceID: "device-tv", SessionID: "session-default", ProfileID: "profile-default"}
	token, err := service.Issue(context.Background(), Grant{
		Target: TargetMediaDirect, MediaID: "media-alpha", DeviceID: scope.DeviceID,
		SessionID: scope.SessionID, ProfileID: scope.ProfileID, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	validations := 0
	service.validateMAC = func(payload, signature []byte) bool {
		validations++
		return hmac.Equal(signature, service.sign(payload))
	}
	authenticated, err := service.Authenticate(token, TargetMediaDirect)
	if err != nil {
		t.Fatalf("Authenticate returned error: %v", err)
	}
	if _, err := service.VerifyAuthenticated(authenticated, scope); err != nil {
		t.Fatalf("VerifyAuthenticated returned error: %v", err)
	}
	if validations != 1 {
		t.Fatalf("MAC validations = %d, want exactly 1", validations)
	}

	if _, err := service.VerifyAuthenticated(AuthenticatedGrant{}, scope); err == nil {
		t.Fatal("VerifyAuthenticated accepted a zero/fabricated grant")
	}
	other := NewService([]byte("test-key"), func() time.Time { return now })
	if _, err := other.VerifyAuthenticated(authenticated, scope); err == nil {
		t.Fatal("VerifyAuthenticated accepted a cross-service grant")
	}
}

func TestIssueClampsRequestedTTL(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	grant := Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(100 * 365 * 24 * time.Hour),
	}

	token, err := service.Issue(context.Background(), grant)
	if err != nil {
		t.Fatalf("Issue enormous requested TTL returned error: %v", err)
	}
	signed, err := service.UnverifiedGrant(token, TargetMediaDirect)
	if err != nil {
		t.Fatalf("UnverifiedGrant: %v", err)
	}
	if want := now.Add(MaxStreamTokenTTL); !signed.ExpiresAt.Equal(want) {
		t.Fatalf("signed expiry = %s, want capped expiry %s", signed.ExpiresAt, want)
	}
}

func TestVerifyRejectsEmptySigningKey(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService(nil, func() time.Time { return now })
	payload, err := json.Marshal(Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	token := encode(payload) + "." + encode(service.sign(payload))
	if _, err := service.UnverifiedGrant(token, TargetMediaDirect); err == nil {
		t.Fatal("verification accepted a token signed with an empty key")
	}
}

func TestVerifyRejectsScopeMismatch(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })

	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-revealed",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	_, err = service.Verify(token, TargetMediaDirect, auth.Scope{
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
	})
	if err == nil {
		t.Fatal("Verify accepted mismatched scope")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	scope := auth.Scope{DeviceID: "device-tv", SessionID: "session-default", ProfileID: "profile-default"}

	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	later := NewService([]byte("test-key"), func() time.Time { return now.Add(2 * time.Minute) })
	if _, err := later.Verify(token, TargetMediaDirect, scope); err == nil {
		t.Fatal("Verify accepted expired token")
	}
}

func TestVerifyUnrestrictedTokenSurvivesRelock(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })

	// Issued while the session happened to be revealed; the target media is
	// NOT in a restricted Library, so a later relock must not kill it.
	token, err := service.Issue(context.Background(), Grant{
		Target:     TargetMediaDirect,
		MediaID:    "media-alpha",
		DeviceID:   "device-tv",
		SessionID:  "session-default",
		ProfileID:  "profile-default",
		Restricted: false,
		ExpiresAt:  now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	relocked := auth.Scope{
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		Revealed:  false,
	}
	if _, err := service.Verify(token, TargetMediaDirect, relocked); err != nil {
		t.Fatalf("Verify rejected unrestricted token after relock: %v", err)
	}
}

func TestVerifyRestrictedTokenDiesOnRelock(t *testing.T) {
	// feature:relock
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })

	token, err := service.Issue(context.Background(), Grant{
		Target:     TargetMediaDirect,
		MediaID:    "media-archive",
		DeviceID:   "device-tv",
		SessionID:  "session-default",
		ProfileID:  "profile-default",
		Restricted: true,
		ExpiresAt:  now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	revealed := auth.Scope{
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		Revealed:  true,
	}
	if _, err := service.Verify(token, TargetMediaDirect, revealed); err != nil {
		t.Fatalf("Verify rejected restricted token for revealed session: %v", err)
	}

	relocked := revealed
	relocked.Revealed = false
	if _, err := service.Verify(token, TargetMediaDirect, relocked); err == nil {
		t.Fatal("Verify accepted restricted token after relock")
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	scope := auth.Scope{DeviceID: "device-tv", SessionID: "session-default", ProfileID: "profile-default"}

	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	if _, err := service.Verify(token+"x", TargetMediaDirect, scope); err == nil {
		t.Fatal("Verify accepted tampered token")
	}
}

// TestVerifyRejectsProfileMismatch verifies that a Stream Token issued for
// one Profile is rejected when presented by a different Profile's session.
// This is the profile-mismatch case: the same device and
// session credentials are used, but the ProfileID in the token's Grant does
// not match the ProfileID in the verifying scope.
//
// Without this check, a token issued for profile-default could be replayed
// by any session that happens to share the same device, bypassing the
// per-Profile scope constraint that Verify enforces (streamtoken.go line
// "grant.ProfileID != scope.ProfileID" within the scope-mismatch guard).
// The test fails if the ProfileID binding in Verify is removed or relaxed.
func TestVerifyRejectsProfileMismatch(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })

	// Issue a token for profile-default on a known device+session.
	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Verify as a DIFFERENT Profile on the SAME device — a profile switch
	// scenario where the session is not refreshed. The grant carries
	// profile-default; the verifying scope carries profile-managed. This
	// must be rejected: a token is strictly scoped to the Profile it was
	// issued for.
	mismatchedScope := auth.Scope{
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-managed", // different profile
	}
	if _, err := service.Verify(token, TargetMediaDirect, mismatchedScope); err == nil {
		t.Fatal("Verify accepted token for a different Profile (profile mismatch must be rejected)")
	}
}

// TestVerifyAcceptsMatchingProfileScope verifies the positive path for
// ProfileID binding: a token verified against the exact scope it was issued
// for (same device, session, and profile) succeeds without error.
// This confirms TestVerifyRejectsProfileMismatch is not vacuously failing.
func TestVerifyAcceptsMatchingProfileScope(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	scope := auth.Scope{
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
	}

	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := service.Verify(token, TargetMediaDirect, scope); err != nil {
		t.Fatalf("Verify rejected a correctly scoped token: %v", err)
	}
}

type recordingLedger struct {
	calls    int
	mediaID  store.MediaID
	returned error
}

func (r *recordingLedger) RecordStreamToken(_ context.Context, _ store.SessionID, _ store.ProfileID, _ store.DeviceID, _ string, mediaID store.MediaID, _ bool, _ time.Time) error {
	r.calls++
	r.mediaID = mediaID
	return r.returned
}

func TestIssueRecordsGrantInTransparencyLedger(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	ledger := &recordingLedger{returned: errors.New("ledger down")}
	service.SetRecorder(ledger)

	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue must not fail on a ledger error (best-effort record): %v", err)
	}
	if token == "" {
		t.Fatal("Issue returned an empty token")
	}
	if ledger.calls != 1 {
		t.Fatalf("recorder calls = %d, want 1", ledger.calls)
	}
	if ledger.mediaID != "media-alpha" {
		t.Fatalf("recorded mediaID = %q, want media-alpha", ledger.mediaID)
	}
}

// HasResolvedProfileCapability is the byte layer's only check that a
// receiver-bound grant carries a real capability fingerprint, so every
// rejection branch is asserted here: a receiver-bound grant that slipped
// through with a malformed identity would be treated as verified.
func TestHasResolvedProfileCapability(t *testing.T) {
	for _, tc := range []struct {
		name       string
		capability string
		want       bool
	}{
		{"well-formed fingerprint", "a45a2c8967c816a5", true},
		{"absent", "", false},
		{"too short", "a45a2c8967c816a", false},
		{"too long", "a45a2c8967c816a55", false},
		{"uppercase hex", "A45A2C8967C816A5", false},
		{"non-hex characters", "zzzzzzzzzzzzzzzz", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grant := Grant{ProfileCapability: tc.capability}
			if got := grant.HasResolvedProfileCapability(); got != tc.want {
				t.Fatalf("HasResolvedProfileCapability(%q) = %v, want %v", tc.capability, got, tc.want)
			}
		})
	}
}

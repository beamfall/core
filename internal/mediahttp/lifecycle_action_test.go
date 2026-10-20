package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// The Stream-Token + direct byte-route LIFECYCLE as one wire action: a real
// token is minted, then the live session relocks mid-stream. The relock rule
// (streamtoken.Verify: a Restricted grant requires the verifying session to
// currently hold reveal; an unrestricted grant survives) is asserted over the
// real /api/media/direct route — not via Verify() unit calls — by fetching
// bytes before and after SetSessionRevealed flips the one server-side switch
// that every byte request re-derives its scope from (auth.go scope()).
//
// Confirmed by reading mediahttp.go: a Verify failure on a byte route yields
// http.StatusUnauthorized (401). There is no 423/StatusLocked anywhere in the
// byte routes (grep of internal/mediahttp + internal/server) — the relock
// refusal IS the generic 401 token-rejection, layered above the reveal-filtered
// PrimaryMediaFile lookup which would itself 404 a now-hidden restricted item.

// streamTokenLifecycleHandler builds a real mediahttp.Handler over a seeded
// store with a fixed token clock, mirroring newTestHandler/newTestHandlerWithBytes
// in mediahttp_test.go. It returns the handler, its auth service (to flip reveal
// live), the token service, and the fixed `now` for minting further tokens.
func streamTokenLifecycleHandler(t *testing.T) (*Handler, *auth.Service, *streamtoken.Service, time.Time) {
	t.Helper()
	st := storetest.Open(t)
	authService := auth.NewService(st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	return handler, authService, tokens, now
}

// streamTokenLifecycleSetBytes writes body to a temp file and registers it as
// mediaID's primary file, mirroring newTestHandlerWithBytes.
func streamTokenLifecycleSetBytes(t *testing.T, handler *Handler, mediaID store.MediaID, body []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), string(mediaID)+".bin")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := handler.store.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
		t.Fatal(err)
	}
}

// streamTokenLifecycleDirectToken mints a real direct-play token for sessionID
// over mediaID, with the given Restricted property (a property of the TARGET
// media at issue time, per streamtoken.Grant). It resolves the live scope for
// the device/profile fields exactly like the playback-info issue path does.
func streamTokenLifecycleDirectToken(t *testing.T, handler *Handler, tokens *streamtoken.Service, now time.Time, sessionID store.SessionID, mediaID store.MediaID, restricted bool) string {
	t.Helper()
	scope, err := handler.auth.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:     streamtoken.TargetMediaDirect,
		MediaID:    mediaID,
		DeviceID:   scope.DeviceID,
		SessionID:  scope.SessionID,
		ProfileID:  scope.ProfileID,
		Restricted: restricted,
		ExpiresAt:  now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// streamTokenLifecycleFetch runs one real Direct byte fetch for token.
func streamTokenLifecycleFetch(handler *Handler, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
	return rec
}

// TestStreamTokenLifecycleUnrestrictedServesOverByteRoute is the baseline leg
// (1): a direct token for an unrestricted Media held by a session that never
// revealed serves bytes (2xx) over the real byte route — reveal is irrelevant
// to unrestricted bytes.
func TestStreamTokenLifecycleUnrestrictedServesOverByteRoute(t *testing.T) {
	handler, _, tokens, now := streamTokenLifecycleHandler(t)
	streamTokenLifecycleSetBytes(t, handler, "media-alpha", []byte("unrestricted bytes"))

	// session-default is an adult Profile that is NOT revealed; media-alpha is in
	// the unrestricted library-local. The token's Restricted property is false.
	token := streamTokenLifecycleDirectToken(t, handler, tokens, now, "session-default", "media-alpha", false)

	rec := streamTokenLifecycleFetch(handler, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "unrestricted bytes" {
		t.Fatalf("body = %q, want unrestricted bytes", rec.Body.String())
	}
}

// TestStreamTokenLifecycleRestrictedDiesOnRelockWhileUnrestrictedSurvives is the
// core relock-mid-stream action (2). A revealed session mints a working
// restricted token that serves bytes; an unrestricted in-flight token is also
// minted. After the session relocks (SetSessionRevealed false — the one
// server-side switch every byte request re-derives scope from), a fresh
// restricted-bytes request over the real route is refused (401, confirmed from
// mediahttp.go), while the previously-issued unrestricted token still serves
// 2xx bytes.
func TestStreamTokenLifecycleRestrictedDiesOnRelockWhileUnrestrictedSurvives(t *testing.T) {
	// feature:relock
	handler, authService, tokens, now := streamTokenLifecycleHandler(t)
	streamTokenLifecycleSetBytes(t, handler, "media-archive", []byte("restricted bytes"))
	streamTokenLifecycleSetBytes(t, handler, "media-alpha", []byte("unrestricted in-flight bytes"))

	// session-revealed (adult Profile, Revealed at seed) currently holds reveal,
	// so a restricted token for media-archive (in the restricted library-archive)
	// verifies and the reveal-filtered byte lookup succeeds.
	restrictedToken := streamTokenLifecycleDirectToken(t, handler, tokens, now, "session-revealed", "media-archive", true)
	// An unrestricted token issued earlier, in flight, for the same session.
	unrestrictedToken := streamTokenLifecycleDirectToken(t, handler, tokens, now, "session-revealed", "media-alpha", false)

	// Before relock: the restricted token serves the restricted bytes.
	pre := streamTokenLifecycleFetch(handler, restrictedToken)
	if pre.Code != http.StatusOK {
		t.Fatalf("pre-relock restricted status = %d, want 200: %s", pre.Code, pre.Body.String())
	}
	if pre.Body.String() != "restricted bytes" {
		t.Fatalf("pre-relock body = %q, want restricted bytes", pre.Body.String())
	}

	// Relock mid-stream: flip the session's reveal off. Every subsequent byte
	// request resolves a non-revealed scope (auth.go scope(): revealed =
	// session.Revealed && adult Profile).
	if err := handler.store.SetSessionRevealed(context.Background(), "session-revealed", false); err != nil {
		t.Fatalf("SetSessionRevealed returned error: %v", err)
	}
	// Confirm the live scope actually relocked, so the assertions below test the
	// relock rule and not a stale read.
	relockedScope, err := authService.Scope(context.Background(), "session-revealed")
	if err != nil {
		t.Fatalf("Scope after relock returned error: %v", err)
	}
	if relockedScope.Revealed {
		t.Fatal("scope still revealed after SetSessionRevealed(false)")
	}

	// After relock: a fresh restricted-bytes request is refused. The Verify
	// relock rule fails first (401), upstream of the now-hidden PrimaryMediaFile
	// lookup that would itself 404 the restricted item.
	post := streamTokenLifecycleFetch(handler, restrictedToken)
	if post.Code != http.StatusUnauthorized {
		t.Fatalf("post-relock restricted status = %d, want 401: %s", post.Code, post.Body.String())
	}
	// Leak check: the refused response must not carry the restricted bytes.
	if post.Body.String() == "restricted bytes" {
		t.Fatal("post-relock restricted response leaked restricted bytes")
	}

	// The previously-issued unrestricted in-flight token still serves 2xx bytes
	// across the same relock — an unrestricted grant survives.
	survivor := streamTokenLifecycleFetch(handler, unrestrictedToken)
	if survivor.Code != http.StatusOK {
		t.Fatalf("post-relock unrestricted status = %d, want 200: %s", survivor.Code, survivor.Body.String())
	}
	if survivor.Body.String() != "unrestricted in-flight bytes" {
		t.Fatalf("post-relock unrestricted body = %q, want in-flight bytes", survivor.Body.String())
	}
}

// TestStreamTokenLifecycleRejectsTamperedAndScopeMismatch covers the negative
// legs (3): a tampered token and a scope-mismatch token are both rejected (401)
// over the real byte route, with no bytes leaked.
func TestStreamTokenLifecycleRejectsTamperedAndScopeMismatch(t *testing.T) {
	handler, _, tokens, now := streamTokenLifecycleHandler(t)
	streamTokenLifecycleSetBytes(t, handler, "media-alpha", []byte("guarded bytes"))

	good := streamTokenLifecycleDirectToken(t, handler, tokens, now, "session-default", "media-alpha", false)

	// Tampered signature: a trailing byte invalidates the HMAC.
	tampered := streamTokenLifecycleFetch(handler, good+"x")
	if tampered.Code != http.StatusUnauthorized {
		t.Fatalf("tampered status = %d, want 401", tampered.Code)
	}
	if tampered.Body.String() == "guarded bytes" {
		t.Fatal("tampered response leaked bytes")
	}

	// Scope mismatch: a validly-signed token whose grant names a different
	// session than the one the live scope resolves. The token is signed for
	// session-manager (a real seeded session), but it is served exactly as
	// minted; Verify resolves the grant's own session scope, so the mismatch we
	// force is the device — the grant claims a session that does not own the
	// device the byte route resolves. Mint a grant whose SessionID is real but
	// whose DeviceID is wrong, so Verify's scope-equality check fails.
	mismatch, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-wrong",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatchRec := streamTokenLifecycleFetch(handler, mismatch)
	if mismatchRec.Code != http.StatusUnauthorized {
		t.Fatalf("scope-mismatch status = %d, want 401: %s", mismatchRec.Code, mismatchRec.Body.String())
	}
	if mismatchRec.Body.String() == "guarded bytes" {
		t.Fatal("scope-mismatch response leaked bytes")
	}
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/reveal"
	"github.com/beamfall/core/internal/store"
)

func (s *Server) mountWebPrivateHandlers() {
	s.handle(http.MethodPost, "/api/web/enroll", reveal.ContentJSON, s.enrollWebDevice)
	s.handle(http.MethodPost, "/api/web/logout", reveal.ContentJSON, s.logoutWebDevice)
	s.handle(http.MethodPost, "/api/web/promote", reveal.ContentJSON, s.promoteWebDevice)
	// First-run setup bootstrap (ADR-0007 §2 / feature:first-run-setup-token):
	// redeems the one-time setup token printed to the server log, creating the
	// first MANAGER device+session without requiring an existing manager. Only
	// valid while setup is incomplete; the token is consumed on first use.
	s.handle(http.MethodPost, "/api/setup", reveal.ContentJSON, s.setupFirstRun)
	// Unauthenticated first-run probe: a brand-new, unenrolled
	// browser cannot reach the session-gated setupState query, so this bare GET
	// reports only whether the one-time setup token is still installed. Like
	// /health it carries no Reveal surface (empty class) and resolves no session,
	// so it is genuinely reachable pre-enrollment; it leaks only the single
	// boolean "is this a fresh server" and never the token value.
	s.handle(http.MethodGet, "/api/setup", "", s.setupRequired)
}

func rejectPublicPlainHTTPWebCredentials(w http.ResponseWriter, r *http.Request) bool {
	if requestIsHTTPS(r) || requestIsLAN(r) {
		return false
	}
	writeError(w, r, http.StatusForbidden, "secure connection or LAN required")
	return true
}

// enrollWebDevice is the browser variant of enrollDevice: the
// session and device credential are set as Secure/HttpOnly/SameSite=Strict
// cookies, so the device secret never enters JS-readable space — the response
// body deliberately omits it. The CSRF header is required even pre-auth so a
// cross-site page cannot burn pairing codes or attach a session to a victim.
func (s *Server) enrollWebDevice(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	var req pairing.EnrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	// The browser enroll path must report its LAN-ness like the native enroll
	// handler does, so the remote-enrollment gate evaluates correctly
	// — without this, a LAN browser would read as off-LAN (IsLAN zero-value).
	req.IsLAN = requestIsLAN(r)
	resp, err := s.pairing.Enroll(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "pairing attempts throttled")
		case errors.Is(err, pairing.ErrInvalidCode):
			writeError(w, r, http.StatusUnauthorized, "invalid pairing code")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid enrollment request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "enrollment failed", err)
		}
		return
	}
	expires, parseErr := time.Parse(time.RFC3339, resp.SessionExpiresAt)
	if parseErr != nil {
		expires = time.Time{} // session cookie: expiry still enforced server-side
	}
	setWebCredentialCookie(w, r, auth.SessionCookie, resp.SessionID, expires)
	setWebCredentialCookie(w, r, auth.DeviceCookie, resp.DeviceID, expires)
	setWebCredentialCookie(w, r, auth.DeviceSecretCookie, resp.DeviceSecret, expires)
	writeJSON(w, http.StatusCreated, map[string]string{
		"deviceID":         resp.DeviceID,
		"sessionExpiresAt": resp.SessionExpiresAt,
		"profileID":        resp.ProfileID,
		"deviceRole":       resp.DeviceRole,
	})
}

// setupFirstRun redeems the one-time setup token (ADR-0007 §2 /
// feature:first-run-setup-token). It bypasses the normal pairing-code flow to
// create the first MANAGER device+session, resolving the circular dependency on
// fresh servers. CSRF header required; token is single-use and cleared on
// success.
func (s *Server) setupFirstRun(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	var req pairing.SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	resp, err := s.pairing.Bootstrap(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "setup attempts throttled")
		case errors.Is(err, pairing.ErrSetupTokenInvalid):
			writeError(w, r, http.StatusUnauthorized, "setup token invalid or already used")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid setup request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "setup failed", err)
		}
		return
	}
	expires, parseErr := time.Parse(time.RFC3339, resp.SessionExpiresAt)
	if parseErr != nil {
		expires = time.Time{}
	}
	setWebCredentialCookie(w, r, auth.SessionCookie, resp.SessionID, expires)
	setWebCredentialCookie(w, r, auth.DeviceCookie, resp.DeviceID, expires)
	setWebCredentialCookie(w, r, auth.DeviceSecretCookie, resp.DeviceSecret, expires)
	writeJSON(w, http.StatusCreated, map[string]string{
		"deviceID":         resp.DeviceID,
		"profileID":        resp.ProfileID,
		"deviceRole":       resp.DeviceRole,
		"sessionExpiresAt": resp.SessionExpiresAt,
	})
}

// setupRequired is the unauthenticated first-run probe: it returns
// {"required": true} while the one-time setup token is still installed and
// {"required": false} once it has been consumed by POST /api/setup. No session,
// no CSRF header — a fresh, unenrolled browser must be able to call it to decide
// whether to route to the setup wizard. It leaks only this single boolean.
func (s *Server) setupRequired(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"required": s.pairing.SetupRequired()})
}

// promoteWebDevice upgrades the calling browser's device to MANAGER
// by redeeming a manager-role pairing code. Promotion is a
// privilege change, so the session ID and device secret rotate on success.
func (s *Server) promoteWebDevice(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	var req pairing.PromoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	resp, err := s.pairing.Promote(r.Context(), scope, req)
	if err != nil {
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "pairing attempts throttled")
		case errors.Is(err, pairing.ErrInvalidCode):
			writeError(w, r, http.StatusUnauthorized, "invalid pairing code")
		case errors.Is(err, pairing.ErrRoleNotGranted):
			writeError(w, r, http.StatusForbidden, "pairing code does not grant the manager role")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid promotion request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "promotion failed", err)
		}
		return
	}
	expires, parseErr := time.Parse(time.RFC3339, resp.SessionExpiresAt)
	if parseErr != nil {
		expires = time.Time{}
	}
	setWebCredentialCookie(w, r, auth.SessionCookie, resp.SessionID, expires)
	setWebCredentialCookie(w, r, auth.DeviceCookie, resp.DeviceID, expires)
	setWebCredentialCookie(w, r, auth.DeviceSecretCookie, resp.DeviceSecret, expires)
	writeJSON(w, http.StatusOK, resp)
}

// sessionRevoker is the logout revocation seam: deleting the session's auth
// row (so its next request fails auth) and clearing its outstanding
// Stream-Token ledger rows. The composition roots hand the server its
// JobStatsProvider as the backing *store.Store, which implements this via
// Store.TerminateSession; a wiring without the capability fails logout closed
// rather than reporting a revocation that never happened.
type sessionRevoker interface {
	TerminateSession(ctx context.Context, id store.SessionID) error
}

// logoutWebDevice ends the calling session server-side, then clears the
// browser's credential cookies. Cookie expiry alone is not revocation — a
// captured credential would stay valid for the session's remaining server-side
// lifetime — so logout authenticates the caller, revokes exactly the calling
// session (auth row plus its outstanding Stream-Token ledger activity), and
// only reports success once the revocation held. Other devices' sessions are
// untouched.
func (s *Server) logoutWebDevice(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	if scope.SessionID == "" {
		writeError(w, r, http.StatusUnauthorized, "session credential required")
		return
	}
	revoker, ok := s.jobStats.(sessionRevoker)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "session revocation unavailable")
		return
	}
	if err := revoker.TerminateSession(r.Context(), scope.SessionID); err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "logout failed", err)
		return
	}
	expired := time.Unix(0, 0).UTC()
	for _, name := range []string{auth.SessionCookie, auth.DeviceCookie, auth.DeviceSecretCookie} {
		setWebCredentialCookie(w, r, name, "", expired)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged-out"})
}

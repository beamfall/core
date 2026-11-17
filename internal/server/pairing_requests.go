package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/pairing"
)

// Device-initiated pairing (CORE-0316). A device with no credential asks for
// a short code, shows it, and polls with the request secret it alone holds;
// an administrator approves or denies the code from the pending list. The
// device routes are pre-subject like /api/enroll; the list, approve and deny
// routes need an administrator session.
//
// A request the web app makes (CORE-2070) carries the CSRF header every web
// request sends, and is a browser request from then on: the service keeps that
// with the request, so a later call cannot shed it by leaving the header off.
// A browser request is bound like /api/web/enroll. Every device call for it is
// refused over public plain HTTP before the request is touched, and on
// approval the credential goes into the HttpOnly cookies, never into
// page-readable JSON.

// pollDevicePairingRequest is the poll body: the request secret plus the
// optional client contract that /api/enroll also accepts.
type pollDevicePairingRequest struct {
	pairing.DevicePollRequest
	ClientContract *capabilities.ClientContract `json:"clientContract,omitempty"`
}

func (s *Server) requestDevicePairing(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	var req pairing.DeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.Browser = sentByWebApp(r)
	if req.Browser && rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	req.RemoteAddr = r.RemoteAddr
	req.IsLAN = requestIsLAN(r)
	resp, err := s.pairing.RequestDevicePairing(r.Context(), req)
	if err != nil {
		writeDevicePairingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) regenerateDevicePairing(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	var req pairing.DevicePollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if s.browserDevicePairing(r, req.RequestSecret) && rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	req.RemoteAddr = r.RemoteAddr
	req.IsLAN = requestIsLAN(r)
	resp, err := s.pairing.RegenerateDevicePairing(r.Context(), req)
	if err != nil {
		writeDevicePairingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// cancelDevicePairing withdraws a request its device no longer wants. It
// answers 204 whether or not the secret named a live request, so it is not an
// oracle for one.
func (s *Server) cancelDevicePairing(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	var req pairing.DevicePollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	req.IsLAN = requestIsLAN(r)
	if err := s.pairing.CancelDevicePairing(r.Context(), req); err != nil {
		writeDevicePairingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sentByWebApp reports whether a browser sent the call: it carries the CSRF
// header every web request sends, or an Origin or Sec-Fetch-Site header, which
// a browser adds itself and page script cannot remove. Without the last two,
// page script could leave the CSRF header off and collect the credential as
// JSON.
func sentByWebApp(r *http.Request) bool {
	return r.Header.Get(auth.CSRFHeader) != "" || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != ""
}

// browserDevicePairing reports whether a device call is a browser's: the
// request was made by the web app, or this call itself carries the header.
func (s *Server) browserDevicePairing(r *http.Request, requestSecret string) bool {
	return sentByWebApp(r) || s.pairing.DeviceRequestBrowserBound(requestSecret)
}

func (s *Server) pollDevicePairing(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	var body pollDevicePairingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	// Refused before the request is touched, so the approval is not spent.
	browser := s.browserDevicePairing(r, body.RequestSecret)
	if browser && rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	req := body.DevicePollRequest
	req.RemoteAddr = r.RemoteAddr
	req.IsLAN = requestIsLAN(r)
	resp, err := s.pairing.PollDevicePairing(r.Context(), req)
	if err != nil {
		writeDevicePairingError(w, r, err)
		return
	}
	if resp.Enrollment != nil && body.ClientContract != nil {
		// As on /api/enroll: the device exists and the request is spent, so
		// a failed build mapping must not withhold the device secret.
		if err := s.bindEnrolledBuild(r.Context(), *resp.Enrollment, *body.ClientContract); err != nil {
			slog.WarnContext(r.Context(), "enrollment build mapping failed", "device", resp.Enrollment.DeviceID, "surface", body.ClientContract.Surface, "build", body.ClientContract.BuildID, "err", err)
		}
	}
	// resp.Browser is the binding the service read under the same lock that
	// collected the approval.
	if (browser || resp.Browser) && resp.Enrollment != nil {
		enrolled := *resp.Enrollment
		setWebSignInCookies(w, r, enrolled.SessionID, enrolled.SessionExpiresAt, enrolled.DeviceID, enrolled.DeviceSecret)
		// Same fields, credential values blanked: the cookies carry them.
		enrolled.DeviceSecret = ""
		enrolled.SessionID = ""
		resp.Enrollment = &enrolled
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeDevicePairingError maps a refused device-side pairing call the way
// /api/enroll maps a refused code.
func writeDevicePairingError(w http.ResponseWriter, r *http.Request, err error) {
	notePairingRefusal(r, refusalOperationEnroll, err)
	switch {
	case errors.Is(err, pairing.ErrThrottled):
		writeError(w, r, http.StatusTooManyRequests, "pairing attempts throttled")
	case errors.Is(err, pairing.ErrInvalidCode):
		writeError(w, r, http.StatusUnauthorized, "invalid pairing code")
	case errors.Is(err, pairing.ErrInvalidRequest):
		writeError(w, r, http.StatusBadRequest, "invalid pairing request")
	default:
		writeErrorCause(w, r, http.StatusInternalServerError, "pairing failed", err)
	}
}

func (s *Server) listDevicePairingRequests(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.devicePairingAdminScope(w, r)
	if !ok {
		return
	}
	resp, err := s.pairing.ListDeviceRequests(r.Context(), scope)
	if err != nil {
		writeDeviceDecisionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) approveDevicePairingRequest(w http.ResponseWriter, r *http.Request) {
	s.decideDevicePairingRequest(w, r, s.pairing.ApproveDeviceRequest)
}

func (s *Server) denyDevicePairingRequest(w http.ResponseWriter, r *http.Request) {
	s.decideDevicePairingRequest(w, r, s.pairing.DenyDeviceRequest)
}

type deviceRequestDecider func(ctx context.Context, scope auth.Scope, req pairing.DeviceRequestDecision) (pairing.DeviceRequestDecisionResponse, error)

func (s *Server) decideDevicePairingRequest(w http.ResponseWriter, r *http.Request, decide deviceRequestDecider) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	scope, ok := s.devicePairingAdminScope(w, r)
	if !ok {
		return
	}
	var req pairing.DeviceRequestDecision
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	resp, err := decide(r.Context(), scope, req)
	if err != nil {
		writeDeviceDecisionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) devicePairingAdminScope(w http.ResponseWriter, r *http.Request) (auth.Scope, bool) {
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return auth.Scope{}, false
	}
	return scope, true
}

// writeDeviceDecisionError maps an administrator's refused list, approve or
// deny. An unknown, expired or already-decided code is 404 rather than 401 so
// the Console does not read it as a lost session.
func writeDeviceDecisionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pairing.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "pairing forbidden")
	case errors.Is(err, pairing.ErrInvalidCode):
		writeError(w, r, http.StatusNotFound, "invalid pairing code")
	case errors.Is(err, pairing.ErrInvalidRequest):
		writeError(w, r, http.StatusBadRequest, "invalid pairing request")
	default:
		writeErrorCause(w, r, http.StatusInternalServerError, "pairing failed", err)
	}
}

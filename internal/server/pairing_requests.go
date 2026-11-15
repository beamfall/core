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
	req.RemoteAddr = r.RemoteAddr
	req.IsLAN = requestIsLAN(r)
	resp, err := s.pairing.RegenerateDevicePairing(r.Context(), req)
	if err != nil {
		writeDevicePairingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) pollDevicePairing(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	var body pollDevicePairingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, r, err)
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

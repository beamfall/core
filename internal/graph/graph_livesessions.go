package graph

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Live sessions dashboard (ADR-0007 §4 Operations), graph half. A manager-gated
// Console read listing currently-active playback sessions — which Profile/Device
// is playing which Media, the resume position, and direct-play vs transcode
// state — plus a mutation to terminate a session ("kill session"). The read
// resolves entirely over the EXISTING Stream Token ledger (no new storage); see
// store.ActiveSessions. Both fields are gated on store.DeviceRoleManager in the
// execute() dispatch, like the other Operations-area fields.

// LiveSessionInfo is one currently-playing session on the wire.
// Title and libraryID are empty when the Media is outside the operator's
// current visibility (a restricted Library not revealed in this session) — the
// session is still listed so active load is never understated, mirroring the
// stream-token ledger's hidden==shape rule.
type LiveSessionInfo struct {
	SessionID       string `json:"sessionID"`
	DeviceID        string `json:"deviceID"`
	DeviceName      string `json:"deviceName"`
	DeviceRole      string `json:"deviceRole"`
	ProfileID       string `json:"profileID"`
	ProfileName     string `json:"profileName"`
	MediaID         string `json:"mediaID"`
	Title           string `json:"title,omitempty"`
	LibraryID       string `json:"libraryID,omitempty"`
	Transcoding     bool   `json:"transcoding"`
	PositionSeconds int    `json:"positionSeconds"`
	DurationSeconds int    `json:"durationSeconds"`
	ExpiresAt       string `json:"expiresAt"`
}

// liveSessions returns every currently-active playback session for the operator
// Console. Reveal-filtered by the operator's OWN scope, exactly
// like the other Console reads — a restricted-Library session the operator
// hasn't revealed appears with its media fields suppressed.
func (h *Handler) liveSessions(ctx context.Context, scope auth.Scope) ([]LiveSessionInfo, error) {
	sessions, err := h.store.ActiveSessions(ctx, time.Now().UTC(), scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]LiveSessionInfo, len(sessions))
	for i, s := range sessions {
		out[i] = LiveSessionInfo{
			SessionID:       string(s.SessionID),
			DeviceID:        string(s.DeviceID),
			DeviceName:      s.DeviceName,
			DeviceRole:      string(s.DeviceRole),
			ProfileID:       string(s.ProfileID),
			ProfileName:     s.ProfileName,
			MediaID:         string(s.MediaID),
			Title:           s.Title,
			LibraryID:       string(s.LibraryID),
			Transcoding:     s.Transcoding,
			PositionSeconds: s.PositionSeconds,
			DurationSeconds: s.DurationSeconds,
			ExpiresAt:       s.ExpiresAt.UTC().Format(time.RFC3339),
		}
	}
	return out, nil
}

// TerminateSessionResult echoes the terminated session id (ADR-0007 §4 "kill
// session"). The mutation succeeds idempotently from the operator's view: a
// success means the session is no longer active.
type TerminateSessionResult struct {
	SessionID string `json:"sessionID"`
}

// terminateSession ends one active Session ("kill session", ADR-0007 §4): it
// deletes the auth_session row so the session's next request fails auth, and
// clears its Stream Token ledger rows so the dashboard reflects the kill at
// once. Manager-gated by the caller. An unknown id is a client error, so the
// operator learns the session was already gone rather than seeing a false
// success.
func (h *Handler) terminateSession(ctx context.Context, scope auth.Scope, req request) (TerminateSessionResult, error) {
	id := trimmedVariable(req, "sessionID")
	if id == "" {
		return TerminateSessionResult{}, clientError("sessionID is required")
	}
	if err := h.store.TerminateSession(ctx, store.SessionID(id)); err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return TerminateSessionResult{}, clientError(string(verr))
		}
		return TerminateSessionResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivitySessionTerminate, "session", id, ""); err != nil {
		return TerminateSessionResult{}, err
	}
	return TerminateSessionResult{SessionID: id}, nil
}

// RevokeDeviceResult echoes the revoked device id and reports how many in-flight
// transcodes were aborted as part of propagation, so the operator
// Console can report that the kill reached live transports, not just future auth.
type RevokeDeviceResult struct {
	DeviceID          string `json:"deviceID"`
	TranscodesAborted int    `json:"transcodesAborted"`
}

// revokeDevice revokes a device key and propagates the kill to that device's
// LIVE transports within one event cycle. Revocation is the
// audited server mutation: it flips the Device's Revoked flag (store.RevokeDevice),
// which immediately fails the device's next per-event scope re-evaluation —
// auth.scope returns "device revoked" — so every long-lived poll-based transport
// (the SSE event stream, tombstone feed, deviceBus presence) stops delivering on
// its next poll (ADR-0012 §4: "every long-lived transport re-evaluates the
// Session per event"). Future requests are blocked the same way. What that flag
// alone does NOT do is abort the device's in-flight transcodes — an FFmpeg
// process keeps running until idle reap — so this also aborts every transcode
// owned by any of the revoked device's active Sessions, through the playback
// termination seam. Other devices' sessions are untouched: only sessions whose
// DeviceID matches the revoked device are terminated. Admin-Session-gated by the
// caller, like terminateSession. An unknown id is a client error.
func (h *Handler) revokeDevice(ctx context.Context, scope auth.Scope, req request) (RevokeDeviceResult, error) {
	id := trimmedVariable(req, "deviceID")
	if id == "" {
		return RevokeDeviceResult{}, clientError("deviceID is required")
	}
	// Confirm the device exists before mutating, so an unknown id is a client
	// error the operator learns about rather than a silent success. Store.Device
	// reports an unknown id as a plain "device not found" error (not a typed
	// ValidationError), so match on that to keep it a client-facing error.
	if _, err := h.store.Device(ctx, store.DeviceID(id)); err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return RevokeDeviceResult{}, clientError(string(verr))
		}
		if strings.Contains(err.Error(), "not found") {
			return RevokeDeviceResult{}, clientError(err.Error())
		}
		return RevokeDeviceResult{}, err
	}
	if err := h.store.RevokeDevice(ctx, store.DeviceID(id)); err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return RevokeDeviceResult{}, clientError(string(verr))
		}
		return RevokeDeviceResult{}, err
	}
	// Abort the revoked device's in-flight transcodes. Enumerate the currently-
	// active playback sessions (the stream-token ledger; resolved through the
	// operator's own filter, but the SessionID/DeviceID identity fields are never
	// reveal-suppressed — only Media titles are), then terminate the transcodes of
	// every session belonging to the revoked device. A revoked device with no
	// live transcode (direct play, or no playback) aborts zero — the event-stream
	// termination above is the whole propagation for that case.
	var active []store.ActiveSession
	if h.transcodeTerminator != nil {
		var err error
		active, err = h.store.ActiveSessions(ctx, time.Now().UTC(), scope.LibraryFilter())
		if err != nil {
			return RevokeDeviceResult{}, err
		}
	}
	aborted := h.abortDeviceTranscodes(activeSessionIDsByDevice(active)[store.DeviceID(id)])
	if err := h.logActivity(ctx, scope, store.ActivityDeviceRevoke, "device", id, ""); err != nil {
		return RevokeDeviceResult{}, err
	}
	return RevokeDeviceResult{DeviceID: id, TranscodesAborted: aborted}, nil
}

func activeSessionIDsByDevice(active []store.ActiveSession) map[store.DeviceID][]store.SessionID {
	type key struct {
		deviceID  store.DeviceID
		sessionID store.SessionID
	}
	byDevice := make(map[store.DeviceID][]store.SessionID)
	seen := make(map[key]bool, len(active))
	for _, session := range active {
		key := key{deviceID: session.DeviceID, sessionID: session.SessionID}
		if seen[key] {
			continue
		}
		seen[key] = true
		byDevice[session.DeviceID] = append(byDevice[session.DeviceID], session.SessionID)
	}
	return byDevice
}

// abortDeviceTranscodes terminates each pre-grouped Session once. Returns the
// count aborted. A nil terminator or empty list aborts zero.
func (h *Handler) abortDeviceTranscodes(sessionIDs []store.SessionID) int {
	if h.transcodeTerminator == nil {
		return 0
	}
	aborted := 0
	for _, sessionID := range sessionIDs {
		aborted += h.transcodeTerminator.TerminateSession(sessionID)
	}
	return aborted
}

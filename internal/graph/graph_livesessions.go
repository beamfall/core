package graph

import (
	"context"
	"errors"
	"slices"
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
	SessionID   string `json:"sessionID"`
	DeviceID    string `json:"deviceID"`
	DeviceName  string `json:"deviceName"`
	DeviceRole  string `json:"deviceRole"`
	ProfileID   string `json:"profileID"`
	ProfileName string `json:"profileName"`
	MediaID     string `json:"mediaID"`
	Title       string `json:"title,omitempty"`
	LibraryID   string `json:"libraryID,omitempty"`
	Transcoding bool   `json:"transcoding"`
	// PlayMethod is "direct", "remux" or "transcode". PlayReason names why the
	// source was not played as-is, from the closed vocabulary in the playback
	// service, and Bitrate is the delivered bits per second. Reason and bitrate
	// are suppressed with the title for a Media outside the operator's view.
	PlayMethod      string `json:"playMethod"`
	PlayReason      string `json:"playReason"`
	Bitrate         int    `json:"bitrate"`
	PositionSeconds int    `json:"positionSeconds"`
	DurationSeconds int    `json:"durationSeconds"`
	ExpiresAt       string `json:"expiresAt"`
}

// PlayObservation is what the playback service last decided for one Session
// and Media: the delivery method, why, and the delivered bitrate.
type PlayObservation struct {
	Method  string
	Reason  string
	Bitrate int
}

// PlaybackObserver reports the playback decision held for a live session.
// Defined at the consumer; the app adapts the playback service to it.
type PlaybackObserver interface {
	PlayObservation(sessionID store.SessionID, mediaID store.MediaID) (PlayObservation, bool)
}

// playDelivery resolves the method, reason and bitrate for one active session.
// With no held decision (a restart, or a client that never asked for playback
// info) it falls back to the Stream Token ledger: transcode when an HLS grant
// is outstanding, direct otherwise.
func (h *Handler) playDelivery(s store.ActiveSession) (method, reason string, bitrate int) {
	method = "direct"
	if s.Transcoding {
		method = "transcode"
	}
	if h.playbackObserver == nil {
		return method, "", 0
	}
	seen, ok := h.playbackObserver.PlayObservation(s.SessionID, s.MediaID)
	if !ok {
		return method, "", 0
	}
	if s.LibraryID == "" {
		return seen.Method, "", 0
	}
	return seen.Method, seen.Reason, seen.Bitrate
}

// liveSessions returns every currently-active playback session for the operator
// Console. Reveal-filtered by the operator's OWN scope, exactly
// like the other Console reads — a restricted-Library session the operator
// hasn't revealed appears with its media fields suppressed.
//
// A grant alone does not prove a playback is still running: grants live for
// hours and a closed or crashed player never revokes them. So a playback with
// no new grant and no progress report within store.LiveSessionActivityWindow is
// left out (CORE-2305), and Transcoding follows the reported play method so the
// two fields never disagree.
func (h *Handler) liveSessions(ctx context.Context, scope auth.Scope) ([]LiveSessionInfo, error) {
	now := time.Now().UTC()
	sessions, err := h.store.ActiveSessions(ctx, now, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	activeSince := now.Add(-store.LiveSessionActivityWindow)
	out := make([]LiveSessionInfo, 0, len(sessions))
	for _, s := range sessions {
		if s.LastActiveAt.Before(activeSince) {
			continue
		}
		method, reason, bitrate := h.playDelivery(s)
		out = append(out, LiveSessionInfo{
			SessionID:       string(s.SessionID),
			DeviceID:        string(s.DeviceID),
			DeviceName:      s.DeviceName,
			DeviceRole:      string(s.DeviceRole),
			ProfileID:       string(s.ProfileID),
			ProfileName:     s.ProfileName,
			MediaID:         string(s.MediaID),
			Title:           s.Title,
			LibraryID:       string(s.LibraryID),
			Transcoding:     method == "transcode",
			PlayMethod:      method,
			PlayReason:      reason,
			Bitrate:         bitrate,
			PositionSeconds: s.PositionSeconds,
			DurationSeconds: s.DurationSeconds,
			ExpiresAt:       s.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// endPlayback ends the calling session's playback of one Media (CORE-2305). A
// player calls it when it closes, so the live-sessions dashboard drops the
// playback at once instead of waiting for the activity window, and the
// session's transcode of that Media stops at once instead of running until
// idle reap (CORE-2345). It touches only the caller's own session, so any
// session-scoped caller may use it, and it is idempotent: ending a playback
// that is not active still reports true.
func (h *Handler) endPlayback(ctx context.Context, scope auth.Scope, req request) error {
	mediaID := trimmedVariable(req, "mediaId")
	if mediaID == "" {
		return clientError("mediaId is required")
	}
	if scope.SessionID == "" {
		return nil
	}
	if err := h.store.EndPlayback(ctx, scope.SessionID, store.MediaID(mediaID)); err != nil {
		return err
	}
	if h.transcodeTerminator != nil {
		h.transcodeTerminator.TerminatePlayback(scope.SessionID, store.MediaID(mediaID))
	}
	return nil
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
	// Abort the revoked device's in-flight transcodes: terminate the transcodes
	// of every session belonging to the revoked device. A revoked device with no
	// live transcode (direct play, or no playback) aborts zero — the event-stream
	// termination above is the whole propagation for that case.
	byDevice, err := h.transcodeSessionIDsByDevice(ctx, scope)
	if err != nil {
		return RevokeDeviceResult{}, err
	}
	aborted := 0
	if ids := byDevice[store.DeviceID(id)]; len(ids) != 0 {
		aborted = h.transcodeTerminator.RevokeSessions(ids)()
	}
	if err := h.logActivity(ctx, scope, store.ActivityDeviceRevoke, "device", id, ""); err != nil {
		return RevokeDeviceResult{}, err
	}
	return RevokeDeviceResult{DeviceID: id, TranscodesAborted: aborted}, nil
}

// transcodeSessionIDsByDevice lists, per device, every session that may own a
// transcode. It joins two sources because neither is complete alone. The
// stream-token ledger (resolved through the operator's own filter, but the
// SessionID/DeviceID identity fields are never reveal-suppressed — only Media
// titles are) still names a session whose auth row a renewal has replaced. The
// device's auth sessions name a session whose ledger rows endPlayback already
// cleared, or whose grants expired while its transcode kept running
// (CORE-2345); the ledger alone missed those, so revocation aborted zero. A
// session with no live transcode costs the terminator a zero-stop no-op. Nil
// when no terminator is wired, since there is nothing to abort.
func (h *Handler) transcodeSessionIDsByDevice(ctx context.Context, scope auth.Scope) (map[store.DeviceID][]store.SessionID, error) {
	if h.transcodeTerminator == nil {
		return nil, nil
	}
	active, err := h.store.ActiveSessions(ctx, time.Now().UTC(), scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	byDevice := activeSessionIDsByDevice(active)
	enrolled, err := h.store.SessionIDsByDevice(ctx)
	if err != nil {
		return nil, err
	}
	for deviceID, sessionIDs := range enrolled {
		for _, sessionID := range sessionIDs {
			if !slices.Contains(byDevice[deviceID], sessionID) {
				byDevice[deviceID] = append(byDevice[deviceID], sessionID)
			}
		}
	}
	return byDevice, nil
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

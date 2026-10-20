package graph

import (
	"context"
	"errors"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Cross-device session control, graph half. The session-
// authenticated remote surface: any device in a Profile may enumerate that
// Profile's active playback sessions, and — over the EXISTING /api/events SSE —
// subscribe to and issue transport commands against another same-Profile
// device's session. This is NOT Beam (no session transfer): the playing device
// stays the transport executor; the controller is stateless.
//
// Both fields are session-scoped (NOT Admin/Console): they sit in the viewer's
// own scope, gated only by the same-Profile + Reveal contract enforced in the
// resolvers below. The enumeration mirrors the Profile-narrowing the /v1/
// ScopeReadSessions surface applies (server.v1Sessions) but on the session-auth
// surface; the transport mutation publishes a session-control event on the
// outbox (store.AppendSessionControlEvent) that the SSE surface delivers only to
// same-Profile, reveal-permitted subscribers (server.eventPermitted).

// ProfileSessionInfo is one active playback session in the caller's Profile on
// the Ambient Remote enumeration. Title/libraryID are empty when
// the session is playing Media outside the caller's current visibility (a
// restricted Library the caller has not revealed) — the session is still listed
// so the remote sees the device is active, but its identifying fields are
// suppressed, the same hidden==shape rule the live-sessions dashboard follows.
// "Current" marks the caller's own session so the remote can exclude itself.
type ProfileSessionInfo struct {
	SessionID       string `json:"sessionID"`
	DeviceID        string `json:"deviceID"`
	DeviceName      string `json:"deviceName"`
	DeviceRole      string `json:"deviceRole"`
	Current         bool   `json:"current"`
	MediaID         string `json:"mediaID,omitempty"`
	Title           string `json:"title,omitempty"`
	LibraryID       string `json:"libraryID,omitempty"`
	Transcoding     bool   `json:"transcoding"`
	PositionSeconds int    `json:"positionSeconds"`
	DurationSeconds int    `json:"durationSeconds"`
}

// profileSessions enumerates the active playback sessions in the CALLER'S OWN
// Profile — never any other Profile's. It resolves over the
// existing Stream Token ledger (store.ActiveSessions, no new storage), filtered
// by the caller's own LibraryFilter so Media identity is reveal-suppressed
// exactly as on the operator dashboard, then narrows to scope.ProfileID (the
// store reports every session's row for the operator dashboard, so the
// own-Profile narrowing is applied here, mirroring server.v1Sessions). A caller
// with no Profile (admin token / unbound scoped key) gets an empty list — there
// is no Profile to scope to.
func (h *Handler) profileSessions(ctx context.Context, scope auth.Scope) ([]ProfileSessionInfo, error) {
	if scope.ProfileID == "" {
		return []ProfileSessionInfo{}, nil
	}
	sessions, err := h.store.ActiveSessions(ctx, time.Now().UTC(), scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]ProfileSessionInfo, 0, len(sessions))
	for _, s := range sessions {
		if s.ProfileID != scope.ProfileID {
			continue // same-Profile only: never list another Profile's session
		}
		out = append(out, ProfileSessionInfo{
			SessionID:       string(s.SessionID),
			DeviceID:        string(s.DeviceID),
			DeviceName:      s.DeviceName,
			DeviceRole:      string(s.DeviceRole),
			Current:         s.SessionID == scope.SessionID,
			MediaID:         string(s.MediaID),
			Title:           s.Title,
			LibraryID:       string(s.LibraryID),
			Transcoding:     s.Transcoding,
			PositionSeconds: s.PositionSeconds,
			DurationSeconds: s.DurationSeconds,
		})
	}
	return out, nil
}

// ControlSessionResult echoes the controlled session id and the command that
// was delivered. Success means the command was authorized and
// published to the target's event channel — the playing device applies it and
// emits the resulting state on its own session channel; the server is the single
// processor, identical regardless of which device sent the command.
type ControlSessionResult struct {
	SessionID string `json:"sessionID"`
	Command   string `json:"command"`
}

// controlSession issues a transport command (skip-media / skip-track / pause /
// resume) against another device's active session in the SAME Profile.
// The authorization is two hard gates, both fail-closed and
// both reporting an unknown/unavailable session rather than leaking existence:
//
//  1. Same-Profile: the target session must resolve to the caller's OWN Profile.
//     A cross-Profile target is reported "unknown session" — a device can never
//     learn that another Profile's session exists, let alone control it.
//  2. Reveal: the target must have an active playback whose Media the CALLER can
//     currently see. ActiveSessions reveal-suppresses Media for a restricted
//     Library the caller has not revealed, so a session playing such Media has
//     its row but with an empty Title — the caller is treated as unable to
//     control it ("session not controllable"), upholding ADR-0035 §"Security
//     surface": an unrevealed device cannot control a Revealed session.
//
// On success the command is published on the outbox (store.AppendSessionControlEvent)
// with the target's resolved Profile + Media in the payload, so the SSE surface
// delivers it to the target device (and any other same-Profile, reveal-permitted
// subscriber) without re-authorizing at delivery time.
func (h *Handler) controlSession(ctx context.Context, scope auth.Scope, req request) (ControlSessionResult, error) {
	targetID := trimmedVariable(req, "sessionID")
	command := trimmedVariable(req, "command")
	if targetID == "" {
		return ControlSessionResult{}, clientError("sessionID is required")
	}
	if !store.SessionControlCommandValid(command) {
		return ControlSessionResult{}, clientError("unknown transport command")
	}
	if scope.ProfileID == "" {
		// No Profile to scope to: a non-Profile caller can never share a Profile
		// with any session, so every target is "unknown" to it.
		return ControlSessionResult{}, clientError("unknown session")
	}

	// Gate 1 — same Profile. Resolve the target session's Profile; a missing or
	// cross-Profile session is reported identically as "unknown session" so the
	// surface is never an existence oracle for another Profile's sessions.
	target, err := h.store.Session(ctx, store.SessionID(targetID))
	if err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return ControlSessionResult{}, clientError(string(verr))
		}
		return ControlSessionResult{}, clientError("unknown session")
	}
	if target.ProfileID != scope.ProfileID {
		return ControlSessionResult{}, clientError("unknown session")
	}

	// Gate 2 — the target has an active playback the caller can see. Reveal is
	// applied through the CALLER's own filter: a session playing Media the caller
	// cannot reveal comes back with its identifying fields suppressed, and is
	// treated as not controllable (the caller never learns what restricted Media
	// it is, nor commands it).
	active, err := h.store.ActiveSessions(ctx, time.Now().UTC(), scope.LibraryFilter())
	if err != nil {
		return ControlSessionResult{}, err
	}
	var mediaID store.MediaID
	found := false
	visible := false
	for _, s := range active {
		if s.SessionID != store.SessionID(targetID) {
			continue
		}
		found = true
		if s.Title != "" { // reveal-visible: Media identity is present
			mediaID = s.MediaID
			visible = true
		}
		break
	}
	if !found {
		return ControlSessionResult{}, clientError("session has no active playback")
	}
	if !visible {
		// Reveal gate: the target is playing restricted Media the caller has not
		// revealed. Refuse without leaking which Media (ADR-0035 §"Security
		// surface").
		return ControlSessionResult{}, clientError("session not controllable")
	}

	now := time.Now().UTC()
	if err := h.store.AppendSessionControlEventWithActivity(ctx, store.SessionID(targetID), store.SessionControlPayload{
		Command:    command,
		FromDevice: scope.DeviceID,
		ProfileID:  target.ProfileID,
		MediaID:    mediaID,
	}, store.ActivityEntry{
		OccurredAt: now,
		Action:     store.ActivitySessionControl,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  target.ProfileID,
		TargetType: "session",
		TargetID:   targetID,
		TokenID:    scope.APITokenID,
		APIKeyID:   scope.APIKeyID,
	}, now); err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return ControlSessionResult{}, clientError(string(verr))
		}
		return ControlSessionResult{}, err
	}
	return ControlSessionResult{SessionID: targetID, Command: command}, nil
}

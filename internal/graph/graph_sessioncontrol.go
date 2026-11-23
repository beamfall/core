package graph

import (
	"context"
	"errors"
	"math"
	"strings"
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
// the Ambient Remote enumeration. MediaID, Title, and LibraryID are empty when
// the session is playing Media outside the caller's current visibility (a
// restricted Library the caller has not revealed) — the session is still listed
// so the remote sees the device is active, but its identifying fields are
// suppressed (ADR-0035 §7). "Current" marks the caller's own session so the
// remote can exclude itself.
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
		// ADR-0035 §7: clear MediaID alongside Title/LibraryID when the
		// Media is outside the caller's visibility so no identity leaks.
		mediaID := string(s.MediaID)
		if s.Title == "" {
			mediaID = ""
		}
		out = append(out, ProfileSessionInfo{
			SessionID:       string(s.SessionID),
			DeviceID:        string(s.DeviceID),
			DeviceName:      s.DeviceName,
			DeviceRole:      string(s.DeviceRole),
			Current:         s.SessionID == scope.SessionID,
			MediaID:         mediaID,
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

// controlSession issues a transport command from the closed session-control
// vocabulary (store.SessionControlCommands) against another device's session
// in the SAME Profile. The authorization is per Profile, fail-closed, and
// every refusal reports an unknown/unavailable session rather than leaking
// existence:
//
//  1. Same-Profile: the target session must resolve to the caller's OWN Profile.
//     A cross-Profile target is reported "unknown session" — a device can never
//     learn that another Profile's session exists, let alone control it.
//  2. Reveal: ActiveSessions reveal-suppresses Media for a restricted Library
//     the caller has not revealed, so a session playing such Media has its row
//     but with an empty Title — the caller is treated as unable to control it
//     ("session not controllable"), upholding ADR-0035 §"Security surface": an
//     unrevealed device cannot control a Revealed session.
//  3. Transport verbs need an active playback on the target. play-media does
//     not (it is how "play on" reaches an idle device), but the target must be
//     a live device session and the Media to start must be visible to the
//     caller; an unknown and a hidden Media answer identically.
//
// On success the command is published on the outbox (store.AppendSessionControlEvent)
// with the target's resolved Profile and a reveal anchor Media in the payload,
// so the SSE surface delivers it to the target device (and any other
// same-Profile, reveal-permitted subscriber) without re-authorizing at delivery
// time. For play-media the anchor is the started Media, so a target that cannot
// see it never receives the command.
func (h *Handler) controlSession(ctx context.Context, scope auth.Scope, req request) (ControlSessionResult, error) {
	targetID := trimmedVariable(req, "sessionID")
	command := trimmedVariable(req, "command")
	if targetID == "" {
		return ControlSessionResult{}, clientError("sessionID is required")
	}
	args, err := sessionControlArgsFromVariables(req)
	if err != nil {
		return ControlSessionResult{}, err
	}
	if _, err := store.ValidateSessionControlArgs(command, args); err != nil {
		return ControlSessionResult{}, sessionControlClientError(err)
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

	// Gate 2 — reveal. Applied through the CALLER's own filter: a session
	// playing Media the caller cannot reveal comes back with its identifying
	// fields suppressed, and is treated as not controllable (the caller never
	// learns what restricted Media it is, nor commands it). ActiveSessions has
	// a row per session and Media, so every row of the target must be visible:
	// one hidden row makes the whole session uncontrollable, whatever order the
	// rows come back in. The rows' Media (a prefetched next item as well as the
	// current one) travel as the receiver's anchor set: which of them is on
	// screen only the receiver knows.
	now := time.Now().UTC()
	active, err := h.store.GrantedSessions(ctx, now, scope.LibraryFilter())
	if err != nil {
		return ControlSessionResult{}, err
	}
	var playing []store.MediaID
	for _, s := range active {
		if s.SessionID != store.SessionID(targetID) {
			continue
		}
		if s.Title == "" { // reveal-hidden: Media identity is suppressed
			return ControlSessionResult{}, clientError("session not controllable")
		}
		playing = append(playing, s.MediaID)
	}
	found := len(playing) > 0
	var activeMedia store.MediaID
	if found {
		activeMedia = playing[0]
	}

	// Gate 3 — what the command needs from the target.
	anchor := activeMedia
	if command == store.SessionCommandPlayMedia {
		if target.Kind == store.SessionKindIntegration || target.DeviceID == "" ||
			(!target.ExpiresAt.IsZero() && !target.ExpiresAt.After(now)) {
			return ControlSessionResult{}, clientError("session not controllable")
		}
		if _, err := h.store.Media(ctx, args.MediaID, scope.LibraryFilter()); err != nil {
			var verr store.ValidationError
			if errors.As(err, &verr) {
				return ControlSessionResult{}, clientError("unknown media")
			}
			return ControlSessionResult{}, err
		}
		anchor = args.MediaID
	} else if !found {
		return ControlSessionResult{}, clientError("session has no active playback")
	}

	payload, err := store.NewSessionControlPayload(command, args, scope.DeviceID, target.ProfileID, anchor)
	if err != nil {
		return ControlSessionResult{}, sessionControlClientError(err)
	}
	if command != store.SessionCommandPlayMedia {
		payload.PlayingMediaIDs = playing
	}
	if err := h.store.AppendSessionControlEventWithActivity(ctx, store.SessionID(targetID), payload, store.ActivityEntry{
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

// sessionControlArgsFromVariables reads the optional per-command arguments.
// Absent and JSON-null arguments are unset; a present argument of the wrong
// type is a client error rather than a silent zero, so a controller never
// seeks to 0 or mutes by accident.
func sessionControlArgsFromVariables(req request) (store.SessionControlArgs, error) {
	var args store.SessionControlArgs
	for _, field := range []struct {
		name string
		dst  func(string)
	}{
		{"direction", func(v string) { args.Direction = v }},
		{"mediaID", func(v string) { args.MediaID = store.MediaID(v) }},
		{"text", func(v string) { args.Text = v }},
	} {
		if raw, ok := req.Variables[field.name]; ok && raw != nil {
			value, ok := raw.(string)
			if !ok {
				return store.SessionControlArgs{}, clientError(field.name + " must be a string")
			}
			field.dst(strings.TrimSpace(value))
		}
	}
	if raw, ok := req.Variables["positionMs"]; ok && raw != nil {
		value, ok := wholeNumber(raw)
		if !ok {
			return store.SessionControlArgs{}, clientError("positionMs must be a whole number")
		}
		args.PositionMs = &value
	}
	if raw, ok := req.Variables["volume"]; ok && raw != nil {
		value, ok := wholeNumber(raw)
		if !ok || value < 0 || value > store.SessionControlMaxVolume {
			return store.SessionControlArgs{}, clientError("volume must be between 0 and 100")
		}
		volume := int(value)
		args.Volume = &volume
	}
	if raw, ok := req.Variables["trackID"]; ok && raw != nil {
		value, ok := raw.(string)
		if !ok {
			return store.SessionControlArgs{}, clientError("trackID must be a string")
		}
		value = strings.TrimSpace(value)
		args.TrackID = &value
	}
	return args, nil
}

// wholeNumber accepts a JSON number with no fractional part that fits in an
// int64 without loss.
func wholeNumber(raw any) (int64, bool) {
	switch value := raw.(type) {
	case float64:
		if value != math.Trunc(value) || math.Abs(value) > 1<<53 {
			return 0, false
		}
		return int64(value), true
	case int:
		return int64(value), true
	case int64:
		return value, true
	default:
		return 0, false
	}
}

func sessionControlClientError(err error) error {
	var verr store.ValidationError
	if errors.As(err, &verr) {
		return clientError(string(verr))
	}
	return err
}

package graph

import (
	"context"
	"errors"
	"log/slog"
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
	ArtworkURL      string `json:"artworkURL,omitempty"`
	PrivateItem     bool   `json:"privateItem,omitempty"`
	Label           string `json:"label,omitempty"`
	Transcoding     bool   `json:"transcoding"`
	PositionSeconds int    `json:"positionSeconds"`
	DurationSeconds int    `json:"durationSeconds"`
	Revision        int64  `json:"revision"`
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
	sessionIDs := make([]store.SessionID, 0, len(sessions))
	for _, s := range sessions {
		if s.ProfileID == scope.ProfileID {
			sessionIDs = append(sessionIDs, s.SessionID)
		}
	}
	revisions, err := h.store.RendererSessionRevisions(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}
	out := make([]ProfileSessionInfo, 0, len(sessionIDs))
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
			Revision:        revisions[s.SessionID],
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

// ControlRendererSessionResult is the ADR-0151 D3 / ADR-0240 D3 result for
// controlRendererSession: APPLIED with the new monotonic revision, or CONFLICT
// with the current revision and a caller-filtered snapshot of that session.
type ControlRendererSessionResult struct {
	Status    string                `json:"status"`
	SessionID string                `json:"sessionID"`
	Command   string                `json:"command"`
	Revision  int64                 `json:"revision"`
	Snapshot  *[]ProfileSessionInfo `json:"snapshot,omitempty"`
}

// controlSession issues a transport command from the closed session-control
// vocabulary (store.SessionControlCommands) against another device's session
// in the SAME Profile. controlSession stays strictly same-Profile (and refuses
// any player target); ADR-0240 D3 forbids relaxing its Profile equality check
// for player targets — revision-checked control lives on controlRendererSession.
// The authorization is per Profile, fail-closed, and every refusal reports an
// unknown/unavailable session rather than leaking existence:
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
// and bumps the target session's monotonic revision so revision-checked callers
// see its effects. The SSE surface delivers it to the target device (and any
// other same-Profile, reveal-permitted subscriber) without re-authorizing at
// delivery time. For play-media the anchor is the started Media, so a target
// that cannot see it never receives the command.
// managedParentalCommands is the closed set of session-control verbs an adult
// Profile may issue against a managed (child) Profile's active session under
// ADR-0242 §2.3 without requiring Admin Session re-auth.
var managedParentalCommands = map[string]bool{
	store.SessionCommandPause:          true,
	store.SessionCommandResume:         true,
	store.SessionCommandStop:           true,
	store.SessionCommandDisplayMessage: true,
}

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
	now := h.now().UTC()
	target, playing, anchor, playerGuard, err := h.authorizeSessionControl(ctx, scope, targetID, command, args, now, false)
	if err != nil {
		return ControlSessionResult{}, err
	}
	if playerGuard != nil {
		// ADR-0240 D3: the legacy controlSession stays same-Profile and refuses
		// every player target; only controlRendererSession drives a display.
		return ControlSessionResult{}, clientError("unknown session")
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

// controlRendererSession issues a revision-checked, idempotent transport command
// against a Renderer Session (ADR-0151 D1-D3, ADR-0240 D3). It enforces the
// same authorization gates and non-leaking refusals as controlSession.
func (h *Handler) controlRendererSession(ctx context.Context, scope auth.Scope, req request) (ControlRendererSessionResult, error) {
	targetID := trimmedVariable(req, "sessionID")
	if targetID == "" {
		return ControlRendererSessionResult{}, clientError("sessionID is required")
	}
	rawRevision, hasRevision := req.Variables["expectedRevision"]
	if !hasRevision || rawRevision == nil {
		return ControlRendererSessionResult{}, clientError("expectedRevision is required")
	}
	expectedRevision, ok := wholeNumber(rawRevision)
	if !ok || expectedRevision < 0 {
		return ControlRendererSessionResult{}, clientError("expectedRevision must be a non-negative integer")
	}
	rawCommandID, hasCommandID := req.Variables["commandID"]
	if !hasCommandID || rawCommandID == nil {
		return ControlRendererSessionResult{}, clientError("commandID is required")
	}
	commandIDStr, ok := rawCommandID.(string)
	if !ok {
		return ControlRendererSessionResult{}, clientError("commandID must be a string")
	}
	commandID := strings.TrimSpace(commandIDStr)
	if err := store.ValidateRendererSessionCommandID(commandID); err != nil {
		return ControlRendererSessionResult{}, sessionControlClientError(err)
	}
	command := trimmedVariable(req, "command")
	args, err := sessionControlArgsFromVariables(req)
	if err != nil {
		return ControlRendererSessionResult{}, err
	}
	if _, err := store.ValidateSessionControlArgs(command, args); err != nil {
		return ControlRendererSessionResult{}, sessionControlClientError(err)
	}

	now := h.now().UTC()
	target, playing, anchor, playerGuard, err := h.authorizeSessionControl(ctx, scope, targetID, command, args, now, true)
	if err != nil {
		return ControlRendererSessionResult{}, err
	}

	payload, err := store.NewSessionControlPayload(command, args, scope.DeviceID, target.ProfileID, anchor)
	if err != nil {
		return ControlRendererSessionResult{}, sessionControlClientError(err)
	}
	if command != store.SessionCommandPlayMedia {
		payload.PlayingMediaIDs = playing
	}
	activityProfile := target.ProfileID
	if playerGuard != nil {
		activityProfile = scope.ProfileID
	}
	activity := &store.ActivityEntry{
		OccurredAt: now,
		Action:     store.ActivitySessionControl,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  activityProfile,
		TargetType: "session",
		TargetID:   targetID,
		TokenID:    scope.APITokenID,
		APIKeyID:   scope.APIKeyID,
	}
	var res store.RendererSessionControlResult
	if playerGuard != nil {
		res, err = h.store.ControlRendererSessionForPlayer(ctx, store.SessionID(targetID), expectedRevision, commandID, payload, activity, now, *playerGuard)
	} else {
		res, err = h.store.ControlRendererSession(ctx, store.SessionID(targetID), expectedRevision, commandID, payload, activity, now)
	}
	if err != nil {
		if errors.Is(err, store.ErrDisplayInParty) {
			if playerGuard != nil {
				if isCtrl, cerr := h.store.IsPlayerController(ctx, target.ProfileID, scope.ProfileID); cerr != nil || !isCtrl {
					return ControlRendererSessionResult{}, clientError("unknown session")
				}
				h.auditPlayerControl(ctx, scope, target, command, "in-party")
			}
			return ControlRendererSessionResult{}, reasonedClientError{
				message: "display in party",
				reason:  ErrorReasonDisplayInParty,
			}
		}
		if errors.Is(err, store.ErrPlayerControlUnauthorized) || errors.Is(err, store.ErrSessionProfileMismatch) {
			if playerGuard != nil {
				h.auditPlayerControl(ctx, scope, target, command, "unauthorized")
			}
			return ControlRendererSessionResult{}, clientError("unknown session")
		}
		if errors.Is(err, store.ErrRendererSessionCommandReuse) {
			if playerGuard != nil {
				if isCtrl, cerr := h.store.IsPlayerController(ctx, target.ProfileID, scope.ProfileID); cerr != nil || !isCtrl {
					return ControlRendererSessionResult{}, clientError("unknown session")
				}
				h.auditPlayerControl(ctx, scope, target, command, "command-reuse")
			}
			return ControlRendererSessionResult{}, clientError("commandID already used for a different command")
		}
		var conflict store.RendererSessionRevisionConflictError
		if errors.As(err, &conflict) {
			if playerGuard != nil {
				if isCtrl, cerr := h.store.IsPlayerController(ctx, target.ProfileID, scope.ProfileID); cerr != nil || !isCtrl {
					return ControlRendererSessionResult{}, clientError("unknown session")
				}
				h.auditPlayerControl(ctx, scope, target, command, "conflict")
			}
			snapshot, snapErr := h.rendererSessionSnapshot(ctx, scope, store.SessionID(targetID), conflict.CurrentRevision, now)
			if snapErr != nil {
				return ControlRendererSessionResult{}, snapErr
			}
			return ControlRendererSessionResult{
				Status:    string(store.RendererSessionControlConflict),
				SessionID: targetID,
				Command:   command,
				Revision:  conflict.CurrentRevision,
				Snapshot:  &snapshot,
			}, nil
		}
		if playerGuard != nil {
			h.auditPlayerControl(ctx, scope, target, command, "error")
		}
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return ControlRendererSessionResult{}, clientError(string(verr))
		}
		return ControlRendererSessionResult{}, err
	}
	if playerGuard != nil {
		resultLabel := "applied"
		if res.Replayed {
			resultLabel = "replayed"
		}
		h.auditPlayerControl(ctx, scope, target, command, resultLabel)
	}
	return ControlRendererSessionResult{
		Status:    string(res.Status),
		SessionID: string(res.SessionID),
		Command:   res.Command,
		Revision:  res.Revision,
	}, nil
}

func (h *Handler) auditPlayerControl(ctx context.Context, scope auth.Scope, target store.Session, verb, result string) {
	slog.InfoContext(ctx, "player session control",
		"controllerProfileID", scope.ProfileID,
		"controllerSessionID", scope.SessionID,
		"displaySessionID", target.ID,
		"displayDeviceID", target.DeviceID,
		"playerProfileID", target.ProfileID,
		"verb", verb,
		"result", result,
	)
}

func (h *Handler) authorizeSessionControl(
	ctx context.Context,
	scope auth.Scope,
	targetID, command string,
	args store.SessionControlArgs,
	now time.Time,
	rendererControl bool,
) (store.Session, []store.MediaID, store.MediaID, *store.PlayerControlGuard, error) {
	if scope.ProfileID == "" {
		// No Profile to scope to: a non-Profile caller can never share a Profile
		// with any session, so every target is "unknown" to it.
		return store.Session{}, nil, "", nil, clientError("unknown session")
	}
	if scope.IsPlayerBound() {
		// A player-bound display is controlled, never a controller (ADR-0240 D2, D4).
		return store.Session{}, nil, "", nil, clientError("session not controllable")
	}

	// Gate 1 — resolve target session and Profile. A missing session or
	// unauthorized cross-Profile target reports "unknown session" identically so
	// the surface is never an existence oracle.
	target, err := h.store.Session(ctx, store.SessionID(targetID))
	if err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			return store.Session{}, nil, "", nil, clientError(string(verr))
		}
		return store.Session{}, nil, "", nil, clientError("unknown session")
	}
	targetProfile, err := h.store.Profile(ctx, target.ProfileID)
	if err != nil {
		return store.Session{}, nil, "", nil, clientError("unknown session")
	}

	if targetProfile.Kind == store.ProfilePlayer {
		if !rendererControl || !declaresPlayerControl(scope) {
			return store.Session{}, nil, "", nil, clientError("unknown session")
		}
		return h.authorizePlayerSessionControl(ctx, scope, target, targetProfile, command, args, now)
	}

	if target.ProfileID != scope.ProfileID {
		if scope.Profile != store.ProfileAdult || targetProfile.Kind != store.ProfileManaged {
			return store.Session{}, nil, "", nil, clientError("unknown session")
		}
		if !managedParentalCommands[command] {
			return store.Session{}, nil, "", nil, clientError("command not permitted for managed session")
		}
	}

	// Gate 2 — reveal. Applied through the CALLER's own filter: a session
	// playing Media the caller cannot reveal comes back with its identifying
	// fields suppressed, and is treated as not controllable (the caller never
	// learns what restricted Media it is, nor commands it). GrantedSessions has
	// a row per session and Media, so every row of the target must be visible:
	// one hidden row makes the whole session uncontrollable, whatever order the
	// rows come back in.
	active, err := h.store.GrantedSessions(ctx, now, scope.LibraryFilter())
	if err != nil {
		return store.Session{}, nil, "", nil, err
	}
	var playing []store.MediaID
	for _, s := range active {
		if s.SessionID != store.SessionID(targetID) || s.ProfileID != target.ProfileID {
			continue
		}
		if s.Title == "" { // reveal-hidden: Media identity is suppressed
			return store.Session{}, nil, "", nil, clientError("session not controllable")
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
			return store.Session{}, nil, "", nil, clientError("session not controllable")
		}
		if _, err := h.store.Media(ctx, args.MediaID, scope.LibraryFilter()); err != nil {
			var verr store.ValidationError
			if errors.As(err, &verr) {
				return store.Session{}, nil, "", nil, clientError("unknown media")
			}
			return store.Session{}, nil, "", nil, err
		}
		anchor = args.MediaID
	} else if !found {
		return store.Session{}, nil, "", nil, clientError("session has no active playback")
	}
	return target, playing, anchor, nil, nil
}

func (h *Handler) authorizePlayerSessionControl(
	ctx context.Context,
	scope auth.Scope,
	target store.Session,
	targetProfile store.Profile,
	command string,
	args store.SessionControlArgs,
	now time.Time,
) (store.Session, []store.MediaID, store.MediaID, *store.PlayerControlGuard, error) {
	if targetProfile.Disabled ||
		target.Kind == store.SessionKindIntegration ||
		target.DeviceID == "" ||
		(!target.ExpiresAt.IsZero() && !target.ExpiresAt.After(now)) {
		return store.Session{}, nil, "", nil, clientError("unknown session")
	}
	dev, err := h.store.Device(ctx, target.DeviceID)
	if err != nil || dev.Revoked || dev.PlayerProfileID != target.ProfileID {
		return store.Session{}, nil, "", nil, clientError("unknown session")
	}
	isController, err := h.store.IsPlayerController(ctx, target.ProfileID, scope.ProfileID)
	if err != nil {
		return store.Session{}, nil, "", nil, err
	}
	if !isController {
		return store.Session{}, nil, "", nil, clientError("unknown session")
	}

	if err := h.checkPlayerControlRateLimit(ctx, scope.ProfileID, now); err != nil {
		h.auditPlayerControl(ctx, scope, target, command, "throttled")
		return store.Session{}, nil, "", nil, err
	}
	if !h.isPlayerSessionOnline(target.ID, now) {
		h.auditPlayerControl(ctx, scope, target, command, "offline")
		return store.Session{}, nil, "", nil, reasonedClientError{
			message: "display offline",
			reason:  ErrorReasonDisplayOffline,
		}
	}
	if command != store.SessionCommandStop {
		inParty, err := h.store.IsPlayerDeviceInLiveParty(ctx, dev.ID, target.ID, now)
		if err != nil {
			return store.Session{}, nil, "", nil, err
		}
		if inParty {
			h.auditPlayerControl(ctx, scope, target, command, "in-party")
			return store.Session{}, nil, "", nil, reasonedClientError{
				message: "display in party",
				reason:  ErrorReasonDisplayInParty,
			}
		}
	}

	guard := &store.PlayerControlGuard{
		PlayerProfileID:   target.ProfileID,
		CallerProfileID:   scope.ProfileID,
		CallerSessionID:   scope.SessionID,
		BindingGeneration: dev.PlayerBindingGeneration,
	}

	callerVisibleFilter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return store.Session{}, nil, "", nil, err
	}
	callerPlaybackFilter, err := h.sealedFilter(ctx, scope.PlaybackFilter())
	if err != nil {
		return store.Session{}, nil, "", nil, err
	}

	if command == store.SessionCommandPlayMedia {
		if _, err := h.store.Media(ctx, args.MediaID, callerPlaybackFilter); err != nil {
			var verr store.ValidationError
			if errors.As(err, &verr) {
				h.auditPlayerControl(ctx, scope, target, command, "denied-caller-playback")
				return store.Session{}, nil, "", nil, clientError("unknown media")
			}
			return store.Session{}, nil, "", nil, err
		}
		playerPlaybackFilter, err := h.auth.PlayerProfilePlaybackFilter(ctx, target.ProfileID)
		if err != nil {
			var verr store.ValidationError
			if errors.As(err, &verr) || errors.Is(err, store.ErrProfileDisabled) {
				return store.Session{}, nil, "", nil, clientError("unknown session")
			}
			return store.Session{}, nil, "", nil, err
		}
		playerPlaybackFilter, err = h.sealedFilter(ctx, playerPlaybackFilter)
		if err != nil {
			return store.Session{}, nil, "", nil, err
		}
		if _, err := h.store.Media(ctx, args.MediaID, playerPlaybackFilter); err != nil {
			var verr store.ValidationError
			if errors.As(err, &verr) {
				h.auditPlayerControl(ctx, scope, target, command, "denied-player-playback")
				return store.Session{}, nil, "", nil, clientError("display cannot play this media")
			}
			return store.Session{}, nil, "", nil, err
		}
		return target, nil, args.MediaID, guard, nil
	}

	// Read the display's active media rows so stop/display-message keep the
	// receiver's anchor and transport verbs check caller visibility/playback.
	active, err := h.store.GrantedSessions(ctx, now, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return store.Session{}, nil, "", nil, err
	}
	var playing []store.MediaID
	for _, s := range active {
		if s.SessionID == target.ID {
			playing = append(playing, s.MediaID)
		}
	}
	var anchor store.MediaID
	if len(playing) > 0 {
		anchor = playing[0]
	}

	switch command {
	case store.SessionCommandStop, store.SessionCommandDisplayMessage:
		return target, playing, anchor, guard, nil
	case store.SessionCommandResume:
		if len(playing) == 0 {
			h.auditPlayerControl(ctx, scope, target, command, "no-active-playback")
			return store.Session{}, nil, "", nil, clientError("session has no active playback")
		}
		for _, mediaID := range playing {
			if _, err := h.store.Media(ctx, mediaID, callerVisibleFilter); err != nil {
				h.auditPlayerControl(ctx, scope, target, command, "denied-caller-visibility")
				return store.Session{}, nil, "", nil, clientError("session not controllable")
			}
			if _, err := h.store.Media(ctx, mediaID, callerPlaybackFilter); err != nil {
				h.auditPlayerControl(ctx, scope, target, command, "denied-caller-playback")
				return store.Session{}, nil, "", nil, clientError("session not controllable")
			}
		}
		return target, playing, anchor, guard, nil
	default:
		if len(playing) == 0 {
			h.auditPlayerControl(ctx, scope, target, command, "no-active-playback")
			return store.Session{}, nil, "", nil, clientError("session has no active playback")
		}
		for _, mediaID := range playing {
			if _, err := h.store.Media(ctx, mediaID, callerVisibleFilter); err != nil {
				h.auditPlayerControl(ctx, scope, target, command, "denied-caller-visibility")
				return store.Session{}, nil, "", nil, clientError("session not controllable")
			}
		}
		return target, playing, anchor, guard, nil
	}
}

// rendererSessionSnapshot returns the caller-filtered GrantedSessions rows for
// targetID only at the given revision, suppressing every Media identity field
// whenever the Media is hidden from the caller's LibraryFilter (and rendering
// "Private item" with position/duration only for Player Profile displays per D6).
func (h *Handler) rendererSessionSnapshot(ctx context.Context, scope auth.Scope, targetID store.SessionID, revision int64, now time.Time) ([]ProfileSessionInfo, error) {
	if scope.ProfileID == "" {
		return []ProfileSessionInfo{}, nil
	}
	target, err := h.store.Session(ctx, targetID)
	if err == nil {
		targetProfile, perr := h.store.Profile(ctx, target.ProfileID)
		if perr == nil && targetProfile.Kind == store.ProfilePlayer {
			return h.playerRendererSessionSnapshot(ctx, scope, target, revision, now)
		}
	}
	active, err := h.store.GrantedSessions(ctx, now, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]ProfileSessionInfo, 0)
	for _, s := range active {
		if s.SessionID != targetID || s.ProfileID != scope.ProfileID {
			continue
		}
		row := ProfileSessionInfo{
			SessionID:       string(s.SessionID),
			DeviceID:        string(s.DeviceID),
			DeviceName:      s.DeviceName,
			DeviceRole:      string(s.DeviceRole),
			Current:         s.SessionID == scope.SessionID,
			Transcoding:     s.Transcoding,
			PositionSeconds: s.PositionSeconds,
			DurationSeconds: s.DurationSeconds,
			Revision:        revision,
		}
		if s.Title != "" {
			row.MediaID = string(s.MediaID)
			row.Title = s.Title
			row.LibraryID = string(s.LibraryID)
			row.ArtworkURL = h.playerArtworkURL(s.MediaID)
		} else {
			row.PositionSeconds = 0
			row.DurationSeconds = 0
		}
		out = append(out, row)
	}
	return out, nil
}

func (h *Handler) playerRendererSessionSnapshot(ctx context.Context, scope auth.Scope, target store.Session, revision int64, now time.Time) ([]ProfileSessionInfo, error) {
	if scope.IsPlayerBound() {
		return []ProfileSessionInfo{}, nil
	}
	dev, err := h.store.Device(ctx, target.DeviceID)
	if err != nil || dev.Revoked || dev.PlayerProfileID != target.ProfileID {
		return []ProfileSessionInfo{}, nil
	}
	isCtrl, err := h.store.IsPlayerController(ctx, target.ProfileID, scope.ProfileID)
	if err != nil || !isCtrl {
		return []ProfileSessionInfo{}, nil
	}
	active, err := h.store.GrantedSessions(ctx, now, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return nil, err
	}
	var matching []store.ActiveSession
	var mediaIDs []store.MediaID
	for _, s := range active {
		if s.SessionID == target.ID {
			matching = append(matching, s)
			mediaIDs = append(mediaIDs, s.MediaID)
		}
	}
	if len(matching) == 0 {
		return []ProfileSessionInfo{}, nil
	}
	callerFilter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	visibleMedia, err := h.store.VisibleMediaBatch(ctx, mediaIDs, callerFilter)
	if err != nil {
		return nil, err
	}
	displayName := dev.PlayerDisplayName
	if displayName == "" {
		displayName = dev.Name
	}
	handle := h.playerDisplayHandle(dev.ID)
	out := make([]ProfileSessionInfo, 0, len(matching))
	for _, s := range matching {
		row := ProfileSessionInfo{
			SessionID:       string(s.SessionID),
			DeviceID:        handle,
			DeviceName:      displayName,
			DeviceRole:      string(s.DeviceRole),
			Current:         false,
			Transcoding:     s.Transcoding,
			PositionSeconds: s.PositionSeconds,
			DurationSeconds: s.DurationSeconds,
			Revision:        revision,
		}
		if media, ok := visibleMedia[s.MediaID]; ok {
			row.MediaID = string(s.MediaID)
			row.Title = media.Title
			row.LibraryID = string(media.LibraryID)
			row.ArtworkURL = h.playerArtworkURL(s.MediaID)
		} else {
			row.PrivateItem = true
			row.Label = "Private item"
		}
		out = append(out, row)
	}
	return out, nil
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

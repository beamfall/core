package graph

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/conductor"
	"github.com/beamfall/core/internal/store"
)

// Watch Party, graph half. Watch Party is a Group
// Conductor session with a full-audio-video posture and a host-role
// transport axis (§2.2/§2.4), plus cross-Profile formation by invite + accept
// with a Reveal intersection at acceptance (§2.3/§2.5). It builds on the AV-4
// substrate unchanged:
//
//   - The host's authoritative play/pause/seek/skip ride the SAME /api/events
//     SSE outbox as the Ambient Group transition (broadcastTransition →
//     store.AppendGroupSyncEvent); there is no second transport (§2.3).
//   - Party + invite state is ephemeral and in-memory (the conductor.Registry) —
//     never persisted, no migration, reassembled on reconnect, exactly like the
//     Ambient Queue and the Ambient Group.
//
// The authorization model differs from the Ambient Group on purpose. An Ambient
// Group is strictly same-Profile (the Registry asserts it). A Watch Party is
// explicitly CROSS-Profile — a host invites OTHER Profiles — so the gate is
// invite-acceptance + the Reveal intersection (§2.3), enforced HERE:
//
//   - The host can only create a party around content its OWN session can see
//     (h.store.Media against the host's LibraryFilter), so a host never anchors a
//     party on Media it could not Reveal.
//   - On accept, the ACCEPTING session's own LibraryFilter must be able to see
//     the party's content. A managed Profile (never revealed) and an unrevealed
//     adult session therefore cannot accept a party whose content is restricted —
//     the party ceiling is the intersection of members' Reveal ceilings, and the
//     most-restrictive member caps it (§2.3). An ineligible accept is rejected
//     with an explicit reason, never a silent exclusion (§2.5 step 4).
//
// The sync channel carries only the Media fingerprint + timeline position, never
// bytes (§2.3); each participant fetches bytes through its own Stream Token,
// so the per-session Reveal contract is the only thing that has to
// hold — and it does, because every accept is gated on the accepting session's
// own filter.

// newPartyNotificationID mints an ephemeral id for one Watch Party invite
// notification. Like the conductor group id it is not a durable key — the invite
// lives only as long as the in-memory party — so a crypto-random hex string is
// sufficient and collision-free in practice.
func newPartyNotificationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "wp-" + hex.EncodeToString(b[:])
}

// WatchPartyParticipant is the minimal participant record exposed on the wire for
// a Watch Party member (ADR-0111 §1 participant-exposure minimization). A
// participant sees others only by their chosen display name and a boolean
// playback-presence flag — never a session id, device id, real name, email, or
// account identifier. Present is always true for a joined member; reserved for
// future "away" signalling.
type WatchPartyParticipant struct {
	DisplayName string `json:"displayName"`
	Present     bool   `json:"present"`
}

// WatchPartyInfo is the minimized wire shape of a Watch Party. HostID and
// Invitees remain as compatibility fields but are always empty; host authority
// and outstanding invite Profile identifiers remain internal to the conductor.
type WatchPartyInfo struct {
	PartyID  string                  `json:"partyID"`
	HostID   string                  `json:"hostID"`
	Content  string                  `json:"content"`
	Relaxed  bool                    `json:"relaxed"`
	Invitees []string                `json:"invitees"`
	Members  []WatchPartyParticipant `json:"members"`
}

// watchPartyInfo builds the minimal wire shape for a Watch Party. ctx and st are
// used to resolve each member's ProfileID to their chosen display name (ADR-0111
// §1 participant-exposure minimization); if the store call fails or a profile is
// not found the display name falls back to a non-identifying label.
func watchPartyInfo(ctx context.Context, st *store.Store, g *conductor.Group) WatchPartyInfo {
	members := g.Members()
	wireMembers := make([]WatchPartyParticipant, len(members))
	for i, m := range members {
		displayName := "Participant"
		if st != nil && m.ProfileID != "" {
			if p, err := st.Profile(ctx, m.ProfileID); err == nil {
				displayName = p.Name
			}
		}
		wireMembers[i] = WatchPartyParticipant{DisplayName: displayName, Present: true}
	}
	return WatchPartyInfo{
		PartyID:  g.ID,
		HostID:   "",
		Content:  string(g.Content()),
		Relaxed:  g.Relaxed(),
		Invitees: []string{},
		Members:  wireMembers,
	}
}

// createWatchParty forms a Watch Party hosted by the caller's session, watching
// `content` (ADR-0038 §2.5 step 1). The caller's session is the host; its
// transport becomes authoritative (§2.4). The host can only anchor a party on
// content its OWN session can Reveal — h.store.Media against the host's filter
// rejects a Media the host cannot see, so a party never starts above its host's
// ceiling. The forming device then joins via acceptWatchParty-equivalent Join
// with its own calibration (§2.5: party begins when the host starts playback).
func (h *Handler) createWatchParty(ctx context.Context, scope auth.Scope, req request) (WatchPartyInfo, error) {
	if h.conductor == nil {
		return WatchPartyInfo{}, clientError("group conductor unavailable")
	}
	if scope.ProfileID == "" || scope.SessionID == "" {
		return WatchPartyInfo{}, clientError("a session in a profile is required to host a watch party")
	}
	content := store.MediaID(trimmedVariable(req, "content"))
	if content == "" {
		return WatchPartyInfo{}, clientError("content is required")
	}
	// The host can only host a party around content its own session can see — the
	// Reveal contract on the host's filter is the party's opening ceiling.
	if _, err := h.store.Media(ctx, content, scope.LibraryFilter()); err != nil {
		return WatchPartyInfo{}, clientError("cannot host a party for content you cannot see")
	}
	relaxed := boolVar(req, "relaxed")
	g := h.conductor.FormParty(scope.ProfileID, scope.SessionID, content, relaxed)
	// The host joins its own party with its calibration, exactly like the Ambient
	// Group forming device joins via Join. Calibration rides the same vars as
	// joinGroup (from the LAN calibration handshake).
	hostMember := conductor.Member{
		SessionID:     scope.SessionID,
		DeviceID:      scope.DeviceID,
		ClockOffset:   time.Duration(int64Var(req, "clockOffsetMS")) * time.Millisecond,
		RenderLatency: time.Duration(int64Var(req, "renderLatencyMS")) * time.Millisecond,
	}
	// Join the host through the invite path so a single code path adds every
	// member: pre-seed the host's own invite, then accept it.
	if err := h.conductor.Invite(g.ID, scope.SessionID, scope.ProfileID); err != nil {
		return WatchPartyInfo{}, partyClientError(err)
	}
	if err := h.conductor.Accept(g.ID, scope.ProfileID, hostMember); err != nil {
		return WatchPartyInfo{}, partyClientError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "watch-party", g.ID, ""); err != nil {
		return WatchPartyInfo{}, err
	}
	return watchPartyInfo(ctx, h.store, g), nil
}

// inviteToWatchParty invites a Profile to the party BY DISPLAY NAME (ADR-0038
// §2.5 step 2): the host names a Profile, the server resolves the name to a
// Profile id, records a pending invite, and drops an in-app notification into
// that Profile's inbox (the existing notification primitive). Only the host may
// invite. Display names are not unique-constrained, so an ambiguous name (more
// than one match) is rejected explicitly rather than guessed; an unknown name is
// rejected too. No Reveal check happens here — eligibility is decided on accept
// against the accepting session's own filter (§2.3).
func (h *Handler) inviteToWatchParty(ctx context.Context, scope auth.Scope, req request) (WatchPartyInfo, error) {
	if h.conductor == nil {
		return WatchPartyInfo{}, clientError("group conductor unavailable")
	}
	if scope.SessionID == "" {
		return WatchPartyInfo{}, clientError("a session is required to invite to a watch party")
	}
	partyID := trimmedVariable(req, "partyID")
	if partyID == "" {
		return WatchPartyInfo{}, clientError("partyID is required")
	}
	displayName := trimmedVariable(req, "displayName")
	if displayName == "" {
		return WatchPartyInfo{}, clientError("displayName is required")
	}
	invitee, err := h.resolveProfileByDisplayName(ctx, displayName)
	if err != nil {
		return WatchPartyInfo{}, err
	}
	if err := h.conductor.Invite(partyID, scope.SessionID, invitee); err != nil {
		return WatchPartyInfo{}, partyClientError(err)
	}
	// Drop the in-app notification into the invitee's inbox. LibraryID stays empty
	// so the invite is always visible to its recipient; the Reveal intersection is
	// enforced on accept, not by hiding the invite.
	if h.store != nil {
		if err := h.store.CreateNotification(ctx, store.Notification{
			ID:                 newPartyNotificationID(),
			RecipientProfileID: invitee,
			Kind:               store.NotificationWatchPartyInvite,
			Title:              "Watch Party invitation",
			Ref:                partyID,
			CreatedAt:          time.Now().UTC(),
		}); err != nil {
			return WatchPartyInfo{}, err
		}
	}
	g, err := h.conductor.Party(partyID)
	if err != nil {
		return WatchPartyInfo{}, partyClientError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "watch-party", partyID, ""); err != nil {
		return WatchPartyInfo{}, err
	}
	return watchPartyInfo(ctx, h.store, g), nil
}

// resolveProfileByDisplayName maps a display name to a single Profile id. The
// name column is not unique-constrained, so this rejects an ambiguous name (more
// than one match) rather than guessing, and rejects an unknown name — both with
// explicit reasons (ADR-0038 §2.5: ineligible/unresolvable invites get a reason,
// never a silent drop).
func (h *Handler) resolveProfileByDisplayName(ctx context.Context, displayName string) (store.ProfileID, error) {
	if h.store == nil {
		return "", clientError("no profile with that display name")
	}
	profiles, err := h.store.Profiles(ctx)
	if err != nil {
		return "", err
	}
	var matched store.ProfileID
	count := 0
	for _, p := range profiles {
		if p.Name == displayName && !p.Disabled {
			matched = p.ID
			count++
		}
	}
	switch count {
	case 0:
		return "", clientError("no profile with that display name")
	case 1:
		return matched, nil
	default:
		return "", clientError("display name is ambiguous")
	}
}

// AcceptWatchPartyResult echoes the party the caller joined and the current
// anchor it must sync to (ADR-0038 §2.5 step 5: a late-joining participant is
// synchronized to the current position). The anchor is the canonical
// {item, position, present_at} re-expressed at acceptance time, so a participant
// joining mid-playback lands on the live timeline, not at zero.
type AcceptWatchPartyResult struct {
	PartyID     string `json:"partyID"`
	Item        string `json:"item"`
	PositionMS  int64  `json:"positionMS"`
	PresentAtMS int64  `json:"presentAtMS"`
	// Degraded is an advisory join-time capacity hint:
	// true when the server is at the concurrent-transcode cap for this content, so
	// a transcode-needing client will be served the lowest rung (the party stays
	// whole rather than refusing the join). A direct-playing client is unaffected.
	// Conservative (PriorityNormal probe) and never authoritative — the real
	// decision is each member's own playbackInfo. False when no probe is wired.
	Degraded bool `json:"degraded,omitempty"`
}

// acceptWatchParty accepts a pending invite from the CALLER'S OWN session and
// joins the party (ADR-0038 §2.5 steps 3-5). This is where the Reveal
// intersection is enforced (§2.3): the accepting session's OWN LibraryFilter
// must be able to see the party's content, or the accept is rejected with an
// explicit reason. A managed Profile (never revealed) and an unrevealed adult
// session therefore cannot accept a party on restricted content — the party
// ceiling is capped at the most-restrictive member. On success the caller joins
// with its calibration (the same offset/latency vars as joinGroup) and receives
// the current anchor to sync to as a late joiner.
func (h *Handler) acceptWatchParty(ctx context.Context, scope auth.Scope, req request) (AcceptWatchPartyResult, error) {
	if h.conductor == nil {
		return AcceptWatchPartyResult{}, clientError("group conductor unavailable")
	}
	if scope.ProfileID == "" || scope.SessionID == "" {
		return AcceptWatchPartyResult{}, clientError("a session in a profile is required to accept a watch party")
	}
	partyID := trimmedVariable(req, "partyID")
	if partyID == "" {
		return AcceptWatchPartyResult{}, clientError("partyID is required")
	}
	g, err := h.conductor.Party(partyID)
	if err != nil {
		return AcceptWatchPartyResult{}, partyClientError(err)
	}
	// The Reveal intersection: the accepting session must be able
	// to Reveal the party's content with ITS OWN filter. h.store.Media returns
	// "media not found" when the filter cannot see it, so a managed or unrevealed
	// session is rejected with an explicit reason — never a silent exclusion.
	content := g.Content()
	if _, err := h.store.Media(ctx, content, scope.LibraryFilter()); err != nil {
		return AcceptWatchPartyResult{}, clientError("cannot join: this party's content is not visible to your profile")
	}
	member := conductor.Member{
		SessionID:     scope.SessionID,
		DeviceID:      scope.DeviceID,
		ClockOffset:   time.Duration(int64Var(req, "clockOffsetMS")) * time.Millisecond,
		RenderLatency: time.Duration(int64Var(req, "renderLatencyMS")) * time.Millisecond,
	}
	if err := h.conductor.Accept(partyID, scope.ProfileID, member); err != nil {
		return AcceptWatchPartyResult{}, partyClientError(err)
	}
	// Late-join sync (§2.5 step 5): hand back the live anchor so the joiner lands
	// on the canonical position.
	anchor, err := h.conductor.PartyAnchor(partyID)
	if err != nil {
		return AcceptWatchPartyResult{}, partyClientError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "watch-party", partyID, ""); err != nil {
		return AcceptWatchPartyResult{}, err
	}
	// Join-time capacity awareness: if the server cannot
	// admit a normal transcode for this member's session on the party content
	// right now, a transcode-needing client will be degraded to the lowest rung
	// (it still joins — the party stays whole). Advisory; a direct-playing client
	// is unaffected. The authoritative per-member decision is its own playbackInfo.
	degraded := h.transcodeAdmission != nil &&
		!h.transcodeAdmission.CanAdmitSession(scope.SessionID, content)
	return AcceptWatchPartyResult{
		PartyID:     partyID,
		Item:        string(anchor.Item),
		PositionMS:  anchor.Position.Milliseconds(),
		PresentAtMS: anchor.PresentAt.UnixMilli(),
		Degraded:    degraded,
	}, nil
}

// watchPartyTransport is the HOST's authoritative play/pause/seek/skip (ADR-0038
// §2.4): only the host session may issue it, and it broadcasts the resulting
// {item, position, present_at} transition to every participant over the existing
// SSE outbox (the AV-4 broadcastTransition path). A non-host caller is rejected
// (ErrNotHost → "not the party host"); non-hosts use requestWatchPartyTransport
// instead. item/position are what the host's client declares (queue composition
// stays plugin-owned), so pause/resume/seek/skip all funnel through here.
func (h *Handler) watchPartyTransport(ctx context.Context, scope auth.Scope, req request) (GroupTransitionResult, error) {
	if h.conductor == nil {
		return GroupTransitionResult{}, clientError("group conductor unavailable")
	}
	if scope.SessionID == "" {
		return GroupTransitionResult{}, clientError("a session is required to control a watch party")
	}
	partyID := trimmedVariable(req, "partyID")
	if partyID == "" {
		return GroupTransitionResult{}, clientError("partyID is required")
	}
	item := store.MediaID(trimmedVariable(req, "item"))
	if item == "" {
		return GroupTransitionResult{}, clientError("item is required")
	}
	position := time.Duration(int64Var(req, "positionMS")) * time.Millisecond
	transition, _, err := h.conductor.HostTransition(partyID, scope.SessionID, item, position)
	if err != nil {
		return GroupTransitionResult{}, partyClientError(err)
	}
	if err := h.broadcastTransition(ctx, partyID, scope.ProfileID, transition); err != nil {
		return GroupTransitionResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "watch-party", partyID, ""); err != nil {
		return GroupTransitionResult{}, err
	}
	return GroupTransitionResult{
		GroupID:     partyID,
		Item:        string(transition.Item),
		PositionMS:  transition.Position.Milliseconds(),
		PresentAtMS: transition.PresentAt.UnixMilli(),
	}, nil
}

// RequestTransportResult tells a non-host participant what happened to its
// seek/skip request. Approved is true in a relaxed party — the
// request was auto-approved and applied authoritatively (the transition was
// broadcast). In a non-relaxed party Approved is false: the request was surfaced
// to the host (a host notification) for approval, and the timeline was NOT
// mutated.
type RequestTransportResult struct {
	PartyID  string `json:"partyID"`
	Approved bool   `json:"approved"`
}

// requestWatchPartyTransport is a NON-host participant's seek/skip request.
// It never mutates the timeline directly. In a relaxed party
// (household auto-approve) the request is applied on the host's behalf — the
// resulting transition is broadcast authoritatively, Approved=true. Otherwise it
// is surfaced to the host for approval via an in-app notification, Approved=false
// and nothing is mutated. The caller must be a party member and must not be the
// host (the host uses watchPartyTransport).
func (h *Handler) requestWatchPartyTransport(ctx context.Context, scope auth.Scope, req request) (RequestTransportResult, error) {
	if h.conductor == nil {
		return RequestTransportResult{}, clientError("group conductor unavailable")
	}
	if scope.SessionID == "" {
		return RequestTransportResult{}, clientError("a session is required to request a watch party seek")
	}
	partyID := trimmedVariable(req, "partyID")
	if partyID == "" {
		return RequestTransportResult{}, clientError("partyID is required")
	}
	item := store.MediaID(trimmedVariable(req, "item"))
	if item == "" {
		return RequestTransportResult{}, clientError("item is required")
	}
	position := time.Duration(int64Var(req, "positionMS")) * time.Millisecond
	decision, err := h.conductor.RequestTransport(partyID, scope.SessionID)
	if err != nil {
		return RequestTransportResult{}, partyClientError(err)
	}
	if !decision.Authoritative {
		// Surface the request to the host: an in-app notification
		// to the host's Profile, addressed by the party id. The timeline is NOT
		// mutated — the host approves or ignores.
		if h.store != nil {
			host, _ := h.partyHostProfile(partyID)
			if host != "" {
				if err := h.store.CreateNotification(ctx, store.Notification{
					ID:                 newPartyNotificationID(),
					RecipientProfileID: host,
					Kind:               store.NotificationWatchPartyInvite,
					Title:              "Watch Party seek request",
					Ref:                partyID,
					CreatedAt:          time.Now().UTC(),
				}); err != nil {
					return RequestTransportResult{}, err
				}
			}
		}
		return RequestTransportResult{PartyID: partyID, Approved: false}, nil
	}
	// Relaxed party: auto-approve by applying the seek on the host's behalf and
	// broadcasting the authoritative transition (ADR-0038 §2.4 household mode).
	transition, _, err := h.conductor.HostTransition(partyID, decision.Host, item, position)
	if err != nil {
		return RequestTransportResult{}, partyClientError(err)
	}
	if err := h.broadcastTransition(ctx, partyID, scope.ProfileID, transition); err != nil {
		return RequestTransportResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "watch-party", partyID, ""); err != nil {
		return RequestTransportResult{}, err
	}
	return RequestTransportResult{PartyID: partyID, Approved: true}, nil
}

// WatchPartyAutoSkipResult tells a party member what happened to its client-side
// auto-skip under the host-authoritative party timeline (ADR-0038
// §2.8 precedence). Applied is true when the skip moved the SHARED timeline — the
// host's own auto-skip, or a non-host's in a relaxed party, applied as an
// authoritative broadcast (Item/PositionMS/PresentAtMS describe that transition).
// Applied is false when the skip was SUPPRESSED — a non-host in a strict party:
// nothing was mutated and no host notification was sent (auto-skip fires unbidden,
// so a per-segment approval prompt would spam the host); the result carries the
// live party anchor instead so the member re-syncs to the host timeline rather
// than seeking locally and diverging.
type WatchPartyAutoSkipResult struct {
	PartyID     string `json:"partyID"`
	Applied     bool   `json:"applied"`
	Item        string `json:"item"`
	PositionMS  int64  `json:"positionMS"`
	PresentAtMS int64  `json:"presentAtMS"`
}

// watchPartyAutoSkip reconciles a member's CLIENT-side auto-skip with the
// host-authoritative party timeline. Auto-skip never
// seeks locally during a party — it routes through the host transport so it can
// only ever move the SHARED timeline:
//
//   - Host (any party): authoritative — identical to a manual host skip
//     (watchPartyTransport). The server broadcasts the new position; each client
//     still seeks itself, preserving ADR-0048's "client seeks, server never
//     injects the playhead" rule.
//   - Non-host, relaxed party: applied on the host's behalf (household
//     auto-approve, §2.4), exactly like requestWatchPartyTransport's relaxed path.
//   - Non-host, strict party: suppressed. No seek, and — unlike a MANUAL non-host
//     seek (requestWatchPartyTransport) — no host notification, because auto-skip
//     fires automatically on every segment crossing. The result returns the live
//     anchor so the member re-syncs to the host's canonical position.
//
// item is the Media being watched and positionMS the skip-segment END in
// original-file time; both come from the member's client.
func (h *Handler) watchPartyAutoSkip(ctx context.Context, scope auth.Scope, req request) (WatchPartyAutoSkipResult, error) {
	if h.conductor == nil {
		return WatchPartyAutoSkipResult{}, clientError("group conductor unavailable")
	}
	if scope.SessionID == "" {
		return WatchPartyAutoSkipResult{}, clientError("a session is required to auto-skip in a watch party")
	}
	partyID := trimmedVariable(req, "partyID")
	if partyID == "" {
		return WatchPartyAutoSkipResult{}, clientError("partyID is required")
	}
	item := store.MediaID(trimmedVariable(req, "item"))
	if item == "" {
		return WatchPartyAutoSkipResult{}, clientError("item is required")
	}
	position := time.Duration(int64Var(req, "positionMS")) * time.Millisecond

	g, err := h.conductor.Party(partyID)
	if err != nil {
		return WatchPartyAutoSkipResult{}, partyClientError(err)
	}
	host, _ := g.Host()

	// apply broadcasts an authoritative transition on hostSession's behalf — the
	// host's own auto-skip, or a relaxed non-host's applied for the host. Mirrors
	// watchPartyTransport (broadcast + activity log on the timeline mutation).
	apply := func(hostSession store.SessionID) (WatchPartyAutoSkipResult, error) {
		transition, _, err := h.conductor.HostTransition(partyID, hostSession, item, position)
		if err != nil {
			return WatchPartyAutoSkipResult{}, partyClientError(err)
		}
		if err := h.broadcastTransition(ctx, partyID, scope.ProfileID, transition); err != nil {
			return WatchPartyAutoSkipResult{}, err
		}
		if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "watch-party", partyID, ""); err != nil {
			return WatchPartyAutoSkipResult{}, err
		}
		return WatchPartyAutoSkipResult{
			PartyID:     partyID,
			Applied:     true,
			Item:        string(transition.Item),
			PositionMS:  transition.Position.Milliseconds(),
			PresentAtMS: transition.PresentAt.UnixMilli(),
		}, nil
	}

	if scope.SessionID == host {
		return apply(host)
	}

	// Non-host: RequestTransport classifies under the party's relaxed setting and
	// enforces membership (ErrNotMember) — it never mutates the timeline itself.
	decision, err := h.conductor.RequestTransport(partyID, scope.SessionID)
	if err != nil {
		return WatchPartyAutoSkipResult{}, partyClientError(err)
	}
	if decision.Authoritative {
		return apply(decision.Host)
	}

	// Strict party: suppress. Nothing is mutated and no host
	// notification is sent; hand back the live anchor so the member re-syncs to the
	// host timeline instead of seeking locally and diverging.
	anchor, err := h.conductor.PartyAnchor(partyID)
	if err != nil {
		return WatchPartyAutoSkipResult{}, partyClientError(err)
	}
	return WatchPartyAutoSkipResult{
		PartyID:     partyID,
		Applied:     false,
		Item:        string(anchor.Item),
		PositionMS:  anchor.Position.Milliseconds(),
		PresentAtMS: anchor.PresentAt.UnixMilli(),
	}, nil
}

// partyHostProfile resolves the Profile that owns the party's host session, used
// to address a host-approval notification. The party's ProfileID is the host's
// Profile (set at FormParty).
func (h *Handler) partyHostProfile(partyID string) (store.ProfileID, error) {
	g, err := h.conductor.Party(partyID)
	if err != nil {
		return "", err
	}
	return g.ProfileID, nil
}

// partyClientError maps the Registry's party errors to client-facing errors. A
// LeaveWatchPartyResult echoes the party id the caller left (ADR-0111 revocation
// propagation). Membership is removed synchronously; the caller's data disappears
// from all party views as soon as the response returns.
type LeaveWatchPartyResult struct {
	PartyID string `json:"partyID"`
}

// leaveWatchParty removes the caller's session from a Watch Party (ADR-0111 §4
// revocation propagation). A participant whose Reveal grant or membership is
// revoked calls this path; the surface client should call it when it receives a
// revocation event so the participant is removed within a bounded interval. The
// party dissolves when the last member leaves. Unlike leaveGroup, this path
// does NOT assert same-Profile ownership — a Watch Party is cross-Profile by
// design, so a guest can leave without knowing the host's Profile.
func (h *Handler) leaveWatchParty(ctx context.Context, scope auth.Scope, req request) (LeaveWatchPartyResult, error) {
	if h.conductor == nil {
		return LeaveWatchPartyResult{}, clientError("group conductor unavailable")
	}
	if scope.SessionID == "" {
		return LeaveWatchPartyResult{}, clientError("a session is required to leave a watch party")
	}
	partyID := trimmedVariable(req, "partyID")
	if partyID == "" {
		return LeaveWatchPartyResult{}, clientError("partyID is required")
	}
	if err := h.conductor.LeaveParty(partyID, scope.SessionID); err != nil {
		return LeaveWatchPartyResult{}, partyClientError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "watch-party", partyID, ""); err != nil {
		return LeaveWatchPartyResult{}, err
	}
	return LeaveWatchPartyResult{PartyID: partyID}, nil
}

// missing party and a non-party group both collapse to "unknown party" so the
// surface never reveals whether a party id exists (the non-oracle rule AV-4
// inherits from §7); ErrNoInvite collapses to "unknown invite" for the same
// reason; ErrNotHost and ErrNotMember surface their honest meaning.
func partyClientError(err error) error {
	switch {
	case errors.Is(err, conductor.ErrNoGroup), errors.Is(err, conductor.ErrNotParty):
		return clientError("unknown party")
	case errors.Is(err, conductor.ErrNoInvite):
		return clientError("unknown invite")
	case errors.Is(err, conductor.ErrNotHost):
		return clientError("not the party host")
	case errors.Is(err, conductor.ErrNotMember):
		return clientError("not a member of this party")
	default:
		return err
	}
}

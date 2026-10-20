package graph

import (
	"context"
	"errors"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/conductor"
	"github.com/beamfall/core/internal/store"
)

// Ambient Group Conductor, graph half. The session-authenticated
// surface a device uses to form/join/leave an Ambient Group and to drive its
// shared, beat-accurate audio timeline. It builds DIRECTLY on the AV-3 substrate:
//
//   - Transport intent rides the SAME /api/events SSE outbox AV-3 reuses — the
//     Conductor's {item, position, present_at} transition and per-member rate
//     nudge are appended via store.AppendGroupSyncEvent and delivered by the same
//     eventStream/eventPermitted path (no second transport, ADR-0035 §8 "The
//     session event channel is reused").
//   - Same-Profile is the AV-3 rule, inherited: every resolver gates on
//     scope.ProfileID, and the in-memory Registry asserts the group's Profile
//     matches the caller's, collapsing a missing/cross-Profile group into one
//     "unknown group" client error so the surface is never a cross-Profile
//     existence oracle.
//
// LAN-only is enforced UPSTREAM of these resolvers: a device may only obtain the
// clock offset + render latency it joins with from the LAN-gated calibration
// endpoint (server.groupCalibrate, which refuses a non-LAN request), so a group
// can only be assembled from LAN-calibrated members. Group MEMBERSHIP itself is
// ephemeral and in-memory (the conductor.Registry) — never persisted, no
// migration, reassembled on reconnect, exactly like the Ambient Queue.

// int64Var reads a numeric variable as int64 (JSON numbers decode as float64).
// The Conductor's wire durations/instants are server-clock milliseconds, which
// exceed int range on 32-bit, so they ride an int64 rather than intVar's int.
func int64Var(req request, name string) int64 {
	switch value := req.Variables[name].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

// GroupMemberInfo is one member of an Ambient Group on the wire (ADR-0035 §8:
// the Ambient Remote surfaces membership). It reports the per-member calibration
// the Conductor holds — clock offset and render latency in milliseconds — so the
// remote can surface the group's sync state.
type GroupMemberInfo struct {
	SessionID       string `json:"sessionID"`
	DeviceID        string `json:"deviceID"`
	ClockOffsetMS   int64  `json:"clockOffsetMS"`
	RenderLatencyMS int64  `json:"renderLatencyMS"`
}

// GroupInfo is the wire shape of an Ambient Group: its ephemeral id and current
// membership. It is the result of forming, joining, and the
// membership read.
type GroupInfo struct {
	GroupID string            `json:"groupID"`
	Members []GroupMemberInfo `json:"members"`
}

func groupInfo(g *conductor.Group) GroupInfo {
	members := g.Members()
	out := make([]GroupMemberInfo, len(members))
	for i, m := range members {
		out[i] = GroupMemberInfo{
			SessionID:       string(m.SessionID),
			DeviceID:        string(m.DeviceID),
			ClockOffsetMS:   m.ClockOffset.Milliseconds(),
			RenderLatencyMS: m.RenderLatency.Milliseconds(),
		}
	}
	return GroupInfo{GroupID: g.ID, Members: out}
}

// formGroup creates a new empty Ambient Group owned by the caller's Profile
// (ADR-0035 §8 "Group formation": the user explicitly forms a group from the
// Ambient Remote). The forming device then joins via joinGroup like any member.
// A caller with no Profile (admin token / unbound scoped key) cannot form a
// group — there is no Profile to own it, and the Conductor is strictly
// Profile-scoped.
func (h *Handler) formGroup(ctx context.Context, scope auth.Scope) (GroupInfo, error) {
	if h.conductor == nil {
		return GroupInfo{}, clientError("group conductor unavailable")
	}
	if scope.ProfileID == "" {
		return GroupInfo{}, clientError("a profile is required to form a group")
	}
	g := h.conductor.Form(scope.ProfileID)
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "group", g.ID, ""); err != nil {
		return GroupInfo{}, err
	}
	return groupInfo(g), nil
}

// joinGroup adds the caller's session to an existing group with its calibrated
// clock offset and render latency (ADR-0035 §8 "Group membership dynamics —
// Join"). The offset+latency must come from a prior LAN calibration handshake
// (server.groupCalibrate); the resolver records them and immediately issues a
// transition anchored to the group's current position so the joining device
// syncs in. Same-Profile is enforced by the Registry: a group owned by another
// Profile is reported "unknown group", never controllable. The member is keyed
// by the caller's OWN session — a device can only join itself, never enroll
// another device's session.
func (h *Handler) joinGroup(ctx context.Context, scope auth.Scope, req request) (GroupInfo, error) {
	if h.conductor == nil {
		return GroupInfo{}, clientError("group conductor unavailable")
	}
	if scope.ProfileID == "" || scope.SessionID == "" {
		return GroupInfo{}, clientError("a session in a profile is required to join a group")
	}
	groupID := trimmedVariable(req, "groupID")
	if groupID == "" {
		return GroupInfo{}, clientError("groupID is required")
	}
	member := conductor.Member{
		SessionID:     scope.SessionID,
		DeviceID:      scope.DeviceID,
		ClockOffset:   time.Duration(int64Var(req, "clockOffsetMS")) * time.Millisecond,
		RenderLatency: time.Duration(int64Var(req, "renderLatencyMS")) * time.Millisecond,
	}
	if err := h.conductor.Join(groupID, scope.ProfileID, member); err != nil {
		return GroupInfo{}, conductorClientError(err)
	}
	g, err := h.conductor.Group(groupID, scope.ProfileID)
	if err != nil {
		return GroupInfo{}, conductorClientError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "group", groupID, ""); err != nil {
		return GroupInfo{}, err
	}
	return groupInfo(g), nil
}

// LeaveGroupResult echoes the group the caller left (ADR-0035 §8 "Group
// membership dynamics — Leave"). The device reverts to independent ambient
// playback; if it was the last member the group dissolves in the Registry.
type LeaveGroupResult struct {
	GroupID string `json:"groupID"`
}

// leaveGroup removes the caller's session from a group. A device
// can only remove its OWN session. Leaving a group the caller is not a member of
// is a client error, so the remote learns it was already out rather than seeing
// a false success.
func (h *Handler) leaveGroup(ctx context.Context, scope auth.Scope, req request) (LeaveGroupResult, error) {
	if h.conductor == nil {
		return LeaveGroupResult{}, clientError("group conductor unavailable")
	}
	if scope.ProfileID == "" || scope.SessionID == "" {
		return LeaveGroupResult{}, clientError("a session in a profile is required to leave a group")
	}
	groupID := trimmedVariable(req, "groupID")
	if groupID == "" {
		return LeaveGroupResult{}, clientError("groupID is required")
	}
	if err := h.conductor.Leave(groupID, scope.ProfileID, scope.SessionID); err != nil {
		return LeaveGroupResult{}, conductorClientError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "group", groupID, ""); err != nil {
		return LeaveGroupResult{}, err
	}
	return LeaveGroupResult{GroupID: groupID}, nil
}

// GroupTransitionResult echoes the group and the present instant the transition
// was scheduled for (server-clock Unix ms), so the issuing remote can surface
// "the group changes track in ~2 s" (ADR-0035 §8 "Transition protocol").
type GroupTransitionResult struct {
	GroupID     string `json:"groupID"`
	Item        string `json:"item"`
	PositionMS  int64  `json:"positionMS"`
	PresentAtMS int64  `json:"presentAtMS"`
}

// transitionGroup advances the group's shared queue to a new item+position and
// broadcasts the {item, position, present_at} transition to every member over
// the existing SSE outbox (ADR-0035 §8 "Transition protocol"). This is the one
// path for skip, natural track end, and pause/resume — they all re-anchor the
// group the same way; the caller passes the item/position the active plugin
// declares (queue composition stays plugin-owned). The caller must be a member
// of the group it transitions — a non-member cannot drive a group's timeline.
func (h *Handler) transitionGroup(ctx context.Context, scope auth.Scope, req request) (GroupTransitionResult, error) {
	if h.conductor == nil {
		return GroupTransitionResult{}, clientError("group conductor unavailable")
	}
	if scope.ProfileID == "" || scope.SessionID == "" {
		return GroupTransitionResult{}, clientError("a session in a profile is required to transition a group")
	}
	groupID := trimmedVariable(req, "groupID")
	if groupID == "" {
		return GroupTransitionResult{}, clientError("groupID is required")
	}
	item := store.MediaID(trimmedVariable(req, "item"))
	if item == "" {
		return GroupTransitionResult{}, clientError("item is required")
	}
	// Only a member may drive its group's timeline.
	if _, err := h.conductor.Group(groupID, scope.ProfileID); err != nil {
		return GroupTransitionResult{}, conductorClientError(err)
	}
	position := time.Duration(int64Var(req, "positionMS")) * time.Millisecond
	transition, _, err := h.conductor.Transition(groupID, scope.ProfileID, scope.SessionID, item, position)
	if err != nil {
		return GroupTransitionResult{}, conductorClientError(err)
	}
	if err := h.broadcastTransition(ctx, groupID, scope.ProfileID, transition); err != nil {
		return GroupTransitionResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityGroupControl, "group", groupID, ""); err != nil {
		return GroupTransitionResult{}, err
	}
	return GroupTransitionResult{
		GroupID:     groupID,
		Item:        string(transition.Item),
		PositionMS:  transition.Position.Milliseconds(),
		PresentAtMS: transition.PresentAt.UnixMilli(),
	}, nil
}

// ReportDriftResult tells the reporting member what correction the Conductor
// issued (ADR-0035 §8 "Drift correction"): "none" (within the NTP window),
// "nudge" (a ±0.1 % rate signal was published to it), or "resync" (a fresh
// transition was broadcast to re-anchor the group). For a nudge the signed Rate
// is echoed so the caller can confirm the correction it will receive on the SSE
// channel.
type ReportDriftResult struct {
	GroupID string  `json:"groupID"`
	Action  string  `json:"action"`
	Rate    float64 `json:"rate,omitempty"`
}

// reportDrift accepts a member's measured drift (its position error vs the
// canonical timeline, in ms; positive == ahead, negative == behind) and issues
// the Conductor's correction. A small drift produces a per-member
// ±0.1 % rate nudge published to the reporting session; a drift beyond the
// recoverable threshold (> 500 ms) produces a full re-sync — a fresh transition
// broadcast that re-anchors the group. The caller must be a member of the group.
func (h *Handler) reportDrift(ctx context.Context, scope auth.Scope, req request) (ReportDriftResult, error) {
	if h.conductor == nil {
		return ReportDriftResult{}, clientError("group conductor unavailable")
	}
	if scope.ProfileID == "" || scope.SessionID == "" {
		return ReportDriftResult{}, clientError("a session in a profile is required to report drift")
	}
	groupID := trimmedVariable(req, "groupID")
	if groupID == "" {
		return ReportDriftResult{}, clientError("groupID is required")
	}
	drift := time.Duration(int64Var(req, "driftMS")) * time.Millisecond
	// The member snapshot ReportDrift returns is unused here: a re-sync broadcasts
	// one transition the Profile gate fans out to every same-Profile subscriber on
	// the SSE channel, never a per-member fan-out — so the snapshot is discarded.
	decision, resync, err := h.conductor.ReportDrift(groupID, scope.ProfileID, scope.SessionID, drift)
	if err != nil {
		return ReportDriftResult{}, conductorClientError(err)
	}
	switch decision.Action {
	case conductor.DriftResync:
		// Full re-sync: re-anchor the group with a fresh transition broadcast.
		if resync != nil {
			if err := h.broadcastTransition(ctx, groupID, scope.ProfileID, *resync); err != nil {
				return ReportDriftResult{}, err
			}
		}
		return ReportDriftResult{GroupID: groupID, Action: "resync"}, nil
	case conductor.DriftNudge:
		// Per-member micro-correction: publish a rate nudge addressed to the
		// reporting session over the SSE channel.
		if err := h.store.AppendGroupSyncEvent(ctx, store.GroupSyncPayload{
			GroupID:   groupID,
			ProfileID: scope.ProfileID,
			Signal:    store.GroupSignalNudge,
			Session:   scope.SessionID,
			Rate:      decision.Rate,
		}, time.Now().UTC()); err != nil {
			return ReportDriftResult{}, err
		}
		return ReportDriftResult{GroupID: groupID, Action: "nudge", Rate: decision.Rate}, nil
	default:
		return ReportDriftResult{GroupID: groupID, Action: "none"}, nil
	}
}

// broadcastTransition appends a {item, position, present_at} transition to the
// SSE outbox for delivery to every same-Profile member. The Profile
// gate on the outbox event is the delivery authorization; the present instant is
// encoded as server-clock Unix ms so each member pairs it with its calibrated
// offset and releases at present − own_render_latency.
func (h *Handler) broadcastTransition(ctx context.Context, groupID string, profile store.ProfileID, t conductor.Transition) error {
	return h.store.AppendGroupSyncEvent(ctx, store.GroupSyncPayload{
		GroupID:     groupID,
		ProfileID:   profile,
		Signal:      store.GroupSignalTransition,
		Item:        t.Item,
		PositionMS:  t.Position.Milliseconds(),
		PresentAtMS: t.PresentAt.UnixMilli(),
	}, time.Now().UTC())
}

// conductorClientError maps the Registry's typed errors to a client-facing
// error, collapsing "no such group" and "wrong profile" into ONE "unknown group"
// message so the surface never reveals whether a group id belongs to another
// Profile (the §7 same-Profile non-oracle rule, inherited by §8).
func conductorClientError(err error) error {
	switch {
	case errors.Is(err, conductor.ErrNoGroup), errors.Is(err, conductor.ErrWrongProfile):
		return clientError("unknown group")
	case errors.Is(err, conductor.ErrNotMember):
		return clientError("not a member of this group")
	default:
		return err
	}
}

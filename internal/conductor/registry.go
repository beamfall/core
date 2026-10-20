package conductor

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

// Registry is the in-memory home of every live Ambient Group. It
// is the single owner of group state for the process; nothing here is persisted,
// so a restart dissolves every group and members re-join from scratch — the
// ephemeral, reassembled-on-reconnect model the Ambient Queue established.
// There is no schema, no migration, and no durable key.
//
// The Registry is safe for concurrent use: its own mutex guards the group map,
// and each Group guards its own membership/anchor. Registry.mu may nest
// Group.mu; Group.mu must never acquire Registry.mu.
type Registry struct {
	now func() time.Time

	mu       sync.RWMutex
	groups   map[string]*Group
	formedAt map[string]time.Time

	// Test-only synchronization hooks keep the lock-order regression deterministic.
	acceptMapLocked          func()
	reapMapLockAttempting    func()
	reportDriftChecked       func()
	leaveGroupLockAttempting func()
}

// groupFormTTL bounds how long a formed group may sit with no member before
// reapAbandoned frees it. Formation and the first Join are meant to be one
// code path (see Form), so this is a generous grace window for that round
// trip — not a normal-case timer.
const groupFormTTL = 5 * time.Minute

// NewRegistry returns an empty Registry with the real wall clock. now is
// injectable in tests so the NTP anchor and present_at math is deterministic.
func NewRegistry() *Registry {
	return NewRegistryWithClock(time.Now)
}

// NewRegistryWithClock returns an empty Registry driven by the supplied clock.
func NewRegistryWithClock(now func() time.Time) *Registry {
	return &Registry{now: now, groups: map[string]*Group{}, formedAt: map[string]time.Time{}}
}

// newGroupID mints an ephemeral 128-bit group id. It is not a durable key — the
// id lives only as long as the in-memory group — so a crypto-random hex string
// is sufficient and collision-free in practice.
func newGroupID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Form creates a new empty group owned by profile and returns it (ADR-0035 §8
// "Group formation": the user explicitly forms a group from the Ambient Remote).
// The group starts with no members and no anchor; the forming device joins via
// Join like any other member, so formation and the first join stay one code path.
func (r *Registry) Form(profile store.ProfileID) *Group {
	now := r.now()
	r.reapAbandoned(now)
	g := &Group{
		ID:        newGroupID(),
		ProfileID: profile,
		members:   map[store.SessionID]*Member{},
	}
	r.mu.Lock()
	r.groups[g.ID] = g
	r.formedAt[g.ID] = now
	r.mu.Unlock()
	return g
}

// reapAbandoned frees every group that is still memberless once groupFormTTL
// has elapsed since Form created it — the forming device may drop its SSE
// connection before ever calling Join, and otherwise only Leave (on the last
// member) ever deletes a group, so an abandoned formation would pin memory
// forever. Called opportunistically from Form so no background reaper or
// session-teardown hook is needed. Each candidate is revalidated and deleted
// under Registry.mu then Group.mu, so Join cannot land between the emptiness
// check and deletion.
func (r *Registry) reapAbandoned(now time.Time) {
	// The formation timestamp doubles as a compare-and-delete token: Join removes
	// it the moment a member arrives, so it must survive until the final locked
	// pass. Dropping it here instead would let a Join that lands while the
	// emptiness check runs off-lock have its group deleted out from under it.
	type candidate struct {
		id       string
		group    *Group
		formedAt time.Time
	}
	if r.reapMapLockAttempting != nil {
		r.reapMapLockAttempting()
	}
	r.mu.RLock()
	var stale []candidate
	for id, formedAt := range r.formedAt {
		if now.Sub(formedAt) < groupFormTTL {
			continue
		}
		stale = append(stale, candidate{id: id, group: r.groups[id], formedAt: formedAt})
	}
	r.mu.RUnlock()
	if len(stale) == 0 {
		return
	}

	for _, c := range stale {
		r.mu.Lock()
		formedAt, ok := r.formedAt[c.id]
		if !ok || !formedAt.Equal(c.formedAt) || r.groups[c.id] != c.group {
			r.mu.Unlock()
			continue
		}
		if c.group == nil {
			delete(r.formedAt, c.id)
			r.mu.Unlock()
			continue
		}
		c.group.mu.Lock()
		if len(c.group.members) == 0 {
			delete(r.groups, c.id)
			delete(r.formedAt, c.id)
		}
		c.group.mu.Unlock()
		r.mu.Unlock()
	}
}

// lookup resolves a group by id and asserts the caller's Profile owns it. A
// missing group and a cross-Profile group are reported as ErrNoGroup and
// ErrWrongProfile respectively; the surface collapses both into one "unknown
// group" response so the Registry is never a cross-Profile existence oracle.
func (r *Registry) lookup(id string, profile store.ProfileID) (*Group, error) {
	r.mu.RLock()
	g := r.groups[id]
	r.mu.RUnlock()
	if g == nil {
		return nil, ErrNoGroup
	}
	if g.ProfileID != profile {
		return nil, ErrWrongProfile
	}
	return g, nil
}

// Group returns the group for id if profile owns it (the Ambient Remote's
// membership read). Same fail-closed semantics as lookup.
func (r *Registry) Group(id string, profile store.ProfileID) (*Group, error) {
	return r.lookup(id, profile)
}

// Join adds (or re-adds) a calibrated member to the group (ADR-0035 §8 "Group
// membership dynamics — Join"). The member supplies its calibrated clock offset
// and render latency; the Conductor records them and the caller then issues a
// transition anchored to the current group position so the device syncs in.
// Re-joining the same session updates its calibration in place (a device that
// re-calibrates mid-session refines its numbers without leaving the group).
// profile must own the group; a cross-Profile join is rejected fail-closed.
func (r *Registry) Join(id string, profile store.ProfileID, m Member) error {
	r.mu.Lock()
	g := r.groups[id]
	if g == nil {
		r.mu.Unlock()
		return ErrNoGroup
	}
	if g.ProfileID != profile {
		r.mu.Unlock()
		return ErrWrongProfile
	}
	g.mu.Lock()
	member := m
	g.members[m.SessionID] = &member
	// The group now has a member, so it is no longer a Form-then-never-join
	// candidate for reapAbandoned; drop its formation timestamp.
	delete(r.formedAt, id)
	g.mu.Unlock()
	r.mu.Unlock()
	return nil
}

// Leave removes a member from the group (ADR-0035 §8 "Group membership dynamics
// — Leave": the device reverts to independent ambient playback). When the LAST
// member leaves, the group dissolves: the remaining device (if any) continues as
// a solo Ambient Display session, and an empty group is removed from the Registry
// so it cannot be re-joined or leak. Leaving a session that is not a member is a
// no-op error (ErrNotMember) so the surface can report it honestly.
func (r *Registry) Leave(id string, profile store.ProfileID, session store.SessionID) error {
	g, err := r.lookup(id, profile)
	if err != nil {
		return err
	}
	if r.leaveGroupLockAttempting != nil {
		r.leaveGroupLockAttempting()
	}
	g.mu.Lock()
	if _, ok := g.members[session]; !ok {
		g.mu.Unlock()
		return ErrNotMember
	}
	delete(g.members, session)
	empty := len(g.members) == 0
	g.mu.Unlock()
	if empty {
		r.mu.Lock()
		delete(r.groups, id)
		delete(r.formedAt, id)
		r.mu.Unlock()
	}
	return nil
}

// Transition advances the group's shared queue to item at position and anchors
// it (ADR-0035 §8 "Transition protocol"). It records the new {item, position}
// against an NTP anchor of now, picks present_at = now + PreBufferWindow, and
// returns the Transition the surface broadcasts to every member over the SSE
// outbox. This is the single path for skip, natural track end, pause/resume, and
// a full drift re-sync — they all re-anchor the group the same way; the only
// difference is what item/position they pass.
//
// The returned member list is a snapshot of who should receive the broadcast at
// the moment the anchor was set, so the caller fans the transition out to the
// exact membership the position was anchored against.
func (r *Registry) Transition(id string, profile store.ProfileID, session store.SessionID, item store.MediaID, position time.Duration) (Transition, []Member, error) {
	g, err := r.lookup(id, profile)
	if err != nil {
		return Transition{}, nil, err
	}
	g.mu.Lock()
	if _, ok := g.members[session]; !ok {
		g.mu.Unlock()
		return Transition{}, nil, ErrNotMember
	}
	now := r.now()
	g.item = item
	g.position = position
	g.anchor = now
	members := make([]Member, 0, len(g.members))
	for _, m := range g.members {
		members = append(members, *m)
	}
	g.mu.Unlock()
	return Transition{
		Item:      item,
		Position:  position,
		PresentAt: now.Add(PreBufferWindow),
	}, members, nil
}

// ExpectedPosition is the canonical position the group's current item should be
// at on the server clock at serverTime, derived from the anchor (ADR-0035 §8
// "Drift correction": the server computes the expected position for each member
// at its reported NTP time). A member compares its own reported position against
// this to compute its drift, which ClassifyDrift then maps to an action.
func (g *Group) ExpectedPosition(serverTime time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.position + serverTime.Sub(g.anchor)
}

// ReportDrift classifies a member's reported drift and, when the action is a
// full re-sync, returns a fresh transition re-anchored to the current group
// state so the member can re-join the timeline (ADR-0035 §8: "re-syncs via a
// full transition-protocol cycle"). For a nudge or no-op it returns the decision
// alone (the rate signal, if any, is the whole correction). drift is the
// member's position error (positive == ahead of canonical, negative == behind).
//
// A resync re-anchors the GROUP (advancing its anchor to now), so every member's
// expected position stays coherent after the re-sync broadcast — which the
// surface delivers to every same-Profile member over the Profile-gated SSE
// channel, not a per-member fan-out, so no member list is returned.
func (r *Registry) ReportDrift(id string, profile store.ProfileID, session store.SessionID, drift time.Duration) (DriftDecision, *Transition, error) {
	g, err := r.lookup(id, profile)
	if err != nil {
		return DriftDecision{}, nil, err
	}
	g.mu.Lock()
	if _, ok := g.members[session]; !ok {
		g.mu.Unlock()
		return DriftDecision{}, nil, ErrNotMember
	}
	if r.reportDriftChecked != nil {
		r.reportDriftChecked()
	}

	decision := ClassifyDrift(drift)
	if decision.Action != DriftResync {
		g.mu.Unlock()
		return decision, nil, nil
	}
	// Full re-sync: re-anchor the group to its current expected position at now
	// and hand back a fresh transition the surface broadcasts. The expected
	// position is the canonical timeline value (anchor + elapsed), so the resync
	// puts the group back on the line it was already on, not back to where it
	// started.
	now := r.now()
	g.position = g.position + now.Sub(g.anchor)
	g.anchor = now
	item := g.item
	position := g.position
	g.mu.Unlock()
	t := Transition{Item: item, Position: position, PresentAt: now.Add(PreBufferWindow)}
	return decision, &t, nil
}

// GroupCount reports how many groups are currently live. It exists for tests and
// the ops surface to assert the ephemeral invariant — a fresh Registry has zero
// groups and no persisted state to load.
func (r *Registry) GroupCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.groups)
}

// SessionInWatchPartyForMedia reports whether session is a current member of a
// Watch Party whose content is mediaID. It is the authoritative,
// server-side membership check playbackInfo consults to decide whether a
// transcode blocked by the concurrent-transcode cap should DEGRADE to the lowest
// rung (ADR-0010 §6 "drop to this quality"; PANEL-16) instead of being refused —
// a party member is kept streaming at reduced quality rather than fractured out
// of the party. Membership lives only here, so a client cannot spoof it to
// escape the cap. It snapshots the group set under the map lock, releases it,
// then reads each group through its own accessors after releasing Registry.mu.
func (r *Registry) SessionInWatchPartyForMedia(session store.SessionID, mediaID store.MediaID) bool {
	r.mu.RLock()
	groups := make([]*Group, 0, len(r.groups))
	for _, g := range r.groups {
		groups = append(groups, g)
	}
	r.mu.RUnlock()
	for _, g := range groups {
		if !g.IsWatchParty() || g.Content() != mediaID {
			continue
		}
		for _, m := range g.Members() {
			if m.SessionID == session {
				return true
			}
		}
	}
	return false
}

package conductor

// Watch Party: a Group Conductor session with a full-audio-video
// posture and a host-role transport axis. A Watch Party reuses the entire AV-4
// Group machinery unchanged — NTP anchor, per-member latency calibration, the
// {item, position, present_at} transition broadcast over the existing SSE
// outbox, and rate-adjustment drift correction (ADR-0038 §2.2: "all existing
// Group Conductor machinery is inherited unchanged"). It adds exactly three
// things on top of an Ambient Group:
//
//   - A host role. The host's play/pause/seek/skip are
//     authoritative timeline mutations broadcast to every participant. A
//     non-host transport is a REQUEST surfaced to the host (or auto-approved
//     under the relaxed setting), never an authoritative mutation.
//   - A full-AV posture: the party advances under host control
//     rather than auto-advancing like an Ambient Group.
//   - Cross-Profile membership via invite + accept. An Ambient
//     Group is strictly same-Profile (the Registry's lookup asserts it); a Watch
//     Party is explicitly cross-Profile — a host invites OTHER Profiles — so the
//     authorization model is invite-acceptance + Reveal-intersection (checked at
//     the surface against each accepting session's own filter), NOT same-Profile.
//
// A Watch Party is EPHEMERAL exactly like an Ambient Group: it lives only in the
// in-memory Registry, has no schema and no migration, and is reassembled on
// reconnect. Party + invite state is held here in-memory; a process restart
// dissolves every party and participants re-form/re-accept from scratch.

import (
	"errors"
	"time"

	"github.com/beamfall/core/internal/store"
)

var (
	// ErrNotParty reports that the group id resolves to an Ambient Group, not a
	// Watch Party — the host-role transport and invite paths are party-only.
	ErrNotParty = errors.New("conductor: group is not a watch party")
	// ErrNotHost reports that the caller's session is not the party host, so it
	// cannot issue an authoritative transport command.
	ErrNotHost = errors.New("conductor: session is not the party host")
	// ErrNoInvite reports that no pending invite exists for the accepting Profile
	// in the party. Collapsed at the surface into "unknown invite" so the party
	// is never a cross-Profile existence oracle.
	ErrNoInvite = errors.New("conductor: no pending invite for this profile")
)

// Posture distinguishes an Ambient Group (auto-advancing, muted-video + audio)
// from a Watch Party (host-controlled, full audio + video) — ADR-0038 §2.2.
type Posture int

const (
	// PostureAmbient is the AV-4 default: the group auto-advances, no host.
	PostureAmbient Posture = iota
	// PostureWatchParty is the full-AV, host-controlled posture.
	PostureWatchParty
)

// Invite is one pending Watch Party invitation addressed to a Profile by the
// host. It is keyed by invitee Profile, holds nothing the
// accepting session does not already prove via its own scope, and is consumed on
// accept. Reveal eligibility is NOT decided here — it is checked at the surface
// against the accepting session's own LibraryFilter, so the
// invite carries only the addressing, never an authorization grant.
type Invite struct {
	// Invitee is the Profile the host named (by display name; the surface resolves
	// the name to this id before recording the invite).
	Invitee store.ProfileID
}

// IsWatchParty reports whether the group is a Watch Party (host-controlled,
// full-AV) rather than an Ambient Group. Safe to call concurrently.
func (g *Group) IsWatchParty() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.posture == PostureWatchParty
}

// Host returns the party's host session and true when the group is a Watch
// Party; the zero session and false for an Ambient Group.
func (g *Group) Host() (store.SessionID, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.posture != PostureWatchParty {
		return "", false
	}
	return g.host, true
}

// Content returns the Media the party is watching (ADR-0038 §2.3: the party's
// content is fixed at formation and is the anchor for the Reveal intersection).
func (g *Group) Content() store.MediaID {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.content
}

// Relaxed reports whether non-host seek/skip requests are auto-approved without
// host confirmation (ADR-0038 §2.4 "a 'relaxed' party mode for household use").
func (g *Group) Relaxed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.relaxed
}

// Invitees returns a snapshot of the Profiles with a pending invite.
func (g *Group) Invitees() []store.ProfileID {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]store.ProfileID, 0, len(g.invites))
	for p := range g.invites {
		out = append(out, p)
	}
	return out
}

// FormParty creates a Watch Party owned by host's Profile, watching content,
// hosted by the host's session. It is the party
// sibling of Form: the host's device joins via Join like any member (so
// formation and the first join stay one path), then drives the timeline as the
// authoritative host. relaxed sets the household auto-approve setting (§2.4).
// The party is cross-Profile by design — members are NOT gated on the owning
// Profile (see partyLookup) — so the host invites OTHER Profiles via Invite and
// they join on Accept after a per-session Reveal check at the surface.
func (r *Registry) FormParty(host store.ProfileID, hostSession store.SessionID, content store.MediaID, relaxed bool) *Group {
	now := r.now()
	r.reapAbandoned(now)
	g := &Group{
		ID:        newGroupID(),
		ProfileID: host,
		members:   map[store.SessionID]*Member{},
		posture:   PostureWatchParty,
		host:      hostSession,
		content:   content,
		relaxed:   relaxed,
		invites:   map[store.ProfileID]Invite{},
	}
	r.mu.Lock()
	r.groups[g.ID] = g
	r.formedAt[g.ID] = now
	r.mu.Unlock()
	return g
}

// partyLookup resolves a Watch Party by id WITHOUT the same-Profile assertion
// lookup applies — a party is cross-Profile by construction. It
// reports ErrNoGroup for a missing group and ErrNotParty for a group that is an
// Ambient Group (so the host-role transport and invite paths can never be aimed
// at an Ambient Group). The caller-authorization on a party is invite-acceptance
// and the host role, NOT the owning Profile, so this deliberately does not gate
// on ProfileID.
func (r *Registry) partyLookup(id string) (*Group, error) {
	r.mu.RLock()
	g := r.groups[id]
	r.mu.RUnlock()
	if g == nil {
		return nil, ErrNoGroup
	}
	if !g.IsWatchParty() {
		return nil, ErrNotParty
	}
	return g, nil
}

// Party returns the Watch Party for id (cross-Profile read for the host/member
// surface). ErrNoGroup / ErrNotParty as partyLookup.
func (r *Registry) Party(id string) (*Group, error) {
	return r.partyLookup(id)
}

// Invite records a pending invitation to invitee in the party (ADR-0038 §2.5
// step 2). Only the host may invite. The invite is pure addressing — Reveal
// eligibility is checked at Accept against the accepting session's own filter
// (§2.3), so recording an invite grants nothing. Re-inviting the same Profile is
// idempotent. A host inviting its own owning Profile is allowed (a household
// member on the host's Profile is a legitimate participant).
func (r *Registry) Invite(id string, host store.SessionID, invitee store.ProfileID) error {
	g, err := r.partyLookup(id)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.host != host {
		return ErrNotHost
	}
	g.invites[invitee] = Invite{Invitee: invitee}
	return nil
}

// Accept consumes a pending invite for profile and joins the accepting session
// as a party member (ADR-0038 §2.5 steps 3-4). The CALLER must already have
// confirmed at the surface that this session can Reveal the party's content
// (§2.3) — the Registry holds no library state, so the Reveal intersection is
// enforced by the surface against the accepting session's own LibraryFilter
// before this is called. A profile with no pending invite is rejected
// (ErrNoInvite). The invite is consumed so an accept is single-use; the member
// is keyed by its own session (a session can only join itself).
func (r *Registry) Accept(id string, profile store.ProfileID, m Member) error {
	r.mu.Lock()
	if r.acceptMapLocked != nil {
		r.acceptMapLocked()
	}
	g := r.groups[id]
	if g == nil {
		r.mu.Unlock()
		return ErrNoGroup
	}
	g.mu.Lock()
	if g.posture != PostureWatchParty {
		g.mu.Unlock()
		r.mu.Unlock()
		return ErrNotParty
	}
	if _, ok := g.invites[profile]; !ok {
		g.mu.Unlock()
		r.mu.Unlock()
		return ErrNoInvite
	}
	delete(g.invites, profile)
	member := m
	member.ProfileID = profile // ADR-0111: record profile so graph can surface display name
	g.members[m.SessionID] = &member
	delete(r.formedAt, id)
	g.mu.Unlock()
	r.mu.Unlock()
	return nil
}

// HostTransition advances the party timeline to item at position and re-anchors
// it, AUTHORITATIVELY — only the host session may call it (ADR-0038 §2.4: the
// host's play/pause/seek/skip are authoritative timeline mutations broadcast to
// all participants). It is the party analogue of Transition: same NTP-anchored
// {item, position, present_at} broadcast over the same SSE outbox, but gated on
// the host role. Pause/resume/seek/skip all funnel through here — they differ
// only in the item/position passed, exactly like the Ambient Group transition.
func (r *Registry) HostTransition(id string, host store.SessionID, item store.MediaID, position time.Duration) (Transition, []Member, error) {
	g, err := r.partyLookup(id)
	if err != nil {
		return Transition{}, nil, err
	}
	now := r.now()
	g.mu.Lock()
	if g.host != host {
		g.mu.Unlock()
		return Transition{}, nil, ErrNotHost
	}
	g.item = item
	g.position = position
	g.anchor = now
	members := make([]Member, 0, len(g.members))
	for _, mem := range g.members {
		members = append(members, *mem)
	}
	g.mu.Unlock()
	return Transition{
		Item:      item,
		Position:  position,
		PresentAt: now.Add(PreBufferWindow),
	}, members, nil
}

// TransportRequest is a non-host participant's seek/skip request (ADR-0038
// §2.4). Authoritative is true ONLY when the party is relaxed (household
// auto-approve) — the caller then applies it via HostTransition on the host's
// behalf; otherwise it is surfaced to the host as a request the host approves or
// ignores, and NOTHING is mutated. A non-member or the host itself calling this
// is a misuse the surface guards; the Registry only classifies.
type TransportRequest struct {
	// Authoritative reports whether the request auto-applies (relaxed party) or
	// is merely surfaced to the host for approval (default).
	Authoritative bool
	// Host is the party host's session, so the surface can address the request
	// notification/event to the host when it is not auto-approved.
	Host store.SessionID
}

// RequestTransport classifies a non-host participant's seek/skip request.
// It NEVER mutates the timeline: in a relaxed party it reports
// Authoritative=true so the surface may apply it on the host's behalf via
// HostTransition; otherwise it reports Authoritative=false and the request is
// surfaced to the host. session must be a party member and must NOT be the host
// (the host uses HostTransition directly). The classification only — the surface
// owns the request-vs-apply effect.
func (r *Registry) RequestTransport(id string, session store.SessionID) (TransportRequest, error) {
	g, err := r.partyLookup(id)
	if err != nil {
		return TransportRequest{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.members[session]; !ok {
		return TransportRequest{}, ErrNotMember
	}
	if session == g.host {
		// The host's own transport is authoritative via HostTransition, not a
		// request — calling the request path as the host is a surface misuse.
		return TransportRequest{}, ErrNotHost
	}
	return TransportRequest{Authoritative: g.relaxed, Host: g.host}, nil
}

// LeaveParty removes session from the Watch Party identified by id (ADR-0111
// revocation propagation). Unlike Leave, this does NOT assert same-Profile
// ownership — a Watch Party is cross-Profile by design, so any member can leave
// regardless of which Profile owns the party. The party dissolves when the last
// member leaves, exactly as Leave does for Ambient Groups. ErrNoGroup and
// ErrNotParty are reported as-is so the surface can route them to partyClientError.
func (r *Registry) LeaveParty(id string, session store.SessionID) error {
	g, err := r.partyLookup(id)
	if err != nil {
		return err
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

// PartyAnchor is the current {item, position, present_at} a late joiner syncs to
// (ADR-0038 §2.5 step 5). It is the party's live timeline state re-expressed at
// `now` so a participant joining mid-playback lands on the canonical position,
// computed the same way ExpectedPosition derives drift — anchor + elapsed.
func (r *Registry) PartyAnchor(id string) (Transition, error) {
	g, err := r.partyLookup(id)
	if err != nil {
		return Transition{}, err
	}
	now := r.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	current := g.position + now.Sub(g.anchor)
	return Transition{
		Item:      g.item,
		Position:  current,
		PresentAt: now.Add(PreBufferWindow),
	}, nil
}

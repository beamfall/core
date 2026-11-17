package conductor

import (
	"errors"
	"time"

	"github.com/beamfall/core/internal/store"
)

// Lead-locked parties (CORE-2243). A lead party is a Watch Party whose host
// session, the lead, owns the audio: it plays freely and is never corrected.
// Every other member is a follower that shows the picture only and stays locked
// to the lead. The lead reports the position it is audibly at; when that
// departs from the shared timeline the Conductor re-anchors the timeline on the
// report, so a lead that stalls or changes speed takes every follower with it.
//
//   - Only the lead moves the timeline. The party is never relaxed, and a
//     follower's transport request is refused at the surface.
//   - An invite to a lead party is not consumed by an accept, and the lead's own
//     Profile keeps its invite, so a follower that reloads joins again.
//   - The party ends when the lead leaves. The host role never passes to a
//     follower: a follower must not start playing audio.

// LeadReanchorThreshold is how far the lead's reported position may sit from
// the shared timeline before the timeline is re-anchored on the report. It is
// the follower's sync target (picture within 40 ms of the lead's audio): a
// smaller deviation is inside what a follower is allowed to be off by.
const LeadReanchorThreshold = 40 * time.Millisecond

// LeadHeartbeat is the longest a lead party goes without re-stating its
// timeline while the lead reports. A follower that missed a transition, or one
// that just opened, is on the timeline within this long.
const LeadHeartbeat = 5 * time.Second

// LeadSilence is how long a playing lead party's lead may go without a report
// or a transport before the timeline is held. The lead reports about once a
// second, so this is three missed reports.
const LeadSilence = 3 * time.Second

// leadReportMaxAge bounds how far in the past a lead report's sample instant
// may lie. An older sample says nothing about where the lead is now and is
// dropped.
const leadReportMaxAge = 5 * time.Second

// leadRestateFloor is the shortest time between two statements of a lead
// party's timeline caused by reports, so a lead cannot flood its followers.
const leadRestateFloor = 250 * time.Millisecond

// leadMaxPosition bounds a reported position.
const leadMaxPosition = 1000 * time.Hour

// Timeline rate bounds, in thousandths of normal speed.
const (
	rateNormal = 1000
	rateMin    = 250
	rateMax    = 4000
)

// ErrNotLead reports that the party is not a lead party, so it takes no lead
// reports.
var ErrNotLead = errors.New("conductor: party has no lead")

// Lead reports whether the party is a lead party. Safe to call concurrently.
func (g *Group) Lead() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lead
}

// FormLeadParty forms a lead party: a host-only Watch Party whose host session
// is the lead.
func (r *Registry) FormLeadParty(host store.ProfileID, hostSession store.SessionID, content store.MediaID) *Group {
	g := r.FormParty(host, hostSession, content, false)
	g.mu.Lock()
	g.lead = true
	g.mu.Unlock()
	return g
}

// LeadReport takes the lead's report that it was audibly at position at the
// server instant at, playing at ratePermille thousandths of normal speed (zero
// means normal). When the report sits further than LeadReanchorThreshold from
// the shared timeline, when the speed changed, or when LeadHeartbeat has passed
// since the timeline was last stated, the timeline is re-anchored on the report
// and restate is true: the surface then broadcasts the returned Transition. A
// timeline the lead paused is not moved by a report; the lead pauses and resumes
// through HostTransition. A timeline held by HoldSilentLead resumes on the
// report. A report is dropped when its sample is older than
// leadReportMaxAge, was taken before the timeline was last anchored, or follows
// the last statement within leadRestateFloor. Only the lead of a lead party may
// report.
func (r *Registry) LeadReport(id string, lead store.SessionID, position time.Duration, at time.Time, ratePermille int) (t Transition, members []Member, restate bool, err error) {
	g, err := r.partyLookup(id)
	if err != nil {
		return Transition{}, nil, false, err
	}
	now := r.now()
	if at.After(now) {
		at = now
	}
	stale := at.Before(now.Add(-leadReportMaxAge))
	position = min(max(position, 0), leadMaxPosition)
	rate := clampRate(ratePermille)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.lead {
		return Transition{}, nil, false, ErrNotLead
	}
	if g.host != lead {
		return Transition{}, nil, false, ErrNotHost
	}
	if !stale {
		g.heard = now
	}
	// A held party resumes on the lead's first current report.
	if g.held && !stale && !at.Before(g.anchor) {
		g.position, g.anchor, g.rate = position, at, rate
		g.paused, g.held = false, false
		g.stated = now
		return g.transitionLocked(now), g.membersLocked(), true, nil
	}
	// A stale sample, one taken before the timeline was last anchored (the
	// lead has since moved it, so the sample describes the old timeline), and
	// one that arrives too soon after the last statement are all dropped.
	if g.paused || stale || at.Before(g.anchor) || now.Sub(g.stated) < leadRestateFloor {
		return g.transitionLocked(now), nil, false, nil
	}
	deviation := position - g.positionAtLocked(at)
	if deviation < 0 {
		deviation = -deviation
	}
	moved := deviation > LeadReanchorThreshold || rate != g.rateLocked()
	if !moved && now.Sub(g.stated) < LeadHeartbeat {
		return g.transitionLocked(now), nil, false, nil
	}
	if moved {
		g.position = position
		g.anchor = at
		g.rate = rate
	}
	g.stated = now
	return g.transitionLocked(now), g.membersLocked(), true, nil
}

// membersLocked is a snapshot copy of the members. The caller holds g.mu.
func (g *Group) membersLocked() []Member {
	members := make([]Member, 0, len(g.members))
	for _, m := range g.members {
		members = append(members, *m)
	}
	return members
}

// HoldSilentLead holds a playing lead party whose lead has not been heard for
// LeadSilence (CORE-2266): the timeline stops where it was when the lead was
// last heard, and held is true so the surface broadcasts the returned paused
// Transition. Followers then wait on the frame the lead was last known to be
// at instead of running ahead of its audio. Otherwise held is false and wait
// is how long until the party is next due a check; wait is zero for a party
// that is paused, which needs no check until the lead moves it again.
func (r *Registry) HoldSilentLead(id string) (t Transition, members []Member, held bool, wait time.Duration, err error) {
	g, err := r.partyLookup(id)
	if err != nil {
		return Transition{}, nil, false, 0, err
	}
	now := r.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.lead {
		return Transition{}, nil, false, 0, ErrNotLead
	}
	if g.paused {
		return Transition{}, nil, false, 0, nil
	}
	if silent := now.Sub(g.heard); silent < LeadSilence {
		return Transition{}, nil, false, LeadSilence - silent, nil
	}
	g.position = g.positionAtLocked(g.heard)
	g.paused, g.held = true, true
	g.stated = now
	return g.transitionLocked(now), g.membersLocked(), true, 0, nil
}

// rateLocked is the timeline's speed in thousandths; the zero value is normal
// speed. The caller holds g.mu.
func (g *Group) rateLocked() int {
	if g.rate == 0 {
		return rateNormal
	}
	return g.rate
}

// positionAtLocked is where the timeline is at instant at. The caller holds
// g.mu.
func (g *Group) positionAtLocked(at time.Time) time.Duration {
	if g.paused {
		return g.position
	}
	return g.position + at.Sub(g.anchor)*time.Duration(g.rateLocked())/rateNormal
}

// transitionLocked states a lead party's timeline as of now: the position the
// timeline is at now, presented now. A lead party has no pre-buffer window,
// because the lead is already playing. The caller holds g.mu.
func (g *Group) transitionLocked(now time.Time) Transition {
	return Transition{
		Item:         g.item,
		Position:     g.positionAtLocked(now),
		PresentAt:    now,
		Paused:       g.paused,
		RatePermille: g.rateLocked(),
		Lead:         true,
	}
}

func clampRate(ratePermille int) int {
	switch {
	case ratePermille == 0:
		return rateNormal
	case ratePermille < rateMin:
		return rateMin
	case ratePermille > rateMax:
		return rateMax
	default:
		return ratePermille
	}
}

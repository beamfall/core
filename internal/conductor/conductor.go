// Package conductor implements the Ambient Group Conductor: the
// server-side coordinator of a beat-accurate, multi-room audio sync group.
//
// The Conductor is EPHEMERAL by construction — it is the runtime sibling of the
// Ambient Queue: never persisted, assembled live, and reassembled
// on reconnect. There is no migration and no schema for it; all state lives in
// this package's in-memory Registry. If the process restarts, every group is
// gone and members re-join (re-calibrate, re-sync) exactly as on a cold join.
//
// The Conductor owns four things per group (ADR-0035 §8 "Group Conductor"):
//
//   - Membership — the set of Ambient Display sessions in the group, each with
//     its measured clock offset and render latency.
//   - Shared queue position + NTP anchor — "the canonical position P was at
//     server NTP time T"; every sync signal is expressed relative to this anchor.
//   - Transition broadcasts — {item, position, present_at}, delivered over the
//     EXISTING /api/events SSE outbox (no second transport), so every member
//     releases the same sample at the same wall-clock instant.
//   - Drift correction — members report drift; the Conductor issues a ±0.1 %
//     rate nudge for small drift and a full re-sync (a fresh transition cycle)
//     when drift exceeds the recoverable threshold (> 500 ms).
//
// LAN-only and same-Profile are enforced at the surface (the HTTP calibration
// endpoint reads requestIsLAN; the graph resolvers gate on scope.ProfileID) —
// this package is the in-memory mechanism, not the authorization boundary, but
// it carries each group's ProfileID so a same-Profile assertion is checkable
// here too (Join/lookup reject a cross-Profile caller fail-closed).
package conductor

import (
	"errors"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

// PreBufferWindow is how far in the future the Conductor schedules a transition
// (ADR-0035 §8 "Transition protocol": present_at = now + 2 s). The pre-buffer
// lets every member fetch and decode the next item before its scheduled release
// instant; the cost is ~2 s of transition latency, comparable to streaming
// services and imperceptible for ambient atmosphere.
const PreBufferWindow = 2 * time.Second

// DriftResyncThreshold is the recoverable-drift ceiling (ADR-0035 §8 "Drift
// correction": > 500 ms re-syncs via a full transition cycle). A member that has
// drifted past this is too far gone for a rate nudge to claw back in reasonable
// time, so it re-anchors with a fresh {item, position, present_at} broadcast.
const DriftResyncThreshold = 500 * time.Millisecond

// DriftNudgeThreshold is the smallest drift worth correcting (ADR-0035 §8: a
// micro-correction issues above ~10 ms). Below it the member is within the LAN
// NTP accuracy window and no signal is sent — correcting sub-threshold drift
// would chase NTP jitter, not real drift.
const DriftNudgeThreshold = 10 * time.Millisecond

// NudgeRate is the playback-rate adjustment a micro-correction applies
// (ADR-0035 §8: "e.g. ±0.1 % playback rate"). 0.001 == 0.1 %. The sign follows
// the drift direction: a member that is AHEAD slows down (negative), one that is
// BEHIND speeds up (positive).
const NudgeRate = 0.001

var (
	// ErrNoGroup reports that no group exists for the given id. Surfaces report
	// it as an unknown group, never leaking whether the id belongs to another
	// Profile.
	ErrNoGroup = errors.New("conductor: no such group")
	// ErrWrongProfile reports that the caller's Profile does not own the group.
	// Surfaces collapse it into the same "unknown group" response so the
	// Conductor is never a cross-Profile existence oracle (ADR-0035 §7 security
	// surface, inherited by §8).
	ErrWrongProfile = errors.New("conductor: group belongs to another profile")
	// ErrNotMember reports that the session is not a member of the group.
	ErrNotMember = errors.New("conductor: session is not a group member")
)

// Member is one Ambient Display session in a group, with the per-device
// calibration the Conductor uses to compute its presentation timestamps
// (ADR-0035 §8 "Latency calibration").
type Member struct {
	// SessionID identifies the member's Ambient Display session. A device's
	// membership is keyed by its session, so a reconnect (new session) is a
	// fresh join — consistent with the ephemeral, reassembled-on-reconnect model.
	SessionID store.SessionID
	// DeviceID identifies the physical device (for the Ambient Remote's
	// membership view); the Conductor never authorizes off it.
	DeviceID store.DeviceID
	// ProfileID is the Profile that owns this session. Populated only for Watch
	// Party members (ADR-0111 participant-exposure minimization): the graph layer
	// uses it to surface the participant's chosen display name rather than raw
	// session or device identifiers. Zero for Ambient Group members (they are
	// same-Profile by definition so the ProfileID adds no information there).
	ProfileID store.ProfileID
	// ClockOffset is the member's local clock minus the server clock, from the
	// four-timestamp calibration (Calibrate.Offset). Adding it to a server NTP
	// instant yields that instant on the member's local clock.
	ClockOffset time.Duration
	// RenderLatency is how long after "first byte decoded" audio reaches the
	// member's speaker — a platform measurement the device reports (CoreAudio on
	// Apple, AudioTrack on Android). The member releases audio this far BEFORE
	// the scheduled present instant so the sound hits the air on time.
	RenderLatency time.Duration
}

// Calibration is the four-timestamp NTP-style exchange result (ADR-0035 §8
// "Latency calibration"). It is computed by Calibrate from the four timestamps
// of one round-trip and is the same math NTP uses.
type Calibration struct {
	// Offset is the member's clock minus the server's clock: a positive Offset
	// means the member's clock is ahead of the server's. ServerToMember adds it
	// to convert a server instant to the member's local clock.
	Offset time.Duration
	// RoundTrip is the measured network round-trip time (the delay component).
	// It is reported back so the member (and the Ambient Remote) can judge the
	// LAN's quality; the Conductor does not gate on it.
	RoundTrip time.Duration
}

// Calibrate computes the clock offset and round-trip from the four timestamps of
// one NTP-style exchange. This is the canonical NTP clock-filter
// math, oriented so Offset is the MEMBER-minus-SERVER offset (the form
// ServerToMember/ReleaseAt add to a server instant to land on the member clock):
//
//	t1 — member's local clock when it SENT the request
//	t2 — server's clock when it RECEIVED the request
//	t3 — server's clock when it SENT the response
//	t4 — member's local clock when it RECEIVED the response
//
//	offset    = ((t1 - t2) + (t4 - t3)) / 2   // member − server
//	roundtrip = (t4 - t1) - (t3 - t2)
//
// The classic NTP offset ((t2−t1)+(t3−t4))/2 is the inverse (server − member);
// we negate it so a member whose clock runs AHEAD of the server has a POSITIVE
// offset, matching the field doc and the ServerToMember addition. The two server
// timestamps are recorded by the server at the calibration endpoint, so the
// member supplies only its own t1/t4 and the offset is immune to symmetric path
// delay to first order — exactly why NTP uses four timestamps rather than two.
func Calibrate(t1, t2, t3, t4 time.Time) Calibration {
	offset := ((t1.Sub(t2)) + (t4.Sub(t3))) / 2
	roundTrip := (t4.Sub(t1)) - (t3.Sub(t2))
	return Calibration{Offset: offset, RoundTrip: roundTrip}
}

// ServerToMember converts a server-clock instant to the member's local clock by
// adding the member's offset (ADR-0035 §8: each member converts present_at via
// its own offset). The member then releases audio at the returned instant minus
// its own render latency so the sample reaches the speaker on the server's
// schedule.
func (m Member) ServerToMember(serverTime time.Time) time.Time {
	return serverTime.Add(m.ClockOffset)
}

// ReleaseAt is the member's LOCAL-clock instant at which it must begin releasing
// audio so the sample hits its speaker exactly at the server-scheduled present
// instant (ADR-0035 §8: "releases audio at T_present − own_render_latency",
// expressed on the member's clock). It composes the clock-offset conversion with
// the render-latency compensation: convert present_at to the member's clock,
// then back off by the render latency.
func (m Member) ReleaseAt(presentAt time.Time) time.Time {
	return m.ServerToMember(presentAt).Add(-m.RenderLatency)
}

// DriftAction is the Conductor's verdict on a member's reported drift
// (ADR-0035 §8 "Drift correction").
type DriftAction int

const (
	// DriftNone means the drift is within the LAN NTP accuracy window; the
	// Conductor sends no signal.
	DriftNone DriftAction = iota
	// DriftNudge means a ±0.1 % playback-rate micro-correction is warranted —
	// recoverable drift that a rate adjustment claws back without a seek
	// artifact.
	DriftNudge
	// DriftResync means the member has drifted beyond the recoverable threshold
	// (> 500 ms) and must re-anchor via a full transition cycle.
	DriftResync
)

// DriftDecision is the outcome of classifying a member's drift: the action plus,
// for a nudge, the signed rate adjustment to apply (positive == speed up a
// member that is behind; negative == slow down a member that is ahead).
type DriftDecision struct {
	Action DriftAction
	// Rate is the playback-rate delta for a DriftNudge (±NudgeRate); zero for
	// DriftNone and DriftResync (a resync re-anchors rather than rate-adjusts).
	Rate float64
}

// ClassifyDrift maps a measured drift to a correction action. drift
// is the member's position error: POSITIVE means the member is AHEAD of the
// canonical timeline (it must slow down), NEGATIVE means it is BEHIND (it must
// speed up). The magnitude selects the action; the sign selects the nudge
// direction.
func ClassifyDrift(drift time.Duration) DriftDecision {
	switch {
	case drift > DriftResyncThreshold || drift < -DriftResyncThreshold:
		return DriftDecision{Action: DriftResync}
	case drift >= DriftNudgeThreshold || drift <= -DriftNudgeThreshold:
		// A member ahead (positive drift) slows down (negative rate); a member
		// behind (negative drift) speeds up (positive rate).
		rate := NudgeRate
		if drift > 0 {
			rate = -NudgeRate
		}
		return DriftDecision{Action: DriftNudge, Rate: rate}
	default:
		return DriftDecision{Action: DriftNone}
	}
}

// Group is the in-memory state of one Ambient Group. It is owned by
// the Registry and never persisted. The mutex guards membership and anchor
// mutation; the Registry holds its own lock for the group map.
type Group struct {
	mu sync.Mutex
	// ID is the group's ephemeral identifier, minted at formation. It is not a
	// durable key — a new group on the same devices after a restart has a new ID.
	ID string
	// ProfileID is the Profile that owns the group. Every member must be in this
	// Profile (the surface enforces same-Profile; the Registry asserts it too).
	ProfileID store.ProfileID
	// members maps session id → Member. Ephemeral; reassembled on reconnect.
	members map[store.SessionID]*Member
	// item is the current shared-queue item id (a Media id, plugin-declared). The
	// queue COMPOSITION stays plugin-owned (Music/Podcast); the Conductor owns
	// only the position state for the group (ADR-0035 §8 "Shared audio queue +
	// position").
	item store.MediaID
	// position is the canonical playback position within item at the anchor.
	position time.Duration
	// anchor is the server NTP instant at which the canonical position was
	// `position` (ADR-0035 §8 "NTP anchor": position P was at server time T).
	anchor time.Time
	// posture distinguishes an Ambient Group (PostureAmbient, the AV-4 default)
	// from a Watch Party (PostureWatchParty) — ADR-0038 §2.2. Set at formation;
	// the zero value keeps every existing Ambient Group an auto-advancing,
	// hostless group, so AV-4 behavior is unchanged.
	posture Posture
	// host is the Watch Party host's session: the one session whose transport is
	// authoritative. Empty for an Ambient Group.
	host store.SessionID
	// content is the Media the Watch Party is watching, fixed at formation. It is
	// the anchor for the Reveal intersection rule: an invitee may
	// only join a party whose content their session can Reveal.
	content store.MediaID
	// relaxed is the Watch Party setting that auto-approves non-host seek/skip
	// requests without host confirmation (ADR-0038 §2.4, household mode).
	relaxed bool
	// invites maps invitee Profile → pending Invite for a Watch Party (ADR-0038
	// §2.5). Ephemeral like everything else here; consumed on accept. Nil for an
	// Ambient Group.
	invites map[store.ProfileID]Invite
}

// Members returns a snapshot copy of the group's members (ADR-0035 §8: the
// Ambient Remote surfaces membership). The copy is safe to read without holding
// the group lock.
func (g *Group) Members() []Member {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Member, 0, len(g.members))
	for _, m := range g.members {
		out = append(out, *m)
	}
	return out
}

// Transition is the {item, position, present_at} broadcast the Conductor sends
// to start every member at the same wall-clock instant (ADR-0035 §8 "Transition
// protocol"). It is the payload the surface serializes onto the SSE outbox.
type Transition struct {
	Item      store.MediaID
	Position  time.Duration
	PresentAt time.Time
}

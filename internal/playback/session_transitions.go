package playback

import (
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

type sessionState uint8

const (
	sessionStarting sessionState = iota
	sessionRunning
	sessionFailed
	sessionRevoking
	sessionRetired
)

type publishSession struct {
	composed *ComposedPicture
	picture  *ffmpeg.ComposedPicture
}

type reuseSession struct {
	priority Priority
	at       time.Time
}
type setSessionResume struct{ seconds int }
type deliverSessionRung struct{ id string }
type recordSourceEnd struct {
	rung    Rung
	segment int
}
type detachSession struct{ cleanupOnRelease, libraryDrain bool }
type advanceFrontier struct {
	run     *encoderRun
	rungID  string
	base    int
	scanned int
}

type reapEncoder struct {
	run          *encoderRun
	outcome      encoderExit
	wroteSegment bool
	sourceEnd    int // -1 when the source end was not found
	rung         Rung
}

type transitionResult struct {
	accepted  bool
	teardown  *sessionTeardown
	lowered   bool
	failedNow bool
}

// transitionLocked owns session and encoder state under Sessions.mu. A key
// selects a head at entry. Once selected, every change targets the exact
// instance and, for off-lock work, its original operation or run.
func (s *Sessions) transitionLocked(session *hlsSession, change any) transitionResult {
	switch change := change.(type) {
	case publishSession:
		if session.state != sessionStarting || s.closed || (s.sessions[session.key] != nil && s.sessions[session.key] != session) {
			return transitionResult{}
		}
		session.composed = change.composed
		session.picture = change.picture
		session.state = sessionRunning
		s.sessions[session.key] = session
		if session.secure != nil && session.secure.Revoked != nil {
			session.revokeDone = make(chan struct{})
		}
		return transitionResult{accepted: true}
	case detachSession:
		if session.state >= sessionRevoking {
			return transitionResult{}
		}
		if s.sessions[session.key] == session {
			delete(s.sessions, session.key)
		} else if session.state == sessionRunning {
			return transitionResult{}
		}
		delete(s.continuations, session)
		t := &sessionTeardown{libraryDrain: change.libraryDrain, session: session, live: session.live, cleanupOnRelease: change.cleanupOnRelease, cutoffDone: make(chan struct{})}
		for probe := range session.probes {
			if probe.handedOff {
				t.probeReaps = append(t.probeReaps, probe.done)
			}
		}
		if t.live != nil {
			t.live.Cutoff()
		}
		if session.run != nil {
			t.liveRun = session.run.live
			if session.proc != nil {
				t.run = session.run
				t.stopChild = !t.run.stopIssued
				t.run.stopIssued = true
				s.acceptEndpointFailureLocked(t.run)
				t.run.acceptReasonLocked(exitStopped)
			}
		}
		if start := session.start; start != nil && start.run != t.run {
			t.preparing = start.run
			if start.permitted && start.run.proc == nil {
				t.spawnDone = start.done
			}
			t.preparingLive = start.run.live
			t.cleanupOnRelease = true
		}
		if session.proc == nil && (session.start == nil || !session.start.permitted) {
			s.releaseEncoderLocked(session)
		}
		if session.revokeDone != nil {
			close(session.revokeDone)
			session.revokeDone = nil
		}
		if p := session.scratch; p != nil {
			p.state = scratchPending
			close(p.cutoff)
			p.drained = make(chan struct{})
			if p.users == 0 {
				close(p.drained)
			}
		}
		s.detached++
		if s.revocations == nil {
			s.revocations = make(map[*hlsSession]*sessionTeardown)
		}
		s.revocations[session] = t
		session.state = sessionRevoking
		session.teardown = t
		return transitionResult{accepted: true, teardown: t}
	case *sessionTeardown:
		if session.state != sessionRevoking || session.teardown != change {
			return transitionResult{}
		}
		s.detached--
		delete(s.revocations, session)
		s.releaseSourceLocked(session.sourceOwner)
		session.teardown = nil
		session.state = sessionRetired
		s.wakeCloseLocked()
		return transitionResult{accepted: true}
	case reapEncoder:
		if change.run.session != session {
			return transitionResult{}
		}
		// Even a detached or replaced run has waiters on its own result.
		change.run.outcome = change.outcome
		result := transitionResult{accepted: true}
		if session.state == sessionRunning && session.run == change.run {
			session.lastCrashed = change.outcome == exitCrashed || change.outcome == exitStalled
			switch {
			case change.wroteSegment:
				session.failedRuns = 0
			case session.lastCrashed:
				session.failedRuns++
				if session.failedRuns >= encoderMaxFailedRuns && !session.transcodeFailed {
					session.transcodeFailed = true
					result.failedNow = true
				}
			}
			if change.sourceEnd >= 0 && session.currentSourceLocked() {
				result.lowered = session.noteSourceEndLocked(session.segmentStart(change.rung, change.sourceEnd))
			}
		}
		return result
	}

	if session.state != sessionRunning {
		return transitionResult{}
	}
	switch change := change.(type) {
	case reuseSession:
		delete(s.resumeHints, session.key)
		session.lastAccess = change.at
		session.priority = max(session.priority, change.priority)
	case setSessionResume:
		if session.started {
			return transitionResult{}
		}
		session.resumeSeconds = max(change.seconds, 0)
	case deliverSessionRung:
		session.deliveredRung = change.id
	case recordSourceEnd:
		return transitionResult{accepted: true, lowered: session.noteSourceEndLocked(session.segmentStart(change.rung, change.segment))}
	case advanceFrontier:
		if session.run != change.run || session.proc == nil || session.currentRung != change.rungID || session.baseSegment != change.base {
			return transitionResult{}
		}
		session.frontier = max(session.frontier, change.scanned)
	default:
		panic("unknown playback session transition")
	}
	return transitionResult{accepted: true}
}

func (session *hlsSession) releaseTranscodeLeaseLocked() {
	if session.transcodeLease != nil {
		session.transcodeLease.Release()
		session.transcodeLease = nil
	}
}

// takeRestartLocked spends one crash restart from the session's sliding
// window budget, reporting false when the budget is exhausted.
func (h *hlsSession) takeRestartLocked(now time.Time) bool {
	recent := h.crashRestarts[:0]
	for _, at := range h.crashRestarts {
		if now.Sub(at) < encoderRestartWindow {
			recent = append(recent, at)
		}
	}
	h.crashRestarts = recent
	if len(recent) >= encoderRestartBudget {
		return false
	}
	h.crashRestarts = append(h.crashRestarts, now)
	return true
}

// noteSourceEndLocked lowers the known source end to at, reporting whether it
// changed (so each new fact is logged once).
func (h *hlsSession) noteSourceEndLocked(at time.Duration) bool {
	if h.sourceEndKnown && at >= h.sourceEnd {
		return false
	}
	h.sourceEnd = at
	h.sourceEndKnown = true
	return true
}

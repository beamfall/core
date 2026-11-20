package playback

import (
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
)

type sessionState uint8

const (
	sessionStarting sessionState = iota
	sessionRunning
	sessionRevoking
	sessionRetired
)

type publishSession struct {
	composed *ComposedPicture
	picture  *ffmpeg.ComposedPicture
}

type reuseSession struct{ priority Priority }
type setSessionResume struct{ seconds int }
type deliverSessionRung struct{ id string }
type recordSourceEnd struct {
	rung    Rung
	segment int
}
type detachSession struct{ cleanupOnRelease bool }
type takeEncoderRestart struct{}

// encoderStart is the claim held across the existing off-lock start path.
// startMu still serializes these claims within an instance.
type encoderStart struct {
	replaced     *ffmpeg.HLSProcess
	replacedLive *encderiv.LiveRun
}

type completeEncoderStart struct {
	start   *encoderStart
	run     *encoderRun // nil when launch failed
	rungID  string
	segment int
}

type finishDetachedStart struct{ start *encoderStart }

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
		if session.state != sessionStarting || s.closed || s.sessions[session.key] != nil {
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
		if session.state != sessionRunning && session.state != sessionStarting {
			return transitionResult{}
		}
		if session.state == sessionRunning {
			if s.sessions[session.key] != session {
				return transitionResult{}
			}
			delete(s.sessions, session.key)
		}
		t := &sessionTeardown{session: session, proc: session.proc, live: session.live, cleanupOnRelease: change.cleanupOnRelease}
		if t.live != nil {
			t.live.Cutoff()
		}
		if session.run != nil {
			t.liveRun = session.run.live
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
		switch {
		case session.proc != nil:
			// Teardown carries the slot and lease until the process stops.
			t.lease = session.transcodeLease
			session.transcodeLease = nil
			t.counted = true
			s.stopping++
		case session.start != nil:
			// The start retains its slot and lease. The last directory user
			// completes cleanup, so termination can return during the start.
			t.cleanupOnRelease = true
			s.stopping++
		default:
			session.releaseTranscodeLeaseLocked()
		}
		session.proc = nil
		session.currentRung = ""
		session.state = sessionRevoking
		session.teardown = t
		return transitionResult{accepted: true, teardown: t}
	case *sessionTeardown:
		if session.state != sessionRevoking || session.teardown != change {
			return transitionResult{}
		}
		if change.counted {
			s.stopping--
		}
		s.detached--
		session.teardown = nil
		if session.start == nil {
			session.state = sessionRetired
		}
		s.wakeCloseLocked()
		return transitionResult{accepted: true}
	case finishDetachedStart:
		if session.state != sessionRevoking || change.start == nil || session.start != change.start {
			return transitionResult{}
		}
		session.start = nil
		session.releaseTranscodeLeaseLocked()
		s.stopping--
		s.starting--
		if session.teardown == nil {
			session.state = sessionRetired
		}
		s.wakeCloseLocked()
		return transitionResult{accepted: true}
	case reapEncoder:
		if change.run.session != session {
			return transitionResult{}
		}
		// Even a detached or replaced run has waiters on its own result.
		change.run.outcome = change.outcome
		result := transitionResult{accepted: true}
		if session.state == sessionRunning {
			if session.run == change.run && session.proc != nil {
				session.proc = nil
				session.currentRung = ""
				session.lastCrashed = change.outcome == exitCrashed || change.outcome == exitStalled
				session.releaseTranscodeLeaseLocked()
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
			}
			// EOF remains an instance-level observation until artifact claims
			// are introduced. Detached instances cannot publish this bound.
			if change.sourceEnd >= 0 {
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
		session.lastAccess = s.now()
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
	case takeEncoderRestart:
		return transitionResult{accepted: session.takeRestartLocked(s.now())}
	case *encoderStart:
		if session.start != nil {
			return transitionResult{}
		}
		if session.proc == nil {
			// Preempt background generation before launching the child.
			session.transcodeLease = s.beginTranscode()
		} else {
			// Carry the existing slot and lease across the in-place replacement.
			change.replaced = session.proc
			if session.run != nil {
				change.replacedLive = session.run.live
			}
			session.proc = nil
			session.currentRung = ""
		}
		session.start = change
		s.starting++
	case completeEncoderStart:
		if change.start == nil || session.start != change.start {
			return transitionResult{}
		}
		session.start = nil
		s.starting--
		s.wakeCloseLocked()
		if change.run == nil {
			session.releaseTranscodeLeaseLocked()
			break
		}
		change.run.session = session
		session.run = change.run
		session.proc = change.run.proc
		session.currentRung = change.rungID
		session.deliveredRung = change.rungID
		session.baseSegment = change.segment
		session.frontier = change.segment - 1
		session.lastAccess = s.now()
		session.started = true
		session.resumeSeconds = 0
		if session.proc.Running() {
			change.run.exited = make(chan struct{})
		}
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

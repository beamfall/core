package playback

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

func TestSessionTransitionsAfterReplacement(t *testing.T) {
	for _, finishFirst := range []bool{false, true} {
		s := &Sessions{sessions: make(map[SessionKey]*hlsSession), now: time.Now}
		key := SessionKey{SessionID: "viewer", MediaID: "media", ProfileCapability: "receiver"}
		old, successor := newSessionReservation(key), newSessionReservation(key)
		run := &encoderRun{session: old, proc: &ffmpeg.HLSProcess{}, reaped: make(chan struct{})}
		s.mu.Lock()
		if !s.transitionLocked(old, publishSession{}).accepted {
			t.Fatal("could not publish the original instance")
		}
		old.run, old.proc = run, run.proc
		s.reserveEncoderLocked(old)
		teardown := s.transitionLocked(old, detachSession{}).teardown
		if teardown == nil || !s.transitionLocked(successor, publishSession{}).accepted {
			t.Fatal("could not replace instance")
		}
		s.reserveEncoderLocked(successor)
		if finishFirst {
			s.transitionLocked(old, reapEncoder{run: run, sourceEnd: 1})
		}
		s.childReapedLocked(run)
		s.transitionLocked(old, teardown)
		if !finishFirst {
			s.transitionLocked(old, reapEncoder{run: run, sourceEnd: 1})
		}
		if s.sessions[key] != successor || s.liveCount() != 1 || !successor.charged || old.charged || old.sourceEndKnown || successor.sourceEndKnown {
			t.Fatal("superseded completion changed the successor")
		}
		s.mu.Unlock()
	}
}

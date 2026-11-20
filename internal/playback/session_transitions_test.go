package playback

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

func TestSessionTransitionsAfterReplacement(t *testing.T) {
	tests := []struct {
		name          string
		startInFlight bool
		teardownFirst bool
	}{
		{name: "encoder start finishes first", startInFlight: true},
		{name: "teardown finishes first", startInFlight: true, teardownFirst: true},
		{name: "encoder reaps after replacement"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			s := &Sessions{sessions: make(map[SessionKey]*hlsSession), now: func() time.Time { return now }}
			key := SessionKey{SessionID: "viewer", MediaID: "media", ProfileCapability: "receiver"}
			rung := Rung{ID: "low"}

			s.mu.Lock()
			defer s.mu.Unlock()
			old := &hlsSession{key: key}
			if !s.transitionLocked(old, publishSession{}).accepted {
				t.Fatal("could not publish the original instance")
			}
			start := &encoderStart{}
			if !s.transitionLocked(old, start).accepted {
				t.Fatal("could not claim the original start")
			}
			oldRun := &encoderRun{proc: &ffmpeg.HLSProcess{}}
			startResult := completeEncoderStart{start: start, run: oldRun, rungID: "old", segment: 9}
			if !tt.startInFlight && !s.transitionLocked(old, startResult).accepted {
				t.Fatal("could not publish the original run")
			}
			teardown := s.transitionLocked(old, detachSession{}).teardown
			if teardown == nil || old.state != sessionRevoking {
				t.Fatal("could not detach the original instance")
			}

			successor := &hlsSession{key: key}
			newStart := &encoderStart{}
			newRun := &encoderRun{proc: &ffmpeg.HLSProcess{}}
			if !s.transitionLocked(successor, publishSession{}).accepted ||
				!s.transitionLocked(successor, newStart).accepted ||
				!s.transitionLocked(successor, completeEncoderStart{start: newStart, run: newRun, rungID: rung.ID, segment: 4}).accepted {
				t.Fatal("could not publish the successor run")
			}
			now = now.Add(time.Second)

			if tt.teardownFirst {
				s.transitionLocked(old, teardown)
				if old.state != sessionRevoking {
					t.Fatal("teardown retired an instance with a start still in flight")
				}
			}
			if tt.startInFlight {
				if s.transitionLocked(old, startResult).accepted || old.run != nil {
					t.Fatal("late launch published a run on the detached instance")
				}
				s.transitionLocked(old, finishDetachedStart{start: start})
			} else {
				s.transitionLocked(old, reapEncoder{run: oldRun, outcome: exitEndOfInput, sourceEnd: 10, rung: rung})
				if oldRun.outcome != exitEndOfInput || old.sourceEndKnown {
					t.Fatal("late reap failed to record its own outcome or published an EOF bound")
				}
				if !s.transitionLocked(old, stoppedDetachedEncoder{teardown: teardown}).accepted {
					t.Fatal("could not release the original encoder slot")
				}
			}
			if !tt.teardownFirst {
				s.transitionLocked(old, teardown)
			}
			if stale := s.transitionLocked(old, detachSession{}); stale.teardown != nil {
				t.Fatal("a duplicate detach claimed cleanup")
			}

			if old.state != sessionRetired || !s.offLockIdleLocked() {
				t.Fatalf("original operation leaked state or accounting: state=%v starting=%d stopping=%d detached=%d",
					old.state, s.starting, s.stopping, s.detached)
			}
			if s.sessions[key] != successor || successor.state != sessionRunning ||
				successor.run != newRun || successor.proc != newRun.proc || successor.start != nil ||
				successor.currentRung != rung.ID || successor.deliveredRung != rung.ID ||
				successor.baseSegment != 4 || successor.frontier != 3 || !successor.started ||
				!successor.lastAccess.Equal(now.Add(-time.Second)) || successor.sourceEndKnown ||
				newRun.outcome != exitStopped || s.liveCount() != 1 {
				t.Fatal("a superseded completion changed the successor")
			}
		})
	}
}

package playback

import (
	"testing"
)

func TestSharedEncoderGroupSharesLeaderAndPromotesOnRevoke(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)

	rung := Rung{ID: "720p", Height: 720, VideoKbps: 4000}
	leader := &hlsSession{
		key:              SessionKey{SessionID: "sess-a", MediaID: "m1", ProfileCapability: "web"},
		profile:          "prof-1",
		library:          "lib-1",
		dir:              t.TempDir(),
		state:            sessionRunning,
		currentRung:      rung.ID,
		baseSegment:      0,
		frontier:         2,
		sourceGeneration: "fp1",
		start:            &encoderStart{},
		run:              &encoderRun{exited: make(chan struct{}), reaped: make(chan struct{})},
		admitted:         true,
		rungs:            []Rung{rung},
	}
	follower := &hlsSession{
		key:              SessionKey{SessionID: "sess-b", MediaID: "m1", ProfileCapability: "web"},
		profile:          "prof-1",
		library:          "lib-1",
		dir:              t.TempDir(),
		state:            sessionRunning,
		sourceGeneration: "fp1",
		admitted:         true,
		rungs:            []Rung{rung},
	}

	s.mu.Lock()
	s.reserveEncoderLocked(leader)
	s.registerSharedLeaderLocked(leader, rung)
	gotLeader := s.attachSharedGroupLocked(follower, rung, 1)
	if gotLeader != leader {
		s.mu.Unlock()
		t.Fatalf("attachSharedGroupLocked = %v, want leader %v", gotLeader, leader)
	}
	if follower.mediaDir() != leader.dir {
		s.mu.Unlock()
		t.Fatalf("follower mediaDir = %q, want %q", follower.mediaDir(), leader.dir)
	}
	s.mu.Unlock()
	if !s.HasSharedEncoder("m1", "fp1") {
		t.Fatal("HasSharedEncoder = false while leader start is active, want true")
	}
	// Detach leader: follower must be promoted and inherit the shared dir & charge.
	origDir := leader.dir
	s.mu.Lock()
	s.detachFromSharedGroupLocked(leader)
	s.mu.Unlock()

	if follower.dir != origDir {
		t.Fatalf("promoted follower.dir = %q, want %q", follower.dir, origDir)
	}
	if follower.sharedDir != "" {
		t.Fatalf("promoted follower.sharedDir = %q, want empty", follower.sharedDir)
	}
	if !follower.charged || leader.charged {
		t.Fatalf("charge did not transfer: follower.charged=%v leader.charged=%v", follower.charged, leader.charged)
	}
}

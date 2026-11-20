package playback

import (
	"context"
	"errors"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// DrainFence identifies one closure of a library's playback admission.
type DrainFence uint64

var ErrDrainFence = errors.New("playback library drain requires the current fence")

type libraryAdmission struct {
	stamp   DrainFence
	closed  bool
	drained bool
	attempt *drainAttempt
}

type drainAttempt struct {
	done       chan struct{}
	err        error
	cleanupErr error
	failed     map[*scratchPath]struct{} // each path gets one attempt
}

func (s *Sessions) admissionStamp() DrainFence {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admissionClock
}

func (s *Sessions) admittedLocked(library store.LibraryID, stamp DrainFence) bool {
	admission := s.admission[library]
	return !s.closed && (admission == nil || !admission.closed && admission.stamp <= stamp)
}

type libraryOperation struct {
	library store.LibraryID
	stamp   DrainFence
	ctx     context.Context
	cancel  context.CancelFunc
}

func (s *Sessions) claimLibrary(ctx context.Context, library store.LibraryID, stamp DrainFence) (*libraryOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.admittedLocked(library, stamp) {
		return nil, ErrSessionEvicted
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	op := &libraryOperation{library: library, stamp: stamp, ctx: ctx, cancel: cancel}
	if s.libraryOps == nil {
		s.libraryOps = make(map[*libraryOperation]struct{})
	}
	s.libraryOps[op] = struct{}{}
	return op, nil
}

func (s *Sessions) releaseLibrary(op *libraryOperation) {
	op.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.libraryOps, op)
	s.wakeCloseLocked()
}

func (s *Sessions) libraryOperationError(op *libraryOperation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.libraryOps[op]; !ok || !s.admittedLocked(op.library, op.stamp) {
		return ErrSessionEvicted
	}
	return op.ctx.Err()
}

func (s *Sessions) libraryProbeContext(op *libraryOperation) context.Context {
	check := func() error { return s.libraryOperationError(op) }
	return ffmpeg.WithPlaybackProbe(op.ctx, check, func() bool { return check() == nil }, func() {})
}

// Drain closes admission on a zero fence. A retry or join uses the exact
// returned fence. A failed or canceled attempt leaves admission closed.
func (s *Sessions) Drain(ctx context.Context, library store.LibraryID, expectedFence DrainFence) (DrainFence, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrSessionEvicted
	}
	admission := s.admission[library]
	var teardowns []*sessionTeardown
	var cancels []context.CancelFunc
	switch {
	case expectedFence == 0 && (admission == nil || !admission.closed):
		s.admissionClock++
		admission = &libraryAdmission{stamp: s.admissionClock, closed: true}
		if s.admission == nil {
			s.admission = make(map[store.LibraryID]*libraryAdmission)
		}
		s.admission[library] = admission
		for allocation := range s.allocations {
			if allocation.library == library {
				allocation.revoked = true
			}
		}
		for _, h := range s.sessions {
			if h.admitted && h.library == library {
				teardowns = append(teardowns, s.transitionLocked(h, detachSession{cleanupOnRelease: true, libraryDrain: true}).teardown)
			}
		}
		for h := range s.continuations {
			if h.admitted && h.library == library {
				teardowns = append(teardowns, s.transitionLocked(h, detachSession{cleanupOnRelease: true, libraryDrain: true}).teardown)
			}
		}
		cancels = s.libraryCancelsLocked(func(id store.LibraryID) bool { return id == library })
	case admission == nil || !admission.closed:
		s.mu.Unlock()
		return 0, ErrDrainFence
	case expectedFence != admission.stamp:
		fence := admission.stamp
		s.mu.Unlock()
		return fence, ErrDrainFence
	}
	fence := admission.stamp
	if admission.drained {
		s.mu.Unlock()
		return fence, ctx.Err()
	}
	if attempt := admission.attempt; attempt != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return fence, ctx.Err()
		case <-attempt.done:
			return fence, attempt.err
		}
	}
	attempt := &drainAttempt{done: make(chan struct{})}
	admission.attempt = attempt
	s.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	s.signalTeardowns(teardowns)
	// A slow reap or removal must not serialize stop signals or cleanup
	// for the other sessions. Revocations and scratch claims retain ownership.
	for _, teardown := range teardowns {
		go s.joinTeardown(teardown)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		attempt.err = ctx.Err()
		if attempt.err != nil {
			break
		}
		if s.closed {
			attempt.err = ErrSessionEvicted
			break
		}
		busy, debt := s.libraryWorkLocked(library, attempt.failed)
		if !busy {
			if debt == nil {
				attempt.err = attempt.cleanupErr
				admission.drained = attempt.err == nil
				break
			}
			s.claimScratchRemovalLocked(debt)
			go s.removeScratch(debt)
		}
		if s.libraryChanged == nil {
			s.libraryChanged = make(chan struct{})
		}
		changed := s.libraryChanged
		s.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		s.mu.Lock()
	}
	admission.attempt = nil
	close(attempt.done)
	return fence, attempt.err
}

// Reopen restores admission after a successful drain by the lifecycle owner.
func (s *Sessions) Reopen(library store.LibraryID, fence DrainFence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionEvicted
	}
	admission := s.admission[library]
	if admission == nil || !admission.closed || !admission.drained || admission.stamp != fence {
		return ErrDrainFence
	}
	s.admissionClock++
	admission.stamp, admission.closed, admission.drained = s.admissionClock, false, false
	return nil
}

func (s *Sessions) libraryCancelsLocked(matches func(store.LibraryID) bool) []context.CancelFunc {
	var cancels []context.CancelFunc
	for op := range s.libraryOps {
		if matches(op.library) {
			cancels = append(cancels, op.cancel)
		}
	}
	for key, load := range s.keyframes.inflight {
		if matches(key.library) {
			cancels = append(cancels, load.cancel)
		}
	}
	return cancels
}

func (s *Sessions) libraryWorkLocked(library store.LibraryID, failed map[*scratchPath]struct{}) (busy bool, debt *scratchPath) {
	for op := range s.libraryOps {
		if op.library == library {
			return true, nil
		}
	}
	for key := range s.keyframes.inflight {
		if key.library == library {
			return true, nil
		}
	}
	for h := range s.revocations {
		if h.admitted && h.library == library {
			return true, nil
		}
	}
	for _, p := range s.paths {
		if !p.libraryOwned || p.library != library {
			continue
		}
		if p.state != scratchDebt {
			return true, nil
		}
		if _, tried := failed[p]; !tried {
			debt = p
		}
	}
	return false, debt
}

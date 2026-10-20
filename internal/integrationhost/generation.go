package integrationhost

import (
	"context"
	"errors"
	"sync"
)

// ErrWorkerGenerationRetired rejects a Host API call from a worker generation
// that has been disabled, replaced, or superseded after a crash.
var ErrWorkerGenerationRetired = errors.New("integrationhost: worker generation retired")

// GenerationFence serializes worker ownership changes with Host API calls. A
// replacement invalidates every older lease, including an ABA transition back
// to the same artifact or configuration.
type GenerationFence struct {
	mu         sync.Mutex
	generation uint64
	current    *GenerationLease
}

// GenerationLease identifies one worker launch attempt.
type GenerationLease struct {
	fence      *GenerationFence
	generation uint64
	active     int
	retired    bool
	done       chan struct{}
	doneClosed bool
}

// Acquire transfers ownership to a fresh generation. The owner must retire and
// await its previous lease before it starts the corresponding replacement
// process; Acquire still invalidates a forgotten lease to fail closed.
func (f *GenerationFence) Acquire() *GenerationLease {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.current != nil {
		f.current.retireLocked()
	}
	f.generation++
	lease := &GenerationLease{fence: f, generation: f.generation, done: make(chan struct{})}
	f.current = lease
	return lease
}

// Retire invalidates this lease if it still owns the fence, then waits for
// in-flight guarded Host API calls. The caller supplies the stop bound so a
// wedged dependency cannot block lifecycle shutdown forever.
func (l *GenerationLease) Retire(ctx context.Context) error {
	if l == nil || l.fence == nil {
		return nil
	}
	l.fence.mu.Lock()
	l.retireLocked()
	done := l.done
	l.fence.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (l *GenerationLease) retireLocked() {
	if l.retired {
		return
	}
	l.retired = true
	if l.fence.current == l {
		l.fence.current = nil
	}
	l.closeDoneLocked()
}

func (l *GenerationLease) closeDoneLocked() {
	if l.active != 0 || l.doneClosed {
		return
	}
	close(l.done)
	l.doneClosed = true
}

// Guard runs work only while this lease is the current owner.
func (l *GenerationLease) Guard(work func()) error {
	if l == nil || l.fence == nil {
		work()
		return nil
	}
	l.fence.mu.Lock()
	if l.retired || l.fence.current != l {
		l.fence.mu.Unlock()
		return ErrWorkerGenerationRetired
	}
	l.active++
	l.fence.mu.Unlock()

	defer func() {
		l.fence.mu.Lock()
		l.active--
		l.closeDoneLocked()
		l.fence.mu.Unlock()
	}()
	work()
	return nil
}

// Current reports whether the lease still owns its fence.
func (l *GenerationLease) Current() bool {
	if l == nil || l.fence == nil {
		return true
	}
	l.fence.mu.Lock()
	defer l.fence.mu.Unlock()
	return !l.retired && l.fence.current == l
}

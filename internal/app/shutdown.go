package app

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const (
	defaultJobDrainGracePeriod = 5 * time.Second
	// httpDrainBound caps the graceful HTTP drain. A connection still live when
	// it elapses is closed outright: byte routes stream media without a write
	// deadline, so waiting for one to finish is unbounded by construction.
	httpDrainBound = 5 * time.Second
	// workerJoinBound caps the wait for runtime workers after job cancellation,
	// so a worker that never returns costs a known delay instead of pinning the
	// process forever with no listener and no way out.
	workerJoinBound = 30 * time.Second
)

// errWorkerJoinTimedOut reports that cancellation did not retire every runtime
// worker within workerJoinBound. The sequence continues past it deliberately —
// the durable checkpoint and the store close are what an operator loses if
// shutdown stalls, so they run either way.
var errWorkerJoinTimedOut = errors.New("app: runtime workers outlasted the shutdown join bound")

// errAlreadyRun reports a second Run on an app whose lifecycle already started.
// Run owns single-ownership resources — a bound listener, the address latch,
// and the store's close — none of which survive a second owner.
var errAlreadyRun = errors.New("app: Run already started for this app")

// shutdownSequence is the single ownership/order contract for retiring the
// runtime. Each phase is synchronous and every wait is bounded: claim
// acceptance stops before the graceful drain, cancellation and a bounded worker
// join happen before runtime state is checkpointed, and only then may the store
// close. HTTP listeners are retired by drainHTTPServer before this sequence
// runs, so no request can enter while jobs are being joined.
type shutdownSequence struct {
	stopClaims    func()
	claimsDrained <-chan struct{}
	gracePeriod   time.Duration
	after         func(time.Duration) <-chan time.Time
	cancelJobs    func()
	joinWorkers   func()
	joinBound     time.Duration
	joinAfter     func(time.Duration) <-chan time.Time
	checkpoint    func() error
	closeStore    func() error
}

func (s shutdownSequence) run() error {
	s.stopClaims()
	s.awaitClaimDrain()
	s.cancelJobs()
	joinErr := s.awaitWorkers()
	checkpointErr := s.checkpoint()
	closeErr := s.closeStore()
	return errors.Join(joinErr, checkpointErr, closeErr)
}

// awaitClaimDrain gives an in-flight job handler the graceful window to finish
// its own durable bookkeeping before cancellation reaches it.
func (s shutdownSequence) awaitClaimDrain() {
	select {
	case <-s.claimsDrained:
	case <-s.after(s.gracePeriod):
	}
}

// awaitWorkers joins the runtime worker group within joinBound. The join runs
// on its own goroutine so the bound is enforceable at all; a WaitGroup wait
// cannot itself be interrupted.
func (s shutdownSequence) awaitWorkers() error {
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		s.joinWorkers()
	}()
	select {
	case <-joined:
		return nil
	case <-s.joinAfter(s.joinBound):
		return errWorkerJoinTimedOut
	}
}

// drainHTTPServer retires an HTTP server within a bound and reports whether the
// bound elapsed. Connections get the graceful window; anything still live when
// it passes is closed outright, so neither a listener nor a connection
// goroutine outlives shutdown. serveErr is consumed on every path, so the Serve
// goroutine is always joined.
func drainHTTPServer(server *http.Server, serveErr <-chan error, bound time.Duration) (bool, error) {
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), bound)
	defer cancelDrain()
	gracefulErr := server.Shutdown(drainCtx)
	if gracefulErr == nil {
		return false, <-serveErr
	}
	closeErr := server.Close()
	return true, errors.Join(closeErr, <-serveErr)
}

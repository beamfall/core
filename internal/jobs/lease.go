package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/store"
)

// ErrLeaseLost is returned by RunOne when the job's lease stopped being the
// live one while its handler was still working. The handler's context is
// cancelled at the same moment, and no progress, yield, success, or failure is
// recorded: whichever worker holds the lease now owns those writes.
var ErrLeaseLost = store.ErrJobLeaseLost

// LeaseRenewer is the renewal half of the job store. It is separate from Store
// (rather than another method on it) so that a Store implementation without a
// durable lease — a benchmark or accounting fake — stays valid; the production
// *store.Store implements it, which the assertion below pins at compile time.
// A Store that does not implement it simply runs without renewal, exactly as
// this package behaved before renewal existed.
type LeaseRenewer interface {
	RenewJobLease(ctx context.Context, id store.JobID, leaseOwner string, now time.Time) (time.Time, error)
}

var _ LeaseRenewer = (*store.Store)(nil)

// leaseGuard owns the single renewal goroutine a running job is allowed, and is
// the handler's stop signal: when the lease is lost the guard cancels the
// handler's context so the work ends promptly rather than continuing against a
// job another worker has taken over.
type leaseGuard struct {
	cancel    context.CancelFunc
	done      chan struct{}
	lost      atomic.Bool
	cancelled atomic.Bool
	once      sync.Once
}

// stop ends renewal and waits for the goroutine to exit, so no renewal work
// outlives the RunOne call that started it.
func (g *leaseGuard) stop() {
	g.once.Do(func() {
		if g.cancel != nil {
			g.cancel()
		}
		if g.done != nil {
			<-g.done
		}
	})
}

func (g *leaseGuard) leaseLost() bool {
	return g.lost.Load()
}

// cancelRequested reports whether an operator cancelled the job while it ran
// (Registry.CancelRunning), as opposed to the lease lapsing on its own.
func (g *leaseGuard) cancelRequested() bool {
	return g.cancelled.Load()
}

// requestCancel stops the handler now: it marks the job as cancelled by the
// operator and cancels the handler's context, which in turn kills any child
// process tree the handler started under it.
func (g *leaseGuard) requestCancel() {
	g.cancelled.Store(true)
	g.cancel()
}

// startLeaseRenewal begins renewing job's lease and returns the context the
// handler must run under. The returned context is cancelled as soon as the
// lease is lost or the job is cancelled through CancelRunning. A store without
// renewal still gets a cancellable context, so a cancel reaches the handler.
func (r *Registry) startLeaseRenewal(ctx context.Context, job store.Job) (context.Context, *leaseGuard) {
	leaseCtx, cancel := context.WithCancel(ctx)
	guard := &leaseGuard{cancel: cancel}
	renewer, ok := r.store.(LeaseRenewer)
	if !ok || r.renewInterval <= 0 {
		return leaseCtx, guard
	}
	guard.done = make(chan struct{})
	go r.renewLeaseUntilDone(leaseCtx, renewer, job, guard)
	return leaseCtx, guard
}

// trackRunning records guard as the stop handle for job id while its handler
// runs; the returned func forgets it again.
func (r *Registry) trackRunning(id store.JobID, guard *leaseGuard) func() {
	r.runningMu.Lock()
	if r.running == nil {
		r.running = map[store.JobID]*leaseGuard{}
	}
	r.running[id] = guard
	r.runningMu.Unlock()
	return func() {
		r.runningMu.Lock()
		if r.running[id] == guard {
			delete(r.running, id)
		}
		r.runningMu.Unlock()
	}
}

// CancelRunning stops the handler running job id in this process, if there is
// one, and reports whether there was. The caller cancels the job in the store
// first; this only makes the worker notice at once instead of at its next lease
// renewal, which can be minutes away (CORE-2394). The handler's context is
// cancelled, so its child processes are killed with it, and RunOne records
// nothing further for the job.
func (r *Registry) CancelRunning(id store.JobID) bool {
	r.runningMu.Lock()
	guard := r.running[id]
	r.runningMu.Unlock()
	if guard == nil {
		return false
	}
	guard.requestCancel()
	return true
}

// renewLeaseUntilDone is the whole renewal loop: one goroutine, one ticker,
// bounded by leaseCtx, which the caller always cancels via guard.stop.
func (r *Registry) renewLeaseUntilDone(leaseCtx context.Context, renewer LeaseRenewer, job store.Job, guard *leaseGuard) {
	defer close(guard.done)
	ticker := time.NewTicker(r.renewInterval)
	defer ticker.Stop()
	lastRenewal := time.Now()
	for {
		select {
		case <-leaseCtx.Done():
			return
		case <-ticker.C:
		}
		renewedAt := time.Now()
		_, err := renewer.RenewJobLease(leaseCtx, job.ID, job.LeaseOwner, renewedAt)
		if err == nil {
			lastRenewal = renewedAt
			continue
		}
		if !leaseIsUnrecoverable(err, renewedAt.Sub(lastRenewal), r.leaseTTL) {
			r.logger.WarnContext(leaseCtx, "job lease renewal deferred", "id", job.ID, "kind", job.Kind)
			continue
		}
		r.logger.WarnContext(leaseCtx, "job lease lost", "id", job.ID, "kind", job.Kind)
		guard.lost.Store(true)
		guard.cancel()
		return
	}
}

// leaseIsUnrecoverable decides whether a renewal error ends the work. A store
// that reports the lease gone is definitive. A transient store error is not, up
// to a point: once a full TTL has passed with no successful renewal the lease
// has lapsed on the server whatever the error said, so the work stops then too.
func leaseIsUnrecoverable(err error, sinceLastRenewal time.Duration, leaseTTL time.Duration) bool {
	if errors.Is(err, store.ErrJobLeaseLost) {
		return true
	}
	return sinceLastRenewal >= leaseTTL
}

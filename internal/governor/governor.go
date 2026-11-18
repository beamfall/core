// Package governor arbitrates resource priority between realtime work and
// background generation (ADR-0013 §2/§4: transcode preempts generation, plus a
// background-CPU budget and worker-pool sizing for weak hardware). It is a
// leaf: producers report activity, consumers ask permission and acquire slots;
// the wiring lives in the composition root.
//
// Admission is a single mutex-linearized state machine, not split atomic
// counters: a transcode start and a generation admission share one lock, so a
// transcode that begins can atomically block every later generation AND cancel
// the generation contexts already in flight in the same critical section. The
// old bare counters left a window where a generation admitted itself against a
// stale "no transcode" read while a transcode was starting, then ran to
// completion competing with realtime playback; leases close that window.
package governor

import (
	"context"
	"errors"
	"sync"
)

// ErrPreempted is the governor-owned cause a background generation's context is
// cancelled with when a realtime transcode preempts it. It is the
// single sentinel the generation wrapper keys on to map preemption to a durable
// yield-and-retry rather than a job failure; any other cancellation cause
// (shutdown, the caller's own deadline) is left to its normal semantics.
var ErrPreempted = errors.New("governor: background generation preempted by realtime transcode")

// ErrScanPriority is the cause a background sweep's context is cancelled with
// when a library scan begins or waits for a worker (CORE-2571). Like
// ErrPreempted it means yield and resume later, never fail.
var ErrScanPriority = errors.New("governor: background sweep yielded to a library scan")

// Yielded reports whether cause asks a background job to yield and resume
// later rather than fail: realtime transcode preemption or scan priority.
func Yielded(cause error) bool {
	return errors.Is(cause, ErrPreempted) || errors.Is(cause, ErrScanPriority)
}

// Options configures the resource governor. The zero value is a
// no-budget governor (generation only yields to transcode); Config supplies the
// low-power defaults for Celeron/N100-class hardware.
type Options struct {
	// GenerationWorkers is the worker-pool size for background generation
	// (artwork/asset sweeps). Values below 1 are clamped to 1.
	GenerationWorkers int
	// BackgroundCPUBudget caps concurrent background generation slots — the
	// throttle that keeps generation from saturating weak CPUs. A value below 1
	// is clamped to 1, so generation never wedges to a full stop on its own.
	BackgroundCPUBudget int
}

// Governor tracks realtime transcode activity and meters background generation
// against a fixed background-CPU budget. All admission state lives under one
// mutex so transcode preemption and generation admission linearize against a
// single lock — the ownership-safe replacement for the former split atomics.
type Governor struct {
	mu sync.Mutex
	// activeTranscodes counts realtime transcode leases currently held. Any
	// non-zero value preempts generation.
	activeTranscodes int
	// activeGeneration counts background-CPU budget slots currently in use,
	// whether claimed through a cancellable BeginGeneration lease or the plain
	// AcquireGeneration compatibility path. It is the number the budget caps.
	activeGeneration int
	// activeScans counts BeginScan leases held. Any non-zero value keeps
	// BeginSweep from admitting a sweep (CORE-2571).
	activeScans int
	// generations holds the cancellable in-flight generation leases keyed by id
	// so a beginning transcode can cancel each one's context. Plain
	// AcquireGeneration claims count toward activeGeneration but are not here:
	// they carry no context to cancel.
	generations map[int64]*GenerationLease
	nextGenID   int64
	workers     int
	budget      int
	// jobs tracks the in-flight jobs of heavy governor job classes (currently
	// inference, ADR-0127 §5) so a capability's disable path can cancel them all
	// through CancelClass.
	jobs *jobRegistry
}

// New creates a Governor with no background-CPU budget cap beyond the
// transcode-preemption invariant (the budget defaults to one slot).
func New() *Governor {
	return NewWithOptions(Options{})
}

// NewWithOptions creates a Governor with the given worker-pool size and
// background-CPU budget. Both are clamped to at least 1.
func NewWithOptions(opts Options) *Governor {
	workers := opts.GenerationWorkers
	if workers < 1 {
		workers = 1
	}
	budget := opts.BackgroundCPUBudget
	if budget < 1 {
		budget = 1
	}
	return &Governor{
		workers:     workers,
		budget:      budget,
		generations: map[int64]*GenerationLease{},
		jobs:        newJobRegistry(),
	}
}

// GenerationWorkers reports the configured background generation worker-pool
// size.
func (g *Governor) GenerationWorkers() int {
	return g.workers
}

// BackgroundCPUBudget reports the configured concurrent background generation
// slot budget.
func (g *Governor) BackgroundCPUBudget() int {
	return g.budget
}

// TranscodeLease is the opaque handle a realtime transcode holds while active.
// It is acquired via BeginTranscode before the FFmpeg process launches, blocks
// generation while held, and is released exactly once — on process exit, on a
// launch failure that rolls the start back, or on shutdown. Release is
// idempotent: a stale, double, reaper, or replacement release is a no-op.
type TranscodeLease struct {
	g       *Governor
	release sync.Once
}

// BeginTranscode marks one realtime transcode active and, in the same critical
// section that blocks every later generation admission, cancels the context of
// every generation lease already in flight with ErrPreempted. It
// is the single linearization point for "a transcode is starting": once it
// returns, no generation can be admitted until the returned lease is released,
// and the generations it preempted observe cancellation and yield. The returned
// lease MUST be released when the transcode ends or its start rolls back.
func (g *Governor) BeginTranscode() *TranscodeLease {
	g.mu.Lock()
	g.activeTranscodes++
	preempted := make([]*GenerationLease, 0, len(g.generations))
	for _, lease := range g.generations {
		preempted = append(preempted, lease)
	}
	g.mu.Unlock()
	// Cancel outside the lock: cancellation may run context AfterFuncs, and a
	// preempted generation racing to release itself must never deadlock on g.mu.
	// Admission is already sealed — activeTranscodes was raised under the lock —
	// so no generation can slip in after this point regardless of ordering here.
	for _, lease := range preempted {
		lease.cancel(ErrPreempted)
	}
	return &TranscodeLease{g: g}
}

// Release ends the transcode lease, freeing generation once the last transcode
// clears. Idempotent (sync.Once): a rollback-after-launch-failure release, a
// reaper release, and a shutdown release can all fire without double-counting.
func (l *TranscodeLease) Release() {
	l.release.Do(func() {
		l.g.mu.Lock()
		defer l.g.mu.Unlock()
		if l.g.activeTranscodes--; l.g.activeTranscodes < 0 {
			l.g.activeTranscodes = 0
		}
	})
}

// TranscodeStarted records one realtime transcode becoming active without
// handing back a lease. It is the counter-style compatibility entry point for
// callers that manage their own start/stop pairing (metrics/e2e harnesses);
// production transcode paths hold a TranscodeLease via BeginTranscode instead.
// Like BeginTranscode it preempts every in-flight generation lease.
func (g *Governor) TranscodeStarted() {
	g.BeginTranscode()
}

// TranscodeStopped records one realtime transcode ending, the counterpart to
// TranscodeStarted. It underflow-guards so a stray stop can never drive the
// count negative and wrongly reopen generation.
func (g *Governor) TranscodeStopped() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeTranscodes--; g.activeTranscodes < 0 {
		g.activeTranscodes = 0
	}
}

// GenerationLease is the opaque handle a background generation holds while it
// runs. Its Context is cancelled with ErrPreempted the moment a realtime
// transcode begins, so the generation yields rather than competing. Release is
// idempotent (sync.Once): a stale, double, reaper, or replacement release is a
// no-op, and the derived context's resources are always freed exactly once.
type GenerationLease struct {
	g       *Governor
	id      int64
	ctx     context.Context
	cancel  context.CancelCauseFunc
	release sync.Once
	// budgeted is false for a BeginPreemptible lease: it yields to transcodes
	// but never held a background-CPU budget slot, so Release returns none.
	budgeted bool
	// scan marks a BeginScan lease, whose Release ends a scan; sweep marks a
	// BeginSweep lease, which a scan cancels with ErrScanPriority.
	scan, sweep bool
}

// BeginGeneration admits one background generation against the transcode-
// preemption invariant and the background-CPU budget, in the same critical
// section a transcode start contends for. It returns false when a transcode is
// active (preemption) or the budget is fully spent (throttle) — the caller
// yields and retries. On success it returns a lease whose Context is derived
// from parent and is cancelled with ErrPreempted if a transcode later preempts
// it; the caller MUST run under lease.Context and Release the lease when done.
func (g *Governor) BeginGeneration(parent context.Context) (*GenerationLease, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeTranscodes != 0 || g.activeGeneration >= g.budget {
		return nil, false
	}
	g.nextGenID++
	id := g.nextGenID
	leaseCtx, cancel := context.WithCancelCause(parent)
	lease := &GenerationLease{g: g, id: id, ctx: leaseCtx, cancel: cancel, budgeted: true}
	g.generations[id] = lease
	g.activeGeneration++
	return lease, true
}

// BeginPreemptible admits background work that must yield to realtime
// transcodes but is not generation, so it takes no background-CPU budget slot:
// library scans (CORE-2393). Scans already size their own worker pool from
// GenerationWorkers, and sharing the generation budget would make a scan and a
// sweep exclude each other on an idle server. It returns false while any
// transcode is active; on success the lease's Context is cancelled with
// ErrPreempted when a transcode begins, exactly like a generation lease.
func (g *Governor) BeginPreemptible(parent context.Context) (*GenerationLease, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.beginLocked(parent, false)
}

// BeginScan admits a library scan: background work that yields to realtime
// transcodes like BeginPreemptible and takes no budget slot, but also has
// priority over the generation sweeps (CORE-2571). It cancels every running
// sweep with ErrScanPriority and, until the lease is released, BeginSweep
// refuses, so a sweep never holds the job worker a scan is waiting for.
func (g *Governor) BeginScan(parent context.Context) (*GenerationLease, bool) {
	g.mu.Lock()
	lease, ok := g.beginLocked(parent, false)
	if !ok {
		g.mu.Unlock()
		return nil, false
	}
	lease.scan = true
	g.activeScans++
	sweeps := g.sweepsLocked()
	g.mu.Unlock()
	for _, sweep := range sweeps {
		sweep.cancel(ErrScanPriority)
	}
	return lease, true
}

// BeginSweep admits a generation sweep (artwork, assets, skip detection,
// decode verification): a BeginGeneration lease that also refuses while a scan
// runs and is cancelled with ErrScanPriority when one begins or waits.
func (g *Governor) BeginSweep(parent context.Context) (*GenerationLease, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeScans != 0 || g.activeGeneration >= g.budget {
		return nil, false
	}
	lease, ok := g.beginLocked(parent, true)
	if !ok {
		return nil, false
	}
	lease.sweep = true
	g.activeGeneration++
	return lease, true
}

// PreemptSweeps cancels every running sweep with ErrScanPriority and returns
// how many it cancelled. The job runner calls it when a scan is runnable but
// queued, so a sweep holding the worker yields it (CORE-2571).
func (g *Governor) PreemptSweeps() int {
	g.mu.Lock()
	sweeps := g.sweepsLocked()
	g.mu.Unlock()
	for _, sweep := range sweeps {
		sweep.cancel(ErrScanPriority)
	}
	return len(sweeps)
}

// ActiveSweeps counts the sweeps holding a lease.
func (g *Governor) ActiveSweeps() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sweepsLocked())
}

// ActiveScans counts the scans holding a lease.
func (g *Governor) ActiveScans() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeScans
}

func (g *Governor) sweepsLocked() []*GenerationLease {
	var sweeps []*GenerationLease
	for _, lease := range g.generations {
		if lease.sweep {
			sweeps = append(sweeps, lease)
		}
	}
	return sweeps
}

// beginLocked registers a lease when no transcode is active. The caller holds
// g.mu and does any budget or scan accounting.
func (g *Governor) beginLocked(parent context.Context, budgeted bool) (*GenerationLease, bool) {
	if g.activeTranscodes != 0 {
		return nil, false
	}
	g.nextGenID++
	id := g.nextGenID
	leaseCtx, cancel := context.WithCancelCause(parent)
	lease := &GenerationLease{g: g, id: id, ctx: leaseCtx, cancel: cancel, budgeted: budgeted}
	g.generations[id] = lease
	return lease, true
}

// Context is the cancellable context the generation must run under: it inherits
// the parent's cancellation and is additionally cancelled with ErrPreempted on
// transcode preemption.
func (l *GenerationLease) Context() context.Context {
	return l.ctx
}

// Release returns the generation's budget slot and deregisters it so a later
// transcode never tries to cancel a finished generation. Idempotent: a
// completion release, a reaper release, and a post-preemption release all
// collapse to one accounting effect. It cancels the derived context with a nil
// cause (leaving any earlier ErrPreempted cause intact) purely to free context
// resources — the cause callers inspect is whichever cancellation happened first.
func (l *GenerationLease) Release() {
	l.release.Do(func() {
		l.g.mu.Lock()
		if _, ok := l.g.generations[l.id]; ok {
			delete(l.g.generations, l.id)
			if l.budgeted {
				if l.g.activeGeneration--; l.g.activeGeneration < 0 {
					l.g.activeGeneration = 0
				}
			}
			if l.scan {
				if l.g.activeScans--; l.g.activeScans < 0 {
					l.g.activeScans = 0
				}
			}
		}
		l.g.mu.Unlock()
		l.cancel(nil)
	})
}

// GenerationAllowed reports whether background generation may run now:
// generation yields whenever any realtime transcode is active (transcode
// preempts generation) OR the background-CPU budget is already fully spent.
func (g *Governor) GenerationAllowed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeTranscodes == 0 && g.activeGeneration < g.budget
}

// AcquireGeneration claims one background-CPU budget slot if generation is
// allowed now, reporting success. A caller that gets true MUST call
// ReleaseGeneration when its work finishes. It is the plain counter-style
// admission (no cancellable lease) for callers that manage their own
// cancellation; production generation handlers hold a GenerationLease via
// BeginGeneration instead. It returns false under transcode preemption or a
// spent budget.
func (g *Governor) AcquireGeneration() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeTranscodes != 0 || g.activeGeneration >= g.budget {
		return false
	}
	g.activeGeneration++
	return true
}

// ReleaseGeneration returns a slot claimed by AcquireGeneration. It guards
// against underflow so a stray release can never open the gate past the budget.
func (g *Governor) ReleaseGeneration() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeGeneration--; g.activeGeneration < 0 {
		g.activeGeneration = 0
	}
}

// ActiveTranscodes returns the number of realtime transcodes currently active
// — the same counter generation preemption keys on (ADR-0013 §6 observability).
func (g *Governor) ActiveTranscodes() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeTranscodes
}

// ActiveGeneration returns the number of background generation slots currently
// claimed against the configured CPU budget.
func (g *Governor) ActiveGeneration() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeGeneration
}

// ClassAllowed reports whether a heavy job of the given class may start now.
// Heavy classes (inference, ADR-0127 §5) are preemptible: they yield whenever a
// realtime transcode is active, exactly like background generation.
func (g *Governor) ClassAllowed(class string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeTranscodes == 0
}

// StartJob registers one in-flight job of the given class and returns a release
// func the caller MUST invoke when the job finishes. cancel is stored so
// CancelClass can preempt the job (typically a context.CancelFunc); a job that
// completes normally releases without ever being cancelled. StartJob does not
// itself gate on transcode preemption — callers check ClassAllowed first; this
// keeps the registry a pure lifecycle ledger the disable path can cancel.
func (g *Governor) StartJob(class string, cancel func()) (release func()) {
	return g.jobs.register(class, cancel)
}

// CancelClass cancels every in-flight job of the given class, returning how many
// were cancelled. It is the disable-cancels-jobs lifecycle path (ADR-0124
// decision 3 / ADR-0127 §5): disabling the AI capability calls
// CancelClass(ClassInference) so no inference job is orphaned past deactivation.
func (g *Governor) CancelClass(class string) int {
	return g.jobs.cancelClass(class)
}

// ActiveJobs reports how many in-flight jobs of the given class are registered
// — the counter the disable path drains to zero (ADR-0013 §6 observability).
func (g *Governor) ActiveJobs(class string) int {
	return g.jobs.activeClass(class)
}

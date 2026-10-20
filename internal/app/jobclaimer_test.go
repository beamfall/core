package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// feature:jobs — regression for the one-job-per-tick throughput cap:
// a queue of N quick background jobs must drain in ONE claim
// window (one drainBackgroundJobs call), not one job per ticker tick.
func TestDrainBackgroundJobsDrainsQueueInOneTickWindow(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()

	const kind = store.JobKind("test-drain-quick")
	var ran atomic.Int64
	beamfall.jobs.Register(kind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		ran.Add(1)
		return jobs.Result{}, nil
	})

	const queued = 5
	now := time.Now().UTC()
	for i := 0; i < queued; i++ {
		if _, err := beamfall.store.EnqueueJob(ctx, store.Job{Kind: kind, CreatedAt: now, RunAfter: now}); err != nil {
			t.Fatalf("EnqueueJob %d: %v", i, err)
		}
	}

	beamfall.drainBackgroundJobs(ctx)

	if got := ran.Load(); got != queued {
		t.Fatalf("jobs run after one drain = %d, want %d (queue must drain per tick, not one job per tick)", got, queued)
	}
}

func TestJobWakeBroadcastCoalescesWithoutLostWakeups(t *testing.T) {
	wake := newJobWake()
	const claimers = 4
	subscriptions := make([]<-chan struct{}, 0, claimers)
	for range claimers {
		ch, unsubscribe := wake.subscribe()
		t.Cleanup(unsubscribe)
		subscriptions = append(subscriptions, ch)
	}

	for range 100 {
		wake.notify()
	}
	assertEachClaimerWoken(t, subscriptions)
	assertNoClaimerWoken(t, subscriptions)

	wake.notify()
	assertEachClaimerWoken(t, subscriptions)
}

func TestJobClaimerStartupBacklogRunsImmediatelyAndCancels(t *testing.T) {
	beamfall := newJobClaimerTestApp(t)
	beamfall.jobClaimSafetyInterval = time.Hour

	const kind = store.JobKind("test-startup-wake")
	ran := make(chan struct{}, 1)
	beamfall.jobs.Register(kind, func(context.Context, store.Job) (jobs.Result, error) {
		ran <- struct{}{}
		return jobs.Result{}, nil
	})
	now := time.Now().UTC()
	if _, err := beamfall.store.EnqueueJob(t.Context(), store.Job{Kind: kind, CreatedAt: now, RunAfter: now}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		beamfall.runJobs(ctx)
		close(done)
	}()
	select {
	case <-ran:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("startup backlog was not claimed immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("job claimer goroutine did not stop after cancellation")
	}
}

func TestJobClaimerLocalEnqueueWakesBeforeSafetyPoll(t *testing.T) {
	beamfall := newJobClaimerTestApp(t)
	beamfall.jobClaimSafetyInterval = time.Hour

	const kind = store.JobKind("test-local-wake")
	ran := make(chan struct{}, 1)
	beamfall.jobs.Register(kind, func(context.Context, store.Job) (jobs.Result, error) {
		ran <- struct{}{}
		return jobs.Result{}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		beamfall.runJobs(ctx)
		close(done)
	}()
	waitForJobWakeSubscribers(t, beamfall, beamfall.governor.GenerationWorkers())

	now := time.Now().UTC()
	enqueuer := wakingScheduleStore{Store: beamfall.store, wake: beamfall.jobWake}
	if _, err := enqueuer.EnqueueJob(t.Context(), store.Job{Kind: kind, CreatedAt: now, RunAfter: now}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	select {
	case <-ran:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("local enqueue was not claimed before the one-hour safety poll")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("job claimers did not stop")
	}
}

func TestJobClaimerExternalInsertFoundBySafetyPoll(t *testing.T) {
	beamfall := newJobClaimerTestApp(t)
	beamfall.jobClaimSafetyInterval = 25 * time.Millisecond

	const kind = store.JobKind("test-external-poll")
	ran := make(chan struct{}, 1)
	beamfall.jobs.Register(kind, func(context.Context, store.Job) (jobs.Result, error) {
		ran <- struct{}{}
		return jobs.Result{}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		beamfall.runJobs(ctx)
		close(done)
	}()
	waitForJobWakeSubscribers(t, beamfall, beamfall.governor.GenerationWorkers())

	now := time.Now().UTC()
	if _, err := beamfall.store.EnqueueJob(t.Context(), store.Job{Kind: kind, CreatedAt: now, RunAfter: now}); err != nil {
		t.Fatalf("external EnqueueJob: %v", err)
	}
	select {
	case <-ran:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("external insert was not discovered by the safety poll")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("job claimers did not stop")
	}
}

func TestIdleJobClaimerDoesNotChurnClaims(t *testing.T) {
	st := newCountingJobStore(0)
	beamfall := newCountingJobApp(st)
	beamfall.jobClaimSafetyInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		beamfall.runJobClaimer(ctx, ctx)
		close(done)
	}()
	select {
	case <-st.firstClaim:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("initial claim did not run")
	}
	time.Sleep(50 * time.Millisecond)
	if got := st.claims.Load(); got != 1 {
		t.Fatalf("idle claim attempts = %d, want only the startup drain attempt", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle claimer did not stop")
	}
}

func BenchmarkJobClaimerWakeThroughput(b *testing.B) {
	st := newCountingJobStore(b.N)
	beamfall := newCountingJobApp(st)
	beamfall.jobs.Register(st.kind, func(context.Context, store.Job) (jobs.Result, error) {
		return jobs.Result{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	b.ResetTimer()
	go func() {
		beamfall.runJobClaimer(ctx, ctx)
		close(done)
	}()
	<-st.allCompleted
	cancel()
	<-done
	b.StopTimer()
	b.ReportMetric(float64(st.claims.Load())/float64(b.N), "claim-attempts/job")
}

func BenchmarkIdleJobClaimerClaimCount(b *testing.B) {
	st := newCountingJobStore(0)
	beamfall := newCountingJobApp(st)
	beamfall.jobClaimSafetyInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		beamfall.runJobClaimer(ctx, ctx)
		close(done)
	}()
	<-st.firstClaim
	initialClaims := st.claims.Load()

	b.ResetTimer()
	for b.Loop() {
		runtime.Gosched()
	}
	b.StopTimer()
	idleClaims := st.claims.Load() - initialClaims
	cancel()
	<-done
	b.ReportMetric(float64(idleClaims), "idle-claim-attempts")
}

func newJobClaimerTestApp(t *testing.T) *App {
	t.Helper()
	beamfall, err := New(t.Context(), testConfig(t), nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := beamfall.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return beamfall
}

func waitForJobWakeSubscribers(t *testing.T, beamfall *App, want int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		beamfall.jobWake.mu.Lock()
		got := len(beamfall.jobWake.subscribers)
		beamfall.jobWake.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("active job-wake subscribers did not reach %d", want)
}

func assertEachClaimerWoken(t *testing.T, subscriptions []<-chan struct{}) {
	t.Helper()
	for i, ch := range subscriptions {
		select {
		case <-ch:
		default:
			t.Fatalf("claimer %d missed wake", i)
		}
	}
}

func assertNoClaimerWoken(t *testing.T, subscriptions []<-chan struct{}) {
	t.Helper()
	for i, ch := range subscriptions {
		select {
		case <-ch:
			t.Fatalf("claimer %d received an uncoalesced duplicate wake", i)
		default:
		}
	}
}

type countingJobStore struct {
	mu           sync.Mutex
	kind         store.JobKind
	pending      int
	target       int64
	claims       atomic.Int64
	completed    atomic.Int64
	firstClaim   chan struct{}
	allCompleted chan struct{}
	firstOnce    sync.Once
	completeOnce sync.Once
}

func newCountingJobStore(pending int) *countingJobStore {
	st := &countingJobStore{
		kind:         "benchmark-job",
		pending:      pending,
		target:       int64(pending),
		firstClaim:   make(chan struct{}),
		allCompleted: make(chan struct{}),
	}
	if pending == 0 {
		close(st.allCompleted)
	}
	return st
}

func newCountingJobApp(st *countingJobStore) *App {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := jobs.NewRegistry(st, "benchmark-worker")
	registry.SetLogger(logger)
	return &App{
		jobs:                   registry,
		logger:                 logger,
		jobWake:                newJobWake(),
		jobClaimSafetyInterval: time.Hour,
	}
}

func (s *countingJobStore) ClaimNextJob(context.Context, string, []store.JobClass, time.Time) (store.Job, error) {
	claim := s.claims.Add(1)
	s.firstOnce.Do(func() { close(s.firstClaim) })
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == 0 {
		return store.Job{}, store.ErrNoRunnableJob
	}
	s.pending--
	return store.Job{
		ID:         store.JobID(fmt.Sprintf("job-%d", claim)),
		Kind:       s.kind,
		Class:      store.JobClassBackground,
		LeaseOwner: "benchmark-worker",
	}, nil
}

func (s *countingJobStore) CompleteJob(context.Context, store.JobID, string, time.Time) error {
	if s.completed.Add(1) == s.target {
		s.completeOnce.Do(func() { close(s.allCompleted) })
	}
	return nil
}

func (*countingJobStore) FailJob(context.Context, store.JobID, string, string, time.Time) error {
	return nil
}

func (*countingJobStore) YieldJob(context.Context, store.JobID, string, []byte, time.Time, time.Time) error {
	return nil
}

func (*countingJobStore) ResumeInterruptedJobs(context.Context, time.Time) (int, error) {
	return 0, nil
}

func (*countingJobStore) RecoverExpiredJobLeases(context.Context, time.Time) (int, error) {
	return 0, nil
}

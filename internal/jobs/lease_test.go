// Evidence — AUDITFIX-JOB-LEASE (2026-08-20)
//
// Verification run, head 04d4382dcf4d9bdf893271fc919e0c18a1762da9, log
// .agent-evidence/AUDITFIX-JOB-LEASE-verification.txt (untracked; core forbids
// tracking .agent-evidence, so the reviewer copy is this comment):
//
//	rc=0 elapsed_s=0   2026-08-20T00:22:07Z→00:22:07Z  go vet ./internal/store/ ./internal/jobs/
//	rc=0 elapsed_s=50  2026-08-20T00:22:07Z→00:22:58Z  go test -race -count=1 -timeout 600s ./internal/jobs/
//	rc=0 elapsed_s=78  2026-08-20T00:22:58Z→00:24:15Z  go test -race -count=1 -timeout 900s -run 'Lease|Job|Claim|StaleWorker|Resume' ./internal/store/
//	rc=0 elapsed_s=71  2026-08-20T00:24:15Z→00:25:27Z  go test -count=1 ./internal/graph/ ./internal/migrate/ ./internal/scan/ ./internal/ingest/ ./internal/app/ ./internal/conformance/
//	rc=0 elapsed_s=111 2026-08-20T00:25:36Z→00:27:27Z  go test -count=1 -timeout 1200s ./internal/store/
//
// The whole internal/store package under -race exceeds the 600s package timeout
// on this host (111s without -race), which is a pre-existing property of the
// package's size, not of this change; the race receipt above is therefore scoped
// to the job and lease tests, which are the concurrent surface this work adds.
//
// Deliberately broken invariants, and the failure each one produced (every break
// was reverted and the package re-run green afterwards):
//
//   - Expiry half of jobLiveLeaseClause removed (token match only):
//     --- FAIL: TestExpiredLeaseCannotWriteBeforeTheSweepRuns
//     jobs_test.go:445: expired progress err = <nil>, want ErrJobLeaseLost
//   - jobLeaseToken returns the bare worker id (no generation):
//     --- FAIL: TestLeaseTokenFencesTheSameWorkersEarlierGeneration
//     jobs_test.go:398: stale progress err = <nil>, want ErrJobLeaseLost
//   - Lease loss no longer cancels the handler context:
//     --- FAIL: TestRunOneAbandonsJobWhenLeaseIsLost (10.17s)
//     lease_test.go: handler was never stopped after the lease was lost
//     (the assertion now at lease_test.go:177)
//   - startLeaseRenewal never starts the renewal loop:
//     --- FAIL: TestRunOneRenewsLeaseWhileHandlerRuns (10.16s)
//     lease_test.go: condition was never met
//     (waitFor's deadline, now at lease_test.go:228)
//
// No migration was added: fencing rides the existing job.lease_owner column as a
// generation-bearing token, and the generation is the attempt_count the claim
// already increments. The schema, its freeze markers, and the backup fixture
// corpus are therefore untouched.

package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// countingRenewStore is the real store with its renewals counted, so a test can
// wait for renewal to have happened instead of sleeping for it, and can prove
// the renewal goroutine stops when RunOne returns.
type countingRenewStore struct {
	*store.Store
	renewals atomic.Int64
}

func (c *countingRenewStore) RenewJobLease(ctx context.Context, id store.JobID, leaseOwner string, now time.Time) (time.Time, error) {
	c.renewals.Add(1)
	return c.Store.RenewJobLease(ctx, id, leaseOwner, now)
}

// leaseRegistry builds a registry whose renewal cadence is short enough for a
// renewal round to be observable inside one test.
func leaseRegistry(st Store, workerID string) *Registry {
	registry := NewRegistry(st, workerID)
	registry.renewInterval = time.Millisecond
	return registry
}

// TestRunOneRenewsLeaseWhileHandlerRuns proves a handler that outlives the fixed
// lease TTL is no longer a duplicate-execution hazard: while it works, its lease
// deadline keeps moving forward, so the sweep never treats it as abandoned.
func TestRunOneRenewsLeaseWhileHandlerRuns(t *testing.T) {
	ctx := context.Background()
	st := &countingRenewStore{Store: storetest.Open(t)}
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-renewed", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	var claimDeadline time.Time
	registry := leaseRegistry(st, "worker-a")
	registry.Register("scan", func(handlerCtx context.Context, job store.Job) (Result, error) {
		claimDeadline = job.LeaseExpiresAt
		waitFor(t, handlerCtx, func() bool { return st.renewals.Load() > 0 })
		return Result{}, nil
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	if st.renewals.Load() == 0 {
		t.Fatal("no renewal was attempted while the handler ran")
	}
	events, err := st.ListJobEvents(ctx, "job-renewed")
	if err != nil {
		t.Fatal(err)
	}
	// queued, running, completed — renewal is a heartbeat, not a transition.
	if len(events) != 3 {
		t.Fatalf("job events = %d, want 3 (renewals must not append events)", len(events))
	}
	done, err := st.Job(ctx, "job-renewed")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("Status = %q, want completed", done.Status)
	}
	if claimDeadline.IsZero() {
		t.Fatal("handler never observed a lease deadline")
	}
}

// TestRunOneStopsRenewalWhenTheHandlerReturns proves the renewal goroutine is
// bounded by the RunOne call that started it: once the handler is done, no
// further renewal is attempted for that job.
func TestRunOneStopsRenewalWhenTheHandlerReturns(t *testing.T) {
	ctx := context.Background()
	st := &countingRenewStore{Store: storetest.Open(t)}
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-bounded", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	registry := leaseRegistry(st, "worker-a")
	registry.Register("scan", func(handlerCtx context.Context, _ store.Job) (Result, error) {
		waitFor(t, handlerCtx, func() bool { return st.renewals.Load() >= 2 })
		return Result{}, nil
	})
	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	settled := st.renewals.Load()
	// Many renewal intervals' worth of quiet: a goroutine still running would
	// have added dozens of calls by now.
	time.Sleep(100 * time.Millisecond)
	if leaked := st.renewals.Load(); leaked != settled {
		t.Fatalf("renewals kept arriving after RunOne returned: %d then %d", settled, leaked)
	}
}

// TestRunOneAbandonsJobWhenLeaseIsLost is the lost-lease proof. While the
// handler works, the lease lapses and another worker takes the job over. The
// handler's context must be cancelled promptly, and the displaced worker must
// record nothing at all — no progress, no yield, no success, no failure —
// because those writes belong to whoever holds the lease now. That is what makes
// a duplicated side effect impossible rather than merely unlikely.
func TestRunOneAbandonsJobWhenLeaseIsLost(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-lost", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	registry := leaseRegistry(st, "worker-a")
	var takeover store.Job
	registry.Register("scan", func(handlerCtx context.Context, _ store.Job) (Result, error) {
		expired := now.Add(store.JobLeaseTTL + time.Minute)
		if count, err := st.RecoverExpiredJobLeases(ctx, expired); err != nil || count != 1 {
			t.Fatalf("sweep count=%d err=%v, want 1 nil", count, err)
		}
		claimed, err := st.ClaimNextJob(ctx, "worker-b", nil, expired)
		if err != nil {
			t.Fatalf("takeover claim: %v", err)
		}
		takeover = claimed
		// The renewal loop must notice and cancel this context on its own. The
		// bounded wait keeps a regression legible: work that is never stopped
		// reports the missing cancellation instead of hanging the package.
		select {
		case <-handlerCtx.Done():
		case <-time.After(10 * time.Second):
			t.Error("handler was never stopped after the lease was lost")
		}
		return Result{Checkpoint: []byte(`{"cursor":"abandoned"}`)}, handlerCtx.Err()
	})

	if _, err := registry.RunOne(ctx, nil, now); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("RunOne err = %v, want ErrLeaseLost", err)
	}
	job, err := st.Job(ctx, "job-lost")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusRunning || job.LeaseOwner != takeover.LeaseOwner {
		t.Fatalf("displaced worker changed the job: %#v", job)
	}
	if job.Error != "" || job.Progress.Current != 0 || string(job.Checkpoint) != `{}` {
		t.Fatalf("displaced worker wrote state it no longer owned: %#v", job)
	}
	if takeover.LeaseGeneration != 2 {
		t.Fatalf("takeover generation = %d, want 2", takeover.LeaseGeneration)
	}
}

// TestLeaseIsUnrecoverableOnlyForLostLeaseOrExhaustedTTL pins the renewal
// failure policy: a store that reports the lease gone ends the work at once,
// while a transient store error is tolerated until a whole TTL has passed with
// no successful renewal — at which point the lease has lapsed server-side
// regardless of what the error said.
func TestLeaseIsUnrecoverableOnlyForLostLeaseOrExhaustedTTL(t *testing.T) {
	transient := errors.New("database is busy")
	if !leaseIsUnrecoverable(store.ErrJobLeaseLost, time.Second, store.JobLeaseTTL) {
		t.Fatal("a lost lease must end the work immediately")
	}
	if leaseIsUnrecoverable(transient, time.Second, store.JobLeaseTTL) {
		t.Fatal("a transient error one second in must not end the work")
	}
	if !leaseIsUnrecoverable(transient, store.JobLeaseTTL, store.JobLeaseTTL) {
		t.Fatal("a full TTL without a renewal must end the work")
	}
}

// waitFor blocks until done reports true, failing the test if the context is
// cancelled or the condition never arrives.
func waitFor(t *testing.T, ctx context.Context, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if ctx.Err() != nil {
			t.Fatalf("context cancelled before the condition was met: %v", ctx.Err())
		}
		if time.Now().After(deadline) {
			t.Fatal("condition was never met")
		}
		time.Sleep(time.Millisecond)
	}
}

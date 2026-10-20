package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestJobPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Unix(100, 0).UTC()
	first, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.EnqueueJob(ctx, Job{
		ID:         "job-scan-1",
		Kind:       "scan",
		Class:      JobClassBackground,
		Payload:    []byte(`{"library":"local"}`),
		Checkpoint: []byte(`{"cursor":"a"}`),
		CreatedAt:  now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != JobStatusQueued {
		t.Fatalf("Status = %q, want queued", created.Status)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	loaded, err := second.Job(ctx, "job-scan-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Payload) != `{"library":"local"}` {
		t.Fatalf("Payload = %s", loaded.Payload)
	}
	if string(loaded.Checkpoint) != `{"cursor":"a"}` {
		t.Fatalf("Checkpoint = %s", loaded.Checkpoint)
	}
}

func TestJobDedupeReturnsActiveAndAllowsAfterTerminal(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(100, 0).UTC()
	first, err := st.EnqueueJob(ctx, Job{ID: "job-first", Kind: "scan", DedupeKey: "library-local", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := st.EnqueueJob(ctx, Job{ID: "job-second", Kind: "scan", DedupeKey: "library-local", CreatedAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != first.ID {
		t.Fatalf("duplicate ID = %q, want %q", duplicate.ID, first.ID)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	next, err := st.EnqueueJob(ctx, Job{ID: "job-third", Kind: "scan", DedupeKey: "library-local", CreatedAt: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != "job-third" {
		t.Fatalf("next ID = %q, want job-third", next.ID)
	}
}

func TestClaimHonorsClassPriorityAndCancellation(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-background-low", Kind: "scan", Class: JobClassBackground, Priority: 1, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-background-high", Kind: "thumbs", Class: JobClassBackground, Priority: 10, CreatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-interactive", Kind: "probe", Class: JobClassInteractive, Priority: 100, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker", []JobClass{JobClassBackground}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "job-background-high" {
		t.Fatalf("claimed %q, want job-background-high", claimed.ID)
	}
	if err := st.CancelJob(ctx, "job-interactive", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNextJob(ctx, "worker", []JobClass{JobClassInteractive}, now.Add(4*time.Second)); !errors.Is(err, ErrNoRunnableJob) {
		t.Fatalf("interactive claim err = %v, want ErrNoRunnableJob", err)
	}
}

func TestYieldedJobKeepsCheckpointAndRunsAgain(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-yield", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.YieldJob(ctx, claimed.ID, claimed.LeaseOwner, []byte(`{"cursor":"b"}`), now.Add(time.Second), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	yielded, err := st.Job(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != JobStatusYielded {
		t.Fatalf("Status = %q, want yielded", yielded.Status)
	}
	if string(yielded.Checkpoint) != `{"cursor":"b"}` {
		t.Fatalf("Checkpoint = %s", yielded.Checkpoint)
	}
	again, err := st.ClaimNextJob(ctx, "worker-b", nil, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != claimed.ID || again.LeaseWorker != "worker-b" || again.LeaseGeneration != 2 {
		t.Fatalf("again = %#v", again)
	}
	if again.LeaseOwner == claimed.LeaseOwner {
		t.Fatalf("re-claim reused the fencing token %q", again.LeaseOwner)
	}
	if string(again.Checkpoint) != `{"cursor":"b"}` {
		t.Fatalf("again checkpoint = %s", again.Checkpoint)
	}
}

func TestResumeInterruptedJobsYieldsRunningJobs(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-resume", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNextJob(ctx, "worker", nil, now); err != nil {
		t.Fatal(err)
	}
	count, err := st.ResumeInterruptedJobs(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("resumed = %d, want 1", count)
	}
	job, err := st.Job(ctx, "job-resume")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobStatusYielded || job.LeaseOwner != "" {
		t.Fatalf("job after resume = %#v", job)
	}
}

func TestStaleWorkerCannotFinishReclaimedJob(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Lease deadlines are stamped from the server clock (leaseNow), never from
	// the caller's `now`, so a lease-timeline test has to be anchored to real
	// time; the sweep is then driven forward by naming a later `now`.
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-lease", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := st.RecoverExpiredJobLeases(ctx, now.Add(JobLeaseTTL+time.Minute)); err != nil || count != 1 {
		t.Fatalf("recover count=%d err=%v, want 1 nil", count, err)
	}
	reclaimed, err := st.ClaimNextJob(ctx, "worker-b", nil, now.Add(JobLeaseTTL+2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, now.Add(JobLeaseTTL+3*time.Minute)); !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("stale complete err = %v, want ErrJobNotRunning", err)
	}
	if err := st.CompleteJob(ctx, reclaimed.ID, reclaimed.LeaseOwner, now.Add(JobLeaseTTL+4*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestJobProgressWritesCheckpointEventsAndOutbox(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-progress", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJobCheckpoint(ctx, "job-progress", claimed.LeaseOwner, []byte(`{"cursor":"c"}`), JobProgress{Current: 7, Total: 10, Unit: "files", Message: "Scanning"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	job, err := st.Job(ctx, "job-progress")
	if err != nil {
		t.Fatal(err)
	}
	if string(job.Checkpoint) != `{"cursor":"c"}` || job.CheckpointVersion != 2 {
		t.Fatalf("checkpoint = %s version=%d", job.Checkpoint, job.CheckpointVersion)
	}
	if job.Progress.Current != 7 || job.Progress.Total != 10 || job.Progress.Unit != "files" {
		t.Fatalf("progress = %#v", job.Progress)
	}
	var eventCount int
	if err := st.eventsR.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_event WHERE job_id = ?", "job-progress").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount < 2 {
		t.Fatalf("job_event count = %d, want at least 2", eventCount)
	}
	var outboxCount int
	if err := st.eventsR.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ?", "job-progress").Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != eventCount {
		t.Fatalf("outbox count = %d, job_event count = %d", outboxCount, eventCount)
	}
}

func TestJobRetryRequeuesTerminalJobWithCheckpointAndEventsAreReadable(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, Job{
		ID:        "job-original",
		Kind:      "scan",
		Class:     JobClassBackground,
		Payload:   []byte(`{"library_id":"library-local"}`),
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker", nil, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJobCheckpoint(ctx, "job-original", claimed.LeaseOwner, []byte(`{"cursor":"abc"}`), JobProgress{Current: 3, Total: 10, Unit: "files", Message: "Scanning"}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := st.FailJob(ctx, "job-original", claimed.LeaseOwner, "failed", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListJobEvents(ctx, "job-original")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	retry, err := st.RetryJob(ctx, "job-original", now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID == "job-original" || retry.Status != JobStatusQueued || retry.Kind != "scan" || retry.Class != JobClassBackground {
		t.Fatalf("retry = %#v", retry)
	}
	if string(retry.Payload) != `{"library_id":"library-local"}` || string(retry.Checkpoint) != `{"cursor":"abc"}` {
		t.Fatalf("retry payload/checkpoint = %s / %s", retry.Payload, retry.Checkpoint)
	}
	if _, err := st.RetryJob(ctx, retry.ID, now.Add(5*time.Second)); !errors.Is(err, ErrJobNotTerminal) {
		t.Fatalf("retry active err = %v, want ErrJobNotTerminal", err)
	}
}

func TestJobStatsGroupsByClassAndStatus(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-stats-a", Kind: "scan", Class: JobClassBackground, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-stats-b", Kind: "probe", Class: JobClassInteractive, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	stats, err := st.JobStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[JobClass]map[JobStatus]int{}
	for _, row := range stats {
		if got[row.Class] == nil {
			got[row.Class] = map[JobStatus]int{}
		}
		got[row.Class][row.Status] = row.Count
	}
	if got[JobClassBackground][JobStatusQueued] != 1 || got[JobClassInteractive][JobStatusQueued] != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}

// leaseTestStore opens a store and enqueues one claimable job. Lease tests are
// anchored to real time because lease deadlines are stamped from the server
// clock; the caller's `now` moves only the sweep and the recorded operation
// timestamps.
func leaseTestStore(t *testing.T, id JobID) (*Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, Job{ID: id, Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return st, now
}

// TestLeaseTokenFencesTheSameWorkersEarlierGeneration is the ABA proof: worker-a
// loses its lease, and worker-a is also the one that picks the job back up. The
// worker id is identical across both leases, so only the generation embedded in
// the fencing token can tell them apart. Every conditional mutation must refuse
// the first-generation token while accepting the second.
func TestLeaseTokenFencesTheSameWorkersEarlierGeneration(t *testing.T) {
	ctx := context.Background()
	st, now := leaseTestStore(t, "job-aba")

	first, err := st.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	expired := now.Add(JobLeaseTTL + time.Minute)
	if count, err := st.RecoverExpiredJobLeases(ctx, expired); err != nil || count != 1 {
		t.Fatalf("recover count=%d err=%v, want 1 nil", count, err)
	}
	second, err := st.ClaimNextJob(ctx, "worker-a", nil, expired)
	if err != nil {
		t.Fatal(err)
	}
	if second.LeaseWorker != first.LeaseWorker {
		t.Fatalf("ABA setup broken: workers differ (%q vs %q)", first.LeaseWorker, second.LeaseWorker)
	}
	if second.LeaseGeneration != first.LeaseGeneration+1 {
		t.Fatalf("generation = %d, want %d", second.LeaseGeneration, first.LeaseGeneration+1)
	}

	stale := first.LeaseOwner
	if err := st.UpdateJobCheckpoint(ctx, "job-aba", stale, []byte(`{"cursor":"stale"}`), JobProgress{Current: 99}, expired); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale progress err = %v, want ErrJobLeaseLost", err)
	}
	if err := st.YieldJob(ctx, "job-aba", stale, []byte(`{"cursor":"stale"}`), expired, expired); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale yield err = %v, want ErrJobLeaseLost", err)
	}
	if err := st.FailJob(ctx, "job-aba", stale, "stale failure", expired); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale fail err = %v, want ErrJobLeaseLost", err)
	}
	if err := st.CompleteJob(ctx, "job-aba", stale, expired); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale complete err = %v, want ErrJobLeaseLost", err)
	}
	if _, err := st.RenewJobLease(ctx, "job-aba", stale, expired); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale renew err = %v, want ErrJobLeaseLost", err)
	}

	// None of the refused writes touched the job the live generation owns.
	held, err := st.Job(ctx, "job-aba")
	if err != nil {
		t.Fatal(err)
	}
	if held.Status != JobStatusRunning || held.LeaseOwner != second.LeaseOwner || held.Progress.Current != 0 {
		t.Fatalf("job after stale writes = %#v", held)
	}
	if err := st.CompleteJob(ctx, "job-aba", second.LeaseOwner, expired); err != nil {
		t.Fatalf("live generation could not complete: %v", err)
	}
}

// TestExpiredLeaseCannotWriteBeforeTheSweepRunsCloses the window the fixed
// 15-minute lease left open: between the moment a lease lapses and the moment
// RecoverExpiredJobLeases notices, the old holder could still record progress or
// finalize the job. Nothing here runs the sweep — expiry alone must refuse the
// write.
func TestExpiredLeaseCannotWriteBeforeTheSweepRuns(t *testing.T) {
	ctx := context.Background()
	st, now := leaseTestStore(t, "job-expired")

	claimed, err := st.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	// Force the recorded deadline into the past without moving the job out of
	// 'running': this is exactly the un-swept expired state.
	if _, err := st.eventsW.ExecContext(ctx, "UPDATE job SET lease_expires_at_ms = ? WHERE id = ?", timeToMillis(now.Add(-time.Second)), "job-expired"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJobCheckpoint(ctx, "job-expired", claimed.LeaseOwner, []byte(`{"cursor":"late"}`), JobProgress{Current: 5}, now); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("expired progress err = %v, want ErrJobLeaseLost", err)
	}
	if err := st.CompleteJob(ctx, "job-expired", claimed.LeaseOwner, now); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("expired complete err = %v, want ErrJobLeaseLost", err)
	}
	if err := st.FailJob(ctx, "job-expired", claimed.LeaseOwner, "late failure", now); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("expired fail err = %v, want ErrJobLeaseLost", err)
	}
	if _, err := st.RenewJobLease(ctx, "job-expired", claimed.LeaseOwner, now); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("expired renew err = %v, want ErrJobLeaseLost", err)
	}
	stale, err := st.Job(ctx, "job-expired")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != JobStatusRunning || stale.Progress.Current != 0 || stale.Error != "" {
		t.Fatalf("expired writes leaked into the job: %#v", stale)
	}
}

// TestRenewJobLeaseKeepsALongRunningJobAliveAndOutOfTheSweep proves renewal is
// what lets work outlive the fixed TTL: after a renewal the sweep no longer
// treats the job as abandoned, and the holder can still finalize it.
func TestRenewJobLeaseKeepsALongRunningJobAliveAndOutOfTheSweep(t *testing.T) {
	ctx := context.Background()
	st, now := leaseTestStore(t, "job-renew")

	claimed, err := st.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	// Wind the recorded deadline down to a minute out — the state a job reaches
	// when it has been working for most of its TTL — so the renewal has room to
	// visibly extend it.
	nearlyExpired := now.Add(time.Minute)
	if _, err := st.eventsW.ExecContext(ctx, "UPDATE job SET lease_expires_at_ms = ? WHERE id = ?", timeToMillis(nearlyExpired), "job-renew"); err != nil {
		t.Fatal(err)
	}
	renewedTo, err := st.RenewJobLease(ctx, "job-renew", claimed.LeaseOwner, nearlyExpired)
	if err != nil {
		t.Fatal(err)
	}
	if !renewedTo.After(nearlyExpired) {
		t.Fatalf("renewed deadline %v does not extend the near deadline %v", renewedTo, nearlyExpired)
	}
	renewed, err := st.Job(ctx, "job-renew")
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.LeaseExpiresAt.Equal(renewedTo) {
		t.Fatalf("persisted deadline = %v, want %v", renewed.LeaseExpiresAt, renewedTo)
	}
	// The sweep window that would have reclaimed the un-renewed lease now finds
	// nothing, because the renewal moved the deadline past it.
	afterOldDeadline := nearlyExpired.Add(time.Second)
	if count, err := st.RecoverExpiredJobLeases(ctx, afterOldDeadline); err != nil || count != 0 {
		t.Fatalf("recover count=%d err=%v, want 0 nil (renewed lease is live)", count, err)
	}
	if err := st.CompleteJob(ctx, "job-renew", claimed.LeaseOwner, afterOldDeadline); err != nil {
		t.Fatalf("renewed holder could not complete: %v", err)
	}
}

// TestLeaseSurvivesReopenAndStaysRecoverable is the crash/restart proof: the
// lease and its fencing token are durable, a restart does not silently hand the
// job to a new worker, and the sweep after the deadline is still what frees it.
func TestLeaseSurvivesReopenAndStaysRecoverable(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Now().UTC()
	first, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.EnqueueJob(ctx, Job{ID: "job-crash", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := first.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	reloaded, err := second.Job(ctx, "job-crash")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LeaseOwner != claimed.LeaseOwner || reloaded.LeaseGeneration != 1 {
		t.Fatalf("lease did not survive reopen: %#v", reloaded)
	}
	// A restarted peer cannot take a job whose lease is still live.
	if _, err := second.ClaimNextJob(ctx, "worker-b", nil, now); !errors.Is(err, ErrNoRunnableJob) {
		t.Fatalf("claim over a live lease err = %v, want ErrNoRunnableJob", err)
	}
	expired := now.Add(JobLeaseTTL + time.Minute)
	if count, err := second.RecoverExpiredJobLeases(ctx, expired); err != nil || count != 1 {
		t.Fatalf("recover count=%d err=%v, want 1 nil", count, err)
	}
	survivor, err := second.ClaimNextJob(ctx, "worker-b", nil, expired)
	if err != nil {
		t.Fatal(err)
	}
	if survivor.LeaseGeneration != 2 || string(survivor.Checkpoint) != `{}` {
		t.Fatalf("post-crash claim = %#v", survivor)
	}
	if err := second.CompleteJob(ctx, "job-crash", claimed.LeaseOwner, expired); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("pre-crash token completed the job: %v", err)
	}
}

// TestLeaseDeadlineIgnoresCallerSuppliedTime pins server-clock authority: a
// worker naming a far-future time neither extends its own lease nor is granted
// one, and a worker naming a long-past time cannot slip a write in behind an
// expired deadline. Only the store's clock moves the lease timeline.
func TestLeaseDeadlineIgnoresCallerSuppliedTime(t *testing.T) {
	ctx := context.Background()
	st, now := leaseTestStore(t, "job-clock")

	claimed, err := st.ClaimNextJob(ctx, "worker-a", nil, now.Add(72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.LeaseExpiresAt.After(now.Add(JobLeaseTTL + time.Hour)) {
		t.Fatalf("claim honoured the caller's future clock: deadline %v", claimed.LeaseExpiresAt)
	}
	renewedTo, err := st.RenewJobLease(ctx, "job-clock", claimed.LeaseOwner, now.Add(72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if renewedTo.After(now.Add(JobLeaseTTL + time.Hour)) {
		t.Fatalf("renewal honoured the caller's future clock: deadline %v", renewedTo)
	}
	// Expire the lease on the server timeline, then let the worker claim it is
	// still early. The write is refused regardless.
	if _, err := st.eventsW.ExecContext(ctx, "UPDATE job SET lease_expires_at_ms = ? WHERE id = ?", timeToMillis(now.Add(-time.Second)), "job-clock"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteJob(ctx, "job-clock", claimed.LeaseOwner, now.Add(-72*time.Hour)); !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("back-dated complete err = %v, want ErrJobLeaseLost", err)
	}
}

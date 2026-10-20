package jobs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestRegistryRunOneCompletesRegisteredJob(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-complete", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(st, "worker")
	registry.Register("scan", func(context.Context, store.Job) (Result, error) {
		return Result{}, nil
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	job, err := st.Job(ctx, "job-complete")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusCompleted {
		t.Fatalf("Status = %q, want completed", job.Status)
	}
}

func TestRegistryRunOneYieldsCheckpoint(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-yield", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(st, "worker")
	registry.Register("scan", func(context.Context, store.Job) (Result, error) {
		return Result{Checkpoint: []byte(`{"cursor":"d"}`), RunAfter: now.Add(time.Minute)}, ErrYielded
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	job, err := st.Job(ctx, "job-yield")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusYielded {
		t.Fatalf("Status = %q, want yielded", job.Status)
	}
	if string(job.Checkpoint) != `{"cursor":"d"}` {
		t.Fatalf("Checkpoint = %s", job.Checkpoint)
	}
}

func TestRegistryRunOneFailsOnHandlerError(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-fail", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(st, "worker")
	registry.Register("scan", func(context.Context, store.Job) (Result, error) {
		return Result{}, errors.New("boom")
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	job, err := st.Job(ctx, "job-fail")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusFailed || job.Error != "boom" {
		t.Fatalf("job = %#v", job)
	}
}

func TestRegistryRunOneMarksMissingHandlerFailed(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-missing", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(st, "worker")

	if _, err := registry.RunOne(ctx, nil, now); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("RunOne err = %v, want ErrNoHandler", err)
	}
	job, err := st.Job(ctx, "job-missing")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusFailed {
		t.Fatalf("Status = %q, want failed", job.Status)
	}
}

func TestRegistryRunOneRecoversHandlerPanic(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-panic", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(st, "worker")
	registry.Register("scan", func(context.Context, store.Job) (Result, error) {
		panic("boom in handler")
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	job, err := st.Job(ctx, "job-panic")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusFailed {
		t.Fatalf("Status = %q, want failed", job.Status)
	}
	if !strings.Contains(job.Error, "panic") || !strings.Contains(job.Error, "boom in handler") {
		t.Fatalf("Error = %q, want panic message", job.Error)
	}
}

func TestRegistryRunOnePersistsYieldWhenContextCanceledMidHandler(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(context.Background(), store.Job{ID: "job-cancel", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	registry := NewRegistry(st, "worker")
	registry.Register("scan", func(handlerCtx context.Context, _ store.Job) (Result, error) {
		cancel() // simulate shutdown arriving while the handler runs
		return Result{Checkpoint: []byte(`{"cursor":"mid"}`)}, ErrYielded
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	job, err := st.Job(context.Background(), "job-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusYielded {
		t.Fatalf("Status = %q, want yielded (resumable)", job.Status)
	}
	if job.LeaseOwner != "" {
		t.Fatalf("LeaseOwner = %q, want released lease", job.LeaseOwner)
	}
	if string(job.Checkpoint) != `{"cursor":"mid"}` {
		t.Fatalf("Checkpoint = %s, want mid-handler checkpoint", job.Checkpoint)
	}
}

// TestRegistryRunOneYieldsWhenShutdownCancelsHandler proves a durable job whose
// handler returns a cancellation error because the outer context was cancelled
// (shutdown, or a governor transcode preemption arriving mid-handler) is
// requeued from its checkpoint, not recorded as failed (ADR-0013 §4: durable
// jobs yield rather than fail).
func TestRegistryRunOneYieldsWhenShutdownCancelsHandler(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(context.Background(), store.Job{ID: "job-shutdown", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	registry := NewRegistry(st, "worker")
	registry.Register("scan", func(handlerCtx context.Context, _ store.Job) (Result, error) {
		cancel() // shutdown arrives mid-handler
		<-handlerCtx.Done()
		// The handler surfaces the cancellation as a plain error (not ErrYielded).
		return Result{Checkpoint: []byte(`{"cursor":"shutdown"}`)}, handlerCtx.Err()
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	job, err := st.Job(context.Background(), "job-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusYielded {
		t.Fatalf("Status = %q, want yielded (durable jobs resume, never fail on shutdown)", job.Status)
	}
	if string(job.Checkpoint) != `{"cursor":"shutdown"}` {
		t.Fatalf("Checkpoint = %s, want the mid-handler checkpoint preserved", job.Checkpoint)
	}
}

// TestRegistryRunOneStillFailsUnrelatedError proves the shutdown-yield guard is
// narrow: a handler error that is not a cancellation error still fails the job
// even if the outer context happens to be cancelled — unrelated error semantics
// are unchanged.
func TestRegistryRunOneStillFailsUnrelatedError(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(context.Background(), store.Job{ID: "job-realfail", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	registry := NewRegistry(st, "worker")
	registry.Register("scan", func(context.Context, store.Job) (Result, error) {
		cancel() // outer ctx is cancelled, but the handler's failure is unrelated
		return Result{}, errors.New("boom")
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	job, err := st.Job(context.Background(), "job-realfail")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != store.JobStatusFailed || job.Error != "boom" {
		t.Fatalf("job = %#v, want failed with real error preserved", job)
	}
}

// TestRedactLogErrorScrubsSecretClasses is the ADR-0013/ADR-0076 redaction
// proof: each secret class that could appear in a handler error string must be
// replaced by [REDACTED] before reaching the operational log. The test fails if
// any injected secret value appears verbatim in the debug log output.
func TestRedactLogErrorScrubsSecretClasses(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
		leaks  []string // substrings that must NOT appear in the log
	}{
		{
			name:   "API key",
			errMsg: "request failed for key bfk_AbCdEfGhIjKlMnOpQr: rate limited",
			leaks:  []string{"bfk_AbCdEfGhIjKlMnOpQr"},
		},
		{
			name:   "Bearer token",
			errMsg: "upstream rejected: Authorization: Bearer super-secret-token-value",
			leaks:  []string{"super-secret-token-value"},
		},
		{
			name:   "URL token query param",
			errMsg: "stream fetch failed: https://host/api/play?token=tok123&q=1",
			leaks:  []string{"tok123"},
		},
		{
			name:   "stream content path",
			errMsg: "read error on /api/media/direct/stream-token-abc/file.mp4",
			leaks:  []string{"stream-token-abc"},
		},
		{
			name:   "pack path",
			errMsg: "decrypt failed on /api/pack/content/pack-key-secret/chunk",
			leaks:  []string{"pack-key-secret"},
		},
		{
			name:   "private endpoint URL",
			errMsg: "connection refused: https://private-endpoint.internal:8080/secret/path",
			leaks:  []string{"private-endpoint.internal", "8080"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redacted := redactLogError(errors.New(tc.errMsg))
			for _, leak := range tc.leaks {
				if strings.Contains(redacted, leak) {
					t.Fatalf("redactLogError(%q) = %q; secret %q still present", tc.errMsg, redacted, leak)
				}
			}
			if !strings.Contains(redacted, "[REDACTED]") {
				t.Fatalf("redactLogError(%q) = %q; want [REDACTED] marker", tc.errMsg, redacted)
			}
		})
	}
}

// TestRunOneDoesNotLogOrPersistSecretInJobFailureDetail proves the debug log
// line and durable job failure state for a failing job handler never contain
// injected secret values. The handler returns an error whose message embeds a
// Bearer token and a stream URL; neither the log nor Console-readable job state
// may expose either.
func TestRunOneDoesNotLogOrPersistSecretInJobFailureDetail(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-secret", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry(st, "worker")
	registry.SetLogger(logger)
	registry.Register("scan", func(context.Context, store.Job) (Result, error) {
		// Simulate a handler that embeds a secret in its error (e.g. a failed
		// upstream request that includes a Bearer token or stream URL).
		return Result{}, errors.New("upstream error: Bearer secret-token-xyz, url=https://private.host/api/media/direct/abc123/file.mp4")
	})

	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("RunOne returned unexpected error: %v", err)
	}

	logOutput := logs.String()
	for _, secret := range []string{"secret-token-xyz", "private.host", "abc123"} {
		if strings.Contains(logOutput, secret) {
			t.Fatalf("log leaked secret %q:\n%s", secret, logOutput)
		}
	}
	if !strings.Contains(logOutput, "job failure detail") {
		t.Fatalf("expected 'job failure detail' log line; got:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] marker in log; got:\n%s", logOutput)
	}
	failed, err := st.Job(ctx, "job-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-token-xyz", "private.host", "abc123"} {
		if strings.Contains(failed.Error, secret) {
			t.Fatalf("stored job error leaked secret %q: %q", secret, failed.Error)
		}
	}
	if !strings.Contains(failed.Error, "[REDACTED]") {
		t.Fatalf("stored job error missing [REDACTED] marker: %q", failed.Error)
	}
	events, err := st.ListJobEvents(ctx, "job-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		for _, secret := range []string{"secret-token-xyz", "private.host", "abc123"} {
			if strings.Contains(string(ev.Payload), secret) {
				t.Fatalf("job event payload leaked secret %q: %+v", secret, ev)
			}
		}
	}
}

func TestRecoverExpiredLeasesRequeuesExpiredLease(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	// Lease deadlines are stamped from the server clock, so this test is
	// anchored to real time and moves the sweep forward by naming a later now.
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-lease", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(st, "worker-a")
	if _, err := st.ClaimNextJob(ctx, "worker-a", nil, now); err != nil {
		t.Fatal(err)
	}

	// Before the lease lapses nothing is recovered.
	count, err := registry.RecoverExpiredLeases(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("recovered = %d before lease expiry, want 0", count)
	}

	count, err = registry.RecoverExpiredLeases(ctx, now.Add(store.JobLeaseTTL+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("recovered = %d, want 1", count)
	}
	again, err := st.ClaimNextJob(ctx, "worker-b", nil, now.Add(store.JobLeaseTTL+2*time.Minute))
	if err != nil {
		t.Fatalf("reclaim after recovery returned error: %v", err)
	}
	if again.ID != "job-lease" || again.LeaseWorker != "worker-b" || again.LeaseGeneration != 2 {
		t.Fatalf("reclaimed job = %#v", again)
	}
}

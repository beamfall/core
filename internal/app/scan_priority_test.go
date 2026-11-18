package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/jobs"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

func TestGovernedSweepYieldsToScanAndResumes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g := governor.New()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	started := make(chan struct{})
	checkpoint := []byte(`{"cursor":"latest"}`)
	var calls atomic.Int32
	sweep := governedSweep(g, logger, func(ctx context.Context, _ store.Job) (jobs.Result, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return jobs.Result{Checkpoint: checkpoint}, ctx.Err()
		}
		return jobs.Result{Checkpoint: []byte(`{"cursor":"complete"}`)}, nil
	})
	type outcome struct {
		result jobs.Result
		err    error
	}
	done := make(chan outcome, 1)
	job := store.Job{ID: "sweep-1", Kind: "artwork-sweep", Checkpoint: []byte(`{"cursor":"old"}`)}
	go func() {
		result, err := sweep(ctx, job)
		done <- outcome{result, err}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("sweep did not start")
	}
	scan := scanYieldsToPlayback(g, logger, func(context.Context, store.Job) (jobs.Result, error) {
		select {
		case got := <-done:
			if !errors.Is(got.err, jobs.ErrYielded) || string(got.result.Checkpoint) != string(checkpoint) || got.result.RunAfter.IsZero() {
				t.Fatalf("preempted sweep = (%+v, %v), want checkpoint and retry", got.result, got.err)
			}
		case <-ctx.Done():
			t.Fatal("scan did not preempt sweep")
		}
		job.Checkpoint = checkpoint
		result, err := sweep(ctx, job)
		if !errors.Is(err, jobs.ErrYielded) || string(result.Checkpoint) != string(checkpoint) || result.RunAfter.IsZero() {
			t.Fatalf("refused sweep = (%+v, %v), want checkpoint and retry", result, err)
		}
		if calls.Load() != 1 {
			t.Fatal("inner sweep ran during scan")
		}
		return jobs.Result{}, nil
	})
	if _, err := scan(ctx, store.Job{ID: "scan-1", Kind: scanpkg.JobKind}); err != nil {
		t.Fatal(err)
	}
	result, err := sweep(ctx, job)
	if err != nil || string(result.Checkpoint) != `{"cursor":"complete"}` || calls.Load() != 2 {
		t.Fatalf("resumed sweep = (%+v, %v), calls = %d", result, err, calls.Load())
	}
	if line := logs.String(); !strings.Contains(line, "background sweep yielded to a library scan") || !strings.Contains(line, "job=sweep-1") {
		t.Fatalf("scan preemption not logged: %s", line)
	}
}

func TestGovernedGenerationIsNotCancelledByScan(t *testing.T) {
	g := governor.New()
	generation := governedGeneration(g, func(ctx context.Context, _ store.Job) (jobs.Result, error) {
		scan := scanYieldsToPlayback(g, nil, func(context.Context, store.Job) (jobs.Result, error) {
			if ctx.Err() != nil {
				t.Fatalf("ordinary generation cancelled during scan: %v", context.Cause(ctx))
			}
			return jobs.Result{}, nil
		})
		if _, err := scan(ctx, store.Job{Kind: scanpkg.JobKind}); err != nil {
			t.Fatalf("scan refused while ordinary generation runs: %v", err)
		}
		return jobs.Result{Checkpoint: []byte("complete")}, ctx.Err()
	})
	result, err := generation(context.Background(), store.Job{Kind: "convert"})
	if err != nil || string(result.Checkpoint) != "complete" {
		t.Fatalf("generation = (%+v, %v), want completion", result, err)
	}
}

func TestQueuedScanPreemptsSweepHoldingOnlyWorker(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	g := governor.New() // One worker: the scan cannot begin its own lease yet.
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	registry := jobs.NewRegistry(st, "scan-priority-test")
	registry.SetLogger(logger)
	started, scanned := make(chan struct{}), make(chan struct{})
	checkpoint := []byte(`{"cursor":"saved"}`)
	registry.Register("artwork-sweep", governedSweep(g, logger, func(ctx context.Context, _ store.Job) (jobs.Result, error) {
		close(started)
		<-ctx.Done()
		return jobs.Result{Checkpoint: checkpoint}, ctx.Err()
	}))
	registry.Register(scanpkg.JobKind, scanYieldsToPlayback(g, logger, func(context.Context, store.Job) (jobs.Result, error) {
		close(scanned)
		return jobs.Result{}, nil
	}))
	a := &App{
		store: st, jobs: registry, governor: g, logger: logger,
		jobWake: newJobWake(), jobClaimSafetyInterval: time.Hour,
	}
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "held-sweep", Kind: "artwork-sweep", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Give the worker its full deadline after the store fixture is initialized.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		a.runJobs(ctx)
	}()
	defer func() {
		cancel()
		<-stopped
		if t.Failed() {
			t.Logf("worker logs:\n%s", logs.String())
		}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("worker did not start sweep")
	}
	job, err := scanpkg.NewJob(scanpkg.JobRequest{ID: "waiting-scan", LibraryID: "lib-1", Root: t.TempDir(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	select {
	case <-scanned:
	case <-ctx.Done():
		t.Fatal("queued scan could not claim worker held by sweep")
	}
	yielded, err := st.Job(ctx, "held-sweep")
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != store.JobStatusYielded || string(yielded.Checkpoint) != string(checkpoint) || !yielded.RunAfter.After(now) {
		t.Fatalf("sweep did not persist its yield: %+v", yielded)
	}
	cancel()
	<-stopped
	if count := strings.Count(logs.String(), "background sweeps preempted for a queued library scan"); count != 1 {
		t.Fatalf("queued-scan preemption logged %d times, want 1: %s", count, logs.String())
	}
}

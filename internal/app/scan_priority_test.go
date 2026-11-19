package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/jobs"
	scanpkg "github.com/beamfall/core/internal/scan"
	schedulepkg "github.com/beamfall/core/internal/schedule"
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

func TestAssetSweepScheduleBuildsCanonicalJob(t *testing.T) {
	now := time.Now().UTC()
	want, err := genassets.NewSweepJob(now)
	if err != nil {
		t.Fatal(err)
	}
	spec := singleJobSchedule(genassets.SweepJobKind, schedulepkg.Daily(3, 0))
	got, err := spec.BuildJobs(context.Background(), now)
	if err != nil || len(got) != 1 {
		t.Fatalf("scheduled jobs = %v, %v", got, err)
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("scheduled job = %#v, want %#v", got[0], want)
	}
}

func TestScanWithoutNewMediaDoesNotEnqueueCatchUp(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer beamfall.Close()
	now := time.Now().UTC()
	job, err := scanpkg.NewJob(scanpkg.JobRequest{LibraryID: "library-local", Root: t.TempDir(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := beamfall.store.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	ran, err := beamfall.jobs.RunOne(ctx, nil, now)
	if err != nil || ran.Kind != scanpkg.JobKind {
		t.Fatalf("scan = %s, %v", ran.Kind, err)
	}
	queued, err := beamfall.store.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range queued {
		if job.Kind == genassets.SweepJobKind || job.Kind == genassets.CatchUpJobKind {
			t.Fatalf("empty scan queued assets: %+v", job)
		}
	}
}

func TestCatchUpRegistrationYieldsToScan(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer beamfall.Close()
	// Earlier than boot artwork jobs so this exact registered handler is claimed.
	now := time.Unix(100, 0).UTC()
	job, err := genassets.NewCatchUpJob(now, 0)
	if err != nil {
		t.Fatal(err)
	}
	created, err := beamfall.store.EnqueueJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	lease, ok := beamfall.governor.BeginScan(ctx)
	if !ok {
		t.Fatal("scan lease refused")
	}
	ran, runErr := beamfall.jobs.RunOne(ctx, nil, now)
	lease.Release()
	if ran.ID != created.ID || runErr != nil {
		t.Fatalf("catch-up during scan = %s, %v", ran.ID, runErr)
	}
	persisted, err := beamfall.store.Job(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != store.JobStatusYielded {
		t.Fatalf("catch-up status = %s, want yielded", persisted.Status)
	}
	// The admission refusal must not generate or advance a rowid checkpoint.
	var checkpoint map[string]any
	if err := json.Unmarshal(persisted.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, ok := checkpoint["last_rowid"]; ok {
		t.Fatalf("refused catch-up ran: %s", persisted.Checkpoint)
	}
}

func TestAssetCatchUpArmsOnlyForMediaAboveFloorAndKeepsEarliestFloor(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer beamfall.Close()
	latest, err := beamfall.store.MaxMediaRowID(ctx)
	if err != nil || latest < 2 {
		t.Fatalf("seeded media rowid = %d, %v", latest, err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catchUps := func() []store.Job {
		t.Helper()
		all, err := beamfall.store.ListJobs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var found []store.Job
		for _, job := range all {
			if job.Kind == genassets.CatchUpJobKind {
				found = append(found, job)
			}
		}
		return found
	}
	enqueueAssetCatchUp(ctx, beamfall.store, logger, beamfall.jobWake, latest)
	if got := catchUps(); len(got) != 0 {
		t.Fatalf("no new media armed a catch-up: %+v", got)
	}
	// Two scan invocations (the first yielded): the queued catch-up keeps the
	// first invocation's floor, so media from both batches is covered.
	enqueueAssetCatchUp(ctx, beamfall.store, logger, beamfall.jobWake, latest-2)
	enqueueAssetCatchUp(ctx, beamfall.store, logger, beamfall.jobWake, latest-1)
	got := catchUps()
	if len(got) != 1 {
		t.Fatalf("catch-up jobs = %d, want 1 deduped", len(got))
	}
	var payload struct {
		AfterRowID int64 `json:"after_rowid"`
	}
	if err := json.Unmarshal(got[0].Payload, &payload); err != nil || payload.AfterRowID != latest-2 {
		t.Fatalf("catch-up floor = %d, %v; want %d", payload.AfterRowID, err, latest-2)
	}
}

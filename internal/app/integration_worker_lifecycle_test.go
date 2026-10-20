package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestIntegrationManagerFencesCrashRestartDisableAndBoundsStop(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{PluginID: pluginID, State: "running"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	var launches atomic.Int32
	var active atomic.Int32
	var maximum atomic.Int32
	restarted := make(chan struct{})
	m := &integrationManager{
		st: st, pluginID: pluginID, logger: slog.Default(),
		waitRestart: func(context.Context, time.Duration) error { return nil },
		runSupervisor: func(runCtx context.Context) error {
			launch := launches.Add(1)
			current := active.Add(1)
			defer active.Add(-1)
			for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); observed = maximum.Load() {
			}
			if launch == 1 {
				return integrationhost.ErrWorkerExited
			}
			if launch == 2 {
				close(restarted)
			}
			<-runCtx.Done()
			return context.Cause(runCtx)
		},
	}
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not restart after crash")
	}
	if err := m.Run(ctx); !errors.Is(err, errIntegrationManagerRunning) {
		t.Fatalf("overlapping manager Run = %v, want already-running fence", err)
	}
	worker, _, err := st.IntegrationWorker(ctx, pluginID)
	if err != nil {
		t.Fatal(err)
	}
	worker.State = "disabled"
	if err := st.SetIntegrationWorker(ctx, worker, time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, integrationhost.ErrPluginDeactivated) {
			t.Fatalf("disable result = %v, want plugin deactivated", err)
		}
	case <-time.After(time.Second):
		t.Fatal("disable did not retire the current generation")
	}
	if got := launches.Load(); got != 2 {
		t.Fatalf("launches = %d, want crash + one replacement", got)
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("overlapping worker generations = %d", got)
	}
	worker, _, err = st.IntegrationWorker(ctx, pluginID)
	if err != nil {
		t.Fatal(err)
	}
	if worker.State != "disabled" {
		t.Fatalf("disable retirement rewrote state to %q", worker.State)
	}

	bounded := &integrationManager{stopBound: 10 * time.Millisecond}
	if err := bounded.waitForRetirement(make(chan error)); !errors.Is(err, errIntegrationWorkerStop) {
		t.Fatalf("bounded stop = %v, want timeout", err)
	}
}

func TestIntegrationManagerDoesNotRestartWhileAdmittedPublicationIsRunning(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{PluginID: pluginID, State: "running"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	publicationStarted := make(chan struct{})
	releasePublication := make(chan struct{})
	release := sync.OnceFunc(func() { close(releasePublication) })
	defer release()
	publicationDone := make(chan struct{})
	var launches atomic.Int32
	m := &integrationManager{
		st: st, pluginID: pluginID, logger: slog.Default(), stopBound: 20 * time.Millisecond,
		waitRestart: func(context.Context, time.Duration) error { return nil },
	}
	m.runSupervisor = func(context.Context) error {
		if launches.Add(1) != 1 {
			return errors.New("replacement started before prior publication retired")
		}
		lease := m.lease
		go func() {
			defer close(publicationDone)
			_ = lease.Guard(func() {
				close(publicationStarted)
				<-releasePublication
			})
		}()
		<-publicationStarted
		return integrationhost.ErrWorkerExited
	}

	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	var err error
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bounded retirement did not return")
	}
	release()
	select {
	case <-publicationDone:
	case <-time.After(3 * time.Second):
		t.Fatal("publication did not finish after release")
	}
	if !errors.Is(err, errIntegrationWorkerStop) {
		t.Fatalf("crash with admitted publication = %v, want bounded retirement error", err)
	}
	if got := launches.Load(); got != 1 {
		t.Fatalf("worker launches = %d, want no replacement while publication is running", got)
	}
}

package integrationhost

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGenerationRetireWaitsForLaterGuardAfterCompletedGuard(t *testing.T) {
	var fence GenerationFence
	lease := fence.Acquire()

	if err := lease.Guard(func() {}); err != nil {
		t.Fatalf("first guard: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- lease.Guard(func() {
			close(entered)
			<-release
		})
	}()
	<-entered

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := lease.Retire(timeoutCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Retire with a blocked guard = %v, want context.DeadlineExceeded", err)
	}

	ran := false
	if err := lease.Guard(func() { ran = true }); !errors.Is(err, ErrWorkerGenerationRetired) {
		t.Fatalf("guard after retirement = %v, want ErrWorkerGenerationRetired", err)
	}
	if ran {
		t.Fatal("guard after retirement ran its work")
	}

	retired := make(chan error, 1)
	go func() { retired <- lease.Retire(context.Background()) }()
	select {
	case err := <-retired:
		t.Fatalf("Retire returned %v while a guard was still running", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	if err := <-guardDone; err != nil {
		t.Fatalf("blocked guard: %v", err)
	}
	select {
	case err := <-retired:
		if err != nil {
			t.Fatalf("Retire after drain = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Retire did not return after the last guard drained")
	}
	if lease.Current() {
		t.Fatal("retired lease still reports current")
	}
}

func TestGenerationRetireWithoutGuardsReturnsImmediately(t *testing.T) {
	var fence GenerationFence
	lease := fence.Acquire()
	if err := lease.Guard(func() {}); err != nil {
		t.Fatalf("guard: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lease.Retire(ctx); err != nil {
		t.Fatalf("Retire = %v, want nil", err)
	}
	if err := lease.Retire(ctx); err != nil {
		t.Fatalf("second Retire = %v, want nil", err)
	}
}

func TestGenerationAcquireRetiresPreviousLeaseAfterItsGuardsDrain(t *testing.T) {
	var fence GenerationFence
	first := fence.Acquire()
	if err := first.Guard(func() {}); err != nil {
		t.Fatalf("first guard: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- first.Guard(func() {
			close(entered)
			<-release
		})
	}()
	<-entered

	second := fence.Acquire()
	if first.Current() || !second.Current() {
		t.Fatal("Acquire did not transfer ownership to the new lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := first.Retire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Retire of superseded lease with a blocked guard = %v, want context.DeadlineExceeded", err)
	}

	close(release)
	if err := <-guardDone; err != nil {
		t.Fatalf("blocked guard: %v", err)
	}
	if err := first.Retire(context.Background()); err != nil {
		t.Fatalf("Retire after drain = %v, want nil", err)
	}
}

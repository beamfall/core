package governor

import (
	"context"
	"testing"
)

// TestInferenceClassPreemptedByTranscode proves the inference job class is a
// heavy preemptible class: it yields whenever a
// realtime transcode is active, exactly like background generation.
func TestInferenceClassPreemptedByTranscode(t *testing.T) {
	g := New()
	if !g.ClassAllowed(ClassInference) {
		t.Fatal("inference blocked with no transcodes")
	}
	g.TranscodeStarted()
	if g.ClassAllowed(ClassInference) {
		t.Fatal("inference allowed while a transcode is active — must be preempted")
	}
	g.TranscodeStopped()
	if !g.ClassAllowed(ClassInference) {
		t.Fatal("inference still blocked after the transcode ended")
	}
}

// TestDisableCancelsInFlightInferenceJobs is the disable-cancels-jobs lifecycle:
// CancelClass cancels every in-flight
// inference job through its registered cancel func and drains the class to zero,
// never orphaning a running job.
func TestDisableCancelsInFlightInferenceJobs(t *testing.T) {
	g := New()

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()

	releaseA := g.StartJob(ClassInference, cancelA)
	g.StartJob(ClassInference, cancelB)
	if got := g.ActiveJobs(ClassInference); got != 2 {
		t.Fatalf("active inference jobs = %d, want 2", got)
	}

	// A normally-completed job deregisters and is not later cancelled.
	releaseA()
	if got := g.ActiveJobs(ClassInference); got != 1 {
		t.Fatalf("active inference jobs after one release = %d, want 1", got)
	}

	// Disable cancels the remaining in-flight job(s) through the governor.
	cancelled := g.CancelClass(ClassInference)
	if cancelled != 1 {
		t.Fatalf("CancelClass cancelled %d jobs, want 1 (the still-in-flight one)", cancelled)
	}
	if got := g.ActiveJobs(ClassInference); got != 0 {
		t.Fatalf("active inference jobs after disable = %d, want 0 — a job was orphaned", got)
	}

	// The completed job's context was never cancelled; the in-flight one was.
	if ctxA.Err() != nil {
		t.Fatal("a completed inference job was cancelled by disable — must be left untouched")
	}
	if ctxB.Err() == nil {
		t.Fatal("an in-flight inference job survived disable — must be cancelled")
	}
}

// TestCancelClassIsolatesClasses proves CancelClass cancels only the named
// class, leaving other classes' in-flight jobs untouched.
func TestCancelClassIsolatesClasses(t *testing.T) {
	g := New()
	otherCancelled := false
	g.StartJob(ClassInference, func() {})
	g.StartJob("other", func() { otherCancelled = true })

	g.CancelClass(ClassInference)
	if g.ActiveJobs(ClassInference) != 0 {
		t.Fatal("inference class not drained")
	}
	if g.ActiveJobs("other") != 1 || otherCancelled {
		t.Fatal("CancelClass leaked into an unrelated class")
	}
}

// TestNilCancelJobOccupiesClassWithoutEffect confirms a job registered with a
// nil cancel still counts toward the class and releases cleanly.
func TestNilCancelJobOccupiesClassWithoutEffect(t *testing.T) {
	g := New()
	release := g.StartJob(ClassInference, nil)
	if g.ActiveJobs(ClassInference) != 1 {
		t.Fatal("nil-cancel job did not register")
	}
	if n := g.CancelClass(ClassInference); n != 1 {
		t.Fatalf("CancelClass counted %d, want 1", n)
	}
	release() // releasing an already-cancelled job is a safe no-op
	if g.ActiveJobs(ClassInference) != 0 {
		t.Fatal("class not empty after cancel + release")
	}
}

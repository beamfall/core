package governor

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestBeginTranscodeBlocksLaterGeneration proves admission ordering
// transcode-then-generation: once BeginTranscode has linearized, no generation
// may be admitted until the transcode lease releases — the "no generation
// admission after transcode linearization" invariant.
func TestBeginTranscodeBlocksLaterGeneration(t *testing.T) {
	g := NewWithOptions(Options{BackgroundCPUBudget: 4})
	lease := g.BeginTranscode()
	if g.ActiveTranscodes() != 1 {
		t.Fatalf("ActiveTranscodes = %d, want 1", g.ActiveTranscodes())
	}
	if _, ok := g.BeginGeneration(context.Background()); ok {
		t.Fatal("generation admitted after a transcode linearized — preemption breached")
	}
	if g.GenerationAllowed() {
		t.Fatal("GenerationAllowed true while a transcode is active")
	}
	lease.Release()
	if g.ActiveTranscodes() != 0 {
		t.Fatalf("ActiveTranscodes after release = %d, want 0", g.ActiveTranscodes())
	}
	gen, ok := g.BeginGeneration(context.Background())
	if !ok {
		t.Fatal("generation not admitted after the transcode released")
	}
	gen.Release()
}

// TestBeginTranscodeCancelsInFlightGeneration proves admission ordering
// generation-then-transcode: an in-flight generation lease is cancelled with
// ErrPreempted the moment a transcode begins, so the generation yields rather
// than competing.
func TestBeginTranscodeCancelsInFlightGeneration(t *testing.T) {
	g := New()
	gen, ok := g.BeginGeneration(context.Background())
	if !ok {
		t.Fatal("generation not admitted on an idle governor")
	}
	if gen.Context().Err() != nil {
		t.Fatal("generation context cancelled before any transcode")
	}

	g.BeginTranscode()

	select {
	case <-gen.Context().Done():
	default:
		t.Fatal("generation context not cancelled by a beginning transcode")
	}
	if cause := context.Cause(gen.Context()); !errors.Is(cause, ErrPreempted) {
		t.Fatalf("cancel cause = %v, want ErrPreempted", cause)
	}
	// The preempted generation still counts against the budget until it releases;
	// releasing it returns the slot exactly once.
	gen.Release()
	if g.ActiveGeneration() != 0 {
		t.Fatalf("ActiveGeneration after release = %d, want 0", g.ActiveGeneration())
	}
}

// TestTranscodeLeaseRollbackResumesGeneration proves the launch-failure rollback
// path: releasing a transcode lease (as the session does when FFmpeg fails to
// launch) drops the transcode count to zero and lets generation resume.
func TestTranscodeLeaseRollbackResumesGeneration(t *testing.T) {
	g := New()
	lease := g.BeginTranscode()
	if g.GenerationAllowed() {
		t.Fatal("generation allowed while the (failed-to-launch) transcode lease is held")
	}
	lease.Release() // FFmpeg launch failed: roll the start back
	if !g.GenerationAllowed() {
		t.Fatal("generation still blocked after the transcode lease rolled back")
	}
	if g.ActiveTranscodes() != 0 {
		t.Fatalf("ActiveTranscodes after rollback = %d, want 0", g.ActiveTranscodes())
	}
}

// TestTranscodeLeaseReleaseIdempotent proves double/stale release of a transcode
// lease is a no-op: the count returns to zero exactly once and a second release
// never drives it negative (which would wrongly reopen generation past budget).
func TestTranscodeLeaseReleaseIdempotent(t *testing.T) {
	g := New()
	a := g.BeginTranscode()
	b := g.BeginTranscode()
	if g.ActiveTranscodes() != 2 {
		t.Fatalf("ActiveTranscodes = %d, want 2", g.ActiveTranscodes())
	}
	a.Release()
	a.Release() // double release: no-op
	a.Release()
	if g.ActiveTranscodes() != 1 {
		t.Fatalf("ActiveTranscodes after idempotent double release = %d, want 1", g.ActiveTranscodes())
	}
	b.Release()
	if g.ActiveTranscodes() != 0 {
		t.Fatalf("ActiveTranscodes = %d, want 0", g.ActiveTranscodes())
	}
}

// TestGenerationLeaseReleaseIdempotent proves double/stale/reaper release of a
// generation lease returns its budget slot exactly once, leaving the budget
// intact for the next admission.
func TestGenerationLeaseReleaseIdempotent(t *testing.T) {
	g := NewWithOptions(Options{BackgroundCPUBudget: 1})
	gen, ok := g.BeginGeneration(context.Background())
	if !ok {
		t.Fatal("generation not admitted")
	}
	gen.Release()
	gen.Release() // reaper/double release: no-op
	if g.ActiveGeneration() != 0 {
		t.Fatalf("ActiveGeneration after double release = %d, want 0", g.ActiveGeneration())
	}
	// The single-slot budget is fully reusable — the double release did not leak a
	// phantom slot or drive the count negative.
	next, ok := g.BeginGeneration(context.Background())
	if !ok {
		t.Fatal("budget slot not reusable after an idempotent release")
	}
	if _, ok := g.BeginGeneration(context.Background()); ok {
		t.Fatal("second slot admitted past a budget of 1 — release drove the count negative")
	}
	next.Release()
}

// TestGenerationCancelVsComplete proves the two cancel-vs-complete orderings map
// to distinct causes: a generation that completes (releases) before a transcode
// arrives is never marked preempted, while one still in flight when the
// transcode begins is. This is the discriminator governedGeneration keys on to
// decide yield-vs-complete.
func TestGenerationCancelVsComplete(t *testing.T) {
	// Complete-before-preempt: release first, then a transcode begins.
	g := New()
	completed, _ := g.BeginGeneration(context.Background())
	completed.Release()
	g.BeginTranscode()
	if errors.Is(context.Cause(completed.Context()), ErrPreempted) {
		t.Fatal("a generation that completed before the transcode was marked preempted")
	}

	// Preempt-before-complete: the transcode begins while the generation is in
	// flight, then it releases.
	g2 := New()
	inflight, _ := g2.BeginGeneration(context.Background())
	g2.BeginTranscode()
	if !errors.Is(context.Cause(inflight.Context()), ErrPreempted) {
		t.Fatal("an in-flight generation preempted by a transcode was not marked preempted")
	}
	inflight.Release()
}

// TestBudgetCapsConcurrentGenerationLeases proves the background-CPU budget caps
// concurrent generation leases and that a freed slot is reusable.
func TestBudgetCapsConcurrentGenerationLeases(t *testing.T) {
	g := NewWithOptions(Options{BackgroundCPUBudget: 2})
	a, ok := g.BeginGeneration(context.Background())
	if !ok {
		t.Fatal("first slot blocked under a fresh budget of 2")
	}
	b, ok := g.BeginGeneration(context.Background())
	if !ok {
		t.Fatal("second slot blocked under a budget of 2")
	}
	if _, ok := g.BeginGeneration(context.Background()); ok {
		t.Fatal("third slot admitted past a budget of 2")
	}
	a.Release()
	c, ok := g.BeginGeneration(context.Background())
	if !ok {
		t.Fatal("freed slot not reusable")
	}
	b.Release()
	c.Release()
	if g.ActiveGeneration() != 0 {
		t.Fatalf("ActiveGeneration = %d, want 0", g.ActiveGeneration())
	}
}

// TestConcurrentAdmissionNeverExceedsBudgetOrLeaks is the race proof (run under
// -race): many goroutines contend BeginTranscode/BeginGeneration/Release against
// one governor. The single mutex must keep the budget from ever being exceeded
// and both counters must return to exactly zero once every lease is released —
// no lost or double decrement.
func TestConcurrentAdmissionNeverExceedsBudgetOrLeaks(t *testing.T) {
	const budget = 3
	g := NewWithOptions(Options{BackgroundCPUBudget: budget})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lease, ok := g.BeginGeneration(context.Background()); ok {
				if got := g.ActiveGeneration(); got > budget {
					t.Errorf("ActiveGeneration = %d, exceeds budget %d", got, budget)
				}
				lease.Release()
			}
			tl := g.BeginTranscode()
			tl.Release()
			g.AcquireGeneration()
			g.ReleaseGeneration()
		}()
	}
	wg.Wait()

	if got := g.ActiveTranscodes(); got != 0 {
		t.Fatalf("ActiveTranscodes leaked: %d, want 0", got)
	}
	if got := g.ActiveGeneration(); got != 0 {
		t.Fatalf("ActiveGeneration leaked: %d, want 0", got)
	}
	// The governor is fully drained: a fresh full budget of generation admits.
	for i := 0; i < budget; i++ {
		if _, ok := g.BeginGeneration(context.Background()); !ok {
			t.Fatalf("slot %d not admittable on a drained governor", i)
		}
	}
}

// TestParentCancellationPropagatesWithoutPreemptCause proves a generation lease
// whose parent context is cancelled (shutdown) reports context.Canceled, not
// ErrPreempted — so governedGeneration does not misclassify a shutdown as a
// transcode preemption. "Unrelated cancellation semantics remain unchanged."
func TestParentCancellationPropagatesWithoutPreemptCause(t *testing.T) {
	g := New()
	parent, cancel := context.WithCancel(context.Background())
	gen, ok := g.BeginGeneration(parent)
	if !ok {
		t.Fatal("generation not admitted")
	}
	cancel() // shutdown, not a transcode
	<-gen.Context().Done()
	if errors.Is(context.Cause(gen.Context()), ErrPreempted) {
		t.Fatal("a parent (shutdown) cancellation was misreported as transcode preemption")
	}
	if !errors.Is(context.Cause(gen.Context()), context.Canceled) {
		t.Fatalf("cancel cause = %v, want context.Canceled", context.Cause(gen.Context()))
	}
	gen.Release()
}

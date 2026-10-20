package governor

import "testing"

func TestGenerationYieldsToActiveTranscodes(t *testing.T) {
	g := New()
	if !g.GenerationAllowed() {
		t.Fatal("generation blocked with no transcodes")
	}
	g.TranscodeStarted()
	g.TranscodeStarted()
	if g.GenerationAllowed() {
		t.Fatal("generation allowed during transcodes")
	}
	g.TranscodeStopped()
	if g.GenerationAllowed() {
		t.Fatal("generation allowed with one transcode still active")
	}
	g.TranscodeStopped()
	if !g.GenerationAllowed() {
		t.Fatal("generation blocked after all transcodes stopped")
	}
	g.TranscodeStopped() // underflow guard
	if !g.GenerationAllowed() {
		t.Fatal("underflow broke the gate")
	}
}

// TestGenerationThrottlesUnderBudget proves the background-CPU budget caps
// concurrent generation and that an active transcode preempts generation
// regardless of budget headroom.
func TestGenerationThrottlesUnderBudget(t *testing.T) {
	g := NewWithOptions(Options{GenerationWorkers: 2, BackgroundCPUBudget: 2})
	if got := g.BackgroundCPUBudget(); got != 2 {
		t.Fatalf("budget = %d, want 2", got)
	}
	if got := g.GenerationWorkers(); got != 2 {
		t.Fatalf("workers = %d, want 2", got)
	}

	// Budget = 2: two slots acquire, the third throttles.
	if !g.AcquireGeneration() {
		t.Fatal("first slot blocked under fresh budget")
	}
	if !g.AcquireGeneration() {
		t.Fatal("second slot blocked under budget of 2")
	}
	if g.AcquireGeneration() {
		t.Fatal("third slot acquired past the budget of 2")
	}
	if g.GenerationAllowed() {
		t.Fatal("generation allowed with the budget fully spent")
	}

	// Releasing a slot reopens the gate.
	g.ReleaseGeneration()
	if !g.GenerationAllowed() {
		t.Fatal("generation still throttled after a slot freed")
	}
	if !g.AcquireGeneration() {
		t.Fatal("freed slot not reusable")
	}

	// Transcode preempts generation even with budget headroom: drain to one
	// in-flight slot (headroom for a second), then start a transcode.
	g.ReleaseGeneration() // back to 1 in flight, 1 slot free
	g.TranscodeStarted()
	if g.GenerationAllowed() {
		t.Fatal("generation allowed while a transcode is active")
	}
	if g.AcquireGeneration() {
		t.Fatal("acquired a generation slot while a transcode is active")
	}

	// Transcode ends: with one slot still in flight, the free slot is grantable.
	g.TranscodeStopped()
	if !g.GenerationAllowed() {
		t.Fatal("generation still blocked after the transcode ended")
	}
	if !g.AcquireGeneration() {
		t.Fatal("free slot not grantable after the transcode ended")
	}

	// Release-underflow guard: extra releases never push the in-flight count
	// negative, which would otherwise grant slots beyond the budget.
	for i := 0; i < 5; i++ {
		g.ReleaseGeneration()
	}
	if !g.AcquireGeneration() || !g.AcquireGeneration() {
		t.Fatal("budget slots unavailable after release underflow")
	}
	if g.AcquireGeneration() {
		t.Fatal("budget exceeded after release underflow")
	}
}

// TestNewDefaultsToSingleSlot confirms the bare New() (and clamped options)
// yields a usable one-slot, one-worker governor — the low-power floor.
func TestNewDefaultsToSingleSlot(t *testing.T) {
	g := New()
	if g.BackgroundCPUBudget() != 1 || g.GenerationWorkers() != 1 {
		t.Fatalf("New() budget=%d workers=%d, want 1/1", g.BackgroundCPUBudget(), g.GenerationWorkers())
	}
	if !g.AcquireGeneration() {
		t.Fatal("single slot not acquirable")
	}
	if g.AcquireGeneration() {
		t.Fatal("second slot acquired past a budget of 1")
	}

	clamped := NewWithOptions(Options{GenerationWorkers: 0, BackgroundCPUBudget: -3})
	if clamped.BackgroundCPUBudget() != 1 || clamped.GenerationWorkers() != 1 {
		t.Fatalf("clamped budget=%d workers=%d, want 1/1", clamped.BackgroundCPUBudget(), clamped.GenerationWorkers())
	}
}

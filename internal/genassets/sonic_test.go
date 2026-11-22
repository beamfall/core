package genassets

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// fakeExcerpt is the ExcerptDecoder test double: it records the window it was
// asked for and returns a fixed buffer.
type fakeExcerpt struct {
	calls  int
	starts []float64
	err    error
}

func (f *fakeExcerpt) AudioExcerpt(_ context.Context, _ string, start, seconds float64, sampleRate, channels int) ([]float32, error) {
	f.calls++
	f.starts = append(f.starts, start)
	if f.err != nil {
		return nil, f.err
	}
	return make([]float32, int(seconds)*sampleRate*channels/100), nil
}

// fakeAnalyzer stands in for the Oscillux sonic profile until the CORE-3072
// artifact exists.
type fakeAnalyzer struct {
	calls  int
	vector []float32
	err    error
}

func (f *fakeAnalyzer) Kind() store.EmbeddingKind { return store.SonicEmbeddingKind }

func (f *fakeAnalyzer) Analyze(_ context.Context, samples []float32, _, _ int) ([]float32, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(samples) == 0 {
		return nil, errors.New("no samples")
	}
	return f.vector, nil
}

func seedSonicTracks(t *testing.T, st *store.Store, n int) []store.MediaID {
	t.Helper()
	ids := make([]store.MediaID, 0, n)
	for i := range n {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("sonic-%d.bin", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("sonic audio %d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		media, _ := seedAudioMedia(t, st, path, fmt.Sprintf("Sonic %d", i))
		ids = append(ids, media.ID)
	}
	return ids
}

func sonicCandidates(t *testing.T, st *store.Store) []store.MediaID {
	t.Helper()
	ids, err := st.SonicAnalysisCandidatesAfter(context.Background(), store.SonicEmbeddingKind, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func runSonicSweep(t *testing.T, svc *Service, st *store.Store) int {
	t.Helper()
	return runSonicSweepFrom(t, svc, st, nil)
}

// runSonicSweepFrom runs one sweep to completion starting from checkpoint.
func runSonicSweepFrom(t *testing.T, svc *Service, st *store.Store, checkpoint []byte) int {
	t.Helper()
	ctx := context.Background()
	job, err := NewSonicSweepJob(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker", []store.JobClass{store.JobClassBackground}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := SonicSweepHandler(svc)
	if checkpoint != nil {
		claimed.Checkpoint = checkpoint
	}
	invocations := 0
	for {
		invocations++
		result, err := handler(ctx, claimed)
		if err == nil {
			if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			return invocations
		}
		if !errors.Is(err, jobs.ErrYielded) {
			t.Fatal(err)
		}
		claimed.Checkpoint = result.Checkpoint
	}
}

func TestSonicSweepInertWithoutAnalyzer(t *testing.T) {
	svc, st, _ := newTestService(t, &fakeGen{})
	seedSonicTracks(t, st, 2)
	if err := st.SetSonicAnalysisEnabled(context.Background(), "library-local", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if svc.SonicAnalysisActive() {
		t.Fatal("sonic analysis active with nothing wired")
	}
	runSonicSweep(t, svc, st)
	if got := sonicCandidates(t, st); len(got) != 2 {
		t.Fatalf("inert sweep changed candidates: %v", got)
	}
}

func TestSonicSweepAnalysesOnlyEnabledLibrariesAndResumes(t *testing.T) {
	svc, st, _ := newTestService(t, &fakeGen{})
	decoder := &fakeExcerpt{}
	analyzer := &fakeAnalyzer{vector: []float32{0.1, 0.2, 0.3}}
	svc.SetSonicAnalysis(decoder, analyzer)
	seedSonicTracks(t, st, sweepJobBatchSize+2)

	// Toggle off (the default): nothing is decoded.
	runSonicSweep(t, svc, st)
	if decoder.calls != 0 {
		t.Fatalf("decoded %d excerpts with the toggle off", decoder.calls)
	}

	if err := st.SetSonicAnalysisEnabled(context.Background(), "library-local", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// One yield after the first batch, one to wrap to the start, then done.
	if invocations := runSonicSweep(t, svc, st); invocations != 3 {
		t.Fatalf("handler invocations = %d, want 3", invocations)
	}
	if analyzer.calls != sweepJobBatchSize+2 {
		t.Fatalf("analysed %d, want %d", analyzer.calls, sweepJobBatchSize+2)
	}
	if got := sonicCandidates(t, st); len(got) != 0 {
		t.Fatalf("candidates after sweep = %v, want none", got)
	}
	// seedAudioMedia records 200 s, so the 60 s window starts at 70 s.
	for _, start := range decoder.starts {
		if start != 70 {
			t.Fatalf("excerpt start = %v, want 70", start)
		}
	}
	// A second sweep finds nothing left to do.
	before := analyzer.calls
	runSonicSweep(t, svc, st)
	if analyzer.calls != before {
		t.Fatal("second sweep re-analysed Media that already have vectors")
	}
}

func TestSonicSweepSkipsFailuresAndBadVectors(t *testing.T) {
	cases := []struct {
		name     string
		decoder  *fakeExcerpt
		analyzer *fakeAnalyzer
	}{
		{"decode error", &fakeExcerpt{err: errors.New("bad file")}, &fakeAnalyzer{vector: []float32{1}}},
		{"analyse error", &fakeExcerpt{}, &fakeAnalyzer{err: errors.New("trap")}},
		{"empty vector", &fakeExcerpt{}, &fakeAnalyzer{vector: nil}},
		{"non-finite vector", &fakeExcerpt{}, &fakeAnalyzer{vector: []float32{float32(math.NaN())}}},
		{"all-zero vector", &fakeExcerpt{}, &fakeAnalyzer{vector: []float32{0, 0}}},
		{"oversized vector", &fakeExcerpt{}, &fakeAnalyzer{vector: make([]float32, sonicMaxVectorDims+1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, st, _ := newTestService(t, &fakeGen{})
			svc.SetSonicAnalysis(tc.decoder, tc.analyzer)
			seedSonicTracks(t, st, 2)
			if err := st.SetSonicAnalysisEnabled(context.Background(), "library-local", true, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			runSonicSweep(t, svc, st) // must finish, not fail the job
			if got := sonicCandidates(t, st); len(got) != 2 {
				t.Fatalf("candidates = %v, want both still unanalysed", got)
			}
		})
	}
}

func TestAnalyzeSonicSkipsEncryptedAndSealedLibraries(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire func(*Service)
	}{
		{"sealed", func(s *Service) { s.SetSealed(func(store.LibraryID) bool { return true }) }},
		{"encrypted", func(s *Service) { s.SetEncrypted(func(store.LibraryID) bool { return true }) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, st, _ := newTestService(t, &fakeGen{})
			decoder := &fakeExcerpt{}
			svc.SetSonicAnalysis(decoder, &fakeAnalyzer{vector: []float32{1}})
			tc.wire(svc)
			ids := seedSonicTracks(t, st, 1)
			analysed, err := svc.AnalyzeSonic(context.Background(), ids[0])
			if err != nil || analysed {
				t.Fatalf("AnalyzeSonic = %v, %v; want skipped", analysed, err)
			}
			if decoder.calls != 0 {
				t.Fatal("decoded an excerpt from a protected Library")
			}
		})
	}
}

func TestSonicExcerptStartCentresWindow(t *testing.T) {
	for duration, want := range map[int]float64{0: 0, 30: 0, 60: 0, 61: 0.5, 200: 70, 3600: 1770} {
		if got := sonicExcerptStart(duration); got != want {
			t.Fatalf("sonicExcerptStart(%d) = %v, want %v", duration, got, want)
		}
	}
}

// toggleOffAnalyzer turns the Library's toggle off while analysing, as an
// Admin would between the sweep selecting a batch and storing a vector.
type toggleOffAnalyzer struct {
	fakeAnalyzer
	st *store.Store
}

func (a *toggleOffAnalyzer) Analyze(ctx context.Context, samples []float32, rate, channels int) ([]float32, error) {
	if err := a.st.SetSonicAnalysisEnabled(ctx, "library-local", false, time.Now().UTC()); err != nil {
		return nil, err
	}
	return a.fakeAnalyzer.Analyze(ctx, samples, rate, channels)
}

func TestAnalyzeSonicHonoursToggleTurnedOffMidBatch(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newTestService(t, &fakeGen{})
	decoder := &fakeExcerpt{}
	svc.SetSonicAnalysis(decoder, &fakeAnalyzer{vector: []float32{1}})
	ids := seedSonicTracks(t, st, 1)

	// Selected while on, but off by the time the item is analysed: no decode.
	if analysed, err := svc.AnalyzeSonic(ctx, ids[0]); err != nil || analysed || decoder.calls != 0 {
		t.Fatalf("toggle off: analysed = %v, err = %v, decodes = %d", analysed, err, decoder.calls)
	}

	// Turned off during analysis: the vector is dropped, not stored.
	if err := st.SetSonicAnalysisEnabled(ctx, "library-local", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	svc.SetSonicAnalysis(decoder, &toggleOffAnalyzer{fakeAnalyzer: fakeAnalyzer{vector: []float32{1}}, st: st})
	if analysed, err := svc.AnalyzeSonic(ctx, ids[0]); err != nil || analysed {
		t.Fatalf("toggled off mid-analysis: analysed = %v, %v; want dropped", analysed, err)
	}
	if err := st.SetSonicAnalysisEnabled(ctx, "library-local", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := sonicCandidates(t, st); len(got) != 1 {
		t.Fatalf("candidates = %v, want the item still unanalysed", got)
	}
}

func TestSonicSweepWrapsToCandidatesBehindCursor(t *testing.T) {
	svc, st, _ := newTestService(t, &fakeGen{})
	analyzer := &fakeAnalyzer{vector: []float32{1, 2}}
	svc.SetSonicAnalysis(&fakeExcerpt{}, analyzer)
	seedSonicTracks(t, st, 2)
	if err := st.SetSonicAnalysisEnabled(context.Background(), "library-local", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// A deduped sweep whose cursor is already past every candidate.
	runSonicSweepFrom(t, svc, st, []byte(`{"version":1,"last_id":"~"}`))
	if analyzer.calls != 2 {
		t.Fatalf("analysed %d, want 2 after wrapping", analyzer.calls)
	}
	if got := sonicCandidates(t, st); len(got) != 0 {
		t.Fatalf("candidates = %v, want none", got)
	}
}

package genassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/quick"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeGen satisfies the Generator seam without FFmpeg, recording call
// arguments and writing real (tiny) JPEG sheets so manifest dimension
// derivation runs the production path.
type fakeGen struct {
	spriteCalls    int
	spriteInterval int
	spriteTileW    int
	spriteCols     int
	spriteRows     int
	sheetCount     int // sheets written per SpriteSheets call
	sheetHeight    int
	spriteErr      error // if non-nil, SpriteSheets writes sheets then returns this error

	clipCalls   int
	clipOffsets []float64
	clipSeg     float64
	clipWidth   int
	clipFPS     int

	grabTimes []float64
	grabDsts  []string

	waveformCalls   int
	waveformSamples []float64 // raw per-window amplitudes returned per call

	silenceCalls int
	silenceSpans [][2]float64 // [start,end] pairs returned per SilenceSpans call

	loudnessCalls      int
	loudnessIntegrated float64 // integrated LUFS returned per LoudnessMeasure call
	loudnessRange      float64 // loudness range LU
	loudnessTruePeak   float64 // true peak dBTP
	loudnessFromTags   bool    // provenance flag returned
}

func (f *fakeGen) SpriteSheets(_ context.Context, _ string, interval, tileWidth, columns, rows int, pattern string) error {
	f.spriteCalls++
	f.spriteInterval, f.spriteTileW, f.spriteCols, f.spriteRows = interval, tileWidth, columns, rows
	for i := 1; i <= f.sheetCount; i++ {
		if err := writeJPEG(fmt.Sprintf(pattern, i), 40, f.sheetHeight); err != nil {
			return err
		}
	}
	return f.spriteErr
}

func (f *fakeGen) SampleClip(_ context.Context, _ string, offsets []float64, segmentSeconds float64, width, maxFPS int, dst string) error {
	f.clipCalls++
	f.clipOffsets, f.clipSeg, f.clipWidth, f.clipFPS = offsets, segmentSeconds, width, maxFPS
	return os.WriteFile(dst, []byte("mp4 bytes"), 0o600)
}

func (f *fakeGen) FrameGrab(_ context.Context, _ string, atSeconds float64, _ int, dst string) error {
	f.grabTimes = append(f.grabTimes, atSeconds)
	f.grabDsts = append(f.grabDsts, filepath.Base(dst))
	return os.WriteFile(dst, []byte("jpeg bytes"), 0o600)
}

func (f *fakeGen) WaveformAmplitudes(_ context.Context, _ string) ([]float64, error) {
	f.waveformCalls++
	out := make([]float64, len(f.waveformSamples))
	copy(out, f.waveformSamples)
	return out, nil
}

func (f *fakeGen) SilenceSpans(_ context.Context, _ string) ([][2]float64, error) {
	f.silenceCalls++
	out := make([][2]float64, len(f.silenceSpans))
	copy(out, f.silenceSpans)
	return out, nil
}

func (f *fakeGen) LoudnessMeasure(_ context.Context, _ string) (integratedLUFS, loudnessRangeLU, truePeakDBTP float64, fromTags bool, err error) {
	f.loudnessCalls++
	return f.loudnessIntegrated, f.loudnessRange, f.loudnessTruePeak, f.loudnessFromTags, nil
}

func writeJPEG(path string, width, height int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, image.NewGray(image.Rect(0, 0, width, height)), nil)
}

// newTestService seeds media-alpha (video, 120s) with a primary file and
// returns the service plus the file's content fingerprint.
func newTestService(t *testing.T, gen Generator) (*Service, *store.Store, string) {
	t.Helper()
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), "media-alpha", path); err != nil {
		t.Fatal(err)
	}
	file, err := st.PrimaryMediaFile(context.Background(), "media-alpha", store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	return NewService(st, gen, t.TempDir()), st, file.ContentFingerprint
}

// seedAudioMedia imports an audio-class Media item backed by a real file at
// path, returning the item and its content fingerprint. Editing the bytes at
// path and re-importing yields a fresh fingerprint — the regeneration key.
func seedAudioMedia(t *testing.T, st *store.Store, path, title string) (store.Media, string) {
	t.Helper()
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               title,
		Container:           "mp3",
		Codec:               "mp3",
		Class:               store.MediaClassAudio,
		ContentFingerprint:  fingerprint,
		DurationSecondsHint: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	return media, fingerprint
}

func TestNormalizeWaveformScalesToUnitPeak(t *testing.T) {
	// feature:waveform feature:generated-assets
	points := normalizeWaveform([]float64{0.1, 0.5, 0.25, 0})
	want := []float64{0.2, 1, 0.5, 0} // scaled against the 0.5 peak
	if len(points) != len(want) {
		t.Fatalf("len = %d, want %d", len(points), len(want))
	}
	for i := range want {
		if math.Abs(points[i]-want[i]) > 0.0005 {
			t.Fatalf("points = %v, want %v", points, want)
		}
		if points[i] < 0 || points[i] > 1 {
			t.Fatalf("point %d = %v is outside [0,1]", i, points[i])
		}
	}
	// A fully silent envelope clamps to all-zeros rather than dividing by zero.
	for _, v := range normalizeWaveform([]float64{0, 0, 0}) {
		if v != 0 {
			t.Fatalf("silent envelope point = %v, want 0", v)
		}
	}
}

func TestBucketWaveformPeakFoldsToTarget(t *testing.T) {
	// 9 samples → 3 buckets, peak per bucket survives.
	got := bucketWaveform([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9}, 3)
	want := []float64{3, 6, 9}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buckets = %v, want %v", got, want)
		}
	}
	// Fewer samples than the target never upsamples — one point per sample.
	if got := bucketWaveform([]float64{0.2, 0.8}, 1000); len(got) != 2 {
		t.Fatalf("short source produced %d points, want 2 (no padding)", len(got))
	}
	if got := bucketWaveform(nil, 1000); got != nil {
		t.Fatalf("empty source produced %v, want nil", got)
	}
}

func TestGenerateWaveformProducesNormalizedEnvelope(t *testing.T) {
	// feature:waveform feature:generated-assets
	// Oversample well past the served point count so the bucket step runs the
	// production fold-down path, and include a clear peak so normalisation has a
	// non-trivial scale.
	samples := make([]float64, 3000)
	for i := range samples {
		samples[i] = 0.1 + 0.4*float64(i%7)/6 // varied, peak 0.5
	}
	gen := &fakeGen{waveformSamples: samples}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio one"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Track One")

	if err := svc.GenerateWaveform(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateWaveform returned error: %v", err)
	}
	data, err := os.ReadFile(svc.WaveformPath(fingerprint))
	if err != nil {
		t.Fatalf("waveform missing: %v", err)
	}
	var env WaveformEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("waveform is not JSON: %v", err)
	}
	if env.Version != waveformVersion {
		t.Fatalf("version = %d, want %d", env.Version, waveformVersion)
	}
	// ~1000 points: the 3000 windows fold down to exactly the target.
	if len(env.Points) != WaveformPoints {
		t.Fatalf("points = %d, want %d", len(env.Points), WaveformPoints)
	}
	maxSeen := 0.0
	for i, p := range env.Points {
		if p < 0 || p > 1 {
			t.Fatalf("point %d = %v is outside [0,1]", i, p)
		}
		if p > maxSeen {
			maxSeen = p
		}
	}
	if math.Abs(maxSeen-1) > 0.0005 {
		t.Fatalf("envelope peak = %v, want 1 (normalised to the loudest point)", maxSeen)
	}
	if name, ok := svc.WaveformAsset(fingerprint); !ok || name != WaveformName {
		t.Fatalf("WaveformAsset = (%q, %v), want (%q, true)", name, ok, WaveformName)
	}
	// Servable through the same fingerprint-keyed route as every other asset.
	if _, err := svc.AssetPath(fingerprint, WaveformName); err != nil {
		t.Fatalf("waveform not servable: %v", err)
	}
}

// TestGenerateWaveformRegeneratesOnBytesNotMetadata is the acceptance test:
// the envelope is a property of the bytes, so it regenerates when the file's
// content fingerprint changes (a re-encode) but never on a metadata edit (same
// bytes, same fingerprint, same asset directory).
func TestGenerateWaveformRegeneratesOnBytesNotMetadata(t *testing.T) {
	// feature:waveform feature:generated-assets
	gen := &fakeGen{waveformSamples: []float64{0.2, 0.4, 0.6}}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _ := seedAudioMedia(t, st, track, "Original Title")

	if err := svc.GenerateWaveform(context.Background(), media.ID); err != nil {
		t.Fatalf("first generate returned error: %v", err)
	}
	// A metadata edit (title) does not touch the file bytes, so the fingerprint —
	// and thus the asset directory — is unchanged: no regeneration.
	newTitle := "Edited Title"
	if err := st.UpdateMediaMetadata(context.Background(), media.ID, store.MediaMetadata{Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateWaveform(context.Background(), media.ID); err != nil {
		t.Fatalf("post-metadata-edit generate returned error: %v", err)
	}
	if gen.waveformCalls != 1 {
		t.Fatalf("waveformCalls after metadata edit = %d, want 1 (metadata must not regenerate)", gen.waveformCalls)
	}

	// Re-encoding the file (new bytes) yields a new content fingerprint, so the
	// asset is keyed to a fresh directory and regenerates.
	if err := os.WriteFile(track, []byte("audio v2 re-encoded longer bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), media.ID, track); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateWaveform(context.Background(), media.ID); err != nil {
		t.Fatalf("post-reencode generate returned error: %v", err)
	}
	if gen.waveformCalls != 2 {
		t.Fatalf("waveformCalls after re-encode = %d, want 2 (fingerprint change must regenerate)", gen.waveformCalls)
	}
}

// TestGenerateWaveformGatedToAudioClass guards the audio-only gate (ADR-0035 §6,
// v1): a video-class Media never gets a waveform envelope.
func TestGenerateWaveformGatedToAudioClass(t *testing.T) {
	// feature:waveform feature:generated-assets
	gen := &fakeGen{waveformSamples: []float64{0.5, 0.5}}
	svc, _, fingerprint := newTestService(t, gen) // media-alpha is video-class

	if err := svc.GenerateWaveform(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GenerateWaveform on video media returned error: %v", err)
	}
	if gen.waveformCalls != 0 {
		t.Fatalf("waveformCalls = %d, want 0 (video-class must not get a waveform)", gen.waveformCalls)
	}
	if _, ok := svc.WaveformAsset(fingerprint); ok {
		t.Fatal("waveform reported available for video-class media")
	}
}

func TestGenerateSilenceMapPersistsSpans(t *testing.T) {
	// feature:silence-map feature:generated-assets
	gen := &fakeGen{silenceSpans: [][2]float64{
		{12.5, 15.75},
		{60, 63.2},
	}}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio one"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Track One")

	if err := svc.GenerateSilenceMap(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateSilenceMap returned error: %v", err)
	}
	data, err := os.ReadFile(svc.SilenceMapPath(fingerprint))
	if err != nil {
		t.Fatalf("silence map missing: %v", err)
	}
	var sm SilenceMap
	if err := json.Unmarshal(data, &sm); err != nil {
		t.Fatalf("silence map is not JSON: %v", err)
	}
	if sm.Version != silenceMapVersion {
		t.Fatalf("version = %d, want %d", sm.Version, silenceMapVersion)
	}
	if len(sm.Spans) != 2 ||
		sm.Spans[0] != (SilenceSpan{StartSeconds: 12.5, EndSeconds: 15.75}) ||
		sm.Spans[1] != (SilenceSpan{StartSeconds: 60, EndSeconds: 63.2}) {
		t.Fatalf("spans = %+v, want the two seeded spans", sm.Spans)
	}
	if name, ok := svc.SilenceMapAsset(fingerprint); !ok || name != SilenceMapName {
		t.Fatalf("SilenceMapAsset = (%q, %v), want (%q, true)", name, ok, SilenceMapName)
	}
	// Servable through the same fingerprint-keyed Stream-Token route as every
	// other asset (AssetPath is what mediahttp resolves behind the token).
	if _, err := svc.AssetPath(fingerprint, SilenceMapName); err != nil {
		t.Fatalf("silence map not servable: %v", err)
	}
}

// TestGenerateSilenceMapEmptyWhenNoSilence guards the publication-marker
// semantics: a file with no detected silence still gets a present manifest with
// an empty span list, so the asset's existence means "analysed", distinct from
// "not yet analysed".
func TestGenerateSilenceMapEmptyWhenNoSilence(t *testing.T) {
	// feature:silence-map feature:generated-assets
	gen := &fakeGen{silenceSpans: nil}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("dense audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Track One")

	if err := svc.GenerateSilenceMap(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateSilenceMap returned error: %v", err)
	}
	if _, ok := svc.SilenceMapAsset(fingerprint); !ok {
		t.Fatal("silence map not published for a file with no detected silence")
	}
	data, err := os.ReadFile(svc.SilenceMapPath(fingerprint))
	if err != nil {
		t.Fatalf("silence map missing: %v", err)
	}
	var sm SilenceMap
	if err := json.Unmarshal(data, &sm); err != nil {
		t.Fatalf("silence map is not JSON: %v", err)
	}
	if len(sm.Spans) != 0 {
		t.Fatalf("spans = %+v, want empty for a file with no silence", sm.Spans)
	}
}

// TestGenerateSilenceMapRegeneratesOnBytesNotMetadata is the acceptance test:
// the map is a property of the bytes, so it regenerates when the file's content
// fingerprint changes (a re-encode) but never on a metadata edit (same bytes,
// same fingerprint, same asset directory).
func TestGenerateSilenceMapRegeneratesOnBytesNotMetadata(t *testing.T) {
	// feature:silence-map feature:generated-assets
	gen := &fakeGen{silenceSpans: [][2]float64{{1, 4}}}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _ := seedAudioMedia(t, st, track, "Original Title")

	if err := svc.GenerateSilenceMap(context.Background(), media.ID); err != nil {
		t.Fatalf("first generate returned error: %v", err)
	}
	// A metadata edit (title) does not touch the file bytes, so the fingerprint —
	// and thus the asset directory — is unchanged: no regeneration.
	newTitle := "Edited Title"
	if err := st.UpdateMediaMetadata(context.Background(), media.ID, store.MediaMetadata{Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateSilenceMap(context.Background(), media.ID); err != nil {
		t.Fatalf("post-metadata-edit generate returned error: %v", err)
	}
	if gen.silenceCalls != 1 {
		t.Fatalf("silenceCalls after metadata edit = %d, want 1 (metadata must not regenerate)", gen.silenceCalls)
	}

	// Re-encoding the file (new bytes) yields a new content fingerprint, so the
	// asset is keyed to a fresh directory and regenerates.
	if err := os.WriteFile(track, []byte("audio v2 re-encoded longer bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), media.ID, track); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateSilenceMap(context.Background(), media.ID); err != nil {
		t.Fatalf("post-reencode generate returned error: %v", err)
	}
	if gen.silenceCalls != 2 {
		t.Fatalf("silenceCalls after re-encode = %d, want 2 (fingerprint change must regenerate)", gen.silenceCalls)
	}
}

// TestGenerateSilenceMapGatedToAudioClass guards the audio-only gate (ADR-0052
// §2.4(a)): a video-class Media never gets a silence map.
func TestGenerateSilenceMapGatedToAudioClass(t *testing.T) {
	// feature:silence-map feature:generated-assets
	gen := &fakeGen{silenceSpans: [][2]float64{{1, 2}}}
	svc, _, fingerprint := newTestService(t, gen) // media-alpha is video-class

	if err := svc.GenerateSilenceMap(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GenerateSilenceMap on video media returned error: %v", err)
	}
	if gen.silenceCalls != 0 {
		t.Fatalf("silenceCalls = %d, want 0 (video-class must not get a silence map)", gen.silenceCalls)
	}
	if _, ok := svc.SilenceMapAsset(fingerprint); ok {
		t.Fatal("silence map reported available for video-class media")
	}
}

// synthBeatEnvelope builds a deterministic energy envelope with a periodic
// energy spike every period windows — a clean metronome the stand-in tempo
// estimator should lock onto. The envelope spans windows for the whole duration.
func synthBeatEnvelope(windows, period int) []float64 {
	env := make([]float64, windows)
	for i := range env {
		if i%period == 0 {
			env[i] = 1.0 // onset spike on the beat
		} else {
			env[i] = 0.1 // quiet between beats
		}
	}
	return env
}

func TestEstimateBeatGridLocksOntoSyntheticTempo(t *testing.T) {
	// feature:beat-grid feature:generated-assets
	// 200s of audio, 4000 windows → 0.05s/window. A spike every 20 windows = a
	// beat every 1.0s = 60 BPM (the band floor), the cleanest case to verify the
	// onset→autocorrelation→phase pipeline.
	const (
		windows  = 4000
		duration = 200.0
		period   = 20 // windows between beats → 1.0s → 60 BPM
	)
	grid := estimateBeatGrid(synthBeatEnvelope(windows, period), duration)
	if math.Abs(grid.BPM-60) > 1 {
		t.Fatalf("BPM = %v, want ~60", grid.BPM)
	}
	if grid.Confidence <= 0 || grid.Confidence > 1 {
		t.Fatalf("confidence = %v, want in (0,1]", grid.Confidence)
	}
	if len(grid.Beats) == 0 {
		t.Fatal("no beats placed for a clearly periodic envelope")
	}
	// Beats are non-decreasing, in range, and roughly one period apart.
	for i := 1; i < len(grid.Beats); i++ {
		if grid.Beats[i] <= grid.Beats[i-1] {
			t.Fatalf("beats not increasing at %d: %v", i, grid.Beats)
		}
		if gap := grid.Beats[i] - grid.Beats[i-1]; math.Abs(gap-1.0) > 0.1 {
			t.Fatalf("beat gap = %v, want ~1.0s", gap)
		}
	}
	if last := grid.Beats[len(grid.Beats)-1]; last >= duration {
		t.Fatalf("last beat %v >= duration %v", last, duration)
	}
}

func TestEstimateBeatGridEmptyOnFlatEnergy(t *testing.T) {
	// feature:beat-grid feature:generated-assets
	// A flat (no onsets) envelope has no periodicity: an empty grid with zero
	// confidence, never a fabricated tempo.
	flat := make([]float64, 4000)
	for i := range flat {
		flat[i] = 0.5
	}
	grid := estimateBeatGrid(flat, 200)
	if grid.BPM != 0 || grid.Confidence != 0 || len(grid.Beats) != 0 {
		t.Fatalf("flat envelope grid = %+v, want zero BPM/confidence and no beats", grid)
	}
	// Too-short envelope also yields an empty grid rather than a spurious tempo.
	if grid := estimateBeatGrid([]float64{0.1, 0.9, 0.1}, 1); len(grid.Beats) != 0 {
		t.Fatalf("short envelope produced beats: %+v", grid)
	}
}

func TestGenerateBeatGridProducesGridAndIsServable(t *testing.T) {
	// feature:beat-grid feature:generated-assets
	gen := &fakeGen{waveformSamples: synthBeatEnvelope(4000, 20)}
	svc, st, _ := newTestService(t, gen) // seedAudioMedia is 200s duration
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio one"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Track One")

	if err := svc.GenerateBeatGrid(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateBeatGrid returned error: %v", err)
	}
	data, err := os.ReadFile(svc.BeatGridPath(fingerprint))
	if err != nil {
		t.Fatalf("beat grid missing: %v", err)
	}
	var bg BeatGrid
	if err := json.Unmarshal(data, &bg); err != nil {
		t.Fatalf("beat grid is not JSON: %v", err)
	}
	if bg.Version != beatGridVersion {
		t.Fatalf("version = %d, want %d", bg.Version, beatGridVersion)
	}
	if math.Abs(bg.BPM-60) > 1 || len(bg.Beats) == 0 {
		t.Fatalf("beat grid = %+v, want ~60 BPM with beats", bg)
	}
	if name, ok := svc.BeatGridAsset(fingerprint); !ok || name != BeatGridName {
		t.Fatalf("BeatGridAsset = (%q, %v), want (%q, true)", name, ok, BeatGridName)
	}
	// Servable through the same fingerprint-keyed Stream-Token route as every
	// other asset (AssetPath is what mediahttp resolves behind the token).
	if _, err := svc.AssetPath(fingerprint, BeatGridName); err != nil {
		t.Fatalf("beat grid not servable: %v", err)
	}
}

// TestGenerateBeatGridRegeneratesOnBytesNotMetadata is the acceptance test: the
// grid is a property of the bytes, so it regenerates when the file's content
// fingerprint changes (a re-encode) but never on a metadata edit (same bytes,
// same fingerprint, same asset directory).
func TestGenerateBeatGridRegeneratesOnBytesNotMetadata(t *testing.T) {
	// feature:beat-grid feature:generated-assets
	gen := &fakeGen{waveformSamples: synthBeatEnvelope(4000, 20)}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _ := seedAudioMedia(t, st, track, "Original Title")

	if err := svc.GenerateBeatGrid(context.Background(), media.ID); err != nil {
		t.Fatalf("first generate returned error: %v", err)
	}
	newTitle := "Edited Title"
	if err := st.UpdateMediaMetadata(context.Background(), media.ID, store.MediaMetadata{Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateBeatGrid(context.Background(), media.ID); err != nil {
		t.Fatalf("post-metadata-edit generate returned error: %v", err)
	}
	if gen.waveformCalls != 1 {
		t.Fatalf("waveformCalls after metadata edit = %d, want 1 (metadata must not regenerate)", gen.waveformCalls)
	}
	if err := os.WriteFile(track, []byte("audio v2 re-encoded longer bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), media.ID, track); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateBeatGrid(context.Background(), media.ID); err != nil {
		t.Fatalf("post-reencode generate returned error: %v", err)
	}
	if gen.waveformCalls != 2 {
		t.Fatalf("waveformCalls after re-encode = %d, want 2 (fingerprint change must regenerate)", gen.waveformCalls)
	}
}

// TestGenerateBeatGridGatedToAudioClass guards the audio-only gate (ADR-0035 §6,
// v1): a video-class Media never gets a beat grid.
func TestGenerateBeatGridGatedToAudioClass(t *testing.T) {
	// feature:beat-grid feature:generated-assets
	gen := &fakeGen{waveformSamples: synthBeatEnvelope(4000, 20)}
	svc, _, fingerprint := newTestService(t, gen) // media-alpha is video-class

	if err := svc.GenerateBeatGrid(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GenerateBeatGrid on video media returned error: %v", err)
	}
	if gen.waveformCalls != 0 {
		t.Fatalf("waveformCalls = %d, want 0 (video-class must not get a beat grid)", gen.waveformCalls)
	}
	if _, ok := svc.BeatGridAsset(fingerprint); ok {
		t.Fatal("beat grid reported available for video-class media")
	}
}

func TestParseSyncLanesRejectsOffGridBars(t *testing.T) {
	// feature:sync-lanes feature:generated-assets
	grid := BeatGrid{Version: beatGridVersion, Beats: []float64{0, 0.5, 1.0}}
	_, err := ParseSyncLanes([]byte(`{"version":1,"meter":4,"confidence":0.8,"bars":[{"time":0.76,"confidence":0.8}],"phrases":[]}`), grid)
	if err == nil {
		t.Fatal("ParseSyncLanes accepted an off-grid bar")
	}
	if _, err := ParseSyncLanes([]byte(`{"version":1,"meter":4,"confidence":1.2,"bars":[],"phrases":[]}`), grid); err == nil {
		t.Fatal("ParseSyncLanes accepted out-of-range confidence")
	}
}

func TestGenerateSyncLanesProducesValidatedAsset(t *testing.T) {
	// feature:sync-lanes feature:generated-assets
	gen := &fakeGen{waveformSamples: synthBeatEnvelope(4000, 20)}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio one"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Track One")

	if err := svc.GenerateSyncLanes(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateSyncLanes returned error: %v", err)
	}
	data, err := os.ReadFile(svc.SyncLanesPath(fingerprint))
	if err != nil {
		t.Fatalf("sync lanes missing: %v", err)
	}
	gridData, err := os.ReadFile(svc.BeatGridPath(fingerprint))
	if err != nil {
		t.Fatalf("beat grid missing: %v", err)
	}
	var grid BeatGrid
	if err := json.Unmarshal(gridData, &grid); err != nil {
		t.Fatal(err)
	}
	lanes, err := ParseSyncLanes(data, grid)
	if err != nil {
		t.Fatalf("sync lanes did not validate: %v", err)
	}
	if lanes.Meter != 4 || len(lanes.Bars) == 0 {
		t.Fatalf("sync lanes = %+v, want meter 4 with bars", lanes)
	}
	if name, ok := svc.SyncLanesAsset(fingerprint); !ok || name != SyncLanesName {
		t.Fatalf("SyncLanesAsset = (%q, %v), want (%q, true)", name, ok, SyncLanesName)
	}
	if _, err := svc.AssetPath(fingerprint, SyncLanesName); err != nil {
		t.Fatalf("sync lanes not servable: %v", err)
	}
}

func TestGenerateSyncLanesRegeneratesOnBytesNotMetadata(t *testing.T) {
	// feature:sync-lanes feature:generated-assets
	gen := &fakeGen{waveformSamples: synthBeatEnvelope(4000, 20)}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _ := seedAudioMedia(t, st, track, "Original Title")

	if err := svc.GenerateSyncLanes(context.Background(), media.ID); err != nil {
		t.Fatalf("first generate returned error: %v", err)
	}
	newTitle := "Edited Title"
	if err := st.UpdateMediaMetadata(context.Background(), media.ID, store.MediaMetadata{Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateSyncLanes(context.Background(), media.ID); err != nil {
		t.Fatalf("post-metadata-edit generate returned error: %v", err)
	}
	if gen.waveformCalls != 1 {
		t.Fatalf("waveformCalls after metadata edit = %d, want 1 (metadata must not regenerate)", gen.waveformCalls)
	}
	if err := os.WriteFile(track, []byte("audio v2 re-encoded longer bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), media.ID, track); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateSyncLanes(context.Background(), media.ID); err != nil {
		t.Fatalf("post-reencode generate returned error: %v", err)
	}
	if gen.waveformCalls != 2 {
		t.Fatalf("waveformCalls after re-encode = %d, want 2 (fingerprint change must regenerate)", gen.waveformCalls)
	}
}

func TestTrickplayPlanPinsSpec(t *testing.T) {
	cases := []struct {
		duration, interval, frames, sheets int
	}{
		{600, 10, 60, 1},        // 10-minute file: default interval, one sheet
		{10800, 10, 1080, 11},   // 3-hour movie: ~1080 frames, 11 sheets
		{30000, 25, 1200, 12},   // ultra-long: interval widens to hold the cap
		{5, 10, 1, 1},           // shorter than one interval: a single frame
		{120000, 100, 1200, 12}, // cap holds however long the file
	}
	for _, c := range cases {
		interval, frames, sheets := trickplayPlan(c.duration)
		if interval != c.interval || frames != c.frames || sheets != c.sheets {
			t.Fatalf("trickplayPlan(%d) = (%d, %d, %d), want (%d, %d, %d)",
				c.duration, interval, frames, sheets, c.interval, c.frames, c.sheets)
		}
		if frames > 1200 {
			t.Fatalf("trickplayPlan(%d) exceeds the frame cap: %d", c.duration, frames)
		}
	}
}

func TestGenerateTrickplayWritesSheetsAndManifest(t *testing.T) {
	// feature:trickplay feature:generated-assets
	gen := &fakeGen{sheetCount: 1, sheetHeight: 1800}
	svc, _, fingerprint := newTestService(t, gen)

	if err := svc.GenerateTrickplay(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GenerateTrickplay returned error: %v", err)
	}
	// The pinned spec travels into the generator: 320w tiles, 10×10 grid, 10s.
	if gen.spriteTileW != 320 || gen.spriteCols != 10 || gen.spriteRows != 10 || gen.spriteInterval != 10 {
		t.Fatalf("sprite args = (w=%d grid=%dx%d interval=%d), want (320, 10x10, 10)",
			gen.spriteTileW, gen.spriteCols, gen.spriteRows, gen.spriteInterval)
	}
	data, err := os.ReadFile(svc.ManifestPath(fingerprint))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	var manifest TrickplayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	want := TrickplayManifest{
		Version:         1,
		IntervalSeconds: 10,
		TileWidth:       320,
		TileHeight:      180, // 1800px sheet ÷ 10 rows
		Columns:         10,
		Rows:            10,
		FrameCount:      12, // 120s seed media ÷ 10s
		Sheets:          []string{"sheet-0001.jpg"},
	}
	if manifest.Version != want.Version || manifest.IntervalSeconds != want.IntervalSeconds ||
		manifest.TileWidth != want.TileWidth || manifest.TileHeight != want.TileHeight ||
		manifest.Columns != want.Columns || manifest.Rows != want.Rows ||
		manifest.FrameCount != want.FrameCount || len(manifest.Sheets) != 1 ||
		manifest.Sheets[0] != want.Sheets[0] {
		t.Fatalf("manifest = %+v, want %+v", manifest, want)
	}
	if name, ok := svc.TrickplayAsset(fingerprint); !ok || name != ManifestName {
		t.Fatalf("TrickplayAsset = (%q, %v), want (%q, true)", name, ok, ManifestName)
	}
}

func TestGenerateTrickplaySkipsUnchangedFingerprint(t *testing.T) {
	gen := &fakeGen{sheetCount: 1, sheetHeight: 1800}
	svc, _, _ := newTestService(t, gen)

	for i := 0; i < 2; i++ {
		if err := svc.GenerateTrickplay(context.Background(), "media-alpha"); err != nil {
			t.Fatalf("GenerateTrickplay pass %d returned error: %v", i, err)
		}
	}
	if gen.spriteCalls != 1 {
		t.Fatalf("spriteCalls = %d, want 1 (unchanged fingerprint must not regenerate)", gen.spriteCalls)
	}
}

func TestGenerateTrickplayPartialSheetsCleanedOnError(t *testing.T) {
	// MEDIA-1: SpriteSheets writes N partial sheets then returns an error.
	// GenerateTrickplay must remove those sheets before returning so neither a
	// subsequent manifest-guard check nor a later successful run sees orphan
	// files.
	gen := &fakeGen{sheetCount: 3, sheetHeight: 1800, spriteErr: errors.New("ffmpeg killed")}
	svc, _, fingerprint := newTestService(t, gen)

	if err := svc.GenerateTrickplay(context.Background(), "media-alpha"); err == nil {
		t.Fatal("expected error from GenerateTrickplay when SpriteSheets fails")
	}

	// No manifest must exist.
	if _, err := os.Stat(svc.ManifestPath(fingerprint)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest must not exist after SpriteSheets error, got err=%v", err)
	}

	// No partial sheet files must remain in the trickplay directory.
	dir := filepath.Dir(svc.ManifestPath(fingerprint))
	sheets, err := filepath.Glob(filepath.Join(dir, "sheet-*.jpg"))
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if len(sheets) != 0 {
		t.Fatalf("partial sheets remain after error: %v", sheets)
	}
}

func TestGenerateTrickplayRemovesStaleSheetsBeforeRegeneration(t *testing.T) {
	gen := &fakeGen{sheetCount: 1, sheetHeight: 1800}
	svc, _, fingerprint := newTestService(t, gen)
	dir := filepath.Dir(svc.ManifestPath(fingerprint))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "sheet-0002.jpg")
	if err := writeJPEG(stale, 40, 1800); err != nil {
		t.Fatal(err)
	}

	if err := svc.GenerateTrickplay(context.Background(), "media-alpha"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(svc.ManifestPath(fingerprint))
	if err != nil {
		t.Fatal(err)
	}
	var manifest TrickplayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sheets) != 1 || manifest.Sheets[0] != "sheet-0001.jpg" {
		t.Fatalf("manifest sheets = %v, want only regenerated sheet", manifest.Sheets)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale sheet still exists: %v", err)
	}
}

func TestGenerateSkipsAudioClassMedia(t *testing.T) {
	gen := &fakeGen{sheetCount: 1, sheetHeight: 1800}
	svc, st, _ := newTestService(t, gen)
	path := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(path, []byte("audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Audio Item",
		Container:           "mp3",
		Codec:               "mp3",
		Class:               store.MediaClassAudio,
		ContentFingerprint:  fingerprint,
		DurationSecondsHint: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateTrickplay(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateTrickplay returned error: %v", err)
	}
	if err := svc.GeneratePreviewClip(context.Background(), media.ID); err != nil {
		t.Fatalf("GeneratePreviewClip returned error: %v", err)
	}
	if gen.spriteCalls != 0 || gen.clipCalls != 0 {
		t.Fatalf("audio media generated assets: sprites=%d clips=%d, want 0/0", gen.spriteCalls, gen.clipCalls)
	}
}

// TestGenerateSkipsImageClassMedia is the ADR-0043 image-class gate: a still
// gets none of the video/audio Generated Assets — no trickplay sprites, no
// preview clip, and no waveform. The generators are affirmatively scoped to
// video/audio class, so image is excluded by construction; this guards that.
func TestGenerateSkipsImageClassMedia(t *testing.T) {
	gen := &fakeGen{sheetCount: 1, sheetHeight: 1800}
	svc, st, _ := newTestService(t, gen)
	path := filepath.Join(t.TempDir(), "still.bin")
	if err := os.WriteFile(path, []byte("image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "Image Item",
		Container:          "jpg",
		Codec:              "mjpeg",
		Class:              store.MediaClassImage,
		ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateTrickplay(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateTrickplay returned error: %v", err)
	}
	if err := svc.GeneratePreviewClip(context.Background(), media.ID); err != nil {
		t.Fatalf("GeneratePreviewClip returned error: %v", err)
	}
	if err := svc.GenerateWaveform(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateWaveform returned error: %v", err)
	}
	if err := svc.GenerateSilenceMap(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateSilenceMap returned error: %v", err)
	}
	if err := svc.GenerateBeatGrid(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateBeatGrid returned error: %v", err)
	}
	if gen.spriteCalls != 0 || gen.clipCalls != 0 || gen.waveformCalls != 0 || gen.silenceCalls != 0 {
		t.Fatalf("image media generated assets: sprites=%d clips=%d waveform=%d silence=%d, want 0/0/0/0",
			gen.spriteCalls, gen.clipCalls, gen.waveformCalls, gen.silenceCalls)
	}
}

func TestPreviewOffsetsPinSpec(t *testing.T) {
	offsets, seg := previewOffsets(600)
	if len(offsets) != 12 {
		t.Fatalf("len(offsets) = %d, want 12", len(offsets))
	}
	if seg != 0.8 {
		t.Fatalf("segment = %v, want 0.8", seg)
	}
	// Evenly spaced inside the middle 90%: first at 5%, last segment ends
	// before the 95% mark.
	if math.Abs(offsets[0]-30) > 0.001 {
		t.Fatalf("first offset = %v, want 30 (5%% of 600)", offsets[0])
	}
	if offsets[len(offsets)-1]+seg > 570.001 {
		t.Fatalf("last segment ends at %v, want <= 570 (95%% of 600)", offsets[len(offsets)-1]+seg)
	}
	for i := 1; i < len(offsets); i++ {
		if offsets[i] <= offsets[i-1] {
			t.Fatalf("offsets are not increasing: %v", offsets)
		}
	}

	// Short media collapses to one contiguous clip (~10s total).
	offsets, seg = previewOffsets(20)
	if len(offsets) != 1 || offsets[0] != 0 {
		t.Fatalf("short media offsets = %v, want [0]", offsets)
	}
	if math.Abs(seg-9.6) > 0.001 {
		t.Fatalf("short media segment = %v, want 9.6", seg)
	}
}

func TestGeneratePreviewClipPinsSpecAndCaches(t *testing.T) {
	gen := &fakeGen{}
	svc, _, fingerprint := newTestService(t, gen)

	for i := 0; i < 2; i++ {
		if err := svc.GeneratePreviewClip(context.Background(), "media-alpha"); err != nil {
			t.Fatalf("GeneratePreviewClip pass %d returned error: %v", i, err)
		}
	}
	if gen.clipCalls != 1 {
		t.Fatalf("clipCalls = %d, want 1 (unchanged fingerprint must not regenerate)", gen.clipCalls)
	}
	if gen.clipWidth != 640 || gen.clipFPS != 24 {
		t.Fatalf("clip args = (w=%d fps=%d), want (640, 24)", gen.clipWidth, gen.clipFPS)
	}
	if len(gen.clipOffsets) != 12 || gen.clipSeg != 0.8 {
		t.Fatalf("clip sampling = (%d segments × %v s), want (12 × 0.8)", len(gen.clipOffsets), gen.clipSeg)
	}
	if name, ok := svc.PreviewClipAsset(fingerprint); !ok || name != PreviewClipName {
		t.Fatalf("PreviewClipAsset = (%q, %v), want (%q, true)", name, ok, PreviewClipName)
	}
}

func TestGeneratePreviewClipHonorsLibraryToggle(t *testing.T) {
	gen := &fakeGen{}
	svc, st, fingerprint := newTestService(t, gen)
	if err := st.SetLibraryPreviewClips(context.Background(), "library-local", false); err != nil {
		t.Fatal(err)
	}

	if err := svc.GeneratePreviewClip(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GeneratePreviewClip returned error: %v", err)
	}
	if gen.clipCalls != 0 {
		t.Fatalf("clipCalls = %d, want 0 with the Library toggle off", gen.clipCalls)
	}
	if _, ok := svc.PreviewClipAsset(fingerprint); ok {
		t.Fatal("preview clip reported available with the toggle off")
	}

	// Flipping the toggle back ON (the default) generates on the next pass.
	if err := st.SetLibraryPreviewClips(context.Background(), "library-local", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.GeneratePreviewClip(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GeneratePreviewClip returned error: %v", err)
	}
	if gen.clipCalls != 1 {
		t.Fatalf("clipCalls = %d, want 1 after re-enabling", gen.clipCalls)
	}
}

func TestGenerateMarkerThumbsKeyedByTimestamp(t *testing.T) {
	gen := &fakeGen{}
	svc, st, fingerprint := newTestService(t, gen)
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}
	now := time.Unix(100, 0).UTC()
	end := 30.0
	point, err := st.CreateMarker(context.Background(), store.Marker{
		MediaID: "media-alpha", Title: "Point", StartSeconds: 12.5,
	}, filter, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMarker(context.Background(), store.Marker{
		MediaID: "media-alpha", Title: "Range", StartSeconds: 20, EndSeconds: &end,
	}, filter, now); err != nil {
		t.Fatal(err)
	}

	if err := svc.GenerateMarkerThumbs(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GenerateMarkerThumbs returned error: %v", err)
	}
	// One grab per Marker; the range Marker grabs at its START time.
	if len(gen.grabTimes) != 2 || gen.grabTimes[0] != 12.5 || gen.grabTimes[1] != 20 {
		t.Fatalf("grab times = %v, want [12.5 20]", gen.grabTimes)
	}
	if gen.grabDsts[0] != "marker-12500.jpg" || gen.grabDsts[1] != "marker-20000.jpg" {
		t.Fatalf("grab files = %v, want timestamp-keyed names", gen.grabDsts)
	}

	// A second pass over unchanged markers grabs nothing.
	if err := svc.GenerateMarkerThumbs(context.Background(), "media-alpha"); err != nil {
		t.Fatal(err)
	}
	if len(gen.grabTimes) != 2 {
		t.Fatalf("grabs after no-op pass = %d, want 2", len(gen.grabTimes))
	}

	// Editing a Marker's timestamp targets a fresh fingerprint+timestamp key.
	point.StartSeconds = 42
	if _, err := st.UpdateMarker(context.Background(), point, filter, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateMarkerThumbs(context.Background(), "media-alpha"); err != nil {
		t.Fatal(err)
	}
	if len(gen.grabTimes) != 3 || gen.grabTimes[2] != 42 {
		t.Fatalf("grab times after edit = %v, want a new grab at 42", gen.grabTimes)
	}
	if _, err := svc.AssetPath(fingerprint, "marker-42000.jpg"); err != nil {
		t.Fatalf("edited marker thumbnail not servable: %v", err)
	}
}

func TestGenerateMarkerThumbsRemovesOrphanedMarkerThumbnails(t *testing.T) {
	gen := &fakeGen{}
	svc, st, fingerprint := newTestService(t, gen)
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}
	now := time.Unix(100, 0).UTC()
	marker, err := st.CreateMarker(context.Background(), store.Marker{
		MediaID: "media-alpha", Title: "Point", StartSeconds: 12.5,
	}, filter, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateMarkerThumbs(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GenerateMarkerThumbs returned error: %v", err)
	}
	if _, err := svc.AssetPath(fingerprint, "marker-12500.jpg"); err != nil {
		t.Fatalf("marker thumbnail missing before delete: %v", err)
	}

	if _, err := st.DeleteMarker(context.Background(), marker.ID, filter); err != nil {
		t.Fatalf("DeleteMarker returned error: %v", err)
	}
	if err := svc.GenerateMarkerThumbs(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GenerateMarkerThumbs after delete returned error: %v", err)
	}
	if _, err := svc.AssetPath(fingerprint, "marker-12500.jpg"); !errors.Is(err, ErrUnknownAsset) {
		t.Fatalf("deleted marker thumbnail error = %v, want ErrUnknownAsset", err)
	}
}

func TestAssetPathRejectsUnknownNamesAndTraversal(t *testing.T) {
	gen := &fakeGen{sheetCount: 1, sheetHeight: 1800}
	svc, _, fingerprint := newTestService(t, gen)
	if err := svc.GenerateTrickplay(context.Background(), "media-alpha"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.AssetPath(fingerprint, ManifestName); err != nil {
		t.Fatalf("manifest not servable: %v", err)
	}
	if _, err := svc.AssetPath(fingerprint, "sheet-0001.jpg"); err != nil {
		t.Fatalf("sheet not servable: %v", err)
	}
	for _, name := range []string{
		"", "..", "../trickplay.json", "trickplay.json/../x", "sheet-1.jpg",
		"marker-.jpg", "secret.txt", "sheet-0002.jpg", // valid shape, missing file
	} {
		if _, err := svc.AssetPath(fingerprint, name); err == nil {
			t.Fatalf("AssetPath(%q) succeeded, want ErrUnknownAsset", name)
		}
	}
}

func TestSweepJobGeneratesAndDedupes(t *testing.T) {
	gen := &fakeGen{
		sheetCount:         1,
		sheetHeight:        1800,
		waveformSamples:    synthBeatEnvelope(4000, 20),
		silenceSpans:       [][2]float64{{1, 4}},
		loudnessIntegrated: -18.2,
		loudnessRange:      4.5,
		loudnessTruePeak:   -1.1,
	}
	svc, st, fingerprint := newTestService(t, gen)
	audioTrack := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(audioTrack, []byte("audio for sweep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, audioFingerprint := seedAudioMedia(t, st, audioTrack, "Sweep Track")
	now := time.Unix(100, 0).UTC()

	job, err := NewSweepJob(now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.EnqueueJob(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	// A second enqueue while one is active dedupes to the same job.
	duplicate, err := st.EnqueueJob(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != created.ID {
		t.Fatalf("duplicate sweep enqueued: %s vs %s", duplicate.ID, created.ID)
	}

	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(SweepJobKind, SweepHandler(svc))
	if _, err := registry.RunOne(context.Background(), []store.JobClass{store.JobClassBackground}, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}

	// media-alpha (file-backed) got its assets; media-archive (no file) was
	// skipped without failing the sweep.
	if _, ok := svc.TrickplayAsset(fingerprint); !ok {
		t.Fatal("sweep did not generate trickplay sprites")
	}
	if _, ok := svc.PreviewClipAsset(fingerprint); !ok {
		t.Fatal("sweep did not generate the preview clip")
	}
	if _, ok := svc.WaveformAsset(audioFingerprint); !ok {
		t.Fatal("sweep did not generate the waveform asset for audio media")
	}
	if _, ok := svc.SilenceMapAsset(audioFingerprint); !ok {
		t.Fatal("sweep did not generate the silence map for audio media")
	}
	if _, ok := svc.BeatGridAsset(audioFingerprint); !ok {
		t.Fatal("sweep did not generate the beat grid for audio media")
	}
	if _, ok := svc.SyncLanesAsset(audioFingerprint); !ok {
		t.Fatal("sweep did not generate the sync lanes for audio media")
	}
	if _, ok := svc.LoudnessAsset(audioFingerprint); !ok {
		t.Fatal("sweep did not generate the loudness asset for audio media")
	}
}

func TestSweepHandlerCompletesExactFinalBatchWithoutExtraInvocation(t *testing.T) {
	svc, st, _ := newTestService(t, &fakeGen{})
	ctx := context.Background()
	ids, err := st.MediaIDsAfter(ctx, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	needed := (sweepJobBatchSize - len(ids)%sweepJobBatchSize) % sweepJobBatchSize
	for i := 0; i < needed; i++ {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("batch-fill-%d.bin", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("audio-%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		seedAudioMedia(t, st, path, fmt.Sprintf("Batch Fill %d", i))
	}
	ids, err = st.MediaIDsAfter(ctx, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 || len(ids)%sweepJobBatchSize != 0 {
		t.Fatalf("fixture count = %d, want positive multiple of %d", len(ids), sweepJobBatchSize)
	}
	created, err := st.EnqueueJob(ctx, store.Job{ID: "job-assets-exact", Kind: SweepJobKind, Class: store.JobClassBackground, DedupeKey: "assets-exact", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimNextJob(ctx, "worker", []store.JobClass{store.JobClassBackground}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != created.ID {
		t.Fatalf("claimed job %s, want %s", job.ID, created.ID)
	}
	handler := SweepHandler(svc)
	invocations := 0
	for {
		invocations++
		result, err := handler(ctx, job)
		if err == nil {
			break
		}
		if !errors.Is(err, jobs.ErrYielded) {
			t.Fatal(err)
		}
		job.Checkpoint = result.Checkpoint
	}
	if want := len(ids) / sweepJobBatchSize; invocations != want {
		t.Fatalf("handler invocations = %d, want %d", invocations, want)
	}
}

// TestGenerateWaveformSkipsSealedLibrary: a Sealed Library gets no Generated
// Asset — the generator's source picker short-circuits before any generation
// (ADR-0098 area 2).
func TestGenerateWaveformSkipsSealedLibrary(t *testing.T) {
	gen := &fakeGen{waveformSamples: []float64{0.2, 0.4, 0.6}}
	svc, st, _ := newTestService(t, gen)
	svc.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Sealed Track") // library-local

	if err := svc.GenerateWaveform(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateWaveform on sealed-Library media returned error: %v", err)
	}
	if gen.waveformCalls != 0 {
		t.Fatalf("waveformCalls = %d, want 0 (sealed Library must be skipped)", gen.waveformCalls)
	}
	if _, ok := svc.WaveformAsset(fingerprint); ok {
		t.Fatal("waveform generated for a sealed Library")
	}
}

func TestGenerateWaveformSkipsEncryptedUnsealedLibrary(t *testing.T) {
	// ENCPRIV-10b: a Generated Asset is a plaintext derivative. An Encrypted
	// Library that is currently Unsealed (sealed predicate false) must still be
	// skipped — no waveform, no existence marker.
	gen := &fakeGen{waveformSamples: []float64{0.2, 0.4, 0.6}}
	svc, st, _ := newTestService(t, gen)
	svc.SetSealed(func(store.LibraryID) bool { return false })
	svc.SetEncrypted(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Encrypted Track") // library-local

	if err := svc.GenerateWaveform(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateWaveform on encrypted-Library media returned error: %v", err)
	}
	if gen.waveformCalls != 0 {
		t.Fatalf("waveformCalls = %d, want 0 (Unsealed encrypted Library must be skipped)", gen.waveformCalls)
	}
	if _, ok := svc.WaveformAsset(fingerprint); ok {
		t.Fatal("waveform generated for an Unsealed encrypted Library")
	}
}

// TestSelectSourcePicksFingerprintedSourceForMultiVersionMedia exercises the
// shared source picker over a multi-Version Media (ADR-0098 area 2: the source
// set is every unsealed file of the Media, not only PrimaryMediaFile). The picker
// returns the deterministic default-Version primary for the unsealed Media.
func TestSelectSourcePicksFingerprintedSourceForMultiVersionMedia(t *testing.T) {
	gen := &fakeGen{}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "v0.bin")
	if err := os.WriteFile(track, []byte("audio v0"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, primaryFP := seedAudioMedia(t, st, track, "Multi-source Track")
	altPath := filepath.Join(t.TempDir(), "v1.bin")
	if err := os.WriteFile(altPath, []byte("audio v1 alternate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaFileVersion(context.Background(), media.ID, store.MediaFile{
		Path:               altPath,
		ContentFingerprint: "sha256:alt-source",
		VersionLabel:       "alt",
		PartOrdinal:        0,
	}, nil); err != nil {
		t.Fatalf("AddMediaFileVersion: %v", err)
	}

	m, file, ok, err := svc.audioSource(context.Background(), media.ID)
	if err != nil {
		t.Fatalf("audioSource: %v", err)
	}
	if !ok || m.ID != media.ID {
		t.Fatalf("audioSource = (%q, ok=%v), want this Media's source", m.ID, ok)
	}
	if file.ContentFingerprint != primaryFP {
		t.Fatalf("audioSource fingerprint = %q, want deterministic default %q", file.ContentFingerprint, primaryFP)
	}
	if err := os.Remove(track); err != nil {
		t.Fatal(err)
	}
	_, file, ok, err = svc.audioSource(context.Background(), media.ID)
	if err != nil || !ok || file.ContentFingerprint != "sha256:alt-source" {
		t.Fatalf("audioSource after missing default = (%q, ok=%v, err=%v), want valid alternate", file.ContentFingerprint, ok, err)
	}
}

func TestRankedGeneratedSourcesTableAndPermutation(t *testing.T) {
	files := []store.MediaFile{
		{Path: "/media/later.bin", ContentFingerprint: "sha256:later", PartOrdinal: 1},
		{Path: "/media/b.bin", ContentFingerprint: "sha256:b", VersionLabel: "b", PartOrdinal: 0},
		{Path: "", ContentFingerprint: "sha256:no-path", PartOrdinal: 0},
		{Path: "/media/no-fingerprint.bin", PartOrdinal: 0},
		{Path: "/media/default.bin", ContentFingerprint: "sha256:default", PartOrdinal: 0},
		{Path: "/media/a.bin", ContentFingerprint: "sha256:a", VersionLabel: "a", PartOrdinal: 0},
	}
	want := []string{"sha256:default", "sha256:a", "sha256:b"}
	assertOrder := func(candidate []store.MediaFile) {
		t.Helper()
		ranked := rankedGeneratedSources(candidate)
		got := make([]string, len(ranked))
		for i := range ranked {
			got[i] = ranked[i].ContentFingerprint
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ranked fingerprints = %v, want %v", got, want)
		}
	}
	assertOrder(files)
	for shift := range len(files) {
		permutation := append([]store.MediaFile(nil), files[shift:]...)
		permutation = append(permutation, files[:shift]...)
		assertOrder(permutation)
	}
	property := func(swaps []uint8) bool {
		permutation := append([]store.MediaFile(nil), files...)
		for i, raw := range swaps {
			a := i % len(permutation)
			b := int(raw) % len(permutation)
			permutation[a], permutation[b] = permutation[b], permutation[a]
		}
		ranked := rankedGeneratedSources(permutation)
		got := make([]string, len(ranked))
		for i := range ranked {
			got[i] = ranked[i].ContentFingerprint
		}
		return reflect.DeepEqual(got, want)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 500}); err != nil {
		t.Fatalf("ranking changed under input permutations: %v", err)
	}
}

// TestRewrapEncryptedLibraryAssetsSuppliesPersistedIdentity proves the rewrap
// enumerator hands the caller each asset's persisted identity — the media
// file's content fingerprint plus the asset name — never just a path
// (TECHDUR-CORE-CRYPTO-1 caller-bound identity).
func TestRewrapEncryptedLibraryAssetsSuppliesPersistedIdentity(t *testing.T) {
	svc, st, fp := newTestService(t, nil)
	file, err := st.PrimaryMediaFile(context.Background(), "media-alpha", store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := svc.assetDir(fp)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ManifestName, WaveformName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("ciphertext"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	type call struct{ path, fingerprint, name string }
	var calls []call
	err = svc.RewrapEncryptedLibraryAssets(context.Background(), []store.MediaFile{file}, func(path, contentFingerprint, assetName string) error {
		calls = append(calls, call{path, contentFingerprint, assetName})
		return nil
	})
	if err != nil {
		t.Fatalf("RewrapEncryptedLibraryAssets: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("rewrap calls = %d, want 2", len(calls))
	}
	for _, c := range calls {
		if c.fingerprint != fp {
			t.Fatalf("rewrap fingerprint = %q, want the persisted %q", c.fingerprint, fp)
		}
		if c.name != filepath.Base(c.path) {
			t.Fatalf("asset name %q does not match enumerated file %q", c.name, c.path)
		}
		if c.name != ManifestName && c.name != WaveformName {
			t.Fatalf("unexpected asset name %q", c.name)
		}
	}

	// A file without a fingerprint is skipped (no identity, nothing enumerable).
	calls = nil
	noFP := file
	noFP.ContentFingerprint = ""
	if err := svc.RewrapEncryptedLibraryAssets(context.Background(), []store.MediaFile{noFP}, func(path, contentFingerprint, assetName string) error {
		calls = append(calls, call{path, contentFingerprint, assetName})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("fingerprint-less file produced %d rewrap calls, want 0", len(calls))
	}
}

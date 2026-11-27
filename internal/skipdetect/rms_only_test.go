package skipdetect

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// The production detector must carry the capability every fingerprint
// segment now rests on.
var _ spectralFingerprinter = (*ffmpeg.Runner)(nil)

// rmsCorpusFile is one ordered sibling of the equal-RMS corpus: its loudness
// signature (head and EOF tail share it, the files being short) and the
// spectral frames that say what the audio actually is.
type rmsCorpusFile struct {
	loudness []float64
	frames   []uint32
}

// detectCorpus seeds the files as ordered siblings of 300 s each, runs
// detection for the one at target and returns its markers.
func detectCorpus(t *testing.T, target int, files ...rmsCorpusFile) []store.Marker {
	t.Helper()
	st := storetest.Open(t)
	enable(t, st, "library-local")
	det := &fakeDetector{
		chaptersByPath:    map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{},
		tailByPath:        map[string][]float64{},
		framesByPath:      map[string][]uint32{},
	}
	var ids []store.MediaID
	for i, f := range files {
		media, path := seedVideo(t, st, "library-local", "rms-corpus-"+string(rune('a'+i)), len(f.loudness))
		det.fingerprintByPath[path] = f.loudness
		det.tailByPath[path] = f.loudness
		det.framesByPath[path] = f.frames
		ids = append(ids, media.ID)
	}
	linkOrderedContainer(t, st, "library-local", "container-rms-corpus", ids...)
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), ids[target]); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	return markersOf(t, st, ids[target])
}

// sharedEdges builds two 300 s loudness signatures that share a 40 s opening
// and a 45 s closing run at level shared, with different levels between.
func sharedEdges(shared float64) [][]float64 {
	a, b := repeat(0.05, 300), repeat(0.75, 300)
	for _, sig := range [][]float64{a, b} {
		copy(sig[:40], repeat(shared, 40))
		copy(sig[255:], repeat(shared, 45))
	}
	return [][]float64{a, b}
}

// scatteredRecap builds three 300 s loudness signatures in which the current
// (last) file's opening repeats short runs found scattered across both prior
// files, the shape reverse fingerprinting reads as a recap.
func scatteredRecap(level float64) [][]float64 {
	first, second, current := repeat(0.05, 300), repeat(0.75, 300), repeat(0.95, 300)
	copy(current[2:6], repeat(level, 4))
	copy(current[7:11], repeat(level+0.1, 4))
	copy(current[12:16], repeat(level+0.2, 4))
	copy(first[60:64], repeat(level, 4))
	copy(first[180:184], repeat(level+0.2, 4))
	copy(second[120:124], repeat(level+0.1, 4))
	return [][]float64{first, second, current}
}

// corpus pairs each loudness signature with frames from sound(i, sig).
func corpus(sigs [][]float64, sound func(i int, sig []float64) []uint32) []rmsCorpusFile {
	files := make([]rmsCorpusFile, len(sigs))
	for i, sig := range sigs {
		files[i] = rmsCorpusFile{loudness: sig, frames: sound(i, sig)}
	}
	return files
}

var (
	// sharedSound: equal loudness runs are the same sound (the control).
	sharedSound = func(_ int, sig []float64) []uint32 { return soundOf(sig) }
	// unrelatedSound: every file is its own audio, whatever its loudness.
	unrelatedSound = func(i int, _ []float64) []uint32 { return noise(uint64(1000+i), 300) }
	// silentWhereQuiet: unrelated audio, with silent frames wherever the
	// loudness is zero.
	silentWhereQuiet = func(i int, sig []float64) []uint32 {
		frames := noise(uint64(2000+i), 300)
		for w, level := range sig {
			if level == 0 {
				copy(frames[w*spectralFramesPerWindow:], constFrames(ffmpeg.SpectralSilentFrame, 1))
			}
		}
		return frames
	}
	// spectralSilence: every frame silent, whatever the loudness says.
	spectralSilence = func(int, []float64) []uint32 {
		return constFrames(ffmpeg.SpectralSilentFrame, 300)
	}
)

// TestEqualRMSAndSilenceCorpusCreatesNoSegments is the CORE-0700 skipdetect
// acceptance corpus: loudness that agrees (the RMS signature is identical
// where it matters) never auto-creates an intro, credits or recap unless the
// spectral sub-fingerprints show the same sound, and shared silence is never
// evidence on either signature. The control rows prove each fixture does
// produce its segments when the sound is shared.
func TestEqualRMSAndSilenceCorpusCreatesNoSegments(t *testing.T) {
	edges, recap := []string{"intro", "credits"}, []string{"recap"}
	cases := []struct {
		name  string
		files []rmsCorpusFile
		kinds []string
		want  bool
	}{
		{"control: shared edges", corpus(sharedEdges(0.3), sharedSound), edges, true},
		{"control: scattered recap", corpus(scatteredRecap(0.25), sharedSound), recap, true},
		{"equal RMS, unrelated sound: edges", corpus(sharedEdges(0.3), unrelatedSound), edges, false},
		{"equal RMS, unrelated sound: recap", corpus(scatteredRecap(0.25), unrelatedSound), recap, false},
		{"shared silence: edges", corpus(sharedEdges(0), silentWhereQuiet), edges, false},
		{"shared silence: recap", corpus(scatteredRecap(0), silentWhereQuiet), recap, false},
		{"shared silence, shared sound model: edges", corpus(sharedEdges(0), sharedSound), edges, false},
		{"audible equal RMS over spectral silence: edges", corpus(sharedEdges(0.3), spectralSilence), edges, false},
		{"audible equal RMS over spectral silence: recap", corpus(scatteredRecap(0.25), spectralSilence), recap, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			markers := detectCorpus(t, len(tc.files)-1, tc.files...)
			for _, kind := range tc.kinds {
				if m, ok := segmentOfKind(markers, kind); ok != tc.want {
					t.Errorf("%s present = %v, want %v (marker %+v)", kind, ok, tc.want, m)
				}
			}
			if !tc.want {
				assertNoFingerprintSegments(t, markers)
			}
		})
	}
}

func assertNoFingerprintSegments(t *testing.T, markers []store.Marker) {
	t.Helper()
	for _, kind := range []string{"intro", "credits", "recap"} {
		if m, ok := segmentOfKind(markers, kind); ok {
			t.Errorf("%s auto-created over [%v, %v] by %s without shared sound", kind, m.StartSeconds, m.EndSeconds, m.Method)
		}
	}
}

// loudnessOnlyDetector has the loudness signature but no spectral capability.
type loudnessOnlyDetector struct{ inner *fakeDetector }

func (d loudnessOnlyDetector) Chapters(ctx context.Context, path string) ([]ffmpeg.Chapter, error) {
	return d.inner.Chapters(ctx, path)
}

func (d loudnessOnlyDetector) AudioFingerprint(ctx context.Context, path string, windowCount int) ([]float64, error) {
	return d.inner.AudioFingerprint(ctx, path, windowCount)
}

// TestLoudnessOnlyDetectorCreatesNoFingerprintSegment verifies a detector that
// can only read loudness fails the fingerprint analysis instead of placing a
// segment from RMS evidence, even on a fixture whose loudness shares a long
// opening run.
func TestLoudnessOnlyDetectorCreatesNoFingerprintSegment(t *testing.T) {
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", "loudness-only-a", 1800)
	b, pathB := seedVideo(t, st, "library-local", "loudness-only-b", 1800)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-loudness-only", a.ID, b.ID)
	det := loudnessOnlyDetector{&fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			pathA: append(repeat(0.5, 40), repeat(0.1, 140)...),
			pathB: append(repeat(0.5, 40), repeat(0.9, 140)...),
		},
	}}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), a.ID); !errors.Is(err, errNoSpectralFingerprints) {
		t.Fatalf("DetectForMedia error = %v, want errNoSpectralFingerprints", err)
	}
	if markers := markersOf(t, st, a.ID); len(markers) != 0 {
		t.Fatalf("loudness-only detector placed segments: %+v", markers)
	}
}

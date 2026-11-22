package skipdetect

import (
	"context"
	"math/rand/v2"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// spectralDetector adds the spectral capability to fakeDetector. Signatures
// are whole-file frame sequences keyed by path; the head call returns the
// first seconds of frames and the tail call the last.
type spectralDetector struct {
	*fakeDetector
	framesByPath map[string][]uint32
}

func (d *spectralDetector) SpectralFingerprint(_ context.Context, path string, seconds int) ([]uint32, error) {
	frames := d.framesByPath[path]
	if n := seconds * spectralFramesPerWindow; len(frames) > n {
		frames = frames[:n]
	}
	return append([]uint32(nil), frames...), nil
}

func (d *spectralDetector) SpectralFingerprintTail(_ context.Context, path string, seconds, _ int) ([]uint32, error) {
	frames := d.framesByPath[path]
	if n := seconds * spectralFramesPerWindow; len(frames) > n {
		frames = frames[len(frames)-n:]
	}
	return append([]uint32(nil), frames...), nil
}

// noise returns seconds of unrelated, detailed sub-fingerprints.
func noise(seed uint64, seconds float64) []uint32 {
	rng := rand.New(rand.NewPCG(seed, ^seed)) // #nosec G404 -- deterministic test signature
	frames := make([]uint32, int(seconds*float64(spectralFramesPerWindow)))
	for i := range frames {
		for !spectralFrameDetailed(frames[i]) {
			frames[i] = rng.Uint32()
		}
	}
	return frames
}

// jitter flips up to two random bits per frame, as a re-encode does.
func jitter(seed uint64, frames []uint32) []uint32 {
	rng := rand.New(rand.NewPCG(seed, seed+1)) // #nosec G404 -- deterministic test signature
	out := append([]uint32(nil), frames...)
	for i := range out {
		flips := rng.IntN(3)
		if !spectralFrameDetailed(frames[i]) {
			continue // a re-encode keeps a lone tone's hash
		}
		for range flips {
			out[i] ^= 1 << rng.IntN(32)
		}
		if !spectralFrameDetailed(out[i]) {
			out[i] = frames[i]
		}
	}
	return out
}

func join(parts ...[]uint32) []uint32 {
	var out []uint32
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

type spectralSibling struct {
	title    string
	duration int
	frames   []uint32
}

// detectSpectral seeds ordered siblings, runs detection for the one at index
// target with the spectral detector, and returns its markers.
func detectSpectral(t *testing.T, target int, siblings ...spectralSibling) []store.Marker {
	t.Helper()
	st := storetest.Open(t)
	enable(t, st, "library-local")
	det := &spectralDetector{
		fakeDetector: &fakeDetector{chaptersByPath: map[string][]ffmpeg.Chapter{}, fingerprintByPath: map[string][]float64{}},
		framesByPath: map[string][]uint32{},
	}
	var ids []store.MediaID
	for _, sib := range siblings {
		media, path := seedVideo(t, st, "library-local", sib.title, sib.duration)
		det.framesByPath[path] = sib.frames
		ids = append(ids, media.ID)
	}
	linkOrderedContainer(t, st, "library-local", "container-spectral", ids...)
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), ids[target]); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	return markersOf(t, st, ids[target])
}

func wantSegment(t *testing.T, markers []store.Marker, kind string, start, end float64) {
	t.Helper()
	m, ok := segmentOfKind(markers, kind)
	if !ok {
		t.Fatalf("no %s segment; markers = %+v", kind, markers)
	}
	if m.EndSeconds == nil || m.StartSeconds != start || *m.EndSeconds != end {
		t.Fatalf("%s = [%v, %v], want [%v, %v]", kind, m.StartSeconds, m.EndSeconds, start, end)
	}
	if m.Method != MethodFingerprint || m.Confidence < fingerprintAutoApplyThreshold {
		t.Fatalf("%s method/confidence = %q/%v", kind, m.Method, m.Confidence)
	}
}

func wantNoSegment(t *testing.T, markers []store.Marker, kinds ...string) {
	t.Helper()
	for _, kind := range kinds {
		if m, ok := segmentOfKind(markers, kind); ok {
			t.Errorf("unexpected %s over [%v, %v]", kind, m.StartSeconds, m.EndSeconds)
		}
	}
}

// TestSpectralIntroAtDifferentTimes: the shared theme starts at 10 s in this
// Media (after a cold open) and at 4 s in its sibling, re-encoded. Loudness
// matching from window 0 cannot see it; the offset search finds it in both.
func TestSpectralIntroAtDifferentTimes(t *testing.T) {
	theme := noise(1, 30)
	a := spectralSibling{"cold-open-a", 1800, join(noise(2, 10), theme, noise(3, 1760))}
	b := spectralSibling{"cold-open-b", 1800, join(noise(4, 4), jitter(5, theme), noise(6, 1766))}
	wantSegment(t, detectSpectral(t, 0, a, b), "intro", 10, 40)
	wantSegment(t, detectSpectral(t, 1, a, b), "intro", 4, 34)
}

// TestSpectralIntroOffsetBySubWindowShift: the sibling's theme sits 3 frames
// (375 ms) off this Media's window grid; the run still covers the theme to
// within a window.
func TestSpectralIntroOffsetBySubWindowShift(t *testing.T) {
	theme := noise(1, 30)
	a := spectralSibling{"shift-a", 1800, join(noise(2, 12), theme, noise(3, 1758))}
	b := spectralSibling{"shift-b", 1800, join(noise(4, 6), noise(7, 3.0/8), jitter(5, theme), noise(6, 1764))}
	wantSegment(t, detectSpectral(t, 0, a, b), "intro", 12, 42)
}

func TestSpectralDifferentAudioDoesNotMatch(t *testing.T) {
	a := spectralSibling{"diff-a", 1800, noise(1, 1800)}
	b := spectralSibling{"diff-b", 1800, noise(2, 1800)}
	wantNoSegment(t, detectSpectral(t, 0, a, b), "intro", "credits")
}

// TestSpectralSharedSilenceDoesNotMatch: two files silent at the same time
// share no audio.
func TestSpectralSharedSilenceDoesNotMatch(t *testing.T) {
	silence := make([]uint32, 40*spectralFramesPerWindow)
	a := spectralSibling{"silent-a", 1800, join(silence, noise(1, 1760))}
	b := spectralSibling{"silent-b", 1800, join(silence, noise(2, 1760))}
	wantNoSegment(t, detectSpectral(t, 0, a, b), "intro", "credits")
}

// TestSpectralIdenticalFilesYieldNoIntro: a duplicate encode matches over the
// whole overlap, so no boundary was seen and nothing is written (CORE-3079).
func TestSpectralIdenticalFilesYieldNoIntro(t *testing.T) {
	frames := noise(1, 1800)
	a := spectralSibling{"dup-a", 1800, frames}
	b := spectralSibling{"dup-b", 1800, jitter(2, frames)}
	wantNoSegment(t, detectSpectral(t, 0, a, b), "intro", "credits")
}

// TestSpectralLongestRunWins: one sibling shares 20 s of the opening, another
// shares 26 s; the longer run is the intro.
func TestSpectralLongestRunWins(t *testing.T) {
	theme := noise(1, 26)
	a := spectralSibling{"long-a", 1800, join(noise(2, 5), theme, noise(3, 1769))}
	b := spectralSibling{"long-b", 1800, join(noise(4, 8), theme[:20*spectralFramesPerWindow], noise(5, 1772))}
	c := spectralSibling{"long-c", 1800, join(noise(6, 2), theme, noise(7, 1772))}
	wantSegment(t, detectSpectral(t, 0, a, b, c), "intro", 5, 31)
}

// TestSpectralCreditsBeforePostCreditsScene: credits shared at different
// distances from EOF (each file closes with its own scene) map onto this
// Media's timeline through the tail offset.
func TestSpectralCreditsBeforePostCreditsScene(t *testing.T) {
	credits := noise(1, 50)
	a := spectralSibling{"tail-a", 1800, join(noise(2, 1730), credits, noise(3, 20))}
	b := spectralSibling{"tail-b", 1900, join(noise(4, 1845), jitter(5, credits), noise(6, 5))}
	wantSegment(t, detectSpectral(t, 0, a, b), "credits", 1730, 1780)
}

// TestSpectralShortFileKeepsIntroAndCreditsApart: on a short file the head
// and tail regions both cover the whole runtime. The intro must come from the
// first half and the credits from the second, even when the intro is the
// longer shared run.
func TestSpectralShortFileKeepsIntroAndCreditsApart(t *testing.T) {
	theme, credits := noise(1, 25), noise(2, 18)
	episode := func(title string, seed uint64) spectralSibling {
		return spectralSibling{title, 111, join(noise(seed, 6), theme, noise(seed+1, 61), credits, noise(seed+2, 1))}
	}
	markers := detectSpectral(t, 1, episode("short-a", 10), episode("short-b", 20), episode("short-c", 30))
	wantSegment(t, markers, "intro", 6, 31)
	wantSegment(t, markers, "credits", 92, 110)
}

func TestSpectralSharedRunConfidence(t *testing.T) {
	theme := noise(1, 30)
	self := join(noise(2, 10), theme, noise(3, 60))
	run := spectralSharedRun(self, join(noise(4, 4), jitter(5, theme), noise(6, 60)), runLimits{maxWindows: maxIntroWindows, durationSeconds: 100})
	if run.startWindow != 10 || run.windows != 30 {
		t.Fatalf("run = %+v, want 30 windows from window 10", run)
	}
	if run.avgDiff > 1.5 || run.confidence() < fingerprintAutoApplyThreshold {
		t.Fatalf("re-encoded theme avgDiff = %.2f confidence = %.2f", run.avgDiff, run.confidence())
	}
	if run.windows >= run.compared {
		t.Fatalf("compared = %d does not exceed the run", run.compared)
	}
}

// constFrames returns seconds of one sub-fingerprint.
func constFrames(hash uint32, seconds float64) []uint32 {
	frames := make([]uint32, int(seconds*float64(spectralFramesPerWindow)))
	for i := range frames {
		frames[i] = hash
	}
	return frames
}

// CORE-3111 review: silence inside an otherwise identical pair breaks the
// match into runs shorter than the overlap, but the pair never diverges, so it
// is the same file and yields no intro.
func TestSpectralIdenticalBrokenBySilenceYieldNoIntro(t *testing.T) {
	frames := join(noise(1, 40), constFrames(ffmpeg.SpectralSilentFrame, 2), noise(2, 138))
	limits := runLimits{maxWindows: maxIntroWindows, durationSeconds: 1800}
	if run := spectralSharedRun(frames, jitter(3, frames), limits); run.windows != 0 {
		t.Fatalf("identical pair matched %+v", run)
	}
	markers := detectSpectral(t, 0,
		spectralSibling{"Silence 1", 1800, join(frames, noise(4, 1620))},
		spectralSibling{"Silence 2", 1800, join(jitter(5, frames), noise(6, 1620))},
	)
	wantNoSegment(t, markers, "intro")
}

// CORE-3111 review: a pure tone sets about one bit, so two different tones sit
// a few bits apart. Different low-detail frames count as unrelated.
func TestSpectralLowDetailFramesDoNotMatch(t *testing.T) {
	markers := detectSpectral(t, 0,
		spectralSibling{"Tone 1", 1800, join(constFrames(0x100, 30), noise(1, 1770))},
		spectralSibling{"Tone 2", 1800, join(constFrames(0x200, 30), noise(2, 1770))},
	)
	wantNoSegment(t, markers, "intro")
	for _, hash := range []uint32{0, 0x100, 0x7, ^uint32(0x7)} {
		if spectralFrameDetailed(hash) {
			t.Errorf("hash %#x counted as detailed", hash)
		}
	}
	if !spectralFrameDetailed(0xF0F0) {
		t.Error("hash 0xF0F0 not detailed")
	}
}

// CORE-3111 review: an intro must end in the first half and credits start in
// the second, so the two can never overlap.
func TestSpectralIntroAndCreditsCannotOverlap(t *testing.T) {
	intro := runLimits{maxWindows: maxIntroWindows, durationSeconds: 100}
	if (sharedRun{startWindow: 30, windows: 40, compared: 100}).plausible(intro) {
		t.Error("intro [30,70) on a 100 s file accepted")
	}
	if !(sharedRun{startWindow: 10, windows: 40, compared: 100}).plausible(intro) {
		t.Error("intro [10,50) on a 100 s file rejected")
	}
	credits := runLimits{durationSeconds: 100, closing: true}
	if (sharedRun{startWindow: 45, windows: 30, compared: 100}).plausible(credits) {
		t.Error("credits [45,75) on a 100 s file accepted")
	}
	if !(sharedRun{startWindow: 55, windows: 30, compared: 100}).plausible(credits) {
		t.Error("credits [55,85) on a 100 s file rejected")
	}
}

// CORE-3111 review: a longer loose run must not hide a shorter trusted one.
func TestSpectralTrustedRunBeatsLongerLooseRun(t *testing.T) {
	loose := sharedRun{windows: 21, compared: 100, avgDiff: 5, diffScale: spectralChanceBits}
	tight := sharedRun{windows: 20, compared: 100, avgDiff: 0, diffScale: spectralChanceBits}
	if loose.trusted() || !tight.trusted() {
		t.Fatalf("trusted: loose %v (%.2f), tight %v (%.2f)", loose.trusted(), loose.confidence(), tight.trusted(), tight.confidence())
	}
	if !tight.beats(loose) || loose.beats(tight) {
		t.Fatal("loose 21-window run beat the trusted 20-window run")
	}
	longer := sharedRun{windows: 25, compared: 100, avgDiff: 0.5, diffScale: spectralChanceBits}
	if !longer.beats(tight) {
		t.Fatal("longer trusted run did not win")
	}
}

// A shared intro made of tones still matches: identical low-detail frames
// agree, as long as each window holds enough detailed frames.
func TestSpectralSharedTonesMatchExactly(t *testing.T) {
	theme := noise(7, 25)
	for i := range theme {
		if i%8 >= 2 {
			theme[i] = 1 << (i % 5)
		}
	}
	markers := detectSpectral(t, 0,
		spectralSibling{"Tones 1", 1800, join(noise(1, 6), theme, noise(2, 1769))},
		spectralSibling{"Tones 2", 1800, join(noise(3, 6), jitter(4, theme), noise(5, 1769))},
	)
	wantSegment(t, markers, "intro", 6, 31)
	if _, verdict := spectralWindowDistance(constFrames(0x100, 1), constFrames(0x100, 1)); verdict != windowIncomparable {
		t.Error("a window with no detailed frames compared")
	}
}

// CORE-3111 review r1: a slowly changing opening still matches itself one
// frame off; the aligned offset never diverges, so the pair is one recording.
func TestSpectralIdenticalSlowOpeningYieldsNoIntro(t *testing.T) {
	var opening []uint32
	for _, hash := range noise(8, 4) { // 32 one-second steps
		opening = append(opening, constFrames(hash, 1)...)
	}
	frames := join(opening, constFrames(ffmpeg.SpectralSilentFrame, 2), noise(9, 146))
	limits := runLimits{maxWindows: maxIntroWindows, durationSeconds: 1800}
	if run := spectralSharedRun(frames, frames, limits); run.windows != 0 {
		t.Fatalf("identical pair matched %+v", run)
	}
}

// CORE-3111 review r1: silence on one side after a shared intro is a
// difference, so the intro keeps its end.
func TestSpectralOneSidedSilenceEndsIntro(t *testing.T) {
	theme := noise(10, 24)
	self := join(theme, constFrames(ffmpeg.SpectralSilentFrame, 60))
	sibling := join(jitter(11, theme), noise(12, 60))
	limits := runLimits{maxWindows: maxIntroWindows, durationSeconds: 1800}
	run := spectralSharedRun(self, sibling, limits)
	if run.startWindow != 0 || run.windows != 24 || !run.trusted() {
		t.Fatalf("got %+v, want a trusted 24-window intro at 0", run)
	}
}

// CORE-3111 review r2: one sibling reprises the theme at 156-180 s. That
// offset overlaps only the other's intro and never diverges; it must not void
// the real 0-24 s intro.
func TestSpectralThemeRepriseKeepsIntro(t *testing.T) {
	theme := noise(11, 24)
	markers := detectSpectral(t, 0,
		spectralSibling{"Reprise 1", 1800, join(theme, noise(12, 1776))},
		spectralSibling{"Reprise 2", 1800, join(jitter(13, theme), noise(14, 132), jitter(15, theme), noise(16, 1620))},
	)
	wantSegment(t, markers, "intro", 0, 24)
}

// CORE-3111 review r2: after a shared intro both siblings carry the same hum
// in six frames a second; the other two are silence in one and a tone in the
// other. Two differences around identical hum are not a match, so the intro
// ends at 24 s.
func TestSpectralHumAroundDifferencesDoesNotExtendIntro(t *testing.T) {
	theme := noise(21, 24)
	humA, humB := constFrames(0x100, 24), constFrames(0x100, 24)
	for i := range humA {
		if i%spectralFramesPerWindow >= 6 {
			humA[i], humB[i] = ffmpeg.SpectralSilentFrame, 0x400
		}
	}
	markers := detectSpectral(t, 0,
		spectralSibling{"Hum 1", 1800, join(theme, humA, noise(22, 1752))},
		spectralSibling{"Hum 2", 1800, join(jitter(23, theme), humB, noise(24, 1752))},
	)
	wantSegment(t, markers, "intro", 0, 24)
}

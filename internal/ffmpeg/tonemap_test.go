package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// conformingFFmpegEnv, when set to "1", declares that the FFmpeg on this host is
// a build Beamfall ships or supports, so failing to run the production tone-map
// chain is a RED, not a tolerated developer-machine condition. The release gate
// and the shipped image set it; a laptop with a stock Homebrew build does not.
const conformingFFmpegEnv = "BEAMFALL_FFMPEG_CONFORMING"

// TestProductionToneMapChainRunsOrIsRefused EXECUTES the production HDR→SDR
// chain against the FFmpeg binary this host actually resolves, and requires
// Beamfall's own spawn-time decision to agree with what that binary can really
// do.
//
// This is the assertion FFMPEG-TONEMAP-BT2390-INVALID-1 was missing. The
// shipped recipe named `tonemap=bt2390` — an option the software `tonemap`
// filter has never accepted — and every existing test compared argv STRINGS, so
// the chain was never once run. It failed at filter-graph parse on every
// platform, including the pinned jellyfin-ffmpeg build in the shipped image.
//
// The chain is read from hdrToSDRFilters(), never copied, so this test can only
// ever verify the recipe production emits.
func TestProductionToneMapChainRunsOrIsRefused(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatalf("ffmpeg is not on PATH, so the production tone-map recipe cannot be executed here — "+
			"and a recipe nobody executes is exactly how tonemap=bt2390 shipped: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	chain := strings.Join(hdrToSDRFilters(), ",")
	_, runErr := DefaultToneMapRunner(ctx, softwareToneMapRender())
	probe := ProbeToneMap(ctx, ToneMapOptions{})

	hdr := Rendition{Height: 720, ColorTransfer: "smpte2084"}
	if !hdr.needsToneMap() {
		t.Fatal("fixture rendition no longer tone-maps; this test would verify nothing")
	}
	refusal := hdr.ToneMapRefusal()

	switch {
	case probe.Status == ToneMapWorking && refusal != "":
		t.Fatalf("this build runs the tone-map chain %q, but Beamfall refuses HDR renditions on it (%s): "+
			"HDR titles are denied a transcode the host can actually perform", chain, refusal)
	case probe.Status != ToneMapWorking && refusal == "":
		t.Fatalf("Beamfall would spawn the tone-map chain %q on a build that cannot run it "+
			"(probe: %s, ffmpeg: %v). Every PQ/HLG/DoVi-7/8 re-encode would die at filter-graph parse. "+
			"Fix the recipe, or make ToneMapRefusal fail closed — never substitute a different tone-map curve.",
			chain, probe.Failure, runErr)
	}

	if probe.Status != ToneMapWorking {
		if os.Getenv(conformingFFmpegEnv) == "1" {
			t.Fatalf("%s=1 declares this host's FFmpeg conforming, but it cannot run the production "+
				"tone-map chain %q (probe: %s, ffmpeg: %v)", conformingFFmpegEnv, chain, probe.Failure, runErr)
		}
		t.Logf("this host's FFmpeg cannot run %q (%s); Beamfall correctly refuses HDR renditions here. "+
			"Set %s=1 on a conforming build to make this a hard failure.", chain, probe.Failure, conformingFFmpegEnv)
		return
	}

	// The chain ran AND moved the highlight: verify the readback shape directly
	// so a probe that silently stopped measuring cannot pass this test either.
	toneMapped, err := DefaultToneMapRunner(ctx, softwareToneMapRender())
	if err != nil {
		t.Fatalf("tone-mapped render failed after the probe reported working: %v", err)
	}
	baseline, err := DefaultToneMapRunner(ctx, ToneMapRender{Source: toneMapProbeSource})
	if err != nil {
		t.Fatalf("baseline render failed: %v", err)
	}
	if len(toneMapped) != toneMapProbeFrameBytes || len(baseline) != toneMapProbeFrameBytes {
		t.Fatalf("frame sizes = %d/%d, want %d each", len(toneMapped), len(baseline), toneMapProbeFrameBytes)
	}
	drop := highlightSample(baseline) - highlightSample(toneMapped)
	t.Logf("chain %q: frame mean %.1f -> %.1f; highlight band %.1f -> %.1f (drop %.1f)",
		chain, meanSample(baseline), meanSample(toneMapped),
		highlightSample(baseline), highlightSample(toneMapped), drop)
	if drop < toneMapMinHighlightDrop {
		t.Fatalf("tone-mapped highlight band fell only %.1f below baseline (want >= %d): the chain ran but "+
			"did not compress the 1000-nit highlight — this is the washed-out failure",
			drop, toneMapMinHighlightDrop)
	}
	if got := highlightSample(toneMapped); got < toneMapMinHighlight {
		t.Fatalf("tone-mapped highlight band = %.1f (want >= %d): the chain ran but drove the picture into "+
			"darkness", got, toneMapMinHighlight)
	}
}

// TestProbeToneMapVerifiesTheProductionChain pins the anti-drift property: the
// probe hands the runner exactly what hdrToSDRFilters() returns, so it can never
// certify a chain other than the one the transcoder emits.
func TestProbeToneMapVerifiesTheProductionChain(t *testing.T) {
	var handed []ToneMapRender
	probe := ProbeToneMap(context.Background(), ToneMapOptions{
		Runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
			handed = append(handed, render)
			return workingToneMapFrame(render), nil
		},
	})
	if probe.Status != ToneMapWorking {
		t.Fatalf("probe = %+v, want working", probe)
	}
	if len(handed) != 2 {
		t.Fatalf("renders = %d, want 2 (tone-mapped + baseline)", len(handed))
	}
	if !slices.Equal(handed[0].Filters, hdrToSDRFilters()) {
		t.Fatalf("probe rendered %v, want the production chain %v", handed[0].Filters, hdrToSDRFilters())
	}
	if len(handed[1].Filters) != 0 {
		t.Fatalf("baseline render carried filters %v, want none", handed[1].Filters)
	}
}

// TestToneMapProbeArgsAreBoundedAndLeastPrivilege pins the hardening properties
// of the probe argv: a synthetic source (no untrusted input can ever reach it),
// exactly one frame, no stdin, and the file-only protocol allowlist immediately
// before -i.
func TestToneMapProbeArgsAreBoundedAndLeastPrivilege(t *testing.T) {
	args := toneMapProbeArgs(softwareToneMapRender())
	iIdx := slices.Index(args, "-i")
	if iIdx < 2 {
		t.Fatalf("no -i in probe args: %v", args)
	}
	if args[iIdx-2] != "-protocol_whitelist" || args[iIdx-1] != "file" {
		t.Fatalf("protocol allowlist not immediately before -i: %v", args)
	}
	if args[iIdx+1] != toneMapProbeInput {
		t.Fatalf("probe input = %q, want the synthetic source %q", args[iIdx+1], toneMapProbeInput)
	}
	if frames := slices.Index(args, "-frames:v"); frames < 0 || args[frames+1] != "1" {
		t.Fatalf("probe is not bounded to one frame: %v", args)
	}
	if !slices.Contains(args, "-nostdin") {
		t.Fatalf("probe args lack -nostdin: %v", args)
	}
	graph := args[slices.Index(args, "-vf")+1]
	if !strings.HasPrefix(graph, toneMapProbeSource) || !strings.HasSuffix(graph, toneMapProbeReadback) {
		t.Fatalf("probe graph %q must run the production chain between the tagged source and the gray readback", graph)
	}
}

// TestProbeToneMapClassifiesEveryFailureClosed drives every branch with a stub
// runner. Not one of them resolves to "assume supported".
func TestProbeToneMapClassifiesEveryFailureClosed(t *testing.T) {
	tests := []struct {
		name        string
		runner      ToneMapRunner
		wantStatus  ToneMapStatus
		wantFailure ToneMapFailure
	}{
		{
			name: "chain rejected by the build",
			runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
				if len(render.Filters) > 0 {
					return nil, errors.New(`Unable to parse "tonemap" option value "bt2390"`)
				}
				return flatFrame(toneMapProbeFrameBytes, 192), nil
			},
			wantStatus:  ToneMapUnsupported,
			wantFailure: ToneMapFailureChainUnrunnable,
		},
		{
			name:        "no ffmpeg binary",
			runner:      func(context.Context, ToneMapRender) ([]byte, error) { return nil, exec.ErrNotFound },
			wantStatus:  ToneMapUnsupported,
			wantFailure: ToneMapFailureBinaryMissing,
		},
		{
			name:        "wedged binary",
			runner:      func(context.Context, ToneMapRender) ([]byte, error) { return nil, context.DeadlineExceeded },
			wantStatus:  ToneMapUnsupported,
			wantFailure: ToneMapFailureTimeout,
		},
		{
			name: "partial frame",
			runner: func(context.Context, ToneMapRender) ([]byte, error) {
				return flatFrame(toneMapProbeFrameBytes/2, 255), nil
			},
			wantStatus:  ToneMapUnsupported,
			wantFailure: ToneMapFailureNoFrame,
		},
		{
			name: "baseline render unsound",
			runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
				if len(render.Filters) > 0 {
					return flatFrame(toneMapProbeFrameBytes, 255), nil
				}
				return nil, errors.New("baseline failed")
			},
			wantStatus:  ToneMapUnsupported,
			wantFailure: ToneMapFailureInconclusive,
		},
		{
			name: "chain runs but is a pass-through",
			runner: func(context.Context, ToneMapRender) ([]byte, error) {
				return flatFrame(toneMapProbeFrameBytes, 192), nil
			},
			wantStatus:  ToneMapUnsupported,
			wantFailure: ToneMapFailureNoTransform,
		},
		{
			// A wrong-direction transform: it moves the highlight far enough
			// to clear the transform floor, but lands it in darkness.
			name: "chain crushes the highlight",
			runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
				if len(render.Filters) > 0 {
					return flatFrame(toneMapProbeFrameBytes, 5), nil
				}
				return flatFrame(toneMapProbeFrameBytes, 173), nil
			},
			wantStatus:  ToneMapUnsupported,
			wantFailure: ToneMapFailureCrushed,
		},
		{
			name:        "verified working",
			runner:      func(_ context.Context, render ToneMapRender) ([]byte, error) { return workingToneMapFrame(render), nil },
			wantStatus:  ToneMapWorking,
			wantFailure: ToneMapFailureNone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ProbeToneMap(context.Background(), ToneMapOptions{Runner: tc.runner})
			if got.Status != tc.wantStatus || got.Failure != tc.wantFailure {
				t.Fatalf("probe = %+v, want {%s %s}", got, tc.wantStatus, tc.wantFailure)
			}
		})
	}
}

// TestToneMapRefusalFailsClosedWithoutAVerifiedChain is the negative control:
// with the tone-map capability not proven, every HDR re-encode is refused with
// the wire reason, and nothing may be spawned. A copied video stream is never
// affected — it is not re-encoded.
func TestToneMapRefusalFailsClosedWithoutAVerifiedChain(t *testing.T) {
	withToneMapSupport(t, false)

	unverified := []struct {
		name string
		r    Rendition
	}{
		{"hdr10 pq", Rendition{Height: 720, ColorTransfer: "smpte2084"}},
		{"hlg", Rendition{Height: 720, ColorTransfer: "arib-std-b67"}},
		{"dolby vision profile 8", Rendition{Height: 720, DoViProfile: 8, DoViBLCompatID: 6}},
	}
	for _, tc := range unverified {
		if got := tc.r.ToneMapRefusal(); got != ToneMapRefusalUnverified {
			t.Fatalf("%s refusal = %q, want %q", tc.name, got, ToneMapRefusalUnverified)
		}
	}
	if got := (Rendition{Height: 720, ColorTransfer: "smpte2084", CopyVideo: true}).ToneMapRefusal(); got != "" {
		t.Fatalf("audio-only (copied video) refusal = %q, want none", got)
	}
	if got := (Rendition{Height: 720}).ToneMapRefusal(); got != "" {
		t.Fatalf("SDR refusal = %q, want none", got)
	}
	if got := (Rendition{Height: 720, DoViProfile: 5}).ToneMapRefusal(); got != ToneMapRefusalDoViP5 {
		t.Fatalf("Dolby Vision profile 5 refusal = %q, want %q", got, ToneMapRefusalDoViP5)
	}

	// The refusal must reach the spawn boundary, not merely the predicate.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := New()
	dir := t.TempDir()
	for _, start := range []struct {
		name string
		fn   func() (*HLSProcess, error)
	}{
		{"StartHLS", func() (*HLSProcess, error) {
			return runner.StartHLS(ctx, "/media/hdr.mkv", dir, unverified[0].r)
		}},
		{"StartHLSSegments", func() (*HLSProcess, error) {
			return runner.StartHLSSegments(ctx, "/media/hdr.mkv", dir, 0, 4, unverified[0].r)
		}},
	} {
		proc, err := start.fn()
		if err == nil {
			t.Fatalf("%s spawned an HDR transcode on a build with no verified tone-map chain (proc=%v)", start.name, proc)
		}
		if !strings.Contains(err.Error(), ToneMapRefusalUnverified) {
			t.Fatalf("%s error = %v, want it to name %q", start.name, err, ToneMapRefusalUnverified)
		}
	}
}

// TestToneMapRefusalAllowsHDRWhenTheChainIsVerified is the matching positive
// control: a proven build must not be refused, or the fail-closed gate would
// simply disable HDR everywhere.
func TestToneMapRefusalAllowsHDRWhenTheChainIsVerified(t *testing.T) {
	withToneMapSupport(t, true)
	if got := (Rendition{Height: 720, ColorTransfer: "smpte2084"}).ToneMapRefusal(); got != "" {
		t.Fatalf("verified build refusal = %q, want none", got)
	}
	if got := (Rendition{Height: 720, DoViProfile: 5}).ToneMapRefusal(); got != ToneMapRefusalDoViP5 {
		t.Fatalf("Dolby Vision profile 5 refusal = %q, want %q", got, ToneMapRefusalDoViP5)
	}
}

// withToneMapSupport states the FFmpeg build a test means, instead of inheriting
// whichever binary sits on the developer's PATH.
func withToneMapSupport(t *testing.T, supported bool) {
	t.Helper()
	previous := toneMapSupported
	toneMapSupported = func() bool { return supported }
	t.Cleanup(func() { toneMapSupported = previous })
}

// softwareToneMapRender is the production software chain over the PQ fixture,
// as the probe hands it to the runner.
func softwareToneMapRender() ToneMapRender {
	return ToneMapRender{Filters: hdrToSDRFilters(), Source: toneMapProbeSource}
}

func flatFrame(size int, sample byte) []byte {
	return bytes.Repeat([]byte{sample}, size)
}

// workingToneMapFrame mimics the pinned build's measured readback of the
// highlight band: 173 untone-mapped (the ~1000-nit end of the source ramp,
// measured 173.125) falling to 123 once the chain compresses it into the 100-nit
// SDR reference (measured 122.859).
func workingToneMapFrame(render ToneMapRender) []byte {
	if len(render.Filters) > 0 {
		return flatFrame(toneMapProbeFrameBytes, 123)
	}
	return flatFrame(toneMapProbeFrameBytes, 173)
}

package playback

import (
	"os"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
)

// TestMain pins the FFmpeg tone-map capability for this package's tests.
//
// FullTranscodeRefusal fails closed: an HDR source on a build that cannot run
// Beamfall's tone-map chain is refused rather than offered a transcode that
// would die at filter-graph parse. That verdict depends on whichever FFmpeg
// happens to sit on the runner's PATH, which is not what negotiation tests are
// about — they assert what playbackInfo decides GIVEN a capability. So the
// package default is "conforming build", and the tests that are about the
// capability itself state the other case explicitly via withToneMapSupport.
func TestMain(m *testing.M) {
	toneMapSupported = func() bool { return true }
	os.Exit(m.Run())
}

// withToneMapSupport states the FFmpeg build one test means.
func withToneMapSupport(t *testing.T, supported bool) {
	t.Helper()
	previous := toneMapSupported
	toneMapSupported = func() bool { return supported }
	t.Cleanup(func() { toneMapSupported = previous })
}

// TestFullTranscodeRefusalFailsClosedOnAnUnverifiedToneMapBuild is the wire-side
// half of the FFMPEG-TONEMAP-BT2390-INVALID-1 fix: when the host's FFmpeg cannot
// run the tone-map chain, playbackInfo must say so up front, with the same
// reason the transcoder uses, instead of handing out an HDR rendition that
// cannot be produced.
func TestFullTranscodeRefusalFailsClosedOnAnUnverifiedToneMapBuild(t *testing.T) {
	withToneMapSupport(t, false)

	hdr := []struct {
		name   string
		signal VideoSignal
	}{
		{"hdr10 pq", VideoSignal{ColorTransfer: "smpte2084"}},
		{"hlg", VideoSignal{ColorTransfer: "arib-std-b67"}},
		{"dolby vision profile 7", VideoSignal{DoViProfile: 7}},
		{"dolby vision profile 8", VideoSignal{DoViProfile: 8, DoViBLCompatID: 6}},
	}
	for _, tc := range hdr {
		if got := tc.signal.FullTranscodeRefusal(); got != ffmpeg.ToneMapRefusalUnverified {
			t.Fatalf("%s refusal = %q, want %q", tc.name, got, ffmpeg.ToneMapRefusalUnverified)
		}
	}
	if got := (VideoSignal{}).FullTranscodeRefusal(); got != "" {
		t.Fatalf("SDR refusal = %q, want none", got)
	}
	// Dolby Vision profile 5 keeps its own, more specific reason: there is no
	// base layer to tone-map at all, verified build or not.
	if got := (VideoSignal{DoViProfile: 5}).FullTranscodeRefusal(); got != ffmpeg.ToneMapRefusalDoViP5 {
		t.Fatalf("Dolby Vision profile 5 refusal = %q, want %q", got, ffmpeg.ToneMapRefusalDoViP5)
	}
}

// TestFullTranscodeRefusalAllowsHDROnAVerifiedBuild is the matching positive
// control: the fail-closed gate must not simply disable HDR everywhere.
func TestFullTranscodeRefusalAllowsHDROnAVerifiedBuild(t *testing.T) {
	withToneMapSupport(t, true)
	for _, signal := range []VideoSignal{
		{ColorTransfer: "smpte2084"},
		{ColorTransfer: "arib-std-b67"},
		{DoViProfile: 8, DoViBLCompatID: 6},
	} {
		if got := signal.FullTranscodeRefusal(); got != "" {
			t.Fatalf("%+v refusal = %q on a verified build, want none", signal, got)
		}
	}
}

// TestPlaybackRefusalReasonsComeFromTheTranscoder pins the two refusal reasons
// to the ffmpeg package's constants, so the reason a client is told and the
// reason the transcoder refuses can never drift apart.
func TestPlaybackRefusalReasonsComeFromTheTranscoder(t *testing.T) {
	if ffmpeg.ToneMapRefusalDoViP5 != "dolby-vision-p5-transcode-unavailable" {
		t.Fatalf("DoVi P5 wire reason changed: %q", ffmpeg.ToneMapRefusalDoViP5)
	}
	if ffmpeg.ToneMapRefusalUnverified != "hdr-tone-map-unverified" {
		t.Fatalf("unverified tone-map wire reason changed: %q", ffmpeg.ToneMapRefusalUnverified)
	}
}

// TestDolbyVision78KeepsTodaysSoftwareToneMapPath is the blast-radius control
// for ADR-0075 Amendment 2026-07-26 D3. The per-transfer-class split introduced
// there governs which HARDWARE backend may be certified for which class, and
// nothing else. Dolby Vision 7 and 8 have no synthesisable lavfi fixture and so
// get no hardware row, but their tone-map path is the SOFTWARE chain over the
// HDR10-compatible PQ base layer (doViBLCompatHDR10) — already covered by the
// PQ software verdict.
//
// An earlier draft of the amendment would have failed DoVi 7/8 closed
// regardless of the software verdict, refusing transcode for every DoVi 7/8
// title that transcodes today. This test is what keeps that regression out: on
// a verified build these must transcode, and DOVI_78_FIXTURE must never cause a
// runtime refusal.
func TestDolbyVision78KeepsTodaysSoftwareToneMapPath(t *testing.T) {
	withToneMapSupport(t, true)

	for _, signal := range []VideoSignal{
		{DoViProfile: 8, DoViBLCompatID: doViBLCompatHDR10, ColorTransfer: "smpte2084"},
		{DoViProfile: 7, DoViBLCompatID: doViBLCompatHDR10, ColorTransfer: "smpte2084"},
		{DoViProfile: 8, DoViBLCompatID: 0},
		{DoViProfile: 7, DoViBLCompatID: 0},
	} {
		if !signal.NeedsToneMap() {
			t.Fatalf("%+v no longer tone-maps; DoVi 7/8 must keep taking the tone-map path", signal)
		}
		if got := signal.FullTranscodeRefusal(); got != "" {
			t.Fatalf("%+v refusal = %q on a build whose SOFTWARE probe passes, want none: the hardware "+
				"fixture gap must gate only the GA claim, never a runtime refusal", signal, got)
		}
		class, classified := ffmpeg.TransferClassFor(signal.ColorTransfer, signal.DoViProfile)
		if !classified || class != ffmpeg.TransferClassDoVi78 {
			t.Fatalf("%+v classified as %q, want %q", signal, class, ffmpeg.TransferClassDoVi78)
		}
		if ffmpeg.HardwareCertifiableClass(class) {
			t.Fatalf("%+v is hardware-certifiable; DoVi 7/8 has no lavfi fixture (DOVI_78_FIXTURE)", signal)
		}
		if got := ffmpeg.SelectToneMapBackend(class, "darwin"); got != "" {
			t.Fatalf("%+v selected hardware backend %q, want the software chain", signal, got)
		}
	}

	// And the software verdict still governs: an unverified build refuses these
	// exactly as it refuses PQ and HLG.
	withToneMapSupport(t, false)
	if got := (VideoSignal{DoViProfile: 8, DoViBLCompatID: doViBLCompatHDR10}).FullTranscodeRefusal(); got != ffmpeg.ToneMapRefusalUnverified {
		t.Fatalf("DoVi 8 refusal on an unverified build = %q, want %q", got, ffmpeg.ToneMapRefusalUnverified)
	}
}

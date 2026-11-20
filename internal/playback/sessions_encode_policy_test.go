package playback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
)

// policyTranscoder applies a Console encode policy the test can change, the
// way ffmpeg.Runner does: H.264 only or hardware off withholds HEVC.
type policyTranscoder struct {
	fakeTranscoder
	policyMu sync.Mutex
	policy   ffmpeg.EncodePolicy
}

func (p *policyTranscoder) setPolicy(policy ffmpeg.EncodePolicy) {
	p.policyMu.Lock()
	defer p.policyMu.Unlock()
	p.policy = policy
}

func (p *policyTranscoder) EncodePolicy() ffmpeg.EncodePolicy {
	p.policyMu.Lock()
	defer p.policyMu.Unlock()
	return p.policy
}

func (p *policyTranscoder) SupportsTranscodeCodec(codec string) bool {
	return p.SupportsTranscodeCodecUnder(p.EncodePolicy(), codec)
}

func (p *policyTranscoder) SupportsTranscodeCodecUnder(policy ffmpeg.EncodePolicy, codec string) bool {
	return codec == "hevc" && !policy.SoftwareOnly && !policy.H264Only
}

// savingTranscoder simulates a Console save landing the moment a session
// reads the policy: every read returns the policy, then flips it.
type savingTranscoder struct {
	policyTranscoder
}

func (p *savingTranscoder) EncodePolicy() ffmpeg.EncodePolicy {
	p.policyMu.Lock()
	defer p.policyMu.Unlock()
	policy := p.policy
	p.policy.SoftwareOnly = !p.policy.SoftwareOnly
	return policy
}

func (p *savingTranscoder) SupportsTranscodeCodec(codec string) bool {
	return p.SupportsTranscodeCodecUnder(p.EncodePolicy(), codec)
}

func hevcSessionRequest(sessions *Sessions) func(SessionKey, string) (string, error) {
	caps := ladderCaps{ClientMaxHeight: 1080, HEVC: true, HEVCMaxHeight: 2160}
	return func(key SessionKey, name string) (string, error) {
		return sessions.FilePathPriorityWithBurnSubtitleAudioLadder(context.Background(), key, "/src/movie.mkv", 120, 3840, 2160, false, false, caps, VideoSignal{}, -1, ffmpeg.AudioProfile{}, PriorityNormal, name)
	}
}

func readMaster(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (p *policyTranscoder) lastRendition(t *testing.T) ffmpeg.Rendition {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.renditions) == 0 {
		t.Fatal("no encode started")
	}
	return p.renditions[len(p.renditions)-1]
}

// TestConsolePolicyChangeKeepsActiveHEVCSession pins Codex's finding on
// CORE-2452: saving H.264 only or hardware off while a viewer plays an HEVC
// rung neither tears that session down nor changes its encoder on a seek;
// sessions started afterwards follow the new policy.
func TestConsolePolicyChangeKeepsActiveHEVCSession(t *testing.T) {
	t.Parallel()
	transcoder := &policyTranscoder{}
	transcoder.segmentsToLay = 1
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	request := hevcSessionRequest(sessions)

	if _, err := request(key, "2160p-hevc/seg00000.m4s"); err != nil {
		t.Fatalf("HEVC segment before the change: %v", err)
	}
	transcoder.setPolicy(ffmpeg.EncodePolicy{SoftwareOnly: true, H264Only: true, Quality: ffmpeg.QualityLow})

	// A far seek on the playing session restarts its encoder.
	if _, err := request(key, "2160p-hevc/seg00020.m4s"); err != nil {
		t.Fatalf("HEVC segment after the Console change: %v", err)
	}
	got := transcoder.lastRendition(t)
	pinned, ok := got.PinnedEncodePolicy()
	if got.VideoCodec != "hevc" || !ok || pinned != (ffmpeg.EncodePolicy{}) {
		t.Fatalf("restarted rendition = %s pinned %+v (%v), want HEVC under the session's original policy", got.VideoCodec, pinned, ok)
	}

	// A session started after the change follows the new policy.
	other := SessionKey{SessionID: "session-other", MediaID: "media-alpha"}
	path, err := request(other, "master.m3u8")
	if err != nil {
		t.Fatalf("master for a new session: %v", err)
	}
	if master := readMaster(t, path); strings.Contains(master, "hevc") {
		t.Fatalf("new session offers HEVC under H.264 only:\n%s", master)
	}
	if _, err := request(other, "1080p/seg00000.m4s"); err != nil {
		t.Fatalf("H.264 segment for the new session: %v", err)
	}
	if pinned, ok := transcoder.lastRendition(t).PinnedEncodePolicy(); !ok || !pinned.SoftwareOnly || pinned.Quality != ffmpeg.QualityLow {
		t.Fatalf("new session pinned %+v (%v), want the new policy", pinned, ok)
	}
}

// TestConsoleHardwareReenableKeepsActiveSoftwareSession pins Codex's second
// finding on CORE-2452: a session that started in software under an
// HEVC-capable grant keeps its H.264 ladder, and its directory, when hardware
// is switched back on; a session started afterwards gets HEVC.
func TestConsoleHardwareReenableKeepsActiveSoftwareSession(t *testing.T) {
	t.Parallel()
	transcoder := &policyTranscoder{policy: ffmpeg.EncodePolicy{SoftwareOnly: true}}
	transcoder.segmentsToLay = 1
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	request := hevcSessionRequest(sessions)
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	first, err := request(key, "1080p/seg00000.m4s")
	if err != nil {
		t.Fatalf("software segment: %v", err)
	}
	transcoder.setPolicy(ffmpeg.EncodePolicy{})
	again, err := request(key, "1080p/seg00020.m4s")
	if err != nil {
		t.Fatalf("software segment after hardware is re-enabled: %v", err)
	}
	if filepath.Dir(filepath.Dir(again)) != filepath.Dir(filepath.Dir(first)) {
		t.Fatalf("session moved from %s to %s: the Console save tore it down", first, again)
	}
	if pinned, ok := transcoder.lastRendition(t).PinnedEncodePolicy(); !ok || !pinned.SoftwareOnly {
		t.Fatalf("restart pinned %+v (%v), want the session's software policy", pinned, ok)
	}

	other := SessionKey{SessionID: "session-other", MediaID: "media-alpha"}
	path, err := request(other, "master.m3u8")
	if err != nil {
		t.Fatalf("master for a new session: %v", err)
	}
	if master := readMaster(t, path); !strings.Contains(master, "2160p-hevc") {
		t.Fatalf("new session lacks HEVC with hardware on:\n%s", master)
	}
}

// TestSessionLadderAndEncoderShareOnePolicySnapshot pins Codex's race finding
// on CORE-2452: a Console save between the ladder decision and the encode
// cannot leave a session with an HEVC rung it encodes in software.
func TestSessionLadderAndEncoderShareOnePolicySnapshot(t *testing.T) {
	t.Parallel()
	transcoder := &savingTranscoder{}
	transcoder.segmentsToLay = 1
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	request := hevcSessionRequest(sessions)
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	path, err := request(key, "master.m3u8")
	if err != nil {
		t.Fatalf("master: %v", err)
	}
	if !strings.Contains(readMaster(t, path), "2160p-hevc") {
		t.Fatal("ladder did not take the hardware policy read at creation")
	}
	if _, err := request(key, "2160p-hevc/seg00000.m4s"); err != nil {
		t.Fatalf("HEVC segment: %v", err)
	}
	if pinned, ok := transcoder.lastRendition(t).PinnedEncodePolicy(); !ok || pinned.SoftwareOnly {
		t.Fatalf("HEVC rung encodes under %+v (%v), want the hardware snapshot its ladder came from", pinned, ok)
	}
}

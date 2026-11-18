package playback

import (
	"context"
	"os"
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
	policy := p.EncodePolicy()
	return codec == "hevc" && !policy.SoftwareOnly && !policy.H264Only
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
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	caps := ladderCaps{ClientMaxHeight: 1080, HEVC: true, HEVCMaxHeight: 2160}
	request := func(key SessionKey, name string) (string, error) {
		return sessions.FilePathPriorityWithBurnSubtitleAudioLadder(context.Background(), key, "/src/movie.mkv", 120, 3840, 2160, false, false, caps, VideoSignal{}, -1, ffmpeg.AudioProfile{}, PriorityNormal, name)
	}

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
	data, err := os.ReadFile(path) // #nosec G304 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hevc") {
		t.Fatalf("new session offers HEVC under H.264 only:\n%s", data)
	}
	if _, err := request(other, "1080p/seg00000.m4s"); err != nil {
		t.Fatalf("H.264 segment for the new session: %v", err)
	}
	if pinned, ok := transcoder.lastRendition(t).PinnedEncodePolicy(); !ok || !pinned.SoftwareOnly || pinned.Quality != ffmpeg.QualityLow {
		t.Fatalf("new session pinned %+v (%v), want the new policy", pinned, ok)
	}
}

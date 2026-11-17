package playback

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// probingTranscoder is a fake transcoder that can also probe, counting probes.
type probingTranscoder struct {
	fakeTranscoder
	probeMu  sync.Mutex
	probes   int
	duration int
}

func (p *probingTranscoder) Probe(context.Context, string) (ffmpeg.Probe, error) {
	p.probeMu.Lock()
	defer p.probeMu.Unlock()
	p.probes++
	return ffmpeg.Probe{DurationSeconds: p.duration}, nil
}

func (p *probingTranscoder) probeCount() int {
	p.probeMu.Lock()
	defer p.probeMu.Unlock()
	return p.probes
}

func TestInfoProbesAMissingRuntimeOnce(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	prober := &probingTranscoder{duration: 7152}
	sessions := NewSessions(prober, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)

	path := filepath.Join(t.TempDir(), "no-runtime.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "No runtime",
		Container:          "mkv",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: "fp-no-runtime",
		VideoWidthHint:     1920,
		VideoHeightHint:    1080,
		Streams:            []store.MediaFileStream{videoStream("h264", "High", 0, ""), audioStream(1, "aac", 2)},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if media.DurationSeconds != 0 {
		t.Fatalf("stored DurationSeconds = %d, want 0 for this fixture", media.DurationSeconds)
	}

	for range 2 {
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: main10_1080p})
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if resp.Timeline.DurationSeconds != 7152 {
			t.Fatalf("Timeline.DurationSeconds = %d, want the probed 7152", resp.Timeline.DurationSeconds)
		}
	}
	if got := prober.probeCount(); got != 1 {
		t.Fatalf("probes = %d, want 1 (cached by content fingerprint)", got)
	}
}

func TestInfoKeepsAStoredRuntimeWithoutProbing(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	prober := &probingTranscoder{duration: 7152}
	sessions := NewSessions(prober, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)
	mediaID := importProbedMedia(t, st, "fp-has-runtime", "mkv", "h264", 1920, 1080, []store.MediaFileStream{
		videoStream("h264", "High", 0, ""), audioStream(1, "aac", 2),
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: main10_1080p})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Timeline.DurationSeconds != 120 || prober.probeCount() != 0 {
		t.Fatalf("Timeline.DurationSeconds = %d, probes = %d; want the stored 120 and no probe", resp.Timeline.DurationSeconds, prober.probeCount())
	}
}

func TestProbedDurationSecondsRemembersAMissForAWhile(t *testing.T) {
	prober := &probingTranscoder{}
	now := time.Unix(1000, 0)
	sessions := NewSessions(prober, t.TempDir(), func() time.Time { return now })
	defer sessions.Close()

	if got := sessions.ProbedDurationSeconds(context.Background(), "fp", "/media/x.mkv"); got != 0 {
		t.Fatalf("first probe = %d, want 0", got)
	}
	_ = sessions.ProbedDurationSeconds(context.Background(), "fp", "/media/x.mkv")
	if prober.probeCount() != 1 {
		t.Fatalf("probes = %d, want 1 within the miss TTL", prober.probeCount())
	}
	now = now.Add(durationProbeMissTTL)
	prober.duration = 60
	if got := sessions.ProbedDurationSeconds(context.Background(), "fp", "/media/x.mkv"); got != 60 || prober.probeCount() != 2 {
		t.Fatalf("after TTL got %d with %d probes, want 60 with 2", got, prober.probeCount())
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	prober.duration = 0
	if got := sessions.ProbedDurationSeconds(cancelled, "fp-other", "/media/y.mkv"); got != 0 {
		t.Fatalf("cancelled probe = %d, want 0", got)
	}
	prober.duration = 30
	if got := sessions.ProbedDurationSeconds(context.Background(), "fp-other", "/media/y.mkv"); got != 30 {
		t.Fatalf("probe after a cancelled request = %d, want 30 (a cancelled miss is not cached)", got)
	}
}

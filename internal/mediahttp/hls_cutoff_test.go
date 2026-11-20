package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

type cutoffTranscoder struct {
	fakeHLSTranscoder
	probe  func() (ffmpeg.Probe, error)
	starts atomic.Int32
}

func (f *cutoffTranscoder) Probe(context.Context, string) (ffmpeg.Probe, error) { return f.probe() }

func (f *cutoffTranscoder) StartHLS(ctx context.Context, src, dir string, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	f.starts.Add(1)
	return f.fakeHLSTranscoder.StartHLS(ctx, src, dir, rendition)
}

func (f *cutoffTranscoder) StartHLSSegments(ctx context.Context, src, dir string, segment, seconds int, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	f.starts.Add(1)
	return f.fakeHLSTranscoder.StartHLSSegments(ctx, src, dir, segment, seconds, rendition)
}

type cutoffCompatResolver func()

func (r cutoffCompatResolver) Resolve(context.Context, string, streamtoken.DecisionWant) (string, bool, error) {
	r()
	return "", false, nil
}

func TestT3HLSCutoffDuringVerificationOrDurationProbe(t *testing.T) {
	for _, stage := range []string{"verification", "duration"} {
		t.Run(stage, func(t *testing.T) {
			h, tokens, scope, now := castTrustBoundaryFixture(t)
			src := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(src, []byte("source data"), 0o600); err != nil {
				t.Fatal(err)
			}
			media, _, err := h.store.ImportMediaFile(t.Context(), store.ImportMediaFile{
				LibraryID: "library-local", Path: src, Title: "source", Container: "mkv",
				Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: "cutoff-source",
				VideoWidthHint: 1280, VideoHeightHint: 720,
			})
			if err != nil || media.DurationSeconds != 0 {
				t.Fatalf("fixture media = %+v, error = %v", media, err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			latch := func() { close(entered); <-release }
			encoder := &cutoffTranscoder{probe: func() (ffmpeg.Probe, error) {
				if stage == "duration" {
					latch()
				}
				return ffmpeg.Probe{DurationSeconds: 120}, nil
			}}
			h.hls = sessiontest.New(t, encoder, t.TempDir(), nil)
			token, err := tokens.Issue(t.Context(), streamtoken.Grant{
				Target: streamtoken.TargetMediaHLS, MediaID: media.ID,
				DeviceID: scope.DeviceID, SessionID: scope.SessionID, ProfileID: scope.ProfileID,
				ExpiresAt: now.Add(time.Hour),
			})
			if err != nil {
				t.Fatal(err)
			}
			if stage == "verification" {
				h.SetCompatDecisionResolver(cutoffCompatResolver(latch))
			}
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() { result <- fetchCastHLS(h, token, "720p/seg00000.m4s") }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("HLS did not reach the latch")
			}
			wantRevoked := 0
			if stage == "duration" {
				wantRevoked = 1
			}
			if got := h.hls.TerminateSession(scope.SessionID); got != wantRevoked {
				t.Fatalf("terminated %d instances, want %d", got, wantRevoked)
			}
			unblock()
			select {
			case rec := <-result:
				if rec.Code != http.StatusGone || encoder.starts.Load() != 0 {
					t.Fatalf("status = %d, encoder starts = %d, body = %q", rec.Code, encoder.starts.Load(), rec.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("HLS did not return after cutoff")
			}
		})
	}
}

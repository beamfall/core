package mediahttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestLiveHLSEncryptedReopenUsesResolutionStatus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{playback.ErrTranscodeCapacity, http.StatusServiceUnavailable},
		{context.DeadlineExceeded, http.StatusServiceUnavailable},
		{playback.ErrArtifactChanged, http.StatusServiceUnavailable},
		{playback.ErrTranscodeFailed, http.StatusInternalServerError},
		{playback.ErrSessionEvicted, http.StatusGone},
		{playback.ErrNotFound, http.StatusNotFound},
		{&os.PathError{Op: "fork/exec", Path: "ffmpeg", Err: os.ErrNotExist}, http.StatusServiceUnavailable},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/hls/720p/seg00000.m4s", nil)
			resolved, reopened := httptest.NewRecorder(), httptest.NewRecorder()
			err := fmt.Errorf("live artifact: %w", tc.err)
			writeLiveHLSError(resolved, r, err)
			serveLiveArtifact(reopened, r, "720p/seg00000.m4s", func() (*encderiv.LiveFile, error) { return nil, err })
			if resolved.Code != tc.code || reopened.Code != tc.code || resolved.Body.String() != reopened.Body.String() {
				t.Fatalf("resolution = %d %q, encrypted reopen = %d %q, want matching %d responses",
					resolved.Code, resolved.Body.String(), reopened.Code, reopened.Body.String(), tc.code)
			}
		})
	}
}

type cutoffTranscoder struct {
	fakeHLSTranscoder
	probe  func(context.Context, string) (ffmpeg.Probe, error)
	starts atomic.Int32
}

func (f *cutoffTranscoder) Probe(ctx context.Context, path string) (ffmpeg.Probe, error) {
	return f.probe(ctx, path)
}

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
	for _, stage := range []string{"verification", "duration", "duration permit"} {
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
			var runner *ffmpeg.Runner
			var marker string
			if stage == "duration permit" {
				dir := filepath.Dir(src)
				marker = filepath.Join(dir, "ffprobe-started")
				t.Setenv("BEAMFALL_T3_PROBE_MARKER", marker)
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				const probe = `#!/bin/sh
: > "$BEAMFALL_T3_PROBE_MARKER"
printf '%s\n' '{"format":{"duration":"120"}}'
`
				if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(probe), 0o700); err != nil { // #nosec G306 -- private executable test fixture.
					t.Fatal(err)
				}
				runner = ffmpeg.New()
				if result, err := runner.Probe(t.Context(), src); err != nil || result.DurationSeconds != 120 {
					t.Fatalf("FFprobe fixture duration = %d, %v", result.DurationSeconds, err)
				}
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
			}
			encoder := &cutoffTranscoder{probe: func(ctx context.Context, path string) (ffmpeg.Probe, error) {
				if runner != nil {
					latch()
					// Preserve the binding's permit while bypassing the supervisor's
					// early context check so the permit itself must deny the spawn.
					return runner.Probe(context.WithoutCancel(ctx), path)
				}
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
			if stage != "verification" {
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
			if marker != "" {
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("cut-off HLS permit let FFprobe start: %v", err)
				}
			}
		})
	}
}

package mediahttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
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
			serveLiveArtifact(reopened, r, "720p/seg00000.m4s", func() (liveArtifactFile, error) { return nil, err })
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

func TestHLSCutoffDuringVerificationOrDurationProbe(t *testing.T) {
	for _, stage := range []string{"verification", "duration", "duration permit", "library drain across scope lookup"} {
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
			latch := sync.OnceFunc(func() { close(entered); <-release })
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
			var probes atomic.Int32
			encoder := &cutoffTranscoder{probe: func(ctx context.Context, path string) (ffmpeg.Probe, error) {
				probes.Add(1)
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
			switch stage {
			case "verification":
				h.SetCompatDecisionResolver(cutoffCompatResolver(latch))
			case "library drain across scope lookup":
				// The admin-window check reads the injected clock inside Scope.
				if err := h.store.SetSessionAdminUntil(t.Context(), scope.SessionID, now.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				h.auth = auth.NewServiceWithClock(h.store, func() time.Time {
					latch()
					return now
				})
			}
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() { result <- fetchCastHLS(h, token, "720p/seg00000.m4s") }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("HLS did not reach the latch")
			}
			if stage == "library drain across scope lookup" {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				fence, err := h.hls.Drain(ctx, media.LibraryID, 0)
				if err != nil {
					t.Fatalf("verification-only binding held drain: %v", err)
				}
				if err := h.hls.Reopen(media.LibraryID, fence); err != nil {
					t.Fatal(err)
				}
			} else {
				wantRevoked := 0
				if stage != "verification" {
					wantRevoked = 1
				}
				if got := h.hls.TerminateSession(scope.SessionID); got != wantRevoked {
					t.Fatalf("terminated %d instances, want %d", got, wantRevoked)
				}
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
			if stage == "library drain across scope lookup" {
				if probes.Load() != 0 {
					t.Fatalf("stale scope lookup started %d probes", probes.Load())
				}
				rec := fetchCastHLS(h, token, "master.m3u8")
				if rec.Code != http.StatusOK || probes.Load() != 1 {
					t.Fatalf("fresh request status = %d, probes = %d, body = %q", rec.Code, probes.Load(), rec.Body.String())
				}
			}
			if marker != "" {
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("cut-off HLS permit let FFprobe start: %v", err)
				}
			}
		})
	}
}

type blockingPlainSegmentWriter struct {
	header      http.Header
	status      int
	buf         bytes.Buffer
	firstWrite  sync.Once
	wroteFirst  chan struct{}
	releaseRead chan struct{}
}

func (w *blockingPlainSegmentWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *blockingPlainSegmentWriter) WriteHeader(code int) { w.status = code }

func (w *blockingPlainSegmentWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	w.firstWrite.Do(func() {
		close(w.wroteFirst)
		<-w.releaseRead
	})
	return n, err
}

type largePlainSegmentTranscoder struct {
	fakeHLSTranscoder
	payload []byte
	starts  *atomic.Int32
}

func (f largePlainSegmentTranscoder) StartHLSSegments(ctx context.Context, _ string, dir string, startSegment, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	if f.starts != nil {
		f.starts.Add(1)
	}
	if err := os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("init"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("seg%05d.m4s", startSegment)), f.payload, 0o600); err != nil {
		return nil, err
	}
	if f.holdChild {
		return ffmpeg.StartHLSCommand(exec.CommandContext(ctx, "sleep", "60"))
	}
	return &ffmpeg.HLSProcess{}, nil
}

func TestHLSLivePlainSegmentReadFailsAfterSessionRevocation(t *testing.T) {
	// 128 KiB exceeds io.Copy's 32 KiB buffer so ServeContent must perform
	// multiple Reads on the open segment fd.
	payload := bytes.Repeat([]byte("plain-segment-byte-"), 7000)
	h, token := newHLSSealedTestHandlerWith(t, largePlainSegmentTranscoder{payload: payload})
	scope, err := h.auth.Scope(t.Context(), "session-default")
	if err != nil {
		t.Fatal(err)
	}

	writer := &blockingPlainSegmentWriter{
		wroteFirst:  make(chan struct{}),
		releaseRead: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.HLS(writer, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/720p/seg00000.m4s", nil))
	}()

	select {
	case <-writer.wroteFirst:
	case <-time.After(5 * time.Second):
		t.Fatal("HLS never started writing segment")
	}

	if stopped := h.hls.TerminateSession(scope.SessionID); stopped != 1 {
		t.Fatalf("TerminateSession stopped %d, want 1", stopped)
	}
	close(writer.releaseRead)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HLS did not stop after TerminateSession")
	}

	if writer.status != http.StatusOK && writer.status != 0 {
		t.Fatalf("unexpected status %d", writer.status)
	}
	if writer.buf.Len() == 0 || writer.buf.Len() >= len(payload) {
		t.Fatalf("wrote %d of %d bytes, want partial read stopped by revocation", writer.buf.Len(), len(payload))
	}

	// Direct check: once cutoff is closed, the next Read on livePlainReadSeeker fails.
	closedCutoff := make(chan struct{})
	close(closedCutoff)
	rs := livePlainReadSeeker{
		ctx:        context.Background(),
		cutoff:     closedCutoff,
		ReadSeeker: bytes.NewReader(payload),
	}
	var buf [16]byte
	if n, err := rs.Read(buf[:]); err == nil || n != 0 {
		t.Fatalf("Read after cutoff = (%d, %v), want (0, error)", n, err)
	}
	if _, err := io.ReadAll(rs); err == nil {
		t.Fatal("ReadAll after cutoff succeeded, want error")
	}
}

func TestHLSSharedLeaderOpenResponseStopsAfterSessionRevocationWhileFollowerContinues(t *testing.T) {
	payload := bytes.Repeat([]byte("shared-leader-byte-"), 7000)
	var starts atomic.Int32
	h, leaderToken := newHLSSealedTestHandlerWith(t, largePlainSegmentTranscoder{
		fakeHLSTranscoder: fakeHLSTranscoder{holdChild: true},
		payload:           payload,
		starts:            &starts,
	})
	leaderScope, err := h.auth.Scope(t.Context(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.EnrollDevice(
		t.Context(),
		store.Device{
			ID:         "device-follower",
			Name:       "Follower",
			Role:       store.DeviceRoleViewer,
			SecretHash: store.HashDeviceSecret("follower-secret"),
		},
		store.Session{
			ID:        "session-follower",
			DeviceID:  "device-follower",
			ProfileID: leaderScope.ProfileID,
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	followerToken, err := h.tokens.Issue(t.Context(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaHLS,
		MediaID:   "media-alpha",
		DeviceID:  "device-follower",
		SessionID: "session-follower",
		ProfileID: leaderScope.ProfileID,
		ExpiresAt: time.Unix(100, 0).UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	leaderWriter := &blockingPlainSegmentWriter{
		wroteFirst:  make(chan struct{}),
		releaseRead: make(chan struct{}),
	}
	var releaseLeaderOnce sync.Once
	releaseLeader := func() { releaseLeaderOnce.Do(func() { close(leaderWriter.releaseRead) }) }
	defer releaseLeader()
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		h.HLS(leaderWriter, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+leaderToken+"/720p/seg00000.m4s", nil))
	}()

	select {
	case <-leaderWriter.wroteFirst:
	case <-time.After(15 * time.Second):
		t.Fatal("shared leader never started writing segment")
	}

	followerWriter := &blockingPlainSegmentWriter{
		wroteFirst:  make(chan struct{}),
		releaseRead: make(chan struct{}),
	}
	var releaseFollowerOnce sync.Once
	releaseFollower := func() { releaseFollowerOnce.Do(func() { close(followerWriter.releaseRead) }) }
	defer releaseFollower()
	followerDone := make(chan struct{})
	go func() {
		defer close(followerDone)
		h.HLS(followerWriter, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+followerToken+"/720p/seg00000.m4s", nil))
	}()

	select {
	case <-followerWriter.wroteFirst:
	case <-time.After(15 * time.Second):
		t.Fatal("shared follower never started writing segment")
	}

	if starts.Load() != 1 {
		t.Fatalf("encoder starts = %d, want 1 shared encode", starts.Load())
	}

	// Revoking the shared leader promotes the follower (keeping scratch open)
	// while still closing the leader's session cutoff immediately.
	if stopped := h.hls.TerminateSession(leaderScope.SessionID); stopped != 1 {
		t.Fatalf("TerminateSession(leader) = %d, want 1", stopped)
	}
	releaseLeader()

	select {
	case <-leaderDone:
	case <-time.After(15 * time.Second):
		t.Fatal("revoked shared leader response did not stop after TerminateSession")
	}
	if leaderWriter.buf.Len() == 0 || leaderWriter.buf.Len() >= len(payload) {
		t.Fatalf("leader wrote %d of %d bytes, want partial read stopped by revocation", leaderWriter.buf.Len(), len(payload))
	}

	releaseFollower()
	select {
	case <-followerDone:
	case <-time.After(15 * time.Second):
		t.Fatal("surviving follower response did not finish")
	}
	if followerWriter.buf.Len() != len(payload) {
		t.Fatalf("follower wrote %d bytes, want full %d bytes", followerWriter.buf.Len(), len(payload))
	}

	// Subsequent request from the promoted follower still serves from the shared encode without restarting.
	rec := httptest.NewRecorder()
	h.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+followerToken+"/720p/seg00000.m4s", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != len(payload) || starts.Load() != 1 {
		t.Fatalf("post-promotion follower status=%d len=%d starts=%d, want 200/%d/1", rec.Code, rec.Body.Len(), starts.Load(), len(payload))
	}
}

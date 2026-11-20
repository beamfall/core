//go:build unix

package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

type profileSwitchEncoder struct {
	child   *ffmpeg.HLSProcess
	entered chan struct{}
	release <-chan struct{}
}

func (e *profileSwitchEncoder) Write(p []byte) (int, error) {
	close(e.entered)
	<-e.release
	return len(p), nil
}

func (e *profileSwitchEncoder) StartHLS(ctx context.Context, _, dir string, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	if err := os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("init"), 0o600); err != nil {
		return nil, err
	}
	if e.child != nil {
		return &ffmpeg.HLSProcess{}, nil
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", "printf ready; exec sleep 60")
	cmd.Stdout = e
	child, err := ffmpeg.StartHLSCommand(cmd)
	e.child = child
	return child, err
}

func (e *profileSwitchEncoder) StartHLSSegments(ctx context.Context, src, dir string, _, _ int, rung ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	return e.StartHLS(ctx, src, dir, rung)
}

func TestSwitchProfileFencesLivePlaybackAndKeepsTheOldProfileCharge(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	src := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: src, Title: "Profile switch fixture", Container: "mp4",
		Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: "profile-switch",
		DurationSecondsHint: 120, VideoWidthHint: 1280, VideoHeightHint: 720,
	})
	if err != nil {
		t.Fatal(err)
	}
	const oldProfile, newProfile = "profile-default", "profile-managed"
	limit := 1
	for _, profile := range []store.ProfileID{oldProfile, newProfile} {
		if _, err := st.UpdateProfileSettings(ctx, profile, store.ProfileSettings{MaxConcurrentStreams: &limit}); err != nil {
			t.Fatal(err)
		}
	}
	release := make(chan struct{})
	encoder := &profileSwitchEncoder{entered: make(chan struct{}), release: release}
	unblock := func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	defer unblock()
	sessions := sessiontest.New(t, encoder, t.TempDir(), nil)
	sessions.SetTranscodeCap(2)
	h.SetTranscodeTerminator(sessions)
	tokens := streamtoken.NewService([]byte("profile-switch-test-key"), nil)
	mediaHandler := mediahttp.NewHandler(st, h.auth, tokens, nil, sessions)
	scopeFor := func(id store.SessionID) auth.Scope {
		t.Helper()
		scope, err := h.auth.Scope(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return scope
	}
	grant := func(scope auth.Scope) string {
		t.Helper()
		token, err := tokens.Issue(ctx, streamtoken.Grant{
			Target: streamtoken.TargetMediaHLS, MediaID: media.ID,
			SessionID: scope.SessionID, ProfileID: scope.ProfileID, DeviceID: scope.DeviceID,
			ExpiresAt: time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	fetch := func(token string, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		mediaHandler.HLS(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/media/hls/"+token+"/720p/init.mp4", nil))
		if rec.Code != want {
			t.Fatalf("HLS status = %d, want %d (%q)", rec.Code, want, rec.Body.String())
		}
	}
	old := scopeFor("session-default")
	oldGrant := grant(old)
	fetch(oldGrant, http.StatusOK)
	select {
	case <-encoder.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("old encoder did not enter its output latch")
	}

	switched := make(chan error, 1)
	go func() {
		_, err := h.switchProfile(ctx, old, request{Variables: map[string]any{"profileID": newProfile}})
		switched <- err
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !encoder.child.StopRequested() {
		select {
		case err := <-switched:
			t.Fatalf("profile switch returned before fencing its child: %v", err)
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("old encoder was not stopped by profile switch")
		}
	}
	newScope := scopeFor(old.SessionID)
	if newScope.ProfileID != newProfile || newScope.SessionID != old.SessionID {
		t.Fatalf("unexpected scope after switch: profile %s, session %s", newScope.ProfileID, newScope.SessionID)
	}
	other := grant(scopeFor("session-revealed"))
	fetch(other, http.StatusServiceUnavailable)
	fetch(grant(newScope), http.StatusOK)
	fetch(oldGrant, http.StatusUnauthorized)
	unblock()
	select {
	case err := <-switched:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("profile switch did not finish after reap")
	}
	fetch(other, http.StatusOK)
}

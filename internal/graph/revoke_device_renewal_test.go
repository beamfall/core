//go:build unix

package graph

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestRevokeDeviceAbortsTranscodeAfterSessionRenewal(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	src := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: src, Title: "Revoke renewal fixture", Container: "mp4",
		Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: "revoke-renewal",
		DurationSecondsHint: 120, VideoWidthHint: 1280, VideoHeightHint: 720,
	})
	if err != nil {
		t.Fatal(err)
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
	h.SetTranscodeTerminator(sessions)
	tokens := streamtoken.NewService([]byte("revoke-renewal-test-key"), nil)
	mediaHandler := mediahttp.NewHandler(st, h.auth, tokens, nil, sessions)

	oldScope, err := h.auth.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := tokens.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaHLS,
		MediaID:   media.ID,
		SessionID: oldScope.SessionID,
		ProfileID: oldScope.ProfileID,
		DeviceID:  oldScope.DeviceID,
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	mediaHandler.HLS(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/media/hls/"+grant+"/720p/init.mp4", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HLS status = %d, want 200 (%q)", rec.Code, rec.Body.String())
	}
	select {
	case <-encoder.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("encoder did not enter its output latch")
	}

	// Renewing the device session deletes session-default and its stream_token_ledger rows,
	// leaving the running HLS transcode attached only by device ID.
	if _, err := st.RenewDeviceSession(ctx, oldScope.DeviceID, "session-renewed", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("RenewDeviceSession: %v", err)
	}

	adminScope, err := h.auth.Scope(ctx, "session-manager")
	if err != nil {
		t.Fatal(err)
	}
	revoked := make(chan RevokeDeviceResult, 1)
	revokeErr := make(chan error, 1)
	go func() {
		res, err := h.revokeDevice(ctx, adminScope, request{Variables: map[string]any{"deviceID": string(oldScope.DeviceID)}})
		if err != nil {
			revokeErr <- err
			return
		}
		revoked <- res
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !encoder.child.StopRequested() {
		select {
		case err := <-revokeErr:
			t.Fatalf("revokeDevice returned error before stopping encoder: %v", err)
		case res := <-revoked:
			t.Fatalf("revokeDevice returned %+v before stopping encoder", res)
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("encoder was not stopped by revokeDevice after session renewal")
		}
	}

	unblock()
	select {
	case err := <-revokeErr:
		t.Fatalf("revokeDevice: %v", err)
	case res := <-revoked:
		if res.DeviceID != string(oldScope.DeviceID) || res.TranscodesAborted != 1 {
			t.Fatalf("revokeDevice = %+v, want DeviceID=%s TranscodesAborted=1", res, oldScope.DeviceID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revokeDevice did not finish after encoder exited")
	}
}

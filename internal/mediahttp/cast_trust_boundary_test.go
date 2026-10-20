package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func castTrustBoundaryFixture(t *testing.T) (*Handler, *streamtoken.Service, auth.Scope, time.Time) {
	t.Helper()
	st := storetest.Open(t)
	src := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(src, []byte("source plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), "media-alpha", src); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	sessions := playback.NewSessions(fakeHLSTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessions)
	return handler, tokens, scope, now
}

func castHLSGrant(scope auth.Scope, expiresAt time.Time, capability string) streamtoken.Grant {
	return streamtoken.Grant{
		Target:                  streamtoken.TargetMediaHLS,
		MediaID:                 "media-alpha",
		DeviceID:                scope.DeviceID,
		SessionID:               scope.SessionID,
		ProfileID:               scope.ProfileID,
		ProfileCapability:       capability,
		ReceiverBound:           true,
		ReceiverProfileResolved: true,
		ExpiresAt:               expiresAt,
	}
}

func fetchCastHLS(handler *Handler, token, name string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/"+name, nil))
	return rec
}

func assertCastRefusalEmitsNoBytes(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	body := rec.Body.String()
	if strings.Contains(body, "source plaintext") || strings.Contains(body, "m4s") {
		t.Fatalf("refusal emitted media bytes: %q", body)
	}
}

func TestHLSCastRouteChangeCreatesNewReceiverSession(t *testing.T) {
	handler, tokens, scope, now := castTrustBoundaryFixture(t)
	senderCapability := playback.CapabilityFingerprint(playback.CapabilityProfile{
		Client:       "beamfall-ios",
		Containers:   []string{"mp4"},
		DirectCodecs: []string{"h264", "hevc"},
	})
	receiverCapability := playback.CapabilityFingerprint(playback.CapabilityProfile{
		Client:       "chromecast-gen2",
		Containers:   []string{"mp4"},
		DirectCodecs: []string{"h264"},
	})
	for _, capability := range []string{senderCapability, receiverCapability} {
		token, err := tokens.Issue(context.Background(), castHLSGrant(scope, now.Add(time.Hour), capability))
		if err != nil {
			t.Fatal(err)
		}
		rec := fetchCastHLS(handler, token, "720p/seg00000.m4s")
		if rec.Code != http.StatusOK {
			t.Fatalf("capability %q status = %d, want 200: %s", capability, rec.Code, rec.Body.String())
		}
	}

	active := map[string]bool{}
	for _, key := range handler.hls.ActiveKeys() {
		if key.SessionID == scope.SessionID && key.MediaID == "media-alpha" {
			active[key.ProfileCapability] = true
		}
	}
	if !active[senderCapability] || !active[receiverCapability] {
		t.Fatalf("active capability sessions = %v, want sender and receiver", active)
	}
}

// The Direct byte route serves the same receiver-bound grants as HLS. It carried
// no receiver check while the guard was duplicated per route, so an unverified
// receiver could take delivery of plaintext bytes by asking for Direct instead.
func TestDirectCastUnresolvedReceiverProfileFailsClosed(t *testing.T) {
	handler, tokens, scope, now := castTrustBoundaryFixture(t)
	grant := castHLSGrant(scope, now.Add(time.Hour), playback.CapabilityFingerprint(playback.CapabilityProfile{
		Client: "chromecast-unknown",
	}))
	grant.Target = streamtoken.TargetMediaDirect
	grant.ReceiverProfileResolved = false
	token, err := tokens.Issue(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for unresolved receiver profile on the Direct route", rec.Code)
	}
	assertCastRefusalEmitsNoBytes(t, rec)
}

func TestHLSCastMissingReceiverProfileFailsClosed(t *testing.T) {
	handler, tokens, scope, now := castTrustBoundaryFixture(t)
	token, err := tokens.Issue(context.Background(), castHLSGrant(scope, now.Add(time.Hour), ""))
	if err != nil {
		t.Fatal(err)
	}
	rec := fetchCastHLS(handler, token, "720p/seg00000.m4s")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for missing receiver profile", rec.Code)
	}
	assertCastRefusalEmitsNoBytes(t, rec)
	if len(handler.hls.ActiveKeys()) != 0 {
		t.Fatal("missing receiver profile created a session")
	}
}

func TestHLSCastUnresolvedReceiverProfileFailsClosed(t *testing.T) {
	handler, tokens, scope, now := castTrustBoundaryFixture(t)
	grant := castHLSGrant(scope, now.Add(time.Hour), playback.CapabilityFingerprint(playback.CapabilityProfile{
		Client: "chromecast-unknown",
	}))
	grant.ReceiverProfileResolved = false
	token, err := tokens.Issue(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	rec := fetchCastHLS(handler, token, "720p/seg00000.m4s")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for unresolved receiver profile", rec.Code)
	}
	assertCastRefusalEmitsNoBytes(t, rec)
	if len(handler.hls.ActiveKeys()) != 0 {
		t.Fatal("unresolved receiver profile created a session")
	}
}

func TestHLSCastReceiverProfileRejectsUnauthorisedTrack(t *testing.T) {
	handler, tokens, scope, now := castTrustBoundaryFixture(t)
	capability := playback.CapabilityFingerprint(playback.CapabilityProfile{
		Client:       "chromecast-gen2",
		Containers:   []string{"mp4"},
		DirectCodecs: []string{"h264"},
	})
	grant := castHLSGrant(scope, now.Add(time.Hour), capability)
	grant.HLSClientMaxHeight = 480
	token, err := tokens.Issue(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	rec := fetchCastHLS(handler, token, "720p/seg00000.m4s")
	if rec.Code == http.StatusOK {
		t.Fatalf("unauthorised 720p track served bytes: %q", rec.Body.String())
	}
	assertCastRefusalEmitsNoBytes(t, rec)
}

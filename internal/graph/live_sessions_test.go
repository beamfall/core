package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// recordActive seeds one active playback by writing the Stream Token ledger row
// the issue path would write — the live-sessions dashboard reads exactly this
// ledger, so this is the real signal, not a stub. target is "media-direct" or
// "media-hls"; expiry is in the future so the row counts as live.
func recordActive(t *testing.T, st *store.Store, session store.SessionID, profile store.ProfileID, device store.DeviceID, target string, media store.MediaID, restricted bool) {
	t.Helper()
	if err := st.RecordStreamToken(t.Context(), session, profile, device, target, media, restricted, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("RecordStreamToken: %v", err)
	}
}

// TestLiveSessionsDashboard is the live-sessions vertical slice:
// an Admin-Session-gated Console read of currently-active playback over the
// existing Stream Token ledger, reporting Profile/Device/Media +
// direct-play-vs-transcode + resume position, plus the kill-session mutation —
// with the operator's own Reveal contract applied to media titles.
//
// feature:live-sessions
func TestLiveSessionsDashboard(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// session-default (viewer, device-tv, profile-default) is directly playing
	// media-alpha; an HLS token for the same media means it is transcoding.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false)
	// session-managed (viewer) is direct-playing media-alpha — no HLS token, so
	// it must read as NOT transcoding. Record a resume position for it.
	recordActive(t, st, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false)
	if err := st.UpsertPlaybackProgress(t.Context(),
		store.PlaybackProgress{ProfileID: "profile-managed", MediaID: "media-alpha", PositionSeconds: 42, DurationSeconds: 120},
		false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatalf("UpsertPlaybackProgress: %v", err)
	}

	// Admin-Session gate: a manager session without an open Admin Session is
	// refused the dashboard.
	_, errs := post(t, handler, `{"query":"query { liveSessions { sessionID } }"}`, authorizeManager)
	if len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("non-admin liveSessions errors = %#v, want admin-session gate", errs)
	}
	openManagerAdminSession(t, st)

	// Manager read: both active sessions appear with the right shape.
	data, errs := post(t, handler,
		`{"query":"query { liveSessions { sessionID deviceID deviceName deviceRole profileID profileName mediaID title transcoding positionSeconds durationSeconds } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("manager liveSessions errors = %#v", errs)
	}
	var got []LiveSessionInfo
	if err := json.Unmarshal(data["liveSessions"], &got); err != nil {
		t.Fatalf("decode liveSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("liveSessions len = %d, want 2: %#v", len(got), got)
	}
	bySession := map[string]LiveSessionInfo{}
	for _, s := range got {
		bySession[s.SessionID] = s
	}

	def, ok := bySession["session-default"]
	if !ok {
		t.Fatalf("session-default not listed: %#v", got)
	}
	if !def.Transcoding {
		t.Errorf("session-default transcoding = false, want true (HLS token present)")
	}
	if def.MediaID != "media-alpha" || def.Title != "Sample Media" {
		t.Errorf("session-default media = %q/%q, want media-alpha/Sample Media", def.MediaID, def.Title)
	}
	if def.DeviceName != "TV" || def.DeviceRole != "viewer" {
		t.Errorf("session-default device = %q/%q, want TV/viewer", def.DeviceName, def.DeviceRole)
	}
	if def.ProfileName != "Default" {
		t.Errorf("session-default profileName = %q, want Default", def.ProfileName)
	}

	managed, ok := bySession["session-managed"]
	if !ok {
		t.Fatalf("session-managed not listed: %#v", got)
	}
	if managed.Transcoding {
		t.Errorf("session-managed transcoding = true, want false (direct only)")
	}
	if managed.PositionSeconds != 42 {
		t.Errorf("session-managed position = %d, want 42", managed.PositionSeconds)
	}

	// Kill session: terminate session-managed, then it disappears from the
	// dashboard and its auth credential stops working.
	data, errs = post(t, handler,
		`{"query":"mutation T($sessionID: String!){ terminateSession(sessionID:$sessionID){ sessionID } }","variables":{"sessionID":"session-managed"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("terminateSession errors = %#v", errs)
	}
	var killed TerminateSessionResult
	if err := json.Unmarshal(data["terminateSession"], &killed); err != nil {
		t.Fatalf("decode terminateSession: %v", err)
	}
	if killed.SessionID != "session-managed" {
		t.Fatalf("terminated = %q, want session-managed", killed.SessionID)
	}

	// The terminated session is gone from the dashboard at once.
	data, errs = post(t, handler, `{"query":"query { liveSessions { sessionID } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("post-kill liveSessions errors = %#v", errs)
	}
	if err := json.Unmarshal(data["liveSessions"], &got); err != nil {
		t.Fatalf("decode liveSessions: %v", err)
	}
	for _, s := range got {
		if s.SessionID == "session-managed" {
			t.Fatalf("session-managed still listed after terminate: %#v", got)
		}
	}

	// The terminated session's credential no longer authenticates — auth
	// re-resolves the (now-deleted) session and rejects it (HTTP 401).
	killedReq := httptest.NewRequest(http.MethodPost, "/api/graphql",
		bytes.NewReader([]byte(`{"query":"query { currentSession { profileID } }"}`)))
	killedReq.Header.Set("X-Beamfall-Session", "session-managed")
	killedReq.Header.Set("X-Beamfall-Device", "device-tv")
	killedReq.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	killedRec := httptest.NewRecorder()
	handler.ServeHTTP(killedRec, killedReq)
	if killedRec.Code != http.StatusUnauthorized {
		t.Fatalf("terminated session status = %d, want 401 (no longer authenticates)", killedRec.Code)
	}

	// Terminating an unknown session is a client error, not a false success.
	_, errs = post(t, handler,
		`{"query":"mutation T($sessionID: String!){ terminateSession(sessionID:$sessionID){ sessionID } }","variables":{"sessionID":"nope"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "session not found") {
		t.Fatalf("terminate unknown errors = %#v, want session not found", errs)
	}
}

// TestLiveSessionsRevealFiltersMedia confirms the dashboard applies the
// operator's OWN Reveal contract to media titles (ADR-0004/0007): a session
// playing restricted-Library Media is still LISTED (active load is never
// understated) but its identifying fields are suppressed when the operator has
// not revealed that Library — the same hidden==shape rule the stream-token
// ledger follows.
//
// feature:live-sessions
func TestLiveSessionsRevealFiltersMedia(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// Admin Session authorizes the Console read; it does NOT imply Reveal, so the
	// operator stays unrevealed and the restricted-Library media is suppressed.
	openManagerAdminSession(t, st)

	// A viewer session is playing media-archive, which lives in the restricted
	// "Archive" Library. session-manager (device-mac) is unrevealed by default.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-archive", true)

	data, errs := post(t, handler,
		`{"query":"query { liveSessions { sessionID mediaID title libraryID } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("liveSessions errors = %#v", errs)
	}
	var got []LiveSessionInfo
	if err := json.Unmarshal(data["liveSessions"], &got); err != nil {
		t.Fatalf("decode liveSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("liveSessions len = %d, want 1 (listed, not dropped): %#v", len(got), got)
	}
	s := got[0]
	if s.SessionID != "session-default" || s.MediaID != "media-archive" {
		t.Fatalf("listed = %q/%q, want session-default/media-archive", s.SessionID, s.MediaID)
	}
	// Media is hidden from the unrevealed operator: identifying fields suppressed.
	if s.Title != "" || s.LibraryID != "" {
		t.Errorf("hidden media leaked title/library: %q/%q", s.Title, s.LibraryID)
	}
}

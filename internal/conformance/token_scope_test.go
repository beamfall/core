package conformance

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

const tokenScopeGoldenPath = "testdata/token-scope.golden.json"

type tokenScopeCase struct {
	Name           string `json:"name"`
	Target         string `json:"target"`
	MediaID        string `json:"mediaID"`
	Scope          string `json:"scope"`
	RouteExercised bool   `json:"routeExercised"`
	HTTPStatus     int    `json:"httpStatus,omitempty"`
	VerifyReason   string `json:"verifyReason"`
	SessionID      string `json:"sessionID,omitempty"`
}

type tokenScopeFixture struct {
	SchemaVersion        int                  `json:"schemaVersion"`
	TokenSource          string               `json:"tokenSource"`
	RevealMutation       string               `json:"revealMutation"`
	RouteTemplate        string               `json:"routeTemplate"`
	PlaybackRequest      playback.InfoRequest `json:"playbackRequest"`
	AuthorizationHeaders map[string]string    `json:"authorizationHeaders"`
	ExpectedBytesBase64  string               `json:"expectedBytesBase64"`
	Cases                []tokenScopeCase     `json:"cases"`
}

const (
	tokenScopeMediaID  = "media-archive"
	tokenScopeBytes    = "beamfall-token-scope-sentinel"
	revealedSessionID  = "session-revealed"
	lockedSessionID    = "session-default"
	tokenScopeTokenKey = "12345678901234567890123456789012"
)

// TestTokenScopeFixtureDrivesRealCoreCheck proves the client fixture against
// both the production token verifier and the real direct-byte HTTP handler.
func TestTokenScopeFixtureDrivesRealCoreCheck(t *testing.T) {
	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		fixture := newTokenScopeFixture()
		assertTokenScopeFixture(t, fixture)
		writeTokenScopeFixture(t, fixture)
		return
	}
	assertTokenScopeFixture(t, readTokenScopeFixture(t))
}

func assertTokenScopeFixture(t *testing.T, fixture tokenScopeFixture) {
	t.Helper()
	if fixture.SchemaVersion != 1 {
		t.Fatalf("token-scope fixture schemaVersion = %d, want 1", fixture.SchemaVersion)
	}
	if len(fixture.Cases) != 3 {
		t.Fatalf("token-scope fixture has %d cases, want 3", len(fixture.Cases))
	}
	if fixture.TokenSource != "POST /api/playback-info" || fixture.RevealMutation != "mutation { relock { revealed } }" || fixture.RouteTemplate != "/api/media/direct/{token}" || fixture.PlaybackRequest.MediaID == "" || len(fixture.AuthorizationHeaders) == 0 {
		t.Fatalf("token-scope fixture client flow is incomplete: %#v", fixture)
	}
	seen := map[string]bool{}
	clientRejectedRevealMismatch := false
	for _, tc := range fixture.Cases {
		seen[tc.VerifyReason] = true
		t.Run(tc.Name, func(t *testing.T) {
			srv := newPlaybackContractServer(t)
			token := tokenFromPlaybackInfo(t, srv, fixture)
			scope := fixtureScopeForCase(t, token, tc)
			assertVerifyReason(t, tc, token, scope)
			if tc.RouteExercised {
				if assertClientCoreCheck(t, srv, fixture, tc, token) && tc.VerifyReason == "stream token requires reveal" {
					clientRejectedRevealMismatch = true
				}
			}
		})
	}
	for _, reason := range []string{"", "stream token requires reveal", "stream token scope mismatch"} {
		if !seen[reason] {
			t.Errorf("token-scope fixture missing outcome %q", reason)
		}
	}
	if !clientRejectedRevealMismatch {
		t.Fatal("generated fixture did not make the client-facing Core check reject a Reveal mismatch")
	}
}

func assertVerifyReason(t *testing.T, tc tokenScopeCase, token string, scope auth.Scope) {
	t.Helper()
	tokens := streamtoken.NewService([]byte(tokenScopeTokenKey), tokenScopeClock)
	grant, err := tokens.UnverifiedGrant(token, streamtoken.Target(tc.Target))
	if err != nil {
		t.Fatalf("validate token for %s: %v", tc.Name, err)
	}
	if grant.MediaID != store.MediaID(tc.MediaID) || !grant.Restricted {
		t.Fatalf("token for %s has mediaID=%q restricted=%v", tc.Name, grant.MediaID, grant.Restricted)
	}
	_, err = tokens.Verify(token, streamtoken.Target(tc.Target), scope)
	if tc.VerifyReason == "" {
		if err != nil {
			t.Fatalf("Verify(%s) = %v, want serve", tc.Name, err)
		}
		return
	}
	if err == nil || err.Error() != tc.VerifyReason {
		t.Fatalf("Verify(%s) = %v, want %q", tc.Name, err, tc.VerifyReason)
	}
}

// assertClientCoreCheck is the reusable client-facing half of the fixture
// contract: fetch the token URL through the real Core HTTP stack and turn any
// non-serving response into a failed client check.
func assertClientCoreCheck(t *testing.T, srv *server.Server, fixture tokenScopeFixture, tc tokenScopeCase, token string) bool {
	t.Helper()
	status, body, err := runClientCoreCheck(t, srv, fixture, tc, token)
	if status != tc.HTTPStatus {
		t.Fatalf("client check %s status = %d, want %d: %s", tc.Name, status, tc.HTTPStatus, body)
	}
	if tc.VerifyReason == "" {
		if err != nil {
			t.Fatalf("client check %s = %v, want served bytes", tc.Name, err)
		}
		if body != expectedTokenScopeBytes(t, fixture) {
			t.Fatalf("client check %s body = %q, want served bytes", tc.Name, body)
		}
		return false
	}
	if err == nil {
		t.Fatalf("client check %s accepted a rejected fixture case", tc.Name)
	}
	if strings.Contains(body, expectedTokenScopeBytes(t, fixture)) {
		t.Fatalf("client check %s leaked restricted bytes", tc.Name)
	}
	return true
}

func runClientCoreCheck(t *testing.T, srv *server.Server, fixture tokenScopeFixture, tc tokenScopeCase, token string) (int, string, error) {
	t.Helper()
	if tc.Scope == "minted-relocked" {
		relockTokenScopeSession(t, srv, fixture)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, rec.Body.String(), fmt.Errorf("Core rejected token-scope fixture with HTTP %d", rec.Code)
	}
	return rec.Code, rec.Body.String(), nil
}

//nolint:gosec // Public deterministic dev-seed credentials are fixture inputs, never production secrets.
func newTokenScopeFixture() tokenScopeFixture {
	return tokenScopeFixture{
		SchemaVersion:  1,
		TokenSource:    "POST /api/playback-info",
		RevealMutation: "mutation { relock { revealed } }",
		RouteTemplate:  "/api/media/direct/{token}",
		PlaybackRequest: playback.InfoRequest{
			MediaID: tokenScopeMediaID,
			CapabilityProfile: playback.CapabilityProfile{
				Client: "token-scope", Containers: []string{"mp4"}, DirectCodecs: []string{"h264"},
			},
		},
		AuthorizationHeaders: map[string]string{
			"X-Beamfall-Session":       revealedSessionID,
			"X-Beamfall-Device":        "device-tv",
			"X-Beamfall-Device-Secret": "dev-tv-secret",
		},
		ExpectedBytesBase64: base64.StdEncoding.EncodeToString([]byte(tokenScopeBytes)),
		Cases: []tokenScopeCase{
			{
				Name: "revealed-serves-restricted", Target: string(streamtoken.TargetMediaDirect), MediaID: tokenScopeMediaID,
				Scope: "minted-revealed", RouteExercised: true,
				HTTPStatus: http.StatusOK,
			},
			{
				Name: "locked-rejects-restricted", Target: string(streamtoken.TargetMediaDirect), MediaID: tokenScopeMediaID,
				Scope: "minted-relocked", RouteExercised: true,
				HTTPStatus: http.StatusUnauthorized, VerifyReason: "stream token requires reveal",
			},
			{
				Name: "wrong-session-scope-mismatch", Target: string(streamtoken.TargetMediaDirect), MediaID: tokenScopeMediaID,
				Scope:        "wrong-session",
				SessionID:    lockedSessionID,
				VerifyReason: "stream token scope mismatch",
			},
		},
	}
}

func writeTokenScopeFixture(t *testing.T, fixture tokenScopeFixture) {
	t.Helper()
	pretty, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenScopeGoldenPath, append(pretty, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTokenScopeFixture(t *testing.T) tokenScopeFixture {
	t.Helper()
	raw, err := os.ReadFile(tokenScopeGoldenPath)
	if err != nil {
		t.Fatalf("token-scope fixture missing (%v); regenerate with BEAMFALL_UPDATE_GOLDEN=1", err)
	}
	var fixture tokenScopeFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode token-scope fixture: %v", err)
	}
	return fixture
}

func tokenFromPlaybackInfo(t *testing.T, srv *server.Server, fixture tokenScopeFixture) string {
	t.Helper()
	body, err := json.Marshal(fixture.PlaybackRequest)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/playback-info", strings.NewReader(string(body)))
	authorizeTokenScopeRequest(req, fixture.AuthorizationHeaders)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("playbackInfo status = %d: %s", rec.Code, rec.Body.String())
	}
	var info playback.InfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	for _, stream := range info.Streams {
		if stream.Kind == "direct-play" {
			return stream.Token
		}
	}
	t.Fatal("playbackInfo did not return a direct-play Stream Token")
	return ""
}

func relockTokenScopeSession(t *testing.T, srv *server.Server, fixture tokenScopeFixture) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": fixture.RevealMutation})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(body)))
	authorizeTokenScopeRequest(req, fixture.AuthorizationHeaders)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"revealed":false`) {
		t.Fatalf("relock status = %d: %s", rec.Code, rec.Body.String())
	}
}

func authorizeTokenScopeRequest(req *http.Request, headers map[string]string) {
	for name, value := range headers {
		req.Header.Set(name, value)
	}
}

func fixtureScopeForCase(t *testing.T, token string, tc tokenScopeCase) auth.Scope {
	t.Helper()
	grant, err := streamtoken.NewService([]byte(tokenScopeTokenKey), tokenScopeClock).UnverifiedGrant(token, streamtoken.TargetMediaDirect)
	if err != nil {
		t.Fatal(err)
	}
	scope := auth.Scope{DeviceID: grant.DeviceID, SessionID: grant.SessionID, ProfileID: grant.ProfileID, Revealed: true}
	switch tc.Scope {
	case "minted-revealed":
		return scope
	case "minted-relocked":
		scope.Revealed = false
		return scope
	case "wrong-session":
		scope.SessionID = store.SessionID(tc.SessionID)
		return scope
	default:
		t.Fatalf("unknown token-scope fixture mode %q", tc.Scope)
		return auth.Scope{}
	}
}

func expectedTokenScopeBytes(t *testing.T, fixture tokenScopeFixture) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(fixture.ExpectedBytesBase64)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func tokenScopeClock() time.Time { return time.Unix(0, 0).UTC() }

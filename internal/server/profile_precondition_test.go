package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// A browser tab still showing Profile A after another tab switched the shared
// session to Profile B names A in X-Beamfall-Profile; the server refuses its
// requests before any handler runs (CORE-2923).

func profileRequest(path, body, profile string) *http.Request {
	req := newJSONRequest(http.MethodPost, path, strings.NewReader(body))
	authorizeViewerCookies(req)
	if profile != "" {
		req.Header.Set(auth.ProfileHeader, profile)
	}
	return req
}

const setPosterSizePref = `{"query":"mutation { setClientPref(key: \"web.poster-size\", value: \"small\") { key value } }"}`

func TestStaleProfileHeaderRefusesGraphQLWrite(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	// Another tab switched the shared session from profile-default to profile-managed.
	if err := st.SetSessionProfile(ctx, "session-default", "profile-managed"); err != nil {
		t.Fatalf("SetSessionProfile: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, profileRequest("/api/graphql", setPosterSizePref, "profile-default"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale write = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var refusal struct {
		Errors []struct {
			Extensions struct {
				Code   string `json:"code"`
				Reason string `json:"reason"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode refusal: %v: %s", err, rec.Body.String())
	}
	if len(refusal.Errors) != 1 || refusal.Errors[0].Extensions.Code != "CONFLICT" || refusal.Errors[0].Extensions.Reason != "PROFILE_MISMATCH" {
		t.Fatalf("refusal = %s, want one CONFLICT / PROFILE_MISMATCH error", rec.Body.String())
	}
	for _, profile := range []store.ProfileID{"profile-default", "profile-managed"} {
		prefs, err := st.ClientPrefs(ctx, profile)
		if err != nil {
			t.Fatalf("ClientPrefs(%s): %v", profile, err)
		}
		if len(prefs) != 0 {
			t.Fatalf("ClientPrefs(%s) = %+v after a refused write, want none", profile, prefs)
		}
	}

	// The tab that switched names the Profile the session is bound to, and
	// its write lands there.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, profileRequest("/api/graphql", setPosterSizePref, "profile-managed"))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"errors"`) {
		t.Fatalf("matching write = %d: %s", rec.Code, rec.Body.String())
	}
	prefs, err := st.ClientPrefs(ctx, "profile-managed")
	if err != nil || len(prefs) != 1 || prefs[0].Value != "small" {
		t.Fatalf("ClientPrefs(profile-managed) = %+v, %v, want the matching write", prefs, err)
	}
}

func TestProfileHeaderAbsentOrBlankIsNotChecked(t *testing.T) {
	for name, header := range map[string]string{"absent": "", "blank": "  "} {
		t.Run(name, func(t *testing.T) {
			srv, st := newTestServerWithStore(t)
			req := profileRequest("/api/graphql", setPosterSizePref, "")
			if header != "" {
				req.Header.Set(auth.ProfileHeader, header)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"errors"`) {
				t.Fatalf("write without a Profile header = %d: %s", rec.Code, rec.Body.String())
			}
			prefs, err := st.ClientPrefs(context.Background(), "profile-default")
			if err != nil || len(prefs) != 1 {
				t.Fatalf("ClientPrefs(profile-default) = %+v, %v, want the write", prefs, err)
			}
		})
	}
}

func TestStaleProfileHeaderRefusesRESTWriteBeforeHandler(t *testing.T) {
	srv := newTestServer(t)
	// An empty body would be a 400 from the subtitle pin handler; the refusal
	// comes first, so the handler never runs.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, profileRequest("/api/media/subtitle-pin", `{}`, "profile-managed"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale REST write = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var refusal profileMismatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode refusal: %v: %s", err, rec.Body.String())
	}
	if refusal != (profileMismatchResponse{Error: "profile mismatch", Code: "CONFLICT", Reason: "PROFILE_MISMATCH"}) {
		t.Fatalf("refusal = %+v", refusal)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	// The matching Profile reaches the handler, which judges the body itself.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, profileRequest("/api/media/subtitle-pin", `{}`, "profile-default"))
	if rec.Code == http.StatusConflict {
		t.Fatalf("matching REST write = 409: %s", rec.Body.String())
	}
}

// An event stream opened for Profile A stops delivering once another tab
// switches the shared session to B, and a reconnect still naming A is refused
// with the mismatch, so the tab learns to reload instead of reading B's events
// (CORE-2923). A stream naming no Profile is the control: it keeps streaming.
func TestStaleProfileHeaderEndsEventStream(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	connect := func(profile string) (*http.Response, context.CancelFunc) {
		streamCtx, cancel := context.WithCancel(ctx)
		req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, httpSrv.URL+"/api/events", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("X-Beamfall-Session", "session-default")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
		if profile != "" {
			req.Header.Set(auth.ProfileHeader, profile)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("connect: %v", err)
		}
		return resp, cancel
	}
	lines := func(resp *http.Response, streamCtx context.Context) <-chan string {
		out := make(chan string, 64)
		go func() {
			defer resp.Body.Close()
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				if rest, ok := strings.CutPrefix(scanner.Text(), "event: "); ok {
					select {
					case out <- rest:
					case <-streamCtx.Done():
						return
					}
				}
			}
		}()
		return out
	}

	boundResp, cancelBound := connect("profile-default")
	defer cancelBound()
	if boundResp.StatusCode != http.StatusOK {
		t.Fatalf("bound connect = %d, want 200", boundResp.StatusCode)
	}
	bound := lines(boundResp, ctx)
	controlResp, cancelControl := connect("")
	defer cancelControl()
	control := lines(controlResp, ctx)

	if err := st.AppendMediaEvent(ctx, "media.updated", "media-alpha", nil, now); err != nil {
		t.Fatalf("append event 1: %v", err)
	}
	waitForEvent(t, bound, "media.updated", "pre-switch event on the bound stream")
	waitForEvent(t, control, "media.updated", "pre-switch event on the control stream")

	if err := st.SetSessionProfile(ctx, "session-default", "profile-managed"); err != nil {
		t.Fatalf("SetSessionProfile: %v", err)
	}
	if err := st.AppendMediaEvent(ctx, "media.created", "media-alpha", nil, now); err != nil {
		t.Fatalf("append event 2: %v", err)
	}
	waitForEvent(t, control, "media.created", "post-switch event on the control stream")
	assertNoEvent(t, bound, "media.created", 750*time.Millisecond)

	staleResp, cancelStale := connect("profile-default")
	defer cancelStale()
	defer staleResp.Body.Close()
	var refusal profileMismatchResponse
	if err := json.NewDecoder(staleResp.Body).Decode(&refusal); err != nil {
		t.Fatalf("decode reconnect refusal: %v", err)
	}
	if staleResp.StatusCode != http.StatusConflict || refusal.Reason != profileMismatchReason {
		t.Fatalf("stale reconnect = %d %+v, want 409 PROFILE_MISMATCH", staleResp.StatusCode, refusal)
	}
}

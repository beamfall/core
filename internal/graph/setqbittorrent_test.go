//go:build !beamfall_appstore

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeQBittorrentConfigurer records the last Configure input and answers with a
// fixed error, standing in for the app-level reconfigure seam. It also doubles
// as the read provider so the post-mutation read reflects what was configured.
type fakeQBittorrentConfigurer struct {
	last       *QBittorrentIngestInput
	err        error
	configured bool
	routes     []QBittorrentRoute
}

func (f *fakeQBittorrentConfigurer) Configure(_ context.Context, in QBittorrentIngestInput) error {
	saved := in
	f.last = &saved
	if f.err != nil {
		return f.err
	}
	// Mimic the real configurer: on success the live config is the new one.
	f.configured = true
	f.routes = in.Routes
	return nil
}

func (f *fakeQBittorrentConfigurer) Config(_ context.Context) (QBittorrentConfig, bool) {
	if !f.configured {
		return QBittorrentConfig{}, false
	}
	return QBittorrentConfig{Routes: f.routes}, true
}

func (f *fakeQBittorrentConfigurer) Status(_ context.Context) []QBittorrentStatusEntry {
	return nil
}

func postSetQBittorrent(t *testing.T, handler http.Handler, authorize func(*http.Request), routesJSON string) *httptest.ResponseRecorder {
	t.Helper()
	mutation := `mutation Q($url: String!, $username: String!, $password: String!, $routes: [QBittorrentRouteInput!]) { setQBittorrentIngest(url: $url, username: $username, password: $password, routes: $routes) { config { enabled routes { category libraryID } } status { releaseName outcome } } }`
	body, err := json.Marshal(map[string]any{
		"query": mutation,
		"variables": map[string]any{
			"url":      "http://localhost:8080",
			"username": "operator",
			"password": secretForTest,
			"routes":   json.RawMessage(routesJSON),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

const secretForTest = "super-secret-pw"

// TestSetQBittorrentIngestMutation is FEAT-10 acceptance (a)+(d): the mutation
// is Admin-Session-gated (non-admin rejected), it forwards routing + the
// password to the configurer (the password bound for the secure store), and the
// password never rides the response wire (invariant #5).
func TestSetQBittorrentIngestMutation(t *testing.T) {
	// feature:qbittorrent-ingest
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	configurer := &fakeQBittorrentConfigurer{}
	handler.SetQBittorrentIngest(configurer)
	handler.SetQBittorrentIngestConfigurer(configurer)

	routesJSON := `[{"category":"films","libraryID":"lib-a"}]`

	// (d) A manager WITHOUT an open Admin Session is refused — the mutation is in
	// the Console class, like the read.
	if rec := postSetQBittorrent(t, handler, authorizeManager, routesJSON); !bytes.Contains(rec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin setQBittorrentIngest = %s", rec.Body.String())
	}
	if configurer.last != nil {
		t.Fatalf("configurer invoked before admin gate passed: %#v", configurer.last)
	}
	openManagerAdminSession(t, st)

	// (a) With an Admin Session, the mutation forwards the input to the seam.
	rec := postSetQBittorrent(t, handler, authorizeManager, routesJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if configurer.last == nil {
		t.Fatalf("configurer not invoked")
	}
	if configurer.last.URL != "http://localhost:8080" || configurer.last.Username != "operator" {
		t.Fatalf("forwarded connection = %#v", configurer.last)
	}
	if !configurer.last.SetPassword || configurer.last.Password != secretForTest {
		t.Fatalf("password not forwarded for secure-store write: %#v", configurer.last)
	}
	if len(configurer.last.Routes) != 1 || configurer.last.Routes[0].Category != "films" || configurer.last.Routes[0].LibraryID != "lib-a" {
		t.Fatalf("forwarded routes = %#v", configurer.last.Routes)
	}

	// Invariant #5: the password (inbound) never appears in the response.
	if bytes.Contains(rec.Body.Bytes(), []byte(secretForTest)) || bytes.Contains(rec.Body.Bytes(), []byte("password")) {
		t.Fatalf("response leaked the password: %s", rec.Body.String())
	}

	// The post-mutation read reflects the new config.
	var resp struct {
		Data struct {
			SetQBittorrentIngest QBittorrentIngestInfo `json:"setQBittorrentIngest"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if !resp.Data.SetQBittorrentIngest.Config.Enabled {
		t.Fatalf("post-mutation config not enabled: %#v", resp.Data.SetQBittorrentIngest)
	}
}

// TestSetQBittorrentIngestRejectsInvalidRouting is FEAT-10 acceptance (b): a
// ValidateRouting rejection (surfaced by the configurer as a client-safe
// ValidationError) is returned to the caller as the mutation error.
func TestSetQBittorrentIngestRejectsInvalidRouting(t *testing.T) {
	// feature:qbittorrent-ingest
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	configurer := &fakeQBittorrentConfigurer{
		err: store.ValidationError(`qbittorrent: routed category "typo" does not exist in qBittorrent`),
	}
	handler.SetQBittorrentIngest(configurer)
	handler.SetQBittorrentIngestConfigurer(configurer)
	openManagerAdminSession(t, st)

	rec := postSetQBittorrent(t, handler, authorizeManager, `[{"category":"typo","libraryID":"lib-a"}]`)
	if !bytes.Contains(rec.Body.Bytes(), []byte("does not exist")) {
		t.Fatalf("invalid routing not surfaced: %s", rec.Body.String())
	}
}

// TestSetQBittorrentIngestUnavailable proves a build with no configurer wired
// reports the feature unavailable rather than silently dropping the write.
func TestSetQBittorrentIngestUnavailable(t *testing.T) {
	// feature:qbittorrent-ingest
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	rec := postSetQBittorrent(t, handler, authorizeManager, `[]`)
	if !bytes.Contains(rec.Body.Bytes(), []byte("unavailable")) {
		t.Fatalf("no-configurer build did not report unavailable: %s", rec.Body.String())
	}
}

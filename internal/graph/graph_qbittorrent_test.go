//go:build !beamfall_appstore

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

// fakeQBittorrentProvider is a test double for the QBittorrentIngestProvider
// seam, answering from fixed config + status values.
type fakeQBittorrentProvider struct {
	config     QBittorrentConfig
	configured bool
	status     []QBittorrentStatusEntry
}

func (f fakeQBittorrentProvider) Config(ctx context.Context) (QBittorrentConfig, bool) {
	return f.config, f.configured
}

func (f fakeQBittorrentProvider) Status(ctx context.Context) []QBittorrentStatusEntry {
	return f.status
}

func TestQBittorrentIngestAdminGated(t *testing.T) {
	// feature:qbittorrent-ingest
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	query := `{"query":"query { qbittorrentIngest { config { enabled routes { category libraryID } } status { releaseName category outcome libraryID detail } } }"}`
	post := func(authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(query)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// A session without an open Admin Session is refused — ingest config is an
	// operator-Console read.
	if rec := post(authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin qbittorrentIngest = %s", rec.Body.String())
	}
	openManagerAdminSession(t, st)

	// No provider wired: ingest reports disabled with an empty feed.
	info := decodeQBittorrent(t, post(authorizeManager))
	if info.Config.Enabled {
		t.Fatalf("no-provider config enabled = true, want false")
	}
	if len(info.Status) != 0 {
		t.Fatalf("no-provider status = %#v, want empty", info.Status)
	}

	// Wire a provider: ingest is configured with a route and one imported row.
	handler.SetQBittorrentIngest(fakeQBittorrentProvider{
		configured: true,
		config: QBittorrentConfig{
			Routes: []QBittorrentRoute{{Category: "films", LibraryID: "library-local"}},
		},
		status: []QBittorrentStatusEntry{
			{ReleaseName: "Some.Release.2024", Category: "films", Outcome: "imported", LibraryID: "library-local"},
			{ReleaseName: "Stray.Release", Category: "unmapped", Outcome: "unrouted", Detail: "no routing for category"},
		},
	})
	info = decodeQBittorrent(t, post(authorizeManager))
	if !info.Config.Enabled {
		t.Fatalf("configured ingest enabled = false, want true")
	}
	if len(info.Config.Routes) != 1 || info.Config.Routes[0].Category != "films" || info.Config.Routes[0].LibraryID != "library-local" {
		t.Fatalf("routes = %#v", info.Config.Routes)
	}
	if len(info.Status) != 2 || info.Status[0].Outcome != "imported" || info.Status[1].Outcome != "unrouted" {
		t.Fatalf("status = %#v", info.Status)
	}
	// No credential ever rides the wire.
	body := post(authorizeManager).Body.String()
	if bytes.Contains([]byte(body), []byte("password")) || bytes.Contains([]byte(body), []byte("secret")) {
		t.Fatalf("ingest surface leaked a credential field: %s", body)
	}
}

func decodeQBittorrent(t *testing.T, rec *httptest.ResponseRecorder) QBittorrentIngestInfo {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			QBittorrentIngest QBittorrentIngestInfo `json:"qbittorrentIngest"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	return resp.Data.QBittorrentIngest
}

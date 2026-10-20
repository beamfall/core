package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

func TestProductScenarioRevealControlsRestrictedNavigationAndPlayback(t *testing.T) {
	// scenario:restricted-reveal-playback
	scenario := productScenario{
		name: "restricted Library stays unavailable until reveal",
		seed: func(t *testing.T) *Server {
			t.Helper()
			return newTestServerWithMediaFile(t, "media-archive", []byte("restricted bytes"))
		},
		actions: []productAction{
			{
				name: "viewer gets hidden-state navigation",
				run: func(t *testing.T, srv *Server) {
					doc := requestManifest(t, srv, authorizeViewer)
					assertLibraryIDs(t, doc, "library-local")
					if doc.Revealed {
						t.Fatal("viewer manifest is revealed")
					}
				},
			},
			{
				name: "viewer cannot stream restricted Media",
				run: func(t *testing.T, srv *Server) {
					resp := requestPlaybackInfo(t, srv, authorizeViewer, "media-archive")
					if resp.Decision != "unavailable" {
						t.Fatalf("Decision = %q, want unavailable", resp.Decision)
					}
					if len(resp.Streams) != 0 {
						t.Fatalf("Streams = %#v, want none", resp.Streams)
					}
				},
			},
			{
				name: "revealed session gets restricted navigation",
				run: func(t *testing.T, srv *Server) {
					doc := requestManifest(t, srv, authorizeRevealed)
					assertLibraryIDs(t, doc, "library-local", "library-archive")
					if !doc.Revealed {
						t.Fatal("revealed manifest is not stamped revealed")
					}
				},
			},
			{
				name: "revealed session streams restricted Media",
				run: func(t *testing.T, srv *Server) {
					resp := requestPlaybackInfo(t, srv, authorizeRevealed, "media-archive")
					if resp.Decision != "direct-play" {
						t.Fatalf("Decision = %q, want direct-play", resp.Decision)
					}
					if len(resp.Streams) == 0 || resp.Streams[0].Kind != "direct-play" {
						t.Fatalf("Streams = %#v, want direct-play first", resp.Streams)
					}
					req := httptest.NewRequest(http.MethodGet, resp.Streams[0].URL, nil)
					rec := httptest.NewRecorder()
					srv.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						t.Fatalf("stream status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
					}
					if rec.Body.String() != "restricted bytes" {
						t.Fatalf("stream body = %q, want seeded bytes", rec.Body.String())
					}
				},
			},
		},
	}
	runProductScenario(t, scenario)
}

type productScenario struct {
	name    string
	seed    func(*testing.T) *Server
	actions []productAction
}

type productAction struct {
	name string
	run  func(*testing.T, *Server)
}

func runProductScenario(t *testing.T, scenario productScenario) {
	t.Helper()
	t.Run(scenario.name, func(t *testing.T) {
		srv := scenario.seed(t)
		for _, action := range scenario.actions {
			t.Run(action.name, func(t *testing.T) {
				action.run(t, srv)
			})
		}
	})
}

func requestManifest(t *testing.T, srv *Server, authorize func(*http.Request)) manifest.Document {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	authorize(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var doc manifest.Document
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return doc
}

func requestPlaybackInfo(t *testing.T, srv *Server, authorize func(*http.Request), mediaID store.MediaID) playback.InfoResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"mediaID": mediaID,
		"capabilityProfile": map[string]any{
			"client":       "beamfall-scenario-test",
			"containers":   []string{"mp4"},
			"directCodecs": []string{"h264"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
	authorize(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("playback-info status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp playback.InfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode playback-info: %v", err)
	}
	return resp
}

func assertLibraryIDs(t *testing.T, doc manifest.Document, want ...string) {
	t.Helper()
	if len(doc.Libraries) != len(want) {
		t.Fatalf("Libraries = %#v, want IDs %v", doc.Libraries, want)
	}
	for i, library := range doc.Libraries {
		if library.ID != want[i] {
			t.Fatalf("Libraries[%d].ID = %q, want %q", i, library.ID, want[i])
		}
	}
}

package server

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
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// REACHABILITY. internal/mediahttp proves the subtitle write
// HANDLERS work when called directly; that is exactly what was already true of
// the store functions this lane was opened to fix, and it is not the same claim
// as "a viewer can reach them". These tests drive the MOUNTED server through
// ServeHTTP, so they fail if the routes are not registered on the mux — the one
// thing a direct handler call can never show.

// newSubtitleRouteServer builds a real server over a store holding one item with
// a .srt sidecar on disk, which is what the offset command needs to resolve a
// track fingerprint.
func newSubtitleRouteServer(t *testing.T) (*Server, *store.Store, store.MediaID) {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "item.mkv")
	if err := os.WriteFile(path, []byte("mkv bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "item.en.srt"),
		[]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "item",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:subtitle-route",
		Sidecars:           []store.MediaFileSidecarCaption{{RelPath: "item.en.srt", Format: "srt", Language: "en"}},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, nil)
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	tokens.SetRecorder(st)
	srv := New(
		manifests,
		playback.NewService(st, tokens, clock),
		beam.NewService(st, authService, clock),
		pairing.NewService(st, clock),
		graph.NewHandler(manifests, authService, st, nil),
		mediahttp.NewHandler(st, authService, tokens,
			artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		st,
	)
	return srv, st, media.ID
}

func serveSubtitleCommand(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := newJSONRequest(http.MethodPost, path, strings.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// Both commands answer on the mounted mux. An unmounted route would fall through
// to the ServeMux's own 404 ("404 page not found"), so a 200 here is only
// reachable through a registered route reaching the real handler.
func TestSubtitleWriteRoutesAreReachableThroughTheMountedServer(t *testing.T) {
	srv, st, mediaID := newSubtitleRouteServer(t)

	pin := serveSubtitleCommand(t, srv, "/api/media/subtitle-pin",
		`{"mediaId":"`+string(mediaID)+`","selected":true,"track":{"language":"en","kind":"sidecar"}}`)
	if pin.Code != http.StatusOK {
		t.Fatalf("POST /api/media/subtitle-pin = %d, want 200 (route unmounted?): %s", pin.Code, pin.Body.String())
	}
	if !strings.Contains(pin.Body.String(), `"pinned":true`) {
		t.Fatalf("pin body = %q, want the handler's own response", pin.Body.String())
	}

	offset := serveSubtitleCommand(t, srv, "/api/media/subtitle-offset",
		`{"mediaId":"`+string(mediaID)+`","sidecarIndex":0,"offsetMs":-750}`)
	if offset.Code != http.StatusOK {
		t.Fatalf("POST /api/media/subtitle-offset = %d, want 200 (route unmounted?): %s", offset.Code, offset.Body.String())
	}
	if !strings.Contains(offset.Body.String(), `"offsetMs":-750`) {
		t.Fatalf("offset body = %q, want the handler's own response", offset.Body.String())
	}

	// Reaching the route is not the same as persisting: confirm the pin the
	// mounted route accepted is the one the playback resolver will read, under the
	// resolver's own group key.
	groupKey, err := st.MediaSubtitleGroup(context.Background(), mediaID)
	if err != nil {
		t.Fatalf("MediaSubtitleGroup: %v", err)
	}
	record, found, err := st.SubtitleChoice(context.Background(), "profile-default", groupKey)
	if err != nil || !found {
		t.Fatalf("SubtitleChoice found=%v err=%v; want the pin the route just accepted", found, err)
	}
	if record.SubLanguage != "en" || !record.Selected {
		t.Fatalf("stored pin = %+v, want the selected English track", record)
	}
}

// The mounted routes keep their credential gate: an unauthenticated caller is
// refused at the route, not served.
func TestSubtitleWriteRoutesRefuseUnauthenticatedCallers(t *testing.T) {
	srv, _, mediaID := newSubtitleRouteServer(t)
	for _, path := range []string{"/api/media/subtitle-pin", "/api/media/subtitle-offset"} {
		t.Run(path, func(t *testing.T) {
			req := newJSONRequest(http.MethodPost, path,
				strings.NewReader(`{"mediaId":"`+string(mediaID)+`","sidecarIndex":0,"offsetMs":100}`))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

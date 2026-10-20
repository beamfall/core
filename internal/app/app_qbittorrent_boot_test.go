package app

// SECAUDIT-20260817-QBIT-APPROVED-ROOT-1: the composition root captures the
// endpoint's category/default save paths at boot as the Ingestor's canonical
// approved download roots — the containment bound on every endpoint-nominated
// payload path — and boots fail-closed (empty roots, imports refused) when the
// capture fails, rather than trusting paths fetched later from a possibly
// compromised endpoint.

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/store"
)

// fakeQBTEndpoint serves the minimal anonymous qBittorrent Web API surface the
// boot capture touches: categories and the global default save path.
func fakeQBTEndpoint(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/torrents/categories", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"films":{"name":"films","savePath":"/downloads/films"},"audio":{"name":"audio","savePath":""}}`))
	})
	mux.HandleFunc("/api/v2/app/defaultSavePath", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("/downloads"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestBuildQBittorrentIngestorCapturesApprovedRootsAtBoot(t *testing.T) {
	// feature:qbittorrent-ingest
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := fakeQBTEndpoint(t)
	cfg := config.Config{DataDir: t.TempDir(), QBittorrentURL: srv.URL}
	ing, err := buildQBittorrentIngestor(ctx, cfg, st, slog.Default(), nil)
	if err != nil {
		t.Fatalf("buildQBittorrentIngestor: %v", err)
	}
	roots := ing.ApprovedRoots()
	sort.Strings(roots)
	// The default save path plus the non-empty category save path; the
	// blank-save-path category contributes nothing (it lands in the default).
	if len(roots) != 2 || roots[0] != "/downloads" || roots[1] != "/downloads/films" {
		t.Fatalf("approved roots = %v, want [/downloads /downloads/films]", roots)
	}
}

func TestBuildQBittorrentIngestorBootsFailClosedWhenEndpointUnreachable(t *testing.T) {
	// feature:qbittorrent-ingest
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// A refused connection (nothing listens on the port) must not fail boot;
	// the ingestor comes up with NO approved roots, so every import is refused
	// until the Console reconfigures ingest.
	cfg := config.Config{DataDir: t.TempDir(), QBittorrentURL: "http://127.0.0.1:1"}
	ing, err := buildQBittorrentIngestor(ctx, cfg, st, slog.Default(), nil)
	if err != nil {
		t.Fatalf("buildQBittorrentIngestor should boot despite unreachable endpoint: %v", err)
	}
	if roots := ing.ApprovedRoots(); len(roots) != 0 {
		t.Fatalf("approved roots = %v, want none (fail-closed)", roots)
	}
	if !ing.Configured() {
		t.Fatalf("endpoint-configured ingestor should still report Configured")
	}
}

package offlinepack

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// io:egress-attestation-status
func TestAttestationStatusSourceFetchesCachesAndFailsClosed(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"entries":{"2A":{"status":"REVOKED"}}}`))
	}))
	defer srv.Close()

	now := testNow
	cache := filepath.Join(t.TempDir(), "attestation-status.json")
	src := &AttestationStatusSource{URL: srv.URL, Client: srv.Client(), CachePath: cache, Now: func() time.Time { return now }}
	list, err := src.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := list.status(big.NewInt(0x2a)); !ok || status != "REVOKED" {
		t.Fatalf("serial 2a status = %q, %v", status, ok)
	}
	info, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode = %v, want 0600", info.Mode().Perm())
	}

	// Fresh: no refetch.
	now = now.Add(time.Hour)
	if _, err := src.Current(context.Background()); err != nil || hits.Load() != 1 {
		t.Fatalf("fresh list: err=%v hits=%d", err, hits.Load())
	}

	// A new process loads the cache and, while the server is down, keeps
	// serving it until the maximum age, then fails closed.
	fail.Store(true)
	now = testNow.Add(2 * 24 * time.Hour)
	restarted := &AttestationStatusSource{URL: srv.URL, Client: srv.Client(), CachePath: cache, Now: func() time.Time { return now }}
	if _, err := restarted.Current(context.Background()); err != nil {
		t.Fatalf("stale-but-usable cache refused: %v", err)
	}
	now = testNow.Add(31 * 24 * time.Hour)
	if _, err := restarted.Current(context.Background()); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("err = %v, want fail closed past max age", err)
	}

	// Recovery once the server is back and the retry spacing has passed.
	fail.Store(false)
	now = now.Add(attestationStatusRetryAfter + time.Second)
	if _, err := restarted.Current(context.Background()); err != nil {
		t.Fatalf("recovery: %v", err)
	}
}

func TestAttestationStatusSourceRefusesWithoutAnyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"no":"entries"}`))
	}))
	defer srv.Close()
	src := &AttestationStatusSource{URL: srv.URL, Client: srv.Client(), Now: func() time.Time { return testNow }}
	if _, err := src.Current(context.Background()); err == nil {
		t.Fatal("served a list from a malformed document")
	}
}

func TestAttestationStatusCacheKeepsItsRealAge(t *testing.T) {
	// A restored cache gets a fresh mtime; its embedded fetch time must win.
	cache := filepath.Join(t.TempDir(), "attestation-status.json")
	body := `{"fetchedAt":"` + testNow.Add(-60*24*time.Hour).UTC().Format(time.RFC3339) + `","body":{"entries":{"2a":{"status":"REVOKED"}}}}`
	if err := os.WriteFile(cache, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	src := &AttestationStatusSource{URL: "http://127.0.0.1:0/unreachable", CachePath: cache, Now: func() time.Time { return testNow }}
	list, err := src.Current(context.Background())
	if err == nil {
		t.Fatal("60-day-old restored cache served as fresh")
	}
	if _, listed := list.status(big.NewInt(0x2a)); !listed {
		t.Fatal("stale list not returned alongside the error")
	}
}

func TestAttestationStatusFetchSurvivesCanceledCaller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entries":{}}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := &AttestationStatusSource{URL: srv.URL, Client: srv.Client(), Now: func() time.Time { return testNow }}
	if _, err := src.Current(ctx); err != nil {
		t.Fatalf("a canceled caller blocked the shared fetch: %v", err)
	}
}

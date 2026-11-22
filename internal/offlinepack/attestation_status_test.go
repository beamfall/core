package offlinepack

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// io:egress-attestation-status
func TestAttestationStatusSourceFetchesAndFailsClosed(t *testing.T) {
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
	src := &AttestationStatusSource{URL: srv.URL, Client: srv.Client(), Now: func() time.Time { return now }}
	list, err := src.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := list.status(big.NewInt(0x2a)); !ok || status != "REVOKED" {
		t.Fatalf("serial 2a status = %q, %v", status, ok)
	}

	// Fresh: no refetch.
	now = now.Add(time.Hour)
	if _, err := src.Current(context.Background()); err != nil || hits.Load() != 1 {
		t.Fatalf("fresh list: err=%v hits=%d", err, hits.Load())
	}

	// Server down: the held list serves until the maximum age, then the
	// source fails closed but still hands back the entries it knows.
	fail.Store(true)
	now = testNow.Add(2 * 24 * time.Hour)
	if _, err := src.Current(context.Background()); err != nil {
		t.Fatalf("stale-but-usable list refused: %v", err)
	}
	now = testNow.Add(31 * 24 * time.Hour)
	list, err = src.Current(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("err = %v, want fail closed past max age", err)
	}
	if _, listed := list.status(big.NewInt(0x2a)); !listed {
		t.Fatal("known revocations dropped with the error")
	}

	// Recovery once the server is back and the retry spacing has passed.
	fail.Store(false)
	now = now.Add(attestationStatusRetryAfter + time.Second)
	if _, err := src.Current(context.Background()); err != nil {
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

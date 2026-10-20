package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// fixedEvents is a one-shot EventStore: it returns the seeded events once (after
// seq 0) and then nothing, so a test loop drains the backlog and stops.
type fixedEvents struct {
	mu     sync.Mutex
	events []store.OutboxEvent
}

func (f *fixedEvents) ReadEventOutbox(_ context.Context, afterSeq int64, limit int) ([]store.OutboxEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.OutboxEvent
	for _, e := range f.events {
		if e.Seq > afterSeq && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

func mediaEvent(seq int64, kind string) store.OutboxEvent {
	return store.OutboxEvent{
		Seq:           seq,
		OccurredAt:    time.Unix(0, 0).UTC(),
		Kind:          kind,
		AggregateType: store.OutboxAggregateMedia,
		AggregateID:   "media-1",
		Payload:       []byte(`{"x":1}`),
	}
}

// newEmitter builds an emitter with test-friendly defaults: a no-wait sleep so
// backoff does not stall the test, and a fast poll.
func newEmitter(t *testing.T, cfg Config, events EventStore, client Doer, dl DeadLetterSink) *Emitter {
	t.Helper()
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Millisecond
	}
	em, err := New(cfg, events, client, dl, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if em == nil {
		t.Fatal("New returned a dormant emitter for a configured endpoint")
	}
	em.sleep = func(context.Context, time.Duration) error { return nil } // no real backoff wait
	return em
}

// TestDeliversSignedPOSTForMatchingEvent: a configured endpoint receives a POST
// whose body carries a valid HMAC signature for a matching event.
func TestDeliversSignedPOSTForMatchingEvent(t *testing.T) {
	secret := []byte("topsecret")
	var gotBody []byte
	var gotSig, gotEventID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get(SignatureHeader)
		gotEventID = r.Header.Get(EventIDHeader)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	events := &fixedEvents{events: []store.OutboxEvent{mediaEvent(7, store.MediaEventCreated)}}
	em := newEmitter(t, Config{
		Endpoints: []Endpoint{{ID: "ha", URL: srv.URL, Secret: secret, EventKinds: []string{store.MediaEventCreated}}},
	}, events, srv.Client(), nil)

	if _, err := em.drain(context.Background(), 0); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if len(gotBody) == 0 {
		t.Fatal("endpoint received no POST")
	}
	// The signature header must be a valid HMAC-SHA256 of the exact body.
	mac := hmac.New(sha256.New, secret)
	mac.Write(gotBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(gotSig), []byte(want)) {
		t.Fatalf("signature mismatch: got %q want %q", gotSig, want)
	}
	if gotEventID != "7" {
		t.Fatalf("event id header: got %q want %q", gotEventID, "7")
	}
	// The body is the SSE wire envelope.
	var env struct {
		Kind        string          `json:"kind"`
		AggregateID string          `json:"aggregateID"`
		Payload     json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("body not the wire envelope: %v", err)
	}
	if env.Kind != store.MediaEventCreated || env.AggregateID != "media-1" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

// TestFilterDropsNonMatchingEvents: an endpoint scoped to one kind never
// receives an event of another kind.
func TestFilterDropsNonMatchingEvents(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	events := &fixedEvents{events: []store.OutboxEvent{
		mediaEvent(1, store.MediaEventCreated),
		mediaEvent(2, store.MediaEventDeleted),
		mediaEvent(3, store.MediaEventUpdated),
	}}
	em := newEmitter(t, Config{
		Endpoints: []Endpoint{{ID: "only-deleted", URL: srv.URL, Secret: []byte("s"), EventKinds: []string{store.MediaEventDeleted}}},
	}, events, srv.Client(), nil)

	if _, err := em.drain(context.Background(), 0); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if hits != 1 {
		t.Fatalf("filter leaked: got %d deliveries, want 1 (only the deleted event)", hits)
	}
}

// failingDoer always returns a 500, so every attempt fails.
type failingDoer struct {
	mu       sync.Mutex
	attempts int
}

func (d *failingDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.attempts++
	d.mu.Unlock()
	return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// TestRetryThenDeadLetter: a failing endpoint is retried up to MaxAttempts with
// exponential backoff, then the exhausted delivery lands in the dead-letter.
func TestRetryThenDeadLetter(t *testing.T) {
	doer := &failingDoer{}
	dl, err := OpenDeadLetter(filepath.Join(t.TempDir(), "dead-letter.json"))
	if err != nil {
		t.Fatalf("OpenDeadLetter: %v", err)
	}

	var backoffs []time.Duration
	events := &fixedEvents{events: []store.OutboxEvent{mediaEvent(1, store.MediaEventCreated)}}
	em := newEmitter(t, Config{
		Endpoints:   []Endpoint{{ID: "down", URL: "http://example.invalid", Secret: []byte("s")}},
		MaxAttempts: 3,
		BaseBackoff: 2 * time.Second,
	}, events, doer, dl)
	// Capture the backoff schedule instead of waiting it out.
	em.sleep = func(_ context.Context, d time.Duration) error {
		backoffs = append(backoffs, d)
		return nil
	}

	if _, err := em.drain(context.Background(), 0); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if doer.attempts != 3 {
		t.Fatalf("attempts: got %d want 3 (MaxAttempts)", doer.attempts)
	}
	// 3 attempts → 2 backoff waits, doubling: 2s then 4s.
	want := []time.Duration{2 * time.Second, 4 * time.Second}
	if len(backoffs) != len(want) {
		t.Fatalf("backoff count: got %v want %v", backoffs, want)
	}
	for i := range want {
		if backoffs[i] != want[i] {
			t.Fatalf("backoff[%d]: got %v want %v (exponential)", i, backoffs[i], want[i])
		}
	}
	entries := dl.Entries()
	if len(entries) != 1 {
		t.Fatalf("dead-letter: got %d entries want 1", len(entries))
	}
	got := entries[0]
	if got.EndpointID != "down" || got.EventSeq != 1 || got.Attempts != 3 {
		t.Fatalf("dead-letter entry: %+v", got)
	}
	if got.LastError == "" {
		t.Fatal("dead-letter entry has no error reason")
	}
	// The dead-letter must never persist the secret.
	if strings.Contains(string(mustJSON(t, got)), "secret") {
		t.Fatal("dead-letter row leaked a secret field")
	}
}

func TestCanceledRetryDoesNotAdvanceCursor(t *testing.T) {
	doer := &failingDoer{}
	dl, err := OpenDeadLetter(filepath.Join(t.TempDir(), "dead-letter.json"))
	if err != nil {
		t.Fatalf("OpenDeadLetter: %v", err)
	}
	events := &fixedEvents{events: []store.OutboxEvent{mediaEvent(1, store.MediaEventCreated)}}
	em := newEmitter(t, Config{
		Endpoints:   []Endpoint{{ID: "down", URL: "http://example.invalid", Secret: []byte("s")}},
		MaxAttempts: 2,
	}, events, doer, dl)
	ctx, cancel := context.WithCancel(context.Background())
	em.sleep = func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	}

	next, err := em.drain(ctx, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("drain error = %v, want context cancellation", err)
	}
	if next != 0 || dl.Cursor() != 0 {
		t.Fatalf("cursor advanced after interrupted delivery: next=%d persisted=%d", next, dl.Cursor())
	}
	if got := len(dl.Entries()); got != 0 {
		t.Fatalf("dead-letter entries = %d, want none before retries exhaust", got)
	}
}

// TestDeadLetterPersistsAcrossReopen: a recorded dead-letter survives a reopen
// (operator can inspect it after a restart).
func TestDeadLetterPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dl.json")
	dl, err := OpenDeadLetter(path)
	if err != nil {
		t.Fatalf("OpenDeadLetter: %v", err)
	}
	if err := dl.Record(DeadLetter{EndpointID: "x", EventSeq: 9, At: time.Now().UTC()}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	reopened, err := OpenDeadLetter(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Entries(); len(got) != 1 || got[0].EventSeq != 9 {
		t.Fatalf("persisted entries: %+v", got)
	}
}

func TestEmitterRestartDoesNotRedeliverDrainedOutboxRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dead-letter.json")
	dl, err := OpenDeadLetter(path)
	if err != nil {
		t.Fatalf("OpenDeadLetter: %v", err)
	}
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfg := Config{Endpoints: []Endpoint{{ID: "sink", URL: srv.URL, Secret: []byte("s")}}}
	events := &fixedEvents{events: []store.OutboxEvent{mediaEvent(1, store.MediaEventCreated)}}

	first := newEmitter(t, cfg, events, srv.Client(), dl)
	if _, err := first.drain(context.Background(), first.currentCursor()); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	reopened, err := OpenDeadLetter(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	second := newEmitter(t, cfg, events, srv.Client(), reopened)
	if _, err := second.drain(context.Background(), second.currentCursor()); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if hits != 1 {
		t.Fatalf("deliveries after restart = %d, want 1", hits)
	}
}

// TestDormantWithNoEndpoints: with no endpoints configured, New returns a nil
// emitter (the plugin is off — the app starts no goroutine).
func TestDormantWithNoEndpoints(t *testing.T) {
	em, err := New(Config{}, &fixedEvents{}, http.DefaultClient, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if em != nil {
		t.Fatal("emitter is not dormant with zero endpoints configured")
	}
}

func TestNilClientUsesBoundedDefaultHTTPClient(t *testing.T) {
	em, err := New(Config{Endpoints: []Endpoint{{ID: "sink", URL: "http://example.invalid", Secret: []byte("s")}}}, &fixedEvents{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client, ok := em.client.(*http.Client)
	if !ok {
		t.Fatalf("client type = %T, want *http.Client", em.client)
	}
	if client != defaultHTTPClient {
		t.Fatal("nil client did not use package default")
	}
	if client.Timeout != defaultHTTPTimeout {
		t.Fatalf("timeout = %v, want %v", client.Timeout, defaultHTTPTimeout)
	}
}

// TestEndpointValidation rejects a configured endpoint missing its secret or URL.
func TestEndpointValidation(t *testing.T) {
	cases := map[string]Endpoint{
		"no secret": {ID: "a", URL: "http://x"},
		"no url":    {ID: "a", Secret: []byte("s")},
		"no id":     {URL: "http://x", Secret: []byte("s")},
	}
	for name, ep := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(Config{Endpoints: []Endpoint{ep}}, &fixedEvents{}, nil, nil, nil); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

type leakingSecrets struct{}

func (leakingSecrets) Secret(ref string) (string, error) {
	return "", errors.New("failed secret ref " + ref + " at /private/secrets")
}

func TestLoadConfigSecretFailureIsFixedDiagnostic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.json")
	secretRef := "token-secret-do-not-leak"
	if err := os.WriteFile(path, []byte(`{"endpoints":[{"id":"sink","url":"https://example.invalid","secret_ref":"`+secretRef+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path, leakingSecrets{})
	if err != ErrSecretDiagnostic {
		t.Fatalf("error = %q, want fixed secret diagnostic", err)
	}
	if strings.Contains(err.Error(), secretRef) || strings.Contains(err.Error(), path) {
		t.Fatalf("diagnostic leaked secret ref or config path: %q", err)
	}
}

func TestLoadConfigFailureLogsCauseAndReturnsFixedDiagnostic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.json")
	if err := os.WriteFile(path, []byte(`{"endpoints":!}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	_, err := LoadConfig(path, nil)
	if err != ErrConfigDiagnostic {
		t.Fatalf("error = %q, want fixed config diagnostic", err)
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("returned diagnostic leaked local cause: %q", err)
	}
	if strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("returned diagnostic leaked local cause: %q", err)
	}
	if got := logs.String(); !strings.Contains(got, path) {
		t.Fatalf("local log omitted config path: %q", got)
	}
	if got := logs.String(); !strings.Contains(got, "invalid character") {
		t.Fatalf("local log omitted config cause: %q", got)
	}
}

type failingDeadLetter struct {
	err error
}

func (f failingDeadLetter) Record(DeadLetter) error { return f.err }

func TestDeadLetterFailureLogsCauseAndReturnsFixedDiagnostic(t *testing.T) {
	var logs bytes.Buffer
	cause := errors.New("write /var/lib/beamfall/webhook-dead-letter.json: no space left on device")
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ep := Endpoint{ID: "sink", URL: "http://example.invalid", Secret: []byte("hmac-secret")}
	em, err := New(
		Config{Endpoints: []Endpoint{ep}, MaxAttempts: 1},
		&fixedEvents{},
		&failingDoer{},
		failingDeadLetter{err: cause},
		logger,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = em.deliverTo(context.Background(), ep, mediaEvent(1, store.MediaEventCreated), []byte("{}"))
	if err != ErrDeadLetterDiagnostic {
		t.Fatalf("error = %q, want fixed dead-letter diagnostic", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("returned diagnostic leaked local cause: %q", err)
	}
	if got := logs.String(); !strings.Contains(got, cause.Error()) {
		t.Fatalf("local log omitted dead-letter cause: %q", got)
	}
}

func TestDeadLetterFailureLogRedactsEndpointSecret(t *testing.T) {
	var logs bytes.Buffer
	secret := []byte("dead-letter-secret-do-not-log")
	cause := errors.New("write failed with credential " + string(secret) + ": no space left on device")
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ep := Endpoint{ID: "sink", URL: "http://example.invalid", Secret: secret}
	em, err := New(
		Config{Endpoints: []Endpoint{ep}, MaxAttempts: 1},
		&fixedEvents{},
		&failingDoer{},
		failingDeadLetter{err: cause},
		logger,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = em.deliverTo(context.Background(), ep, mediaEvent(1, store.MediaEventCreated), []byte("{}"))
	if err != ErrDeadLetterDiagnostic {
		t.Fatalf("error = %q, want fixed dead-letter diagnostic", err)
	}
	if strings.Contains(err.Error(), string(secret)) {
		t.Fatalf("returned diagnostic leaked endpoint secret: %q", err)
	}
	if got := logs.String(); strings.Contains(got, string(secret)) {
		t.Fatalf("local log leaked endpoint secret: %q", got)
	}
	if got := logs.String(); !strings.Contains(got, "no space left on device") {
		t.Fatalf("local log omitted redacted dead-letter cause: %q", got)
	}
}

func TestMalformedSecretBearingURLIsFixedDiagnostic(t *testing.T) {
	ep := Endpoint{ID: "sink", URL: "http://%gh.example.invalid/path?token=do-not-leak", Secret: []byte("hmac-secret")}
	em := newEmitter(t, Config{Endpoints: []Endpoint{ep}, MaxAttempts: 1}, &fixedEvents{}, http.DefaultClient, nil)
	err := em.post(context.Background(), ep, mediaEvent(1, store.MediaEventCreated), []byte("{}"), []byte("sig"))
	if err != ErrRequestDiagnostic {
		t.Fatalf("error = %q, want fixed request diagnostic", err)
	}
	if strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("diagnostic leaked URL credential: %q", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

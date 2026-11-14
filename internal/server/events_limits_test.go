package server

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// openTestStream connects one SSE stream to httpSrv and returns its response
// status, a channel of every line it receives, and a cancel that drops it.
func openTestStream(t *testing.T, client *http.Client, url string, authorize func(*http.Request)) (int, <-chan string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/events", nil)
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	authorize(req)
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("connect: %v", err)
	}
	lines := make(chan string, 64)
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		close(lines)
		return resp.StatusCode, lines, cancel
	}
	go func() {
		defer resp.Body.Close()
		defer close(lines)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return resp.StatusCode, lines, cancel
}

func newStreamClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// TestEventStreamKeepaliveBoundStaysUnder15s pins the production constants: a
// keepalive is due once the stream has been silent for eventKeepaliveInterval
// and the stream wakes to write it then, so the interval itself must leave
// room under the 15 s bound.
func TestEventStreamKeepaliveBoundStaysUnder15s(t *testing.T) {
	// feature:sse-events
	if eventKeepaliveInterval > 12*time.Second {
		t.Fatalf("keepalive interval = %v, want headroom under 15s", eventKeepaliveInterval)
	}
}

// TestEventStreamIdleEmitsKeepalive proves an idle stream writes the SSE
// comment frame on the keepalive cadence rather than staying silent.
func TestEventStreamIdleEmitsKeepalive(t *testing.T) {
	// feature:sse-events
	srv, _ := newTestServerWithStore(t)
	srv.eventStreams.keepalive = 300 * time.Millisecond
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	status, lines, cancel := openTestStream(t, newStreamClient(t), httpSrv.URL, authorizeViewer)
	defer cancel()
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	start := time.Now()
	seen := 0
	deadline := time.After(3 * time.Second)
	for seen < 2 {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before keepalive")
			}
			if strings.HasPrefix(line, "event:") {
				t.Fatalf("idle stream delivered an event: %q", line)
			}
			if line == ": keepalive" {
				seen++
			}
		case <-deadline:
			t.Fatalf("saw %d keepalive frames in 3s, want 2", seen)
		}
	}
	if elapsed := time.Since(start); elapsed < 2*300*time.Millisecond {
		t.Fatalf("two keepalives after %v; they should be spaced by the interval", elapsed)
	}
}

// TestEventStreamPerSessionCap proves the (cap+1)th concurrent stream for one
// session is refused with 429, another session is unaffected, and closing a
// stream frees its slot.
func TestEventStreamPerSessionCap(t *testing.T) {
	// feature:sse-events
	srv, _ := newTestServerWithStore(t)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	client := newStreamClient(t)

	cancels := make([]context.CancelFunc, 0, eventStreamsPerPrincipal)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for i := 0; i < eventStreamsPerPrincipal; i++ {
		status, _, cancel := openTestStream(t, client, httpSrv.URL, authorizeViewer)
		cancels = append(cancels, cancel)
		if status != http.StatusOK {
			t.Fatalf("stream %d status = %d, want 200", i+1, status)
		}
	}

	status, _, cancel := openTestStream(t, client, httpSrv.URL, authorizeViewer)
	cancel()
	if status != http.StatusTooManyRequests {
		t.Fatalf("stream %d status = %d, want 429", eventStreamsPerPrincipal+1, status)
	}

	status, _, cancelOther := openTestStream(t, client, httpSrv.URL, authorizeRevealed)
	defer cancelOther()
	if status != http.StatusOK {
		t.Fatalf("other session status = %d, want 200", status)
	}

	// Dropping one stream releases its slot once the handler notices.
	cancels[0]()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, _, cancel := openTestStream(t, client, httpSrv.URL, authorizeViewer)
		cancels = append(cancels, cancel)
		if status == http.StatusOK {
			break
		}
		cancel()
		if time.Now().After(deadline) {
			t.Fatalf("slot never released: status = %d", status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEventStreamStateReleasesSlots(t *testing.T) {
	var state eventStreamState
	if !state.acquire("session:a", 1) {
		t.Fatal("first acquire refused")
	}
	if state.acquire("session:a", 1) {
		t.Fatal("acquire over the cap succeeded")
	}
	if !state.acquire("token:a", 1) {
		t.Fatal("a different principal kind shares the cap")
	}
	state.release("session:a")
	state.release("token:a")
	if got := state.openStreams(); got != 0 {
		t.Fatalf("openStreams = %d after release, want 0", got)
	}
	if len(state.perKey) != 0 {
		t.Fatalf("perKey not emptied: %v", state.perKey)
	}
	if !state.acquire("session:a", 1) {
		t.Fatal("acquire after release refused")
	}
}

// countingEventStore counts every store read the SSE loop makes.
type countingEventStore struct {
	EventStore
	outbox  atomic.Int64
	media   atomic.Int64
	library atomic.Int64
}

func (c *countingEventStore) ReadEventOutbox(ctx context.Context, afterSeq int64, limit int) ([]store.OutboxEvent, error) {
	c.outbox.Add(1)
	return c.EventStore.ReadEventOutbox(ctx, afterSeq, limit)
}

func (c *countingEventStore) Media(ctx context.Context, id store.MediaID, filter store.LibraryFilter) (store.Media, error) {
	c.media.Add(1)
	return c.EventStore.Media(ctx, id, filter)
}

func (c *countingEventStore) LibraryVisible(ctx context.Context, id store.LibraryID, filter store.LibraryFilter) (bool, error) {
	c.library.Add(1)
	return c.EventStore.LibraryVisible(ctx, id, filter)
}

// countScopes routes the stream's Scope derivations through a counter. Each
// derivation is the multi-read auth path (session, device, secret, Profile).
func countScopes(srv *Server) *atomic.Int64 {
	var n atomic.Int64
	srv.eventStreams.resolve = func(ctx context.Context, r *http.Request) (auth.Scope, error) {
		n.Add(1)
		return srv.auth.ScopeFromRequest(ctx, r)
	}
	return &n
}

// serveStreamFor runs one /api/events request for d and returns the body.
// during runs concurrently with the handler.
func serveStreamFor(t *testing.T, srv *Server, d time.Duration, during func()) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeHTTP(rec, req)
	}()
	if during != nil {
		during()
	}
	<-done
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestEventStreamIdleDoesNotPollStore proves an idle stream reads the outbox
// once, when it opens, and then waits for the store's commit signal: no
// further outbox reads, no Scope re-derivation and no Media or Library lookups
// (CORE-1158). Before, every stream read the outbox four times a second.
func TestEventStreamIdleDoesNotPollStore(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	counting := &countingEventStore{EventStore: st}
	srv.SetEvents(counting)
	scopes := countScopes(srv)

	serveStreamFor(t, srv, 1200*time.Millisecond, nil)

	if polls := counting.outbox.Load(); polls != 1 {
		t.Fatalf("outbox reads = %d over an idle 1.2s, want 1 (the opening backlog read)", polls)
	}
	if got := counting.media.Load() + counting.library.Load(); got != 0 {
		t.Fatalf("idle stream made %d Media/Library reads, want 0", got)
	}
	if got := scopes.Load(); got != 1 {
		t.Fatalf("Scope derivations = %d, want 1 (the handshake only)", got)
	}
}

// TestEventStreamDeliversOnCommitSignal proves an event appended while the
// stream is idle is delivered at once: the fallback read is 30 s away, so a
// prompt delivery can only come from the commit signal.
func TestEventStreamDeliversOnCommitSignal(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	status, lines, cancel := openTestStream(t, newStreamClient(t), httpSrv.URL, authorizeViewer)
	defer cancel()
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	time.Sleep(100 * time.Millisecond) // let the stream drain its empty backlog and wait
	const want = "event: job.queued"
	for i := range 2 {
		sent := time.Now()
		if _, err := st.EnqueueJob(context.Background(), store.Job{Kind: "scan", CreatedAt: sent}); err != nil {
			t.Fatalf("enqueue job %d: %v", i, err)
		}
		deadline := time.After(2 * time.Second)
		for got := false; !got; {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed before %q", want)
				}
				got = line == want
			case <-deadline:
				t.Fatalf("%q not delivered within 2s of its commit", want)
			}
		}
		if elapsed := time.Since(sent); elapsed > time.Second {
			t.Fatalf("%q delivered %v after its commit, want it on the commit signal", want, elapsed)
		}
	}
}

// silentEventStore never signals a commit: it stands in for a writer in
// another process, whose events only the fallback read can find.
type silentEventStore struct{ EventStore }

func (silentEventStore) EventOutboxChanged() <-chan struct{} { return nil }

// TestEventStreamFallbackReadFindsUnsignalledEvent proves an event whose
// writer could not signal is still delivered, by the slow fallback read.
func TestEventStreamFallbackReadFindsUnsignalledEvent(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	srv.SetEvents(silentEventStore{st})
	srv.eventStreams.fallback = 200 * time.Millisecond

	body := serveStreamFor(t, srv, 900*time.Millisecond, func() {
		time.Sleep(100 * time.Millisecond)
		if _, err := st.EnqueueJob(context.Background(), store.Job{Kind: "scan", CreatedAt: time.Now()}); err != nil {
			t.Errorf("enqueue job: %v", err)
		}
	})
	if !strings.Contains(body, "event: job.queued") {
		t.Fatalf("fallback read did not deliver the unsignalled event:\n%s", body)
	}
}

// TestEventStreamRederivesScopeWhenEventsArrive proves the cached Scope is
// refreshed before a batch of new events is filtered, and only then.
func TestEventStreamRederivesScopeWhenEventsArrive(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	scopes := countScopes(srv)

	body := serveStreamFor(t, srv, 1200*time.Millisecond, func() {
		time.Sleep(500 * time.Millisecond)
		if err := st.AppendMediaEvent(context.Background(), "media.updated", "media-alpha", nil, time.Unix(0, 0).UTC()); err != nil {
			t.Errorf("append event: %v", err)
		}
	})
	if !strings.Contains(body, "event: media.updated") {
		t.Fatalf("stream missing appended event:\n%s", body)
	}
	if got := scopes.Load(); got != 2 {
		t.Fatalf("Scope derivations = %d, want 2 (handshake + one for the new batch)", got)
	}
}

// TestEventStreamBacklogRederivesScope proves a backlog drained on the first
// poll re-derives the Scope: the header flush after the handshake can block
// long enough for a revocation, so the handshake Scope is not trusted for it.
func TestEventStreamBacklogRederivesScope(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	scopes := countScopes(srv)
	if err := st.AppendMediaEvent(context.Background(), "media.updated", "media-alpha", nil, time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("append event: %v", err)
	}
	body := serveStreamFor(t, srv, 600*time.Millisecond, nil)
	if !strings.Contains(body, "event: media.updated") {
		t.Fatalf("stream missing backlog event:\n%s", body)
	}
	if got := scopes.Load(); got != 2 {
		t.Fatalf("Scope derivations = %d, want 2 (handshake + one for the backlog)", got)
	}
}

// TestEventStreamIdleRecheckEndsRevokedStream proves an idle stream re-checks
// its Scope periodically, so a revoked device's stream ends without any event
// having to arrive.
func TestEventStreamIdleRecheckEndsRevokedStream(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	srv.eventStreams.recheck = 300 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeHTTP(rec, req)
	}()
	time.Sleep(100 * time.Millisecond)
	revokedAt := time.Now()
	if err := st.RevokeDevice(context.Background(), "device-tv"); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle stream still open 3s after revocation")
	}
	if elapsed := time.Since(revokedAt); elapsed > 300*time.Millisecond+500*time.Millisecond {
		t.Fatalf("revoked idle stream ended after %v", elapsed)
	}
	if srv.eventStreams.openStreams() != 0 {
		t.Fatal("ended stream still counted open")
	}
}

// TestMetricsExposesOpenEventStreams proves /metrics reports the open-streams
// gauge and that it falls back when streams close.
func TestMetricsExposesOpenEventStreams(t *testing.T) {
	// feature:sse-events
	srv, _ := newTestServerWithStore(t)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	client := newStreamClient(t)

	scrape := func() float64 {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, authorizedScrape(t, srv))
		if rec.Code != http.StatusOK {
			t.Fatalf("scrape status = %d", rec.Code)
		}
		values := scrapeMetrics(t, rec.Body.String())
		value, ok := values["beamfall_events_streams_open"]
		if !ok {
			t.Fatalf("scrape missing beamfall_events_streams_open:\n%s", rec.Body.String())
		}
		return value
	}
	if got := scrape(); got != 0 {
		t.Fatalf("open streams before connect = %v, want 0", got)
	}
	_, _, cancelA := openTestStream(t, client, httpSrv.URL, authorizeViewer)
	_, _, cancelB := openTestStream(t, client, httpSrv.URL, authorizeRevealed)
	if got := scrape(); got != 2 {
		cancelA()
		cancelB()
		t.Fatalf("open streams = %v, want 2", got)
	}
	cancelA()
	cancelB()
	deadline := time.Now().Add(3 * time.Second)
	for scrape() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("open streams = %v after close, want 0", scrape())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// flushFailWriter accepts writes but reports a failing flush via FlushError.
type flushFailWriter struct {
	*httptest.ResponseRecorder
}

func (flushFailWriter) FlushError() error { return errors.New("flush failed") }
func (flushFailWriter) Flush()            {}

func TestKeepaliveFlushErrorSurfacesThroughWrappers(t *testing.T) {
	var inner http.ResponseWriter = flushFailWriter{httptest.NewRecorder()}
	_, tracked := trackResponseWriter(inner)
	wrapped := &baseURLResponseWriter{ResponseWriter: tracked}
	if err := writeKeepalive(wrapped, wrapped); err == nil {
		t.Fatal("keepalive flush failure was swallowed by the response wrappers")
	}
}

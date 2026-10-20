package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

// newTestServerWithStore builds the same server newTestServer builds but hands
// the test the backing store so it can emit outbox events the SSE surface
// streams. Same dev seed: media-alpha lives in the open library-local,
// media-archive in the restricted library-archive (sqlite.go seed).
func newTestServerWithStore(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st := storetest.Open(t)
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	if err := st.SetSessionAdminUntil(context.Background(), "session-manager", clock().Add(time.Hour)); err != nil {
		t.Fatalf("SetSessionAdminUntil: %v", err)
	}
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, nil)
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	tokens.SetRecorder(st)
	srv := New(
		manifests,
		playback.NewService(st, tokens, clock),
		beam.NewService(st, authService, clock),
		pairing.NewService(st, clock),
		testServerGraphHandler{Handler: graph.NewHandler(manifests, authService, st, nil)},
		mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		st,
	)
	srv.SetEvents(st)
	srv.SetPushNotifications(st)
	srv.SetV1Store(st)
	srv.SetV1ScanEnqueuer(testV1ScanEnqueuer(st))
	return srv, st
}

// collectSSE connects to /api/events with the given authorizer and returns the
// body the handler streamed before the (short-lived) request context expires.
// The handler tails until the client disconnects; a deadline context is the
// test's stand-in for the client going away after it has drained the backlog.
func collectSSE(t *testing.T, srv *Server, authorize func(*http.Request)) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	authorize(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("SSE status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec.Body.String()
}

func TestEventStreamRequiresSession(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

type eventsTestStore struct {
	*store.Store
	readErr error
}

func (s *eventsTestStore) ReadEventOutbox(context.Context, int64, int) ([]store.OutboxEvent, error) {
	return nil, s.readErr
}

func TestEventStreamOutboxReadErrorsAreLogged(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	stErr := errors.New("store read exploded")
	srv.SetEvents(&eventsTestStore{Store: st, readErr: stErr})

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 structured log, got %d: %q", len(lines), logs.String())
	}
	line := lines[0]
	for _, want := range []string{
		"event stream outbox read failed",
		"handler=/api/events",
		"cursor=0",
		"store read exploded",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log = %q, want %q", line, want)
		}
	}
}

func TestEventStreamOutboxReadCancellationIsQuiet(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetEvents(&eventsTestStore{Store: st, readErr: context.Canceled})

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := logs.String(); got != "" {
		t.Fatalf("unexpected log for cancellation path: %q", got)
	}
}

// TestEventStreamRevealFiltersRestrictedMedia is the sse-events proof: the
// reveal-filtered SSE surface streams the event outbox, and a
// Media-scoped event for restricted Media (media-archive, library-archive)
// never reaches an unrevealed session, while a visible-Media event
// (media-alpha) and a content-free job event both do. A revealed session
// receives the restricted event too — the same reveal boundary the JSON read
// surfaces enforce, applied to the long-lived delivery channel.
func TestEventStreamRevealFiltersRestrictedMedia(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	// A content-free job event (always delivered) and two Media-scoped events:
	// one visible, one restricted.
	if _, err := st.EnqueueJob(ctx, store.Job{Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := st.AppendMediaEvent(ctx, "media.updated", "media-alpha", []byte(`{"field":"title"}`), now); err != nil {
		t.Fatalf("append visible media event: %v", err)
	}
	if err := st.AppendMediaEvent(ctx, "media.updated", "media-archive", []byte(`{"field":"title"}`), now); err != nil {
		t.Fatalf("append restricted media event: %v", err)
	}

	unrevealed := collectSSE(t, srv, authorizeViewer)
	// The stream emits events at all (proves the fanout works).
	if !strings.Contains(unrevealed, "event: job.queued") {
		t.Fatalf("unrevealed stream missing job event:\n%s", unrevealed)
	}
	// The visible Media event reaches the unrevealed session.
	if !strings.Contains(unrevealed, "media-alpha") {
		t.Fatalf("unrevealed stream missing visible media event:\n%s", unrevealed)
	}
	// The restricted Media event is filtered out — it never reaches the
	// unrevealed session (the load-bearing reveal assertion).
	if strings.Contains(unrevealed, "media-archive") {
		t.Fatalf("unrevealed stream leaked restricted media event:\n%s", unrevealed)
	}

	// A revealed session sees the restricted event too.
	revealed := collectSSE(t, srv, authorizeRevealed)
	if !strings.Contains(revealed, "media-archive") {
		t.Fatalf("revealed stream missing restricted media event:\n%s", revealed)
	}
	if !strings.Contains(revealed, "media-alpha") {
		t.Fatalf("revealed stream missing visible media event:\n%s", revealed)
	}
}

// TestImportEmittedEventRevealFilters is the end-to-end H13 proof: a real
// store write (ImportMediaFile into the restricted library) emits the
// media.created outbox event, and the SSE delivery side reveal-filters it — the
// unrevealed viewer never sees it, the revealed session does. This exercises the
// WRITE→DELIVER chain H13 wires, not a hand-appended event.
func TestImportEmittedEventRevealFilters(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()

	restricted, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-archive",
		Path:               "/media/h13-restricted.mp4",
		Title:              "H13 Restricted",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:h13restricted",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile into restricted library: %v", err)
	}

	// The unrevealed viewer must not see the restricted item's created event.
	unrevealed := collectSSE(t, srv, authorizeViewer)
	if strings.Contains(unrevealed, string(restricted.ID)) {
		t.Fatalf("unrevealed stream leaked the restricted import event:\n%s", unrevealed)
	}

	// A revealed session sees the restricted item's created event — this also
	// proves the import actually emitted one (the unrevealed absence is not
	// vacuous).
	revealed := collectSSE(t, srv, authorizeRevealed)
	if !strings.Contains(revealed, string(restricted.ID)) {
		t.Fatalf("revealed stream missing the restricted import event:\n%s", revealed)
	}
	if !strings.Contains(revealed, "event: media.created") {
		t.Fatalf("revealed stream missing the media.created kind:\n%s", revealed)
	}
}

// TestEventStreamDeliversDeletionRevealFiltered is the H13 follow-up B proof:
// a media.deleted event now actually reaches clients (the delivery side gates it
// by the deleted Media's Library, not a live read of the gone row), WITHOUT
// weakening Reveal. Three load-bearing assertions, exercising the real
// TrashMedia write path → SSE delivery chain:
//
//  1. a deletion in the OPEN library-local reaches a normal (unrevealed) scope;
//  2. a deletion in the RESTRICTED library-archive does NOT reach an unrevealed
//     scope (no existence leak — the deletion stream is not an oracle);
//  3. that same restricted deletion DOES reach a revealed/authorized scope.
func TestEventStreamDeliversDeletionRevealFiltered(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()

	// Trash the open-library item (visible to any scope) and the restricted item
	// (only trashable via a revealed filter) through the real write path.
	openFilter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	revealedFilter := store.LibraryFilter{
		AllowedIDs: []store.LibraryID{"library-local", "library-archive"},
		Revealed:   true,
	}
	if err := st.TrashMedia(ctx, "media-alpha", openFilter); err != nil {
		t.Fatalf("TrashMedia open: %v", err)
	}
	if err := st.TrashMedia(ctx, "media-archive", revealedFilter); err != nil {
		t.Fatalf("TrashMedia restricted: %v", err)
	}

	// (1) + (2): a normal viewer sees the open deletion, never the restricted one.
	unrevealed := collectSSE(t, srv, authorizeViewer)
	if !strings.Contains(unrevealed, "event: media.deleted") || !strings.Contains(unrevealed, "media-alpha") {
		t.Fatalf("unrevealed stream missing the open-library deletion:\n%s", unrevealed)
	}
	if strings.Contains(unrevealed, "media-archive") {
		t.Fatalf("unrevealed stream leaked the restricted deletion (existence oracle):\n%s", unrevealed)
	}

	// (3): a revealed session sees the restricted deletion too — proving the
	// unrevealed absence above is a real filter, not a dropped-everywhere event.
	revealed := collectSSE(t, srv, authorizeRevealed)
	if !strings.Contains(revealed, "media-archive") {
		t.Fatalf("revealed stream missing the restricted deletion:\n%s", revealed)
	}
	if !strings.Contains(revealed, "media-alpha") {
		t.Fatalf("revealed stream missing the open-library deletion:\n%s", revealed)
	}
}

// TestEventStreamResumesFromCursor proves the Last-Event-ID / ?after= cursor:
// a reader that has already seen up to a Seq receives only newer events on
// reconnect (no duplicate redelivery of the backlog).
func TestEventStreamResumesFromCursor(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()
	if err := st.AppendMediaEvent(ctx, "media.updated", "media-alpha", nil, now); err != nil {
		t.Fatalf("append first event: %v", err)
	}

	// Read the backlog to learn the first event's Seq from its id: line.
	first := collectSSE(t, srv, authorizeViewer)
	seq := lastEventID(t, first)

	// A second event arrives after the cursor.
	if err := st.AppendMediaEvent(ctx, "media.created", "media-alpha", nil, now); err != nil {
		t.Fatalf("append second event: %v", err)
	}

	ctxTO, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctxTO)
	authorizeViewer(req)
	req.Header.Set("Last-Event-ID", seq)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "event: media.created") {
		t.Fatalf("resumed stream missing the new event:\n%s", body)
	}
	if strings.Contains(body, "event: media.updated") {
		t.Fatalf("resumed stream re-delivered the already-seen event:\n%s", body)
	}
}

func TestEventStreamOldCursorForcesFullRefresh(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetEvents(&eventsTestStore{Store: st, readErr: store.ErrEventCursorTooOld})

	ctxTO, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctxTO)
	authorizeViewer(req)
	req.Header.Set("Last-Event-ID", "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "event: client.fullRefresh") || !strings.Contains(body, `"reason":"cursor-too-old"`) {
		t.Fatalf("old cursor did not force full refresh:\n%s", body)
	}
	if got := strings.Count(body, "event: client.fullRefresh"); got != 1 {
		t.Fatalf("full refresh events = %d, want exactly one:\n%s", got, body)
	}
}

// TestEventStreamEnforcesEventsSubscribeScope is the DEV-1 per-route authz
// proof: /api/events declares the events-subscribe scope,
// so a scoped developer key that lacks it is rejected (403) at the handshake
// before any stream headers are written, while a key that carries it streams
// the backlog like any authenticated caller. Session-authenticated callers
// (covered by the other tests in this file) carry no API scopes and keep their
// existing session/Reveal gating untouched.
func TestEventStreamEnforcesEventsSubscribeScope(t *testing.T) {
	// feature:scoped-key-auth
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()
	if err := st.AppendMediaEvent(ctx, "media.updated", "media-alpha", []byte(`{"field":"title"}`), now); err != nil {
		t.Fatalf("append event: %v", err)
	}

	// A key WITHOUT events-subscribe is forbidden from /api/events.
	_, ingestKey, err := st.CreateScopedAPIKey(ctx, "ingest-only", []store.APIScope{store.ScopeIngest}, "", now)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey (ingest): %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+ingestKey)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ingest-only key status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	// A key WITH events-subscribe streams the backlog.
	_, subKey, err := st.CreateScopedAPIKey(ctx, "subscriber", []store.APIScope{store.ScopeEventsSubscribe}, "", now)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey (subscribe): %v", err)
	}
	body := collectSSE(t, srv, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+subKey)
	})
	if !strings.Contains(body, "media-alpha") {
		t.Fatalf("events-subscribe key did not receive the event:\n%s", body)
	}
}

// lastEventID extracts the last id: line value from an SSE body.
func lastEventID(t *testing.T, body string) string {
	t.Helper()
	id := ""
	for _, line := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(line, "id: "); ok {
			id = strings.TrimSpace(rest)
		}
	}
	if id == "" {
		t.Fatalf("no id: line in SSE body:\n%s", body)
	}
	return id
}

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// events_action_test.go treats /api/events as a reveal-filtered ACTION
//: not a pure read but an authenticated, LibraryFilter-scoped
// subscription that must never become an existence oracle for hidden Media.
// It complements events_test.go (which proves the wire-level fanout) by
// exercising the reveal gate directly (eventPermitted), proving the
// byte-for-byte indistinguishability of the hidden-vs-absent stream, and
// covering the ?after= query cursor branch of resumeCursor that the
// header-based test in events_test.go does not.
//
// Reused harness from events_test.go / server_test.go:
//   - newTestServerWithStore  (Server + backing store, SetEvents wired)
//   - collectSSE              (drives /api/events to context-timeout)
//   - authorizeViewer         (session-default: sees library-local only)
//   - authorizeRevealed       (session-revealed: sees library-archive too)
//   - lastEventID             (extracts the trailing id: line)
// Seed (sqlite.go): media-alpha ∈ library-local (open); media-archive ∈
// library-archive (IsRestricted).

// eventsActionUnrevealedFilter is the LibraryFilter the seed's session-default
// resolves to: the open library only, reveal off. media-archive is not in
// AllowedIDs and reveal is off, so Media(media-archive) resolves to not-found.
func eventsActionUnrevealedFilter() store.LibraryFilter {
	return store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}, Revealed: false}
}

// eventsActionRevealedFilter is the filter session-revealed resolves to: both
// libraries, reveal on, so the restricted Media resolves.
func eventsActionRevealedFilter() store.LibraryFilter {
	return store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local", "library-archive"}, Revealed: true}
}

// TestEventsStreamPermitsGateByReveal drives the reveal gate (eventPermitted)
// directly — the unit-level seam events_test.go only reaches through the wire.
// A Media-scoped event for restricted media-archive is denied under the
// unrevealed filter but permitted under the revealed one; the visible
// media-alpha event is permitted under both; a non-Media (job) aggregate is
// always permitted regardless of filter (job status carries no catalog
// content). This is the same boundary the wire test asserts, isolated to the
// decision function.
func TestEventsStreamPermitsGateByReveal(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	restricted := store.OutboxEvent{Kind: "media.updated", AggregateType: store.OutboxAggregateMedia, AggregateID: "media-archive", OccurredAt: now}
	visible := store.OutboxEvent{Kind: "media.updated", AggregateType: store.OutboxAggregateMedia, AggregateID: "media-alpha", OccurredAt: now}
	job := store.OutboxEvent{Kind: "job.queued", AggregateType: "job", AggregateID: "job-1", OccurredAt: now}

	unrevealed := eventsActionUnrevealedFilter()
	revealed := eventsActionRevealedFilter()

	// The Profile arg is irrelevant for Media/job aggregates (only session-control
	// events gate on it); pass the seed Profile for these calls.
	const profile = store.ProfileID("profile-default")

	// Restricted Media: denied unrevealed, permitted revealed (the load-bearing
	// reveal assertion at the gate).
	if srv.eventPermitted(ctx, restricted, profile, unrevealed) {
		t.Fatal("eventPermitted allowed restricted media event to an unrevealed filter")
	}
	if !srv.eventPermitted(ctx, restricted, profile, revealed) {
		t.Fatal("eventPermitted denied restricted media event to a revealed filter")
	}

	// Visible Media: permitted under both filters.
	if !srv.eventPermitted(ctx, visible, profile, unrevealed) {
		t.Fatal("eventPermitted denied a visible media event to an unrevealed filter")
	}
	if !srv.eventPermitted(ctx, visible, profile, revealed) {
		t.Fatal("eventPermitted denied a visible media event to a revealed filter")
	}

	// Job aggregate: always permitted (not Media-scoped), even under the
	// narrowest filter.
	if !srv.eventPermitted(ctx, job, profile, unrevealed) {
		t.Fatal("eventPermitted denied a content-free job event")
	}
}

// TestEventsStreamUnknownMediaDeniedNotErrored confirms the existence-oracle
// guard at the gate: a Media-scoped event naming an id that does not exist at
// all is treated exactly like a hidden one — denied (Media resolves to
// not-found), never surfaced and never distinguished from a restricted id.
func TestEventsStreamUnknownMediaDeniedNotErrored(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	unknown := store.OutboxEvent{Kind: "media.updated", AggregateType: store.OutboxAggregateMedia, AggregateID: "media-does-not-exist", OccurredAt: now}
	const profile = store.ProfileID("profile-default")
	if srv.eventPermitted(ctx, unknown, profile, eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted allowed an event for a nonexistent media id")
	}
	// A nonexistent id is also denied to a revealed filter — the gate keys on
	// resolvability, not on reveal alone, so an unknown id is never an oracle.
	if srv.eventPermitted(ctx, unknown, profile, eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted allowed an event for a nonexistent media id under a revealed filter")
	}
}

// TestEventsStreamHiddenEventLeavesNoTrace is the existence-oracle proof at the
// wire: the unrevealed stream must be byte-for-byte identical whether the
// hidden (restricted-Media) event exists or not. Two independent servers are
// seeded identically with a visible event (so the delivered cursor/id lines
// match), and only one additionally carries the restricted event; an
// unrevealed viewer's stream from each must be exactly equal. events_test.go
// asserts absence-of-substring; this asserts full indistinguishability — a
// reader cannot infer the hidden event's existence from anything in the
// response (id gaps, length, ordering).
func TestEventsStreamHiddenEventLeavesNoTrace(t *testing.T) {
	now := time.Unix(0, 0).UTC()

	// Server A: only the visible event exists.
	srvAbsent, stAbsent := newTestServerWithStore(t)
	if err := stAbsent.AppendMediaEvent(context.Background(), "media.updated", "media-alpha", []byte(`{"field":"title"}`), now); err != nil {
		t.Fatalf("append visible event (absent server): %v", err)
	}

	// Server B: the same visible event (same Seq=1) plus the hidden restricted
	// event after it (Seq=2). The visible event's id line is identical across
	// both because seq is assigned in append order from an empty outbox.
	srvPresent, stPresent := newTestServerWithStore(t)
	if err := stPresent.AppendMediaEvent(context.Background(), "media.updated", "media-alpha", []byte(`{"field":"title"}`), now); err != nil {
		t.Fatalf("append visible event (present server): %v", err)
	}
	if err := stPresent.AppendMediaEvent(context.Background(), "media.updated", "media-archive", []byte(`{"field":"title"}`), now); err != nil {
		t.Fatalf("append hidden event (present server): %v", err)
	}

	absent := collectSSE(t, srvAbsent, authorizeViewer)
	present := collectSSE(t, srvPresent, authorizeViewer)

	// Sanity: the visible event is actually delivered (otherwise an empty ==
	// empty comparison would pass vacuously).
	if !strings.Contains(absent, "media-alpha") {
		t.Fatalf("absent stream missing the visible event (test would be vacuous):\n%s", absent)
	}
	// The hidden id never appears in the present stream...
	if strings.Contains(present, "media-archive") {
		t.Fatalf("present stream leaked the hidden restricted event:\n%s", present)
	}
	// ...and the two unrevealed streams are byte-for-byte identical: the hidden
	// event left no trace at all (no extra id line, no length difference, no gap).
	if present != absent {
		t.Fatalf("unrevealed stream differs by hidden-event existence (existence oracle):\npresent:\n%q\nabsent:\n%q", present, absent)
	}
}

// TestEventsStreamResumesFromAfterQuery proves the ?after= query cursor branch
// of resumeCursor: a first connection (no Last-Event-ID header) passes its
// last-seen Seq as ?after=, and the stream delivers only events strictly after
// it — no replay of the already-seen event, no gap before the new one.
// events_test.go covers the Last-Event-ID header branch; this covers the query
// branch (the EventSource-less / first-connection path).
func TestEventsStreamResumesFromAfterQuery(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()
	if err := st.AppendMediaEvent(ctx, "media.updated", "media-alpha", nil, now); err != nil {
		t.Fatalf("append first event: %v", err)
	}

	// Learn the first event's Seq from the backlog read.
	first := collectSSE(t, srv, authorizeViewer)
	if !strings.Contains(first, "event: media.updated") {
		t.Fatalf("backlog missing the first event:\n%s", first)
	}
	seq := lastEventID(t, first)

	// A second event arrives strictly after the cursor.
	if err := st.AppendMediaEvent(ctx, "media.created", "media-alpha", nil, now); err != nil {
		t.Fatalf("append second event: %v", err)
	}

	// Resume via the ?after= query (no Last-Event-ID header).
	ctxTO, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events?after="+seq, nil).WithContext(ctxTO)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	// The newer event is delivered (no gap)...
	if !strings.Contains(body, "event: media.created") {
		t.Fatalf("?after= resume missing the new event:\n%s", body)
	}
	// ...and the already-seen event is not replayed (no duplicate redelivery).
	if strings.Contains(body, "event: media.updated") {
		t.Fatalf("?after= resume re-delivered the already-seen event:\n%s", body)
	}
}

// TestEventsStreamHeaderCursorWinsOverQuery proves resumeCursor's precedence:
// when both Last-Event-ID and ?after= are present, the header wins (the SSE
// standard reconnect cursor). The header points past the first event and the
// query points before it; if the query were honored the already-seen event
// would replay. It must not.
func TestEventsStreamHeaderCursorWinsOverQuery(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()
	if err := st.AppendMediaEvent(ctx, "media.updated", "media-alpha", nil, now); err != nil {
		t.Fatalf("append first event: %v", err)
	}

	first := collectSSE(t, srv, authorizeViewer)
	seq := lastEventID(t, first)

	if err := st.AppendMediaEvent(ctx, "media.created", "media-alpha", nil, now); err != nil {
		t.Fatalf("append second event: %v", err)
	}

	// Header resumes past the first event; query (?after=0) would replay it.
	ctxTO, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events?after=0", nil).WithContext(ctxTO)
	authorizeViewer(req)
	req.Header.Set("Last-Event-ID", seq)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "event: media.created") {
		t.Fatalf("header-cursor resume missing the new event:\n%s", body)
	}
	// The header (past the first event) won; the query (?after=0) was ignored,
	// so the already-seen event is not replayed.
	if strings.Contains(body, "event: media.updated") {
		t.Fatalf("query cursor overrode the Last-Event-ID header (replayed seen event):\n%s", body)
	}
}

// Mid-stream relock (a session losing reveal while its SSE stream is live, so
// delivery stops mid-stream) is NOT covered here: the existing harness captures
// the LibraryFilter once at handler entry (eventStream reads
// scope.LibraryFilter() before the loop) and collectSSE buffers the whole
// response to a httptest.NewRecorder before any assertion runs. There is no
// seam to mutate the session's reveal state during an in-flight request. Relock
// is enforced by re-deriving the filter on the NEXT connection, which the
// reveal-by-filter tests above already cover. Driving an actual mid-stream
// relock would require a live server with a controllable reveal clock and a
// reader interleaved with a store mutation — out of scope for this unit harness.
// Flagged for the integrator.

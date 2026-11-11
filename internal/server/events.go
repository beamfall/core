package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// EventStore is the SSE surface's slice of the store (interface at the
// consumer, ADR-0016): the append-only outbox read, the reveal-filtered Media
// lookup that gates create/update events, and the Library-visibility check that
// gates deletion events (whose Media row is already gone, so a live read would
// drop them — see eventPermitted).
type EventStore interface {
	ReadEventOutbox(ctx context.Context, afterSeq int64, limit int) ([]store.OutboxEvent, error)
	Media(ctx context.Context, id store.MediaID, filter store.LibraryFilter) (store.Media, error)
	LibraryVisible(ctx context.Context, id store.LibraryID, filter store.LibraryFilter) (bool, error)
}

// eventPollInterval is how often a connected SSE reader checks the outbox for
// new events once it has drained the backlog. Short enough to feel realtime,
// long enough that an idle stream costs almost nothing.
const eventPollInterval = 250 * time.Millisecond

// eventBatchLimit bounds how many outbox rows one poll reads, so a reader that
// reconnects far behind the tail catches up in bounded steps instead of one
// unbounded query.
const eventBatchLimit = 256

// eventKeepaliveInterval is how long a stream may go without writing before it
// sends an SSE comment frame. Idle streams through the relay or an operator's
// reverse proxy are otherwise dropped silently, and the failed write is how the
// handler learns its peer is gone. Polling is tick-granular, so 10 s keeps the
// observed gap under 15 s.
const eventKeepaliveInterval = 10 * time.Second

// eventScopeRecheckInterval bounds how long a stream trusts its cached Scope
// while idle. The Scope is always re-derived before a batch of events is
// filtered (see eventStream), so this only bounds how long a revoked or expired
// session's idle stream stays open without delivering anything.
const eventScopeRecheckInterval = 5 * time.Second

// eventStreamsPerPrincipal caps concurrently open streams per session, API
// token or scoped key.
const eventStreamsPerPrincipal = 8

type beforeEventEmit func(context.Context) (store.ProfileID, store.LibraryFilter, error)

// eventStreamState tracks open event streams for the per-principal cap and
// the open-streams gauge. The duration and resolver fields are test seams; zero
// values mean the package defaults and auth.Service.ScopeFromRequest.
type eventStreamState struct {
	mu        sync.Mutex
	perKey    map[string]int
	open      int
	keepalive time.Duration
	recheck   time.Duration
	resolve   func(context.Context, *http.Request) (auth.Scope, error)
}

// acquire reserves a stream slot for key, reporting false when key already
// holds limit open streams.
func (e *eventStreamState) acquire(key string, limit int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.perKey == nil {
		e.perKey = map[string]int{}
	}
	if e.perKey[key] >= limit {
		return false
	}
	e.perKey[key]++
	e.open++
	return true
}

func (e *eventStreamState) release(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.perKey[key] <= 1 {
		delete(e.perKey, key)
	} else {
		e.perKey[key]--
	}
	e.open--
}

// openStreams is the number of currently open event streams.
func (e *eventStreamState) openStreams() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.open
}

// eventStreamKey names the principal a stream counts against. Each credential
// kind gets its own namespace so ids cannot collide across kinds.
func eventStreamKey(scope auth.Scope) string {
	switch {
	case scope.APIKeyID != "":
		return "key:" + scope.APIKeyID
	case scope.APITokenID != "":
		return "token:" + scope.APITokenID
	default:
		return "session:" + string(scope.SessionID)
	}
}

func (s *Server) eventScope(ctx context.Context, r *http.Request) (auth.Scope, error) {
	if s.eventStreams.resolve != nil {
		return s.eventStreams.resolve(ctx, r)
	}
	return s.auth.ScopeFromRequest(ctx, r)
}

func (s *Server) eventKeepalive() time.Duration {
	if s.eventStreams.keepalive > 0 {
		return s.eventStreams.keepalive
	}
	return eventKeepaliveInterval
}

func (s *Server) eventScopeRecheck() time.Duration {
	if s.eventStreams.recheck > 0 {
		return s.eventStreams.recheck
	}
	return eventScopeRecheckInterval
}

// SetEvents wires the SSE surface's store. The /api/events route is always
// mounted (the Reveal registry's completeness invariant — no surface escapes
// it); without a wired store the handler reports the stream unavailable rather
// than serving an unfiltered firehose.
func (s *Server) SetEvents(events EventStore) {
	s.events = events
}

// events streams the reveal-filtered event outbox as Server-Sent Events.
// It is an EventFanout Reveal surface: every Media-scoped event is
// resolved through the caller's LibraryFilter before it is written, so an
// event for restricted Media never reaches an unrevealed session — the same
// reveal boundary the JSON read surfaces enforce, applied to a long-lived
// delivery channel. Job-scoped events carry no library content (a job status
// is not catalog content) and are always delivered.
//
// The stream first drains the backlog the client has not seen (cursor via the
// Last-Event-ID header or the ?after= query), then tails for new events until
// the client disconnects.
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request, beforeEmit beforeEventEmit, cacheControl string) {
	// Gate the handshake: an invalid session is rejected with a 401 before any
	// stream headers are written. The resolved Scope seeds the loop's cache; the
	// loop re-derives it before filtering any events, so reveal/revocation/expiry
	// changes still take effect before the next delivery.
	scope, err := s.eventScope(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	// Per-route scope enforcement: a scoped developer key must
	// carry events-subscribe to open the SSE stream. Session- and admin-token-
	// authenticated callers carry no API scopes and are unaffected — their
	// existing session/Reveal gating is the whole authorization for them.
	if scope.IsScopedKey() && !scope.HasAPIScope(store.ScopeEventsSubscribe) {
		writeError(w, r, http.StatusForbidden, "API key missing required scope: events-subscribe")
		return
	}
	if s.events == nil {
		writeError(w, r, http.StatusServiceUnavailable, "event stream unavailable")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// No streaming transport (should not happen on the production server);
		// fail loudly rather than buffer the whole stream.
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	key := eventStreamKey(scope)
	if !s.eventStreams.acquire(key, eventStreamsPerPrincipal) {
		writeError(w, r, http.StatusTooManyRequests, "too many open event streams for this session")
		return
	}
	defer s.eventStreams.release(key)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	cursor := resumeCursor(r)
	ctx := r.Context()
	ticker := time.NewTicker(eventPollInterval)
	defer ticker.Stop()

	keepalive := s.eventKeepalive()
	recheck := s.eventScopeRecheck()
	lastWrite := time.Now()
	scopeCheckedAt := lastWrite
	// fresh marks a Scope derived during the current poll: the handshake's for
	// the first poll, or the periodic re-check's. Any later poll re-derives
	// before it filters events.
	fresh := true
	refreshScope := func() error {
		next, err := s.eventScope(ctx, r)
		if err != nil {
			return err
		}
		scope, scopeCheckedAt, fresh = next, time.Now(), true
		return nil
	}
	// currentScope is what drainEvents filters with. Re-deriving here, only when
	// the outbox has something to deliver, keeps the ADR-0004 Reveal contract on
	// this long-lived transport — a relock, revocation, expiry or
	// access-schedule change is seen before the next event is written — while
	// an idle poll costs one outbox read. A session that can no longer resolve
	// a Scope ends the stream.
	currentScope := func() (store.ProfileID, store.LibraryFilter, error) {
		if !fresh {
			if err := refreshScope(); err != nil {
				return "", store.LibraryFilter{}, err
			}
		}
		return scope.ProfileID, scope.LibraryFilter(), nil
	}

	for {
		// An idle stream still re-checks its Scope periodically, so a revoked
		// or expired session's stream ends rather than polling forever.
		if time.Since(scopeCheckedAt) >= recheck {
			if err := refreshScope(); err != nil {
				return
			}
		}

		next, wrote, err := s.drainEventsWith(ctx, w, flusher, cursor, beforeEmit, currentScope)
		fresh = false
		if err != nil {
			var readErr *eventOutboxReadError
			if errors.As(err, &readErr) && !errors.Is(readErr.err, context.Canceled) && !errors.Is(readErr.err, context.DeadlineExceeded) {
				slog.ErrorContext(ctx, "event stream outbox read failed", "handler", "/api/events", "cursor", cursor, "error", err)
			}
			return // store error or client gone: end the stream
		}
		cursor = next
		if wrote {
			lastWrite = time.Now()
		} else if time.Since(lastWrite) >= keepalive {
			if err := writeKeepalive(w, flusher); err != nil {
				return // peer gone: release the handler
			}
			lastWrite = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// drainEvents writes every currently-pending, reveal-permitted event after
// cursor and returns the new cursor. Filtered-out events still advance the
// cursor (so a relocked session does not re-examine them forever) but are never
// written — a hidden event is indistinguishable from no event.
func (s *Server) drainEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, profileID store.ProfileID, filter store.LibraryFilter, cursor int64, beforeEmit beforeEventEmit) (int64, error) {
	next, _, err := s.drainEventsWith(ctx, w, flusher, cursor, beforeEmit, func() (store.ProfileID, store.LibraryFilter, error) {
		return profileID, filter, nil
	})
	return next, err
}

// drainEventsWith is drainEvents with the subscriber's Profile and filter
// resolved lazily: scope is called once, and only when the outbox read finds
// something to act on, so an empty poll costs exactly one store read. It also
// reports whether anything was written.
func (s *Server) drainEventsWith(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, cursor int64, beforeEmit beforeEventEmit, scope func() (store.ProfileID, store.LibraryFilter, error)) (int64, bool, error) {
	wrote := false
	resolved := false
	var profileID store.ProfileID
	var filter store.LibraryFilter
	resolve := func() error {
		if resolved {
			return nil
		}
		var err error
		profileID, filter, err = scope()
		resolved = err == nil
		return err
	}
	for {
		events, err := s.events.ReadEventOutbox(ctx, cursor, eventBatchLimit)
		if err != nil {
			if errors.Is(err, store.ErrEventCursorTooOld) {
				if err := resolve(); err != nil {
					return cursor, wrote, err
				}
				if beforeEmit != nil {
					if _, _, err := beforeEmit(ctx); err != nil {
						return cursor, wrote, err
					}
				}
				if err := writeFullRefreshEvent(w); err != nil {
					return cursor, wrote, err
				}
				flusher.Flush()
				// The client must replace its local state before it can resume.
				// End this connection after the one-shot instruction; retaining the
				// stale cursor would emit the same full-refresh event every poll.
				return cursor, true, store.ErrEventCursorTooOld
			}
			return cursor, wrote, &eventOutboxReadError{err: err}
		}
		if len(events) == 0 {
			return cursor, wrote, nil
		}
		if err := resolve(); err != nil {
			return cursor, wrote, err
		}
		for _, event := range events {
			cursor = event.Seq
			currentProfileID := profileID
			currentFilter := filter
			if beforeEmit != nil {
				currentProfileID, currentFilter, err = beforeEmit(ctx)
				if err != nil {
					return cursor, wrote, err
				}
			}
			if !s.eventPermitted(ctx, event, currentProfileID, currentFilter) {
				continue
			}
			if err := writeSSEEvent(w, event); err != nil {
				return cursor, wrote, err
			}
			wrote = true
		}
		flusher.Flush()
		if len(events) < eventBatchLimit {
			return cursor, wrote, nil
		}
	}
}

type eventOutboxReadError struct {
	err error
}

func (e *eventOutboxReadError) Error() string {
	return "event stream outbox read failed: " + e.err.Error()
}

func (e *eventOutboxReadError) Unwrap() error {
	return e.err
}

// eventPermitted reports whether event may reach a session with this filter.
// Every non-Media aggregate carries no catalog content and is always permitted.
//
// Media-scoped events split by kind because a deletion's Media row is gone:
//   - media.deleted is gated by the deleted Media's Library (carried in the
//     event payload, store-written): the event reaches a session only when that
//     Library is visible under the filter (its AllowedIDs allow-list AND the
//     restricted-Library reveal guard). A live Media read here would resolve the
//     now-trashed row to not-found and silently drop every deletion. Filtering by
//     Library preserves Reveal — a deletion in a restricted Library never reaches
//     an unrevealed session — without re-reading the gone row, and an empty or
//     unknown LibraryID fails closed (LibraryVisible returns false), so the
//     deletion stream is never an existence oracle for a hidden Library.
//   - every other Media event (created/updated) is gated through the reveal-
//     filtered live Media read: a hidden or unknown id both resolve to not-found,
//     so the stream cannot be used as an existence oracle for hidden Media.
//
// Session-control events are the cross-device transport channel:
// a command one device issued against another's session. They are gated on TWO
// axes, both fail-closed:
//   - Profile: the event reaches a subscriber only when the subscriber's
//     Profile equals the target session's Profile (carried in the payload,
//     store-written). A different-Profile subscriber — even on the same SSE
//     stream — never observes the command, so cross-device control can never
//     cross a Profile boundary (the hard same-Profile constraint).
//   - Reveal: when the command names a Media (skip/pause/resume of an active
//     playback), that Media is reveal-filtered through the subscriber's live
//     filter exactly like a created/updated event — a command for restricted
//     Media never reaches an unrevealed subscriber, so the transport channel is
//     not a reveal side-channel. A command with no Media (none resolved at issue
//     time) skips the Media read and rides only the Profile gate.
func (s *Server) eventPermitted(ctx context.Context, event store.OutboxEvent, profileID store.ProfileID, filter store.LibraryFilter) bool {
	if event.AggregateType == store.OutboxAggregateSession {
		var payload store.SessionControlPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false // malformed payload — fail closed, never over-deliver
		}
		// Same-Profile gate: an empty subscriber Profile (admin token / scoped
		// key without a Profile binding) never matches, so a non-Profile context
		// is never delivered a session-control command.
		if profileID == "" || payload.ProfileID != profileID {
			return false
		}
		if payload.MediaID == "" {
			return true
		}
		if _, err := s.events.Media(ctx, payload.MediaID, filter); err != nil {
			return false
		}
		return true
	}
	if event.AggregateType == store.OutboxAggregateGroup {
		// Group Conductor sync signals are Profile-gated only — the
		// group is same-Profile by construction (formed within one Profile, every
		// member same-Profile) and the signal carries a queue position/anchor, not
		// a Media identity to reveal-filter. A different-Profile subscriber — or a
		// non-Profile context (admin token / unbound scoped key) — never observes
		// the signal, so multi-room sync can never cross a Profile boundary. This
		// is the §7 same-Profile gate the §8 channel reuses, fail-closed.
		var payload store.GroupSyncPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false // malformed payload — fail closed, never over-deliver
		}
		return profileID != "" && payload.ProfileID == profileID
	}
	if event.AggregateType == store.OutboxAggregateExtension {
		// Parental extension-request outcome (ADR-0039 §2.3 step 6) is Profile-gated
		// only — it names no Media (invariant #5: the payload carries only the
		// request id, the managed Profile, and the resolved status), so the Profile
		// gate is the whole delivery authorization. A different-Profile subscriber —
		// or a non-Profile context (admin token / unbound scoped key) — never
		// observes another Profile's extension outcome, fail-closed.
		var payload store.ExtensionEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false // malformed payload — fail closed, never over-deliver
		}
		return profileID != "" && payload.ProfileID == profileID
	}
	if event.AggregateType == store.OutboxAggregateInvalidation {
		// Server-initiated client-cache invalidation is
		// Profile-gated only — it names no Media (invariant #5: the payload carries
		// the affected Profile and a content-free Reason hint), so the Profile gate
		// is the whole delivery authorization. Without this explicit branch the
		// catch-all below would deliver it to EVERY session (over-delivery), so the
		// gate is load-bearing: a different-Profile subscriber — or a non-Profile
		// context (admin token / unbound scoped key) — never observes another
		// Profile's invalidation, fail-closed.
		var payload store.InvalidationPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false // malformed payload — fail closed, never over-deliver
		}
		return profileID != "" && payload.ProfileID == profileID
	}
	if event.AggregateType != store.OutboxAggregateMedia {
		return true
	}
	if event.Kind == store.MediaEventDeleted {
		var payload store.MediaDeletedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false // malformed payload — fail closed, never over-deliver
		}
		visible, err := s.events.LibraryVisible(ctx, payload.LibraryID, filter)
		if err != nil {
			return false
		}
		return visible
	}
	if _, err := s.events.Media(ctx, store.MediaID(event.AggregateID), filter); err != nil {
		return false
	}
	return true
}

// resumeCursor reads the reconnection cursor: the SSE standard Last-Event-ID
// header (set automatically by EventSource on reconnect) wins, then the
// ?after= query for first connections. An unparsable value means "from the
// start" (0), never an error.
func resumeCursor(r *http.Request) int64 {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("after")
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 {
		return 0
	}
	return seq
}

// writeSSEEvent serializes one outbox event in the text/event-stream framing:
// an id: line (the resume cursor), an event: type, and a single JSON data:
// line. The payload is the event's own JSON, wrapped so the kind and subject
// travel with it.
type sseEventEnvelope struct {
	Kind          string          `json:"kind"`
	AggregateType string          `json:"aggregateType"`
	AggregateID   string          `json:"aggregateID"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Payload       json.RawMessage `json:"payload"`
}

// writeKeepalive writes an SSE comment frame, which every EventSource client
// ignores, and flushes it. The flush error is what reveals a peer that is gone;
// a writer chain without a reportable flush falls back to the plain Flusher.
func writeKeepalive(w http.ResponseWriter, flusher http.Flusher) error {
	if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
		return err
	}
	err := http.NewResponseController(w).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		flusher.Flush()
		return nil
	}
	return err
}

func writeFullRefreshEvent(w http.ResponseWriter) error {
	if _, err := w.Write([]byte("event: client.fullRefresh\n")); err != nil {
		return err
	}
	_, err := w.Write([]byte("data: {\"reason\":\"cursor-too-old\"}\n\n"))
	return err
}

func writeSSEEvent(w http.ResponseWriter, event store.OutboxEvent) error {
	payload := event.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	data, err := json.Marshal(sseEventEnvelope{
		Kind:          event.Kind,
		AggregateType: event.AggregateType,
		AggregateID:   event.AggregateID,
		OccurredAt:    event.OccurredAt,
		Payload:       json.RawMessage(payload),
	})
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("id: " + strconv.FormatInt(event.Seq, 10) + "\n")); err != nil {
		return err
	}
	if _, err := w.Write([]byte("event: " + event.Kind + "\n")); err != nil {
		return err
	}
	if _, err := w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n\n"))
	return err
}

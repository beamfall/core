package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

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

type beforeEventEmit func(context.Context) (store.ProfileID, store.LibraryFilter, error)

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
	// stream headers are written. The resolved Scope is checked once for the
	// events-subscribe API scope but otherwise not kept — the
	// event loop below re-derives it every poll so reveal/revocation/expiry
	// changes take effect immediately, never against this connect-time snapshot.
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	cursor := resumeCursor(r)
	ctx := r.Context()
	ticker := time.NewTicker(eventPollInterval)
	defer ticker.Stop()

	for {
		// Re-derive the Scope (and thus the LibraryFilter) every poll so a
		// relock, revocation, expiry, or access-schedule change takes effect
		// immediately on this long-lived transport (ADR-0004 Reveal contract):
		// ScopeFromRequest reads the live session/device/Profile each call. A
		// session that can no longer resolve a Scope (expired/revoked/out of
		// schedule) ends the stream cleanly rather than keeping a stale filter.
		scope, err := s.auth.ScopeFromRequest(ctx, r)
		if err != nil {
			return
		}
		filter := scope.LibraryFilter()

		next, err := s.drainEvents(ctx, w, flusher, scope.ProfileID, filter, cursor, beforeEmit)
		if err != nil {
			var readErr *eventOutboxReadError
			if errors.As(err, &readErr) && !errors.Is(readErr.err, context.Canceled) && !errors.Is(readErr.err, context.DeadlineExceeded) {
				slog.ErrorContext(ctx, "event stream outbox read failed", "handler", "/api/events", "cursor", cursor, "error", err)
			}
			return // store error or client gone: end the stream
		}
		cursor = next
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
	for {
		events, err := s.events.ReadEventOutbox(ctx, cursor, eventBatchLimit)
		if err != nil {
			if errors.Is(err, store.ErrEventCursorTooOld) {
				if beforeEmit != nil {
					if _, _, err := beforeEmit(ctx); err != nil {
						return cursor, err
					}
				}
				if err := writeFullRefreshEvent(w); err != nil {
					return cursor, err
				}
				flusher.Flush()
				// The client must replace its local state before it can resume.
				// End this connection after the one-shot instruction; retaining the
				// stale cursor would emit the same full-refresh event every poll.
				return cursor, store.ErrEventCursorTooOld
			}
			return cursor, &eventOutboxReadError{err: err}
		}
		if len(events) == 0 {
			return cursor, nil
		}
		for _, event := range events {
			cursor = event.Seq
			currentProfileID := profileID
			currentFilter := filter
			if beforeEmit != nil {
				currentProfileID, currentFilter, err = beforeEmit(ctx)
				if err != nil {
					return cursor, err
				}
			}
			if !s.eventPermitted(ctx, event, currentProfileID, currentFilter) {
				continue
			}
			if err := writeSSEEvent(w, event); err != nil {
				return cursor, err
			}
		}
		flusher.Flush()
		if len(events) < eventBatchLimit {
			return cursor, nil
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

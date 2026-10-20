package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// EngagementEventID is a stable engagement-event identifier (server-issued).
type EngagementEventID string

// EngagementEvent is one row of the ADR-0005 §1 engagement event log: a
// timestamped, Profile-scoped record of a single engagement, riding the
// append-only `engagement_event` table. It is the raw log the materialized
// playback_progress projection is derived alongside — NOT a
// replacement for it.
//
// Kind is a PLUGIN-DECLARED custom event kind: an opaque string the owning
// plugin defines and core only ever groups or compares, never parses or names
// (invariant #1). The event targets a Media (MediaID) and/or an Entity
// (EntityID) the same way the existing engagement rows reference their targets;
// at least one is required.
type EngagementEvent struct {
	ID        EngagementEventID
	ProfileID ProfileID
	// MediaID is the Media target, or "" when the event targets only an Entity.
	MediaID MediaID
	// EntityID is the optional Entity target (ADR-0027 entity aggregates), or ""
	// when the event targets only a Media.
	EntityID EntityID
	// Kind is the plugin-declared event kind (opaque to core).
	Kind string
	// Value is a small optional signed integer the kind owns — e.g. a +1
	// increment or a -1 "remove last" correction. A running aggregate is the SUM
	// of Value over the kind. Defaults to 0 for kinds that carry no count.
	Value int
	// Payload is an optional small opaque string for kinds that carry more than a
	// count; "" when unused. Stored verbatim, never parsed by core.
	Payload string
	// OccurredAt is the true instant the engagement happened. Defaults to now on
	// append when zero.
	OccurredAt time.Time
	// CreatedAt is when the row was appended (server clock).
	CreatedAt time.Time
}

// EngagementEventAggregate is the derived rollup of an engagement-event kind for
// one Profile+target: Count is the number of events, Sum is the SUM of their
// Value (the running count for an increment/correction kind). LastOccurredAt is
// the most recent event's true instant, zero when there are no events.
type EngagementEventAggregate struct {
	Count          int
	Sum            int
	LastOccurredAt time.Time
}

// EngagementEventTarget identifies the target of an engagement-event read. At
// least one of MediaID / EntityID is set; both may be set to scope to events
// that named both. An all-empty target reads across all of the Profile's events
// of the kind (the profile+kind index path).
type EngagementEventTarget struct {
	MediaID  MediaID
	EntityID EntityID
}

// AppendEngagementEvent appends one engagement event to the ADR-0005 §1 log.
// The write is append-only: it only ever INSERTs. A correction is a separate
// compensating event (e.g. Value -1 cancelling a prior +1), never an UPDATE or
// DELETE of history — so the full timeline is always recoverable and an
// aggregate is the SUM over the kind.
//
// The event is Profile-scoped (ProfileID required) and must name at least one
// target (a Media and/or an Entity), mirroring how the existing engagement rows
// reference targets. Kind is the plugin-declared event kind and must be
// non-empty; core stores it verbatim. The id is server-issued; OccurredAt
// defaults to now when zero.
func (s *Store) AppendEngagementEvent(ctx context.Context, event EngagementEvent) (EngagementEvent, error) {
	// Authority face: authoring engagement is main-only. Attached
	// refuses at the store seam — every caller (graph counters, plugins) is gated.
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return EngagementEvent{}, err
	}
	if event.ProfileID == "" {
		return EngagementEvent{}, ValidationError("profile ID is required")
	}
	if event.MediaID == "" && event.EntityID == "" {
		return EngagementEvent{}, ValidationError("engagement event needs a media or entity target")
	}
	if event.Kind == "" {
		return EngagementEvent{}, ValidationError("engagement event kind is required")
	}
	if event.Kind == playbackProjectionEventKind {
		return EngagementEvent{}, ValidationError("engagement event kind is reserved by core")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return EngagementEvent{}, err
	}

	id, err := randomEngagementEventID()
	if err != nil {
		return EngagementEvent{}, err
	}
	event.ID = id
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	event.CreatedAt = time.Now().UTC()

	if _, err := s.eventsW.ExecContext(ctx, `
		INSERT INTO engagement_event(id, profile_id, media_id, entity_id, kind, value, payload, occurred_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		event.ID, event.ProfileID,
		nullString(string(event.MediaID)), nullString(string(event.EntityID)),
		event.Kind, event.Value, nullString(event.Payload),
		timeToMillis(event.OccurredAt), timeToMillis(event.CreatedAt),
	); err != nil {
		return EngagementEvent{}, err
	}
	return event, nil
}

// EngagementEventsFor lists a Profile's events of one kind for a target, newest
// first. It is Profile-scoped — a Profile only ever sees its own events (the
// reads filter on profile_id like every other engagement read). The full
// history is returned, including compensating correction rows, so a caller can
// replay the timeline rather than only see the net aggregate.
func (s *Store) EngagementEventsFor(ctx context.Context, profileID ProfileID, target EngagementEventTarget, kind string) ([]EngagementEvent, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	if kind == "" {
		return nil, ValidationError("engagement event kind is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}

	query := `
		SELECT id, profile_id, media_id, entity_id, kind, value, payload, occurred_at_ms, created_at_ms
		FROM engagement_event
		WHERE profile_id = ? AND kind = ?`
	args := []any{profileID, kind}
	if target.MediaID != "" {
		query += " AND media_id = ?"
		args = append(args, target.MediaID)
	}
	if target.EntityID != "" {
		query += " AND entity_id = ?"
		args = append(args, target.EntityID)
	}
	query += " ORDER BY occurred_at_ms DESC, id DESC"

	rows, err := s.eventsR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []EngagementEvent{}
	for rows.Next() {
		var (
			ev       EngagementEvent
			mediaID  sql.NullString
			entityID sql.NullString
			payload  sql.NullString
			occurred int64
			created  int64
		)
		if err := rows.Scan(&ev.ID, &ev.ProfileID, &mediaID, &entityID,
			&ev.Kind, &ev.Value, &payload, &occurred, &created); err != nil {
			return nil, err
		}
		ev.MediaID = MediaID(mediaID.String)
		ev.EntityID = EntityID(entityID.String)
		ev.Payload = payload.String
		ev.OccurredAt = millisToTime(occurred)
		ev.CreatedAt = millisToTime(created)
		events = append(events, ev)
	}
	return events, rows.Err()
}

// EngagementEventAggregateFor is the derived rollup of one kind for a
// Profile+target: the event Count, the SUM of their Value (the running count
// for an increment/correction kind — ADR-0005 §1 "counts are derived, never
// stored"), and the most recent event time. It reads only the Profile's own
// events (Profile-scoped). A target with no events yields a zero aggregate.
func (s *Store) EngagementEventAggregateFor(ctx context.Context, profileID ProfileID, target EngagementEventTarget, kind string) (EngagementEventAggregate, error) {
	if profileID == "" {
		return EngagementEventAggregate{}, ValidationError("profile ID is required")
	}
	if kind == "" {
		return EngagementEventAggregate{}, ValidationError("engagement event kind is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return EngagementEventAggregate{}, err
	}

	query := `
		SELECT COUNT(*), COALESCE(SUM(value), 0), COALESCE(MAX(occurred_at_ms), 0)
		FROM engagement_event
		WHERE profile_id = ? AND kind = ?`
	args := []any{profileID, kind}
	if target.MediaID != "" {
		query += " AND media_id = ?"
		args = append(args, target.MediaID)
	}
	if target.EntityID != "" {
		query += " AND entity_id = ?"
		args = append(args, target.EntityID)
	}

	var agg EngagementEventAggregate
	var lastOccurred int64
	if err := s.eventsR.QueryRowContext(ctx, query, args...).Scan(&agg.Count, &agg.Sum, &lastOccurred); err != nil {
		return EngagementEventAggregate{}, err
	}
	agg.LastOccurredAt = millisToTime(lastOccurred)
	return agg, nil
}

func randomEngagementEventID() (EngagementEventID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return EngagementEventID("engevent-" + hex.EncodeToString(raw[:])), nil
}

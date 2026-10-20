package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// OutboxEvent is one row of the append-only event outbox (the realtime spine
// for the SSE event stream, ADR-0040). Seq is the monotonic cursor a long-lived
// reader resumes from. AggregateType/AggregateID identify the subject: "job"
// events carry no content (a job status is not library content), while "media"
// events carry the affected Media id so the SSE surface can reveal-filter them
// (a restricted-Media event never reaches an unrevealed session).
type OutboxEvent struct {
	Seq           int64
	OccurredAt    time.Time
	Kind          string
	AggregateType string
	AggregateID   string
	Payload       []byte
}

// OutboxAggregateMedia is the AggregateType for events whose AggregateID is a
// Media id; the SSE surface resolves these through the caller's reveal filter.
const OutboxAggregateMedia = "media"

// ErrEventCursorTooOld means the caller's outbox cursor predates the retained
// tail; clients must discard local incremental state and perform a full refresh.
var ErrEventCursorTooOld = errors.New("event outbox cursor too old")

// OutboxAggregateSession is the AggregateType for cross-device session-control
// events: a transport command (skip/pause/resume) one device
// issues against another device's active session in the SAME Profile. The
// AggregateID is the TARGET session id. Unlike a "media" event, the SSE surface
// gates these by BOTH the subscriber's Profile (a session-control event reaches
// only same-Profile subscribers — the payload carries the target Profile) AND
// the target Media's Reveal (a command for restricted Media never reaches an
// unrevealed subscriber). The playing/target device receives the command on its
// existing event channel and remains the transport executor; the controlling
// device is a stateless remote (ADR-0035 §7 — "not Beam").
const OutboxAggregateSession = "session"

// ReadEventOutbox returns up to limit outbox events with Seq greater than
// afterSeq, oldest first. It is the read half of the realtime event spine: a
// long-lived SSE reader polls with the last Seq it delivered. Read-only;
// reveal filtering is the caller's concern (a "media" event is filtered through
// the session's LibraryFilter at delivery, never here — this read is unscoped
// like ListJobs, and the SSE handler is the reveal boundary).
func (s *Store) ReadEventOutbox(ctx context.Context, afterSeq int64, limit int) ([]OutboxEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if afterSeq > 0 {
		var minSeq sql.NullInt64
		if err := s.eventsR.QueryRowContext(ctx, "SELECT MIN(seq) FROM event_outbox").Scan(&minSeq); err != nil {
			return nil, err
		}
		if minSeq.Valid && afterSeq < minSeq.Int64-1 {
			return nil, ErrEventCursorTooOld
		}
	}
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT seq, occurred_at_ms, kind, aggregate_type, aggregate_id, payload_json
		FROM event_outbox
		WHERE seq > ?
		ORDER BY seq
		LIMIT ?
	`, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []OutboxEvent
	for rows.Next() {
		var event OutboxEvent
		var occurred int64
		var payload string
		if err := rows.Scan(&event.Seq, &occurred, &event.Kind, &event.AggregateType, &event.AggregateID, &payload); err != nil {
			return nil, err
		}
		event.OccurredAt = millisToTime(occurred)
		event.Payload = []byte(payload)
		events = append(events, event)
	}
	return events, rows.Err()
}

// ReadEventOutboxFiltered returns the next batch of outbox events after afterSeq
// that an Integration Worker holding event-subscribe may observe,
// Reveal-filtered through the worker's bound LibraryFilter. It reuses the same
// Reveal boundary the SSE surface enforces (server.eventPermitted), restricted to
// the worker-appropriate subset:
//
//   - Media events are reveal-gated: media.deleted by its payload Library (the
//     row is gone) via LibraryVisible; media.created/updated via a filtered Media
//     read. A hidden or unknown id fails closed, so the stream is never an
//     existence oracle for hidden content.
//   - Per-Profile device aggregates (session-control, group-sync, extension
//     outcomes) are DROPPED: those are device-subscriber signals gated on a
//     subscriber Profile a worker does not carry. A worker is not a device and
//     never executes cross-device transport, so it never receives them.
//   - Other content-free aggregates (e.g. library/job lifecycle) pass through.
//
// The cursor advances past every event (filtered or delivered), like the SSE
// drain, so a worker never re-examines a dropped event.
func (s *Store) ReadEventOutboxFiltered(ctx context.Context, afterSeq int64, limit int, filter LibraryFilter) ([]OutboxEvent, error) {
	events, err := s.ReadEventOutbox(ctx, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]OutboxEvent, 0, len(events))
	for _, event := range events {
		permitted, err := s.workerEventPermitted(ctx, event, filter)
		if err != nil {
			return nil, err
		}
		if permitted {
			out = append(out, event)
		}
	}
	return out, nil
}

// workerEventPermitted is the worker subset of server.eventPermitted. Per-Profile
// device aggregates are never delivered to a worker (fail closed); Media events
// are reveal-gated; everything else content-free passes.
func (s *Store) workerEventPermitted(ctx context.Context, event OutboxEvent, filter LibraryFilter) (bool, error) {
	switch event.AggregateType {
	case OutboxAggregateSession, OutboxAggregateGroup, OutboxAggregateExtension:
		// Device-subscriber signals gated on a Profile a worker does not carry.
		return false, nil
	case OutboxAggregateMedia:
		if event.Kind == MediaEventDeleted {
			var payload MediaDeletedPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return false, nil // malformed — fail closed
			}
			visible, err := s.LibraryVisible(ctx, payload.LibraryID, filter)
			if err != nil {
				return false, err
			}
			return visible, nil
		}
		if _, err := s.Media(ctx, MediaID(event.AggregateID), filter); err != nil {
			// Hidden or unknown id resolves to not-found: drop (no existence leak).
			return false, nil
		}
		return true, nil
	default:
		// Content-free aggregate (library/job lifecycle): no Reveal concern.
		return true, nil
	}
}

// Media outbox event kinds. The SSE surface delivers these to every
// session that can currently see the named Media; clients refetch the affected
// item (created/updated) or drop it (deleted) on receipt.
const (
	// MediaEventCreated fires when a scan first imports a Media row.
	MediaEventCreated = "media.created"
	// MediaEventUpdated fires when an existing Media's descriptive metadata or
	// scanned fields change.
	MediaEventUpdated = "media.updated"
	// MediaEventDeleted fires when a Media is trashed (soft-delete) or merged
	// away. A live Media read at delivery would resolve the now-trashed row to
	// not-found and drop the event, so a deletion event carries its Media's
	// LibraryID in the payload (MediaDeletedPayload) and the delivery side
	// (server.eventPermitted) reveal-filters it by that Library's restriction
	// instead of re-reading the gone row — preserving Reveal without an existence
	// leak (the LibraryID is store-written, never client input).
	MediaEventDeleted = "media.deleted"
)

// MediaDeletedPayload is the JSON body of a media.deleted outbox event. It
// carries the deleted Media's LibraryID so the SSE delivery side can reveal-
// filter the event by that Library's restriction without a live Media read (the
// row is trashed/gone by the time the event is delivered). The id is the same
// one stored as the deletion's content-identity; the field name "libraryID"
// matches the camelCase wire shape the SSE surface emits.
type MediaDeletedPayload struct {
	LibraryID LibraryID `json:"libraryID"`
}

// mediaDeletedPayload marshals a deletion event's library reference. The library
// id is always known to the deleting write path (it resolved or owned the Media
// before trashing it), so this never fails on real input; a marshal error
// returns nil, leaving the event payload empty (the delivery side then fails
// closed, dropping the event rather than over-delivering).
func mediaDeletedPayload(libraryID LibraryID) []byte {
	raw, err := json.Marshal(MediaDeletedPayload{LibraryID: libraryID})
	if err != nil {
		return nil
	}
	return raw
}

// AppendMediaEvent records a Media-scoped event in the outbox so realtime
// subscribers (the SSE surface, ADR-0040) learn that a Media changed. The event
// is reveal-relevant by construction: it names its Media in AggregateID, and the
// SSE surface delivers it only to sessions that can currently see that Media.
func (s *Store) AppendMediaEvent(ctx context.Context, kind string, mediaID MediaID, payload []byte, now time.Time) error {
	if kind == "" || mediaID == "" {
		return ValidationError("media event kind and media ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.appendMediaEventLocked(ctx, kind, mediaID, payload, now)
}

// appendMediaEventLocked is the lock-free body of AppendMediaEvent. Callers must
// already hold s.mu (read or better) and have passed guardLocked. The store's
// own catalog write paths call this AFTER their catalog.db transaction has
// COMMITTED, while still holding the read lock they acquired for the write.
//
// CROSS-DB ORDERING (deliberate): Media rows live in catalog.db and the
// event_outbox lives in events.db (separate SQLite files, separate pools), so a
// single atomic transaction across both is impossible. We emit the event only
// after the catalog write has committed — never inside the catalog tx — so an
// outbox row can never reference an uncommitted or rolled-back Media. The cost
// is a possible missed event (catalog commits, then the process dies before the
// outbox write): a client misses one live push but the next read/poll is
// correct. We prefer a missed event over a phantom one (an event for a Media
// that was never persisted). This mirrors the seed path (sqlite.go: catalog tx
// commits, then events tx) and is the inverse-safe sibling of the job path,
// where job_event and event_outbox are BOTH in events.db and so commit together
// in one tx (appendJobEventSQL).
func (s *Store) appendMediaEventLocked(ctx context.Context, kind string, mediaID MediaID, payload []byte, now time.Time) error {
	now = normalizeTime(now)
	_, err := s.eventsW.ExecContext(ctx,
		"INSERT INTO event_outbox(occurred_at_ms, kind, aggregate_type, aggregate_id, payload_json) VALUES (?, ?, ?, ?, ?)",
		timeToMillis(now), kind, OutboxAggregateMedia, mediaID, string(normalizeJSON(payload)),
	)
	return err
}

// appendMediaEventsLocked is the batched form of appendMediaEventLocked: it
// records the SAME Kind/Payload/OccurredAt for many Media at once, with one
// multi-row INSERT per bulkWriteChunkSize-sized chunk
// instead of one events-writer statement per Media. It carries the identical
// cross-DB-ordering contract as appendMediaEventLocked (callers must already
// hold s.mu, have passed guardLocked, and call this AFTER their catalog.db
// transaction has committed) — this only changes how many INSERT statements
// N events cost, never their column values or per-row semantics. A row's Seq
// still comes from event_outbox's own autoincrement rowid, assigned in the
// order the rows are listed within each multi-row INSERT, so the emitted
// order matches mediaIDs. mediaIDs empty is a no-op (no statement at all).
func (s *Store) appendMediaEventsLocked(ctx context.Context, kind string, mediaIDs []MediaID, payload []byte, now time.Time) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	now = normalizeTime(now)
	occurred := timeToMillis(now)
	payloadJSON := string(normalizeJSON(payload))
	for start := 0; start < len(mediaIDs); start += bulkWriteChunkSize {
		chunk := mediaIDs[start:min(start+bulkWriteChunkSize, len(mediaIDs))]
		rowPlaceholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*5)
		for i, id := range chunk {
			rowPlaceholders[i] = "(?, ?, ?, ?, ?)"
			args = append(args, occurred, kind, OutboxAggregateMedia, id, payloadJSON)
		}
		//nolint:gosec // G202: values is joined literal "(?, ?, ?, ?, ?)" placeholder groups; all values bound via args.
		if _, err := s.eventsW.ExecContext(ctx, `
			INSERT INTO event_outbox(occurred_at_ms, kind, aggregate_type, aggregate_id, payload_json) VALUES `+strings.Join(rowPlaceholders, ", "),
			args...); err != nil {
			return err
		}
	}
	return nil
}

// Session-control transport command vocabulary. The set is
// closed and generic (invariant #1) — no domain or media-kind verbs: a command
// names a transport axis, not what is playing. Clients ship this vocabulary
// compiled in and ignore an unknown verb, exactly like the Section/Source
// vocabularies. "skip-media" advances to the next/previous Media item;
// "skip-track" advances within the current item's track list (audio session);
// "pause"/"resume" toggle transport. Direction (prev/next) rides in the
// command payload, not a separate verb.
var sessionControlCommands = map[string]bool{
	"skip-media": true,
	"skip-track": true,
	"pause":      true,
	"resume":     true,
}

// SessionControlCommandValid reports whether command is a member of the closed
// session-control transport vocabulary. The graph layer validates the verb
// before authorizing, so an unknown verb is a client error, never an outbox row.
func SessionControlCommandValid(command string) bool {
	return sessionControlCommands[command]
}

// SessionControlKind is the single outbox Kind for every cross-device transport
// command. The specific verb travels in the payload, so one Kind
// covers the whole closed vocabulary and the SSE event:type line is stable.
const SessionControlKind = "session.control"

// SessionControlPayload is the JSON body of a session.control outbox event. It
// carries the issued transport Command, the controlling device's id (so the
// target can attribute the command — never authorize off it), and the
// MediaID/ProfileID the SSE delivery side reveal/Profile-filters on. ProfileID
// and MediaID are store-resolved at issue time from the TARGET session's active
// playback, never client input, so a controller cannot widen delivery past its
// own Profile or probe a hidden Media by spoofing the payload.
type SessionControlPayload struct {
	Command    string    `json:"command"`
	FromDevice DeviceID  `json:"fromDevice,omitempty"`
	ProfileID  ProfileID `json:"profileID"`
	MediaID    MediaID   `json:"mediaID,omitempty"`
}

// AppendSessionControlEvent records a cross-device transport command in the
// outbox. The AggregateID is the TARGET session id; the payload
// carries the verb plus the target's resolved Profile and Media so the SSE
// surface can Profile-gate and reveal-filter delivery WITHOUT a live read at
// delivery time (the issuing graph resolver already authorized same-Profile and
// reveal-visibility; the payload is the store-written record of that decision).
// The command is delivered identically regardless of which device issued it —
// the server is the single processor (ADR-0035 §7 — "processes them identically
// regardless of which authenticated device sent them"); the target/playing
// device, receiving it on its existing channel, remains the transport executor.
func (s *Store) AppendSessionControlEvent(ctx context.Context, target SessionID, payload SessionControlPayload, now time.Time) error {
	raw, err := sessionControlPayloadJSON(target, payload)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return appendSessionControlEventSQL(ctx, s.eventsW, target, raw, now)
}

// AppendSessionControlEventWithActivity records the transport command and its
// activity audit row in one events.db transaction. Use it for control surfaces
// where publishing without the audit row would create an incomplete trail.
func (s *Store) AppendSessionControlEventWithActivity(ctx context.Context, target SessionID, payload SessionControlPayload, activity ActivityEntry, now time.Time) error {
	raw, err := sessionControlPayloadJSON(target, payload)
	if err != nil {
		return err
	}
	if activity.OccurredAt.IsZero() {
		activity.OccurredAt = now
	}
	if err := validateActivityEntry(&activity); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendSessionControlEventSQL(ctx, tx, target, raw, now); err != nil {
		return err
	}
	if err := appendActivitySQL(ctx, tx, activity); err != nil {
		return err
	}
	return tx.Commit()
}

func sessionControlPayloadJSON(target SessionID, payload SessionControlPayload) ([]byte, error) {
	if target == "" {
		return nil, ValidationError("target session is required")
	}
	if !sessionControlCommands[payload.Command] {
		return nil, ValidationError("unknown transport command")
	}
	if payload.ProfileID == "" {
		return nil, ValidationError("session-control event is missing its Profile")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return normalizeJSON(raw), nil
}

func appendSessionControlEventSQL(ctx context.Context, exec sqlExecer, target SessionID, payload []byte, now time.Time) error {
	now = normalizeTime(now)
	_, err := exec.ExecContext(ctx,
		"INSERT INTO event_outbox(occurred_at_ms, kind, aggregate_type, aggregate_id, payload_json) VALUES (?, ?, ?, ?, ?)",
		timeToMillis(now), SessionControlKind, OutboxAggregateSession, target, string(payload),
	)
	return err
}

// OutboxAggregateGroup is the AggregateType for Ambient Group Conductor sync
// signals. The AggregateID is the ephemeral group id. Like a
// session-control event these are Profile-gated at delivery (the payload carries
// the group's Profile, store-written) so a Conductor signal never crosses a
// Profile boundary; UNLIKE a media event they are NOT reveal-filtered on a Media
// id — the signal carries a queue position/anchor, and a group is already
// same-Profile-scoped at formation, so the Profile gate is the whole delivery
// authorization (the §8 sync channel reuses the §7 event substrate, no new
// transport). The Conductor's group membership itself lives in memory (the
// conductor package); the outbox carries only the broadcast, never group state.
const OutboxAggregateGroup = "group"

// GroupSyncKind is the single outbox Kind for every Group Conductor sync signal.
// The signal SHAPE (transition vs drift-correction) travels in the
// payload, so one Kind covers the whole channel and the SSE event:type line is
// stable, mirroring SessionControlKind.
const GroupSyncKind = "group.sync"

// Group Conductor sync-signal shapes. The set is closed and
// generic (invariant #1) — a signal names a sync mechanism, never what is
// playing. Clients ship this vocabulary compiled in and ignore an unknown shape,
// exactly like the Section/Source and session-control vocabularies.
const (
	// GroupSignalTransition is the {item, position, present_at} broadcast every
	// member converts to its own clock and releases at — the start/seek-together
	// signal (ADR-0035 §8 "Transition protocol"). A full drift re-sync (> 500 ms)
	// is also a transition: it re-anchors the member with a fresh present_at.
	GroupSignalTransition = "transition"
	// GroupSignalNudge is a ±0.1 % playback-rate micro-correction for a single
	// member that has drifted within the recoverable window (ADR-0035 §8 "Drift
	// correction"). It targets one member session (Session in the payload) so the
	// member ignores a nudge addressed to a peer.
	GroupSignalNudge = "nudge"
)

var groupSyncSignals = map[string]bool{
	GroupSignalTransition: true,
	GroupSignalNudge:      true,
}

// GroupSyncPayload is the JSON body of a group.sync outbox event.
// ProfileID is the SSE delivery gate (store-written at issue time from the
// group's owning Profile, never client input, so a member cannot widen delivery
// past its own Profile). Signal selects the shape; the remaining fields are
// populated per shape:
//
//   - transition: Item, Position(ms), PresentAt(server NTP, ms) — every member
//     converts PresentAt via its own clock offset and releases at
//     PresentAt − own_render_latency.
//   - nudge: Session (the one member it targets) + Rate (the signed ±0.1 %
//     playback-rate delta); a member ignores a nudge whose Session is not its own.
//
// Time fields are server-clock Unix milliseconds — the wire-stable encoding the
// member pairs with its calibrated clock offset; the conductor package works in
// time.Time/time.Duration and the surface converts at the boundary.
type GroupSyncPayload struct {
	GroupID     string    `json:"groupID"`
	ProfileID   ProfileID `json:"profileID"`
	Signal      string    `json:"signal"`
	Item        MediaID   `json:"item,omitempty"`
	PositionMS  int64     `json:"positionMS,omitempty"`
	PresentAtMS int64     `json:"presentAtMS,omitempty"`
	Session     SessionID `json:"session,omitempty"`
	Rate        float64   `json:"rate,omitempty"`
}

// GroupSyncSignalValid reports whether signal is a member of the closed
// Group-Conductor sync vocabulary. The surface validates the shape before
// appending, so an unknown shape is a programming error, never an outbox row.
func GroupSyncSignalValid(signal string) bool {
	return groupSyncSignals[signal]
}

// OutboxAggregateExtension is the AggregateType for parental extension-request
// outcome events (ADR-0039 §2.3 step 6). The AggregateID is the request id. The
// outcome (approved/denied/expired) is delivered to the managed Profile's active
// session over the SAME /api/events SSE the playback session lifecycle already
// rides. Like a group-sync event it is Profile-gated only — the payload carries
// the managed Profile (store-written from the request, never client input), so a
// different-Profile subscriber never observes another Profile's extension
// outcome. It is NOT reveal-filtered on a Media id: the outcome names no Media
// (the request carries only an opaque trigger source, invariant #5), so the
// Profile gate is the whole delivery authorization, fail-closed.
const OutboxAggregateExtension = "extension"

// ExtensionEventKind is the single outbox Kind for every extension-request
// outcome (ADR-0039 §2.3 step 6). The specific outcome travels in the payload's
// Status, so one Kind covers the whole lifecycle and the SSE event:type line is
// stable, mirroring SessionControlKind and GroupSyncKind.
const ExtensionEventKind = "extension.resolved"

// ExtensionEventPayload is the JSON body of an extension.resolved outbox event.
// RequestID is the opaque handle the managed client round-trips to fetch the
// outcome over the authenticated API (no content rides the event — invariant
// #5). ProfileID is the SSE delivery gate (the managed Profile, store-written at
// issue time, never client input). Status is the resolved lifecycle state
// (approved | denied | expired); GrantMinutes is the granted length on approve.
type ExtensionEventPayload struct {
	RequestID    string    `json:"requestID"`
	ProfileID    ProfileID `json:"profileID"`
	Status       string    `json:"status"`
	GrantMinutes int       `json:"grantMinutes,omitempty"`
}

// AppendExtensionEvent records an extension-request outcome in the outbox so the
// managed Profile's active session learns the result in real time (ADR-0039 §2.3
// step 6 — "delivered to the managed Profile's active Session via a server-sent
// event"). The AggregateID is the request id; the payload carries the managed
// Profile so the SSE surface Profile-gates delivery WITHOUT a live read, the same
// fail-closed gate the §7/§8 channels use. The Profile is set by the resolver
// from the request row, never client input, so an outcome can never cross a
// Profile boundary.
func (s *Store) AppendExtensionEvent(ctx context.Context, payload ExtensionEventPayload, now time.Time) error {
	if payload.RequestID == "" {
		return ValidationError("extension event request id is required")
	}
	if payload.ProfileID == "" {
		return ValidationError("extension event is missing its Profile")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	now = normalizeTime(now)
	_, err = s.eventsW.ExecContext(ctx,
		"INSERT INTO event_outbox(occurred_at_ms, kind, aggregate_type, aggregate_id, payload_json) VALUES (?, ?, ?, ?, ?)",
		timeToMillis(now), ExtensionEventKind, OutboxAggregateExtension, payload.RequestID, string(normalizeJSON(raw)),
	)
	return err
}

// OutboxAggregateInvalidation is the AggregateType for server-initiated
// client-cache invalidation signals (PANEL-17, ADR-0096 "invalidate on Library
// access change"). The AggregateID is the affected Profile id. It is the
// server's way to tell a STILL-CONNECTED session that its Reveal/access ceiling
// changed underneath it — e.g. a manager tightened a managed Profile's access
// schedule — so the client purges OS-owned caches that may now
// surface newly-restricted content (tvOS Top Shelf, CarPlay/Auto metadata, Cast
// receiver, Android Engage rows). Like the extension/group channels it is
// Profile-gated only (the payload carries the affected Profile, store-written):
// a different-Profile subscriber never observes another Profile's invalidation.
// It carries NO Media identity and NO titles (invariant #5) — only the Profile
// (the delivery gate) and a content-free Reason hint — so it is never a reveal
// side-channel. (A device whose own session was REVOKED is NOT reached this way:
// the SSE spine is poll-based, so a revoked device's next poll is auth-rejected
// before it can read any event; that purge is client-side on the auth failure.)
const OutboxAggregateInvalidation = "invalidation"

// InvalidationKind is the single outbox Kind for every client-cache invalidation
// signal. The specific Reason travels in the payload, so one Kind
// covers the channel and the SSE event:type line is stable, mirroring
// SessionControlKind / GroupSyncKind / ExtensionEventKind.
const InvalidationKind = "client.invalidate"

// Client-cache invalidation reasons. The set is closed and generic
// (invariant #1) — a reason names why a cache is stale, never what is playing.
// Clients ship this vocabulary compiled in and purge regardless of an unknown
// reason (the reason is an advisory hint, not an authorization input).
const (
	// InvalidationReasonAccessChanged fires when a Profile's access policy
	// (access schedule / enforced filter — ADR-0087) changed under it, so cached
	// OS surfaces may now surface newly out-of-policy content.
	InvalidationReasonAccessChanged = "access-changed"
	// InvalidationReasonLibraryRestrictionChanged fires when a Library's
	// restriction flag was toggled. Unlike a per-Profile ceiling
	// change it affects every Profile granted the Library, so the mutation
	// fans this reason out one-per-Profile (each event still Profile-gated and
	// content-free). The reason is identical in both directions — it never
	// names the Library or which way it moved, so it is not a reveal/existence
	// oracle (invariant #5).
	InvalidationReasonLibraryRestrictionChanged = "library-restriction-changed"
)

var invalidationReasons = map[string]bool{
	InvalidationReasonAccessChanged:             true,
	InvalidationReasonLibraryRestrictionChanged: true,
}

// InvalidationReasonValid reports whether reason is a member of the closed
// invalidation-reason vocabulary. The caller validates before appending, so an
// unknown reason is a programming error, never an outbox row.
func InvalidationReasonValid(reason string) bool {
	return invalidationReasons[reason]
}

// InvalidationPayload is the JSON body of a client.invalidate outbox event.
// ProfileID is the SSE delivery gate (the affected Profile, store-written at
// issue time, never client input). Reason is the content-free advisory hint
// from the closed vocabulary. No Media id, no titles — invariant #5.
type InvalidationPayload struct {
	ProfileID ProfileID `json:"profileID"`
	Reason    string    `json:"reason"`
}

// AppendInvalidationEvent records a server-initiated client-cache invalidation
// in the outbox so the affected Profile's still-connected sessions purge OS-owned
// caches that may now surface newly-restricted content. The
// AggregateID is the affected Profile id; the payload carries the same Profile so
// the SSE surface Profile-gates delivery WITHOUT a live read — the same
// fail-closed gate the §7/§8/extension channels use. The Profile is set by the
// caller from the mutated record, never client input, so an invalidation can
// never cross a Profile boundary.
func (s *Store) AppendInvalidationEvent(ctx context.Context, payload InvalidationPayload, now time.Time) error {
	if payload.ProfileID == "" {
		return ValidationError("invalidation event is missing its Profile")
	}
	if !invalidationReasons[payload.Reason] {
		return ValidationError("unknown invalidation reason")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	now = normalizeTime(now)
	_, err = s.eventsW.ExecContext(ctx,
		"INSERT INTO event_outbox(occurred_at_ms, kind, aggregate_type, aggregate_id, payload_json) VALUES (?, ?, ?, ?, ?)",
		timeToMillis(now), InvalidationKind, OutboxAggregateInvalidation, string(payload.ProfileID), string(normalizeJSON(raw)),
	)
	return err
}

// AppendGroupSyncEvent records a Group Conductor sync signal in the outbox.
// The AggregateID is the ephemeral group id; the payload carries
// the group's Profile so the SSE surface Profile-gates delivery WITHOUT a live
// read (group membership lives only in memory — there is nothing durable to read
// — so the Profile carried here is the delivery authorization, and it was set
// from the group's owning Profile at formation, same-Profile by construction).
// The signal is delivered to every same-Profile subscriber on the existing
// /api/events channel; a member acts on a transition addressed to its group and
// on a nudge addressed to its own session, ignoring the rest.
func (s *Store) AppendGroupSyncEvent(ctx context.Context, payload GroupSyncPayload, now time.Time) error {
	if payload.GroupID == "" {
		return ValidationError("group id is required")
	}
	if payload.ProfileID == "" {
		return ValidationError("group-sync event is missing its Profile")
	}
	if !groupSyncSignals[payload.Signal] {
		return ValidationError("unknown group sync signal")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	now = normalizeTime(now)
	_, err = s.eventsW.ExecContext(ctx,
		"INSERT INTO event_outbox(occurred_at_ms, kind, aggregate_type, aggregate_id, payload_json) VALUES (?, ?, ?, ?, ?)",
		timeToMillis(now), GroupSyncKind, OutboxAggregateGroup, payload.GroupID, string(normalizeJSON(raw)),
	)
	return err
}

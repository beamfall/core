package store

import (
	"context"
	"database/sql"
	"strings"
)

// ActivityEntriesAfter returns activity entries with an id above afterID whose
// action is one of actions, oldest first. Only entries with no Library
// reference are returned: they name no Media, so nothing in them can belong
// to a hidden or sealed Library. It is the tail read behind outbound
// notifications, which follow the activity log by id the way the realtime
// spine follows the outbox by seq.
func (s *Store) ActivityEntriesAfter(ctx context.Context, afterID int64, actions []string, limit int) ([]ActivityEntry, error) {
	if len(actions) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	args := make([]any, 0, len(actions)+2)
	args = append(args, afterID)
	for _, action := range actions {
		args = append(args, action)
	}
	args = append(args, limit)
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(actions)), ", ")
	// #nosec G202 -- only generated placeholders are interpolated; every value is bound.
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT id, operation_id, occurred_at_ms, action, session_id, device_id, profile_id, target_type, target_id, library_id, token_id, api_key_id
		FROM activity_entry
		WHERE id > ? AND library_id = '' AND action IN (`+placeholders+`)
		ORDER BY id
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []ActivityEntry
	for rows.Next() {
		var entry ActivityEntry
		var operationID sql.NullString
		var occurredAt int64
		if err := rows.Scan(&entry.ID, &operationID, &occurredAt, &entry.Action, &entry.SessionID,
			&entry.DeviceID, &entry.ProfileID, &entry.TargetType, &entry.TargetID, &entry.LibraryID, &entry.TokenID, &entry.APIKeyID); err != nil {
			return nil, err
		}
		entry.OperationID = operationID.String
		entry.OccurredAt = millisToTime(occurredAt)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// LatestActivityID returns the highest activity entry id, or zero when the
// log is empty. A reader that starts at this id sees only entries appended
// after it.
func (s *Store) LatestActivityID(ctx context.Context) (int64, error) {
	return s.latestEventsID(ctx, "SELECT MAX(id) FROM activity_entry")
}

// LatestEventSeq returns the highest event outbox seq, or zero when the
// outbox is empty. A reader that starts at this seq sees only events appended
// after it.
func (s *Store) LatestEventSeq(ctx context.Context) (int64, error) {
	return s.latestEventsID(ctx, "SELECT MAX(seq) FROM event_outbox")
}

func (s *Store) latestEventsID(ctx context.Context, query string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	var latest sql.NullInt64
	if err := s.eventsR.QueryRowContext(ctx, query).Scan(&latest); err != nil {
		return 0, err
	}
	return latest.Int64, nil
}

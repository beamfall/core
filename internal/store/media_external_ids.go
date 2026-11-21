package store

import (
	"context"
	"strings"
)

// MediaExternalID is one plugin-contributed identifier attached to Media.
// Scheme is deliberately open vocabulary, constrained only to the stable
// lowercase token shape clients and plugins can compare.
type MediaExternalID struct {
	Scheme string
	Value  string
}

// PublicFeedIdentity protects detail reads of feed identities written before
// URL GUIDs were made opaque. Other external-ID schemes keep their values.
func (id MediaExternalID) PublicFeedIdentity(title string) (value, safeTitle string) {
	guid := id.Value
	switch id.Scheme {
	case subscriptionFeedGUIDScheme:
		value = feedGUIDExternalID(id.Value)
	case ProvisionalExternalIDScheme:
		if _, suffix, ok := strings.Cut(guid, "\x00"); ok {
			guid = suffix
		}
		value = provisionalGUIDExternalID(id.Value)
	default:
		return id.Value, title
	}
	if title == guid {
		title = feedEntryFallbackTitle(guid)
	}
	return value, title
}

// AddMediaExternalID attaches an external identifier to Media idempotently.
// A newly attached identifier announces media.updated, so a subscriber waiting
// for an identifier (a request tool watching for one it asked for) sees it even
// when hydration attaches it after the scan's media.created. Re-attaching an
// identifier the Media already carries is a silent no-op.
func (s *Store) AddMediaExternalID(ctx context.Context, mediaID MediaID, scheme string, value string) error {
	scheme = strings.TrimSpace(scheme)
	value = strings.TrimSpace(value)
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	if !validExternalIDScheme(scheme) {
		return ValidationError("external ID scheme is invalid")
	}
	if value == "" {
		return ValidationError("external ID value is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_external_id(media_id, scheme, value)
		VALUES (?, ?, ?)
	`, mediaID, scheme, value)
	if err != nil {
		return err
	}
	if inserted, err := result.RowsAffected(); err == nil && inserted > 0 {
		s.emitMediaUpdated(ctx, mediaID)
	}
	return nil
}

// MediaExternalIDs returns external identifiers for one Media item in stable
// scheme/value order.
func (s *Store) MediaExternalIDs(ctx context.Context, mediaID MediaID) ([]MediaExternalID, error) {
	if mediaID == "" {
		return nil, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT scheme, value
		FROM media_external_id
		WHERE media_id = ?
		ORDER BY scheme, value
	`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaExternalID
	for rows.Next() {
		var id MediaExternalID
		if err := rows.Scan(&id.Scheme, &id.Value); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MediaExternalIDsBatch returns external identifiers for many Media items in
// one read, keyed by Media ID, each slice in stable scheme/value order. Media
// with no identifiers are absent from the map. Callers apply visibility before
// asking: this read does not filter.
func (s *Store) MediaExternalIDsBatch(ctx context.Context, mediaIDs []MediaID) (map[MediaID][]MediaExternalID, error) {
	out := make(map[MediaID][]MediaExternalID, len(mediaIDs))
	if len(mediaIDs) == 0 {
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	for start := 0; start < len(mediaIDs); start += bulkWriteChunkSize {
		chunk := mediaIDs[start:min(start+bulkWriteChunkSize, len(mediaIDs))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		// #nosec G202 -- only generated placeholders are interpolated; IDs are bound args.
		rows, err := s.catalogR.QueryContext(ctx, `
			SELECT media_id, scheme, value
			FROM media_external_id
			WHERE media_id IN (`+sqlPlaceholders(len(chunk))+`)
			ORDER BY media_id, scheme, value
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var mediaID MediaID
			var id MediaExternalID
			if err := rows.Scan(&mediaID, &id.Scheme, &id.Value); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[mediaID] = append(out[mediaID], id)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MediaIDsByExternalID looks up all Media rows carrying scheme/value. The
// result is intentionally not unique at the external-id level: duplicate
// imports may share an identifier until a later dedupe pass resolves them.
func (s *Store) MediaIDsByExternalID(ctx context.Context, scheme string, value string) ([]MediaID, error) {
	scheme = strings.TrimSpace(scheme)
	value = strings.TrimSpace(value)
	if !validExternalIDScheme(scheme) {
		return nil, ValidationError("external ID scheme is invalid")
	}
	if value == "" {
		return nil, ValidationError("external ID value is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	opaque, _ := (MediaExternalID{Scheme: scheme, Value: value}).PublicFeedIdentity("")
	guid := value
	if scheme == ProvisionalExternalIDScheme {
		if _, suffix, ok := strings.Cut(value, "\x00"); ok {
			guid = suffix
		}
	}
	legacyLookup := (scheme == subscriptionFeedGUIDScheme || scheme == ProvisionalExternalIDScheme) &&
		strings.HasPrefix(guid, opaqueFeedGUIDPrefix)
	query := `
		SELECT media_id, value
		FROM media_external_id
		WHERE scheme = ? AND value IN (?, ?)
		ORDER BY media_id
	`
	args := []any{scheme, opaque, value}
	if legacyLookup {
		// Old feed identifiers contain the original URL. Compare their public
		// representation without exposing that stored value to the caller.
		query = `
			SELECT media_id, value FROM media_external_id WHERE scheme = ? ORDER BY media_id
		`
		args = []any{scheme}
	}
	rows, err := s.catalogR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		candidate := MediaExternalID{Scheme: scheme}
		if err := rows.Scan(&id, &candidate.Value); err != nil {
			return nil, err
		}
		if legacyLookup && candidate.Value != value {
			public, _ := candidate.PublicFeedIdentity("")
			if public != value {
				continue
			}
		}
		if len(ids) == 0 || ids[len(ids)-1] != id {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func validExternalIDScheme(scheme string) bool {
	if scheme == "" {
		return false
	}
	for _, r := range scheme {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			continue
		}
		return false
	}
	return true
}

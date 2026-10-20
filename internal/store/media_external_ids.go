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

// AddMediaExternalID attaches an external identifier to Media idempotently.
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
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_external_id(media_id, scheme, value)
		VALUES (?, ?, ?)
	`, mediaID, scheme, value)
	return err
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
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT media_id
		FROM media_external_id
		WHERE scheme = ? AND value = ?
		ORDER BY media_id
	`, scheme, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
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

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MaxProfilePictureBytes bounds a stored Profile picture. The server stores
// only its own normalized JPEG (CORE-1240), which is far smaller; the bound
// stops a caller of SetProfilePicture from storing an unnormalized upload.
const MaxProfilePictureBytes = 512 << 10

// ReorderProfiles sets the order every Profile picker shows (CORE-2477).
// ids lists Profiles in their new order; it must name existing Profiles,
// each once. Profiles it leaves out keep their relative order after the
// listed ones, so a client that does not see disabled Profiles can still
// reorder the rest.
func (s *Store) ReorderProfiles(ctx context.Context, ids []ProfileID) error {
	if len(ids) == 0 {
		return ValidationError("profile order is required")
	}
	seen := make(map[ProfileID]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return ValidationError("profile ID is required")
		}
		if seen[id] {
			return ValidationError("profile order lists a profile twice")
		}
		seen[id] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM profile ORDER BY sort_order, name, id`)
	if err != nil {
		return err
	}
	var current []ProfileID
	for rows.Next() {
		var id ProfileID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		current = append(current, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	known := make(map[ProfileID]bool, len(current))
	for _, id := range current {
		known[id] = true
	}
	for _, id := range ids {
		if !known[id] {
			return ValidationError("unknown profile")
		}
	}
	order := append([]ProfileID(nil), ids...)
	for _, id := range current {
		if !seen[id] {
			order = append(order, id)
		}
	}
	for position, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE profile SET sort_order = ? WHERE id = ?`, position, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetProfilePicture stores image, already normalized by the caller, as the
// Profile's picture and returns the version stamp clients use to refetch it.
func (s *Store) SetProfilePicture(ctx context.Context, id ProfileID, image []byte, now time.Time) (int64, error) {
	if id == "" {
		return 0, ValidationError("profile ID is required")
	}
	if len(image) == 0 {
		return 0, ValidationError("picture is empty")
	}
	if len(image) > MaxProfilePictureBytes {
		return 0, ValidationError("picture is too large")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	if exists, err := s.profileExistsSQL(ctx, id); err != nil {
		return 0, err
	} else if !exists {
		return 0, ValidationError("unknown profile")
	}
	// The version is the picture's immutable cache identity, so a replacement
	// always gets a larger one, even within the same millisecond.
	var version int64
	err := s.catalogW.QueryRowContext(ctx, `
		INSERT INTO profile_picture(profile_id, image, updated_at_ms) VALUES (?, ?, ?)
		ON CONFLICT(profile_id) DO UPDATE SET
			image = excluded.image,
			updated_at_ms = MAX(excluded.updated_at_ms, profile_picture.updated_at_ms + 1)
		RETURNING updated_at_ms
	`, id, image, now.UTC().UnixMilli()).Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// ClearProfilePicture removes the Profile's picture. Removing a picture the
// Profile does not have is not an error.
func (s *Store) ClearProfilePicture(ctx context.Context, id ProfileID) error {
	if id == "" {
		return ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if exists, err := s.profileExistsSQL(ctx, id); err != nil {
		return err
	} else if !exists {
		return ValidationError("unknown profile")
	}
	_, err := s.catalogW.ExecContext(ctx, `DELETE FROM profile_picture WHERE profile_id = ?`, id)
	return err
}

// ErrNoProfilePicture reports that a Profile has no stored picture.
var ErrNoProfilePicture = errors.New("profile has no picture")

// ProfilePicture returns the Profile's stored picture and its version stamp,
// or ErrNoProfilePicture.
func (s *Store) ProfilePicture(ctx context.Context, id ProfileID) ([]byte, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, 0, err
	}
	var image []byte
	var version int64
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT image, updated_at_ms FROM profile_picture WHERE profile_id = ?`, id).Scan(&image, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNoProfilePicture
	}
	if err != nil {
		return nil, 0, err
	}
	return image, version, nil
}

// ProfilePictureVersions returns the picture version stamp of every Profile
// that has a picture. Profiles without one are absent.
func (s *Store) ProfilePictureVersions(ctx context.Context) (map[ProfileID]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT profile_id, updated_at_ms FROM profile_picture`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := map[ProfileID]int64{}
	for rows.Next() {
		var id ProfileID
		var version int64
		if err := rows.Scan(&id, &version); err != nil {
			return nil, err
		}
		versions[id] = version
	}
	return versions, rows.Err()
}

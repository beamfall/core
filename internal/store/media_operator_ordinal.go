package store

import (
	"context"
	"database/sql"
	"errors"
)

const MediaOrdinalLockField = "ordinal"

// MediaOrdinal is the manual lookup ordinal for one Media item.
type MediaOrdinal struct {
	Sequence string
	Index    int
}

// MediaOperatorOrdinal reads the manual tier on the plane that owns the Media.
// The caller gates visibility when exposing this value over the wire.
func (s *Store) MediaOperatorOrdinal(ctx context.Context, id MediaID) (*MediaOrdinal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if errors.Is(err, errPlaneMediaNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ordinal MediaOrdinal
	err = plane.reader.QueryRowContext(ctx,
		`SELECT sequence, ordinal_index FROM media_operator_ordinal WHERE media_id = ?`, id).
		Scan(&ordinal.Sequence, &ordinal.Index)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ordinal, nil
}

// ClearMediaOrdinal removes the manual tier without changing match pins.
func (s *Store) ClearMediaOrdinal(ctx context.Context, id MediaID) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if errors.Is(err, errPlaneMediaNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, err := deleteMediaOrdinal(ctx, plane.writer, id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count != 0, err
}

func deleteMediaOrdinal(ctx context.Context, db sqlExecer, id MediaID) (sql.Result, error) {
	return db.ExecContext(ctx, `DELETE FROM media_operator_ordinal WHERE media_id = ?`, id)
}

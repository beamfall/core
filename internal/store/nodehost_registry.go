package store

import (
	"database/sql"
	"errors"
	"time"
)

// PutNodehostCode stores a one-time Origin Node enrollment code.
func (s *Store) PutNodehostCode(code string, expiresAt time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.Exec(`
INSERT OR REPLACE INTO nodehost_enrollment_code (code, expires_at_ms)
VALUES (?, ?)
`, code, expiresAt.UnixMilli())
	return err
}

// ConsumeNodehostCodeAndPutNode atomically consumes an unexpired enrollment code
// and persists the enrolled-node document.
func (s *Store) ConsumeNodehostCodeAndPutNode(code string, now time.Time, nodeID string, state string, nodeJSON []byte) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	tx, err := s.catalogW.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var expiresAtMS int64
	if err := tx.QueryRow(`SELECT expires_at_ms FROM nodehost_enrollment_code WHERE code = ?`, code).Scan(&expiresAtMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if now.UnixMilli() >= expiresAtMS {
		if _, err := tx.Exec(`DELETE FROM nodehost_enrollment_code WHERE code = ?`, code); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	if _, err := tx.Exec(`DELETE FROM nodehost_enrollment_code WHERE code = ?`, code); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`
INSERT OR REPLACE INTO nodehost_node (id, state, updated_at_ms, document_json)
VALUES (?, ?, ?, ?)
`, nodeID, state, now.UnixMilli(), nodeJSON); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// PutNodehostNode upserts an enrolled-node document.
func (s *Store) PutNodehostNode(nodeID string, state string, nodeJSON []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.Exec(`
INSERT OR REPLACE INTO nodehost_node (id, state, updated_at_ms, document_json)
VALUES (?, ?, ?, ?)
`, nodeID, state, s.serverNow().UnixMilli(), nodeJSON)
	return err
}

// NodehostNode loads an enrolled-node document.
func (s *Store) NodehostNode(nodeID string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, false, err
	}
	var data []byte
	if err := s.catalogR.QueryRow(`SELECT document_json FROM nodehost_node WHERE id = ?`, nodeID).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

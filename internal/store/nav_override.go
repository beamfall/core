package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// NavOverrideData is the operator-editable override blob for the Navigation
// Manifest and Layout (WEB-5J / ADR-0061 §Layout editor).
type NavOverrideData struct {
	// HiddenNodeIDs suppresses nav nodes by ID ("ambient", "library-<id>", etc.).
	HiddenNodeIDs []string `json:"hiddenNodeIDs,omitempty"`
	// NodeOrder, if non-empty, defines the canonical node order. IDs not listed
	// are appended in their default order after the listed ones.
	NodeOrder []string `json:"nodeOrder,omitempty"`
	// NodeLabelOverrides replaces the manifest-generated title for a node.
	// Each value is a locale map (key = BCP-47 tag, e.g. "en").
	NodeLabelOverrides map[string]map[string]string `json:"nodeLabelOverrides,omitempty"`
	// ThemeAccentColor, when non-empty, replaces the manifest's default accent
	// color. Must be a "#RRGGBB" hex string.
	ThemeAccentColor string `json:"themeAccentColor,omitempty"`
	// LayoutSectionOverrides customises the Section list per Layout ID.
	LayoutSectionOverrides map[string]SectionOverrideData `json:"layoutSectionOverrides,omitempty"`
}

// SectionOverrideData controls visibility and order within one Layout.
type SectionOverrideData struct {
	HiddenSectionIDs []string `json:"hiddenSectionIDs,omitempty"`
	// SectionOrder, if non-empty, sets the canonical section order. Unlisted
	// sections are appended after in default order.
	SectionOrder []string `json:"sectionOrder,omitempty"`
}

// NavOverrideHistoryEntry is one entry in the audit log.
type NavOverrideHistoryEntry struct {
	ID        int64
	Scope     string
	ScopeID   string // '' for server-level
	Data      NavOverrideData
	CreatedAt time.Time
}

// navScopeID normalises a *string scope_id to the non-NULL empty-string form
// used in the database (SQLite does not permit expression columns in PRIMARY KEY).
func navScopeID(scopeID *string) string {
	if scopeID == nil {
		return ""
	}
	return *scopeID
}

// NavOverride returns the current override for the given scope. Returns an
// empty NavOverrideData (not an error) when no override has been written yet.
func (s *Store) NavOverride(ctx context.Context, scope string, scopeID *string) (NavOverrideData, error) {
	var raw string
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT data FROM nav_override WHERE scope = ? AND scope_id = ?`,
		scope, navScopeID(scopeID),
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return NavOverrideData{}, nil
	}
	if err != nil {
		return NavOverrideData{}, err
	}
	var out NavOverrideData
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return NavOverrideData{}, err
	}
	return out, nil
}

// SetNavOverride writes a new override blob, saving the prior value (if any)
// to the history table and pruning history beyond 50 entries per scope.
func (s *Store) SetNavOverride(ctx context.Context, scope string, scopeID *string, data NavOverrideData, now time.Time) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	sid := navScopeID(scopeID)
	ts := now.UTC().Format(time.RFC3339)
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Save current value to history (ignore not-found).
	var prev string
	_ = tx.QueryRowContext(ctx,
		`SELECT data FROM nav_override WHERE scope = ? AND scope_id = ?`, scope, sid,
	).Scan(&prev)
	if prev != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO nav_override_history(scope, scope_id, data, created_at) VALUES(?, ?, ?, ?)`,
			scope, sid, prev, ts,
		); err != nil {
			return err
		}
		// Prune: keep at most 50 history rows per scope.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM nav_override_history WHERE scope = ? AND scope_id = ?
			 AND id NOT IN (SELECT id FROM nav_override_history WHERE scope = ? AND scope_id = ? ORDER BY id DESC LIMIT 50)`,
			scope, sid, scope, sid,
		); err != nil {
			return err
		}
	}

	// Upsert the new value.
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO nav_override(scope, scope_id, data, updated_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(scope, scope_id) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at`,
		scope, sid, string(raw), ts,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetNavOverride clears the override (saving the prior value to history)
// so the manifest reverts to plugin/library defaults.
func (s *Store) ResetNavOverride(ctx context.Context, scope string, scopeID *string, now time.Time) error {
	return s.SetNavOverride(ctx, scope, scopeID, NavOverrideData{}, now)
}

// NavOverrideHistory returns the last `limit` history entries for the given scope, newest first.
func (s *Store) NavOverrideHistory(ctx context.Context, scope string, scopeID *string, limit int) ([]NavOverrideHistoryEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.catalogR.QueryContext(ctx,
		`SELECT id, scope, scope_id, data, created_at FROM nav_override_history
		 WHERE scope = ? AND scope_id = ? ORDER BY id DESC LIMIT ?`,
		scope, navScopeID(scopeID), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NavOverrideHistoryEntry
	for rows.Next() {
		var e NavOverrideHistoryEntry
		var rawData, createdAt string
		if err := rows.Scan(&e.ID, &e.Scope, &e.ScopeID, &rawData, &createdAt); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339, createdAt)
		e.CreatedAt = t
		if err := json.Unmarshal([]byte(rawData), &e.Data); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RestoreNavOverride replaces the current override with a specific history entry.
func (s *Store) RestoreNavOverride(ctx context.Context, scope string, scopeID *string, historyID int64, now time.Time) (NavOverrideData, error) {
	var rawData string
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT data FROM nav_override_history WHERE id = ? AND scope = ?`, historyID, scope,
	).Scan(&rawData)
	if errors.Is(err, sql.ErrNoRows) {
		return NavOverrideData{}, ValidationError("history entry not found")
	}
	if err != nil {
		return NavOverrideData{}, err
	}
	var data NavOverrideData
	if err := json.Unmarshal([]byte(rawData), &data); err != nil {
		return NavOverrideData{}, err
	}
	if err := s.SetNavOverride(ctx, scope, scopeID, data, now); err != nil {
		return NavOverrideData{}, err
	}
	return data, nil
}

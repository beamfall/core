package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

// ProfileAccessWindow is one allowed weekly time window for a managed Profile
// (ADR-0004 §4 / ADR-0007 — server-enforced access schedules). Weekday follows
// Go's time.Weekday (0 = Sunday … 6 = Saturday). StartMinute and EndMinute are
// server-local minutes-from-midnight; the window is the half-open interval
// [StartMinute, EndMinute) so adjacent windows do not overlap on the boundary.
// EndMinute may be 1440 (end of day). A window never spans midnight — the
// Console splits a cross-midnight allowance into two windows.
type ProfileAccessWindow struct {
	Weekday     int
	StartMinute int
	EndMinute   int
}

// validate reports a malformed window. Half-open interval: end must be strictly
// after start, weekday 0–6, minutes within a day (end may reach 1440).
func (w ProfileAccessWindow) validate() error {
	if w.Weekday < 0 || w.Weekday > 6 {
		return ValidationError("access window weekday must be 0 through 6")
	}
	if w.StartMinute < 0 || w.StartMinute > 1439 {
		return ValidationError("access window start minute must be 0 through 1439")
	}
	if w.EndMinute < 1 || w.EndMinute > 1440 {
		return ValidationError("access window end minute must be 1 through 1440")
	}
	if w.EndMinute <= w.StartMinute {
		return ValidationError("access window end minute must be after start minute")
	}
	return nil
}

// ProfileAccessWindows returns a Profile's allowed time windows in stable
// (weekday, start, end) order. An empty result means the Profile has no
// schedule — the enforcement default is "always allowed" (see AllowedAt).
func (s *Store) ProfileAccessWindows(ctx context.Context, profileID ProfileID) ([]ProfileAccessWindow, error) {
	if profileID == "" {
		return nil, errors.New("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT weekday, start_minute, end_minute
		FROM profile_access_schedules
		WHERE profile_id = ?
		ORDER BY weekday, start_minute, end_minute
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	windows := []ProfileAccessWindow{}
	for rows.Next() {
		var w ProfileAccessWindow
		if err := rows.Scan(&w.Weekday, &w.StartMinute, &w.EndMinute); err != nil {
			return nil, err
		}
		windows = append(windows, w)
	}
	return windows, rows.Err()
}

// SetProfileAccessWindows replaces a managed Profile's allowed time windows
// (full-replace, like SetLibraryRoots). Passing an empty slice clears the
// schedule, restoring the always-allowed default. Only managed Profiles carry
// a schedule — an adult Profile has the Reveal path, not a time gate, so this
// refuses a non-managed target. Windows are normalized: validated, then
// deduplicated and stored in stable order.
func (s *Store) SetProfileAccessWindows(ctx context.Context, profileID ProfileID, windows []ProfileAccessWindow) error {
	if profileID == "" {
		return errors.New("profile ID is required")
	}
	for _, w := range windows {
		if err := w.validate(); err != nil {
			return err
		}
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
	defer tx.Rollback()
	var kind ProfileKind
	err = tx.QueryRowContext(ctx, `SELECT kind FROM profile WHERE id = ?`, profileID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("profile not found")
	}
	if err != nil {
		return err
	}
	if kind != ProfileManaged {
		return ValidationError("access schedules apply to managed profiles only")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_access_schedules WHERE profile_id = ?`, profileID); err != nil {
		return err
	}
	seen := map[ProfileAccessWindow]bool{}
	for _, w := range windows {
		if seen[w] {
			continue
		}
		seen[w] = true
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO profile_access_schedules(profile_id, weekday, start_minute, end_minute)
			VALUES (?, ?, ?, ?)
		`, profileID, w.Weekday, w.StartMinute, w.EndMinute); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AllowedAt reports whether a set of access windows admits the instant t,
// evaluated in t's own location (callers pass server-local time). The contract
// is additive-safe: no windows means no schedule, which is always allowed. With
// windows present, the instant must fall inside one window for that weekday —
// the half-open interval [start, end) in minutes-from-midnight.
func AllowedAt(windows []ProfileAccessWindow, t time.Time) bool {
	if len(windows) == 0 {
		return true
	}
	weekday := int(t.Weekday())
	minute := t.Hour()*60 + t.Minute()
	return slices.ContainsFunc(windows, func(w ProfileAccessWindow) bool {
		return w.Weekday == weekday && minute >= w.StartMinute && minute < w.EndMinute
	})
}

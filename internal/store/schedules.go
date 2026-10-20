package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type ScheduleID string
type ScheduleTriggerKind string
type ScheduleEveryUnit string

const (
	ScheduleTriggerEvery  ScheduleTriggerKind = "every"
	ScheduleTriggerDaily  ScheduleTriggerKind = "daily"
	ScheduleTriggerWeekly ScheduleTriggerKind = "weekly"

	ScheduleEveryMinutes ScheduleEveryUnit = "minutes"
	ScheduleEveryHours   ScheduleEveryUnit = "hours"
)

type ScheduleTrigger struct {
	Kind    ScheduleTriggerKind
	N       int
	Unit    ScheduleEveryUnit
	Weekday int
	Hour    int
	Minute  int
}

type Schedule struct {
	ID                   ScheduleID
	JobKind              JobKind
	Trigger              ScheduleTrigger
	Enabled              bool
	Orphaned             bool
	LastCompletedEnqueue time.Time
}

func (s *Store) LibraryRoots(ctx context.Context, libraryID LibraryID) ([]string, error) {
	if libraryID == "" {
		return nil, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if err := libraryExists(ctx, s.catalogR, libraryID); err != nil {
		return nil, err
	}
	return libraryRootsSQL(ctx, s.catalogR, libraryID)
}

// AllLibraryRoots returns the configured root folders of every Library.
func (s *Store) AllLibraryRoots(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT root FROM library_roots ORDER BY root`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roots := []string{}
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

func (s *Store) SetLibraryRoots(ctx context.Context, libraryID LibraryID, roots []string) error {
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	normalized, err := normalizeLibraryRoots(roots)
	if err != nil {
		return err
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
	if err := libraryExistsTx(ctx, tx, libraryID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_roots WHERE library_id = ?`, libraryID); err != nil {
		return err
	}
	for _, root := range normalized {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_roots(library_id, root) VALUES (?, ?)`, libraryID, root); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListSchedules(ctx context.Context) ([]Schedule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, scheduleSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schedules := []Schedule{}
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func (s *Store) Schedule(ctx context.Context, id ScheduleID) (Schedule, error) {
	if id == "" {
		return Schedule{}, errors.New("schedule ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Schedule{}, err
	}
	schedule, err := scanSchedule(s.catalogR.QueryRowContext(ctx, scheduleSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, ValidationError("schedule not found")
	}
	return schedule, err
}

func (s *Store) CreateSchedule(ctx context.Context, schedule Schedule) error {
	if err := validateSchedule(schedule); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO schedules(id, job_kind, trigger_kind, every_n, every_unit, weekday, hour, minute, enabled, orphaned, last_completed_enqueue_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, schedule.ID, schedule.JobKind, schedule.Trigger.Kind, nullableScheduleInt(schedule.Trigger.N, schedule.Trigger.Kind == ScheduleTriggerEvery),
		nullableScheduleUnit(schedule.Trigger), nullableScheduleInt(schedule.Trigger.Weekday, schedule.Trigger.Kind == ScheduleTriggerWeekly),
		nullableScheduleInt(schedule.Trigger.Hour, schedule.Trigger.Kind == ScheduleTriggerDaily || schedule.Trigger.Kind == ScheduleTriggerWeekly),
		nullableScheduleInt(schedule.Trigger.Minute, schedule.Trigger.Kind == ScheduleTriggerDaily || schedule.Trigger.Kind == ScheduleTriggerWeekly),
		boolToInt(schedule.Enabled), boolToInt(schedule.Orphaned), timeToMillis(schedule.LastCompletedEnqueue))
	return err
}

func (s *Store) UpdateSchedule(ctx context.Context, id ScheduleID, trigger *ScheduleTrigger, enabled *bool) (Schedule, error) {
	if id == "" {
		return Schedule{}, errors.New("schedule ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Schedule{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Schedule{}, err
	}
	defer tx.Rollback()
	row, err := scanSchedule(tx.QueryRowContext(ctx, scheduleSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, ValidationError("schedule not found")
	}
	if err != nil {
		return Schedule{}, err
	}
	if trigger != nil {
		row.Trigger = *trigger
	}
	if enabled != nil {
		row.Enabled = *enabled
	}
	if err := validateSchedule(row); err != nil {
		return Schedule{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE schedules
		SET trigger_kind = ?, every_n = ?, every_unit = ?, weekday = ?, hour = ?, minute = ?, enabled = ?
		WHERE id = ?
	`, row.Trigger.Kind, nullableScheduleInt(row.Trigger.N, row.Trigger.Kind == ScheduleTriggerEvery), nullableScheduleUnit(row.Trigger),
		nullableScheduleInt(row.Trigger.Weekday, row.Trigger.Kind == ScheduleTriggerWeekly),
		nullableScheduleInt(row.Trigger.Hour, row.Trigger.Kind == ScheduleTriggerDaily || row.Trigger.Kind == ScheduleTriggerWeekly),
		nullableScheduleInt(row.Trigger.Minute, row.Trigger.Kind == ScheduleTriggerDaily || row.Trigger.Kind == ScheduleTriggerWeekly),
		boolToInt(row.Enabled), id)
	if err := jobMutationResult(result, err, ValidationError("schedule not found")); err != nil {
		return Schedule{}, err
	}
	if err := tx.Commit(); err != nil {
		return Schedule{}, err
	}
	return row, nil
}

func (s *Store) SetScheduleOrphaned(ctx context.Context, id ScheduleID, orphaned bool) error {
	if id == "" {
		return errors.New("schedule ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `UPDATE schedules SET orphaned = ? WHERE id = ?`, boolToInt(orphaned), id)
	return jobMutationResult(result, err, ValidationError("schedule not found"))
}

func (s *Store) MarkScheduleCompleted(ctx context.Context, id ScheduleID, now time.Time) error {
	if id == "" {
		return errors.New("schedule ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	// MAX keeps completion monotonic: a replayed post-crash completion, or a
	// completion computed under a backward clock jump, can never move the
	// cursor backward and re-fire an already-published occurrence.
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE schedules SET last_completed_enqueue_ms = MAX(last_completed_enqueue_ms, ?) WHERE id = ?
	`, timeToMillis(now), id)
	return jobMutationResult(result, err, ValidationError("schedule not found"))
}

func normalizeLibraryRoots(roots []string) ([]string, error) {
	seen := map[string]bool{}
	normalized := []string{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			return nil, ValidationError("library root is required")
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		cleaned := filepath.Clean(absolute)
		if !seen[cleaned] {
			seen[cleaned] = true
			normalized = append(normalized, cleaned)
		}
	}
	slices.Sort(normalized)
	return normalized, nil
}

func libraryRootsSQL(ctx context.Context, db *sql.DB, libraryID LibraryID) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT root FROM library_roots WHERE library_id = ? ORDER BY root`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roots := []string{}
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

func libraryExists(ctx context.Context, db *sql.DB, id LibraryID) error {
	var one int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM library WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("library not found")
	}
	return err
}

func libraryExistsTx(ctx context.Context, tx *sql.Tx, id LibraryID) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM library WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("library not found")
	}
	return err
}

const scheduleSelect = `
	SELECT id, job_kind, trigger_kind, every_n, every_unit, weekday, hour, minute, enabled, orphaned, last_completed_enqueue_ms
	FROM schedules
`

func scanSchedule(scanner interface{ Scan(dest ...any) error }) (Schedule, error) {
	var schedule Schedule
	var n, weekday, hour, minute sql.NullInt64
	var unit sql.NullString
	var enabled, orphaned int
	var last int64
	if err := scanner.Scan(&schedule.ID, &schedule.JobKind, &schedule.Trigger.Kind,
		&n, &unit, &weekday, &hour, &minute, &enabled, &orphaned, &last); err != nil {
		return Schedule{}, err
	}
	if n.Valid {
		schedule.Trigger.N = int(n.Int64)
	}
	if unit.Valid {
		schedule.Trigger.Unit = ScheduleEveryUnit(unit.String)
	}
	if weekday.Valid {
		schedule.Trigger.Weekday = int(weekday.Int64)
	}
	if hour.Valid {
		schedule.Trigger.Hour = int(hour.Int64)
	}
	if minute.Valid {
		schedule.Trigger.Minute = int(minute.Int64)
	}
	schedule.Enabled = enabled != 0
	schedule.Orphaned = orphaned != 0
	schedule.LastCompletedEnqueue = millisToTime(last)
	return schedule, nil
}

func validateSchedule(schedule Schedule) error {
	if schedule.ID == "" {
		return ValidationError("schedule ID is required")
	}
	if schedule.JobKind == "" {
		return ValidationError("schedule job kind is required")
	}
	if err := ValidateScheduleTrigger(schedule.Trigger); err != nil {
		return err
	}
	if schedule.LastCompletedEnqueue.IsZero() {
		return ValidationError("schedule last enqueue time is required")
	}
	return nil
}

func ValidateScheduleTrigger(trigger ScheduleTrigger) error {
	switch trigger.Kind {
	case ScheduleTriggerEvery:
		if trigger.N <= 0 {
			return ValidationError("every trigger requires a positive n")
		}
		if trigger.Unit != ScheduleEveryMinutes && trigger.Unit != ScheduleEveryHours {
			return ValidationError("every trigger unit must be minutes or hours")
		}
	case ScheduleTriggerDaily:
		if trigger.Hour < 0 || trigger.Hour > 23 {
			return ValidationError("daily trigger hour must be 0 through 23")
		}
		if trigger.Minute < 0 || trigger.Minute > 59 {
			return ValidationError("daily trigger minute must be 0 through 59")
		}
	case ScheduleTriggerWeekly:
		if trigger.Weekday < 0 || trigger.Weekday > 6 {
			return ValidationError("weekly trigger weekday must be 0 through 6")
		}
		if trigger.Hour < 0 || trigger.Hour > 23 {
			return ValidationError("weekly trigger hour must be 0 through 23")
		}
		if trigger.Minute < 0 || trigger.Minute > 59 {
			return ValidationError("weekly trigger minute must be 0 through 59")
		}
	default:
		return ValidationError("trigger kind must be every, daily, or weekly")
	}
	return nil
}

func nullableScheduleInt(value int, keep bool) any {
	if !keep {
		return nil
	}
	return value
}

func nullableScheduleUnit(trigger ScheduleTrigger) any {
	if trigger.Kind != ScheduleTriggerEvery {
		return nil
	}
	return trigger.Unit
}

func scanScheduleID(libraryID LibraryID) ScheduleID {
	return ScheduleID("scan:" + string(libraryID))
}

func defaultScanSchedule(id LibraryID, now time.Time) Schedule {
	return Schedule{
		ID:                   scanScheduleID(id),
		JobKind:              "scan",
		Trigger:              ScheduleTrigger{Kind: ScheduleTriggerEvery, N: 12, Unit: ScheduleEveryHours},
		Enabled:              true,
		LastCompletedEnqueue: now,
	}
}

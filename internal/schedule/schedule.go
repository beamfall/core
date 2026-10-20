// Package schedule reconciles declared schedules and ticks due durable jobs.
package schedule

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

type Trigger = store.ScheduleTrigger
type Kind = store.ScheduleTriggerKind
type Unit = store.ScheduleEveryUnit

const (
	KindEvery  = store.ScheduleTriggerEvery
	KindDaily  = store.ScheduleTriggerDaily
	KindWeekly = store.ScheduleTriggerWeekly

	Minutes = store.ScheduleEveryMinutes
	Hours   = store.ScheduleEveryHours
)

type Spec struct {
	ID             string
	JobKind        store.JobKind
	DefaultTrigger Trigger
	BuildJobs      func(ctx context.Context, now time.Time) ([]store.Job, error)
}

type Store interface {
	ListSchedules(context.Context) ([]store.Schedule, error)
	CreateSchedule(context.Context, store.Schedule) error
	UpdateSchedule(context.Context, store.ScheduleID, *store.ScheduleTrigger, *bool) (store.Schedule, error)
	SetScheduleOrphaned(context.Context, store.ScheduleID, bool) error
	MarkScheduleCompleted(context.Context, store.ScheduleID, time.Time) error
	// Durable occurrence journal (AUDITFIX-SCHEDULER-DURABILITY): occurrence
	// identity and member enqueue progress persist across restart, so a crash
	// at any enqueue boundary recovers to exactly-once publication.
	LatestScheduleOccurrence(context.Context, store.ScheduleID) (store.ScheduleOccurrence, bool, error)
	BeginScheduleOccurrence(context.Context, store.ScheduleID, time.Time, time.Time, []store.Job) (store.ScheduleOccurrence, error)
	EnqueueScheduleOccurrenceMember(context.Context, store.ScheduleID, time.Time, int, time.Time) (store.Job, error)
	CompleteScheduleOccurrence(context.Context, store.ScheduleID, time.Time, time.Time) error
}

type Service struct {
	store  Store
	logger *slog.Logger

	// mu guards specs and rows: Tick runs on the jobs goroutine while
	// Register/Reload/Update arrive from graph handler goroutines.
	mu    sync.Mutex
	specs map[store.ScheduleID]Spec
	rows  map[store.ScheduleID]store.Schedule
}

func NewService(st Store) *Service {
	return &Service{
		store:  st,
		specs:  map[store.ScheduleID]Spec{},
		rows:   map[store.ScheduleID]store.Schedule{},
		logger: slog.Default(),
	}
}

func (s *Service) SetLogger(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	s.logger = logger
}

func (s *Service) Register(spec Spec) {
	if spec.ID == "" || spec.JobKind == "" || spec.BuildJobs == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.specs[store.ScheduleID(spec.ID)] = spec
}

func (s *Service) Reconcile(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.store.ListSchedules(ctx)
	if err != nil {
		return err
	}
	known := map[store.ScheduleID]store.Schedule{}
	for _, row := range rows {
		known[row.ID] = row
	}
	for id, spec := range s.specs {
		if err := ValidateTrigger(spec.DefaultTrigger); err != nil {
			return err
		}
		if row, ok := known[id]; ok {
			if row.Orphaned {
				if err := s.store.SetScheduleOrphaned(ctx, id, false); err != nil {
					return err
				}
				row.Orphaned = false
			}
			s.rows[id] = row
			continue
		}
		row := store.Schedule{
			ID:                   id,
			JobKind:              spec.JobKind,
			Trigger:              spec.DefaultTrigger,
			Enabled:              true,
			LastCompletedEnqueue: now,
		}
		if err := s.store.CreateSchedule(ctx, row); err != nil {
			return err
		}
		s.rows[id] = row
	}
	for id, row := range known {
		if _, ok := s.specs[id]; ok {
			continue
		}
		if !row.Orphaned {
			if err := s.store.SetScheduleOrphaned(ctx, id, true); err != nil {
				return err
			}
			row.Orphaned = true
		}
		s.rows[id] = row
	}
	return nil
}

func (s *Service) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadLocked(ctx)
}

func (s *Service) reloadLocked(ctx context.Context) error {
	rows, err := s.store.ListSchedules(ctx)
	if err != nil {
		return err
	}
	s.rows = map[store.ScheduleID]store.Schedule{}
	for _, row := range rows {
		s.rows[row.ID] = row
	}
	return nil
}

func (s *Service) Tick(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rows) == 0 {
		if err := s.reloadLocked(ctx); err != nil {
			return err
		}
	}
	ids := make([]store.ScheduleID, 0, len(s.rows))
	for id := range s.rows {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := s.tickScheduleLocked(ctx, id, now); err != nil {
			return err
		}
	}
	return nil
}

// tickScheduleLocked drives one schedule through the durable occurrence
// journal: recover an incomplete occurrence first (identity-based, so a
// restart or clock jump can neither duplicate nor drop members), re-apply a
// journaled completion the catalog cursor missed, then open a new occurrence
// when the schedule is due.
func (s *Service) tickScheduleLocked(ctx context.Context, id store.ScheduleID, now time.Time) error {
	row := s.rows[id]
	latest, found, err := s.store.LatestScheduleOccurrence(ctx, id)
	if err != nil {
		return err
	}
	if found && !latest.Completed() {
		// Resume is unconditional: the members were journaled when the
		// occurrence legitimately fired, so publication finishes even if the
		// schedule was disabled or orphaned mid-occurrence, and even if the
		// clock jumped backward — occurrence identity, never elapsed time,
		// decides what still needs publishing.
		s.publishOccurrenceLocked(ctx, &row, latest, now)
		return nil
	}
	if found && row.LastCompletedEnqueue.Before(latest.CompletedAt) {
		// A crash landed between the journaled completion and the catalog
		// cursor update: re-apply the recorded instant without re-enqueueing.
		if err := s.applyCompletionLocked(ctx, &row, latest.CompletedAt); err != nil {
			return err
		}
	}
	if !row.Enabled || row.Orphaned {
		return nil
	}
	due := NextDue(row.LastCompletedEnqueue, row.Trigger, time.Local)
	if due.After(now) {
		return nil
	}
	spec, ok := s.specs[id]
	if !ok {
		return nil
	}
	jobs, err := spec.BuildJobs(ctx, now)
	if err != nil {
		s.logger.WarnContext(ctx, "scheduled job build failed", "schedule", string(id), "error", err)
		return nil
	}
	occurrence, err := s.store.BeginScheduleOccurrence(ctx, id, due, now, jobs)
	if err != nil {
		s.logger.WarnContext(ctx, "schedule occurrence journal failed", "schedule", string(id), "error", err)
		return nil
	}
	s.publishOccurrenceLocked(ctx, &row, occurrence, now)
	return nil
}

// publishOccurrenceLocked publishes every still-pending member of the
// occurrence, then journals and applies completion once none remain. Member
// failures are logged and retried on a later tick; already-published members
// are skipped by the journal itself.
func (s *Service) publishOccurrenceLocked(ctx context.Context, row *store.Schedule, occurrence store.ScheduleOccurrence, now time.Time) {
	pending := 0
	for _, member := range occurrence.Members {
		if member.JobID != "" {
			continue
		}
		if _, err := s.store.EnqueueScheduleOccurrenceMember(ctx, occurrence.ScheduleID, occurrence.Due, member.Ordinal, now); err != nil {
			pending++
			s.logger.WarnContext(ctx, "scheduled job enqueue failed", "schedule", string(occurrence.ScheduleID), "jobKind", string(member.Kind), "error", err)
		}
	}
	if pending != 0 {
		return
	}
	completedAt := scheduleCompletionInstant(row.LastCompletedEnqueue, row.Trigger, now)
	if completedAt.Before(occurrence.Due) {
		// A backward clock jump during recovery must not journal a completion
		// behind the occurrence it completes.
		completedAt = occurrence.Due
	}
	if err := s.store.CompleteScheduleOccurrence(ctx, occurrence.ScheduleID, occurrence.Due, completedAt); err != nil {
		s.logger.WarnContext(ctx, "schedule occurrence completion failed", "schedule", string(occurrence.ScheduleID), "error", err)
		return
	}
	if err := s.applyCompletionLocked(ctx, row, completedAt); err != nil {
		s.logger.WarnContext(ctx, "schedule completion cursor update failed", "schedule", string(occurrence.ScheduleID), "error", err)
	}
}

// applyCompletionLocked moves the schedule's catalog cursor (monotonic in the
// store) and the in-memory row forward, never backward.
func (s *Service) applyCompletionLocked(ctx context.Context, row *store.Schedule, completedAt time.Time) error {
	if err := s.store.MarkScheduleCompleted(ctx, row.ID, completedAt); err != nil {
		return err
	}
	if row.LastCompletedEnqueue.Before(completedAt) {
		row.LastCompletedEnqueue = completedAt
	}
	s.rows[row.ID] = *row
	return nil
}

func scheduleCompletionInstant(last time.Time, trigger Trigger, now time.Time) time.Time {
	if trigger.Kind != KindEvery {
		return now
	}
	interval := time.Duration(trigger.N) * time.Minute
	if trigger.Unit == Hours {
		interval = time.Duration(trigger.N) * time.Hour
	}
	if interval <= 0 || now.Before(last) {
		return now
	}
	intervals := now.Sub(last) / interval
	if intervals < 1 {
		return now
	}
	return last.Add(intervals * interval)
}

func (s *Service) Update(ctx context.Context, id store.ScheduleID, trigger *Trigger, enabled *bool) (store.Schedule, error) {
	row, err := s.store.UpdateSchedule(ctx, id, trigger, enabled)
	if err != nil {
		return store.Schedule{}, err
	}
	s.mu.Lock()
	s.rows[id] = row
	s.mu.Unlock()
	return row, nil
}

func ValidateTrigger(trigger Trigger) error {
	return store.ValidateScheduleTrigger(trigger)
}

func Every(n int, unit Unit) Trigger {
	return Trigger{Kind: KindEvery, N: n, Unit: unit}
}

func Daily(hour, minute int) Trigger {
	return Trigger{Kind: KindDaily, Hour: hour, Minute: minute}
}

func Weekly(weekday time.Weekday, hour, minute int) Trigger {
	return Trigger{Kind: KindWeekly, Weekday: int(weekday), Hour: hour, Minute: minute}
}

func NextDue(last time.Time, trigger Trigger, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	switch trigger.Kind {
	case KindEvery:
		interval := time.Duration(trigger.N) * time.Minute
		if trigger.Unit == Hours {
			interval = time.Duration(trigger.N) * time.Hour
		}
		return last.Add(interval)
	case KindDaily:
		base := last.In(loc)
		next := time.Date(base.Year(), base.Month(), base.Day(), trigger.Hour, trigger.Minute, 0, 0, loc)
		if !next.After(base) {
			next = time.Date(base.Year(), base.Month(), base.Day()+1, trigger.Hour, trigger.Minute, 0, 0, loc)
		}
		return next
	case KindWeekly:
		base := last.In(loc)
		days := (trigger.Weekday - int(base.Weekday()) + 7) % 7
		next := time.Date(base.Year(), base.Month(), base.Day()+days, trigger.Hour, trigger.Minute, 0, 0, loc)
		if !next.After(base) {
			next = time.Date(base.Year(), base.Month(), base.Day()+days+7, trigger.Hour, trigger.Minute, 0, 0, loc)
		}
		return next
	default:
		return time.Time{}
	}
}

func ScanScheduleID(libraryID store.LibraryID) store.ScheduleID {
	return store.ScheduleID("scan:" + string(libraryID))
}

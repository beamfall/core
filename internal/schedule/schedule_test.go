package schedule

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

func TestTriggerValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		trigger Trigger
		wantErr bool
	}{
		{"every minutes", Every(30, Minutes), false},
		{"every hours", Every(12, Hours), false},
		{"every missing n", Trigger{Kind: KindEvery, Unit: Hours}, true},
		{"every unknown unit", Trigger{Kind: KindEvery, N: 1, Unit: "days"}, true},
		{"daily", Daily(3, 15), false},
		{"daily midnight", Daily(0, 15), false},
		{"daily range", Daily(24, 0), true},
		{"weekly", Weekly(time.Friday, 4, 30), false},
		{"weekly range", Weekly(7, 4, 30), true},
		{"unknown", Trigger{Kind: "cron"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateTrigger(tc.trigger)
			if tc.wantErr && err == nil {
				t.Fatal("ValidateTrigger succeeded, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateTrigger returned error: %v", err)
			}
		})
	}
}

func TestNextDue(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		last    time.Time
		trigger Trigger
		want    time.Time
	}{
		{
			name:    "every",
			last:    time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC),
			trigger: Every(12, Hours),
			want:    time.Date(2026, 6, 12, 22, 0, 0, 0, time.UTC),
		},
		{
			name:    "daily strictly after last",
			last:    time.Date(2026, 6, 12, 3, 0, 0, 0, loc),
			trigger: Daily(3, 0),
			want:    time.Date(2026, 6, 13, 3, 0, 0, 0, loc),
		},
		{
			name:    "weekly",
			last:    time.Date(2026, 6, 12, 5, 0, 0, 0, loc),
			trigger: Weekly(time.Friday, 4, 0),
			want:    time.Date(2026, 6, 19, 4, 0, 0, 0, loc),
		},
		{
			name:    "daily across spring DST",
			last:    time.Date(2026, 3, 7, 3, 0, 0, 0, loc),
			trigger: Daily(3, 0),
			want:    time.Date(2026, 3, 8, 3, 0, 0, 0, loc),
		},
		{
			name:    "weekly across fall DST",
			last:    time.Date(2026, 10, 31, 4, 0, 0, 0, loc),
			trigger: Weekly(time.Saturday, 4, 0),
			want:    time.Date(2026, 11, 7, 4, 0, 0, 0, loc),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NextDue(tc.last, tc.trigger, loc)
			if !got.Equal(tc.want) {
				t.Fatalf("NextDue = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestReconcileSchedules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()
	st := newMemoryStore()
	service := NewService(st)
	service.Register(Spec{ID: "database-backup", JobKind: "database-backup", DefaultTrigger: Daily(3, 0), BuildJobs: oneJob("database-backup")})

	if err := service.Reconcile(ctx, now); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	created := st.mustSchedule("database-backup")
	if created.Trigger.Kind != KindDaily || created.Trigger.Hour != 3 || !created.Enabled || created.Orphaned {
		t.Fatalf("created schedule = %#v", created)
	}

	editedTrigger := Every(6, Hours)
	enabled := false
	if _, err := st.UpdateSchedule(ctx, "database-backup", &editedTrigger, &enabled); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx, now.Add(time.Hour)); err != nil {
		t.Fatalf("second Reconcile returned error: %v", err)
	}
	existing := st.mustSchedule("database-backup")
	if existing.Trigger.N != 6 || existing.Enabled {
		t.Fatalf("existing schedule was overwritten: %#v", existing)
	}

	service = NewService(st)
	if err := service.Reconcile(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("orphan Reconcile returned error: %v", err)
	}
	if !st.mustSchedule("database-backup").Orphaned {
		t.Fatal("missing declarer did not mark row orphaned")
	}

	service.Register(Spec{ID: "database-backup", JobKind: "database-backup", DefaultTrigger: Daily(3, 0), BuildJobs: oneJob("database-backup")})
	if err := service.Reconcile(ctx, now.Add(3*time.Hour)); err != nil {
		t.Fatalf("return Reconcile returned error: %v", err)
	}
	returned := st.mustSchedule("database-backup")
	if returned.Orphaned || returned.Enabled {
		t.Fatalf("returned schedule = %#v, want unorphaned with user-disabled state", returned)
	}
}

func TestTickEnqueuesDueSchedulesOnce(t *testing.T) {
	t.Parallel()
	// feature:scheduler
	ctx := context.Background()
	st := newMemoryStore()
	service := NewService(st)
	service.Register(Spec{ID: "database-backup", JobKind: "database-backup", DefaultTrigger: Every(1, Hours), BuildJobs: oneJob("database-backup")})
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	if err := service.Reconcile(ctx, last); err != nil {
		t.Fatal(err)
	}

	due := last.Add(time.Hour)
	if err := service.Tick(ctx, due); err != nil {
		t.Fatalf("Tick returned error: %v", err)
	}
	if got := len(st.enqueued); got != 1 {
		t.Fatalf("enqueued = %d, want 1", got)
	}
	if err := service.Tick(ctx, due.Add(time.Minute)); err != nil {
		t.Fatalf("second Tick returned error: %v", err)
	}
	if got := len(st.enqueued); got != 1 {
		t.Fatalf("enqueued after second tick = %d, want still 1", got)
	}
}

func TestTickRetriesFailedEnqueueWithoutAdvancingCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore()
	service := NewService(st)
	service.Register(Spec{ID: "database-backup", JobKind: "database-backup", DefaultTrigger: Every(1, Hours), BuildJobs: oneJob("database-backup")})
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	if err := service.Reconcile(ctx, last); err != nil {
		t.Fatal(err)
	}

	st.enqueueErr = errors.New("write failed")
	now := last.Add(2 * time.Hour)
	if err := service.Tick(ctx, now); err != nil {
		t.Fatalf("failed enqueue Tick returned error: %v", err)
	}
	if !st.mustSchedule("database-backup").LastCompletedEnqueue.Equal(last) {
		t.Fatalf("failed enqueue advanced last: %#v", st.mustSchedule("database-backup"))
	}
	st.enqueueErr = nil
	if err := service.Tick(ctx, now); err != nil {
		t.Fatalf("retry Tick returned error: %v", err)
	}
	if got := len(st.enqueued); got != 1 {
		t.Fatalf("retry enqueued = %d, want 1", got)
	}
	if st.mustSchedule("database-backup").LastCompletedEnqueue.Equal(last) {
		t.Fatal("retry did not advance the completion cursor")
	}
}

func TestTickRetriesOnlyFailedJobsAfterPartialEnqueue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore()
	service := NewService(st)
	builds := 0
	service.Register(Spec{
		ID:             "multi-job",
		JobKind:        "first",
		DefaultTrigger: Every(1, Hours),
		BuildJobs: func(_ context.Context, now time.Time) ([]store.Job, error) {
			builds++
			return []store.Job{
				{Kind: "first", DedupeKey: "first", CreatedAt: now},
				{Kind: "second", DedupeKey: "second", CreatedAt: now},
			}, nil
		},
	})
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	if err := service.Reconcile(ctx, last); err != nil {
		t.Fatal(err)
	}
	st.enqueueErrors = map[store.JobKind]error{"second": errors.New("write failed")}

	if err := service.Tick(ctx, last.Add(time.Hour)); err != nil {
		t.Fatalf("partial Tick returned error: %v", err)
	}
	delete(st.enqueueErrors, "second")
	if err := service.Tick(ctx, last.Add(time.Hour+time.Minute)); err != nil {
		t.Fatalf("retry Tick returned error: %v", err)
	}

	if builds != 1 {
		t.Fatalf("BuildJobs calls = %d, want 1", builds)
	}
	if got := jobKinds(st.enqueueAttempts); !slices.Equal(got, []store.JobKind{"first", "second", "second"}) {
		t.Fatalf("enqueue attempts = %v, want first second second", got)
	}
	if got := jobKinds(st.enqueued); !slices.Equal(got, []store.JobKind{"first", "second"}) {
		t.Fatalf("enqueued jobs = %v, want first second", got)
	}
	if got := st.mustSchedule("multi-job").LastCompletedEnqueue; !got.Equal(last.Add(time.Hour)) {
		t.Fatalf("last completed enqueue = %s, want due instant %s", got, last.Add(time.Hour))
	}
}

func TestTickEveryScheduleAnchorsCompletionToLatestDueInstant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore()
	service := NewService(st)
	// No dedupe key: each anchored occurrence must visibly publish its own
	// job even though no worker drains the queue between ticks.
	service.Register(Spec{ID: "hourly", JobKind: "hourly", DefaultTrigger: Every(1, Hours), BuildJobs: func(_ context.Context, now time.Time) ([]store.Job, error) {
		return []store.Job{{Kind: "hourly", CreatedAt: now}}, nil
	}})
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	if err := service.Reconcile(ctx, last); err != nil {
		t.Fatal(err)
	}

	if err := service.Tick(ctx, last.Add(3*time.Hour+17*time.Minute)); err != nil {
		t.Fatalf("late Tick returned error: %v", err)
	}
	anchored := last.Add(3 * time.Hour)
	if got := st.mustSchedule("hourly").LastCompletedEnqueue; !got.Equal(anchored) {
		t.Fatalf("last completed enqueue = %s, want anchored %s", got, anchored)
	}
	if err := service.Tick(ctx, anchored.Add(59*time.Minute)); err != nil {
		t.Fatalf("pre-due Tick returned error: %v", err)
	}
	if got := len(st.enqueued); got != 1 {
		t.Fatalf("enqueued before next anchor = %d, want 1", got)
	}
	if err := service.Tick(ctx, anchored.Add(time.Hour)); err != nil {
		t.Fatalf("next anchored Tick returned error: %v", err)
	}
	if got := len(st.enqueued); got != 2 {
		t.Fatalf("enqueued at next anchor = %d, want 2", got)
	}
}

func TestTickAdvancesEmptyBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore()
	service := NewService(st)
	service.Register(Spec{ID: "scan:library-local", JobKind: "scan", DefaultTrigger: Every(12, Hours), BuildJobs: func(_ context.Context, _ time.Time) ([]store.Job, error) {
		return nil, nil
	}})
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	if err := service.Reconcile(ctx, last); err != nil {
		t.Fatal(err)
	}
	now := last.Add(13 * time.Hour)
	if err := service.Tick(ctx, now); err != nil {
		t.Fatalf("empty Tick returned error: %v", err)
	}
	if len(st.enqueued) != 0 || !st.mustSchedule("scan:library-local").LastCompletedEnqueue.Equal(last.Add(12*time.Hour)) {
		t.Fatalf("empty build state: enqueued=%d schedule=%#v", len(st.enqueued), st.mustSchedule("scan:library-local"))
	}
}

func TestTickSkipsDisabledOrOrphanedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	now := last.Add(13 * time.Hour)
	cases := []struct {
		name string
		row  store.Schedule
	}{
		{"disabled", store.Schedule{
			ID: "scan:library-local", JobKind: "scan", Trigger: Every(12, Hours),
			Enabled: false, LastCompletedEnqueue: last,
		}},
		{"orphaned", store.Schedule{
			ID: "scan:library-local", JobKind: "scan", Trigger: Every(12, Hours),
			Enabled: true, Orphaned: true, LastCompletedEnqueue: last,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := newMemoryStore()
			st.rows["scan:library-local"] = tc.row
			service := NewService(st)
			service.Register(Spec{ID: "scan:library-local", JobKind: "scan", DefaultTrigger: Every(12, Hours), BuildJobs: oneJob("scan")})
			if err := service.Tick(ctx, now); err != nil {
				t.Fatalf("Tick returned error: %v", err)
			}
			if len(st.enqueued) != 0 {
				t.Fatalf("%s schedule enqueued %d jobs", tc.name, len(st.enqueued))
			}
			if !st.mustSchedule("scan:library-local").LastCompletedEnqueue.Equal(last) {
				t.Fatalf("%s schedule advanced", tc.name)
			}
		})
	}
}

// TestTickKillMatrixRecoversExactlyOnce crashes the scheduler at EVERY
// enqueue boundary of a two-member occurrence — before the occurrence journal
// write, after it, between the two member publications, after publication but
// before journaled completion, and after journaled completion but before the
// catalog cursor update — then restarts with a fresh Service over the same
// persisted state. Every boundary must converge to exactly one publication
// per member and one cursor advance: no duplicate jobs, no missing jobs.
func TestTickKillMatrixRecoversExactlyOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	due := last.Add(time.Hour)
	// Persistent writes per healthy occurrence: begin journal, member 0,
	// member 1, journaled completion, catalog cursor.
	const occurrenceWrites = 5
	for crashAfter := 0; crashAfter <= occurrenceWrites; crashAfter++ {
		t.Run(fmt.Sprintf("crash-after-%d-writes", crashAfter), func(t *testing.T) {
			t.Parallel()
			st := newMemoryStore()
			builds := 0
			spec := Spec{
				ID:             "multi-job",
				JobKind:        "first",
				DefaultTrigger: Every(1, Hours),
				BuildJobs: func(_ context.Context, now time.Time) ([]store.Job, error) {
					builds++
					return []store.Job{
						{Kind: "first", DedupeKey: "first", CreatedAt: now},
						{Kind: "second", DedupeKey: "second", CreatedAt: now},
					}, nil
				},
			}
			service := NewService(st)
			service.Register(spec)
			if err := service.Reconcile(ctx, last); err != nil {
				t.Fatal(err)
			}

			st.failAfterWrites = crashAfter
			_ = service.Tick(ctx, due) // crash run: the boundary write and everything after it is refused

			// Restart: persisted journal survives, Service memory does not.
			st.failAfterWrites = -1
			restarted := NewService(st)
			restarted.Register(spec)
			if err := restarted.Reconcile(ctx, due.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := restarted.Tick(ctx, due.Add(time.Minute)); err != nil {
				t.Fatalf("recovery Tick returned error: %v", err)
			}

			if got := jobKinds(st.enqueued); !slices.Equal(got, []store.JobKind{"first", "second"}) {
				t.Fatalf("recovered jobs = %v, want exactly one first and one second", got)
			}
			if got := st.mustSchedule("multi-job").LastCompletedEnqueue; !got.Equal(due) {
				t.Fatalf("recovered cursor = %s, want due instant %s", got, due)
			}
			wantBuilds := 1
			if crashAfter == 0 {
				// The crash landed before the occurrence journal existed, so
				// the restart legitimately rebuilds the member set once.
				wantBuilds = 2
			}
			if builds != wantBuilds {
				t.Fatalf("BuildJobs calls = %d, want %d", builds, wantBuilds)
			}
		})
	}
}

// TestTickResumesAcrossRestartWithoutRebuilding proves the restart path reads
// the journaled member specs instead of re-running BuildJobs: a partial
// occurrence resumed by a brand-new Service publishes only the missing member.
func TestTickResumesAcrossRestartWithoutRebuilding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore()
	builds := 0
	spec := Spec{
		ID:             "multi-job",
		JobKind:        "first",
		DefaultTrigger: Every(1, Hours),
		BuildJobs: func(_ context.Context, now time.Time) ([]store.Job, error) {
			builds++
			return []store.Job{
				{Kind: "first", DedupeKey: "first", CreatedAt: now},
				{Kind: "second", DedupeKey: "second", CreatedAt: now},
			}, nil
		},
	}
	service := NewService(st)
	service.Register(spec)
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	if err := service.Reconcile(ctx, last); err != nil {
		t.Fatal(err)
	}
	st.enqueueErrors = map[store.JobKind]error{"second": errors.New("write failed")}
	if err := service.Tick(ctx, last.Add(time.Hour)); err != nil {
		t.Fatalf("partial Tick returned error: %v", err)
	}
	delete(st.enqueueErrors, "second")

	restarted := NewService(st)
	restarted.Register(spec)
	if err := restarted.Reconcile(ctx, last.Add(time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Tick(ctx, last.Add(time.Hour+time.Minute)); err != nil {
		t.Fatalf("restart Tick returned error: %v", err)
	}

	if builds != 1 {
		t.Fatalf("BuildJobs calls = %d, want 1 (restart must resume from the journal)", builds)
	}
	if got := jobKinds(st.enqueued); !slices.Equal(got, []store.JobKind{"first", "second"}) {
		t.Fatalf("enqueued jobs = %v, want first second", got)
	}
	if got := st.mustSchedule("multi-job").LastCompletedEnqueue; !got.Equal(last.Add(time.Hour)) {
		t.Fatalf("cursor = %s, want due instant %s", got, last.Add(time.Hour))
	}
}

// TestTickClockJumpNeverDuplicatesOrRewinds covers both jump directions: a
// backward jump after completion fires nothing and never rewinds the cursor,
// and a backward jump during recovery of a partial occurrence still finishes
// publication with the completion clamped to the occurrence's due instant.
func TestTickClockJumpNeverDuplicatesOrRewinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	last := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	t.Run("backward jump after completion", func(t *testing.T) {
		t.Parallel()
		st := newMemoryStore()
		service := NewService(st)
		service.Register(Spec{ID: "hourly", JobKind: "hourly", DefaultTrigger: Every(1, Hours), BuildJobs: oneJob("hourly")})
		if err := service.Reconcile(ctx, last); err != nil {
			t.Fatal(err)
		}
		due := last.Add(time.Hour)
		if err := service.Tick(ctx, due); err != nil {
			t.Fatal(err)
		}
		if err := service.Tick(ctx, last.Add(-6*time.Hour)); err != nil {
			t.Fatalf("backward Tick returned error: %v", err)
		}
		if got := len(st.enqueued); got != 1 {
			t.Fatalf("enqueued after backward jump = %d, want 1", got)
		}
		if got := st.mustSchedule("hourly").LastCompletedEnqueue; !got.Equal(due) {
			t.Fatalf("cursor after backward jump = %s, want %s (never rewinds)", got, due)
		}
	})

	t.Run("backward jump during partial recovery", func(t *testing.T) {
		t.Parallel()
		st := newMemoryStore()
		builds := 0
		spec := Spec{
			ID:             "multi-job",
			JobKind:        "first",
			DefaultTrigger: Every(1, Hours),
			BuildJobs: func(_ context.Context, now time.Time) ([]store.Job, error) {
				builds++
				return []store.Job{
					{Kind: "first", DedupeKey: "first", CreatedAt: now},
					{Kind: "second", DedupeKey: "second", CreatedAt: now},
				}, nil
			},
		}
		service := NewService(st)
		service.Register(spec)
		if err := service.Reconcile(ctx, last); err != nil {
			t.Fatal(err)
		}
		due := last.Add(time.Hour)
		st.enqueueErrors = map[store.JobKind]error{"second": errors.New("write failed")}
		if err := service.Tick(ctx, due); err != nil {
			t.Fatal(err)
		}
		delete(st.enqueueErrors, "second")

		restarted := NewService(st)
		restarted.Register(spec)
		if err := restarted.Tick(ctx, last.Add(-6*time.Hour)); err != nil {
			t.Fatalf("backward recovery Tick returned error: %v", err)
		}
		if got := jobKinds(st.enqueued); !slices.Equal(got, []store.JobKind{"first", "second"}) {
			t.Fatalf("recovered jobs = %v, want first second", got)
		}
		if got := st.mustSchedule("multi-job").LastCompletedEnqueue; !got.Equal(due) {
			t.Fatalf("cursor = %s, want clamped due instant %s", got, due)
		}
		if builds != 1 {
			t.Fatalf("BuildJobs calls = %d, want 1", builds)
		}
	})
}

func oneJob(kind store.JobKind) func(context.Context, time.Time) ([]store.Job, error) {
	return func(_ context.Context, now time.Time) ([]store.Job, error) {
		return []store.Job{{Kind: kind, DedupeKey: string(kind), CreatedAt: now}}, nil
	}
}

// memoryStore is a journal-faithful in-memory double of the store's schedule
// surface: occurrence identity and member publication marks persist across
// Service instances (a "restart" builds a new Service over the same
// memoryStore), publication is atomic per member, cursor updates are
// monotonic, and failAfterWrites refuses the Nth-and-later persistent writes
// to emulate a crash at an exact enqueue boundary.
type memoryStore struct {
	rows            map[store.ScheduleID]store.Schedule
	occurrences     []store.ScheduleOccurrence
	enqueued        []store.Job
	enqueueAttempts []store.Job
	enqueueErr      error
	enqueueErrors   map[store.JobKind]error
	// failAfterWrites < 0 means unlimited; otherwise that many journal/cursor
	// writes succeed and every later one is refused.
	failAfterWrites int
	jobSequence     int
}

var errMemoryStoreCrash = errors.New("persistent write refused")

func newMemoryStore() *memoryStore {
	return &memoryStore{rows: map[store.ScheduleID]store.Schedule{}, failAfterWrites: -1}
}

func (m *memoryStore) writeGate() error {
	if m.failAfterWrites < 0 {
		return nil
	}
	if m.failAfterWrites == 0 {
		return errMemoryStoreCrash
	}
	m.failAfterWrites--
	return nil
}

func (m *memoryStore) ListSchedules(context.Context) ([]store.Schedule, error) {
	rows := make([]store.Schedule, 0, len(m.rows))
	for _, row := range m.rows {
		rows = append(rows, row)
	}
	return rows, nil
}

func (m *memoryStore) CreateSchedule(_ context.Context, row store.Schedule) error {
	m.rows[row.ID] = row
	return nil
}

func (m *memoryStore) UpdateSchedule(_ context.Context, id store.ScheduleID, trigger *Trigger, enabled *bool) (store.Schedule, error) {
	row := m.rows[id]
	if trigger != nil {
		row.Trigger = *trigger
	}
	if enabled != nil {
		row.Enabled = *enabled
	}
	m.rows[id] = row
	return row, nil
}

func (m *memoryStore) SetScheduleOrphaned(_ context.Context, id store.ScheduleID, orphaned bool) error {
	row := m.rows[id]
	row.Orphaned = orphaned
	m.rows[id] = row
	return nil
}

func (m *memoryStore) MarkScheduleCompleted(_ context.Context, id store.ScheduleID, now time.Time) error {
	if err := m.writeGate(); err != nil {
		return err
	}
	row := m.rows[id]
	if row.LastCompletedEnqueue.Before(now) {
		row.LastCompletedEnqueue = now
	}
	m.rows[id] = row
	return nil
}

func (m *memoryStore) LatestScheduleOccurrence(_ context.Context, id store.ScheduleID) (store.ScheduleOccurrence, bool, error) {
	index := m.latestOccurrenceIndex(id)
	if index < 0 {
		return store.ScheduleOccurrence{}, false, nil
	}
	return copyOccurrence(m.occurrences[index]), true, nil
}

func (m *memoryStore) BeginScheduleOccurrence(_ context.Context, id store.ScheduleID, due, started time.Time, jobs []store.Job) (store.ScheduleOccurrence, error) {
	if err := m.writeGate(); err != nil {
		return store.ScheduleOccurrence{}, err
	}
	for index, occurrence := range m.occurrences {
		if occurrence.ScheduleID == id && occurrence.Due.Equal(due) {
			return copyOccurrence(m.occurrences[index]), nil
		}
	}
	occurrence := store.ScheduleOccurrence{ScheduleID: id, Due: due, StartedAt: started}
	for ordinal, job := range jobs {
		occurrence.Members = append(occurrence.Members, store.ScheduleOccurrenceMember{
			Ordinal:   ordinal,
			Kind:      job.Kind,
			Class:     job.Class,
			Priority:  job.Priority,
			DedupeKey: job.DedupeKey,
			Payload:   job.Payload,
			CreatedAt: job.CreatedAt,
			RunAfter:  job.RunAfter,
		})
	}
	m.occurrences = append(m.occurrences, occurrence)
	return copyOccurrence(occurrence), nil
}

func (m *memoryStore) EnqueueScheduleOccurrenceMember(_ context.Context, id store.ScheduleID, due time.Time, ordinal int, now time.Time) (store.Job, error) {
	occurrence := m.findOccurrence(id, due)
	if occurrence == nil || ordinal < 0 || ordinal >= len(occurrence.Members) {
		return store.Job{}, errors.New("schedule occurrence member not found")
	}
	member := &occurrence.Members[ordinal]
	if member.JobID != "" {
		return m.jobByID(member.JobID)
	}
	attempt := store.Job{Kind: member.Kind, DedupeKey: member.DedupeKey, Payload: member.Payload, CreatedAt: member.CreatedAt}
	m.enqueueAttempts = append(m.enqueueAttempts, attempt)
	if err := m.writeGate(); err != nil {
		return store.Job{}, err
	}
	if m.enqueueErr != nil {
		return store.Job{}, m.enqueueErr
	}
	if err := m.enqueueErrors[member.Kind]; err != nil {
		return store.Job{}, err
	}
	for _, existing := range m.enqueued {
		if member.DedupeKey != "" && existing.Kind == member.Kind && existing.DedupeKey == member.DedupeKey {
			member.JobID = existing.ID
			member.EnqueuedAt = now
			return existing, nil
		}
	}
	m.jobSequence++
	published := attempt
	published.ID = store.JobID(fmt.Sprintf("job-%d", m.jobSequence))
	m.enqueued = append(m.enqueued, published)
	member.JobID = published.ID
	member.EnqueuedAt = now
	return published, nil
}

func (m *memoryStore) CompleteScheduleOccurrence(_ context.Context, id store.ScheduleID, due, completedAt time.Time) error {
	if err := m.writeGate(); err != nil {
		return err
	}
	occurrence := m.findOccurrence(id, due)
	if occurrence == nil {
		return errors.New("schedule occurrence not found")
	}
	for _, member := range occurrence.Members {
		if member.JobID == "" {
			return errors.New("schedule occurrence has unpublished members")
		}
	}
	if occurrence.CompletedAt.IsZero() {
		occurrence.CompletedAt = completedAt
	}
	kept := m.occurrences[:0]
	for _, candidate := range m.occurrences {
		if candidate.ScheduleID == id && !candidate.CompletedAt.IsZero() && candidate.Due.Before(due) {
			continue
		}
		kept = append(kept, candidate)
	}
	m.occurrences = kept
	return nil
}

func (m *memoryStore) findOccurrence(id store.ScheduleID, due time.Time) *store.ScheduleOccurrence {
	for index := range m.occurrences {
		if m.occurrences[index].ScheduleID == id && m.occurrences[index].Due.Equal(due) {
			return &m.occurrences[index]
		}
	}
	return nil
}

func (m *memoryStore) latestOccurrenceIndex(id store.ScheduleID) int {
	latest := -1
	for index := range m.occurrences {
		if m.occurrences[index].ScheduleID != id {
			continue
		}
		if latest < 0 || m.occurrences[index].Due.After(m.occurrences[latest].Due) {
			latest = index
		}
	}
	return latest
}

func (m *memoryStore) jobByID(id store.JobID) (store.Job, error) {
	for _, job := range m.enqueued {
		if job.ID == id {
			return job, nil
		}
	}
	return store.Job{}, errors.New("job not found")
}

func copyOccurrence(occurrence store.ScheduleOccurrence) store.ScheduleOccurrence {
	copied := occurrence
	copied.Members = slices.Clone(occurrence.Members)
	return copied
}

func jobKinds(jobs []store.Job) []store.JobKind {
	kinds := make([]store.JobKind, len(jobs))
	for i, job := range jobs {
		kinds[i] = job.Kind
	}
	return kinds
}

func (m *memoryStore) mustSchedule(id store.ScheduleID) store.Schedule {
	row, ok := m.rows[id]
	if !ok {
		panic("missing schedule " + id)
	}
	return row
}

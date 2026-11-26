package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	schedulepkg "github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestClassifyBackupError(t *testing.T) {
	cases := []struct {
		err  error
		want BackupFailureClass
	}{
		{&os.PathError{Op: "write", Err: syscall.ENOSPC}, BackupFailureDestinationFull},
		{&os.PathError{Op: "mkdir", Err: syscall.EACCES}, BackupFailureUnwritable},
		{&os.PathError{Op: "mkdir", Err: syscall.EROFS}, BackupFailureUnwritable},
		{errors.New("sqlite busy"), BackupFailureSnapshot},
		{&backupClassedError{class: BackupFailureIntegrity, err: errors.New("x")}, BackupFailureIntegrity},
	}
	for _, tc := range cases {
		if got := classifyBackupError(tc.err); got != tc.want {
			t.Errorf("classify(%v) = %s, want %s", tc.err, got, tc.want)
		}
	}
}

// A full destination fails the Job with the destination-full class and leaves
// every prior set intact.
func TestDatabaseBackupHandlerFullDestinationKeepsPriorBackups(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir, store.Options{SeedDevelopmentData: true, AllowedRoots: []string{dest}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(dataDir))

	handler := databaseBackupHandler(st, dataDir, dest, nil)
	if _, err := handler(ctx, store.Job{ID: "j1", Kind: databaseBackupJobKind}); err != nil {
		t.Fatal(err)
	}
	before, err := st.BackupHistory(ctx, dest)
	if err != nil || len(before) != 1 {
		t.Fatalf("history = %v, %v", before, err)
	}

	orig := backupFreeBytes
	backupFreeBytes = func(string) (uint64, bool) { return 0, true }
	defer func() { backupFreeBytes = orig }()

	var logs bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	_, err = handler(ctx, store.Job{ID: "j2", Kind: databaseBackupJobKind})
	slog.SetDefault(prevLogger)
	if err == nil || classifyBackupError(err) != BackupFailureDestinationFull {
		t.Fatalf("err = %v, want destination-full", err)
	}
	// The failure is logged with its class...
	if !strings.Contains(logs.String(), `"msg":"scheduled backup failed"`) || !strings.Contains(logs.String(), `"backup_failure_class":"destination-full"`) {
		t.Fatalf("missing classified failure log line:\n%s", logs.String())
	}
	// ...emitted to the webhook outbox with a content-free payload...
	failed := backupOutboxEvents(t, st, store.BackupEventFailed)
	if len(failed) != 1 || !strings.Contains(string(failed[0].Payload), `"failureClass":"destination-full"`) {
		t.Fatalf("backup.failed outbox events = %+v", failed)
	}
	if strings.Contains(string(failed[0].Payload), dest) {
		t.Fatalf("backup event payload leaks the destination path: %s", failed[0].Payload)
	}
	if got := backupOutboxEvents(t, st, store.BackupEventCompleted); len(got) != 1 {
		t.Fatalf("backup.completed outbox events = %d, want 1 for the first run", len(got))
	}
	// ...and raises the failed-backup banner, not the integrity one.
	status, err := backupcore.LoadStatus(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !status.BannerActive(backupcore.BannerBackupFailed) || status.BannerActive(backupcore.BannerIntegrityFailed) {
		t.Fatalf("banners after destination-full = %+v", status)
	}
	if status.LastFailure == nil || status.LastFailure.FailureClass != string(BackupFailureDestinationFull) {
		t.Fatalf("last failure = %+v", status.LastFailure)
	}
	after, err := st.BackupHistory(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Directory != before[0].Directory || !after[0].Valid {
		t.Fatalf("prior backup changed after failed run: before=%v after=%v", before, after)
	}
}

func backupOutboxEvents(t *testing.T, st *store.Store, kind string) []store.OutboxEvent {
	t.Helper()
	events, err := st.ReadEventOutbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.OutboxEvent
	for _, e := range events {
		if e.Kind == kind {
			if e.AggregateType != store.OutboxAggregateBackup {
				t.Fatalf("backup event aggregate = %q", e.AggregateType)
			}
			out = append(out, e)
		}
	}
	return out
}

// An integrity failure raises its own banner and webhook kind, distinct from a
// failed backup (ADR-0192 §6), and a later verified backup supersedes it.
func TestReportBackupOutcomeIntegrityFailureIsDistinct(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	dataDir := t.TempDir()

	reportBackupOutcome(ctx, st, dataDir, BackupFailureIntegrity, "/data/backups/beamfall-backup-1")
	if got := backupOutboxEvents(t, st, store.BackupEventIntegrityFailed); len(got) != 1 || got[0].AggregateID != "beamfall-backup-1" {
		t.Fatalf("integrity events = %+v", got)
	}
	if got := backupOutboxEvents(t, st, store.BackupEventFailed); len(got) != 0 {
		t.Fatalf("integrity failure also emitted backup.failed: %+v", got)
	}
	status, err := backupcore.LoadStatus(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !status.BannerActive(backupcore.BannerIntegrityFailed) || status.BannerActive(backupcore.BannerBackupFailed) {
		t.Fatalf("banners after integrity failure = %+v", status)
	}

	time.Sleep(2 * time.Millisecond)
	reportBackupOutcome(ctx, st, dataDir, "", "/data/backups/beamfall-backup-2")
	status, err = backupcore.LoadStatus(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.BannerActive(backupcore.BannerIntegrityFailed) {
		t.Fatal("a later verified backup did not supersede the integrity banner")
	}
}

func TestBackupScheduleTriggerStaysInWindow(t *testing.T) {
	for m := range backupJitterWindowMinutes {
		tr := backupScheduleTrigger(func(int) int { return m })
		start := tr.Hour*60 + tr.Minute
		if tr.Kind != schedulepkg.KindDaily || start < 2*60 || start >= 3*60+30 {
			t.Fatalf("jitter %d gave trigger %+v outside 02:00-03:29", m, tr)
		}
	}
	if tr := backupScheduleTrigger(func(int) int { return 500 }); tr.Hour != 2 || tr.Minute != 0 {
		t.Fatalf("out-of-range jitter = %+v, want 02:00", tr)
	}
}

// The built-in daily backup Schedule fires under a controlled clock: nothing
// before its seeded minute, exactly one database-backup Job once due, and no
// duplicate on the next tick. (TestScheduledDatabaseBackupEnqueuesAndWrites-
// ThroughWorker drives the enqueued Job through the real worker.)
func TestScheduledBackupFiresUnderControlledClock(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	service := schedulepkg.NewService(st)
	registerBuiltInSchedules(ctx, st, service, t.TempDir(), 0, false)

	day := time.Date(2026, 11, 25, 1, 0, 0, 0, time.Local)
	if err := service.Reconcile(ctx, day); err != nil {
		t.Fatal(err)
	}
	row, err := st.Schedule(ctx, store.ScheduleID(databaseBackupJobKind))
	if err != nil {
		t.Fatal(err)
	}
	if row.Trigger.Kind != schedulepkg.KindDaily || row.Trigger.Hour < 2 || row.Trigger.Hour > 3 || !row.Enabled {
		t.Fatalf("seeded backup schedule = %+v", row)
	}
	due := time.Date(2026, 11, 25, row.Trigger.Hour, row.Trigger.Minute, 0, 0, time.Local)

	backupJobs := func() int {
		jobs, err := st.ListJobs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, j := range jobs {
			if j.Kind == databaseBackupJobKind {
				n++
			}
		}
		return n
	}
	for _, step := range []struct {
		at   time.Time
		want int
	}{
		{due.Add(-time.Minute), 0},
		{due, 1},
		{due.Add(time.Minute), 1},
	} {
		if err := service.Tick(ctx, step.at); err != nil {
			t.Fatal(err)
		}
		if got := backupJobs(); got != step.want {
			t.Fatalf("at %s: %d database-backup jobs, want %d", step.at.Format(time.TimeOnly), got, step.want)
		}
	}

	// The next night's fire while the first backup is still outstanding is
	// deduped onto it: two backups never queue or run at once.
	nextDay := due.AddDate(0, 0, 1)
	if err := service.Tick(ctx, nextDay); err != nil {
		t.Fatal(err)
	}
	if got := backupJobs(); got != 1 {
		t.Fatalf("next-day re-fire with a backup outstanding: %d database-backup jobs, want 1", got)
	}
	// Once the outstanding backup finishes, the following night fires anew.
	for {
		job, err := st.ClaimNextJob(ctx, "test-worker", nil, nextDay)
		if err != nil || job.ID == "" {
			break
		}
		if err := st.CompleteJob(ctx, job.ID, job.LeaseOwner, nextDay); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Tick(ctx, due.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if got := backupJobs(); got != 2 {
		t.Fatalf("night after the backup completed: %d database-backup jobs, want 2", got)
	}
}

type corruptVerifySealer struct {
	backupcore.DirectorySealer
}

func (corruptVerifySealer) VerifyEnvelopeStructure(io.Reader) error {
	return errors.New("simulated envelope verification failure")
}

// CORE-3516 #15: scheduled integrity failures preserve the created Backup Set
// name in LastIntegrityFailure and backup.integrity_failed outbox events.
func TestDatabaseBackupHandlerIntegrityFailureRecordsCreatedSetName(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir, store.Options{SeedDevelopmentData: true, AllowedRoots: []string{dest}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	st.SetSnapshotSealer(corruptVerifySealer{DirectorySealer: backupcore.NewDirectorySealer(dataDir)})

	handler := databaseBackupHandler(st, dataDir, dest, nil)
	if _, err := handler(ctx, store.Job{ID: "j-int", Kind: databaseBackupJobKind}); err == nil || classifyBackupError(err) != BackupFailureIntegrity {
		t.Fatalf("err = %v, want integrity failure", err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) == 0 {
		t.Fatalf("read backup dest: %+v, %v", entries, err)
	}
	wantSet := filepath.Base(entries[0].Name())
	status, err := backupcore.LoadStatus(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastIntegrityFailure == nil || status.LastIntegrityFailure.Set != wantSet {
		t.Fatalf("LastIntegrityFailure = %+v, want Set=%q", status.LastIntegrityFailure, wantSet)
	}
	events := backupOutboxEvents(t, st, store.BackupEventIntegrityFailed)
	if len(events) != 1 || events[0].AggregateID != wantSet {
		t.Fatalf("integrity failed events = %+v, want AggregateID=%q", events, wantSet)
	}
}

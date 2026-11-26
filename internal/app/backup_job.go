package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/jobs"
	schedulepkg "github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/store"
)

// BackupFailureClass is the ADR-0192 §6 failure taxonomy for the scheduled
// backup Job. Structured log lines carry it as backup_failure_class.
type BackupFailureClass string

const (
	BackupFailureDestinationFull BackupFailureClass = "destination-full"
	BackupFailureUnwritable      BackupFailureClass = "unwritable"
	BackupFailureSnapshot        BackupFailureClass = "snapshot-error"
	BackupFailureIntegrity       BackupFailureClass = "integrity-failure"
)

// backupClassedError carries the class of a failed scheduled backup.
type backupClassedError struct {
	class BackupFailureClass
	err   error
}

func (e *backupClassedError) Error() string { return fmt.Sprintf("%s: %v", e.class, e.err) }
func (e *backupClassedError) Unwrap() error { return e.err }

// classifyBackupError maps a backup error to a failure class.
func classifyBackupError(err error) BackupFailureClass {
	var classed *backupClassedError
	switch {
	case errors.As(err, &classed):
		return classed.class
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return BackupFailureDestinationFull
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EROFS):
		return BackupFailureUnwritable
	default:
		return BackupFailureSnapshot
	}
}

// backupFreeBytes reports free bytes on the filesystem holding dir (the nearest
// existing ancestor when dir does not exist yet); ok is false when unknown.
// Swapped in tests.
var backupFreeBytes = func(dir string) (uint64, bool) {
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 0, false
		}
		dir = parent
	}
	free, ok, err := freeBytesOnFS(dir)
	return free, ok && err == nil
}

// backupSpacePrecheck refuses to start a backup the destination cannot hold:
// it needs about the size of the newest existing set (1x) free.
func backupSpacePrecheck(ctx context.Context, st *store.Store, dest string) error {
	history, err := st.BackupHistory(ctx, dest)
	if err != nil || len(history) == 0 {
		return nil
	}
	need := history[0].SizeBytes
	if free, ok := backupFreeBytes(dest); ok && free < math.MaxInt64 && need > int64(free) {
		return &backupClassedError{class: BackupFailureDestinationFull, err: fmt.Errorf("destination has %d bytes free, newest set is %d", free, need)}
	}
	return nil
}

// backupJitterWindowMinutes is the span of the seeded backup start time,
// 02:00-03:29 local: inside ADR-0192 §6's jittered night window and before the
// 03:30 events compaction, whose pre-compaction state the backup captures.
const backupJitterWindowMinutes = 90

// backupScheduleTrigger is the default daily trigger for the backup Schedule,
// jittered so a fleet of installs does not start at the same minute. The
// schedule catalog seeds it once per install and never rewrites an existing
// row, so the chosen minute is stable across restarts.
func backupScheduleTrigger(intn func(int) int) schedulepkg.Trigger {
	m := intn(backupJitterWindowMinutes)
	if m < 0 || m >= backupJitterWindowMinutes {
		m = 0
	}
	return schedulepkg.Daily(2+m/60, m%60)
}

// databaseBackupHandler runs the scheduled backup (BKUP-12): space precheck,
// backup, Layer-2 integrity verification of the new set (BKUP-11), then policy
// retention. A failure at any step leaves every prior set untouched: pruning
// runs only after a verified backup. Every outcome is logged with its class,
// recorded in the backup status file under dataDir (the ADR-0192 §6 banners)
// and appended to the event outbox for webhooks.
func databaseBackupHandler(st *store.Store, dataDir, dest string, encryptedBackupCipher func() store.EncryptedLibraryDBCipher) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var createdSet string
		fail := func(err error) (jobs.Result, error) {
			class := classifyBackupError(err)
			slog.ErrorContext(ctx, "scheduled backup failed", "backup_failure_class", string(class), "dest", dest, "error", err)
			reportBackupOutcome(ctx, st, dataDir, class, createdSet)
			var classed *backupClassedError
			if errors.As(err, &classed) {
				return jobs.Result{}, err
			}
			return jobs.Result{}, &backupClassedError{class: class, err: err}
		}
		if err := backupSpacePrecheck(ctx, st, dest); err != nil {
			return fail(err)
		}
		var (
			result store.BackupResult
			err    error
		)
		if encryptedBackupCipher != nil {
			result, err = st.BackupWithOptions(ctx, dest, store.BackupOptions{EncryptedLibraryDBCipher: encryptedBackupCipher()})
		} else {
			result, err = st.Backup(ctx, dest)
		}
		if err != nil {
			return fail(err)
		}
		createdSet = result.Directory
		if err := st.VerifyBackupSet(ctx, result.Manifest); err != nil {
			return fail(&backupClassedError{class: BackupFailureIntegrity, err: err})
		}
		// Pruning is non-fatal: a cleanup failure must not retry and write
		// another full backup.
		pruned, pruneErr := st.PruneBackupsByPolicy(ctx, dest, time.Now().UTC())
		if pruneErr != nil {
			slog.WarnContext(ctx, "backup retention prune failed", "dest", dest, "error", pruneErr)
		}
		slog.InfoContext(ctx, "scheduled backup completed", "dest", dest, "set", result.Directory, "pruned", pruned)
		// Reached only after VerifyBackupSet passed on the new set.
		reportBackupOutcome(ctx, st, dataDir, "", result.Directory)
		return jobs.Result{}, nil
	}
}

// reportBackupOutcome persists the outcome for the console banners and emits
// the webhook event. Both are best-effort: a reporting failure is logged and
// never changes the Job's own outcome.
func reportBackupOutcome(ctx context.Context, st *store.Store, dataDir string, class BackupFailureClass, set string) {
	now := time.Now().UTC()
	integrity := class == BackupFailureIntegrity
	if dataDir != "" {
		// A success is reported only after the Layer-2 check passed.
		run := backupcore.RunRecord{At: now, FailureClass: string(class), Set: set, IntegrityVerified: class == ""}
		if _, err := backupcore.RecordRun(dataDir, run, integrity); err != nil {
			slog.WarnContext(ctx, "backup status write failed", "error", err)
		}
	}
	kind := store.BackupEventCompleted
	switch {
	case integrity:
		kind = store.BackupEventIntegrityFailed
	case class != "":
		kind = store.BackupEventFailed
	}
	if err := st.AppendBackupEvent(ctx, kind, store.BackupEventPayload{FailureClass: string(class), Set: set}, now); err != nil {
		slog.WarnContext(ctx, "backup event append failed", "error", err)
	}
}

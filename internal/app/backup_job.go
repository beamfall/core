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

	"github.com/beamfall/core/internal/jobs"
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

// databaseBackupHandler runs the scheduled backup (BKUP-12): space precheck,
// backup, Layer-2 integrity verification of the new set (BKUP-11), then policy
// retention. A failure at any step leaves every prior set untouched: pruning
// runs only after a verified backup, and a failure is logged with its class.
func databaseBackupHandler(st *store.Store, dest string, encryptedBackupCipher func() store.EncryptedLibraryDBCipher) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		fail := func(err error) (jobs.Result, error) {
			class := classifyBackupError(err)
			slog.ErrorContext(ctx, "scheduled backup failed", "backup_failure_class", string(class), "dest", dest, "error", err)
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
		return jobs.Result{}, nil
	}
}

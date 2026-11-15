package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/beamfall/core/internal/restorestage"
	"github.com/beamfall/core/internal/store"
)

// errStagedRestoreUnsafe marks a staged restore failure that left the data
// directory missing or the original only in the rollback copy. Starting then
// would boot an empty server beside the preserved original, so the start
// stops and the restore journal stays for the next start to refuse on.
var errStagedRestoreUnsafe = errors.New("staged restore failed and the original data was not put back")

// applyStagedRestore runs a restore an admin staged from the console
// (CORE-1720). The caller holds the data directory's server lock (release),
// so no other server can open the data while it is replaced. It reuses the
// offline restore path: verify, identity preflight, rename the live data
// directory to a rollback copy, restore, and on failure put the original
// back. The outcome is recorded in the data directory for the console.
//
// It returns the release for the lock it now holds: a successful restore
// replaces the data directory, so the lock moves to the restored one. On error
// the lock is released and the server must not start.
func applyStagedRestore(ctx context.Context, dataDir string, logger *slog.Logger, release func()) (func(), error) {
	marker, ok, err := restorestage.Pending(dataDir)
	if err != nil {
		logger.Error("staged restore could not be read; starting on the current data", "error", err)
		return release, nil
	}
	if !ok {
		return release, nil
	}
	result := restorestage.Result{
		AppliedAt:       time.Now().UTC().Format(time.RFC3339),
		BackupCreatedAt: marker.Summary.CreatedAt,
	}
	snapshot, restoreErr := runStagedRestore(ctx, dataDir, marker.ReplaceIdentity)
	switch {
	case errors.Is(restoreErr, errStagedRestoreUnsafe):
		release()
		return nil, restoreErr
	case restoreErr != nil:
		result.Error = restoreErr.Error()
		logger.Error("staged restore failed; starting on the original data", "error", restoreErr)
	default:
		// The held lock file moved with the original into the rollback
		// copy; take the restored directory's lock before the journal goes,
		// so a server racing this start still finds the journal and stops.
		restored, lockErr := acquireServerLock(dataDir)
		release()
		// The restore is complete and the rollback copy whole, so the
		// journal goes even when another start took the restored directory
		// first; keeping it would refuse every later start.
		if err := restorestage.ClearJournal(dataDir); err != nil {
			if restored != nil {
				restored()
			}
			return nil, fmt.Errorf("staged restore applied but its journal could not be cleared: %w", err)
		}
		result.OK = true
		result.RollbackCopy = snapshot
		logger.Info("staged restore applied", "rollback_copy", snapshot)
		if lockErr != nil {
			if err := restorestage.WriteResult(dataDir, result); err != nil {
				logger.Error("staged restore result could not be recorded", "error", err)
			}
			return nil, fmt.Errorf("staged restore applied, but the restored data directory could not be locked: %w", lockErr)
		}
		release = restored
	}
	if err := restorestage.WriteResult(dataDir, result); err != nil {
		logger.Error("staged restore result could not be recorded", "error", err)
	}
	return release, nil
}

// runStagedRestore claims the staged set and restores it, journaling the
// rollback copy's path before the live data directory is renamed to it. It
// clears the journal on a failure that left the original in place and wraps
// errStagedRestoreUnsafe, keeping the journal, on one that did not. On
// success the journal stays for the caller to clear once it holds the lock.
func runStagedRestore(ctx context.Context, dataDir string, replaceIdentity bool) (string, error) {
	claimed, manifest, err := restorestage.Claim(dataDir, time.Now())
	if err != nil {
		return "", err
	}
	// The claimed set is a decrypted copy; the encrypted set stays in its
	// backup folder, so the copy is removed whatever the outcome.
	defer os.RemoveAll(claimed)
	if err := store.VerifyBackupManifest(ctx, manifest); err != nil {
		return "", err
	}
	if err := store.PreflightRestoreIdentity(ctx, manifest, dataDir, replaceIdentity); err != nil {
		return "", err
	}
	snapshot := newRestoreSnapshotPath(dataDir)
	journal := restorestage.Journal{StartedAt: time.Now().UTC().Format(time.RFC3339), Snapshot: snapshot}
	if err := restorestage.WriteJournal(dataDir, journal); err != nil {
		return "", err
	}
	if _, err := snapshotRestoreDataDirTo(dataDir, snapshot); err != nil {
		return "", settleFailedStagedRestore(dataDir, snapshot, err)
	}
	if err := restoreBackupFn(ctx, manifest, dataDir); err != nil {
		return snapshot, settleFailedStagedRestore(dataDir, snapshot, rollbackFailedRestoreDataDir(dataDir, snapshot, err))
	}
	return snapshot, nil
}

// settleFailedStagedRestore clears the journal when the original data is
// back in the data directory and otherwise keeps it and marks the failure
// unsafe to start on.
func settleFailedStagedRestore(dataDir, snapshot string, failure error) error {
	_, snapErr := os.Lstat(snapshot)
	_, dataErr := os.Lstat(dataDir)
	if snapErr == nil || dataErr != nil {
		return fmt.Errorf("%w: %w", errStagedRestoreUnsafe, failure)
	}
	if err := restorestage.ClearJournal(dataDir); err != nil {
		return fmt.Errorf("%w: %w; the original data is back but the restore journal could not be cleared: %w", errStagedRestoreUnsafe, failure, err)
	}
	return failure
}

// refuseInterruptedRestore stops a start while a staged restore's journal
// exists: another start is replacing the data directory, or one stopped
// partway and the original data may be only in the rollback copy.
func refuseInterruptedRestore(dataDir string) error {
	journal, ok, err := restorestage.ReadJournal(dataDir)
	if !ok {
		return nil
	}
	if err != nil {
		return fmt.Errorf("a staged restore journal exists at %s and could not be read (%w); a restore is running or was interrupted. Inspect the data directory and remove the journal to start", restorestage.JournalPath(dataDir), err)
	}
	return fmt.Errorf("a staged restore is running or was interrupted: the original data is at %s. Stop any other server, keep the data directory %s or move the original back to it, then remove %s to start", journal.Snapshot, dataDir, restorestage.JournalPath(dataDir))
}

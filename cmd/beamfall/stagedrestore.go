package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/beamfall/core/internal/restorestage"
	"github.com/beamfall/core/internal/store"
)

// applyStagedRestore runs a restore an admin staged from the console
// (CORE-1720) before the server takes its lock and opens the store. It reuses
// the offline restore path: verify, identity preflight, rename the live data
// directory to a rollback copy, restore, and on failure put the original back.
// The outcome is recorded in the data directory for the console to report.
//
// A staged restore that fails leaves the original data in place and the
// server starts on it. The one case that stops the start is a failure whose
// rollback did not put the original back: starting then would create an
// empty data directory beside the preserved original.
func applyStagedRestore(ctx context.Context, dataDir string, logger *slog.Logger) error {
	marker, ok, err := restorestage.Pending(dataDir)
	if err != nil {
		logger.Error("staged restore could not be read; starting on the current data", "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	result := restorestage.Result{
		AppliedAt:       time.Now().UTC().Format(time.RFC3339),
		BackupCreatedAt: marker.Summary.CreatedAt,
	}
	snapshot, restoreErr := runStagedRestore(ctx, dataDir, marker.ReplaceIdentity)
	if restoreErr != nil {
		if snapshot != "" {
			if _, statErr := os.Lstat(snapshot); statErr == nil {
				return fmt.Errorf("staged restore failed and the original data was not put back: %w", restoreErr)
			}
		}
		result.Error = restoreErr.Error()
		logger.Error("staged restore failed; starting on the original data", "error", restoreErr)
	} else {
		result.OK = true
		result.RollbackCopy = snapshot
		logger.Info("staged restore applied", "rollback_copy", snapshot)
	}
	if err := restorestage.WriteResult(dataDir, result); err != nil {
		logger.Error("staged restore result could not be recorded", "error", err)
	}
	return nil
}

// runStagedRestore claims the staged set and restores it. It returns the
// rollback copy's path once the live data directory has been renamed to it,
// including on a failed restore, so the caller can tell whether the rollback
// put the original back.
func runStagedRestore(ctx context.Context, dataDir string, replaceIdentity bool) (string, error) {
	if err := refuseServerLock(dataDir); err != nil {
		return "", err
	}
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
	snapshot, err := snapshotRestoreDataDir(dataDir)
	if err != nil {
		return "", err
	}
	if err := restoreBackupFn(ctx, manifest, dataDir); err != nil {
		return snapshot, rollbackFailedRestoreDataDir(dataDir, snapshot, err)
	}
	return snapshot, nil
}

package app

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/store"
)

var (
	bypassLocksMu sync.Mutex
	bypassLocks   = map[string]*sync.Mutex{}
)

func lockUpgradeBypass(dataDir string) func() {
	key := filepath.Clean(dataDir)
	if resolved, err := filepath.EvalSymlinks(dataDir); err == nil {
		key = resolved
	}
	bypassLocksMu.Lock()
	mu := bypassLocks[key]
	if mu == nil {
		mu = &sync.Mutex{}
		bypassLocks[key] = mu
	}
	bypassLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

func checkBootUpgradeGate(dataDir string, allowUnverified bool, now time.Time) (backupcore.BootUpgradeResult, error) {
	return backupcore.EvaluateBootUpgrade(dataDir, capabilities.ReleaseVersion, allowUnverified, now)
}

func recordBootUpgradeOutcome(ctx context.Context, st *store.Store, dataDir string, boot backupcore.BootUpgradeResult, logger *slog.Logger, now time.Time) error {
	now = now.UTC()
	if !boot.Changed {
		return backupcore.SeedBootBinaryVersion(dataDir, capabilities.ReleaseVersion)
	}
	if !boot.Verified && st != nil {
		kind := store.BackupEventUpgradeWithoutBackup
		if boot.Bypassed {
			kind = store.BackupEventUpgradeBypassed
		}
		if err := st.AppendBackupEvent(ctx, kind, store.BackupEventPayload{
			Set:         boot.FailedSet,
			FromVersion: boot.FromVersion,
			ToVersion:   boot.ToVersion,
			Policy:      boot.Policy,
		}, now); err != nil {
			return err
		}
	}
	if err := backupcore.CommitBootUpgrade(dataDir, boot, now); err != nil {
		return err
	}
	if boot.Verified {
		if logger != nil {
			logger.Info("boot binary upgrade verified by backup gate",
				"fromVersion", boot.FromVersion,
				"toVersion", boot.ToVersion,
				"policy", boot.Policy,
			)
		}
		return nil
	}
	if logger != nil {
		logger.Warn("boot binary upgrade proceeded without verified backup",
			"fromVersion", boot.FromVersion,
			"toVersion", boot.ToVersion,
			"policy", boot.Policy,
			"bypassed", boot.Bypassed,
			"override", boot.Override,
			"failedSet", boot.FailedSet,
			"reason", boot.Reason,
		)
	}
	return nil
}

func summarizeStagedUpdate(m StagedUpdateMarker, exists bool) graph.StagedUpdateInfo {
	if !exists {
		return graph.StagedUpdateInfo{}
	}
	out := graph.StagedUpdateInfo{
		Staged:           m.Status == "staged",
		Status:           m.Status,
		TargetVersion:    m.TargetVersion,
		Channel:          m.Channel,
		Commit:           m.Commit,
		ArtifactName:     m.ArtifactName,
		ArtifactSHA256:   m.ArtifactSHA256,
		SourceDistDir:    m.SourceDistDir,
		BypassAuthorized: m.BypassAuthorized,
		RollbackBinary:   m.RollbackBinary,
	}
	if !m.StagedAt.IsZero() {
		out.StagedAt = m.StagedAt.UTC().Format(time.RFC3339)
	}
	if m.AppliedAt != nil && !m.AppliedAt.IsZero() {
		out.AppliedAt = m.AppliedAt.UTC().Format(time.RFC3339)
	}
	return out
}

type updateGateOps struct {
	dataDir   string
	store     *store.Store
	logger    *slog.Logger
	verifyKey ed25519.PublicKey
	now       func() time.Time
}

func (o *updateGateOps) clock() time.Time {
	if o.now != nil {
		return o.now().UTC()
	}
	return time.Now().UTC()
}

func (o *updateGateOps) AuthorizeUpgradeBypass(ctx context.Context, actorProfileID, targetVersion, confirmation, reason string) (backupcore.Status, error) {
	if err := ctx.Err(); err != nil {
		return backupcore.Status{}, err
	}
	unlock := lockUpgradeBypass(o.dataDir)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return backupcore.Status{}, err
	}
	now := o.clock()
	prevStatus, err := backupcore.LoadStatus(o.dataDir)
	if err != nil {
		return backupcore.Status{}, err
	}
	st, err := backupcore.RecordUpgradeBypass(o.dataDir, targetVersion, confirmation, reason, actorProfileID, now)
	if err != nil {
		return backupcore.Status{}, err
	}
	if o.store != nil {
		if err := o.store.AppendBackupEvent(ctx, store.BackupEventUpgradeBypassed, store.BackupEventPayload{
			Set:       st.LastUpgradeBypass.FailedSet,
			ToVersion: targetVersion,
			Policy:    backupcore.UpgradeGateSoft,
		}, now); err != nil {
			_ = backupcore.RestoreUpgradeBypass(o.dataDir, targetVersion, now, prevStatus.LastUpgradeBypass)
			return backupcore.Status{}, err
		}
	}
	if o.logger != nil {
		o.logger.Warn("backup update gate elevated bypass authorized",
			"targetVersion", targetVersion,
			"actorProfileID", actorProfileID,
			"policy", backupcore.UpgradeGateSoft,
			"failedSet", st.LastUpgradeBypass.FailedSet,
			"reason", reason,
		)
	}
	return st, nil
}

func (o *updateGateOps) StageUpdate(ctx context.Context, distDir string) (graph.StagedUpdateInfo, error) {
	now := o.clock()
	marker, err := StageUpdate(o.dataDir, distDir, o.verifyKey, now)
	if err != nil {
		return graph.StagedUpdateInfo{}, err
	}
	backupStatus, _ := backupcore.LoadStatus(o.dataDir)
	if o.logger != nil {
		o.logger.Info("core update staged",
			"targetVersion", marker.TargetVersion,
			"artifactName", marker.ArtifactName,
			"bypassAuthorized", marker.BypassAuthorized,
		)
	}
	if o.store != nil {
		if err := o.store.AppendBackupEvent(ctx, store.BackupEventUpdateStaged, store.BackupEventPayload{
			ToVersion: marker.TargetVersion,
			Policy:    backupStatus.EffectiveUpgradeGate(),
		}, now); err != nil {
			return graph.StagedUpdateInfo{}, err
		}
	}
	return summarizeStagedUpdate(marker, true), nil
}

func (o *updateGateOps) CancelStagedUpdate(_ context.Context) (bool, error) {
	return CancelStagedUpdate(o.dataDir)
}

func (o *updateGateOps) StagedUpdate(_ context.Context) (graph.StagedUpdateInfo, error) {
	marker, ok, err := LoadStagedUpdate(o.dataDir)
	if err != nil {
		return graph.StagedUpdateInfo{}, err
	}
	return summarizeStagedUpdate(marker, ok), nil
}

func (o *updateGateOps) AcknowledgeUnverifiedUpgradeBanner(_ context.Context) (backupcore.Status, error) {
	return backupcore.AcknowledgeUpgradeBanner(o.dataDir, o.clock())
}

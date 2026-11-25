package app

import (
	"context"
	"errors"
	"os"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/restorestage"
)

// backupOps is the console's scheduled-backup state and restore drill
// (CORE-0778, ADR-0192 §5 L3 and §6), backed by the data directory's status
// file and restore stager.
type backupOps struct {
	dataDir string
	stager  *restorestage.Stager
	// idle reports whether the server is idle enough for a drill; nil skips
	// the check.
	idle func() bool
}

func (o backupOps) Status() (backupcore.Status, error) {
	return backupcore.LoadStatus(o.dataDir)
}

func (o backupOps) AcknowledgeBanner(banner backupcore.Banner) (backupcore.Status, error) {
	return backupcore.AcknowledgeBanner(o.dataDir, banner, time.Now())
}

func (o backupOps) SetUpgradeGate(gate string) (backupcore.Status, error) {
	return backupcore.SetUpgradeGate(o.dataDir, gate)
}

// Drill runs "Verify my backup now" under the ADR-0192 §5 L3 guardrails: the
// data directory's free space, the idle check, one drill at a time and the
// default hard timeout.
func (o backupOps) Drill(ctx context.Context, envelope, backupKey string) (restorestage.DrillReport, error) {
	return o.stager.Drill(ctx, envelope, backupKey, restorestage.DrillGuards{
		FreeBytes: backupFreeBytes,
		Idle:      o.idle,
	})
}

// CurrentKeyFingerprint is the fingerprint of the public wrap key new sets
// are sealed to; empty before one is provisioned.
func (o backupOps) CurrentKeyFingerprint() (string, error) {
	key, err := backupcore.LoadPublicWrapKey(o.dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return key.Fingerprint(), nil
}

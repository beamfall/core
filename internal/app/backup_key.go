package app

import (
	"errors"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/graph"
)

// backupKeyEscrow adapts the data directory's bootstrap escrow to the console
// backup key fields (CORE-2032).
type backupKeyEscrow struct {
	dataDir string
	now     func() time.Time
}

func (e backupKeyEscrow) Pending() (bool, time.Time, error) {
	escrow, err := backupcore.ReadBootstrapEscrow(e.dataDir, e.now())
	if err != nil {
		if backupKeyNotPending(err) {
			return false, time.Time{}, nil
		}
		return false, time.Time{}, err
	}
	return true, escrow.ExpiresAt, nil
}

func (e backupKeyEscrow) Reveal() (string, string, time.Time, error) {
	pending, err := backupcore.RevealBootstrapEscrow(e.dataDir, e.now())
	if err != nil {
		if backupKeyNotPending(err) {
			return "", "", time.Time{}, graph.ErrBackupKeyNotPending
		}
		return "", "", time.Time{}, err
	}
	return pending.EscrowID, pending.Key.Display(), pending.ExpiresAt, nil
}

// Acknowledge confirms the key revealed for escrowID. The console already
// showed it, so the presenter only lets the existing acknowledge path install
// it and remove the escrow. An expired escrow is refused, not confirmed.
func (e backupKeyEscrow) Acknowledge(escrowID string) error {
	escrow, err := backupcore.ReadBootstrapEscrow(e.dataDir, e.now())
	switch {
	case backupKeyNotPending(err):
		return graph.ErrBackupKeyNotPending
	case err != nil:
		return err
	case escrow.ID != escrowID:
		return graph.ErrBackupKeyChanged
	}
	err = backupcore.AcknowledgeBootstrapEscrow(e.dataDir, escrowID, func(backupcore.BackupKey) error { return nil })
	switch {
	case errors.Is(err, backupcore.ErrBootstrapEscrowNotFound):
		return graph.ErrBackupKeyNotPending
	case errors.Is(err, backupcore.ErrBootstrapEscrowChanged):
		return graph.ErrBackupKeyChanged
	}
	return err
}

func backupKeyNotPending(err error) bool {
	return errors.Is(err, backupcore.ErrBootstrapEscrowNotFound) || errors.Is(err, backupcore.ErrBootstrapEscrowExpired)
}

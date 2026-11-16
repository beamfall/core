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
}

func (e backupKeyEscrow) Pending() (bool, time.Time, error) {
	escrow, err := backupcore.ReadBootstrapEscrow(e.dataDir)
	if err != nil {
		if errors.Is(err, backupcore.ErrBootstrapEscrowNotFound) {
			return false, time.Time{}, nil
		}
		return false, time.Time{}, err
	}
	return true, escrow.ExpiresAt, nil
}

func (e backupKeyEscrow) Reveal() (string, string, time.Time, error) {
	pending, err := backupcore.RevealBootstrapEscrow(e.dataDir)
	if err != nil {
		if errors.Is(err, backupcore.ErrBootstrapEscrowNotFound) {
			return "", "", time.Time{}, graph.ErrBackupKeyNotPending
		}
		return "", "", time.Time{}, err
	}
	return pending.ConfirmationID, pending.Key.Display(), pending.ExpiresAt, nil
}

// Acknowledge confirms the key revealed with confirmationID. The console
// already showed it, so the presenter only lets the acknowledge path confirm
// it and remove the escrow. Past the save-by date the key is still confirmed
// (CORE-2175).
func (e backupKeyEscrow) Acknowledge(confirmationID string) error {
	err := backupcore.AcknowledgeBootstrapEscrow(e.dataDir, confirmationID, func(backupcore.BackupKey) error { return nil })
	switch {
	case errors.Is(err, backupcore.ErrBootstrapEscrowNotFound):
		return graph.ErrBackupKeyNotPending
	case errors.Is(err, backupcore.ErrBootstrapEscrowChanged):
		return graph.ErrBackupKeyChanged
	}
	return err
}

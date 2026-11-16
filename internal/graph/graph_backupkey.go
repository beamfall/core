package graph

import (
	"context"
	"errors"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Console backup key (CORE-2032). The server mints a backup key on first start
// and keeps it in a bootstrap escrow until the owner confirms it, past the
// 30-day save-by date if need be (CORE-2175). The owner reveals the key, saves
// it, and confirms; confirming keeps that key, so every backup set opens with
// it, and removes the escrow.
// Every field is Admin-Session gated in execute(), and no API token tier may
// reveal or confirm the key (agentTokenDenied). The key is returned only by
// revealBackupKey and is never logged.

// BackupKeyEscrow is the data directory's backup key escrow behind the console
// fields.
type BackupKeyEscrow interface {
	// Pending reports whether a key is waiting to be confirmed and its
	// save-by date.
	Pending() (bool, time.Time, error)
	// Reveal returns the ID that confirms the pending key, the key, and its
	// save-by date. It returns ErrBackupKeyNotPending when nothing waits.
	Reveal() (escrowID, backupKey string, expiresAt time.Time, err error)
	// Acknowledge confirms the key escrowID names: ErrBackupKeyNotPending
	// when nothing waits, ErrBackupKeyChanged when escrowID does not name the
	// pending key.
	Acknowledge(escrowID string) error
}

var (
	// ErrBackupKeyNotPending: no backup key is waiting to be confirmed.
	ErrBackupKeyNotPending = errors.New("no backup key is waiting to be saved")
	// ErrBackupKeyChanged: the key being confirmed is not the pending one.
	ErrBackupKeyChanged = errors.New("the backup key changed; reveal it again")
)

// SetBackupKeyEscrow wires the data directory's backup key escrow.
func (h *Handler) SetBackupKeyEscrow(escrow BackupKeyEscrow) {
	h.backupKeyEscrow = escrow
}

// BackupKeyStatusInfo says whether the owner still has to save the backup
// key. It never carries the key.
type BackupKeyStatusInfo struct {
	Pending   bool   `json:"pending"`
	ExpiresAt string `json:"expiresAt"`
	// DaysLeft is the whole days, rounded up, until the save-by date. The key
	// stays pending after it until the owner confirms it.
	DaysLeft int `json:"daysLeft"`
}

// RevealedBackupKeyInfo is the backup key to save and the escrow ID that
// confirms it.
type RevealedBackupKeyInfo struct {
	EscrowID  string `json:"escrowID"`
	BackupKey string `json:"backupKey"`
	ExpiresAt string `json:"expiresAt"`
	DaysLeft  int    `json:"daysLeft"`
}

func (h *Handler) backupKeyStatus() (BackupKeyStatusInfo, error) {
	if h.backupKeyEscrow == nil {
		return BackupKeyStatusInfo{}, nil
	}
	pending, expiresAt, err := h.backupKeyEscrow.Pending()
	if err != nil || !pending {
		return BackupKeyStatusInfo{}, err
	}
	return BackupKeyStatusInfo{
		Pending:   true,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		DaysLeft:  backupKeyDaysLeft(expiresAt, time.Now()),
	}, nil
}

func (h *Handler) revealBackupKey(ctx context.Context, scope auth.Scope) (RevealedBackupKeyInfo, error) {
	if h.backupKeyEscrow == nil {
		return RevealedBackupKeyInfo{}, clientError(ErrBackupKeyNotPending.Error())
	}
	escrowID, key, expiresAt, err := h.backupKeyEscrow.Reveal()
	if err != nil {
		return RevealedBackupKeyInfo{}, backupKeyError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backupKey", "revealed", ""); err != nil {
		return RevealedBackupKeyInfo{}, err
	}
	return RevealedBackupKeyInfo{
		EscrowID:  escrowID,
		BackupKey: key,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		DaysLeft:  backupKeyDaysLeft(expiresAt, time.Now()),
	}, nil
}

func (h *Handler) acknowledgeBackupKey(ctx context.Context, scope auth.Scope, req request) (BackupKeyStatusInfo, error) {
	if h.backupKeyEscrow == nil {
		return BackupKeyStatusInfo{}, clientError(ErrBackupKeyNotPending.Error())
	}
	escrowID := trimmedVariable(req, "escrowID")
	if escrowID == "" {
		return BackupKeyStatusInfo{}, clientError("escrowID required")
	}
	if err := h.backupKeyEscrow.Acknowledge(escrowID); err != nil {
		return BackupKeyStatusInfo{}, backupKeyError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backupKey", "acknowledged", ""); err != nil {
		return BackupKeyStatusInfo{}, err
	}
	return h.backupKeyStatus()
}

// backupKeyError shows the two refusals as-is and hides anything else.
func backupKeyError(err error) error {
	switch {
	case errors.Is(err, ErrBackupKeyNotPending):
		return clientError(ErrBackupKeyNotPending.Error())
	case errors.Is(err, ErrBackupKeyChanged):
		return clientError(ErrBackupKeyChanged.Error())
	default:
		return err
	}
}

// backupKeyDaysLeft rounds up, so the last partial day reads as one.
func backupKeyDaysLeft(expiresAt, now time.Time) int {
	left := expiresAt.Sub(now)
	if left <= 0 {
		return 0
	}
	return int((left + 24*time.Hour - 1) / (24 * time.Hour))
}

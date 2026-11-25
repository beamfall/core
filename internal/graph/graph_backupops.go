package graph

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/restorestage"
	"github.com/beamfall/core/internal/store"
)

// Console backup operations (ADR-0192 §5 L3 and §6, CORE-0778 BKUP-14/15):
// the two distinct failure banners and their acknowledgement, the update-gate
// policy (soft by default, hard block as an opt-in), and "Verify my backup
// now", the restore drill. Every field is Admin-Session gated in execute().
// The drill takes the backup key as an argument only: it is never stored,
// logged or returned, and no API token tier may run it (agentTokenDenied).

// BackupOps is the data directory's scheduled-backup state and drill runner.
type BackupOps interface {
	Status() (backupcore.Status, error)
	AcknowledgeBanner(banner backupcore.Banner) (backupcore.Status, error)
	SetUpgradeGate(gate string) (backupcore.Status, error)
	Drill(ctx context.Context, envelope, backupKey string) (restorestage.DrillReport, error)
}

// SetBackupOps wires the scheduled-backup state and drill runner.
func (h *Handler) SetBackupOps(ops BackupOps) {
	h.backupOps = ops
}

// BackupStatusInfo is the scheduled-backup state behind the console's backup
// banners, recovery evidence and update gate. It carries no key material and
// no paths beyond Backup Set names.
type BackupStatusInfo struct {
	// BackupFailedBanner and IntegrityFailedBanner say whether each distinct
	// failure banner is displayed: its failure is neither superseded by a later
	// verified backup nor acknowledged.
	BackupFailedBanner    bool             `json:"backupFailedBanner"`
	IntegrityFailedBanner bool             `json:"integrityFailedBanner"`
	LastSuccess           *BackupRunInfo   `json:"lastSuccess"`
	LastFailure           *BackupRunInfo   `json:"lastFailure"`
	LastIntegrityFailure  *BackupRunInfo   `json:"lastIntegrityFailure"`
	UpgradeGate           string           `json:"upgradeGate"`
	LastDrill             *BackupDrillInfo `json:"lastDrill"`
}

// BackupRunInfo is one scheduled-backup outcome.
type BackupRunInfo struct {
	At           string `json:"at"`
	FailureClass string `json:"failureClass"`
	Set          string `json:"set"`
}

// BackupDrillInfo is one restore-drill outcome.
type BackupDrillInfo struct {
	At       string `json:"at"`
	Verified bool   `json:"verified"`
	Set      string `json:"set"`
	Reason   string `json:"reason"`
	// The fields below are set only on the result of verifyBackupNow.
	BackupCreatedAt string `json:"backupCreatedAt"`
	Databases       int    `json:"databases"`
	Libraries       int    `json:"libraries"`
	DurationMs      int64  `json:"durationMs"`
}

func (h *Handler) backupOpsOrErr() (BackupOps, error) {
	if h.backupOps == nil {
		return nil, clientError("backup operations are unavailable")
	}
	return h.backupOps, nil
}

func (h *Handler) backupStatus() (BackupStatusInfo, error) {
	if h.backupOps == nil {
		return BackupStatusInfo{UpgradeGate: backupcore.UpgradeGateSoft}, nil
	}
	status, err := h.backupOps.Status()
	if err != nil {
		return BackupStatusInfo{}, err
	}
	return backupStatusInfo(status), nil
}

func (h *Handler) acknowledgeBackupBanner(ctx context.Context, scope auth.Scope, req request) (BackupStatusInfo, error) {
	ops, err := h.backupOpsOrErr()
	if err != nil {
		return BackupStatusInfo{}, err
	}
	banner := backupcore.Banner(trimmedVariable(req, "banner"))
	status, err := ops.AcknowledgeBanner(banner)
	if errors.Is(err, backupcore.ErrUnknownBanner) {
		return BackupStatusInfo{}, clientError("banner must be backup-failed or integrity-failed")
	}
	if err != nil {
		return BackupStatusInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backup", "banner-acknowledged:"+string(banner), ""); err != nil {
		return BackupStatusInfo{}, err
	}
	return backupStatusInfo(status), nil
}

func (h *Handler) setBackupUpgradeGate(ctx context.Context, scope auth.Scope, req request) (BackupStatusInfo, error) {
	ops, err := h.backupOpsOrErr()
	if err != nil {
		return BackupStatusInfo{}, err
	}
	gate := strings.ToLower(trimmedVariable(req, "gate"))
	status, err := ops.SetUpgradeGate(gate)
	if errors.Is(err, backupcore.ErrUnknownUpgradeGate) {
		return BackupStatusInfo{}, clientError("gate must be soft or hard")
	}
	if err != nil {
		return BackupStatusInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backup", "upgrade-gate:"+gate, ""); err != nil {
		return BackupStatusInfo{}, err
	}
	return backupStatusInfo(status), nil
}

func (h *Handler) verifyBackupNow(ctx context.Context, scope auth.Scope, req request) (BackupDrillInfo, error) {
	ops, err := h.backupOpsOrErr()
	if err != nil {
		return BackupDrillInfo{}, err
	}
	report, err := ops.Drill(ctx, trimmedVariable(req, "envelope"), stringVar(req, "backupKey"))
	if err != nil {
		return BackupDrillInfo{}, stagedRestoreError(err)
	}
	outcome := "restore-verified"
	if !report.Verified {
		outcome = "restore-failed"
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backup", "drill:"+outcome, ""); err != nil {
		return BackupDrillInfo{}, err
	}
	return BackupDrillInfo{
		At:              time.Now().UTC().Format(time.RFC3339),
		Verified:        report.Verified,
		Set:             report.Set,
		Reason:          report.Reason,
		BackupCreatedAt: report.BackupCreatedAt,
		Databases:       report.Databases,
		Libraries:       report.Libraries,
		DurationMs:      report.Duration.Milliseconds(),
	}, nil
}

func backupStatusInfo(s backupcore.Status) BackupStatusInfo {
	info := BackupStatusInfo{
		BackupFailedBanner:    s.BannerActive(backupcore.BannerBackupFailed),
		IntegrityFailedBanner: s.BannerActive(backupcore.BannerIntegrityFailed),
		LastSuccess:           backupRunInfo(s.LastSuccess),
		LastFailure:           backupRunInfo(s.LastFailure),
		LastIntegrityFailure:  backupRunInfo(s.LastIntegrityFailure),
		UpgradeGate:           s.EffectiveUpgradeGate(),
	}
	if d := s.LastDrill; d != nil {
		info.LastDrill = &BackupDrillInfo{At: d.At.UTC().Format(time.RFC3339), Verified: d.Verified, Set: d.Set, Reason: d.Reason}
	}
	return info
}

func backupRunInfo(r *backupcore.RunRecord) *BackupRunInfo {
	if r == nil {
		return nil
	}
	return &BackupRunInfo{At: r.At.UTC().Format(time.RFC3339), FailureClass: r.FailureClass, Set: r.Set}
}

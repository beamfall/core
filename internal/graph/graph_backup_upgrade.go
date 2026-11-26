package graph

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/store"
)

// UpdateGateOps connects Console GraphQL resolvers to server-side elevated
// update-gate bypass authorization, signed dist staging, and banner dismissal
// (ADR-0243). Interface lives at the consumer (ADR-0016).
type UpdateGateOps interface {
	AuthorizeUpgradeBypass(ctx context.Context, actorProfileID, targetVersion, confirmation, reason string) (backupcore.Status, error)
	StageUpdate(ctx context.Context, distDir string) (StagedUpdateInfo, error)
	CancelStagedUpdate(ctx context.Context) (bool, error)
	StagedUpdate(ctx context.Context) (StagedUpdateInfo, error)
	AcknowledgeUnverifiedUpgradeBanner(ctx context.Context) (backupcore.Status, error)
}

// SetUpdateGateOps wires the server update-gate bypass and dist staging handler.
func (h *Handler) SetUpdateGateOps(ops UpdateGateOps) {
	h.updateGateOps = ops
}

// UpgradeBypassInfo summarizes the recorded step-up Admin bypass under the
// soft backup update gate (ADR-0243 D2).
type UpgradeBypassInfo struct {
	At             string `json:"at"`
	TargetVersion  string `json:"targetVersion"`
	ActorProfileID string `json:"actorProfileID"`
	Policy         string `json:"policy"`
	FailedSet      string `json:"failedSet"`
	Reason         string `json:"reason"`
	AppliedAt      string `json:"appliedAt"`
	Valid          bool   `json:"valid"`
}

// UpgradeEventInfo summarizes the newest unverified or bypassed release
// upgrade detected before SQLite migrations ran (ADR-0243 D3).
type UpgradeEventInfo struct {
	At          string `json:"at"`
	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
	Bypassed    bool   `json:"bypassed"`
	Reason      string `json:"reason"`
}

// StagedUpdateInfo describes any signed server release distribution staged
// under the data directory pending `beamfall upgrade apply` (ADR-0243 D1).
type StagedUpdateInfo struct {
	Staged           bool   `json:"staged"`
	Status           string `json:"status"`
	StagedAt         string `json:"stagedAt"`
	TargetVersion    string `json:"targetVersion"`
	Channel          string `json:"channel"`
	Commit           string `json:"commit"`
	ArtifactName     string `json:"artifactName"`
	ArtifactSHA256   string `json:"artifactSHA256"`
	SourceDistDir    string `json:"sourceDistDir"`
	BypassAuthorized bool   `json:"bypassAuthorized"`
	AppliedAt        string `json:"appliedAt"`
	RollbackBinary   string `json:"rollbackBinary"`
}

func (h *Handler) updateGateOpsOrErr() (UpdateGateOps, error) {
	if h.updateGateOps == nil {
		return nil, clientError("update gate operations are unavailable")
	}
	return h.updateGateOps, nil
}

func enrichBackupStatusInfo(out BackupStatusInfo, status backupcore.Status) BackupStatusInfo {
	out.UnverifiedUpgradeBanner = status.UnverifiedUpgradeBannerActive()
	out.FailedSet = status.FailedSetForUpgradeGate()
	out.LastBinaryVersion = status.LastBinaryVersion
	if status.LastUpgradeBypass != nil {
		appliedAt := ""
		if status.LastUpgradeBypass.AppliedAt != nil && !status.LastUpgradeBypass.AppliedAt.IsZero() {
			appliedAt = status.LastUpgradeBypass.AppliedAt.UTC().Format(time.RFC3339)
		}
		out.LastUpgradeBypass = &UpgradeBypassInfo{
			At:             status.LastUpgradeBypass.At.UTC().Format(time.RFC3339),
			TargetVersion:  status.LastUpgradeBypass.TargetVersion,
			ActorProfileID: status.LastUpgradeBypass.ActorProfileID,
			Policy:         status.LastUpgradeBypass.Policy,
			FailedSet:      status.LastUpgradeBypass.FailedSet,
			Reason:         status.LastUpgradeBypass.Reason,
			AppliedAt:      appliedAt,
			Valid:          status.HasMatchingBypass(status.LastUpgradeBypass.TargetVersion),
		}
	}
	if status.LastUnverifiedUpgrade != nil {
		out.LastUnverifiedUpgrade = &UpgradeEventInfo{
			At:          status.LastUnverifiedUpgrade.At.UTC().Format(time.RFC3339),
			FromVersion: status.LastUnverifiedUpgrade.FromVersion,
			ToVersion:   status.LastUnverifiedUpgrade.ToVersion,
			Bypassed:    status.LastUnverifiedUpgrade.Bypassed,
			Reason:      status.LastUnverifiedUpgrade.Reason,
		}
	}
	return out
}

func (h *Handler) acknowledgeUpgradeBanner(ctx context.Context, scope auth.Scope) (BackupStatusInfo, error) {
	ops, err := h.updateGateOpsOrErr()
	if err != nil {
		return BackupStatusInfo{}, err
	}
	status, err := ops.AcknowledgeUnverifiedUpgradeBanner(ctx)
	if err != nil {
		return BackupStatusInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backup", "banner-acknowledged:"+string(backupcore.BannerUnverifiedUpgrade), ""); err != nil {
		return BackupStatusInfo{}, err
	}
	return enrichBackupStatusInfo(backupStatusInfo(status), status), nil
}

func (h *Handler) authorizeBackupUpgradeBypass(ctx context.Context, scope auth.Scope, req request) (BackupStatusInfo, error) {
	if h.auth == nil {
		return BackupStatusInfo{}, clientError("authentication service is unavailable")
	}
	ops, err := h.updateGateOpsOrErr()
	if err != nil {
		return BackupStatusInfo{}, err
	}
	if scope.SessionID == "" {
		return BackupStatusInfo{}, clientError("admin session required")
	}
	if scope.DeviceRole != store.DeviceRoleManager {
		return BackupStatusInfo{}, clientError("a manager device is required")
	}

	targetVersion := trimmedVariable(req, "targetVersion")
	confirmation := trimmedVariable(req, "confirmation")
	reason := trimmedVariable(req, "reason")
	password := stringVar(req, "password")

	if err := h.auth.VerifyAdminReauth(ctx, scope, password); err != nil {
		switch {
		case errors.Is(err, auth.ErrAdminLocked):
			return BackupStatusInfo{}, clientError("too many admin password attempts; retry later")
		case errors.Is(err, auth.ErrNotManager):
			return BackupStatusInfo{}, clientError("a manager device is required")
		case errors.Is(err, auth.ErrAdminPassword):
			return BackupStatusInfo{}, clientError("admin password rejected")
		default:
			return BackupStatusInfo{}, err
		}
	}
	if targetVersion == "" {
		return BackupStatusInfo{}, clientError("targetVersion is required")
	}
	if staged, err := ops.StagedUpdate(ctx); err == nil && staged.Staged && strings.TrimSpace(staged.TargetVersion) != targetVersion {
		return BackupStatusInfo{}, clientError("targetVersion does not match the staged update version " + staged.TargetVersion)
	}

	status, err := ops.AuthorizeUpgradeBypass(ctx, string(scope.ProfileID), targetVersion, confirmation, reason)
	if err != nil {
		if errors.Is(err, backupcore.ErrUpgradeGateHardBypassDenied) ||
			errors.Is(err, backupcore.ErrUpgradeConfirmationInvalid) ||
			errors.Is(err, backupcore.ErrUpgradeBypassReasonRequired) {
			return BackupStatusInfo{}, clientError(err.Error())
		}
		return BackupStatusInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backup", "upgrade-bypass:"+targetVersion, ""); err != nil {
		return BackupStatusInfo{}, err
	}
	return enrichBackupStatusInfo(backupStatusInfo(status), status), nil
}

func (h *Handler) stageCoreUpdate(ctx context.Context, scope auth.Scope, req request) (StagedUpdateInfo, error) {
	ops, err := h.updateGateOpsOrErr()
	if err != nil {
		return StagedUpdateInfo{}, err
	}
	distDir := trimmedVariable(req, "distDir")
	if distDir == "" {
		return StagedUpdateInfo{}, clientError("distDir is required")
	}
	info, err := ops.StageUpdate(ctx, distDir)
	if err != nil {
		return StagedUpdateInfo{}, clientError(err.Error())
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backup", "update-staged:"+info.TargetVersion, ""); err != nil {
		return StagedUpdateInfo{}, err
	}
	return info, nil
}

func (h *Handler) cancelStagedCoreUpdate(ctx context.Context, scope auth.Scope) (bool, error) {
	ops, err := h.updateGateOpsOrErr()
	if err != nil {
		return false, err
	}
	removed, err := ops.CancelStagedUpdate(ctx)
	if err != nil || !removed {
		return removed, err
	}
	return true, h.logActivity(ctx, scope, store.ActivityConfigUpdate, "backup", "update-stage-canceled", "")
}

func (h *Handler) stagedCoreUpdate(ctx context.Context) (StagedUpdateInfo, error) {
	if h.updateGateOps == nil {
		return StagedUpdateInfo{}, nil
	}
	return h.updateGateOps.StagedUpdate(ctx)
}

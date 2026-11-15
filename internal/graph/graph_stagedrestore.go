package graph

import (
	"context"
	"errors"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/restorestage"
	"github.com/beamfall/core/internal/store"
)

// Console staged restore (CORE-1720). An admin picks a published Backup Set,
// sees what it holds and its versions, and stages a restore that the next
// server start applies before the store opens, keeping the previous data
// directory as a rollback copy. The backup key is an argument only: it is
// never stored, logged or returned. Every field is Admin-Session gated in
// execute().

// RestoreStager is the staging seam behind the console fields.
type RestoreStager interface {
	Inspect(ctx context.Context, envelope, backupKey string) (store.BackupManifestSummary, error)
	Stage(ctx context.Context, envelope, backupKey string, replaceIdentity bool) (restorestage.Marker, error)
	Cancel() (bool, error)
	Status() (restorestage.Status, error)
}

// SetRestoreStager wires the data directory's restore stager.
func (h *Handler) SetRestoreStager(stager RestoreStager) {
	h.restoreStager = stager
}

// BackupContentsInfo is what a Backup Set holds and whether this server can
// restore it.
type BackupContentsInfo struct {
	CreatedAt                           string                       `json:"createdAt"`
	CreatedBy                           string                       `json:"createdBy"`
	Reason                              string                       `json:"reason"`
	RestoreInstructionsVersion          int                          `json:"restoreInstructionsVersion"`
	SupportedRestoreInstructionsVersion int                          `json:"supportedRestoreInstructionsVersion"`
	Databases                           []BackupContentsDatabaseInfo `json:"databases"`
	OperationalFiles                    []string                     `json:"operationalFiles"`
	LibraryRootCount                    int                          `json:"libraryRootCount"`
	Compatible                          bool                         `json:"compatible"`
	// Refusal says why the set cannot be restored when Compatible is false.
	Refusal string `json:"refusal"`
}

// BackupContentsDatabaseInfo is one database snapshot in a Backup Set.
type BackupContentsDatabaseInfo struct {
	Name                   string `json:"name"`
	Database               string `json:"database"`
	SchemaVersion          int    `json:"schemaVersion"`
	SupportedSchemaVersion int    `json:"supportedSchemaVersion"`
	SizeBytes              int64  `json:"sizeBytes"`
}

// StagedRestoreInfo is the staged restore, if any, and the last applied one.
type StagedRestoreInfo struct {
	Staged   bool   `json:"staged"`
	StagedAt string `json:"stagedAt"`
	Envelope string `json:"envelope"`
	// ReplaceIdentity is whether the staged restore replaces this server's
	// secrets and certificates with the backup's.
	ReplaceIdentity bool                `json:"replaceIdentity"`
	Contents        *BackupContentsInfo `json:"contents"`
	LastResult      *RestoreResultInfo  `json:"lastResult"`
}

// RestoreResultInfo is the outcome of the last staged restore a start ran.
type RestoreResultInfo struct {
	AppliedAt       string `json:"appliedAt"`
	OK              bool   `json:"ok"`
	Error           string `json:"error"`
	RollbackCopy    string `json:"rollbackCopy"`
	BackupCreatedAt string `json:"backupCreatedAt"`
}

func (h *Handler) restoreStagerOrErr() (RestoreStager, error) {
	if h.restoreStager == nil {
		return nil, clientError("staged restore is unavailable")
	}
	return h.restoreStager, nil
}

// stagedRestoreError shows a refusal as-is and hides anything else.
func stagedRestoreError(err error) error {
	var refusal restorestage.RefusalError
	if errors.As(err, &refusal) {
		return clientError(refusal.Error())
	}
	return err
}

func (h *Handler) inspectBackup(ctx context.Context, req request) (BackupContentsInfo, error) {
	stager, err := h.restoreStagerOrErr()
	if err != nil {
		return BackupContentsInfo{}, err
	}
	summary, err := stager.Inspect(ctx, trimmedVariable(req, "envelope"), stringVar(req, "backupKey"))
	if err != nil {
		return BackupContentsInfo{}, stagedRestoreError(err)
	}
	return backupContentsInfo(summary), nil
}

func (h *Handler) stageRestore(ctx context.Context, scope auth.Scope, req request) (StagedRestoreInfo, error) {
	stager, err := h.restoreStagerOrErr()
	if err != nil {
		return StagedRestoreInfo{}, err
	}
	marker, err := stager.Stage(ctx, trimmedVariable(req, "envelope"), stringVar(req, "backupKey"), boolVar(req, "replaceIdentity"))
	if err != nil {
		return StagedRestoreInfo{}, stagedRestoreError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "restore", "staged", ""); err != nil {
		return StagedRestoreInfo{}, err
	}
	return h.stagedRestoreStatus(&marker)
}

func (h *Handler) cancelStagedRestore(ctx context.Context, scope auth.Scope) (bool, error) {
	stager, err := h.restoreStagerOrErr()
	if err != nil {
		return false, err
	}
	removed, err := stager.Cancel()
	if err != nil || !removed {
		return removed, err
	}
	return true, h.logActivity(ctx, scope, store.ActivityConfigUpdate, "restore", "canceled", "")
}

func (h *Handler) stagedRestore() (StagedRestoreInfo, error) {
	if h.restoreStager == nil {
		return StagedRestoreInfo{}, nil
	}
	return h.stagedRestoreStatus(nil)
}

// stagedRestoreStatus reads the current status; staged, when set, is the
// marker just written so the reply does not depend on a re-read.
func (h *Handler) stagedRestoreStatus(staged *restorestage.Marker) (StagedRestoreInfo, error) {
	status, err := h.restoreStager.Status()
	if err != nil {
		return StagedRestoreInfo{}, err
	}
	if staged != nil {
		status.Staged = staged
	}
	var info StagedRestoreInfo
	if status.Staged != nil {
		contents := backupContentsInfo(status.Staged.Summary)
		info.Staged = true
		info.StagedAt = status.Staged.StagedAt
		info.Envelope = status.Staged.Envelope
		info.ReplaceIdentity = status.Staged.ReplaceIdentity
		info.Contents = &contents
	}
	if result := status.LastResult; result != nil {
		info.LastResult = &RestoreResultInfo{
			AppliedAt:       result.AppliedAt,
			OK:              result.OK,
			Error:           result.Error,
			RollbackCopy:    result.RollbackCopy,
			BackupCreatedAt: result.BackupCreatedAt,
		}
	}
	return info, nil
}

func backupContentsInfo(summary store.BackupManifestSummary) BackupContentsInfo {
	databases := make([]BackupContentsDatabaseInfo, 0, len(summary.Databases))
	for _, db := range summary.Databases {
		databases = append(databases, BackupContentsDatabaseInfo(db))
	}
	files := summary.OperationalFiles
	if files == nil {
		files = []string{}
	}
	return BackupContentsInfo{
		CreatedAt:                           summary.CreatedAt,
		CreatedBy:                           summary.CreatedBy,
		Reason:                              summary.Reason,
		RestoreInstructionsVersion:          summary.RestoreInstructionsVersion,
		SupportedRestoreInstructionsVersion: summary.SupportedRestoreInstructionsVersion,
		Databases:                           databases,
		OperationalFiles:                    files,
		LibraryRootCount:                    summary.LibraryRootCount,
		Compatible:                          summary.Compatible,
		Refusal:                             summary.Refusal,
	}
}

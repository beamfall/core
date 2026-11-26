package store

// Additional ADR-0192 §6 / ADR-0243 backup update-gate webhook event kinds.
// Like scheduled-backup outcomes, they are webhook-only and never reach SSE.
const (
	BackupEventUpgradeBypassed      = "backup.upgrade_bypassed"
	BackupEventUpgradeWithoutBackup = "backup.upgrade_without_backup"
	BackupEventUpdateStaged         = "backup.update_staged"
)

func init() {
	backupEventKinds[BackupEventUpgradeBypassed] = true
	backupEventKinds[BackupEventUpgradeWithoutBackup] = true
	backupEventKinds[BackupEventUpdateStaged] = true
}

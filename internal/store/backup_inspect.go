package store

import (
	"context"
	"errors"
	"sort"
)

// BackupManifestSummary describes what a decrypted Backup Set holds and whether
// this binary can restore it. Refusal carries the reason when Compatible is
// false; it is never a checksum or a stored absolute path.
type BackupManifestSummary struct {
	CreatedAt                           string
	CreatedBy                           string
	Reason                              string
	RestoreInstructionsVersion          int
	SupportedRestoreInstructionsVersion int
	Databases                           []BackupDatabaseSummary
	OperationalFiles                    []string
	LibraryRootCount                    int
	Compatible                          bool
	Refusal                             string
}

// BackupDatabaseSummary is one database snapshot in a Backup Set, with the
// newest schema version this binary can open for that database.
type BackupDatabaseSummary struct {
	Name                   string
	Database               string
	SchemaVersion          int
	SupportedSchemaVersion int
	SizeBytes              int64
}

// InspectBackupManifest reads a decrypted Backup Set manifest and reports its
// contents and versions. It runs the same verification the restore path runs
// (manifest version, schema versions within this binary, snapshot and
// operational-file integrity) and reports a failure as a refusal rather than an
// error, so a caller can report why a set cannot be restored. It returns an error
// only when the manifest cannot be read at all.
func InspectBackupManifest(ctx context.Context, manifestPath string) (BackupManifestSummary, error) {
	manifest, err := readManifestFile(manifestPath)
	if err != nil {
		return BackupManifestSummary{}, errors.New("backup manifest could not be read")
	}
	summary := BackupManifestSummary{
		CreatedAt:                           manifest.CreatedAt,
		CreatedBy:                           manifest.CreatedBy,
		Reason:                              manifest.Reason,
		RestoreInstructionsVersion:          manifest.RestoreInstructionsVersion,
		SupportedRestoreInstructionsVersion: backupRestoreInstructionsVersion,
		Databases:                           make([]BackupDatabaseSummary, 0, len(manifest.Databases)),
		OperationalFiles:                    make([]string, 0, len(manifest.OperationalFiles)),
		LibraryRootCount:                    len(manifest.LibraryRoots),
	}
	for name, snapshot := range manifest.Databases {
		summary.Databases = append(summary.Databases, BackupDatabaseSummary{
			Name:                   name,
			Database:               snapshot.Database,
			SchemaVersion:          snapshot.SchemaVersion,
			SupportedSchemaVersion: supportedBackupSchemaVersion(name, snapshot),
			SizeBytes:              snapshot.SizeBytes,
		})
	}
	sort.Slice(summary.Databases, func(i, j int) bool { return summary.Databases[i].Name < summary.Databases[j].Name })
	for name := range manifest.OperationalFiles {
		summary.OperationalFiles = append(summary.OperationalFiles, name)
	}
	sort.Strings(summary.OperationalFiles)
	if _, err := readVerifiedBackupManifest(ctx, manifestPath); err != nil {
		if ctx.Err() != nil {
			return BackupManifestSummary{}, ctx.Err()
		}
		summary.Refusal = err.Error()
		return summary, nil
	}
	summary.Compatible = true
	return summary, nil
}

func supportedBackupSchemaVersion(name string, snapshot BackupDatabaseSnapshot) int {
	switch {
	case snapshot.Database == encryptedLibraryDBLabel:
		return schemaFreezeEncryptedLibraryMigrations
	case name == "catalog":
		return schemaFreezeCatalogMigrations
	case name == "events":
		return schemaFreezeEventsMigrations
	default:
		return 0
	}
}

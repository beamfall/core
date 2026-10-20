package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// SnapshotSealer encrypts and structurally verifies an opaque Backup Set.
// The app composition root supplies the implementation so store remains a
// storage leaf with no dependency on backup cryptography.
type SnapshotSealer interface {
	SealDirectory(ctx context.Context, sourceDir, envelopePath string) (keyFingerprint string, err error)
	VerifyEnvelopeStructure(src io.Reader) error
}

// SnapshotOpener decrypts an opaque Backup Set into a caller-owned directory.
// Restore callers supply it explicitly because store never owns secret backup
// key material.
type SnapshotOpener interface {
	OpenDirectory(ctx context.Context, envelopePath, destDir string) error
}

type BackupResult struct {
	Directory          string
	Manifest           string
	Envelope           string
	Catalog            BackupDatabaseSnapshot
	Events             BackupDatabaseSnapshot
	EncryptedLibraries []BackupDatabaseSnapshot
	CreatedAt          time.Time
}

type BackupDatabaseSnapshot struct {
	Database      string `json:"database"`
	LibraryID     string `json:"libraryId,omitempty"`
	State         string `json:"state,omitempty"`
	MigrationDir  string `json:"migrationDir,omitempty"`
	CipherFormat  string `json:"cipherFormat,omitempty"`
	SourcePath    string `json:"sourcePath"`
	BackupPath    string `json:"backupPath"`
	SchemaVersion int    `json:"schemaVersion"`
	SizeBytes     int64  `json:"sizeBytes"`
	SHA256        string `json:"sha256"`
}

type BackupRotationArtifact struct {
	Kind     string
	Path     string
	Manifest string
	Action   string
}

type BackupOptions struct {
	EncryptedLibraryDBCipher EncryptedLibraryDBCipher
	LibraryRoots             []BackupLibraryRoot
}

type BackupLibraryRoot struct {
	LibraryID string `json:"libraryId"`
	Path      string `json:"path"`
	VolumeID  string `json:"volumeId"`
}

type BackupOperationalFile struct {
	RelativePath string `json:"relativePath"`
	Kind         string `json:"kind"`
	BackupPath   string `json:"backupPath"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
}

type RestoreOptions struct {
	SnapshotOpener                 SnapshotOpener
	ReplaceIdentity                bool
	CurrentLibraryRoots            []BackupLibraryRoot
	VolumeMismatchWarningThreshold float64
	MediaMissingWarningThreshold   float64
}

type RestoreWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RestoreReport struct {
	Warnings            []RestoreWarning          `json:"warnings"`
	MediaReconciliation MediaReconciliationReport `json:"mediaReconciliation"`
}

type MediaReconciliationReport struct {
	TotalReferences   int     `json:"totalReferences"`
	MissingReferences int     `json:"missingReferences"`
	MissingRatio      float64 `json:"missingRatio"`
	Severity          string  `json:"severity"`
}

type backupSetManifest struct {
	Type                       string                            `json:"type,omitempty"`
	CreatedAt                  string                            `json:"createdAt"`
	CreatedBy                  string                            `json:"createdBy"`
	Reason                     string                            `json:"reason"`
	RestoreInstructionsVersion int                               `json:"restoreInstructionsVersion"`
	ConsistencyProtocol        string                            `json:"consistencyProtocol"`
	SchemaVersions             map[string]int                    `json:"schemaVersions"`
	Databases                  map[string]BackupDatabaseSnapshot `json:"databases"`
	OperationalFiles           map[string]BackupOperationalFile  `json:"operationalFiles,omitempty"`
	LibraryRoots               []BackupLibraryRoot               `json:"libraryRoots,omitempty"`
	Notes                      []string                          `json:"notes"`
}

const backupRestoreInstructionsVersion = 2

const (
	backupEnvelopeBasename = "set.bbk"
	backupReceiptBasename  = "receipt.json"
)

type backupSetReceipt struct {
	Type                    string `json:"type"`
	CreatedAt               string `json:"createdAt"`
	Envelope                string `json:"envelope"`
	EnvelopeVersion         int    `json:"envelopeVersion"`
	KeyFingerprint          string `json:"keyFingerprint"`
	EnvelopeSizeBytes       int64  `json:"envelopeSizeBytes"`
	EnvelopeSHA256          string `json:"envelopeSha256"`
	ManifestFormatEncrypted bool   `json:"manifestFormatEncrypted"`
}

var ErrRestoreIdentityConflict = errors.New("restore identity conflicts with live identity")

func (s *Store) Backup(ctx context.Context, destDir string) (BackupResult, error) {
	return s.BackupWithOptions(ctx, destDir, BackupOptions{})
}

func (s *Store) BackupWithOptions(ctx context.Context, destDir string, opts BackupOptions) (BackupResult, error) {
	canonicalDest, err := s.guardBackupDest(destDir)
	if err != nil {
		return BackupResult{}, err
	}
	if s.snapshotSealer == nil {
		return BackupResult{}, errors.New("backup snapshot sealer is not configured")
	}
	canonicalDest, destRoot, err := s.prepareBackupDestination(canonicalDest)
	if err != nil {
		return BackupResult{}, err
	}
	defer func() { _ = destRoot.Close() }()
	stagingName, err := mkdirTempBackupDir(destRoot, ".beamfall-backup-staging-")
	if err != nil {
		return BackupResult{}, err
	}
	stagingRoot := filepath.Join(canonicalDest, stagingName)
	defer func() { _ = destRoot.RemoveAll(stagingName) }()
	stagingRoot, err = confinedBackupPath(canonicalDest, stagingRoot)
	if err != nil {
		return BackupResult{}, err
	}
	plaintext, err := s.backupPlaintextWithOptions(ctx, stagingRoot, opts)
	if err != nil {
		return BackupResult{}, err
	}
	name := filepath.Base(plaintext.Directory)
	finalDir := filepath.Join(canonicalDest, name)
	partialName := name + ".partial"
	partialDir := filepath.Join(canonicalDest, partialName)
	if err := destRoot.RemoveAll(partialName); err != nil {
		return BackupResult{}, err
	}
	if err := destRoot.Mkdir(partialName, 0o700); err != nil {
		return BackupResult{}, err
	}
	partialDir, err = confinedBackupPath(canonicalDest, partialDir)
	if err != nil {
		return BackupResult{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = destRoot.RemoveAll(partialName)
		}
	}()
	envelopePath := filepath.Join(partialDir, backupEnvelopeBasename)
	envelopePath, err = confinedBackupPath(canonicalDest, envelopePath)
	if err != nil {
		return BackupResult{}, err
	}
	keyFingerprint, err := s.snapshotSealer.SealDirectory(ctx, plaintext.Directory, envelopePath)
	if err != nil {
		return BackupResult{}, err
	}
	envelopeSize, envelopeSHA, err := fileSizeAndSHA256(envelopePath)
	if err != nil {
		return BackupResult{}, err
	}
	receipt := backupSetReceipt{
		Type:                    "backup",
		CreatedAt:               plaintext.CreatedAt.Format(time.RFC3339Nano),
		Envelope:                backupEnvelopeBasename,
		EnvelopeVersion:         2,
		KeyFingerprint:          keyFingerprint,
		EnvelopeSizeBytes:       envelopeSize,
		EnvelopeSHA256:          envelopeSHA,
		ManifestFormatEncrypted: true,
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return BackupResult{}, err
	}
	if err := writeFileSync0600(filepath.Join(partialDir, backupReceiptBasename), append(raw, '\n')); err != nil {
		return BackupResult{}, err
	}
	if err := syncDir(partialDir); err != nil {
		return BackupResult{}, err
	}
	if err := destRoot.RemoveAll(stagingName); err != nil {
		return BackupResult{}, fmt.Errorf("remove plaintext backup staging: %w", err)
	}
	if err := syncDir(canonicalDest); err != nil {
		return BackupResult{}, err
	}
	if err := destRoot.Rename(partialName, name); err != nil {
		return BackupResult{}, err
	}
	if err := syncDir(canonicalDest); err != nil {
		return BackupResult{}, err
	}
	published = true
	envelopePath = filepath.Join(finalDir, backupEnvelopeBasename)
	plaintext.Directory = finalDir
	plaintext.Manifest = envelopePath
	plaintext.Envelope = envelopePath
	plaintext.Catalog.BackupPath = envelopePath
	plaintext.Events.BackupPath = envelopePath
	for index := range plaintext.EncryptedLibraries {
		plaintext.EncryptedLibraries[index].BackupPath = envelopePath
	}
	slog.InfoContext(ctx, "encrypted backup written", "directory", finalDir)
	return plaintext, nil
}

func (s *Store) backupPlaintextWithOptions(ctx context.Context, destDir string, opts BackupOptions) (BackupResult, error) {
	if destDir == "" {
		return BackupResult{}, errors.New("backup destination is required")
	}
	if err := validateBackupLibraryRoots(opts.LibraryRoots); err != nil {
		return BackupResult{}, err
	}
	canonicalDest, destRoot, err := s.prepareBackupDestination(destDir)
	if err != nil {
		return BackupResult{}, err
	}
	defer func() { _ = destRoot.Close() }()
	destDir = canonicalDest
	createdAt := time.Now().UTC()
	name := "beamfall-" + createdAt.Format("20060102T150405.000000000Z")
	finalDir := filepath.Join(destDir, name)
	partialName := name + ".partial"
	partialDir := filepath.Join(destDir, partialName)
	if err := destRoot.RemoveAll(partialName); err != nil {
		return BackupResult{}, err
	}
	if err := destRoot.Mkdir(partialName, 0o700); err != nil {
		return BackupResult{}, err
	}
	partialDir, err = confinedBackupPath(destDir, partialDir)
	if err != nil {
		return BackupResult{}, err
	}
	cleanupPartial := true
	defer func() {
		if cleanupPartial {
			_ = destRoot.RemoveAll(partialName)
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return BackupResult{}, err
	}

	catalog, err := snapshotDatabase(ctx, s.catalogW, s.catalogPath, "catalog", filepath.Join(partialDir, "catalog.db"))
	if err != nil {
		return BackupResult{}, err
	}
	events, err := snapshotDatabase(ctx, s.eventsW, s.eventsPath, "events", filepath.Join(partialDir, "events.db"))
	if err != nil {
		return BackupResult{}, err
	}
	encrypted, err := s.snapshotEncryptedLibraryDBsLocked(ctx, partialDir, finalDir, opts.EncryptedLibraryDBCipher)
	if err != nil {
		return BackupResult{}, err
	}
	catalog.BackupPath = filepath.Join(finalDir, "catalog.db")
	events.BackupPath = filepath.Join(finalDir, "events.db")
	manifestPath := filepath.Join(partialDir, "manifest.json")
	schemaVersions := map[string]int{
		"catalog": catalog.SchemaVersion,
		"events":  events.SchemaVersion,
	}
	databases := map[string]BackupDatabaseSnapshot{
		"catalog": catalog,
		"events":  events,
	}
	for _, snapshot := range encrypted {
		key := "encrypted_library/" + snapshot.LibraryID
		schemaVersions[key] = snapshot.SchemaVersion
		databases[key] = snapshot
	}
	operationalFiles, err := snapshotOperationalFiles(s.dataDir, partialDir, finalDir)
	if err != nil {
		return BackupResult{}, err
	}
	libraryRoots := opts.LibraryRoots
	if libraryRoots == nil {
		libraryRoots, err = s.backupLibraryRootsLocked(ctx)
		if err != nil {
			return BackupResult{}, err
		}
	}
	manifest := backupSetManifest{
		Type:                       "backup",
		CreatedAt:                  createdAt.Format(time.RFC3339Nano),
		CreatedBy:                  "beamfall-store",
		Reason:                     "on-demand",
		RestoreInstructionsVersion: backupRestoreInstructionsVersion,
		ConsistencyProtocol:        "store-read-lock write barrier; encrypted-library working snapshots are encrypted before manifesting",
		SchemaVersions:             schemaVersions,
		Databases:                  databases,
		OperationalFiles:           operationalFiles,
		LibraryRoots:               normalizeBackupLibraryRoots(libraryRoots),
		Notes: []string{
			"Database snapshots are consistent SQLite VACUUM INTO outputs.",
			"Encrypted-library DB snapshots are stored only as ciphertext artifacts in this backup set.",
			"Operational identity, configuration, and integration-state files are checksummed in this backup set; ACME and regenerable state are excluded.",
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupResult{}, err
	}
	if err := writeFileSync0600(manifestPath, append(data, '\n')); err != nil {
		return BackupResult{}, err
	}
	// Flush every payload and nested directory before the rename publishes the
	// set. Syncing only partialDir would not make file contents or entries in the
	// encrypted-library subdirectories durable.
	if err := syncTree(partialDir); err != nil {
		return BackupResult{}, err
	}
	if err := destRoot.Rename(partialName, name); err != nil {
		return BackupResult{}, err
	}
	// fsync the destination's parent so the rename itself is durable: the backup
	// is only "written" once the directory entry survives a crash.
	if err := syncDir(destDir); err != nil {
		return BackupResult{}, err
	}
	cleanupPartial = false
	slog.InfoContext(ctx, "backup written", "directory", finalDir)
	return BackupResult{
		Directory:          finalDir,
		Manifest:           filepath.Join(finalDir, "manifest.json"),
		Catalog:            catalog,
		Events:             events,
		EncryptedLibraries: encrypted,
		CreatedAt:          createdAt,
	}, nil
}

// guardBackupDest confines a backup destination to a sanctioned path allowlist
// (the PathPicker allowedRoots of ADR-0116, plus the store data dir) and returns
// the canonical absolute destination. It lives in the store layer so the guard
// holds for every caller, not only the graph backup mutation. The path is
// canonicalized first (absolute, symlinks resolved on the existing prefix, `..`
// collapsed by filepath.Clean), then required to sit within one sanctioned root;
// any dest outside every root — e.g. /tmp/escape — is rejected.
func (s *Store) guardBackupDest(destDir string) (string, error) {
	canonical, err := canonicalDestPath(destDir)
	if err != nil {
		return "", err
	}
	for _, root := range s.sanctionedBackupRoots() {
		if pathWithinRoot(root, canonical) {
			return canonical, nil
		}
	}
	return "", ValidationError("backup destination is outside the sanctioned path allowlist")
}

// prepareBackupDestination creates and opens destDir through its matched
// sanctioned root. Root-relative operations reject symlink and traversal
// escapes even if the filesystem changes after the initial canonical check.
func (s *Store) prepareBackupDestination(destDir string) (string, *os.Root, error) {
	canonical, err := s.guardBackupDest(destDir)
	if err != nil {
		return "", nil, err
	}
	matchedRoot := ""
	for _, root := range s.sanctionedBackupRoots() {
		if pathWithinRoot(root, canonical) {
			matchedRoot = root
			break
		}
	}
	if matchedRoot == "" {
		return "", nil, ValidationError("backup destination is outside the sanctioned path allowlist")
	}
	relative, err := filepath.Rel(matchedRoot, canonical)
	if err != nil {
		return "", nil, err
	}
	sanctionedRoot, err := os.OpenRoot(matchedRoot)
	if err != nil {
		return "", nil, err
	}
	if err := sanctionedRoot.MkdirAll(relative, 0o700); err != nil {
		_ = sanctionedRoot.Close()
		return "", nil, err
	}
	destRoot, err := sanctionedRoot.OpenRoot(relative)
	closeErr := sanctionedRoot.Close()
	if err != nil {
		return "", nil, err
	}
	if closeErr != nil {
		_ = destRoot.Close()
		return "", nil, closeErr
	}
	resolved, err := canonicalDestPath(canonical)
	if err != nil {
		_ = destRoot.Close()
		return "", nil, err
	}
	if !pathWithinRoot(matchedRoot, resolved) {
		_ = destRoot.Close()
		return "", nil, ValidationError("backup destination escaped its sanctioned root")
	}
	return resolved, destRoot, nil
}

func confinedBackupPath(root, path string) (string, error) {
	canonicalRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	resolvedRoot, err := canonicalDestPath(canonicalRoot)
	if err != nil {
		return "", err
	}
	if resolvedRoot != canonicalRoot {
		return "", ValidationError("backup root was replaced after validation")
	}
	canonical, err := canonicalDestPath(path)
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(canonicalRoot, canonical) {
		return "", ValidationError("backup path escaped its intended root")
	}
	return canonical, nil
}

func mkdirTempBackupDir(root *os.Root, prefix string) (string, error) {
	for range 100 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", err
		}
		name := prefix + hex.EncodeToString(suffix[:])
		if err := root.Mkdir(name, 0o700); err == nil {
			return name, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("backup: unable to allocate staging directory")
}

// canonicalDestPath canonicalizes a backup destination that may not exist yet:
// it makes the path absolute and clean (collapsing `..`), resolves symlinks on
// the nearest existing ancestor, then re-appends the not-yet-created tail. This
// yields a stable canonical path so containment is checked against the same
// resolved form the store's allowedRoots carry — without requiring the dest to
// already exist (a backup dir is created after this guard).
func canonicalDestPath(destDir string) (string, error) {
	trimmed := strings.TrimSpace(destDir)
	if trimmed == "" {
		return "", ValidationError("backup destination is required")
	}
	abs, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", err
	}
	existing := abs
	tail := ""
	for {
		resolved, evalErr := filepath.EvalSymlinks(existing)
		if evalErr == nil {
			return filepath.Join(resolved, tail), nil
		}
		if !errors.Is(evalErr, os.ErrNotExist) {
			return "", evalErr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs, nil
		}
		tail = filepath.Join(filepath.Base(existing), tail)
		existing = parent
	}
}

// sanctionedBackupRoots is the allowlist a backup destination must fall within:
// the PathPicker allowedRoots and the store data dir. Roots are
// canonicalized so containment is checked against resolved absolute paths.
func (s *Store) sanctionedBackupRoots() []string {
	roots := make([]string, 0, len(s.allowedRoots)+1)
	if dataRoot, err := cleanPathPickerRoot(s.dataDir); err == nil && dataRoot != string(filepath.Separator) {
		roots = append(roots, dataRoot)
	}
	for _, root := range s.allowedRoots {
		cleaned, err := cleanPathPickerRoot(root)
		if err == nil && cleaned != string(filepath.Separator) {
			roots = append(roots, cleaned)
		}
	}
	return roots
}

func RestoreBackup(ctx context.Context, manifestPath string, dataDir string) error {
	report, err := RestoreBackupWithOptions(ctx, manifestPath, dataDir, RestoreOptions{})
	for _, warning := range report.Warnings {
		slog.WarnContext(ctx, "backup restore compatibility warning", "code", warning.Code, "message", warning.Message)
	}
	return err
}

// RollbackMigrationSnapshot atomically restores only the live DB tree from a
// plaintext pre-migration v2 snapshot. Identity and configuration remain
// untouched, and the replaced DB tree is retained for recovery.
func RollbackMigrationSnapshot(ctx context.Context, manifestPath, dataDir string) (string, error) {
	if strings.TrimSpace(manifestPath) == "" {
		return "", errors.New("pre-migration manifest is required")
	}
	if strings.TrimSpace(dataDir) == "" {
		return "", errors.New("rollback data-dir is required")
	}
	manifest, err := readVerifiedBackupManifest(ctx, manifestPath)
	if err != nil {
		return "", err
	}
	if manifest.Type != migrationSnapshotType || manifest.RestoreInstructionsVersion != migrationSnapshotVersion {
		return "", errors.New("rollback requires a pre-migration manifest v2")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	staged, err := os.MkdirTemp(dataDir, ".rollback-db-")
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(staged)
		}
	}()
	if err := restoreInto(ctx, staged, manifest); err != nil {
		return "", err
	}
	liveDB := filepath.Join(dataDir, "db")
	previous := liveDB + ".pre-rollback-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if _, err := os.Stat(liveDB); err == nil {
		if err := os.Rename(liveDB, previous); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	} else {
		previous = ""
	}
	if err := os.Rename(staged, liveDB); err != nil {
		if previous != "" {
			_ = os.Rename(previous, liveDB)
		}
		return "", err
	}
	if err := syncDir(dataDir); err != nil {
		return previous, err
	}
	cleanup = false
	return previous, nil
}

func RestoreBackupWithOptions(ctx context.Context, manifestPath string, dataDir string, opts RestoreOptions) (RestoreReport, error) {
	if strings.TrimSpace(manifestPath) == "" {
		return RestoreReport{}, errors.New("backup manifest is required")
	}
	if strings.TrimSpace(dataDir) == "" {
		return RestoreReport{}, errors.New("restore data-dir is required")
	}
	var decryptedSetDir string
	if filepath.Base(manifestPath) == backupEnvelopeBasename {
		if opts.SnapshotOpener == nil {
			return RestoreReport{}, errors.New("encrypted backup requires a BackupKey")
		}
		parent := filepath.Dir(dataDir)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return RestoreReport{}, err
		}
		reserved, err := os.MkdirTemp(parent, "."+filepath.Base(dataDir)+".decrypt-")
		if err != nil {
			return RestoreReport{}, err
		}
		if err := os.Remove(reserved); err != nil {
			return RestoreReport{}, err
		}
		decryptedSetDir = reserved
		defer os.RemoveAll(decryptedSetDir)
		if err := opts.SnapshotOpener.OpenDirectory(ctx, manifestPath, decryptedSetDir); err != nil {
			return RestoreReport{}, err
		}
		manifestPath = filepath.Join(decryptedSetDir, backupManifestBasename)
	}
	if err := recoverRestorePublish(dataDir); err != nil {
		return RestoreReport{}, fmt.Errorf("recover interrupted restore publish: %w", err)
	}
	manifest, err := readVerifiedBackupManifest(ctx, manifestPath)
	if err != nil {
		return RestoreReport{}, err
	}
	report := RestoreReport{Warnings: restoreCompatibilityWarnings(manifest)}
	currentLibraryRoots := opts.CurrentLibraryRoots
	if currentLibraryRoots == nil {
		currentLibraryRoots = currentBackupLibraryRoots(manifest.LibraryRoots)
	}
	report.Warnings = append(report.Warnings, reconcileLibraryRoots(manifest.LibraryRoots, currentLibraryRoots, opts.VolumeMismatchWarningThreshold)...)
	if err := prepareRestoreTarget(dataDir, manifest, opts.ReplaceIdentity); err != nil {
		return report, err
	}
	parent := filepath.Dir(dataDir)
	stageDir, err := os.MkdirTemp(parent, "."+filepath.Base(dataDir)+".restore-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(stageDir)
	if err := restoreInto(ctx, filepath.Join(stageDir, "db"), manifest); err != nil {
		return report, err
	}
	reconciliation, warnings := reconcileCatalogMediaReferences(
		ctx,
		filepath.Join(stageDir, "db", "catalog.db"),
		opts.MediaMissingWarningThreshold,
	)
	report.MediaReconciliation = reconciliation
	report.Warnings = append(report.Warnings, warnings...)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := restoreOperationalFiles(manifest, stageDir); err != nil {
		return report, err
	}
	if err := publishStagedRestore(stageDir, dataDir, manifest, opts.ReplaceIdentity); err != nil {
		return report, err
	}
	return report, nil
}

func reconcileCatalogMediaReferences(ctx context.Context, catalogPath string, threshold float64) (MediaReconciliationReport, []RestoreWarning) {
	report := MediaReconciliationReport{Severity: "clean"}
	db, err := openSQLiteReader(catalogPath)
	if err != nil {
		return unavailableMediaReconciliationReport(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT path FROM media_file WHERE missing_at_ms IS NULL ORDER BY path`)
	if err != nil {
		return unavailableMediaReconciliationReport(err)
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return unavailableMediaReconciliationReport(err)
		}
		report.TotalReferences++
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			report.MissingReferences++
		}
	}
	if err := rows.Err(); err != nil {
		return unavailableMediaReconciliationReport(err)
	}
	if report.MissingReferences == 0 {
		return report, nil
	}
	report.MissingRatio = float64(report.MissingReferences) / float64(report.TotalReferences)
	if threshold <= 0 || threshold > 1 {
		threshold = 0.5
	}
	if report.MissingRatio >= threshold {
		report.Severity = "wrong-volume-mapping"
		return report, []RestoreWarning{{
			Code: "media_volume_mapping",
			Message: fmt.Sprintf(
				"wrong volume mapping? %d of %d catalog media references are unavailable",
				report.MissingReferences,
				report.TotalReferences,
			),
		}}
	}
	report.Severity = "routine-drift"
	return report, []RestoreWarning{{
		Code: "media_reference_drift",
		Message: fmt.Sprintf(
			"media reference drift: %d of %d catalog media references are unavailable",
			report.MissingReferences,
			report.TotalReferences,
		),
	}}
}

func unavailableMediaReconciliationReport(_ error) (MediaReconciliationReport, []RestoreWarning) {
	return MediaReconciliationReport{Severity: "unavailable"}, []RestoreWarning{{
		Code:    "media_reference_reconciliation_unavailable",
		Message: "media reference reconciliation unavailable; restore continued without checking catalog media paths",
	}}
}

func restoreCompatibilityWarnings(manifest backupSetManifest) []RestoreWarning {
	if manifest.RestoreInstructionsVersion != 1 {
		return []RestoreWarning{}
	}
	return []RestoreWarning{
		{Code: "legacy_manifest_missing_secrets", Message: "manifest v1 has no secrets/ payload; secrets were not restored"},
		{Code: "legacy_manifest_missing_stream_token", Message: "manifest v1 has no stream-token.key payload; stream-token.key was not restored"},
		{Code: "legacy_manifest_missing_tls", Message: "manifest v1 has no tls/ payload; TLS identity was not restored"},
		{Code: "legacy_manifest_missing_config", Message: "manifest v1 has no config/ payload; configuration was not restored"},
		{Code: "legacy_manifest_missing_integration_state", Message: "manifest v1 has no integration JSON payload; integration state was not restored"},
	}
}

func normalizeBackupLibraryRoots(roots []BackupLibraryRoot) []BackupLibraryRoot {
	normalized := make([]BackupLibraryRoot, 0, len(roots))
	seen := map[string]struct{}{}
	for _, root := range roots {
		root.LibraryID = strings.TrimSpace(root.LibraryID)
		root.Path = filepath.Clean(strings.TrimSpace(root.Path))
		root.VolumeID = strings.TrimSpace(root.VolumeID)
		if root.Path == "." || root.Path == "" {
			continue
		}
		key := root.LibraryID + "\x00" + root.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, root)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].LibraryID != normalized[j].LibraryID {
			return normalized[i].LibraryID < normalized[j].LibraryID
		}
		return normalized[i].Path < normalized[j].Path
	})
	return normalized
}

func (s *Store) backupLibraryRootsLocked(ctx context.Context) ([]BackupLibraryRoot, error) {
	rows, err := s.catalogR.QueryContext(ctx, `SELECT library_id, root FROM library_roots ORDER BY library_id, root`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roots := []BackupLibraryRoot{}
	for rows.Next() {
		var root BackupLibraryRoot
		if err := rows.Scan(&root.LibraryID, &root.Path); err != nil {
			return nil, err
		}
		root.VolumeID = volumeIDForRoot(root.Path)
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

func currentBackupLibraryRoots(recorded []BackupLibraryRoot) []BackupLibraryRoot {
	current := make([]BackupLibraryRoot, 0, len(recorded))
	for _, root := range recorded {
		root.VolumeID = volumeIDForRoot(root.Path)
		current = append(current, root)
	}
	return current
}

func volumeIDForRoot(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if volume := filepath.VolumeName(cleaned); volume != "" {
		return "volume:" + strings.ToLower(filepath.Clean(volume))
	}
	if info, err := os.Stat(cleaned); err == nil {
		value := reflect.ValueOf(info.Sys())
		if value.Kind() == reflect.Pointer && !value.IsNil() {
			value = value.Elem()
		}
		if value.Kind() == reflect.Struct {
			device := value.FieldByName("Dev")
			switch device.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				return fmt.Sprintf("device:%d", device.Int())
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				return fmt.Sprintf("device:%d", device.Uint())
			}
		}
	}
	digest := sha256.Sum256([]byte(cleaned))
	return "unavailable:" + hex.EncodeToString(digest[:8])
}

func validateBackupLibraryRoots(roots []BackupLibraryRoot) error {
	seen := map[string]struct{}{}
	for _, root := range roots {
		path := strings.TrimSpace(root.Path)
		if path == "" {
			return ValidationError("backup library root path is required")
		}
		if strings.TrimSpace(root.VolumeID) == "" {
			return ValidationError("backup library root volume id is required")
		}
		key := strings.TrimSpace(root.LibraryID) + "\x00" + filepath.Clean(path)
		if _, ok := seen[key]; ok {
			return ValidationError("backup library roots contain a duplicate")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func reconcileLibraryRoots(recorded, current []BackupLibraryRoot, threshold float64) []RestoreWarning {
	if len(recorded) == 0 {
		return []RestoreWarning{}
	}
	if current == nil {
		return []RestoreWarning{{
			Code:    "library_root_reconciliation_unavailable",
			Message: "library root reconciliation unavailable: the restore host did not provide current roots and volume IDs",
		}}
	}
	recorded = normalizeBackupLibraryRoots(recorded)
	current = normalizeBackupLibraryRoots(current)
	if len(recorded) == 0 {
		return []RestoreWarning{}
	}
	if threshold <= 0 || threshold > 1 {
		threshold = 0.5
	}
	matched := make([]bool, len(current))
	changed := 0
	unmatchedRecorded := 0
	for _, want := range recorded {
		match := -1
		for i, got := range current {
			if !matched[i] && got == want {
				match = i
				break
			}
		}
		if match < 0 {
			for i, got := range current {
				if matched[i] {
					continue
				}
				if (want.LibraryID != "" && got.LibraryID == want.LibraryID) ||
					(want.LibraryID == "" && got.Path == want.Path) {
					match = i
					break
				}
			}
		}
		if match < 0 {
			unmatchedRecorded++
			continue
		}
		matched[match] = true
		if current[match].Path != want.Path || current[match].VolumeID != want.VolumeID {
			changed++
		}
	}
	unmatchedCurrent := 0
	for _, used := range matched {
		if !used {
			unmatchedCurrent++
		}
	}
	mismatches := changed + max(unmatchedRecorded, unmatchedCurrent)
	if mismatches == 0 {
		return []RestoreWarning{}
	}
	denominator := max(len(recorded), len(current))
	ratio := float64(mismatches) / float64(denominator)
	if ratio >= threshold {
		return []RestoreWarning{{
			Code:    "library_volume_mapping",
			Message: fmt.Sprintf("wrong volume mapping? %d of %d library roots differ between the backup and restore host", mismatches, denominator),
		}}
	}
	return []RestoreWarning{{
		Code:    "library_root_drift",
		Message: fmt.Sprintf("library root drift: %d of %d library roots differ between the backup and restore host", mismatches, denominator),
	}}
}

func VerifyBackupManifest(ctx context.Context, manifestPath string) error {
	_, err := readVerifiedBackupManifest(ctx, manifestPath)
	return err
}

// PreflightRestoreIdentity verifies a backup and compares its complete v2
// secrets/ and tls/ digest sets with a live target without mutating either.
// Callers that snapshot or rename a live data directory before restore must run
// this preflight first so the identity replacement decision cannot be bypassed.
func PreflightRestoreIdentity(ctx context.Context, manifestPath, dataDir string, replaceIdentity bool) error {
	if strings.TrimSpace(dataDir) == "" {
		return errors.New("restore data-dir is required")
	}
	manifest, err := readVerifiedBackupManifest(ctx, manifestPath)
	if err != nil {
		return err
	}
	if manifest.RestoreInstructionsVersion == 1 {
		return nil
	}
	return ensureNoIdentityConflict(dataDir, manifest, replaceIdentity)
}

// backupManifestBasename is the fixed manifest file name inside a published
// backup set. The confined verifier requires it so a symlinked file elsewhere
// cannot masquerade as a set manifest.
const backupManifestBasename = "manifest.json"

// ensureBackupSchemaWithinBinary refuses a manifest whose catalog, events, or
// encrypted-library database schema version exceeds what this binary's frozen
// migrations understand. Restoring a forward-incompatible schema would leave a
// data dir the running binary cannot open.
func ensureBackupSchemaWithinBinary(manifest backupSetManifest) error {
	maxima := map[string]int{
		"catalog": schemaFreezeCatalogMigrations,
		"events":  schemaFreezeEventsMigrations,
	}
	for name, max := range maxima {
		snapshot, ok := manifest.Databases[name]
		if !ok {
			return fmt.Errorf("backup manifest missing %s snapshot", name)
		}
		if snapshot.SchemaVersion > max {
			return fmt.Errorf("backup manifest %s schema version %d is newer than this binary supports (%d)", name, snapshot.SchemaVersion, max)
		}
	}
	for key, snapshot := range manifest.Databases {
		if snapshot.Database != encryptedLibraryDBLabel {
			continue
		}
		if snapshot.SchemaVersion > schemaFreezeEncryptedLibraryMigrations {
			return fmt.Errorf("backup manifest %s encrypted-library schema version %d is newer than this binary supports (%d)", key, snapshot.SchemaVersion, schemaFreezeEncryptedLibraryMigrations)
		}
	}
	return nil
}

// BackupSetSummary is one entry of a backup destination's history: the published
// set directory, when it was created, its on-disk size, and whether it passes
// confined integrity verification. Error carries a sanitized reason when Valid
// is false — never a computed checksum of an arbitrary file.
type BackupSetSummary struct {
	Directory string
	CreatedAt time.Time
	SizeBytes int64
	Valid     bool
	Error     string
}

// BackupHistory lists the published backup sets under destDir (newest first),
// verifying each through the confined VerifyBackupSet so no client-facing caller
// can turn history into a file-read oracle. destDir is guarded to the sanctioned
// backup allowlist.
func (s *Store) BackupHistory(ctx context.Context, destDir string) ([]BackupSetSummary, error) {
	canonicalDest, err := s.guardBackupDest(destDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(canonicalDest)
	if os.IsNotExist(err) {
		return []BackupSetSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	summaries := make([]BackupSetSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !publishedBackupSetName(entry.Name()) {
			continue
		}
		setDir := filepath.Join(canonicalDest, entry.Name())
		summaries = append(summaries, summarizeBackupSet(ctx, s, setDir))
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})
	return summaries, nil
}

func (s *Store) EncryptedLibraryBackupRotationArtifacts(ctx context.Context, libraryID LibraryID) ([]BackupRotationArtifact, error) {
	if strings.TrimSpace(string(libraryID)) == "" {
		return nil, ValidationError("library id is required")
	}
	root := filepath.Join(s.dataDir, "backups")
	component := safeLibraryPathComponent(libraryID)
	artifacts := []BackupRotationArtifact{}

	preMigrationDir := filepath.Join(root, "pre-migration", "encrypted-library", component)
	if entries, err := os.ReadDir(preMigrationDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), encryptedLibraryDBCipherExt) {
				continue
			}
			artifacts = append(artifacts, BackupRotationArtifact{
				Kind:   "pre-migration-snapshot",
				Path:   filepath.Join(preMigrationDir, entry.Name()),
				Action: "destroy-or-explicitly-retain",
			})
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if sessions, err := migrationSnapshots(filepath.Join(root, "pre-migration")); err == nil {
		for _, session := range sessions {
			for _, snapshot := range session.manifest.Databases {
				if snapshot.Database != encryptedLibraryDBLabel || snapshot.LibraryID != string(libraryID) {
					continue
				}
				artifacts = append(artifacts, BackupRotationArtifact{
					Kind:     "pre-migration-snapshot",
					Path:     snapshot.BackupPath,
					Manifest: filepath.Join(session.path, backupManifestBasename),
					Action:   "destroy-or-explicitly-retain",
				})
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	hasBackupSets := false
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !entry.IsDir() || !publishedBackupSetName(entry.Name()) {
			continue
		}
		envelopePath := filepath.Join(root, entry.Name(), backupEnvelopeBasename)
		if receipt, receiptErr := readBackupSetReceipt(filepath.Join(root, entry.Name(), backupReceiptBasename)); receiptErr == nil &&
			receipt.Type == "backup" && receipt.ManifestFormatEncrypted {
			// Encrypted-by-default sets intentionally expose no library inventory.
			// Conservatively flag every opaque set: production backups include all
			// encrypted libraries that existed when the snapshot was taken.
			hasBackupSets = true
			artifacts = append(artifacts, BackupRotationArtifact{
				Kind:     "backup-manifest-entry",
				Path:     envelopePath,
				Manifest: envelopePath,
				Action:   "rotate-or-destroy",
			})
			continue
		}
		manifestPath := filepath.Join(root, entry.Name(), backupManifestBasename)
		manifest, err := readManifestFile(manifestPath)
		if err != nil {
			continue
		}
		for _, snapshot := range manifest.Databases {
			if snapshot.Database != encryptedLibraryDBLabel || snapshot.LibraryID != string(libraryID) {
				continue
			}
			hasBackupSets = true
			artifacts = append(artifacts, BackupRotationArtifact{
				Kind:     "backup-manifest-entry",
				Path:     snapshot.BackupPath,
				Manifest: manifestPath,
				Action:   "rotate-or-destroy",
			})
		}
	}

	// ADR-0115 off-site destinations receive replicated copies of exactly these
	// encrypted-library backup sets, so old-DEK ciphertext may exist off-site
	// whenever a replicable backup set exists. We FLAG that residual copy here;
	// we deliberately do NOT rotate it: off-site ciphertext is wrapped under a
	// separate BackupKey (internal/backup replication), not the library DEK, so
	// the DEK re-wrap this response performs cannot invalidate it — the BackupKey
	// must be rotated independently.
	//
	// The concrete destination list is intentionally not enumerated: replication
	// is driven by a runtime BucketClient assembled at server start, the
	// destination config is not persisted under the store data dir, no local
	// replication receipt is written (objects/receipts live in the remote
	// bucket), and the graph Handler holds no replication config. Enumerating the
	// specific destinations needs cross-package wiring of a replication
	// destination registry that does not exist today — tracked as a follow-up.
	if hasBackupSets {
		artifacts = append(artifacts, BackupRotationArtifact{
			Kind:   "offsite-destination",
			Action: "rotate-backup-key-separately",
		})
	}

	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Kind != artifacts[j].Kind {
			return artifacts[i].Kind < artifacts[j].Kind
		}
		if artifacts[i].Manifest != artifacts[j].Manifest {
			return artifacts[i].Manifest < artifacts[j].Manifest
		}
		return artifacts[i].Path < artifacts[j].Path
	})
	return artifacts, nil
}

func summarizeBackupSet(ctx context.Context, s *Store, setDir string) BackupSetSummary {
	summary := BackupSetSummary{Directory: setDir}
	if size, err := dirSizeBytes(setDir); err == nil {
		summary.SizeBytes = size
	}
	envelopePath := filepath.Join(setDir, backupEnvelopeBasename)
	receipt, receiptErr := readBackupSetReceipt(filepath.Join(setDir, backupReceiptBasename))
	if receiptErr == nil {
		summary.CreatedAt, _ = time.Parse(time.RFC3339Nano, receipt.CreatedAt)
	} else {
		manifest, err := readManifestFile(filepath.Join(setDir, backupManifestBasename))
		if err == nil {
			summary.CreatedAt, _ = time.Parse(time.RFC3339Nano, manifest.CreatedAt)
		}
		envelopePath = filepath.Join(setDir, backupManifestBasename)
	}
	if verifyErr := s.VerifyBackupSet(ctx, envelopePath); verifyErr != nil {
		summary.Valid = false
		summary.Error = verifyErr.Error()
		return summary
	}
	summary.Valid = true
	return summary
}

// VerifyBackupSet verifies a published backup set through a confined path model
// so it is safe to expose to admin clients. It additionally requires the
// manifest.json to sit inside a sanctioned backup root and returns only a
// sanitized failure reason. Both online verification and offline restore derive
// snapshot and operational paths from the manifest directory rather than
// trusting stored absolute BackupPath values.
func (s *Store) VerifyBackupSet(ctx context.Context, manifestPath string) error {
	if filepath.Base(manifestPath) == backupEnvelopeBasename {
		return s.verifyEncryptedBackupSet(ctx, manifestPath)
	}
	setDir, err := s.resolveBackupSetDir(manifestPath)
	if err != nil {
		return err
	}
	manifest, err := readManifestFile(filepath.Join(setDir, backupManifestBasename))
	if err != nil {
		return errors.New("backup set manifest could not be read")
	}
	if manifest.RestoreInstructionsVersion < 1 || manifest.RestoreInstructionsVersion > backupRestoreInstructionsVersion || manifest.ConsistencyProtocol == "" {
		return errors.New("backup set manifest is unsupported")
	}
	for _, required := range []string{"catalog", "events"} {
		snapshot, ok := manifest.Databases[required]
		if !ok || snapshot.Database != required {
			return errors.New("backup set is missing a required database")
		}
	}
	for name, snapshot := range manifest.Databases {
		path, derr := confinedSnapshotPath(setDir, snapshot)
		if derr != nil {
			return fmt.Errorf("backup set entry %s failed integrity verification", sanitizeEntryName(name))
		}
		if verifyBackupSnapshotBytesConfined(snapshot, path) != nil {
			return fmt.Errorf("backup set entry %s failed integrity verification", sanitizeEntryName(name))
		}
	}
	if manifest.RestoreInstructionsVersion == backupRestoreInstructionsVersion {
		for name, payload := range manifest.OperationalFiles {
			path, derr := confinedOperationalPath(setDir, payload)
			if derr != nil || validateOperationalEntry(name, payload) != nil || verifyOperationalFile(payload, path) != nil {
				return fmt.Errorf("backup set operational entry %s failed integrity verification", sanitizeOperationalName(name))
			}
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (s *Store) verifyEncryptedBackupSet(ctx context.Context, envelopePath string) error {
	resolved, err := filepath.EvalSymlinks(envelopePath)
	if err != nil || filepath.Base(resolved) != backupEnvelopeBasename {
		return errors.New("backup set envelope could not be read")
	}
	setDir := filepath.Dir(resolved)
	allowed := false
	for _, root := range s.sanctionedBackupRoots() {
		if pathWithinRoot(root, setDir) {
			allowed = true
			break
		}
	}
	if !allowed {
		return errors.New("backup set is outside the sanctioned path allowlist")
	}
	entries, err := os.ReadDir(setDir)
	if err != nil {
		return errors.New("backup set envelope could not be read")
	}
	if len(entries) != 2 {
		return errors.New("encrypted backup set contains unexpected plaintext or metadata")
	}
	for _, entry := range entries {
		if entry.IsDir() || (entry.Name() != backupEnvelopeBasename && entry.Name() != backupReceiptBasename) {
			return errors.New("encrypted backup set contains unexpected plaintext or metadata")
		}
	}
	receipt, err := readBackupSetReceipt(filepath.Join(setDir, backupReceiptBasename))
	if err != nil || receipt.Type != "backup" || receipt.Envelope != backupEnvelopeBasename ||
		receipt.EnvelopeVersion != 2 || !receipt.ManifestFormatEncrypted || receipt.KeyFingerprint == "" {
		return errors.New("backup set receipt is unsupported")
	}
	size, checksum, err := fileSizeAndSHA256(resolved)
	if err != nil || size != receipt.EnvelopeSizeBytes || checksum != receipt.EnvelopeSHA256 {
		return errors.New("backup set envelope failed integrity verification")
	}
	src, err := os.Open(resolved) //nolint:gosec // resolved path is confined to a sanctioned backup set
	if err != nil {
		return errors.New("backup set envelope could not be read")
	}
	defer src.Close()
	if s.snapshotSealer == nil {
		return errors.New("backup set envelope verifier unavailable")
	}
	if err := s.snapshotSealer.VerifyEnvelopeStructure(src); err != nil {
		return errors.New("backup set envelope is malformed")
	}
	return ctx.Err()
}

func readBackupSetReceipt(path string) (backupSetReceipt, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed receipt filename inside a backup set
	if err != nil {
		return backupSetReceipt{}, err
	}
	var receipt backupSetReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return backupSetReceipt{}, err
	}
	return receipt, nil
}

// resolveBackupSetDir canonicalizes the manifest path (resolving symlinks),
// requires the basename to be manifest.json, requires its directory to sit
// within a sanctioned backup root, and returns that resolved set directory.
func (s *Store) resolveBackupSetDir(manifestPath string) (string, error) {
	if strings.TrimSpace(manifestPath) == "" {
		return "", errors.New("backup manifest is required")
	}
	resolved, err := filepath.EvalSymlinks(manifestPath)
	if err != nil {
		return "", errors.New("backup set manifest could not be read")
	}
	if filepath.Base(resolved) != backupManifestBasename {
		return "", errors.New("backup set manifest path is not a manifest.json")
	}
	setDir := filepath.Dir(resolved)
	for _, root := range s.sanctionedBackupRoots() {
		if pathWithinRoot(root, setDir) {
			return setDir, nil
		}
	}
	return "", errors.New("backup set is outside the sanctioned path allowlist")
}

// confinedSnapshotPath derives a snapshot's on-disk path from the set layout
// (never the manifest's stored absolute BackupPath) and requires it to
// canonicalize within setDir, so a crafted manifest cannot escape the set.
func confinedSnapshotPath(setDir string, snapshot BackupDatabaseSnapshot) (string, error) {
	var rel string
	switch {
	case snapshot.Database == encryptedLibraryDBLabel:
		if snapshot.LibraryID == "" {
			return "", errors.New("encrypted-library snapshot missing library id")
		}
		component := safeLibraryPathComponent(LibraryID(snapshot.LibraryID))
		rel = filepath.Join("encrypted-library", component, "encrypted_library"+encryptedLibraryDBCipherExt)
	case snapshot.Database == "catalog":
		rel = "catalog.db"
	case snapshot.Database == "events":
		rel = "events.db"
	default:
		return "", fmt.Errorf("unsupported snapshot database %q", snapshot.Database)
	}
	candidate := filepath.Join(setDir, rel)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", errors.New("snapshot path could not be resolved")
	}
	if !pathWithinRoot(setDir, resolved) {
		return "", errors.New("snapshot path escapes backup set")
	}
	return resolved, nil
}

// verifyBackupSnapshotBytesConfined checks the derived file against the
// manifest's recorded size and sha256, returning only a generic error (callers
// sanitize further) so no computed checksum reaches a client.
func verifyBackupSnapshotBytesConfined(snapshot BackupDatabaseSnapshot, path string) error {
	if snapshot.SHA256 == "" {
		return errors.New("snapshot missing checksum")
	}
	size, checksum, err := fileSizeAndSHA256(path)
	if err != nil {
		return err
	}
	if size != snapshot.SizeBytes || checksum != snapshot.SHA256 {
		return errors.New("snapshot integrity mismatch")
	}
	return nil
}

func sanitizeEntryName(name string) string {
	if name == "catalog" || name == "events" || strings.HasPrefix(name, "encrypted_library/") {
		return name
	}
	return "database"
}

func readManifestFile(manifestPath string) (backupSetManifest, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return backupSetManifest{}, err
	}
	var manifest backupSetManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return backupSetManifest{}, err
	}
	return manifest, nil
}

func dirSizeBytes(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, ierr := entry.Info()
		if ierr != nil {
			return ierr
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func publishedBackupSetName(name string) bool {
	const prefix = "beamfall-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	_, err := time.Parse("20060102T150405.000000000Z", strings.TrimPrefix(name, prefix))
	return err == nil
}

// PruneBackups keeps the newest keep published backup sets under destDir and
// removes the rest. destDir is guarded to the sanctioned backup allowlist. A
// keep < 1 retains every set (invariant #6 — the backup copies are the
// recoverable state; pruning never wipes them all).
func (s *Store) PruneBackups(ctx context.Context, destDir string, keep int) (int, error) {
	if keep < 1 {
		return 0, nil
	}
	canonicalDest, err := s.guardBackupDest(destDir)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(canonicalDest)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	sets := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && publishedBackupSetName(entry.Name()) {
			sets = append(sets, entry.Name())
		}
	}
	// The set directory name embeds a lexicographically sortable UTC timestamp
	// (beamfall-20060102T150405...Z), so a descending name sort is newest-first.
	sort.Sort(sort.Reverse(sort.StringSlice(sets)))
	pruned := 0
	for _, name := range sets[min(keep, len(sets)):] {
		if ctx.Err() != nil {
			return pruned, ctx.Err()
		}
		if err := os.RemoveAll(filepath.Join(canonicalDest, name)); err != nil {
			return pruned, err
		}
		pruned++
	}
	return pruned, nil
}

func readVerifiedBackupManifest(ctx context.Context, manifestPath string) (backupSetManifest, error) {
	if strings.TrimSpace(manifestPath) == "" {
		return backupSetManifest{}, errors.New("backup manifest is required")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return backupSetManifest{}, err
	}
	var manifest backupSetManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return backupSetManifest{}, err
	}
	if manifest.RestoreInstructionsVersion < 1 {
		return backupSetManifest{}, fmt.Errorf("backup manifest restore instructions version %d is unsupported", manifest.RestoreInstructionsVersion)
	}
	if manifest.RestoreInstructionsVersion > backupRestoreInstructionsVersion {
		return backupSetManifest{}, fmt.Errorf("backup manifest restore instructions version %d is newer than supported version %d", manifest.RestoreInstructionsVersion, backupRestoreInstructionsVersion)
	}
	if manifest.ConsistencyProtocol == "" {
		return backupSetManifest{}, errors.New("backup manifest missing consistency protocol")
	}
	// Refuse a manifest whose database schema version is newer than this binary
	// understands: restoring it would adopt a forward-incompatible schema the
	// running migrations cannot open. Check the entries actually restored
	// (manifest.Databases[*].SchemaVersion), not only the parallel SchemaVersions
	// map, so a stale or missing map cannot bypass the guard.
	if err := ensureBackupSchemaWithinBinary(manifest); err != nil {
		return backupSetManifest{}, err
	}
	resolvedManifest, err := filepath.EvalSymlinks(manifestPath)
	if err != nil {
		return backupSetManifest{}, err
	}
	setDir := filepath.Dir(resolvedManifest)
	for name, snapshot := range manifest.Databases {
		path, err := confinedSnapshotPath(setDir, snapshot)
		if err != nil {
			return backupSetManifest{}, fmt.Errorf("backup manifest entry %s: %w", name, err)
		}
		if err := verifyBackupSnapshotFile(snapshot, path); err != nil {
			return backupSetManifest{}, fmt.Errorf("backup manifest entry %s: %w", name, err)
		}
		snapshot.BackupPath = path
		manifest.Databases[name] = snapshot
	}
	if manifest.RestoreInstructionsVersion == backupRestoreInstructionsVersion {
		for name, payload := range manifest.OperationalFiles {
			if err := validateOperationalEntry(name, payload); err != nil {
				return backupSetManifest{}, fmt.Errorf("backup manifest operational entry %s: %w", name, err)
			}
			path, err := confinedOperationalPath(setDir, payload)
			if err != nil {
				return backupSetManifest{}, fmt.Errorf("backup manifest operational entry %s: %w", name, err)
			}
			if err := verifyOperationalFile(payload, path); err != nil {
				return backupSetManifest{}, fmt.Errorf("backup manifest operational entry %s: %w", name, err)
			}
			payload.BackupPath = path
			manifest.OperationalFiles[name] = payload
		}
	}
	select {
	case <-ctx.Done():
		return backupSetManifest{}, ctx.Err()
	default:
		return manifest, nil
	}
}

func restoreInto(ctx context.Context, dbDir string, manifest backupSetManifest) error {
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return err
	}
	for name, dest := range map[string]string{
		"catalog": filepath.Join(dbDir, "catalog.db"),
		"events":  filepath.Join(dbDir, "events.db"),
	} {
		snapshot, ok := manifest.Databases[name]
		if !ok {
			return fmt.Errorf("backup manifest missing %s snapshot", name)
		}
		if err := copyVerified0600(snapshot, dest); err != nil {
			return err
		}
	}
	for key, snapshot := range manifest.Databases {
		if snapshot.Database != encryptedLibraryDBLabel {
			continue
		}
		if snapshot.LibraryID == "" {
			return fmt.Errorf("backup manifest entry %s missing library id", key)
		}
		if snapshot.CipherFormat != encryptedLibraryDBCipherExt {
			return fmt.Errorf("backup manifest entry %s has unsupported encrypted-library cipher format %q", key, snapshot.CipherFormat)
		}
		if snapshot.MigrationDir != "migrate/encrypted_library" {
			return fmt.Errorf("backup manifest entry %s has unsupported encrypted-library migration dir %q", key, snapshot.MigrationDir)
		}
		dest := filepath.Join(dbDir, "encrypted-library", safeLibraryPathComponent(LibraryID(snapshot.LibraryID)), "encrypted_library"+encryptedLibraryDBCipherExt)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := copyVerified0600(snapshot, dest); err != nil {
			return err
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// copyVerified0600 copies a backup snapshot to dest and verifies the restored
// bytes against the manifest's recorded size and SHA-256, failing closed on any
// mismatch (JBF-7; precedent: internal/backup verifyCiphertext). This catches
// bit-rot or tampering of the backup set before the corrupt copy is adopted as
// live data.
func copyVerified0600(snapshot BackupDatabaseSnapshot, dest string) error {
	if err := copyFile0600(snapshot.BackupPath, dest); err != nil {
		return err
	}
	return verifyBackupSnapshotFile(snapshot, dest)
}

func verifyBackupSnapshot(snapshot BackupDatabaseSnapshot) error {
	return verifyBackupSnapshotFile(snapshot, snapshot.BackupPath)
}

func verifyBackupSnapshotFile(snapshot BackupDatabaseSnapshot, path string) error {
	if snapshot.SHA256 == "" {
		return fmt.Errorf("backup snapshot %s missing sha256 checksum", snapshot.BackupPath)
	}
	size, checksum, err := fileSizeAndSHA256(path)
	if err != nil {
		return err
	}
	if size != snapshot.SizeBytes || checksum != snapshot.SHA256 {
		return fmt.Errorf("restore integrity check failed for %s: size=%d/%d sha256=%s/%s", snapshot.BackupPath, size, snapshot.SizeBytes, checksum, snapshot.SHA256)
	}
	return nil
}

func snapshotOperationalFiles(dataDir, partialDir, finalDir string) (map[string]BackupOperationalFile, error) {
	files := map[string]BackupOperationalFile{}
	for _, root := range []string{"config", "integration-state", "integrations", "secrets", "tls"} {
		sourceRoot := filepath.Join(dataDir, root)
		err := filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(dataDir, path)
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("operational backup refuses symlink %s", rel)
			}
			if entry.IsDir() {
				if entry.Name() == "acme" {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("operational backup refuses non-regular file %s", rel)
			}
			if (root == "integrations" || root == "integration-state") &&
				(!strings.EqualFold(filepath.Ext(rel), ".json") || info.Size() > 1<<20) {
				return nil
			}
			return snapshotOperationalFile(path, rel, partialDir, finalDir, files)
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	for _, name := range []string{"integration-state.json", "stream-token.key"} {
		source := filepath.Join(dataDir, name)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("operational backup refuses non-regular file %s", name)
		}
		if name == "integration-state.json" && info.Size() > 1<<20 {
			continue
		}
		if err := snapshotOperationalFile(source, name, partialDir, finalDir, files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func snapshotOperationalFile(source, rel, partialDir, finalDir string, files map[string]BackupOperationalFile) error {
	rel, err := cleanOperationalRelativePath(rel)
	if err != nil {
		return err
	}
	partialPath := filepath.Join(partialDir, "operational", rel)
	partialRoot, err := os.OpenRoot(partialDir)
	if err != nil {
		return err
	}
	defer func() { _ = partialRoot.Close() }()
	if err := partialRoot.MkdirAll(filepath.Join("operational", filepath.Dir(rel)), 0o700); err != nil {
		return err
	}
	partialPath, err = confinedBackupPath(partialDir, partialPath)
	if err != nil {
		return err
	}
	if err := copyFile0600(source, partialPath); err != nil {
		return err
	}
	size, checksum, err := fileSizeAndSHA256(partialPath)
	if err != nil {
		return err
	}
	files[filepath.ToSlash(rel)] = BackupOperationalFile{
		RelativePath: filepath.ToSlash(rel),
		Kind:         operationalFileKind(rel),
		BackupPath:   filepath.Join(finalDir, "operational", rel),
		SizeBytes:    size,
		SHA256:       checksum,
	}
	return nil
}

func operationalFileKind(rel string) string {
	top := strings.Split(filepath.ToSlash(rel), "/")[0]
	switch top {
	case "secrets", "tls":
		return "identity"
	case "config":
		return "configuration"
	case "stream-token.key":
		return "stream-token"
	default:
		return "integration-state"
	}
}

func cleanOperationalRelativePath(path string) (string, error) {
	path = filepath.FromSlash(strings.TrimSpace(path))
	if path == "" || path == "." || filepath.IsAbs(path) {
		return "", errors.New("operational backup path must be relative")
	}
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("operational backup path escapes the backup set")
	}
	top := strings.Split(cleaned, string(filepath.Separator))[0]
	switch top {
	case "secrets", "tls", "config", "integrations", "integration-state", "stream-token.key", "integration-state.json":
	default:
		return "", fmt.Errorf("unsupported operational backup path %q", filepath.ToSlash(cleaned))
	}
	for _, component := range strings.Split(cleaned, string(filepath.Separator)) {
		if component == "acme" {
			return "", errors.New("ACME state is excluded from operational backups")
		}
	}
	return cleaned, nil
}

func confinedOperationalPath(setDir string, payload BackupOperationalFile) (string, error) {
	rel, err := cleanOperationalRelativePath(payload.RelativePath)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(setDir, "operational", rel)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", errors.New("operational path could not be resolved")
	}
	if !pathWithinRoot(filepath.Join(setDir, "operational"), resolved) {
		return "", errors.New("operational path escapes backup set")
	}
	return resolved, nil
}

func verifyOperationalFile(payload BackupOperationalFile, path string) error {
	if payload.SHA256 == "" {
		return errors.New("operational file missing checksum")
	}
	size, checksum, err := fileSizeAndSHA256(path)
	if err != nil {
		return err
	}
	if size != payload.SizeBytes || checksum != payload.SHA256 {
		return errors.New("operational file integrity mismatch")
	}
	return nil
}

func validateOperationalEntry(name string, payload BackupOperationalFile) error {
	rel, err := cleanOperationalRelativePath(payload.RelativePath)
	if err != nil {
		return err
	}
	if filepath.ToSlash(rel) != name {
		return errors.New("operational manifest key does not match relative path")
	}
	if payload.Kind != operationalFileKind(rel) {
		return errors.New("operational manifest kind does not match relative path")
	}
	return nil
}

func sanitizeOperationalName(name string) string {
	if _, err := cleanOperationalRelativePath(name); err == nil {
		return filepath.ToSlash(filepath.Clean(name))
	}
	return "operational-file"
}

func prepareRestoreTarget(dataDir string, manifest backupSetManifest, replaceIdentity bool) error {
	entries, err := os.ReadDir(dataDir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return err
		}
		entries = nil
	} else if err != nil {
		return err
	}
	if manifest.RestoreInstructionsVersion == 1 {
		if len(entries) != 0 {
			return errors.New("restore data-dir must be empty")
		}
		//nolint:gosec // dataDir is a private directory and requires owner execute permission.
		return os.Chmod(dataDir, 0o700)
	}
	if err := ensureNoIdentityConflict(dataDir, manifest, replaceIdentity); err != nil {
		return err
	}
	for _, payload := range manifest.OperationalFiles {
		rel, err := cleanOperationalRelativePath(payload.RelativePath)
		if err != nil {
			return err
		}
		if err := ensureOperationalDestinationSafe(dataDir, rel); err != nil {
			return err
		}
	}
	return os.Chmod(dataDir, 0o700)
}

func ensureNoIdentityConflict(dataDir string, manifest backupSetManifest, replaceIdentity bool) error {
	want := map[string]string{}
	for _, payload := range manifest.OperationalFiles {
		rel, err := cleanOperationalRelativePath(payload.RelativePath)
		if err != nil {
			return err
		}
		if isIdentityRelativePath(rel) {
			want[filepath.ToSlash(rel)] = payload.SHA256
		}
	}
	got, err := liveIdentityChecksums(dataDir)
	if err != nil {
		return err
	}
	if len(got) == 0 {
		return nil
	}
	if len(got) == len(want) {
		match := true
		for path, checksum := range want {
			if got[path] != checksum {
				match = false
				break
			}
		}
		if match {
			return nil
		}
	}
	if replaceIdentity {
		return nil
	}
	return fmt.Errorf("%w: live secrets/ or tls/ differs from the backup; set replace-identity explicitly to proceed", ErrRestoreIdentityConflict)
}

func isIdentityRelativePath(rel string) bool {
	top := strings.Split(filepath.ToSlash(rel), "/")[0]
	return top == "secrets" || top == "tls"
}

func liveIdentityChecksums(dataDir string) (map[string]string, error) {
	checksums := map[string]string{}
	for _, root := range []string{"secrets", "tls"} {
		base := filepath.Join(dataDir, root)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(dataDir, path)
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("restore identity path is a symlink: %s", rel)
			}
			if entry.IsDir() {
				if entry.Name() == "acme" {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("restore identity path is not a regular file: %s", rel)
			}
			_, checksum, err := fileSizeAndSHA256(path)
			if err != nil {
				return err
			}
			checksums[filepath.ToSlash(rel)] = checksum
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return checksums, nil
}

func restoreOperationalFiles(manifest backupSetManifest, dataDir string) error {
	if manifest.RestoreInstructionsVersion == 1 {
		return nil
	}
	keys := make([]string, 0, len(manifest.OperationalFiles))
	for key := range manifest.OperationalFiles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		payload := manifest.OperationalFiles[key]
		rel, err := cleanOperationalRelativePath(payload.RelativePath)
		if err != nil {
			return err
		}
		if err := ensureOperationalDestinationSafe(dataDir, rel); err != nil {
			return err
		}
		dest := filepath.Join(dataDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := copyFile0600(payload.BackupPath, dest); err != nil {
			return err
		}
		if err := verifyOperationalFile(payload, dest); err != nil {
			return err
		}
	}
	return nil
}

func missingRestoreParentDirs(root, parent string) ([]string, error) {
	missing := []string{}
	for current := parent; current != root; current = filepath.Dir(current) {
		if !pathWithinRoot(root, current) {
			return nil, errors.New("restore destination parent escapes data directory")
		}
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("restore destination parent is not a directory: %s", current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		missing = append(missing, current)
	}
	return missing, nil
}

func ensureOperationalDestinationSafe(dataDir, rel string) error {
	current := dataDir
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore operational destination is a symlink: %s", filepath.ToSlash(rel))
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("restore operational destination parent is not a directory: %s", filepath.ToSlash(rel))
		}
		if i == len(parts)-1 && info.IsDir() {
			return fmt.Errorf("restore operational destination file is a directory: %s", filepath.ToSlash(rel))
		}
	}
	return nil
}

func (s *Store) snapshotEncryptedLibraryDBsLocked(ctx context.Context, partialDir, finalDir string, cipher EncryptedLibraryDBCipher) ([]BackupDatabaseSnapshot, error) {
	libraryIDs := make([]string, 0, len(s.encryptedDBs))
	for libraryID, db := range s.encryptedDBs {
		if db.state == "failed-quarantined" {
			return nil, fmt.Errorf("%w: %s", ErrEncryptedLibraryDBQuarantined, db.quarantine)
		}
		if db.state == "unsealed-registered" && db.writer != nil {
			libraryIDs = append(libraryIDs, string(libraryID))
		}
	}
	sealed, err := sealedEncryptedLibraryCiphertexts(s.encryptedDBRoot, s.encryptedDBs)
	if err != nil {
		return nil, err
	}
	for libraryID := range sealed {
		libraryIDs = append(libraryIDs, string(libraryID))
	}
	if len(libraryIDs) == 0 {
		return nil, nil
	}
	if len(s.encryptedDBs) > 0 && cipher == nil {
		return nil, errors.New("encrypted-library DB backup cipher is required")
	}
	sort.Strings(libraryIDs)
	snapshots := make([]BackupDatabaseSnapshot, 0, len(libraryIDs))
	for _, rawID := range libraryIDs {
		libraryID := LibraryID(rawID)
		db := s.encryptedDBs[libraryID]
		component := safeLibraryPathComponent(libraryID)
		partialLibraryDir := filepath.Join(partialDir, "encrypted-library", component)
		if err := os.MkdirAll(partialLibraryDir, 0o700); err != nil {
			return nil, err
		}
		ciphertextSnapshot := filepath.Join(partialLibraryDir, "encrypted_library"+encryptedLibraryDBCipherExt)
		var snapshot BackupDatabaseSnapshot
		if db != nil && db.state == "unsealed-registered" && db.writer != nil {
			plaintextSnapshot := filepath.Join(partialLibraryDir, "encrypted_library.snapshot.db")
			snapshot, err = snapshotDatabase(ctx, db.writer, db.workingPath, encryptedLibraryDBLabel, plaintextSnapshot)
			if err != nil {
				return nil, err
			}
			if err := cipher.SealDBFile(ctx, libraryID, plaintextSnapshot, ciphertextSnapshot); err != nil {
				if wipeErr := wipeOrRemoveFile(plaintextSnapshot); wipeErr != nil {
					slog.Warn("plaintext snapshot cleanup failed", "path", plaintextSnapshot, "error", wipeErr)
				}
				return nil, err
			}
			if err := wipeOrRemoveFile(plaintextSnapshot); err != nil {
				return nil, fmt.Errorf("clean plaintext encrypted-library backup snapshot: %w", err)
			}
			if err := os.Chmod(ciphertextSnapshot, 0o600); err != nil {
				return nil, err
			}
			snapshot.State = "unsealed-snapshot-encrypted"
			snapshot.SourcePath = db.ciphertextPath
		} else {
			source := sealed[libraryID]
			if source == "" {
				continue
			}
			if err := copyFile0600(source, ciphertextSnapshot); err != nil {
				return nil, err
			}
			snapshot = BackupDatabaseSnapshot{
				Database:      encryptedLibraryDBLabel,
				LibraryID:     rawID,
				State:         "sealed-ciphertext",
				MigrationDir:  "migrate/encrypted_library",
				CipherFormat:  encryptedLibraryDBCipherExt,
				SourcePath:    source,
				BackupPath:    filepath.Join(finalDir, "encrypted-library", component, "encrypted_library"+encryptedLibraryDBCipherExt),
				SchemaVersion: schemaFreezeEncryptedLibraryMigrations,
			}
		}
		size, checksum, err := fileSizeAndSHA256(ciphertextSnapshot)
		if err != nil {
			return nil, err
		}
		snapshot.Database = encryptedLibraryDBLabel
		snapshot.LibraryID = rawID
		snapshot.MigrationDir = "migrate/encrypted_library"
		snapshot.CipherFormat = encryptedLibraryDBCipherExt
		snapshot.BackupPath = filepath.Join(finalDir, "encrypted-library", component, "encrypted_library"+encryptedLibraryDBCipherExt)
		snapshot.SizeBytes = size
		snapshot.SHA256 = checksum
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func sealedEncryptedLibraryCiphertexts(root string, registered map[LibraryID]*encryptedLibraryDB) (map[LibraryID]string, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	paths := map[LibraryID]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		libraryID := LibraryID(entry.Name())
		if db := registered[libraryID]; db != nil && db.state == "unsealed-registered" {
			continue
		}
		path := filepath.Join(root, entry.Name(), "encrypted_library"+encryptedLibraryDBCipherExt)
		if _, err := os.Stat(path); err == nil {
			paths[libraryID] = path
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return paths, nil
}

func snapshotDatabase(ctx context.Context, db *sql.DB, sourcePath string, name string, destPath string) (BackupDatabaseSnapshot, error) {
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+quoteSQLiteString(destPath)); err != nil {
		return BackupDatabaseSnapshot{}, err
	}
	if err := os.Chmod(destPath, 0o600); err != nil {
		return BackupDatabaseSnapshot{}, err
	}
	version, err := userVersion(ctx, db)
	if err != nil {
		return BackupDatabaseSnapshot{}, err
	}
	size, checksum, err := fileSizeAndSHA256(destPath)
	if err != nil {
		return BackupDatabaseSnapshot{}, err
	}
	return BackupDatabaseSnapshot{
		Database:      name,
		SourcePath:    sourcePath,
		BackupPath:    destPath,
		SchemaVersion: version,
		SizeBytes:     size,
		SHA256:        checksum,
	}, nil
}

// writeFileSync0600 writes data to path with 0600 perms and fsyncs the file
// before returning, so the bytes are durable on disk (plain os.WriteFile leaves
// them in the page cache). Used for the backup manifest.
func writeFileSync0600(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// syncDir fsyncs a directory so its name-to-inode entries (creations, renames)
// are durable. A directory fsync is the POSIX barrier that makes a rename
// crash-safe.
func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}

func fileSizeAndSHA256(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

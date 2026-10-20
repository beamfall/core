package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func migrate(ctx context.Context, logger *slog.Logger, db *sql.DB, dbPath string, name string, migrations fs.FS, dir string, backupDir string) error {
	return migrateWithBackup(ctx, logger, db, dbPath, name, migrations, dir, backupDir, backupDatabase)
}

func migrateWithBackup(ctx context.Context, logger *slog.Logger, db *sql.DB, dbPath string, name string, migrations fs.FS, dir string, backupDir string, backup func(context.Context, *slog.Logger, *sql.DB, string, string, string) error) error {
	entries, err := fs.ReadDir(migrations, dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	files := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry)
		}
	}
	missing, err := missingRecordedMigration(ctx, db, files)
	if err != nil {
		return err
	}
	if missing != "" {
		return fmt.Errorf("%s recorded migration %s is missing from the migration filesystem; migration files are append-only", name, missing)
	}

	version, err := userVersion(ctx, db)
	if err != nil {
		return err
	}
	if version > len(files) {
		return fmt.Errorf("%s schema version %d is newer than this binary supports (%d)", name, version, len(files))
	}
	if version < len(files) {
		logger.InfoContext(ctx, "applying migrations", "database", name, "from", version, "to", len(files))
		sessionSnapshot, err := ensureMigrationSessionSnapshot(ctx, logger, db, dbPath, name, len(files), backupDir)
		if err != nil {
			return err
		}
		if !sessionSnapshot {
			err = backup(ctx, logger, db, dbPath, name, backupDir)
		}
		if err != nil {
			return err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		  name TEXT PRIMARY KEY,
		  checksum TEXT NOT NULL,
		  applied_at INTEGER NOT NULL
		);
	`); err != nil {
		return err
	}
	for _, entry := range files {
		path := filepath.Join(dir, entry.Name())
		sqlText, err := fs.ReadFile(migrations, path)
		if err != nil {
			return err
		}
		checksum := migrationChecksum(sqlText)
		applied, err := migrationChecksumInDB(ctx, tx, entry.Name())
		if err != nil {
			return err
		}
		if applied != "" {
			if applied != checksum {
				if !acceptLegacyMigrationChecksum(name, entry.Name(), applied) {
					return fmt.Errorf("%s migration %s checksum drift", name, entry.Name())
				}
			}
			continue
		}
		if err := prepareMigrationCompatibility(ctx, tx, name, entry.Name()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(sqlText)); err != nil {
			return fmt.Errorf("apply %s migration %s: %w", name, entry.Name(), err)
		}
		if err := checkForeignKeys(ctx, tx, name, entry.Name()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(name, checksum, applied_at) VALUES (?, ?, unixepoch())",
			entry.Name(), checksum,
		); err != nil {
			return err
		}
		logger.InfoContext(ctx, "applied migration", "database", name, "migration", entry.Name())
	}
	var appliedCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&appliedCount); err != nil {
		return err
	}
	// The row-count stamp is monotonic only while migration files remain
	// append-only; missing recorded files are rejected before version checks.
	nextVersion := max(version, appliedCount)
	if nextVersion != version {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", nextVersion)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// checkForeignKeys asserts the database has no foreign-key violations after a
// migration was applied. SQLite's safe table-rebuild procedure (CREATE _new →
// INSERT…SELECT → DROP → RENAME, as in catalog 0004/0005) does not rewrite the
// FK clauses recorded in *other* tables when a referenced table is renamed, so a
// rebuild of a referenced table can leave dependent rows pointing at nothing
// while the schema text still looks correct. Because the whole migration set
// runs inside one transaction, the writer's PRAGMA foreign_keys is silently a
// no-op there, so deferred enforcement never fires on its own. PRAGMA
// foreign_key_check is the explicit detector: it returns one row per violation
// (child table, violating rowid, referenced parent table) and zero rows when the
// graph is intact. Any row is a hard failure naming the migration that
// introduced it — a loud stop is the whole point, never weaken it to pass.
func checkForeignKeys(ctx context.Context, tx *sql.Tx, name string, migration string) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return rows.Err()
	}
	// table = child table holding the bad reference; rowid may be NULL for
	// rowid-less (WITHOUT ROWID / view) tables; parent = referenced table.
	var table, parent string
	var rowid sql.NullInt64
	var fkid int
	if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
		return err
	}
	rowDesc := "rowid <none>"
	if rowid.Valid {
		rowDesc = fmt.Sprintf("rowid %d", rowid.Int64)
	}
	return fmt.Errorf("%s migration %s left a foreign-key violation: table %q (%s) references missing parent %q", name, migration, table, rowDesc, parent)
}

type backupManifest struct {
	Database      string `json:"database"`
	SourcePath    string `json:"sourcePath"`
	BackupPath    string `json:"backupPath"`
	SchemaVersion int    `json:"schemaVersion"`
	CreatedAt     string `json:"createdAt"`
}

const (
	migrationSnapshotType    = "pre-migration"
	migrationSnapshotVersion = 2
)

type migrationSessionManifest struct {
	Type                       string                            `json:"type"`
	RestoreInstructionsVersion int                               `json:"restoreInstructionsVersion"`
	SessionID                  string                            `json:"sessionId"`
	CreatedAt                  string                            `json:"createdAt"`
	CreatedBy                  string                            `json:"createdBy"`
	Reason                     string                            `json:"reason"`
	ConsistencyProtocol        string                            `json:"consistencyProtocol"`
	FromVersions               map[string]int                    `json:"fromVersions"`
	ToVersions                 map[string]int                    `json:"toVersions"`
	Databases                  map[string]BackupDatabaseSnapshot `json:"databases"`
}

func ensureMigrationSessionSnapshot(ctx context.Context, logger *slog.Logger, db *sql.DB, dbPath, name string, targetVersion int, backupDir string) (bool, error) {
	dataDir, ok := canonicalMigrationLayout(dbPath, name, backupDir)
	if !ok {
		return false, nil
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return true, err
	}
	backupRoot, err := os.OpenRoot(backupDir)
	if err != nil {
		return true, err
	}
	chmodErr := backupRoot.Chmod(".", 0o700)
	closeErr := backupRoot.Close()
	if chmodErr != nil {
		return true, chmodErr
	}
	if closeErr != nil {
		return true, closeErr
	}
	fromVersions, err := migrationCoreVersions(ctx, db, dbPath, dataDir)
	if err != nil {
		return true, err
	}
	toVersions := migrationTargetVersions()
	toVersions[name] = targetVersion
	_, found, err := findMigrationSessionSnapshot(backupDir, fromVersions, toVersions)
	if err != nil || found {
		return true, err
	}
	finalDir, err := publishMigrationSessionSnapshot(ctx, db, dbPath, dataDir, backupDir, fromVersions, toVersions)
	if err != nil {
		return true, err
	}
	logger.InfoContext(ctx, "pre-migration session snapshot written", "session", filepath.Base(finalDir), "path", finalDir)
	return true, nil
}

func canonicalMigrationLayout(dbPath, name, backupDir string) (string, bool) {
	if name != "catalog" && name != "events" {
		return "", false
	}
	dataDir := filepath.Dir(filepath.Dir(filepath.Clean(dbPath)))
	canonicalDB := filepath.Join(dataDir, "db", name+".db")
	canonicalBackups := filepath.Join(dataDir, "backups", "pre-migration")
	return dataDir, filepath.Clean(dbPath) == canonicalDB && filepath.Clean(backupDir) == canonicalBackups
}

func migrationCoreVersions(ctx context.Context, current *sql.DB, currentPath, dataDir string) (map[string]int, error) {
	versions := make(map[string]int, 2)
	for _, name := range []string{"catalog", "events"} {
		path := filepath.Join(dataDir, "db", name+".db")
		if filepath.Clean(path) == filepath.Clean(currentPath) {
			version, err := userVersion(ctx, current)
			if err != nil {
				return nil, err
			}
			versions[name] = version
			continue
		}
		version, err := userVersionAtPath(ctx, path)
		if err != nil {
			return nil, err
		}
		versions[name] = version
	}
	return versions, nil
}

func userVersionAtPath(ctx context.Context, path string) (int, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, err
	}
	db, err := openSQLiteReader(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return userVersion(ctx, db)
}

func migrationTargetVersions() map[string]int {
	return map[string]int{"catalog": schemaFreezeCatalogMigrations, "events": schemaFreezeEventsMigrations}
}

func migrationSessionID(fromVersions, toVersions map[string]int) (string, error) {
	tuple := struct {
		From map[string]int `json:"from"`
		To   map[string]int `json:"to"`
	}{From: fromVersions, To: toVersions}
	data, err := json.Marshal(tuple)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16]), nil
}

func findMigrationSessionSnapshot(backupDir string, currentVersions, targetVersions map[string]int) (string, bool, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return "", false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(backupDir, entry.Name(), backupManifestBasename)
		data, err := os.ReadFile(manifestPath) //nolint:gosec // fixed filename under the data directory
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		var manifest migrationSessionManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		if migrationManifestMatches(manifest, entry.Name(), currentVersions, targetVersions) {
			return filepath.Join(backupDir, entry.Name()), true, nil
		}
	}
	return "", false, nil
}

func migrationManifestMatches(manifest migrationSessionManifest, dirName string, currentVersions, targetVersions map[string]int) bool {
	if manifest.Type != migrationSnapshotType ||
		manifest.RestoreInstructionsVersion != migrationSnapshotVersion ||
		dirName != "session-"+manifest.SessionID {
		return false
	}
	expectedID, err := migrationSessionID(manifest.FromVersions, manifest.ToVersions)
	if err != nil || expectedID != manifest.SessionID {
		return false
	}
	for name, target := range targetVersions {
		if manifest.ToVersions[name] != target {
			return false
		}
		current, ok := currentVersions[name]
		if !ok || (current != manifest.FromVersions[name] && current != target) {
			return false
		}
	}
	return true
}

func publishMigrationSessionSnapshot(ctx context.Context, current *sql.DB, currentPath, dataDir, backupDir string, fromVersions, toVersions map[string]int) (string, error) {
	sessionID, err := migrationSessionID(fromVersions, toVersions)
	if err != nil {
		return "", err
	}
	finalDir := filepath.Join(backupDir, "session-"+sessionID)
	if _, err := os.Stat(finalDir); err == nil {
		return "", fmt.Errorf("pre-migration session %s exists but is not reusable", sessionID)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	partialDir, err := os.MkdirTemp(backupDir, ".session-"+sessionID+".partial-")
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(partialDir)
		}
	}()
	databases := make(map[string]BackupDatabaseSnapshot)
	for _, name := range []string{"catalog", "events"} {
		source := filepath.Join(dataDir, "db", name+".db")
		dest := filepath.Join(partialDir, name+".db")
		snapshot, err := snapshotMigrationDatabase(ctx, current, currentPath, source, dest, filepath.Join(finalDir, name+".db"), name)
		if err != nil {
			return "", err
		}
		databases[name] = snapshot
	}
	encrypted, err := snapshotMigrationEncryptedLibraries(dataDir, partialDir, finalDir)
	if err != nil {
		return "", err
	}
	for name, snapshot := range encrypted {
		databases[name] = snapshot
	}
	manifest := migrationSessionManifest{
		Type:                       migrationSnapshotType,
		RestoreInstructionsVersion: migrationSnapshotVersion,
		SessionID:                  sessionID,
		CreatedAt:                  time.Now().UTC().Format(time.RFC3339Nano),
		CreatedBy:                  "beamfall-store",
		Reason:                     "pre-migration",
		ConsistencyProtocol:        "all database snapshots published before any migration transaction begins",
		FromVersions:               fromVersions,
		ToVersions:                 toVersions,
		Databases:                  databases,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeFileSync0600(filepath.Join(partialDir, backupManifestBasename), append(data, '\n')); err != nil {
		return "", err
	}
	if err := syncDir(partialDir); err != nil {
		return "", err
	}
	if err := os.Rename(partialDir, finalDir); err != nil {
		_, found, findErr := findMigrationSessionSnapshot(backupDir, fromVersions, toVersions)
		if findErr != nil {
			return "", findErr
		}
		if !found {
			return "", err
		}
		return finalDir, nil
	}
	cleanup = false
	if err := syncDir(backupDir); err != nil {
		return "", err
	}
	return finalDir, nil
}

func snapshotMigrationDatabase(ctx context.Context, current *sql.DB, currentPath, sourcePath, partialPath, finalPath, name string) (BackupDatabaseSnapshot, error) {
	db := current
	if filepath.Clean(sourcePath) != filepath.Clean(currentPath) {
		var err error
		db, err = openSQLiteWriter(sourcePath)
		if err != nil {
			return BackupDatabaseSnapshot{}, err
		}
		defer db.Close()
	}
	snapshot, err := snapshotDatabase(ctx, db, sourcePath, name, partialPath)
	if err != nil {
		return BackupDatabaseSnapshot{}, err
	}
	snapshot.BackupPath = finalPath
	return snapshot, nil
}

func snapshotMigrationEncryptedLibraries(dataDir, partialDir, finalDir string) (map[string]BackupDatabaseSnapshot, error) {
	root := filepath.Join(dataDir, "db", "encrypted-library")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return map[string]BackupDatabaseSnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	snapshots := make(map[string]BackupDatabaseSnapshot)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		component := entry.Name()
		source := filepath.Join(root, component, "encrypted_library"+encryptedLibraryDBCipherExt)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("encrypted-library migration snapshot source is not a regular file: %s", source)
		}
		partialLibraryDir := filepath.Join(partialDir, "encrypted-library", component)
		if err := os.MkdirAll(partialLibraryDir, 0o700); err != nil {
			return nil, err
		}
		partialPath := filepath.Join(partialLibraryDir, "encrypted_library"+encryptedLibraryDBCipherExt)
		if err := copyFile0600(source, partialPath); err != nil {
			return nil, err
		}
		if err := syncDir(partialLibraryDir); err != nil {
			return nil, err
		}
		size, checksum, err := fileSizeAndSHA256(partialPath)
		if err != nil {
			return nil, err
		}
		name := "encrypted_library/" + component
		snapshots[name] = BackupDatabaseSnapshot{
			Database:      encryptedLibraryDBLabel,
			LibraryID:     component,
			State:         "sealed-ciphertext",
			MigrationDir:  "migrate/encrypted_library",
			CipherFormat:  encryptedLibraryDBCipherExt,
			SourcePath:    source,
			BackupPath:    filepath.Join(finalDir, "encrypted-library", component, "encrypted_library"+encryptedLibraryDBCipherExt),
			SchemaVersion: schemaFreezeEncryptedLibraryMigrations,
			SizeBytes:     size,
			SHA256:        checksum,
		}
	}
	if len(snapshots) > 0 {
		if err := syncDir(filepath.Join(partialDir, "encrypted-library")); err != nil {
			return nil, err
		}
	}
	return snapshots, nil
}

func backupDatabase(ctx context.Context, logger *slog.Logger, db *sql.DB, dbPath string, name string, backupDir string) error {
	if backupDir == "" {
		return errors.New("backup dir is required")
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	createdAt := time.Now().UTC()
	stem := createdAt.Format("20060102T150405.000000000Z") + "-" + name
	backupPath := filepath.Join(backupDir, stem+".db")
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+quoteSQLiteString(backupPath)); err != nil {
		return err
	}
	if err := os.Chmod(backupPath, 0o600); err != nil {
		return err
	}
	version, err := userVersion(ctx, db)
	if err != nil {
		return err
	}
	manifest := backupManifest{
		Database:      name,
		SourcePath:    dbPath,
		BackupPath:    backupPath,
		SchemaVersion: version,
		CreatedAt:     createdAt.Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "pre-migration backup written", "database", name, "path", backupPath)
	return os.WriteFile(filepath.Join(backupDir, stem+".json"), append(data, '\n'), 0o600)
}

func quoteSQLiteString(value string) string {
	quoted := "'"
	for _, r := range value {
		if r == '\'' {
			quoted += "''"
			continue
		}
		quoted += string(r)
	}
	return quoted + "'"
}

func userVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func missingRecordedMigration(ctx context.Context, db *sql.DB, files []fs.DirEntry) (string, error) {
	var tableExists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'
	)`).Scan(&tableExists); err != nil {
		return "", err
	}
	if tableExists == 0 {
		return "", nil
	}

	available := make(map[string]bool, len(files))
	for _, file := range files {
		available[file.Name()] = true
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM schema_migrations ORDER BY name")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", err
		}
		if !available[name] {
			return name, nil
		}
	}
	return "", rows.Err()
}

func migrationChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func acceptLegacyMigrationChecksum(dbName, migrationName, checksum string) bool {
	if dbName == "catalog" && migrationName == "0050_media_file_streams.sql" {
		// PLAY-1 tightened the first stream-inventory migration to include
		// hdr10_plus. PLAY-2 tightened it again to include codec level.
		// NGAUDIT-C5-C1-PROBE tightened it again with disposition flags.
		// Databases that already applied either earlier 0050 are repaired by
		// later compatibility migrations instead of failing checksum drift.
		return checksum == "27ad18c20eea2a55087f59ff3e5c37bf82d4785b4eb7247797b344f67c3f36e6" ||
			checksum == "bec2759c8867219760cb9e3c875e9f4d10621613fd153f105e8ff0d5ebc32412" ||
			checksum == "a9103b930f052151acfb285f6694d81e4075b4a0621521fb7f2029294534dd78"
	}
	return false
}

func prepareMigrationCompatibility(ctx context.Context, tx *sql.Tx, dbName, migrationName string) error {
	if dbName == "catalog" && migrationName == "0073_media_file_stream_hdr10_plus.sql" {
		ok, err := tableColumnExists(ctx, tx, "media_file_stream", "hdr10_plus")
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "ALTER TABLE media_file_stream ADD COLUMN hdr10_plus INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("repair media_file_stream.hdr10_plus: %w", err)
		}
	}
	if dbName == "catalog" && migrationName == "0074_media_file_stream_level.sql" {
		ok, err := tableColumnExists(ctx, tx, "media_file_stream", "level")
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "ALTER TABLE media_file_stream ADD COLUMN level INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("repair media_file_stream.level: %w", err)
		}
	}
	if dbName == "catalog" && migrationName == "0096_media_file_stream_dispositions.sql" {
		for _, column := range []string{"is_hearing_impaired", "is_visual_impaired", "is_commentary"} {
			ok, err := tableColumnExists(ctx, tx, "media_file_stream", column)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
			if _, err := tx.ExecContext(ctx, "ALTER TABLE media_file_stream ADD COLUMN "+column+" INTEGER NOT NULL DEFAULT 0"); err != nil {
				return fmt.Errorf("repair media_file_stream.%s: %w", column, err)
			}
		}
	}
	return nil
}

func tableColumnExists(ctx context.Context, tx *sql.Tx, tableName, columnName string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteString(tableName)+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func migrationChecksumInDB(ctx context.Context, db queryRower, name string) (string, error) {
	var checksum string
	err := db.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE name = ?", name).Scan(&checksum)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return checksum, err
}

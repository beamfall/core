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
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type fixtureColumn struct {
	name       string
	typ        string
	defaultSQL sql.NullString
	primaryKey bool
	notNull    bool
}

type fixtureForeignKey struct {
	childColumn  string
	parentTable  string
	parentColumn string
}

type fixtureTableExpectation struct {
	Rows     int    `json:"rows"`
	Checksum string `json:"checksum"`
}

type fixtureExpectations struct {
	SchemaFingerprint string                             `json:"schemaFingerprint"`
	Tables            map[string]fixtureTableExpectation `json:"tables"`
	SchemaVersions    map[string]int                     `json:"schemaVersions"`
	FixturePolicy     map[string]int                     `json:"fixturePolicy"`
}

type fixtureDescriptor struct {
	Format              int      `json:"format"`
	Envelope            string   `json:"envelope"`
	Expectations        string   `json:"expectations"`
	EncryptedLibraryIDs []string `json:"encryptedLibraryIDs"`
	SyntheticKey        string   `json:"syntheticTestBackupKey"`
	CredentialScope     string   `json:"credentialScope"`
}

const fixtureMediaTable = "media"

func TestBackupFixtureSeedCoversEveryDurableTable(t *testing.T) {
	ctx := context.Background()
	output := os.Getenv("BEAMFALL_BACKUP_FIXTURE_DIR")
	dataDir := t.TempDir()
	opts := Options{SeedProfile: SeedProfileAcceptance, EnableEncryptedLibraryDBLifecycle: true}
	if output != "" {
		opts.AllowedRoots = []string{output}
	}
	st, err := Open(ctx, dataDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	installBackupTestSealer(st, dataDir)
	if err := prepareFixtureMediaReferences(ctx, st.catalogW); err != nil {
		t.Fatal(err)
	}
	encryptedLibraryID, err := prepareFixtureEncryptedLibrary(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	for label, db := range fixtureDatabases(st) {
		if err := seedEveryDurableFixtureTable(ctx, db); err != nil {
			t.Fatalf("%s fixture seed: %v", label, err)
		}
		if err := assertFixtureDatabase(ctx, db); err != nil {
			t.Fatalf("%s fixture coverage: %v", label, err)
		}
	}
	if output == "" {
		return
	}
	if err := os.MkdirAll(output, 0o700); err != nil { //nolint:gosec // G703: output is an explicit test-only fixture generation destination.
		t.Fatal(err)
	}
	outputRoot, err := os.OpenRoot(output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := outputRoot.Close(); err != nil {
			t.Errorf("close fixture output root: %v", err)
		}
	})
	result, err := st.BackupWithOptions(ctx, output, BackupOptions{EncryptedLibraryDBCipher: copyEncryptedLibraryDBCipher{}})
	if err != nil {
		t.Fatal(err)
	}
	opener, keyDisplay := bootstrapBackupTestCredentials(t, dataDir, time.Now())
	expectations, err := collectFixtureExpectations(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(expectations, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := outputRoot.WriteFile("expectations.json", append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalOutput, err := filepath.EvalSymlinks(output)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := filepath.Rel(canonicalOutput, result.Envelope)
	if err != nil || strings.HasPrefix(envelope, "..") {
		t.Fatalf("fixture envelope path escapes output: %q: %v", envelope, err)
	}
	descriptor := fixtureDescriptor{
		Format:       1,
		Envelope:     filepath.ToSlash(envelope),
		Expectations: "expectations.json",
		EncryptedLibraryIDs: []string{
			string(encryptedLibraryID),
		},
		SyntheticKey:    keyDisplay,
		CredentialScope: "public synthetic fixture data only; never accepted for production",
	}
	raw, err = json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := outputRoot.WriteFile("fixture.json", append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	restoreDir := filepath.Join(t.TempDir(), "restored")
	report, err := RestoreBackupWithOptions(ctx, result.Envelope, restoreDir, RestoreOptions{SnapshotOpener: opener})
	if err != nil {
		t.Fatalf("restore generated fixture: %v", err)
	}
	if report.MediaReconciliation.Severity != "clean" {
		t.Fatalf("generated fixture media reconciliation = %#v", report.MediaReconciliation)
	}
	restored, err := Open(ctx, restoreDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("boot restored fixture: %v", err)
	}
	defer restored.Close()
	if err := verifyFixtureExpectations(ctx, restored, expectations); err != nil {
		t.Fatalf("generated fixture drill: %v", err)
	}
}

// TestBackupRestoreDrillDetectsRowChecksumCorruption pins the drill to the
// path a real regression takes: a future migration corrupts a row, not a raw
// UPDATE against an already-open store. It seeds a store, backs it up,
// restores into a fresh data directory, boots the restored store, applies a
// deliberately-corrupting migration through the SAME append-only migration
// path a production restart uses (migrate(), fed a merged fs.FS — see
// corruptingCatalogMigrationFS — rather than a change to the tracked
// internal/store/migrate/catalog corpus), and asserts the drill's row
// checksum diff catches it.
func TestBackupRestoreDrillDetectsRowChecksumCorruption(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	backupOut := t.TempDir()
	st, err := Open(ctx, dataDir, Options{SeedProfile: SeedProfileAcceptance, EnableEncryptedLibraryDBLifecycle: true, AllowedRoots: []string{backupOut}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	installBackupTestSealer(st, dataDir)
	if err := prepareFixtureMediaReferences(ctx, st.catalogW); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareFixtureEncryptedLibrary(ctx, st); err != nil {
		t.Fatal(err)
	}
	for _, db := range fixtureDatabases(st) {
		if err := seedEveryDurableFixtureTable(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := collectFixtureExpectations(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	// The test-only migration below adds one row to the append-only
	// schema_migrations ledger (applied_at is wall-clock, so it can never
	// reproduce a precomputed checksum). That is expected migration
	// bookkeeping, not the row-content corruption under test — exclude it so
	// the drill's verdict reflects the corrupted "library" row, not a ledger
	// diff every migration would trigger.
	delete(expected.Tables, "catalog.schema_migrations")

	result, err := st.BackupWithOptions(ctx, backupOut, BackupOptions{EncryptedLibraryDBCipher: copyEncryptedLibraryDBCipher{}})
	if err != nil {
		t.Fatal(err)
	}
	opener, _ := bootstrapBackupTestCredentials(t, dataDir, time.Now())

	restoreDir := filepath.Join(t.TempDir(), "restored")
	report, err := RestoreBackupWithOptions(ctx, result.Envelope, restoreDir, RestoreOptions{SnapshotOpener: opener})
	if err != nil {
		t.Fatalf("restore fixture: %v", err)
	}
	if report.MediaReconciliation.Severity != "clean" {
		t.Fatalf("restored fixture media reconciliation = %#v", report.MediaReconciliation)
	}
	restored, err := Open(ctx, restoreDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("boot restored fixture: %v", err)
	}
	defer restored.Close()

	corrupting := corruptingCatalogMigrationFS(t,
		"0109_test_only_corrupting_row_update.sql",
		"UPDATE library SET title = title || '-corrupted' WHERE id = (SELECT id FROM library ORDER BY id LIMIT 1);\n",
	)
	backupDir := filepath.Join(restoreDir, "backups", "pre-migration")
	if err := migrate(ctx, restored.logger, restored.catalogW, restored.catalogPath, "catalog", corrupting, "migrate/catalog", backupDir); err != nil {
		t.Fatalf("apply test-only corrupting migration: %v", err)
	}

	if err := verifyFixtureExpectations(ctx, restored, expected); err == nil || !strings.Contains(err.Error(), "catalog.library checksum") {
		t.Fatalf("corrupting migration was not caught by row checksum diff: %v", err)
	}
}

// corruptingCatalogMigrationFS returns the real catalog migration set plus one
// synthetic migration appended after the highest real ordinal. migrate()
// accepts any fs.FS, so this is the smallest test-only injection point for "a
// future migration corrupts a row" — it never adds a file to the tracked
// internal/store/migrate/catalog corpus, which stays append-only production
// history.
func corruptingCatalogMigrationFS(t *testing.T, name, sqlText string) fs.FS {
	t.Helper()
	const dir = "migrate/catalog"
	entries, err := fs.ReadDir(catalogMigrations, dir)
	if err != nil {
		t.Fatal(err)
	}
	merged := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(catalogMigrations, path.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		merged[path.Join(dir, entry.Name())] = &fstest.MapFile{Data: data}
	}
	merged[path.Join(dir, name)] = &fstest.MapFile{Data: []byte(sqlText)}
	return merged
}

func TestBackupFixtureCorpusDrill(t *testing.T) {
	ctx := context.Background()
	fixtureDir := os.Getenv("BEAMFALL_BACKUP_FIXTURE_DRILL_DIR")
	if fixtureDir == "" {
		t.Skip("set BEAMFALL_BACKUP_FIXTURE_DRILL_DIR to a generated fixture directory")
	}
	raw, err := os.ReadFile(filepath.Join(fixtureDir, "fixture.json")) //nolint:gosec // G304: fixtureDir is an explicit test-only corpus input.
	if err != nil {
		t.Fatal(err)
	}
	var descriptor fixtureDescriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Format != 1 || descriptor.CredentialScope != "public synthetic fixture data only; never accepted for production" {
		t.Fatalf("invalid fixture descriptor: %+v", descriptor)
	}
	opener := backupTestOpenerFromDisplay(t, descriptor.SyntheticKey)
	raw, err = os.ReadFile(filepath.Join(fixtureDir, descriptor.Expectations)) //nolint:gosec // G304: Expectations comes from the test-only fixture descriptor.
	if err != nil {
		t.Fatal(err)
	}
	var expected fixtureExpectations
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	restoreDir := filepath.Join(t.TempDir(), "restored")
	envelope := filepath.Join(fixtureDir, filepath.FromSlash(descriptor.Envelope))
	report, err := RestoreBackupWithOptions(ctx, envelope, restoreDir, RestoreOptions{SnapshotOpener: opener})
	if err != nil {
		t.Fatalf("restore fixture: %v", err)
	}
	if report.MediaReconciliation.Severity != "clean" {
		t.Fatalf("fixture media reconciliation = %#v", report.MediaReconciliation)
	}
	restored, err := Open(ctx, restoreDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("boot restored fixture: %v", err)
	}
	defer restored.Close()
	if err := verifyFixtureExpectations(ctx, restored, expected); err != nil {
		t.Fatal(err)
	}
}

func prepareFixtureMediaReferences(ctx context.Context, db *sql.DB) error {
	paths := []string{"backup.go", "sqlite.go", "backup_fixture_test.go"}
	rows, err := db.QueryContext(ctx, "SELECT id FROM media_file WHERE missing_at_ms IS NULL ORDER BY id")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(ids) > len(paths) {
		return fmt.Errorf("fixture has %d live media references, only %d stable paths", len(ids), len(paths))
	}
	for index, id := range ids {
		info, err := os.Stat(paths[index])
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("stable fixture media path %s is unavailable", paths[index])
		}
		if _, err := db.ExecContext(ctx, "UPDATE media_file SET path = ? WHERE id = ?", paths[index], id); err != nil {
			return err
		}
	}
	return nil
}

func prepareFixtureEncryptedLibrary(ctx context.Context, st *Store) (LibraryID, error) {
	var rawID string
	if err := st.catalogW.QueryRowContext(ctx, "SELECT id FROM library ORDER BY id LIMIT 1").Scan(&rawID); err != nil {
		return "", err
	}
	libraryID := LibraryID(rawID)
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		return "", err
	}
	db := st.encryptedDBs[libraryID]
	if db == nil || db.writer == nil {
		return "", errors.New("encrypted fixture database was not registered")
	}
	if err := seedEveryDurableFixtureTable(ctx, db.writer); err != nil {
		return "", err
	}
	if err := assertFixtureDatabase(ctx, db.writer); err != nil {
		return "", err
	}
	return libraryID, nil
}

func fixtureDatabases(st *Store) map[string]*sql.DB {
	databases := map[string]*sql.DB{"catalog": st.catalogW, "events": st.eventsW}
	for libraryID, db := range st.encryptedDBs {
		if db != nil && db.state == "unsealed-registered" && db.writer != nil {
			databases["encrypted:"+string(libraryID)] = db.writer
		}
	}
	return databases
}

func seedEveryDurableFixtureTable(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer db.ExecContext(ctx, "PRAGMA foreign_keys = ON") //nolint:errcheck // assertion below verifies the final posture
	if _, err := db.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		return err
	}
	defer db.ExecContext(ctx, "PRAGMA ignore_check_constraints = OFF") //nolint:errcheck // test cleanup; subsequent assertion catches failure
	tables, err := durableFixtureTables(ctx, db)
	if err != nil {
		return err
	}
	for _, table := range tables {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteFixtureIdentifier(table)).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := insertFixtureRow(ctx, db, table); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		if table == "media" {
			if err := duplicateFixtureMediaRow(ctx, db); err != nil {
				return fmt.Errorf("media secondary row: %w", err)
			}
		}
	}
	return nil
}

func duplicateFixtureMediaRow(ctx context.Context, db *sql.DB) error {
	columns, err := fixtureColumns(ctx, db, fixtureMediaTable)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(columns))
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, quoteFixtureIdentifier(column.name))
		if column.name == "id" {
			values = append(values, "?")
		} else {
			values = append(values, quoteFixtureIdentifier(column.name))
		}
	}
	_, err = db.ExecContext(ctx,
		"INSERT INTO "+fixtureMediaTable+" ("+strings.Join(names, ",")+") SELECT "+strings.Join(values, ",")+" FROM "+fixtureMediaTable+" ORDER BY id LIMIT 1", //nolint:gosec // G202: fixtureMediaTable is a test-local constant; columns are quoted fixture-schema identifiers.
		"fixture-media-secondary",
	)
	return err
}

func durableFixtureTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name
		FROM pragma_table_list
		WHERE schema = 'main'
		  AND type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func insertFixtureRow(ctx context.Context, db *sql.DB, table string) error {
	columns, err := fixtureColumns(ctx, db, table)
	if err != nil {
		return err
	}
	foreignKeys, err := fixtureForeignKeys(ctx, db, table)
	if err != nil {
		return err
	}
	foreignValues := map[string]any{}
	for _, foreignKey := range foreignKeys {
		childRequired := false
		for _, column := range columns {
			if column.name == foreignKey.childColumn {
				childRequired = column.notNull
				break
			}
		}
		if !childRequired && (table != "media_relation" || foreignKey.childColumn != "to_media_id") {
			foreignValues[foreignKey.childColumn] = nil
			continue
		}
		if foreignKey.parentTable == table {
			foreignValues[foreignKey.childColumn] = nil
			continue
		}
		value, found, err := firstFixtureValue(ctx, db, foreignKey.parentTable, foreignKey.parentColumn)
		if err != nil {
			return err
		}
		if !found {
			parentColumns, err := fixtureColumns(ctx, db, foreignKey.parentTable)
			if err != nil {
				return err
			}
			parentType := ""
			for _, parentColumn := range parentColumns {
				if parentColumn.name == foreignKey.parentColumn {
					parentType = parentColumn.typ
					break
				}
			}
			value = syntheticFixtureValue(foreignKey.parentTable, foreignKey.parentColumn, parentType)
		}
		foreignValues[foreignKey.childColumn] = value
	}
	if table == "media_relation" {
		value, found, err := fixtureValueAtOffset(ctx, db, "media", "id", 1)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("media_relation requires two seeded media rows")
		}
		foreignValues["to_media_id"] = value
	}
	if table == "entity_relation" {
		fromEntity, found, err := firstFixtureValue(ctx, db, "entity", "id")
		if err != nil {
			return err
		}
		if !found {
			return errors.New("entity_relation source entity is missing")
		}
		foreignValues["from_entity_id"] = fromEntity
		foreignValues["to_entity_id"] = nil
		foreignValues["to_media_id"] = syntheticFixtureValue("media", "id", "TEXT")
	}
	names := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	for _, column := range columns {
		if value, ok := foreignValues[column.name]; ok {
			names = append(names, quoteFixtureIdentifier(column.name))
			args = append(args, value)
			continue
		}
		if value, ok, err := fixtureSemanticValue(ctx, db, table, column.name); err != nil {
			return err
		} else if ok {
			names = append(names, quoteFixtureIdentifier(column.name))
			args = append(args, value)
			continue
		}
		if !column.notNull && !column.primaryKey {
			continue
		}
		if column.defaultSQL.Valid && !column.primaryKey {
			continue
		}
		names = append(names, quoteFixtureIdentifier(column.name))
		args = append(args, syntheticFixtureValue(table, column.name, column.typ))
	}
	placeholders := make([]string, len(names))
	for index := range placeholders {
		placeholders[index] = "?"
	}
	_, err = db.ExecContext(ctx,
		"INSERT INTO "+quoteFixtureIdentifier(table)+" ("+strings.Join(names, ",")+") VALUES ("+strings.Join(placeholders, ",")+")", //nolint:gosec // G202: table and columns come only from the test fixture's local SQLite schema and are identifier-quoted.
		args...,
	)
	return err
}

func fixtureSemanticValue(ctx context.Context, db *sql.DB, table, column string) (any, bool, error) {
	if value, ok := map[string]any{
		"detection_run.completion_outcome":      "completed",
		"detection_run_lifecycle.event_type":    "revoke",
		"detection_run_lifecycle.model_id":      "fixture-model",
		"detection_run_lifecycle.model_version": "fixture-version",
		"detection_run_lifecycle.model_hash":    "fixture-hash",
	}[table+"."+column]; ok {
		return value, true, nil
	}
	source := map[string][2]string{
		"profile_id":          {"profile", "id"},
		"library_id":          {"library", "id"},
		"media_id":            {"media", "id"},
		"entity_id":           {"entity", "id"},
		"device_id":           {"device", "id"},
		"session_id":          {"session", "id"},
		"content_fingerprint": {"media_file", "content_fingerprint"},
	}[column]
	if source[0] == "" {
		return nil, false, nil
	}
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", source[0]).Scan(&exists); err != nil {
		return nil, false, err
	}
	if exists == 0 {
		return nil, false, nil
	}
	return firstFixtureValue(ctx, db, source[0], source[1])
}

func fixtureColumns(ctx context.Context, db *sql.DB, table string) ([]fixtureColumn, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_xinfo("+quoteFixtureIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []fixtureColumn
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, typ string
		var defaultSQL sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultSQL, &primaryKey, &hidden); err != nil {
			return nil, err
		}
		if hidden != 0 {
			continue
		}
		columns = append(columns, fixtureColumn{
			name: name, typ: typ, defaultSQL: defaultSQL,
			primaryKey: primaryKey != 0, notNull: notNull != 0,
		})
	}
	return columns, rows.Err()
}

func fixtureForeignKeys(ctx context.Context, db *sql.DB, table string) ([]fixtureForeignKey, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_list("+quoteFixtureIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []fixtureForeignKey
	for rows.Next() {
		var id, seq int
		var parentTable, childColumn, parentColumn, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &parentTable, &childColumn, &parentColumn, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		keys = append(keys, fixtureForeignKey{childColumn: childColumn, parentTable: parentTable, parentColumn: parentColumn})
	}
	return keys, rows.Err()
}

func firstFixtureValue(ctx context.Context, db *sql.DB, table, column string) (any, bool, error) {
	return fixtureValueAtOffset(ctx, db, table, column, 0)
}

func fixtureValueAtOffset(ctx context.Context, db *sql.DB, table, column string, offset int) (any, bool, error) {
	query := "SELECT " + quoteFixtureIdentifier(column) + " FROM " + quoteFixtureIdentifier(table) +
		" ORDER BY " + quoteFixtureIdentifier(column) + " LIMIT 1 OFFSET ?"
	var value any
	err := db.QueryRowContext(ctx, query, offset).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	return value, err == nil, err
}

func syntheticFixtureValue(table, column, typ string) any {
	if value, ok := map[string]any{
		"playback_rendition.kind":         "hls",
		"playback_rendition.status":       "ready",
		"schedules.trigger_kind":          "daily",
		"marker_suppression.segment_kind": "intro",
		"segment_detection.segment_kind":  "intro",
		"segment_detection.start_seconds": float64(0),
		"segment_detection.end_seconds":   float64(1),
		"segment_detection.confidence":    float64(0.5),
		"subscription.retention_kind":     "keep-all",
	}[table+"."+column]; ok {
		return value
	}
	upper := strings.ToUpper(typ)
	switch {
	case strings.Contains(upper, "INT"):
		return int64(1)
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOA"), strings.Contains(upper, "DOUB"):
		return float64(1)
	case strings.Contains(upper, "BLOB"):
		return []byte("fixture-" + table + "-" + column)
	default:
		return "fixture-" + table + "-" + column
	}
}

func quoteFixtureIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func assertFixtureDatabase(ctx context.Context, db *sql.DB) error {
	tables, err := durableFixtureTables(ctx, db)
	if err != nil {
		return err
	}
	for _, table := range tables {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteFixtureIdentifier(table)).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("durable table %s is empty", table)
		}
	}
	var violation string
	if err := db.QueryRowContext(ctx, "SELECT format('%s:%s:%s', `table`, rowid, parent) FROM pragma_foreign_key_check LIMIT 1").Scan(&violation); err == nil {
		return fmt.Errorf("foreign-key violation %s", violation)
	} else if err != sql.ErrNoRows {
		return err
	}
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("integrity_check = %s", integrity)
	}
	return nil
}

func collectFixtureExpectations(ctx context.Context, st *Store) (fixtureExpectations, error) {
	expectations := fixtureExpectations{
		SchemaFingerprint: fixtureSchemaFingerprint(),
		Tables:            map[string]fixtureTableExpectation{},
		SchemaVersions:    migrationTargetVersions(),
		FixturePolicy:     map[string]int{"months": 12, "minimumRecentReleases": 3},
	}
	for label, db := range fixtureDatabases(st) {
		tables, err := durableFixtureTables(ctx, db)
		if err != nil {
			return fixtureExpectations{}, err
		}
		for _, table := range tables {
			tableColumns, err := fixtureColumns(ctx, db, table)
			if err != nil {
				return fixtureExpectations{}, err
			}
			order := make([]string, 0, len(tableColumns))
			for _, column := range tableColumns {
				order = append(order, quoteFixtureIdentifier(column.name))
			}
			rows, err := db.QueryContext(ctx, "SELECT * FROM "+quoteFixtureIdentifier(table)+" ORDER BY "+strings.Join(order, ",")) //nolint:gosec // G202: table and order columns are identifier-quoted names from the test fixture's local SQLite schema.
			if err != nil {
				return fixtureExpectations{}, err
			}
			columns, err := rows.Columns()
			if err != nil {
				return fixtureExpectations{}, errors.Join(err, rows.Close())
			}
			hash := sha256.New()
			count := 0
			for rows.Next() {
				values := make([]any, len(columns))
				dest := make([]any, len(columns))
				for index := range values {
					dest[index] = &values[index]
				}
				if err := rows.Scan(dest...); err != nil {
					return fixtureExpectations{}, errors.Join(err, rows.Close())
				}
				encoded, _ := json.Marshal(values)
				hash.Write(encoded)
				count++
			}
			if err := rows.Close(); err != nil {
				return fixtureExpectations{}, err
			}
			expectations.Tables[label+"."+table] = fixtureTableExpectation{Rows: count, Checksum: hex.EncodeToString(hash.Sum(nil))}
		}
		if strings.HasPrefix(label, "encrypted:") {
			version, err := userVersion(ctx, db)
			if err != nil {
				return fixtureExpectations{}, err
			}
			expectations.SchemaVersions[label] = version
		}
	}
	return expectations, nil
}

func verifyFixtureExpectations(ctx context.Context, st *Store, expected fixtureExpectations) error {
	for database := range expected.SchemaVersions {
		if !strings.HasPrefix(database, "encrypted:") {
			continue
		}
		libraryID := LibraryID(strings.TrimPrefix(database, "encrypted:"))
		if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
			return fmt.Errorf("unseal fixture library %s: %w", libraryID, err)
		}
	}
	actual, err := collectFixtureExpectations(ctx, st)
	if err != nil {
		return err
	}
	if actual.SchemaFingerprint != expected.SchemaFingerprint {
		return fmt.Errorf("schema fingerprint got %s, want %s", actual.SchemaFingerprint, expected.SchemaFingerprint)
	}
	for database, want := range expected.SchemaVersions {
		if got := actual.SchemaVersions[database]; got != want {
			return fmt.Errorf("%s schema version got %d, want %d", database, got, want)
		}
	}
	for table, want := range expected.Tables {
		got, ok := actual.Tables[table]
		if !ok {
			return fmt.Errorf("%s missing after restore", table)
		}
		if got.Rows != want.Rows {
			return fmt.Errorf("%s row count got %d, want %d", table, got.Rows, want.Rows)
		}
		if got.Checksum != want.Checksum {
			return fmt.Errorf("%s checksum got %s, want %s", table, got.Checksum, want.Checksum)
		}
	}
	for table := range actual.Tables {
		if _, ok := expected.Tables[table]; !ok {
			return fmt.Errorf("%s unexpected after restore", table)
		}
	}
	for _, db := range fixtureDatabases(st) {
		if err := assertFixtureDatabase(ctx, db); err != nil {
			return err
		}
	}
	return nil
}

func fixtureSchemaFingerprint() string {
	raw, _ := json.Marshal(struct {
		Catalog          int `json:"catalog"`
		Events           int `json:"events"`
		EncryptedLibrary int `json:"encryptedLibrary"`
	}{
		Catalog:          schemaFreezeCatalogMigrations,
		Events:           schemaFreezeEventsMigrations,
		EncryptedLibrary: schemaFreezeEncryptedLibraryMigrations,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

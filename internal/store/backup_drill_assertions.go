package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DrillAssertionReport is the outcome of the Layer-1 suite a restore drill
// runs on the store it booted from a restored set (ADR-0192 §5 L1).
type DrillAssertionReport struct {
	// Databases is the number of databases that passed integrity_check and
	// foreign_key_check.
	Databases int
	// Tables is the number of tables whose row count and row checksum matched
	// the snapshot in the set.
	Tables int
	// Migrated names databases whose schema version moved while the restored
	// store booted. Their rows are not compared with the snapshot, because a
	// migration may rewrite them; they still pass the integrity checks.
	Migrated []string
}

// drillComparedDatabases are the plaintext databases the drill compares row by
// row. Encrypted Library databases stay sealed in a drill; restore already
// verified their ciphertext against the manifest's SHA-256.
var drillComparedDatabases = []string{"catalog", "events"}

// VerifyRestoredAgainstBackup runs the Layer-1 assertion suite on a store that
// was booted from the set at manifestPath: PRAGMA integrity_check and
// foreign_key_check on the catalog and events databases, then, for each one
// whose schema version did not move during boot, a per-table row count and
// row checksum compared with the snapshot recorded in the set. It reads the
// snapshot files read-only and writes nothing.
func (s *Store) VerifyRestoredAgainstBackup(ctx context.Context, manifestPath string) (DrillAssertionReport, error) {
	manifest, err := readVerifiedBackupManifest(ctx, manifestPath)
	if err != nil {
		return DrillAssertionReport{}, err
	}
	restored := map[string]*sql.DB{"catalog": s.catalogW, "events": s.eventsW}
	var report DrillAssertionReport
	for _, name := range drillComparedDatabases {
		db := restored[name]
		if db == nil {
			return report, fmt.Errorf("restored %s database is not open", name)
		}
		if err := checkDatabaseIntegrity(ctx, db); err != nil {
			return report, fmt.Errorf("%s: %w", name, err)
		}
		report.Databases++
		snapshot, ok := manifest.Databases[name]
		if !ok {
			return report, fmt.Errorf("backup manifest has no %s database", name)
		}
		version, err := userVersion(ctx, db)
		if err != nil {
			return report, err
		}
		if version != snapshot.SchemaVersion {
			report.Migrated = append(report.Migrated, name)
			continue
		}
		compared, err := compareWithSnapshot(ctx, db, snapshot.BackupPath)
		if err != nil {
			return report, fmt.Errorf("%s: %w", name, err)
		}
		report.Tables += compared
	}
	return report, ctx.Err()
}

// checkDatabaseIntegrity runs the full integrity_check (boot runs only the
// faster quick_check) and foreign_key_check.
func checkDatabaseIntegrity(ctx context.Context, db *sql.DB) error {
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("integrity_check failed")
	}
	var table string
	err := db.QueryRowContext(ctx, "SELECT `table` FROM pragma_foreign_key_check LIMIT 1").Scan(&table)
	if err == nil {
		return fmt.Errorf("foreign-key violation in %s", table)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

// compareWithSnapshot checks every table of the snapshot database against the
// restored one by row count and row checksum, and returns how many matched.
func compareWithSnapshot(ctx context.Context, restored *sql.DB, snapshotPath string) (int, error) {
	snapshot, err := openSQLiteReader(snapshotPath)
	if err != nil {
		return 0, err
	}
	defer snapshot.Close()
	want, err := tableChecksums(ctx, snapshot)
	if err != nil {
		return 0, err
	}
	got, err := tableChecksums(ctx, restored)
	if err != nil {
		return 0, err
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		restoredTable, ok := got[name]
		switch {
		case !ok:
			return 0, fmt.Errorf("table %s missing after restore", name)
		case restoredTable.rows != want[name].rows:
			return 0, fmt.Errorf("table %s row count %d, backup recorded %d", name, restoredTable.rows, want[name].rows)
		case restoredTable.checksum != want[name].checksum:
			return 0, fmt.Errorf("table %s row checksum differs from the backup", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			return 0, fmt.Errorf("table %s appeared after restore", name)
		}
	}
	return len(names), nil
}

type tableChecksum struct {
	rows     int
	checksum string
}

// tableChecksums hashes every user table's rows, in a stable order over all
// columns, so two databases with the same schema compare value by value.
func tableChecksums(ctx context.Context, db *sql.DB) (map[string]tableChecksum, error) {
	tables, err := userTables(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make(map[string]tableChecksum, len(tables))
	for _, table := range tables {
		sum, err := checksumTable(ctx, db, table)
		if err != nil {
			return nil, fmt.Errorf("checksum %s: %w", table, err)
		}
		out[table] = sum
	}
	return out, nil
}

func userTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name FROM pragma_table_list
		WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%'
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

func checksumTable(ctx context.Context, db *sql.DB, table string) (tableChecksum, error) {
	columns, err := tableColumnNames(ctx, db, table)
	if err != nil {
		return tableChecksum{}, err
	}
	if len(columns) == 0 {
		return tableChecksum{}, nil
	}
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = quoteSQLiteIdentifier(column)
	}
	list := strings.Join(quoted, ",")
	rows, err := db.QueryContext(ctx, "SELECT "+list+" FROM "+quoteSQLiteIdentifier(table)+" ORDER BY "+list) //nolint:gosec // G202: identifiers are quoted names read from the database's own schema.
	if err != nil {
		return tableChecksum{}, err
	}
	defer rows.Close()
	hash := sha256.New()
	count := 0
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return tableChecksum{}, err
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return tableChecksum{}, err
		}
		hash.Write(encoded)
		hash.Write([]byte{'\n'})
		count++
	}
	if err := rows.Err(); err != nil {
		return tableChecksum{}, err
	}
	return tableChecksum{rows: count, checksum: hex.EncodeToString(hash.Sum(nil))}, nil
}

func tableColumnNames(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func quoteSQLiteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

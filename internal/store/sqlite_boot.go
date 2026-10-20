package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

// openSQLiteWriter returns the single-writer handle for one database file
// (ADR-0011 §1: one write connection, BEGIN IMMEDIATE, WAL).
func openSQLiteWriter(path string) (*sql.DB, error) {
	return openSQLiteWriterWithPragmas(path, nil)
}

func openSQLiteWriterWithPragmas(path string, pragmas []string) (*sql.DB, error) {
	pragmas = append([]string{}, pragmas...)
	pragmas = append(pragmas, "foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(10000)")
	dsn := sqliteDSN(path, pragmas, "immediate")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = FULL;
		PRAGMA wal_autocheckpoint = 1000;
		PRAGMA busy_timeout = 10000;
	`); err != nil {
		_ = db.Close()
		return nil, err
	}
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		_ = db.Close()
		return nil, err
	}
	if !strings.EqualFold(journalMode, "wal") {
		_ = db.Close()
		return nil, fmt.Errorf("journal mode %q for %s, want wal", journalMode, path)
	}
	return db, nil
}

// openSQLiteReader returns a read pool so reads do not contend with the writer.
// cache_size and mmap_size are set per-connection on the read pool only (the
// single writer keeps its own semantics, ADR-0011 §1): a negative cache_size is
// a KiB page-cache budget (-65536 ≈ 64 MiB per reader) and mmap_size memory-maps
// the database so hot pages are served without read() syscalls — both are cheap
// read-throughput wins at the ADR-0011 §2 target scale.
func openSQLiteReader(path string) (*sql.DB, error) {
	return openSQLiteReaderWithPragmas(path, nil)
}

func openSQLiteReaderWithPragmas(path string, pragmas []string) (*sql.DB, error) {
	pragmas = append([]string{}, pragmas...)
	pragmas = append(pragmas, "query_only(1)", "busy_timeout(10000)", "cache_size(-65536)", "mmap_size(268435456)")
	dsn := sqliteDSN(path, pragmas, "")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// openSQLite opens a standalone read/write handle for tooling and tests.
func openSQLite(path string) (*sql.DB, error) {
	return openSQLiteWriter(path)
}

func sqliteDSN(path string, pragmas []string, txlock string) string {
	dsn := (&url.URL{Scheme: "file", Path: path}).String()
	var query []string
	for _, pragma := range pragmas {
		query = append(query, "_pragma="+url.QueryEscape(pragma))
	}
	if txlock != "" {
		query = append(query, "_txlock="+url.QueryEscape(txlock))
	}
	if len(query) == 0 {
		return dsn
	}
	return dsn + "?" + strings.Join(query, "&")
}

// DatabaseCorruptedError is returned by Open when a database file fails its
// startup integrity check (PANEL-10 / ADR-0011 reliability). The server refuses
// to start on a corrupt database rather than run on — and potentially overwrite
// or further damage — it. It is recoverable by construction (invariant #6): the
// corrupt file is left in place untouched (never auto-deleted, never replaced
// with a fresh empty database — that would silently lose the catalog), and the
// operator restores a pre-migration backup over it or recovers it out-of-band.
type DatabaseCorruptedError struct {
	Name   string // "catalog" | "events"
	Path   string
	Detail string // first integrity-check failure line
}

func (e *DatabaseCorruptedError) Error() string {
	return fmt.Sprintf("%s database at %s failed its startup integrity check (%s); "+
		"the server will not start on a corrupt database. Restore the most recent "+
		"pre-migration backup over it (see <data-dir>/backups/pre-migration) or "+
		"recover it out-of-band with `sqlite3 %q .recover`, then restart. Guided "+
		"restore flow: /manage/backups#restore.",
		e.Name, e.Path, e.Detail, e.Path)
}

// checkSQLiteIntegrity runs PRAGMA quick_check — a fast structural b-tree
// validation, the ADR-0011-audit-recommended boot check (cheaper than the full
// integrity_check and without its false-positive sensitivity to in-flight WAL
// states). A healthy database returns exactly one row, "ok"; anything else is
// treated as corruption and surfaced as a *DatabaseCorruptedError so the caller
// refuses startup with restore guidance rather than failing later mid-query.
func checkSQLiteIntegrity(ctx context.Context, db *sql.DB, name, path string) error {
	corrupt := func(detail string) error {
		return &DatabaseCorruptedError{Name: name, Path: path, Detail: detail}
	}
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return corrupt(err.Error())
	}
	defer rows.Close()
	var results []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return corrupt(err.Error())
		}
		results = append(results, line)
	}
	if err := rows.Err(); err != nil {
		return corrupt(err.Error())
	}
	if len(results) == 1 && strings.EqualFold(results[0], "ok") {
		return nil
	}
	detail := "quick_check returned no rows"
	if len(results) > 0 {
		detail = results[0]
	}
	return corrupt(detail)
}

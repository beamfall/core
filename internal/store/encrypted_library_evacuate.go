package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// encryptedLibraryEvacuation moves one catalog table's rows for an Encrypted
// Library into that Library's protected plane (ADR-0045). Rows written to
// catalog.db before the plane owned the table are legacy plaintext; they are
// copied on unseal and then removed from catalog.db.
type encryptedLibraryEvacuation struct {
	table   string
	columns []string
	// where selects the Library's rows in catalog.db; its single placeholder
	// binds the Library ID.
	where string
	// mediaColumn names a column holding a Media ID that must already live in
	// the protected plane for the Library's rows to move.
	mediaColumn string
}

// encryptedLibraryEvacuations lists tables in parent-first order; deletes run
// in reverse. A Library's rows move all together or not at all: when any row
// names Media still held in catalog.db (legacy rows written before the plane
// owned that Media), every row stays there, as private as that Media already
// is, and nothing is dropped.
var encryptedLibraryEvacuations = []encryptedLibraryEvacuation{
	{
		table:   "collection",
		columns: []string{"id", "library_id", "parent_id", "name", "sort_order", "trashed_at_ms", "created_at_ms", "updated_at_ms"},
		where:   "library_id = ?",
	},
	{
		table:       "collection_media",
		columns:     []string{"collection_id", "media_id", "sort_order", "added_at_ms"},
		where:       "collection_id IN (SELECT id FROM collection WHERE library_id = ?)",
		mediaColumn: "media_id",
	},
}

// evacuatePlaintextEncryptedLibraryRowsLocked runs at the end of a successful
// unseal. The protected copy commits first; the catalog delete follows. A
// failure in either step returns an error so the caller unwinds the unseal:
// catalog.db still holds the rows and the next unseal retries, and the copy is
// idempotent (INSERT OR IGNORE).
func (s *Store) evacuatePlaintextEncryptedLibraryRowsLocked(ctx context.Context, plane catalogPlane) error {
	type tableRows struct {
		spec encryptedLibraryEvacuation
		rows [][]any
	}
	var pending []tableRows
	for _, spec := range encryptedLibraryEvacuations {
		rows, err := readEvacuationRows(ctx, s.catalogR, spec, plane.libraryID)
		if err != nil {
			return fmt.Errorf("read plaintext %s rows: %w", spec.table, err)
		}
		if len(rows) > 0 {
			pending = append(pending, tableRows{spec: spec, rows: rows})
		}
	}
	if len(pending) == 0 {
		return nil
	}
	for _, p := range pending {
		if p.spec.mediaColumn == "" {
			continue
		}
		column := columnIndex(p.spec.columns, p.spec.mediaColumn)
		for _, row := range p.rows {
			var one int
			err := plane.reader.QueryRowContext(ctx, `SELECT 1 FROM media WHERE id = ?`, row[column]).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				if s.logger != nil {
					s.logger.WarnContext(ctx, "plaintext encrypted-library rows kept in catalog: they name media outside the protected plane", "libraryID", plane.libraryID, "table", p.spec.table)
				}
				return nil
			}
			if err != nil {
				return fmt.Errorf("check %s media in protected plane: %w", p.spec.table, err)
			}
		}
	}

	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Self-referencing parents may arrive in any order; check FKs at commit.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	for _, p := range pending {
		insert := evacuationInsertSQL(p.spec)
		for _, row := range p.rows {
			if _, err := tx.ExecContext(ctx, insert, row...); err != nil {
				return fmt.Errorf("copy %s row to protected plane: %w", p.spec.table, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if err := s.deletePlaintextEvacuatedRowsLocked(ctx, plane.libraryID); err != nil {
		return fmt.Errorf("delete evacuated plaintext rows: %w", err)
	}
	if s.logger != nil {
		s.logger.InfoContext(ctx, "evacuated plaintext encrypted-library rows", "libraryID", plane.libraryID, "tables", len(pending))
	}
	return nil
}

func readEvacuationRows(ctx context.Context, reader *sql.DB, spec encryptedLibraryEvacuation, libraryID LibraryID) ([][]any, error) {
	// rowid order keeps insertion order stable for equal sort keys.
	rows, err := reader.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE %s ORDER BY rowid`,
		strings.Join(spec.columns, ", "), spec.table, spec.where), libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(spec.columns))
		ptrs := make([]any, len(spec.columns))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func evacuationInsertSQL(spec encryptedLibraryEvacuation) string {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(spec.columns)), ", ")
	return fmt.Sprintf(`INSERT OR IGNORE INTO %s (%s) VALUES (%s)`, spec.table, strings.Join(spec.columns, ", "), placeholders)
}

func columnIndex(columns []string, name string) int {
	for i, c := range columns {
		if c == name {
			return i
		}
	}
	panic("store: evacuation column " + name + " not declared")
}

// deletePlaintextEvacuatedRowsLocked removes the Library's evacuated rows from
// catalog.db child-first, on one connection with secure_delete so the freed
// pages are zeroed rather than left holding the plaintext.
func (s *Store) deletePlaintextEvacuatedRowsLocked(ctx context.Context, libraryID LibraryID) error {
	conn, err := s.catalogW.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var previous int
	if err := conn.QueryRowContext(ctx, `PRAGMA secure_delete`).Scan(&previous); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), fmt.Sprintf(`PRAGMA secure_delete = %d`, previous))
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := len(encryptedLibraryEvacuations) - 1; i >= 0; i-- {
		spec := encryptedLibraryEvacuations[i]
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s`, spec.table, spec.where), libraryID); err != nil {
			return fmt.Errorf("delete plaintext %s rows: %w", spec.table, err)
		}
	}
	return tx.Commit()
}

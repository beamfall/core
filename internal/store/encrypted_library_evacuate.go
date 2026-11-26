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
	// the protected plane for the group's rows to move.
	mediaColumn string
	// orderBy replaces the default rowid read order, for WITHOUT ROWID tables.
	orderBy string
}

// encryptedLibraryEvacuationGroup is a set of tables whose rows move together
// or not at all. Tables are listed parent-first; deletes run in reverse.
type encryptedLibraryEvacuationGroup struct {
	name   string
	tables []encryptedLibraryEvacuation
	// pendingKey, when set, is an encrypted_library_metadata key the plane
	// migration that created the tables sets. The group runs only while the
	// key is present, and the key is cleared once catalog.db no longer holds
	// the rows, so the move happens once per plane.
	pendingKey string
}

// encryptedLibraryEvacuations lists the groups. A Collection group whose rows
// name Media still held in catalog.db (legacy rows written before the plane
// owned that Media) stays there whole, as private as that Media already is,
// and nothing is dropped.
var encryptedLibraryEvacuations = []encryptedLibraryEvacuationGroup{
	{
		name: "collections",
		tables: []encryptedLibraryEvacuation{
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
		},
	},
	{
		// CORE-0769: names and filter, Source, seed and rules expressions.
		name:       "expressions",
		pendingKey: "legacy_expression_evacuation",
		tables: []encryptedLibraryEvacuation{
			{
				table:   "saved_filter",
				columns: []string{"id", "profile_id", "library_id", "name", "filter_json", "pinned", "created_at_ms"},
				where:   "library_id = ?",
			},
			{
				table:   "smart_collection",
				columns: []string{"id", "library_id", "name", "source_expr", "created_at_ms", "created_by_profile_id", "sort_expr"},
				where:   "library_id = ?",
			},
			{
				table:   "saved_station",
				columns: []string{"id", "profile_id", "library_id", "name", "seed_json", "rules_json", "tuning_json", "created_at_ms"},
				where:   "library_id = ?",
			},
			{
				table:   "channel",
				columns: []string{"id", "library_id", "profile_id", "name", "source_expr", "ordering_policy", "repeat_policy", "created_at_ms"},
				where:   "library_id = ?",
			},
		},
	},
	{
		// CORE-0706: feed URLs, auth headers, GUIDs, enclosure URLs and paths
		// written before Encrypted Libraries refused Subscriptions, and the
		// item-state imports that name those Subscriptions.
		name:       "subscriptions",
		pendingKey: "legacy_subscription_evacuation",
		tables: []encryptedLibraryEvacuation{
			{
				table: "subscription",
				columns: []string{"id", "library_id", "feed_url", "root", "title", "added_at_ms", "last_polled_at_ms", "etag",
					"last_modified", "retention_kind", "retention_n", "auth_header", "auth_ref", "last_error_class",
					"failing_since_ms", "consecutive_failures", "next_poll_at_ms", "retired_at_ms", "moved_candidate_url",
					"moved_observations", "moved_at_ms", "refresh_requested_at_ms", "download_policy", "download_latest_n"},
				where: "library_id = ?",
			},
			{
				table:   "subscription_seen_entry",
				columns: []string{"subscription_id", "guid", "seen_at_ms", "path", "feed_position"},
				where:   legacySubscriptionChildren,
			},
			{
				table: "subscription_entry",
				columns: []string{"subscription_id", "guid", "media_id", "feed_position", "published_at_ms", "first_seen_at_ms",
					"enclosure_url", "enclosure_type", "enclosure_length", "download_state", "download_path"},
				where:       legacySubscriptionChildren,
				mediaColumn: "media_id",
			},
			{
				table: "subscription_entry_collision",
				columns: []string{"subscription_id", "guid", "bound_path", "incoming_path", "incoming_media_id",
					"first_seen_at_ms", "last_seen_at_ms", "observations"},
				where:       legacySubscriptionChildren,
				mediaColumn: "incoming_media_id",
				orderBy:     "subscription_id, guid, incoming_path",
			},
			{
				table: "item_state_import",
				columns: []string{"profile_id", "subscription_id", "source", "item_guid", "played", "has_progress", "progress",
					"starred", "imported_at_ms", "applied_at_ms", "applied_media_id", "import_run_id", "import_importer_id",
					"import_match_method"},
				where: legacySubscriptionChildren,
			},
		},
	},
}

const legacySubscriptionChildren = "subscription_id IN (SELECT id FROM subscription WHERE library_id = ?)"

type evacuationTableRows struct {
	spec encryptedLibraryEvacuation
	rows [][]any
}

// copyPlaintextEncryptedLibraryRowsLocked runs at the end of a successful
// unseal. It only copies: the returned plan deletes the catalog rows later,
// together with every other evacuation (commitEvacuationPlansLocked). A copy
// failure returns an error so the caller unwinds the unseal with catalog.db
// still holding the rows; the next unseal retries, and the copy is idempotent
// (INSERT OR IGNORE). Clearing a group's pending key comes last and only logs
// a failure: the rows are already out of catalog.db, and a rerun finds nothing
// left to move.
func (s *Store) copyPlaintextEncryptedLibraryRowsLocked(ctx context.Context, plane catalogPlane) (evacuationPlan, error) {
	var pending []evacuationTableRows
	var clearKeys []string
	for _, group := range encryptedLibraryEvacuations {
		if group.pendingKey != "" {
			set, err := encryptedLibraryMetadataKeySet(ctx, plane.reader, group.pendingKey)
			if err != nil {
				return evacuationPlan{}, fmt.Errorf("read %s evacuation state: %w", group.name, err)
			}
			if !set {
				continue
			}
		}
		rows, err := s.readEvacuationGroupLocked(ctx, plane, group)
		if err != nil {
			return evacuationPlan{}, err
		}
		if rows == nil {
			continue
		}
		pending = append(pending, rows...)
		if group.pendingKey != "" {
			clearKeys = append(clearKeys, group.pendingKey)
		}
	}
	var plan evacuationPlan
	if len(pending) > 0 {
		if err := copyEvacuationRows(ctx, plane.writer, pending); err != nil {
			return evacuationPlan{}, err
		}
		libraryID := plane.libraryID
		plan.deleteRows = func(ctx context.Context, tx *sql.Tx) error {
			for i := len(pending) - 1; i >= 0; i-- {
				spec := pending[i].spec
				if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s`, spec.table, spec.where), libraryID); err != nil {
					return fmt.Errorf("delete plaintext %s rows: %w", spec.table, err)
				}
			}
			return nil
		}
		plan.moved = len(pending)
	}
	plan.after = func(ctx context.Context) {
		for _, key := range clearKeys {
			if _, err := plane.writer.ExecContext(ctx, `DELETE FROM encrypted_library_metadata WHERE key = ?`, key); err != nil && s.logger != nil {
				s.logger.WarnContext(ctx, "encrypted-library evacuation state not cleared; the next unseal reruns it", "libraryID", plane.libraryID, "key", key, "error", err)
			}
		}
	}
	return plan, nil
}

// readEvacuationGroupLocked returns the group's catalog rows for the Library,
// or nil when the group must stay in catalog.db. A group with no rows returns
// an empty, non-nil slice so its pending key is still cleared.
func (s *Store) readEvacuationGroupLocked(ctx context.Context, plane catalogPlane, group encryptedLibraryEvacuationGroup) ([]evacuationTableRows, error) {
	out := []evacuationTableRows{}
	for _, spec := range group.tables {
		rows, err := readEvacuationRows(ctx, s.catalogR, spec, plane.libraryID)
		if err != nil {
			return nil, fmt.Errorf("read plaintext %s rows: %w", spec.table, err)
		}
		if len(rows) == 0 {
			continue
		}
		if spec.mediaColumn != "" {
			column := columnIndex(spec.columns, spec.mediaColumn)
			for _, row := range rows {
				var one int
				err := plane.reader.QueryRowContext(ctx, `SELECT 1 FROM media WHERE id = ?`, row[column]).Scan(&one)
				if errors.Is(err, sql.ErrNoRows) {
					if s.logger != nil {
						s.logger.WarnContext(ctx, "plaintext encrypted-library rows kept in catalog: they name media outside the protected plane", "libraryID", plane.libraryID, "group", group.name, "table", spec.table)
					}
					return nil, nil
				}
				if err != nil {
					return nil, fmt.Errorf("check %s media in protected plane: %w", spec.table, err)
				}
			}
		}
		out = append(out, evacuationTableRows{spec: spec, rows: rows})
	}
	return out, nil
}

func encryptedLibraryMetadataKeySet(ctx context.Context, reader *sql.DB, key string) (bool, error) {
	var one int
	err := reader.QueryRowContext(ctx, `SELECT 1 FROM encrypted_library_metadata WHERE key = ?`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func copyEvacuationRows(ctx context.Context, writer *sql.DB, pending []evacuationTableRows) error {
	tx, err := writer.BeginTx(ctx, nil)
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
	return tx.Commit()
}

func readEvacuationRows(ctx context.Context, reader *sql.DB, spec encryptedLibraryEvacuation, libraryID LibraryID) ([][]any, error) {
	// rowid order keeps insertion order stable for equal sort keys; a WITHOUT
	// ROWID table orders by its primary key.
	order := "rowid"
	if spec.orderBy != "" {
		order = spec.orderBy
	}
	rows, err := reader.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE %s ORDER BY %s`,
		strings.Join(spec.columns, ", "), spec.table, spec.where, order), libraryID)
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

// evacuationPlan is the delete half of an evacuation whose copy has already
// committed to the Library plane.
type evacuationPlan struct {
	deleteRows func(ctx context.Context, tx *sql.Tx) error
	after      func(ctx context.Context)
	moved      int
}

// commitEvacuationPlansLocked removes every plan's rows from the shared
// catalog in ONE transaction, only after all plans have copied. A failure
// leaves every shared row in place, so the unwind that follows loses nothing
// and the next unseal retries the whole move.
func (s *Store) commitEvacuationPlansLocked(ctx context.Context, libraryID LibraryID, plans ...evacuationPlan) error {
	hasDeletes := false
	for _, p := range plans {
		hasDeletes = hasDeletes || p.deleteRows != nil
	}
	if hasDeletes {
		if err := s.runEvacuationDeletesLocked(ctx, plans); err != nil {
			return err
		}
		if s.logger != nil {
			s.logger.InfoContext(ctx, "evacuated encrypted-library rows from the shared catalog", "libraryID", libraryID)
		}
	}
	for _, p := range plans {
		if p.after != nil {
			p.after(ctx)
		}
	}
	return nil
}

// runEvacuationDeletesLocked deletes child-first on one connection with
// secure_delete so the freed pages are zeroed rather than left holding the
// plaintext.
func (s *Store) runEvacuationDeletesLocked(ctx context.Context, plans []evacuationPlan) error {
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
	for _, p := range plans {
		if p.deleteRows == nil {
			continue
		}
		if err := p.deleteRows(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

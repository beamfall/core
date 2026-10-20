package store

import (
	"context"
	"strings"
)

// PurgePluginData hard-deletes Libraries' Entities of the given Kinds plus all
// their dependent rows (PANEL-8 / ADR-0104 Uninstall). It is the ONLY purge path
// for plugin Entity data: deactivation leaves data inert and fully reversible,
// and only an explicit, operator-confirmed Uninstall reaches here.
//
// Scope is (library_id IN libraries, kind IN kinds). A Library binds exactly one
// Plugin, so purging by Library + Kind unambiguously removes only
// the uninstalled plugin's data and can never touch another plugin's same-named
// Kind in a different Library.
//
// One DELETE does the whole job: entity_field_{int,real,text,date} and
// entity_relation (BOTH endpoints — from_entity_id and to_entity_id are each
// ON DELETE CASCADE, migration 0010) cascade off the entity row, and the
// entity_fts_delete trigger (migration 0020) clears the FTS row. The catalog
// writer runs with foreign_keys = ON (sqlite.go openSQLiteWriter), so the cascade
// is guaranteed — dependent rows can never be orphaned.
//
// The per-(kind,field) field INDEXES are deliberately RETAINED. Their names are
// global by Kind (idx_<table>_<kind>_<field>, entity_index.go), and a different
// plugin may legitimately declare the same Kind name, so dropping them on uninstall
// could corrupt another plugin's query surface. An index over zero rows is cheap;
// reclaiming a genuinely-unused index is a separate, collision-checked follow-up.
//
// Returns the number of Entity rows purged. Empty libraries or kinds are no-ops.
func (s *Store) PurgePluginData(ctx context.Context, libraries []LibraryID, kinds []string) (int64, error) {
	if len(libraries) == 0 || len(kinds) == 0 {
		return 0, nil
	}
	for _, libraryID := range libraries {
		if libraryID == "" {
			return 0, ValidationError("library id is required")
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	libraryPlaceholders := make([]string, len(libraries))
	kindPlaceholders := make([]string, len(kinds))
	args := make([]any, 0, len(libraries)+len(kinds))
	for i, libraryID := range libraries {
		libraryPlaceholders[i] = "?"
		args = append(args, string(libraryID))
	}
	for i, k := range kinds {
		kindPlaceholders[i] = "?"
		args = append(args, k)
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		"DELETE FROM entity WHERE library_id IN ("+strings.Join(libraryPlaceholders, ",")+") AND kind IN ("+strings.Join(kindPlaceholders, ",")+")",
		args...)
	if err != nil {
		return 0, err
	}
	purged, _ := res.RowsAffected()
	return purged, tx.Commit()
}

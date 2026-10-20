package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// EntityFieldIndex declares one plugin-declared Entity Kind field's index
// posture (the FieldSpec Filterable/Sortable/Searchable flags, lowered to the
// store's vocabulary). Registered at plugin activation: Filterable/Sortable
// ensure a composite index on the field's typed side table; Searchable folds
// the field's text values into entity full-text search.
type EntityFieldIndex struct {
	Kind  string
	Field string
	Type  EntityFieldType
	// Filterable allows EntityKindOptions equality filtering on the field.
	Filterable bool
	// Sortable allows EntityKindOptions ordering by the field.
	Sortable bool
	// Searchable folds the field's values into entity_fts (text fields only).
	Searchable bool
	// orderingSource is set only through RegisterEntityKindOrderings.
	orderingSource bool
}

// EntityKindPolicy declares one plugin-declared Entity Kind's KIND-level
// policies (the EntityKind.{FullTextSearchable,OrphanPolicy,CursorPaginated}
// seams, lowered to the store's vocabulary). Registered at plugin activation
// alongside the per-field index postures. Distinct from EntityFieldIndex,
// which is per-FIELD: these govern the whole kind's instances.
type EntityKindPolicy struct {
	Kind string
	// FullTextSearchable opts this kind's instances into entity full-text
	// search (title always, plus registered-Searchable field values). A kind
	// that does NOT opt in has no entity_fts row and never surfaces in
	// SearchEntities — even by title.
	FullTextSearchable bool
	// OrphanGC opts this kind into store-side orphan garbage collection
	// (OrphanPolicy:"gc"). A kind that does not opt in ("retain", the default)
	// keeps its orphaned instances until an explicit delete.
	OrphanGC bool
	// CursorPaginated opts this kind's list reads into stable keyset-cursor
	// pagination (EntitiesByKindCursor) rather than offset paging. Advisory:
	// the cursor query is available for any kind, but callers consult this to
	// choose the O(page) path for large kinds.
	CursorPaginated bool
}

// entityIndexNamePattern is the declared-name shape (the plugin contract's
// lowercase slug) every interpolated kind/field identifier must match before
// it reaches SQL text — interpolation is injection-safe by construction.
var entityIndexNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// entityFieldTableFor maps a declared field type to its typed side table and
// value column — the closed set every interpolated table name comes from.
func entityFieldTableFor(fieldType EntityFieldType) (table, column string, ok bool) {
	switch fieldType {
	case EntityFieldText, EntityFieldURL:
		return "entity_field_text", "value", true
	case EntityFieldNumber:
		return "entity_field_real", "value", true
	case EntityFieldFlag:
		return "entity_field_int", "value", true
	case EntityFieldDate:
		return "entity_field_date", "value_ms", true
	}
	return "", "", false
}

// RegisterEntityFieldIndexes installs plugin-declared field index postures:
// it creates the composite indexes Filterable/Sortable fields need
// (CREATE INDEX IF NOT EXISTS — idempotent across boots), records which
// fields are Searchable, and re-syncs the entity FTS rows of every kind a
// spec covers so declarations apply to entities upserted before activation.
// Two plugins declaring the same (kind, field) must agree on its type; the
// boolean postures merge by OR.
func (s *Store) RegisterEntityFieldIndexes(ctx context.Context, specs []EntityFieldIndex) error {
	for _, spec := range specs {
		if !entityIndexNamePattern.MatchString(spec.Kind) {
			return fmt.Errorf("entity field index kind %q must be a lowercase slug", spec.Kind)
		}
		if !entityIndexNamePattern.MatchString(spec.Field) {
			return fmt.Errorf("entity field index field %q must be a lowercase slug", spec.Field)
		}
		if _, _, ok := entityFieldTableFor(spec.Type); !ok {
			return fmt.Errorf("entity field index %s.%s has unknown type %q", spec.Kind, spec.Field, spec.Type)
		}
		if spec.Searchable && spec.Type != EntityFieldText {
			return fmt.Errorf("entity field index %s.%s is searchable but type %q, only text fields can be searchable", spec.Kind, spec.Field, spec.Type)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if s.entityIndexes == nil {
		s.entityIndexes = map[string]map[string]EntityFieldIndex{}
	}
	kinds := map[string]bool{}
	for _, spec := range specs {
		fields := s.entityIndexes[spec.Kind]
		if fields == nil {
			fields = map[string]EntityFieldIndex{}
			s.entityIndexes[spec.Kind] = fields
		}
		if prior, exists := fields[spec.Field]; exists {
			if prior.Type != spec.Type {
				return fmt.Errorf("entity field index %s.%s declared as both %q and %q", spec.Kind, spec.Field, prior.Type, spec.Type)
			}
			spec.Filterable = spec.Filterable || prior.Filterable
			spec.Sortable = spec.Sortable || prior.Sortable
			spec.Searchable = spec.Searchable || prior.Searchable
			spec.orderingSource = spec.orderingSource || prior.orderingSource
		}
		fields[spec.Field] = spec
		kinds[spec.Kind] = true

		if spec.Filterable || spec.Sortable {
			if err := applyEntityFieldIndexDDL(ctx, s.catalogW, spec); err != nil {
				return err
			}
		}
	}
	for _, db := range s.encryptedDBs {
		if db == nil || db.state != "unsealed-registered" || db.writer == nil {
			continue
		}
		for _, spec := range specs {
			if spec.Filterable || spec.Sortable {
				if err := applyEntityFieldIndexDDL(ctx, db.writer, spec); err != nil {
					return err
				}
			}
		}
	}
	for kind := range kinds {
		if err := s.resyncEntityFTSLocked(ctx, kind); err != nil {
			return err
		}
		for _, db := range s.encryptedDBs {
			if db == nil || db.state != "unsealed-registered" || db.writer == nil || db.reader == nil {
				continue
			}
			if err := s.resyncEntityFTSForDBLocked(ctx, db.reader, db.writer, kind); err != nil {
				return err
			}
		}
	}
	return nil
}

// RegisterEntityKindPolicies installs plugin-declared KIND-level policies: it
// records each kind's FullTextSearchable / OrphanGC / CursorPaginated posture
// and re-syncs the entity FTS rows of every kind whose FullTextSearchable
// posture it sets, so declarations apply to entities upserted before
// activation (an opt-out kind's title-only FTS rows are cleared; an opt-in
// kind's rows are (re)built). The boolean postures merge by OR when two active
// plugins declare the same kind — but ValidateBundles rejects colliding kind
// names across plugins, so in practice each kind has a single declarer.
func (s *Store) RegisterEntityKindPolicies(ctx context.Context, policies []EntityKindPolicy) error {
	for _, policy := range policies {
		if !entityIndexNamePattern.MatchString(policy.Kind) {
			return fmt.Errorf("entity kind policy kind %q must be a lowercase slug", policy.Kind)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if s.entityKindPolicies == nil {
		s.entityKindPolicies = map[string]EntityKindPolicy{}
	}
	kinds := map[string]bool{}
	for _, policy := range policies {
		if prior, exists := s.entityKindPolicies[policy.Kind]; exists {
			policy.FullTextSearchable = policy.FullTextSearchable || prior.FullTextSearchable
			policy.OrphanGC = policy.OrphanGC || prior.OrphanGC
			policy.CursorPaginated = policy.CursorPaginated || prior.CursorPaginated
		}
		s.entityKindPolicies[policy.Kind] = policy
		kinds[policy.Kind] = true
	}
	for kind := range kinds {
		if err := s.resyncEntityFTSLocked(ctx, kind); err != nil {
			return err
		}
		for _, db := range s.encryptedDBs {
			if db == nil || db.state != "unsealed-registered" || db.writer == nil || db.reader == nil {
				continue
			}
			if err := s.resyncEntityFTSForDBLocked(ctx, db.reader, db.writer, kind); err != nil {
				return err
			}
		}
	}
	return nil
}

// EntityKindPolicy returns one registered plugin-declared kind policy.
func (s *Store) EntityKindPolicy(kind string) (EntityKindPolicy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	policy, ok := s.entityKindPolicies[kind]
	return policy, ok
}

// entityKindFullTextSearchableLocked reports whether a kind opts into entity
// full-text search. Callers hold s.mu (read or write).
func (s *Store) entityKindFullTextSearchableLocked(kind string) bool {
	return s.entityKindPolicies[kind].FullTextSearchable
}

// orphanGCKindsLocked returns the sorted kind names that declared
// OrphanPolicy:"gc" — the set PurgeOrphanEntities collects. Sorted so the
// generated IN-list is deterministic. Callers hold s.mu (read or write).
func (s *Store) orphanGCKindsLocked() []string {
	var kinds []string
	for kind, policy := range s.entityKindPolicies {
		if policy.OrphanGC {
			kinds = append(kinds, kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}

// entityKindPlaceholders builds the comma-joined "?" list and matching args
// for a set of kind names.
func entityKindPlaceholders(kinds []string) (string, []any) {
	placeholders := make([]string, len(kinds))
	args := make([]any, len(kinds))
	for i, kind := range kinds {
		placeholders[i] = "?"
		args[i] = kind
	}
	return strings.Join(placeholders, ","), args
}

func applyEntityFieldIndexDDL(ctx context.Context, db *sql.DB, spec EntityFieldIndex) error {
	table, column, _ := entityFieldTableFor(spec.Type)
	name := fmt.Sprintf("idx_%s_%s_%s", table, spec.Kind, spec.Field)
	statement := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %q ON %s(%s, entity_id) WHERE kind = %s AND field = %s`,
		name, table, column, quoteSQLiteString(spec.Kind), quoteSQLiteString(spec.Field),
	)
	_, err := db.ExecContext(ctx, statement)
	return err
}

// entityFieldSideTableSuffixes is the closed set of side-table suffixes that
// appear in a plugin field index name (the fourth `_`-delimited token). It
// mirrors entityFieldTableFor's tables (entity_field_{text,real,int,date}) and
// bounds parseEntityFieldIndexName against a future table addition.
var entityFieldSideTableSuffixes = map[string]bool{"text": true, "real": true, "int": true, "date": true}

// parseEntityFieldIndexName recovers the (kind, field) a plugin field index
// covers from its name. applyEntityFieldIndexDDL builds names as
// idx_<table>_<kind>_<field> where <table> is entity_field_<suffix>; since Kind
// and Field are underscore-free slugs (entityIndexNamePattern), splitting on
// `_` yields exactly six tokens — idx / entity / field / <suffix> / <kind> /
// <field> — and the parse is unambiguous. Anything else (a core
// entity_field_*_kfv_idx base index, a stray name, an unknown suffix) returns
// ok=false and is left untouched.
func parseEntityFieldIndexName(name string) (kind, field string, ok bool) {
	parts := strings.Split(name, "_")
	if len(parts) != 6 || parts[0] != "idx" || parts[1] != "entity" || parts[2] != "field" {
		return "", "", false
	}
	if !entityFieldSideTableSuffixes[parts[3]] {
		return "", "", false
	}
	return parts[4], parts[5], true
}

// ReconcileEntityFieldIndexes drops the composite indexes and FTS contributions
// of Entity Kind fields that NO currently-active plugin still declares (ADR-0104
// §2: field-schema evolution within an active plugin). Run ONCE at boot AFTER
// RegisterEntityFieldIndexes has installed the full active set, so s.entityIndexes
// is the authoritative "what the current revisions declare" set. A persisted index
// or stale FTS contribution whose (kind, field) is absent from that set belongs to
// a field or Kind a plugin revision REMOVED; its query surface is now dead and is
// reclaimed here. Stored Entity rows are NEVER touched — removed-field data stays
// inert and is purged only by an explicit Uninstall (PurgePluginData, ADR-0104 §1).
// Re-declaring a field in a later revision recreates its index + FTS via the normal
// load path (RegisterEntityFieldIndexes), restoring its preserved values.
//
// Index names are global per (kind, field), so the membership test in
// s.entityIndexes IS the collision check: if ANY active plugin still declares the
// (kind, field) as Filterable/Sortable it stays in the map and its index is
// retained — a different plugin's same-named Kind can never have its surface
// dropped out from under it.
func (s *Store) ReconcileEntityFieldIndexes(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.dropRetiredEntityFieldIndexesLocked(ctx, s.catalogW); err != nil {
		return err
	}
	if err := s.resyncRetiredEntityFTSForDBLocked(ctx, s.catalogR, s.catalogW); err != nil {
		return err
	}
	for _, db := range s.encryptedDBs {
		if db == nil || db.state != "unsealed-registered" || db.writer == nil || db.reader == nil {
			continue
		}
		if err := s.dropRetiredEntityFieldIndexesLocked(ctx, db.writer); err != nil {
			return err
		}
		if err := s.resyncRetiredEntityFTSForDBLocked(ctx, db.reader, db.writer); err != nil {
			return err
		}
	}
	return nil
}

// dropRetiredEntityFieldIndexesLocked drops every plugin field index in db whose
// (kind, field) no active plugin still declares Filterable/Sortable. Callers hold
// s.mu (write). Names are collected fully before any DROP so a single-conn writer
// never reads and writes the same connection concurrently.
func (s *Store) dropRetiredEntityFieldIndexesLocked(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_entity_field_%'`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, name := range names {
		kind, field, ok := parseEntityFieldIndexName(name)
		if !ok {
			continue
		}
		if spec, declared := s.entityIndexes[kind][field]; declared && (spec.Filterable || spec.Sortable) {
			continue // still an active query surface
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("DROP INDEX IF EXISTS %q", name)); err != nil {
			return err
		}
	}
	return nil
}

// resyncRetiredEntityFTSForDBLocked clears the FTS contribution of Kinds that
// carry searchable text yet are no longer registered — i.e. ENTIRELY-removed
// Kinds (absent from s.entityIndexes). A removed *searchable-only* field grows no
// composite index, so dropping indexes alone cannot reclaim its FTS surface; this
// is the FTS half. Kinds that still have ANY field registered stay in
// s.entityIndexes and were already resynced against the live set by
// RegisterEntityFieldIndexes, so they are skipped here. The `fields <> ”` filter
// skips never-searchable Kinds (their FTS document is already title-only), so this
// is not an every-boot full rewrite. Callers hold s.mu (write).
func (s *Store) resyncRetiredEntityFTSForDBLocked(ctx context.Context, reader, writer *sql.DB) error {
	rows, err := reader.QueryContext(ctx,
		`SELECT DISTINCT e.kind FROM entity e JOIN entity_fts f ON f.entity_id = e.id WHERE f.fields <> ''`)
	if err != nil {
		return err
	}
	var kinds []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			rows.Close()
			return err
		}
		kinds = append(kinds, kind)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, kind := range kinds {
		if _, registered := s.entityIndexes[kind]; registered {
			continue
		}
		if err := s.resyncEntityFTSForDBLocked(ctx, reader, writer, kind); err != nil {
			return err
		}
	}
	return nil
}

// resyncEntityFTSLocked rebuilds one kind's entity_fts rows against the
// currently registered Searchable set. Callers hold s.mu (write).
func (s *Store) resyncEntityFTSLocked(ctx context.Context, kind string) error {
	return s.resyncEntityFTSForDBLocked(ctx, s.catalogR, s.catalogW, kind)
}

func (s *Store) resyncEntityFTSForDBLocked(ctx context.Context, reader, writer *sql.DB, kind string) error {
	rows, err := reader.QueryContext(ctx, `SELECT id, title FROM entity WHERE kind = ?`, kind)
	if err != nil {
		return err
	}
	entities, err := func() ([]Entity, error) {
		defer rows.Close()
		var entities []Entity
		for rows.Next() {
			entity := Entity{Kind: kind}
			if err := rows.Scan(&entity.ID, &entity.Title); err != nil {
				return nil, err
			}
			entities = append(entities, entity)
		}
		return entities, rows.Err()
	}()
	if err != nil {
		return err
	}
	ids := make([]EntityID, len(entities))
	index := make(map[EntityID]int, len(entities))
	for i := range entities {
		ids[i] = entities[i].ID
		index[entities[i].ID] = i
	}
	if err := s.loadEntityFieldsFrom(ctx, reader, ids, func(id EntityID, field EntityField) {
		i := index[id]
		entities[i].Fields = append(entities[i].Fields, field)
	}); err != nil {
		return err
	}
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM entity_fts WHERE entity_id IN (SELECT id FROM entity WHERE kind = ?)
	`, kind); err != nil {
		return err
	}
	// Only kinds that opted into FullTextSearchable carry an FTS row; a kind
	// that did not opt in stays cleared (delete-only above) so it never
	// surfaces in SearchEntities, not even by title.
	if s.entityKindFullTextSearchableLocked(kind) {
		for _, entity := range entities {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO entity_fts(entity_id, title, fields) VALUES (?, ?, ?)
			`, entity.ID, entity.Title, s.entitySearchTextLocked(entity)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) replayEntityFieldIndexesForEncryptedDBLocked(ctx context.Context, db *encryptedLibraryDB) error {
	if db == nil || db.writer == nil || db.reader == nil {
		return nil
	}
	kinds := map[string]bool{}
	for _, fields := range s.entityIndexes {
		for _, spec := range fields {
			if spec.Filterable || spec.Sortable {
				if err := applyEntityFieldIndexDDL(ctx, db.writer, spec); err != nil {
					return err
				}
			}
			kinds[spec.Kind] = true
		}
	}
	for kind := range kinds {
		if err := s.resyncEntityFTSForDBLocked(ctx, db.reader, db.writer, kind); err != nil {
			return err
		}
	}
	// An encrypted library unsealed after boot persists its own indexes/FTS
	// across seal cycles, so a revision that removed a field/Kind leaves the same
	// dead surface here as in the catalog. Reconcile it to the active set too
	//, now that the active indexes are (re)created above.
	if err := s.dropRetiredEntityFieldIndexesLocked(ctx, db.writer); err != nil {
		return err
	}
	if err := s.resyncRetiredEntityFTSForDBLocked(ctx, db.reader, db.writer); err != nil {
		return err
	}
	return nil
}

// entitySearchTextLocked composes the entity_fts fields column: the entity's
// registered-Searchable text values, field-name-sorted for a deterministic
// document. Callers hold s.mu (read or write).
func (s *Store) entitySearchTextLocked(entity Entity) string {
	specs := s.entityIndexes[entity.Kind]
	if len(specs) == 0 {
		return ""
	}
	fields := append([]EntityField(nil), entity.Fields...)
	sortEntityFields(fields)
	var parts []string
	for _, field := range fields {
		spec, declared := specs[field.Name]
		if !declared || !spec.Searchable {
			continue
		}
		if value := strings.TrimSpace(field.Text); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

// SearchEntities returns Entities whose title or Searchable field values match
// the FTS query, reveal-filtered exactly like SearchMedia (ADR-0011: results
// never name restricted content to unrevealed scopes — an entity in a hidden
// Library is indistinguishable from one that does not exist). The query is
// matched as a prefix of each word so type-ahead search works. limit ≤ 0
// means the default 50.
func (s *Store) SearchEntities(ctx context.Context, filter LibraryFilter, query string, limit int) ([]SourceItem, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []SourceItem{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	restricted := map[LibraryID]bool{}
	for _, library := range libraries {
		if library.IsRestricted {
			restricted[library.ID] = true
		}
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	return s.fuzzySearchEntitiesLocked(ctx, visible, restricted, query, limit, "", nil)
}

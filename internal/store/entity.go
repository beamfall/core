package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// EntityID is a stable Entity instance identifier.
type EntityID string

// EntityFieldType is the fixed field-schema vocabulary plugins declare
// against (mirrors the plugin contract's FieldTypes; ADR-0003 §2).
type EntityFieldType string

const (
	// EntityFieldText is free text, stored in the text side table.
	EntityFieldText EntityFieldType = "text"
	// EntityFieldNumber is numeric, stored in the real side table.
	EntityFieldNumber EntityFieldType = "number"
	// EntityFieldDate is a point in time, stored as millis in the date side table.
	EntityFieldDate EntityFieldType = "date"
	// EntityFieldFlag is boolean, stored in the int side table.
	EntityFieldFlag EntityFieldType = "flag"
	// EntityFieldURL is a link, stored in the text side table.
	EntityFieldURL EntityFieldType = "url"
)

// EntityField is one declared field value on an Entity instance. Exactly the
// member matching Type carries the value (Text serves both text and url).
type EntityField struct {
	Name   string
	Type   EntityFieldType
	Text   string
	Number float64
	Flag   bool
	Date   time.Time
}

// Entity is one persisted instance of a plugin-declared Entity Kind:
// generic core schema — plugins contribute kinds as data,
// never tables. Entities are Library-scoped so reveal filtering applies.
//
// Kind stores the bare declared kind name, unqualified by plugin. That is
// collision-safe by construction: a Library binds at most one plugin
// (library.plugin_id), Entities always carry a library_id, and Entity IDs
// derive from (library, kind, name) — so two plugins declaring the same kind
// name can never produce ambiguous rows; the (library_id, kind) pair resolves
// the owner. The one read that spans Libraries by bare kind, the
// "entity-items" Source, separates plugins via its Library scope
// (SourceQuery.LibraryID).
type Entity struct {
	ID        EntityID
	LibraryID LibraryID
	Kind      string
	Title     string
	Fields    []EntityField
}

var errVisibleEntityNotFound = errors.New("entity not found")

// IsEntityNotFound reports whether err is the error Entity returns for an
// Entity that does not exist, is in trash or is not visible under the filter.
func IsEntityNotFound(err error) bool {
	return errors.Is(err, errVisibleEntityNotFound)
}

// EntityRelation is one relation row: entity→entity or entity→Media (exactly
// one target set), named per the declaring plugin's RelationSpec, with
// render-only attributes (ADR-0003 §2: credit-style links are relations with
// attributes, never entities).
type EntityRelation struct {
	FromEntityID EntityID
	Name         string
	ToEntityID   EntityID
	ToMediaID    MediaID
	Attributes   map[string]string
	// Cardinality mirrors the declaring RelationSpec's: "" or "many" places
	// no constraint; "one" means the target Media is claimed by at most one
	// source entity for this relation name — upserting a second source
	// replaces the first (enforced for Media targets only; entity-target
	// enforcement is deferred).
	Cardinality string
	// SortOrder is the row's optional position among the source entity's
	// children (OrderedChildren containers). Nil means unordered; reads put
	// ordered rows first.
	SortOrder *int
	// KeepAttributes leaves an existing row's attributes as they are on
	// upsert: only a new row takes Attributes. For writers that re-order or
	// re-link a row without carrying its attributes (CORE-2913).
	KeepAttributes bool
	// Writer is the automated writer that owns a Media-target row, for
	// example a scraper's key; empty means an operator or an unattributed
	// writer. Every upsert records its own Writer, so an operator re-linking a
	// scraper's row takes it over. Automated cleanup
	// (RemoveWrittenMediaRelations) removes only rows its own Writer owns.
	Writer string
}

// WrittenRelation names the Media-target rows one automated cleanup may
// remove: rows under relation Name from a source Entity of Kind.
type WrittenRelation struct {
	Kind string
	Name string
}

// RelatedEntity pairs an Entity with the attributes of the relation row that
// reached it.
type RelatedEntity struct {
	Entity         Entity
	Attributes     map[string]string
	Relation       string
	SortOrder      *int
	OperatorLocked bool
}

// EntityRelationRow is a reveal-filtered curation read over one source
// Entity's relation rows. Exactly one target is set.
type EntityRelationRow struct {
	Name       string
	ToEntity   *Entity
	ToMedia    *Media
	Attributes map[string]string
	SortOrder  *int
}

// UpsertEntity inserts or fully replaces an Entity instance and its field
// values. Like ImportMediaFile this is a server-internal write path (scan,
// scraper, identification) — reads are reveal-filtered, writes are not.
func (s *Store) UpsertEntity(ctx context.Context, entity Entity) error {
	return s.upsertEntity(ctx, entity, false)
}

// UpsertEntityKeepTitle is UpsertEntity for a writer that does not own the
// title: entity.Title only titles a new row, and an existing row, trashed or
// not, keeps its stored title inside the same write transaction, so a
// concurrent rename is never overwritten. Field values are replaced.
func (s *Store) UpsertEntityKeepTitle(ctx context.Context, entity Entity) error {
	return s.upsertEntity(ctx, entity, true)
}

func validateEntity(entity Entity) error {
	if entity.ID == "" {
		return errors.New("entity ID is required")
	}
	if entity.LibraryID == "" {
		return errors.New("library ID is required")
	}
	if entity.Kind == "" {
		return errors.New("entity kind is required")
	}
	if entity.Title == "" {
		return errors.New("entity title is required")
	}
	names := map[string]bool{}
	for _, field := range entity.Fields {
		if field.Name == "" {
			return errors.New("entity field name is required")
		}
		if names[field.Name] {
			return errors.New("duplicate entity field " + field.Name)
		}
		names[field.Name] = true
		switch field.Type {
		case EntityFieldText, EntityFieldNumber, EntityFieldFlag, EntityFieldURL:
		case EntityFieldDate:
			if field.Date.IsZero() {
				return errors.New("entity date field " + field.Name + " requires a value")
			}
		default:
			return errors.New("unknown entity field type " + string(field.Type))
		}
	}
	return nil
}

func (s *Store) upsertEntity(ctx context.Context, entity Entity, keepTitle bool) error {
	// Authority face: an Entity is a catalog-identity row. An
	// Attached node identifies locally (probe/parse — byte-local), but the
	// row-authoring side-effect of Identification is main-only; it index-pushes.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if err := validateEntity(entity); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, entity.LibraryID)
	if err != nil {
		return err
	}

	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	title, err := s.upsertEntityTx(ctx, tx, entity, keepTitle)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !plane.encrypted {
		s.titleFolds.note(entity.LibraryID, entity.Kind, entity.ID, title)
	}
	return nil
}

func (s *Store) upsertEntityTx(ctx context.Context, tx *sql.Tx, entity Entity, keepTitle bool) (string, error) {
	titleUpdate := "excluded.title"
	if keepTitle {
		titleUpdate = "entity.title"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO entity(id, library_id, kind, title) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  library_id = excluded.library_id,
		  kind = excluded.kind,
		  title = `+titleUpdate+`
	`, entity.ID, entity.LibraryID, entity.Kind, entity.Title); err != nil {
		return "", err
	}
	if keepTitle {
		// The search document and title fold below index the stored title.
		if err := tx.QueryRowContext(ctx, `SELECT title FROM entity WHERE id = ?`, entity.ID).Scan(&entity.Title); err != nil {
			return "", err
		}
	}
	for _, table := range entityFieldTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE entity_id = ?`, entity.ID); err != nil {
			return "", err
		}
	}
	for _, field := range entity.Fields {
		var table string
		var value any
		switch field.Type {
		case EntityFieldText, EntityFieldURL:
			table, value = "entity_field_text", field.Text
		case EntityFieldNumber:
			table, value = "entity_field_real", field.Number
		case EntityFieldFlag:
			table, value = "entity_field_int", boolToInt(field.Flag)
		case EntityFieldDate:
			table, value = "entity_field_date", timeToMillis(field.Date.UTC())
		}
		column := "value"
		if table == "entity_field_date" {
			column = "value_ms"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO `+table+`(entity_id, kind, field, field_type, `+column+`) VALUES (?, ?, ?, ?, ?)
		`, entity.ID, entity.Kind, field.Name, field.Type, value); err != nil {
			return "", err
		}
	}
	// Entity FTS sync (the upsert half — deletion is the entity_fts_delete
	// trigger): replace the entity's search document with its title plus the
	// registered-Searchable field values. Only kinds that opted into
	// FullTextSearchable carry an FTS row (the KIND-level seam); a non-opted
	// kind is delete-only so it never surfaces in SearchEntities.
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_fts WHERE entity_id = ?`, entity.ID); err != nil {
		return "", err
	}
	if s.entityKindFullTextSearchableLocked(entity.Kind) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO entity_fts(entity_id, title, fields) VALUES (?, ?, ?)
		`, entity.ID, entity.Title, s.entitySearchTextLocked(entity)); err != nil {
			return "", err
		}
	}
	return entity.Title, nil
}

// EnsureEntity creates an Entity when no row carries its ID and otherwise
// leaves the existing row, its title and its field values untouched. It is
// the write a field-less hydration (a name-only credit) uses so that it never
// erases the fields an earlier UpsertEntity stored.
func (s *Store) EnsureEntity(ctx context.Context, entity Entity) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if entity.ID == "" {
		return errors.New("entity ID is required")
	}
	if entity.LibraryID == "" {
		return errors.New("library ID is required")
	}
	if entity.Kind == "" {
		return errors.New("entity kind is required")
	}
	if entity.Title == "" {
		return errors.New("entity title is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, entity.LibraryID)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.ensureEntityTx(ctx, tx, entity); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ensureEntityTx(ctx context.Context, tx *sql.Tx, entity Entity) error {
	// One insert-if-absent statement, so a concurrent UpsertEntity of the
	// same Entity either lands first and is kept or lands after and wins.
	result, err := tx.ExecContext(ctx, `
		INSERT INTO entity(id, library_id, kind, title) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, entity.ID, entity.LibraryID, entity.Kind, entity.Title)
	if err != nil {
		return err
	}
	if inserted, err := result.RowsAffected(); err != nil || inserted == 0 {
		return err
	}
	if s.entityKindFullTextSearchableLocked(entity.Kind) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO entity_fts(entity_id, title, fields) VALUES (?, ?, ?)
		`, entity.ID, entity.Title, s.entitySearchTextLocked(entity)); err != nil {
			return err
		}
	}
	return nil
}

var entityFieldTables = []string{
	"entity_field_int", "entity_field_real", "entity_field_text", "entity_field_date",
}

// Entity returns an Entity instance only when its Library is visible under
// the provided filter (the Reveal contract covers entities, not just Media).
func (s *Store) Entity(ctx context.Context, id EntityID, filter LibraryFilter) (Entity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Entity{}, err
	}
	plane, err := s.catalogPlaneForEntityLocked(ctx, id)
	if err != nil {
		return Entity{}, err
	}
	entity, err := s.visibleEntitySQLFrom(ctx, plane.reader, id, filter)
	if err != nil {
		return Entity{}, err
	}
	entity.Fields, err = s.entityFieldsSQLFrom(ctx, plane.reader, id)
	if err != nil {
		return Entity{}, err
	}
	return entity, nil
}

// EntitiesByKind returns one page of the Library's Entity instances of one
// declared kind,
// ordered by title. The Library itself must be visible under the filter.
// opts may narrow by equality on a registered-Filterable field and order by a
// registered-Sortable one (EntityKindOptions; the zero value keeps the plain
// title page). Pages like SourceItems: limit ≤ 0 means the default page (50),
// capped at 200; offset < 0 reads as 0. (title, id) ordering keeps pages
// stable.
func (s *Store) EntitiesByKind(ctx context.Context, library LibraryID, kind string, filter LibraryFilter, opts EntityKindOptions, limit, offset int) ([]Entity, error) {
	if library == "" {
		return nil, errors.New("library ID is required")
	}
	if kind == "" {
		return nil, errors.New("entity kind is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if err := s.libraryVisibleSQL(ctx, library, filter); err != nil {
		return nil, err
	}
	entities, err := s.entitiesByKindSQL(ctx, library, kind, opts, limit, offset)
	if err != nil {
		return nil, err
	}
	index := make(map[EntityID]int, len(entities))
	ids := make([]EntityID, len(entities))
	for i := range entities {
		index[entities[i].ID] = i
		ids[i] = entities[i].ID
	}
	if err := s.loadEntityFields(ctx, ids, func(id EntityID, field EntityField) {
		i := index[id]
		entities[i].Fields = append(entities[i].Fields, field)
	}); err != nil {
		return nil, err
	}
	for i := range entities {
		if entities[i].Fields == nil {
			entities[i].Fields = []EntityField{}
		}
		sortEntityFields(entities[i].Fields)
	}
	return entities, nil
}

// UpsertEntityRelation inserts or replaces one relation row. Exactly one of
// ToEntityID / ToMediaID must be set; re-upserting the same (from, name, to)
// replaces the attributes and sort order. A "one"-cardinality write to a
// Media target replaces any other source entity's row for the same relation
// name on that Media.
func (s *Store) UpsertEntityRelation(ctx context.Context, relation EntityRelation) error {
	_, err := s.upsertEntityRelation(ctx, relation, false)
	return err
}

// UpsertHydratedEntityRelation adds a derived link while respecting operator locks.
func (s *Store) UpsertHydratedEntityRelation(ctx context.Context, relation EntityRelation) error {
	_, err := s.upsertEntityRelation(ctx, relation, true)
	return err
}

// UpsertHydratedEntityRelationWritten is UpsertHydratedEntityRelation that
// also reports whether it wrote the row: false when an operator lock blocked
// the derived link, so the caller does not treat the link as its own.
func (s *Store) UpsertHydratedEntityRelationWritten(ctx context.Context, relation EntityRelation) (bool, error) {
	return s.upsertEntityRelation(ctx, relation, true)
}

func (s *Store) upsertEntityRelation(ctx context.Context, relation EntityRelation, hydrated bool) (bool, error) {
	// Authority face: Entity-relation catalog rows are main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return false, err
	}
	if relation.FromEntityID == "" {
		return false, errors.New("relation source entity is required")
	}
	if relation.Name == "" {
		return false, errors.New("relation name is required")
	}
	if (relation.ToEntityID == "") == (relation.ToMediaID == "") {
		return false, errors.New("relation requires exactly one target")
	}
	switch relation.Cardinality {
	case "", "one", "many":
	default:
		return false, errors.New("unknown relation cardinality " + relation.Cardinality)
	}
	attributes, sortOrder, err := entityRelationRowValues(relation)
	if err != nil {
		return false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	fromPlane, err := s.relationWritePlaneLocked(ctx, relation)
	if err != nil {
		return false, err
	}
	if relation.ToEntityID != "" {
		_, err = fromPlane.writer.ExecContext(ctx, `
			INSERT INTO entity_relation(from_entity_id, relation, to_entity_id, attributes, sort_order)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(from_entity_id, relation, to_entity_id) WHERE to_entity_id IS NOT NULL
			DO UPDATE SET attributes = CASE WHEN ? THEN entity_relation.attributes ELSE excluded.attributes END,
				sort_order = excluded.sort_order
		`, relation.FromEntityID, relation.Name, relation.ToEntityID, attributes, sortOrder, relation.KeepAttributes)
		return err == nil, err
	}
	tx, err := fromPlane.writer.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if hydrated {
		blocked, err := hydrationRelationBlocked(ctx, tx, relation)
		if err != nil || blocked {
			return false, err
		}
	}
	if relation.Cardinality == "one" {
		// One-cardinality: this Media belongs to at most one source entity
		// for this relation name — replace, never duplicate.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM entity_relation
			WHERE relation = ? AND to_media_id = ? AND from_entity_id <> ?
			  AND (NOT ? OR operator_locked_at_ms IS NULL)
		`, relation.Name, relation.ToMediaID, relation.FromEntityID, hydrated); err != nil {
			return false, err
		}
	}
	if err := insertMediaRelationTx(ctx, tx, relation, attributes, sortOrder); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ReplaceMediaRelations rewrites the Media-target relations of one Media for
// every relation name the given rows carry: the Media's previous rows under
// each name are replaced in one transaction. Operator locks protect their
// rows and block conflicting derived rows. Names absent from the input stay
// as they are. Every row must target mediaID and the source Entities must
// already exist on the Media's catalog plane.
func (s *Store) ReplaceMediaRelations(ctx context.Context, mediaID MediaID, relations []EntityRelation) error {
	// Authority face: Entity-relation catalog rows are main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if mediaID == "" {
		return errors.New("relation target media is required")
	}
	type row struct {
		relation   EntityRelation
		attributes any
		sortOrder  any
	}
	rows := make([]row, 0, len(relations))
	names := []string{}
	seen := map[string]bool{}
	for _, relation := range relations {
		if relation.FromEntityID == "" {
			return errors.New("relation source entity is required")
		}
		if relation.Name == "" {
			return errors.New("relation name is required")
		}
		if relation.ToEntityID != "" || relation.ToMediaID != mediaID {
			return errors.New("replaced relations must all target the media")
		}
		switch relation.Cardinality {
		case "", "one", "many":
		default:
			return errors.New("unknown relation cardinality " + relation.Cardinality)
		}
		attributes, sortOrder, err := entityRelationRowValues(relation)
		if err != nil {
			return err
		}
		rows = append(rows, row{relation: relation, attributes: attributes, sortOrder: sortOrder})
		if !seen[relation.Name] {
			seen[relation.Name] = true
			names = append(names, relation.Name)
		}
	}
	if len(rows) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	toPlane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fromPlane, err := s.catalogPlaneForEntityLocked(ctx, r.relation.FromEntityID)
		if err != nil {
			return err
		}
		if fromPlane.writer != toPlane.writer {
			return errors.New("encrypted cross-library entity relations are not supported")
		}
	}
	tx, err := toPlane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM entity_relation WHERE relation = ? AND to_media_id = ? AND operator_locked_at_ms IS NULL`, name, mediaID,
		); err != nil {
			return err
		}
	}
	for _, r := range rows {
		blocked, err := hydrationRelationBlocked(ctx, tx, r.relation)
		if err != nil {
			return err
		}
		if blocked {
			continue
		}
		if err := insertMediaRelationTx(ctx, tx, r.relation, r.attributes, r.sortOrder); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RemoveWrittenMediaRelations deletes one Media's Media-target rows that
// writer owns, under each relation from a source Entity of that relation's
// Kind, in one transaction. Rows an operator or another writer owns, other
// relation names, other source kinds and other Media are left as they are; no
// relations removes nothing.
func (s *Store) RemoveWrittenMediaRelations(ctx context.Context, mediaID MediaID, writer string, relations []WrittenRelation) error {
	// Authority face: Entity-relation catalog rows are main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if mediaID == "" {
		return errors.New("relation target media is required")
	}
	if writer == "" {
		return errors.New("relation writer is required")
	}
	if len(relations) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, relation := range relations {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM entity_relation
			WHERE to_media_id = ? AND relation = ? AND writer = ?
			  AND from_entity_id IN (SELECT id FROM entity WHERE kind = ?)
		`, mediaID, relation.Name, writer, relation.Kind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceEntityRelations rewrites the Entity-target relations of one source
// Entity under each of names: the source's previous Entity-target rows under
// those names are deleted and the given rows inserted, in one transaction, so
// a name with no rows is cleared. Other relation names, and Media-target rows,
// are left as they are. Every row must come from fromID, target an Entity and
// carry one of names.
func (s *Store) ReplaceEntityRelations(ctx context.Context, fromID EntityID, names []string, relations []EntityRelation) error {
	// Authority face: Entity-relation catalog rows are main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if fromID == "" {
		return errors.New("relation source entity is required")
	}
	type row struct {
		relation   EntityRelation
		attributes any
		sortOrder  any
	}
	named := map[string]bool{}
	for _, name := range names {
		if name == "" {
			return errors.New("relation name is required")
		}
		named[name] = true
	}
	rows := make([]row, 0, len(relations))
	for _, relation := range relations {
		if relation.FromEntityID != fromID || relation.ToEntityID == "" || relation.ToMediaID != "" {
			return errors.New("replaced relations must all come from the entity and target an entity")
		}
		if !named[relation.Name] {
			return errors.New("replaced relation " + relation.Name + " is not one of the replaced names")
		}
		switch relation.Cardinality {
		case "", "one", "many":
		default:
			return errors.New("unknown relation cardinality " + relation.Cardinality)
		}
		attributes, sortOrder, err := entityRelationRowValues(relation)
		if err != nil {
			return err
		}
		rows = append(rows, row{relation: relation, attributes: attributes, sortOrder: sortOrder})
	}
	if len(names) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	fromPlane, err := s.catalogPlaneForEntityLocked(ctx, fromID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		toPlane, err := s.catalogPlaneForEntityLocked(ctx, r.relation.ToEntityID)
		if err != nil {
			return err
		}
		if fromPlane.encrypted || toPlane.encrypted {
			if !fromPlane.encrypted || !toPlane.encrypted || fromPlane.libraryID != toPlane.libraryID {
				return errors.New("encrypted cross-library entity relations are not supported")
			}
		}
	}
	tx, err := fromPlane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM entity_relation WHERE from_entity_id = ? AND relation = ? AND to_entity_id IS NOT NULL`, fromID, name,
		); err != nil {
			return err
		}
	}
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO entity_relation(from_entity_id, relation, to_entity_id, attributes, sort_order)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(from_entity_id, relation, to_entity_id) WHERE to_entity_id IS NOT NULL
			DO UPDATE SET attributes = excluded.attributes, sort_order = excluded.sort_order
		`, fromID, r.relation.Name, r.relation.ToEntityID, r.attributes, r.sortOrder); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// hydrationRelationBlocked also honors stored single-valued locks when a
// derived writer omits cardinality.
func hydrationRelationBlocked(ctx context.Context, tx *sql.Tx, relation EntityRelation) (bool, error) {
	var blocked bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM entity_relation
			WHERE to_media_id = ? AND relation = ? AND operator_locked_at_ms IS NOT NULL
			  AND (operator_lock_one = 1 OR ? OR from_entity_id = ?)
		)
	`, relation.ToMediaID, relation.Name, relation.Cardinality == "one", relation.FromEntityID).Scan(&blocked)
	return blocked, err
}

// entityRelationRowValues encodes a relation's attributes and sort order as
// their nullable column values.
func entityRelationRowValues(relation EntityRelation) (attributes, sortOrder any, err error) {
	if len(relation.Attributes) > 0 {
		data, err := json.Marshal(relation.Attributes)
		if err != nil {
			return nil, nil, err
		}
		attributes = string(data)
	}
	if relation.SortOrder != nil {
		sortOrder = *relation.SortOrder
	}
	return attributes, sortOrder, nil
}

// insertMediaRelationTx upserts one Media-target row. A row a local sidecar
// owns (LocalRelationWriter) is left as it is by an automated writer; only an
// operator write (no Writer) takes it over.
func insertMediaRelationTx(ctx context.Context, tx *sql.Tx, relation EntityRelation, attributes, sortOrder any) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO entity_relation(from_entity_id, relation, to_media_id, attributes, sort_order, writer)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))
		ON CONFLICT(from_entity_id, relation, to_media_id) WHERE to_media_id IS NOT NULL
		DO UPDATE SET attributes = CASE WHEN ? THEN entity_relation.attributes ELSE excluded.attributes END,
			sort_order = excluded.sort_order, writer = excluded.writer
		WHERE entity_relation.writer IS NOT '`+LocalRelationWriter+`' OR excluded.writer IS NULL
	`, relation.FromEntityID, relation.Name, relation.ToMediaID, attributes, sortOrder, relation.Writer, relation.KeepAttributes)
	return err
}

// EntitiesForMedia returns the visible Entities linked to one visible Media.
// An empty name reads all links, grouped by relation name. Each group's rows
// follow relation sort order and then title.
func (s *Store) EntitiesForMedia(ctx context.Context, mediaID MediaID, relationName string, filter LibraryFilter) ([]RelatedEntity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	// The relations live beside the Media item: in the catalog, or in an
	// unlocked encrypted library's own database (same-library only).
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, mediaID, filter); err != nil {
		return nil, err
	}
	// Read before the relation query: a catalog read nested inside an open
	// catalog cursor holds two reader connections at once and can exhaust the
	// pool under concurrent reads.
	visibleLibraries, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil {
		return nil, err
	}
	query := `
		SELECT e.id, e.library_id, e.kind, e.title, r.attributes, r.relation, r.sort_order, r.operator_locked_at_ms IS NOT NULL
		FROM entity_relation r
		JOIN entity e ON e.id = r.from_entity_id
		WHERE r.to_media_id = ? AND e.trashed_at_ms IS NULL`
	args := []any{mediaID}
	if relationName != "" {
		query += " AND r.relation = ?"
		args = append(args, relationName)
	}
	rows, err := plane.reader.QueryContext(ctx, query+
		" ORDER BY r.relation, (r.sort_order IS NULL), r.sort_order, e.title, e.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	related := []RelatedEntity{}
	for rows.Next() {
		var item RelatedEntity
		entity := &item.Entity
		var attributes sql.NullString
		if err := rows.Scan(&entity.ID, &entity.LibraryID, &entity.Kind, &entity.Title,
			&attributes, &item.Relation, &item.SortOrder, &item.OperatorLocked); err != nil {
			return nil, err
		}
		if !slices.Contains(visibleLibraries, entity.LibraryID) {
			continue
		}
		if item.Attributes, err = decodeRelationAttributes(attributes); err != nil {
			return nil, err
		}
		related = append(related, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	index := make(map[EntityID][]int, len(related))
	ids := make([]EntityID, 0, len(related))
	for i := range related {
		id := related[i].Entity.ID
		if len(index[id]) == 0 {
			ids = append(ids, id)
		}
		index[id] = append(index[id], i)
	}
	if err := s.loadEntityFieldsFrom(ctx, plane.reader, ids, func(id EntityID, field EntityField) {
		for _, i := range index[id] {
			related[i].Entity.Fields = append(related[i].Entity.Fields, field)
		}
	}); err != nil {
		return nil, err
	}
	for i := range related {
		if related[i].Entity.Fields == nil {
			related[i].Entity.Fields = []EntityField{}
		}
		sortEntityFields(related[i].Entity.Fields)
	}
	return related, nil
}

// libraryRestrictionsLocked maps every Library row, trashed or not, to its
// restricted flag: the visibility input a catalog join on library would read.
func (s *Store) libraryRestrictionsLocked(ctx context.Context) (map[LibraryID]bool, error) {
	rows, err := s.catalogR.QueryContext(ctx, `SELECT id, is_restricted FROM library`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	restricted := map[LibraryID]bool{}
	for rows.Next() {
		var id LibraryID
		var flag int
		if err := rows.Scan(&id, &flag); err != nil {
			return nil, err
		}
		restricted[id] = flag != 0
	}
	return restricted, rows.Err()
}

// MediaMembership is one generic Entity an item belongs to: the relation name
// the plugin declared the link under, plus the target Entity's bare Kind and
// Title. It carries no domain vocabulary (invariant 1) — "album membership" is
// just a (Relation="member", Kind="album", Title=…) tuple to core; the export
// manifest reproduces these verbatim so a third party can read group membership
// without core ever naming the plugin's concepts.
type MediaMembership struct {
	Relation string
	Kind     string
	Title    string
}

// MediaForEntity returns the Media items a visible Entity relates to, in the
// shape Source queries use. Trashed Media and Media in Libraries not visible
// under the filter are omitted.
func (s *Store) MediaForEntity(ctx context.Context, entityID EntityID, filter LibraryFilter) ([]SourceItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	items, err := s.mediaForEntitySQL(ctx, entityID, filter, -1, 0, nil)
	if err != nil {
		return nil, err
	}
	// Locked-mode ceilings keep above-ceiling Media in this list, as on every
	// browse surface; flag them so a client never counts them as playable.
	if !filter.PlaybackGate {
		if err := s.annotateLockedLocked(ctx, filter.Certification, items, filter.GrantedMediaIDs); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// mediaForEntitySQL backs MediaForEntity and the "entity-media" Source kind:
// the Media a visible Entity relates to, paged. Rows carrying a sort_order
// come first in that order (OrderedChildren containers — the play-next seed);
// unordered rows follow, title-ordered. limit -1 means every row (SQLite's
// LIMIT -1). The context Entity must itself be visible under the filter; an
// entity hidden behind the Reveal contract errors the same way a missing one
// does.
func (s *Store) mediaForEntitySQL(ctx context.Context, entityID EntityID, filter LibraryFilter, limit, offset int, source *SourceQuery) ([]SourceItem, error) {
	// Relations and Media live beside the Entity: the catalog, or an
	// encrypted Library's own database (a sealed one errors here).
	plane, err := s.catalogPlaneForEntityLocked(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleEntitySQLFrom(ctx, plane.reader, entityID, filter); err != nil {
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
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+3)
	args = append(args, entityID)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	if library, encrypted, err := s.encryptedLibraryForReaderLocked(ctx, plane.reader); err != nil {
		return nil, err
	} else if encrypted {
		// The encrypted plane has no library table; scope by the catalog
		// Library row's plugin instead.
		pluginID := ""
		if library.PluginID != nil {
			pluginID = *library.PluginID
		}
		certClause, certArgs = planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	}
	args = append(args, certArgs...)
	extrasClause := ""
	if source != nil && source.ExcludeExtras {
		// Extras: the Extra bit or the Extra side of an extra-of relation (CORE-2506).
		extrasClause = mediaExcludeExtrasClause + ` AND NOT EXISTS (
			SELECT 1 FROM media_relation x WHERE x.from_media_id = m.id AND x.relation = ?)`
		args = append(args, MediaRelationExtraOf)
	}
	// #nosec G202 -- fragments are generated placeholders and closed certification and Extras SQL.
	statement := `
		SELECT m.id, m.title, m.duration_seconds, m.library_id, COALESCE(m.year, 0)
		FROM entity_relation r
		JOIN media m ON m.id = r.to_media_id
		WHERE r.from_entity_id = ? AND m.trashed_at_ms IS NULL
			AND m.library_id IN (` + strings.Join(placeholders, ", ") + `)` + certClause + extrasClause + `
		ORDER BY (r.sort_order IS NULL), r.sort_order, m.title, m.id
		LIMIT ? OFFSET ?`
	if source != nil && source.ExcludeEditionGroupOf != "" {
		if _, err := s.visibleMediaSQL(ctx, source.ExcludeEditionGroupOf, filter); err != nil {
			return nil, err
		}
		// Eligibility starts with visible credits. Representatives use the same
		// visible set, independent of the credit, so Related cannot pick different
		// siblings for different signals. Mute still applies after Source selection.
		// UNION terminates chains and cycles; relations stay in this reader's plane.
		// #nosec G202 -- fragments are generated placeholders and closed certification SQL.
		statement = `
		WITH RECURSIVE visible_media AS (
			SELECT m.id, m.title, m.duration_seconds, m.library_id, COALESCE(m.year, 0) AS year FROM media m
			WHERE m.trashed_at_ms IS NULL
			AND m.library_id IN (` + strings.Join(placeholders, ", ") + `)` + certClause + extrasClause + `
		), credited AS (
			SELECT m.id, MIN(r.sort_order) AS sort_order
			FROM visible_media m JOIN entity_relation r ON r.to_media_id = m.id
			WHERE r.from_entity_id = ? GROUP BY m.id
		), edition_group(seed, id) AS (
			SELECT id, id FROM credited
			UNION
			SELECT g.seed, CASE WHEN r.from_media_id = g.id THEN r.to_media_id ELSE r.from_media_id END
			FROM edition_group g JOIN media_relation r
			ON r.relation = 'edition-of' AND (r.from_media_id = g.id OR r.to_media_id = g.id)
		), group_keys AS (
			SELECT seed, MIN(id) AS group_id FROM edition_group GROUP BY seed
		), candidates AS (
			SELECT k.group_id, m.id, m.title, m.duration_seconds, m.library_id, m.year,
			       MIN(c.sort_order) AS sort_order,
			       EXISTS (SELECT 1 FROM media_relation r WHERE r.from_media_id = m.id
			               AND r.relation = 'edition-of') AS is_edition
			FROM group_keys k JOIN credited c ON c.id = k.seed
			JOIN edition_group g ON g.seed = k.seed JOIN visible_media m ON m.id = g.id
			WHERE NOT EXISTS (SELECT 1 FROM edition_group excluded
			                  WHERE excluded.seed = k.seed AND excluded.id = ?)
			GROUP BY k.group_id, m.id
		), ranked AS (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY is_edition, id) AS position
			FROM candidates
		)
		SELECT id, title, duration_seconds, library_id, year FROM ranked WHERE position = 1
		ORDER BY (sort_order IS NULL), sort_order, title, id LIMIT ? OFFSET ?`
		// The folded query binds visibility before the credited Entity.
		args = append(args[1:], entityID, source.ExcludeEditionGroupOf)
	}
	args = append(args, limit, offset)
	// #nosec G202 -- statement contains only closed SQL and generated placeholders.
	rows, err := plane.reader.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceItem{}
	for rows.Next() {
		var id MediaID
		var title string
		var durationSeconds int
		var libraryID LibraryID
		var year int
		if err := rows.Scan(&id, &title, &durationSeconds, &libraryID, &year); err != nil {
			return nil, err
		}
		// Library-level restricted flag: annotated on items this
		// reveal-filtered query already lawfully returns; the graph layer
		// suppresses it on the wire unless the scope is Revealed.
		items = append(items, SourceItem{
			ID:              id,
			Kind:            "media",
			Title:           title,
			Summary:         mediaDurationSummary(durationSeconds),
			Restricted:      restricted[libraryID],
			LibraryID:       libraryID,
			Year:            year,
			DurationSeconds: durationSeconds,
		})
	}
	return items, rows.Err()
}

// DepthAwareMediaCount returns how many distinct visible Media an Entity
// relates to, counting its own links plus those of every descendant reachable
// through the self-referential parent relation (ADR-0027 D2 §1's depth-aware
// counts). parentRelation names the same-Kind child→parent relation the
// hierarchy is built on (the declaring plugin's RelationSpec / EntityMap
// ParentRelation); a child links to its parent under it, so descendants are the
// entities whose parent chain climbs to this one. An empty parentRelation, or a
// kind that is not hierarchical, yields the flat own-Media count — the rollup
// then spans only the context Entity. Media in Libraries the filter hides are
// excluded the same way mediaForEntitySQL excludes them, so the count never
// leaks across the Reveal contract; the context Entity must itself be visible.
//
// Generic by construction: the traversal is over relation rows by name, the
// count is over Media links — no domain vocabulary, no kind-specific branch.
func (s *Store) DepthAwareMediaCount(ctx context.Context, entityID EntityID, parentRelation string, filter LibraryFilter) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	if _, err := s.visibleEntitySQL(ctx, entityID, filter); err != nil {
		return 0, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return 0, err
	}
	var visible []LibraryID
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(visible))
	libArgs := make([]any, len(visible))
	for i, id := range visible {
		placeholders[i] = "?"
		libArgs[i] = id
	}
	libIn := strings.Join(placeholders, ", ")
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)

	// subtree gathers the context Entity and every descendant reachable by
	// climbing the child→parent relation in reverse (a child's row points
	// to_entity_id = parent, so descendants are the from_entity_id of rows whose
	// target is already in the set). When parentRelation is empty no edges
	// match and the subtree is the single context Entity — a flat count.
	args := make([]any, 0, 2+len(visible)*2+len(certArgs))
	args = append(args, entityID)       // anchor
	args = append(args, parentRelation) // recursive edge filter
	args = append(args, libArgs...)     // child-entity library scope
	args = append(args, libArgs...)     // media library scope
	args = append(args, certArgs...)
	var count int
	err = s.catalogR.QueryRowContext(ctx, `
		WITH RECURSIVE subtree(id) AS (
			SELECT id FROM entity WHERE id = ? AND trashed_at_ms IS NULL
			UNION
			SELECT child.id
			FROM entity_relation r
			JOIN subtree ON subtree.id = r.to_entity_id
			JOIN entity child ON child.id = r.from_entity_id
			WHERE r.relation = ?
				AND child.trashed_at_ms IS NULL
				AND child.library_id IN (`+libIn+`)
		)
		SELECT COUNT(DISTINCT m.id)
		FROM subtree
		JOIN entity_relation r ON r.from_entity_id = subtree.id
		JOIN media m ON m.id = r.to_media_id
		WHERE m.trashed_at_ms IS NULL
			AND m.library_id IN (`+libIn+`)`+certClause+`
	`, args...).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// PurgeOrphanEntities deletes Entities no relation row references in either
// role — the sweep that runs alongside the trash retention purge. Only kinds
// that declared OrphanPolicy:"gc" (registered via RegisterEntityKindPolicies)
// are collected; a kind with the default "retain" policy keeps its orphaned
// instances until an explicit delete. An Entity related to anything survives,
// even when every related Media is trashed: purging the Media cascades its
// relation rows away, and the next sweep then collects the Entity (trash stays
// restorable, hard invariant 6). Returns the purged IDs so the caller can
// remove the Entities' artwork files. Field-table rows go with the entity rows
// via ON DELETE CASCADE.
//
// Residual: Entities held only by entity→entity relations (a container chain
// with no Media left anywhere) survive the sweep — conservative on purpose.
func (s *Store) PurgeOrphanEntities(ctx context.Context) ([]EntityID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	gcKinds := s.orphanGCKindsLocked()
	if len(gcKinds) == 0 {
		return nil, nil // no kind opted into gc — nothing is collectible
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	kindPlaceholders, kindArgs := entityKindPlaceholders(gcKinds)
	orphans := `
		SELECT id FROM entity
		WHERE kind IN (` + kindPlaceholders + `)
		  AND id NOT IN (SELECT from_entity_id FROM entity_relation)
		  AND id NOT IN (SELECT to_entity_id FROM entity_relation WHERE to_entity_id IS NOT NULL)
	`
	rows, err := tx.QueryContext(ctx, orphans+` ORDER BY id`, kindArgs...)
	if err != nil {
		return nil, err
	}
	var purged []EntityID
	for rows.Next() {
		var id EntityID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		purged = append(purged, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(purged) == 0 {
		return nil, tx.Rollback()
	}
	idPlaceholders, idArgs := entityIDPlaceholders(purged)
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity WHERE id IN (`+idPlaceholders+`)`, idArgs...); err != nil {
		return nil, err
	}
	return purged, tx.Commit()
}

func (s *Store) visibleEntitySQL(ctx context.Context, id EntityID, filter LibraryFilter) (Entity, error) {
	plane, err := s.catalogPlaneForEntityLocked(ctx, id)
	if err != nil {
		return Entity{}, err
	}
	return s.visibleEntitySQLFrom(ctx, plane.reader, id, filter)
}

func (s *Store) visibleEntitySQLFrom(ctx context.Context, db *sql.DB, id EntityID, filter LibraryFilter) (Entity, error) {
	var entity Entity
	err := db.QueryRowContext(ctx, `
		SELECT e.id, e.library_id, e.kind, e.title
		FROM entity e
		WHERE e.id = ? AND e.trashed_at_ms IS NULL
	`, id).Scan(&entity.ID, &entity.LibraryID, &entity.Kind, &entity.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return Entity{}, errVisibleEntityNotFound
	}
	if err != nil {
		return Entity{}, err
	}
	library, err := s.libraryByIDLocked(ctx, entity.LibraryID)
	if err != nil {
		return Entity{}, err
	}
	if !libraryVisible(library, filter) {
		return Entity{}, errVisibleEntityNotFound
	}
	return entity, nil
}

// libraryVisibleSQL reports "library not found" both for missing Libraries
// and for Libraries hidden from the caller's scope, so restricted Libraries
// are indistinguishable from nonexistent ones (the Reveal contract).
func (s *Store) libraryVisibleSQL(ctx context.Context, id LibraryID, filter LibraryFilter) error {
	var restricted int
	err := s.catalogR.QueryRowContext(ctx, `SELECT is_restricted FROM library WHERE id = ?`, id).Scan(&restricted)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("library not found")
	}
	if err != nil {
		return err
	}
	if !libraryVisible(Library{ID: id, IsRestricted: restricted != 0}, filter) {
		return errors.New("library not found")
	}
	return nil
}

func scanEntities(rows *sql.Rows) ([]Entity, error) {
	defer rows.Close()
	entities := []Entity{}
	for rows.Next() {
		var entity Entity
		if err := rows.Scan(&entity.ID, &entity.LibraryID, &entity.Kind, &entity.Title); err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	return entities, rows.Err()
}

// loadEntityFields fills Fields for a batch of Entities in four queries per
// id batch (one per typed side table) instead of four per Entity — the list
// paths (EntitiesByKind, EntitiesForMedia, RelatedEntities) were 4×N. The id
// set is chunked (entityIDBatchSize per query) so the IN-list never approaches
// modernc.org/sqlite's 32766 bind-variable cap on large libraries;
// every caller is bounded for free. Aggregation is order-independent — each row
// is routed to its entity by id — so chunking changes nothing for small inputs.
func (s *Store) loadEntityFields(ctx context.Context, ids []EntityID, assign func(EntityID, EntityField)) error {
	return s.loadEntityFieldsFrom(ctx, s.catalogR, ids, assign)
}

func (s *Store) loadEntityFieldsFrom(ctx context.Context, db *sql.DB, ids []EntityID, assign func(EntityID, EntityField)) error {
	if len(ids) == 0 {
		return nil
	}
	for _, batch := range entityIDBatches(ids, entityIDBatchSize) {
		placeholders, args := entityIDPlaceholders(batch)
		for _, table := range entityFieldTables {
			column := "value"
			if table == "entity_field_date" {
				column = "value_ms"
			}
			rows, err := db.QueryContext(ctx,
				`SELECT entity_id, field, field_type, `+column+` FROM `+table+
					` WHERE entity_id IN (`+placeholders+`)`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id EntityID
				var field EntityField
				switch table {
				case "entity_field_text":
					err = rows.Scan(&id, &field.Name, &field.Type, &field.Text)
				case "entity_field_real":
					err = rows.Scan(&id, &field.Name, &field.Type, &field.Number)
				case "entity_field_int":
					var value int64
					if err = rows.Scan(&id, &field.Name, &field.Type, &value); err == nil {
						field.Flag = value != 0
					}
				case "entity_field_date":
					var millis int64
					if err = rows.Scan(&id, &field.Name, &field.Type, &millis); err == nil {
						field.Date = millisToTime(millis)
					}
				}
				if err != nil {
					rows.Close()
					return err
				}
				assign(id, field)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
	}
	return nil
}

// entityIDBatchSize bounds loadEntityFields' IN-list well under SQLite's 32766
// bind-variable cap — the same 500 the engagement prune sweep uses.
const entityIDBatchSize = 500

// entityIDBatches splits ids into fixed-size chunks; the EntityID sibling of
// mediaIDBatches. Returns nil for an empty input.
func entityIDBatches(ids []EntityID, size int) [][]EntityID {
	if len(ids) == 0 {
		return nil
	}
	batches := make([][]EntityID, 0, (len(ids)+size-1)/size)
	for len(ids) > 0 {
		n := size
		if len(ids) < n {
			n = len(ids)
		}
		batches = append(batches, ids[:n])
		ids = ids[n:]
	}
	return batches
}

// entityIDPlaceholders builds the comma-joined "?" list and matching args for
// one batch; the EntityID sibling of mediaIDPlaceholders.
func entityIDPlaceholders(ids []EntityID) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return strings.Join(placeholders, ","), args
}

// sortEntityFields keeps the deterministic by-name field order the
// per-entity loader guaranteed.
func sortEntityFields(fields []EntityField) {
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
}

func (s *Store) entityFieldsSQLFrom(ctx context.Context, db *sql.DB, id EntityID) ([]EntityField, error) {
	fields := []EntityField{}
	for _, table := range entityFieldTables {
		column := "value"
		if table == "entity_field_date" {
			column = "value_ms"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT field, field_type, `+column+` FROM `+table+` WHERE entity_id = ?`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var field EntityField
			switch table {
			case "entity_field_text":
				err = rows.Scan(&field.Name, &field.Type, &field.Text)
			case "entity_field_real":
				err = rows.Scan(&field.Name, &field.Type, &field.Number)
			case "entity_field_int":
				var value int64
				if err = rows.Scan(&field.Name, &field.Type, &value); err == nil {
					field.Flag = value != 0
				}
			case "entity_field_date":
				var millis int64
				if err = rows.Scan(&field.Name, &field.Type, &millis); err == nil {
					field.Date = millisToTime(millis)
				}
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			fields = append(fields, field)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields, nil
}

func decodeRelationAttributes(raw sql.NullString) (map[string]string, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	attributes := map[string]string{}
	if err := json.Unmarshal([]byte(raw.String), &attributes); err != nil {
		// Older hydration wrote a tag alongside the render attributes.
		var legacy struct {
			Attributes map[string]string `json:"attributes"`
			Tag        json.RawMessage   `json:"@hydration"`
		}
		if legacyErr := json.Unmarshal([]byte(raw.String), &legacy); legacyErr != nil || len(legacy.Tag) == 0 {
			return nil, err
		}
		return legacy.Attributes, nil
	}
	return attributes, nil
}

// RelatedEntities returns the Entities the given Entity points at through one
// relation name (the entity→entity read path: container children, links).
// The source Entity must be visible; far-side Entities in libraries the
// filter cannot see are silently omitted.
func (s *Store) RelatedEntities(ctx context.Context, entityID EntityID, relationName string, filter LibraryFilter) ([]RelatedEntity, error) {
	if relationName == "" {
		return nil, errors.New("relation name is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if _, err := s.visibleEntitySQL(ctx, entityID, filter); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT e.id, e.library_id, e.kind, e.title, l.is_restricted, r.attributes
		FROM entity_relation r
		JOIN entity e ON e.id = r.to_entity_id
		JOIN library l ON l.id = e.library_id
		WHERE r.from_entity_id = ? AND r.relation = ? AND e.trashed_at_ms IS NULL
		ORDER BY e.title, e.id
	`, entityID, relationName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	related := []RelatedEntity{}
	for rows.Next() {
		var entity Entity
		var restricted int
		var attributes sql.NullString
		if err := rows.Scan(&entity.ID, &entity.LibraryID, &entity.Kind, &entity.Title, &restricted, &attributes); err != nil {
			return nil, err
		}
		if !libraryVisible(Library{ID: entity.LibraryID, IsRestricted: restricted != 0}, filter) {
			continue
		}
		item := RelatedEntity{Entity: entity}
		if item.Attributes, err = decodeRelationAttributes(attributes); err != nil {
			return nil, err
		}
		related = append(related, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	index := make(map[EntityID]int, len(related))
	ids := make([]EntityID, len(related))
	for i := range related {
		index[related[i].Entity.ID] = i
		ids[i] = related[i].Entity.ID
	}
	if err := s.loadEntityFields(ctx, ids, func(id EntityID, field EntityField) {
		i := index[id]
		related[i].Entity.Fields = append(related[i].Entity.Fields, field)
	}); err != nil {
		return nil, err
	}
	for i := range related {
		if related[i].Entity.Fields == nil {
			related[i].Entity.Fields = []EntityField{}
		}
		sortEntityFields(related[i].Entity.Fields)
	}
	return s.orderRelatedEntities(ctx, entityID, relationName, related)
}

// EntityRelationRows returns one bounded page of visible relation rows for a
// visible source Entity. Visibility is part of the SQL candidate set, before
// ordering and pagination, so hidden targets cannot create short or shifted
// pages. Graph callers supply limit and offset; omission preserves the
// internal all-rows compatibility path.
func (s *Store) EntityRelationRows(ctx context.Context, entityID EntityID, relationName string, filter LibraryFilter, page ...int) ([]EntityRelationRow, error) {
	limit, offset := boundedReadPage(page)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForEntityLocked(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleEntitySQLFrom(ctx, plane.reader, entityID, filter); err != nil {
		return nil, err
	}
	visible, visibleArgs, err := s.entityRelationVisibleSQLLocked(ctx, plane, filter)
	if err != nil {
		return nil, err
	}
	if visible == "" {
		return []EntityRelationRow{}, nil
	}
	args := make([]any, 0, 3+len(visibleArgs)+2)
	args = append(args, entityID, relationName, relationName)
	args = append(args, visibleArgs...)
	args = append(args, limit, offset)
	// #nosec G202 -- interpolated fragments are internally generated placeholders and closed certification SQL.
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT r.relation, r.attributes, r.sort_order,
		       e.id, e.library_id, e.kind, e.title,
		       m.id, m.library_id, m.title, m.summary, m.year,
		       m.duration_seconds, m.container, m.codec, m.video_width,
		       m.video_height, m.media_class, m.mutated_by
		FROM entity_relation r
		LEFT JOIN entity e ON e.id = r.to_entity_id AND e.trashed_at_ms IS NULL
		LEFT JOIN media m ON m.id = r.to_media_id AND m.trashed_at_ms IS NULL
		WHERE r.from_entity_id = ? AND (? = '' OR r.relation = ?)
		  AND `+visible+`
		ORDER BY r.relation, (r.sort_order IS NULL), r.sort_order, r.to_entity_id, r.to_media_id
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EntityRelationRow{}
	for rows.Next() {
		var (
			item        EntityRelationRow
			attributes  sql.NullString
			sortOrder   sql.NullInt64
			entityID    sql.NullString
			entityLib   sql.NullString
			entityKind  sql.NullString
			entityTitle sql.NullString
			mediaID     sql.NullString
			mediaLib    sql.NullString
			mediaTitle  sql.NullString
			summary     sql.NullString
			year        sql.NullInt64
			duration    sql.NullInt64
			container   sql.NullString
			codec       sql.NullString
			width       sql.NullInt64
			height      sql.NullInt64
			class       sql.NullString
			mutatedBy   sql.NullString
		)
		if err := rows.Scan(&item.Name, &attributes, &sortOrder,
			&entityID, &entityLib, &entityKind, &entityTitle,
			&mediaID, &mediaLib, &mediaTitle, &summary, &year,
			&duration, &container, &codec, &width, &height, &class, &mutatedBy); err != nil {
			return nil, err
		}
		if decoded, err := decodeRelationAttributes(attributes); err != nil {
			return nil, err
		} else {
			item.Attributes = decoded
		}
		if sortOrder.Valid {
			v := int(sortOrder.Int64)
			item.SortOrder = &v
		}
		if entityID.Valid {
			item.ToEntity = &Entity{
				ID: EntityID(entityID.String), LibraryID: LibraryID(entityLib.String),
				Kind: entityKind.String, Title: entityTitle.String,
			}
			out = append(out, item)
			continue
		}
		if mediaID.Valid {
			item.ToMedia = &Media{
				ID: MediaID(mediaID.String), LibraryID: LibraryID(mediaLib.String),
				Title: mediaTitle.String, Summary: summary.String, Year: int(year.Int64),
				DurationSeconds: int(duration.Int64), Container: container.String,
				Codec: codec.String, VideoWidth: int(width.Int64), VideoHeight: int(height.Int64),
				Class: MediaClass(class.String), MutatedBy: mutationSourceFromDB(mutatedBy.String),
			}
			out = append(out, item)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	entityIndexes := map[EntityID][]int{}
	entityIDs := []EntityID{}
	for i := range out {
		if out[i].ToEntity == nil {
			continue
		}
		id := out[i].ToEntity.ID
		if len(entityIndexes[id]) == 0 {
			entityIDs = append(entityIDs, id)
		}
		entityIndexes[id] = append(entityIndexes[id], i)
	}
	if err := s.loadEntityFieldsFrom(ctx, plane.reader, entityIDs, func(id EntityID, field EntityField) {
		for _, i := range entityIndexes[id] {
			out[i].ToEntity.Fields = append(out[i].ToEntity.Fields, field)
		}
	}); err != nil {
		return nil, err
	}
	for _, indexes := range entityIndexes {
		for _, i := range indexes {
			if out[i].ToEntity.Fields == nil {
				out[i].ToEntity.Fields = []EntityField{}
			}
			sortEntityFields(out[i].ToEntity.Fields)
		}
	}
	return out, nil
}

// MediaRelationName is the fixed Core vocabulary of Media->Media relation
// shapes. Like the entity-relation and archetype vocabularies
// (invariant #3) these are named by SHAPE, never by domain: a plugin maps its
// domain label ("Director's Cut", "Trailer") onto one of these shapes — Core
// stores only the shape.
type MediaRelationName string

const (
	// MediaRelationEditionOf groups separate cuts of one work: a cut Media is
	// an edition-of one of its sibling cuts, and they group together on the
	// detail screen. Each cut stays its own Media with its own engagement and
	// markers (their timelines differ — ADR-0009 §2, CONTEXT.md "Edition").
	MediaRelationEditionOf MediaRelationName = "edition-of"
	// MediaRelationExtraOf links an Extra (a typed child Media) to its parent
	// Media. The child also carries the generic media.extra bit, which is what
	// suppresses it from the default discovery surfaces; this relation is the
	// "extras of X" grouping read (ADR-0009 §3, CONTEXT.md "Extra").
	MediaRelationExtraOf MediaRelationName = "extra-of"
)

func validMediaRelationName(name MediaRelationName) bool {
	switch name {
	case MediaRelationEditionOf, MediaRelationExtraOf:
		return true
	default:
		return false
	}
}

// LinkMediaRelation records one Media->Media relation row,
// idempotent for the same (from, relation, to). Both Media must exist and
// differ. Server-internal like UpsertEntityRelation: scan and identification
// call it; reads (MediaRelationsOf) are reveal-filtered, this write is not. The
// relation name must be a known Core shape.
func (s *Store) LinkMediaRelation(ctx context.Context, fromMediaID MediaID, relation MediaRelationName, toMediaID MediaID) error {
	if fromMediaID == "" || toMediaID == "" {
		return errors.New("both media IDs are required")
	}
	if fromMediaID == toMediaID {
		return errors.New("a media relation cannot link a media item to itself")
	}
	if !validMediaRelationName(relation) {
		return errors.New("unknown media relation " + string(relation))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	fromPlane, err := s.catalogPlaneForMediaLocked(ctx, fromMediaID)
	if err != nil {
		return err
	}
	toPlane, err := s.catalogPlaneForMediaLocked(ctx, toMediaID)
	if err != nil {
		return err
	}
	if fromPlane.encrypted || toPlane.encrypted {
		if !fromPlane.encrypted || !toPlane.encrypted || fromPlane.libraryID != toPlane.libraryID {
			return errors.New("encrypted cross-library media relations are not supported")
		}
	}
	_, err = fromPlane.writer.ExecContext(ctx, `
		INSERT INTO media_relation(from_media_id, relation, to_media_id)
		VALUES (?, ?, ?)
		ON CONFLICT(from_media_id, relation, to_media_id) DO NOTHING
	`, fromMediaID, relation, toMediaID)
	return err
}

// MediaRelationsOf returns the visible Media that point AT the given Media under
// the named relation — the reverse-lookup read. For MediaRelationExtraOf with
// the parent as the target it returns the parent's Extras (each is directly
// addressable here even though it is suppressed from the default browse
// Sources); for MediaRelationEditionOf it returns the sibling cuts grouped on
// the detail screen. Results are reveal-filtered: a related Media in a Library
// not visible under the filter is omitted. Graph callers supply a bounded
// page; omission preserves the internal all-rows compatibility path.
func (s *Store) MediaRelationsOf(ctx context.Context, toMediaID MediaID, relation MediaRelationName, filter LibraryFilter, page ...int) ([]Media, error) {
	if toMediaID == "" {
		return nil, errors.New("media ID is required")
	}
	if !validMediaRelationName(relation) {
		return nil, errors.New("unknown media relation " + string(relation))
	}
	limit, offset := boundedReadPage(page)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, toMediaID)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, toMediaID, filter); err != nil {
		return nil, err
	}
	visibleLibraries, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(visibleLibraries) == 0 {
		return []Media{}, nil
	}
	libraries, libraryArgs := libraryIDPlaceholders(visibleLibraries)
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	if library, encrypted, err := s.encryptedLibraryForReaderLocked(ctx, plane.reader); err != nil {
		return nil, err
	} else if encrypted {
		pluginID := ""
		if library.PluginID != nil {
			pluginID = *library.PluginID
		}
		certClause, certArgs = planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	}
	args := make([]any, 0, 2+len(libraryArgs)+len(certArgs)+2)
	args = append(args, toMediaID, relation)
	args = append(args, libraryArgs...)
	args = append(args, certArgs...)
	args = append(args, limit, offset)
	// #nosec G202 -- interpolated fragments are internally generated placeholders and closed certification SQL.
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT m.id, m.library_id, m.title, m.summary, m.year, m.duration_seconds,
		       m.container, m.codec, m.video_width, m.video_height, m.media_class, m.mutated_by
		FROM media_relation r
		JOIN media m ON m.id = r.from_media_id
		WHERE r.to_media_id = ? AND r.relation = ? AND m.trashed_at_ms IS NULL
		  AND m.library_id IN (`+libraries+`)`+certClause+`
		ORDER BY m.title COLLATE NOCASE, m.id
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Media{}
	for rows.Next() {
		var media Media
		var summary, mutatedBy sql.NullString
		var year sql.NullInt64
		if err := rows.Scan(&media.ID, &media.LibraryID, &media.Title, &summary, &year,
			&media.DurationSeconds, &media.Container, &media.Codec, &media.VideoWidth,
			&media.VideoHeight, &media.Class, &mutatedBy); err != nil {
			return nil, err
		}
		media.Summary = summary.String
		media.Year = int(year.Int64)
		media.MutatedBy = mutationSourceFromDB(mutatedBy.String)
		out = append(out, media)
	}
	return out, rows.Err()
}

const (
	defaultBoundedReadLimit = 50
	maxBoundedReadLimit     = 200
	maxBoundedReadOffset    = 10_000
)

func boundedReadPage(page []int) (int, int) {
	if len(page) == 0 {
		return -1, 0
	}
	limit := defaultBoundedReadLimit
	offset := 0
	if len(page) > 0 && page[0] > 0 {
		limit = min(page[0], maxBoundedReadLimit)
	}
	if len(page) > 1 && page[1] > 0 {
		offset = min(page[1], maxBoundedReadOffset)
	}
	return limit, offset
}

func (s *Store) visibleLibraryIDsLocked(ctx context.Context, filter LibraryFilter) ([]LibraryID, error) {
	return visibleLibraryIDsFrom(ctx, s.catalogR, filter)
}

// visibleLibraryIDsFrom reads the visible set through q, so a write can make
// the check inside a catalog writer transaction.
func visibleLibraryIDsFrom(ctx context.Context, q rowsQueryer, filter LibraryFilter) ([]LibraryID, error) {
	libraries, err := loadLibraries(ctx, q)
	if err != nil {
		return nil, err
	}
	visible := make([]LibraryID, 0, len(libraries))
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	return visible, nil
}

// relationWritePlaneLocked returns the plane that holds a relation's rows (its
// source entity's), refusing an encrypted relation that crosses libraries.
func (s *Store) relationWritePlaneLocked(ctx context.Context, relation EntityRelation) (catalogPlane, error) {
	fromPlane, err := s.catalogPlaneForEntityLocked(ctx, relation.FromEntityID)
	if err != nil {
		return catalogPlane{}, err
	}
	var toPlane catalogPlane
	if relation.ToEntityID != "" {
		toPlane, err = s.catalogPlaneForEntityLocked(ctx, relation.ToEntityID)
	} else {
		toPlane, err = s.catalogPlaneForMediaLocked(ctx, relation.ToMediaID)
	}
	if err != nil {
		return catalogPlane{}, err
	}
	if fromPlane.encrypted || toPlane.encrypted {
		if !fromPlane.encrypted || !toPlane.encrypted || fromPlane.libraryID != toPlane.libraryID {
			return catalogPlane{}, errors.New("encrypted cross-library entity relations are not supported")
		}
	}
	return fromPlane, nil
}

// entityRelationVisibleSQLLocked returns the condition, over entity_relation r
// LEFT JOINed to live entity e and media m, that keeps the rows whose target
// the filter can see, with its arguments. It returns "" when the filter sees no
// library at all.
func (s *Store) entityRelationVisibleSQLLocked(ctx context.Context, plane catalogPlane, filter LibraryFilter) (string, []any, error) {
	visibleLibraries, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil || len(visibleLibraries) == 0 {
		return "", nil, err
	}
	entityLibraries, entityLibraryArgs := libraryIDPlaceholders(visibleLibraries)
	mediaLibraries, mediaLibraryArgs := libraryIDPlaceholders(visibleLibraries)
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	if library, encrypted, err := s.encryptedLibraryForReaderLocked(ctx, plane.reader); err != nil {
		return "", nil, err
	} else if encrypted {
		pluginID := ""
		if library.PluginID != nil {
			pluginID = *library.PluginID
		}
		certClause, certArgs = planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	}
	args := make([]any, 0, len(entityLibraryArgs)+len(mediaLibraryArgs)+len(certArgs))
	args = append(args, entityLibraryArgs...)
	args = append(args, mediaLibraryArgs...)
	args = append(args, certArgs...)
	return `((e.id IS NOT NULL AND e.library_id IN (` + entityLibraries + `))
		    OR (m.id IS NOT NULL AND m.library_id IN (` + mediaLibraries + `)` + certClause + `))`, args, nil
}

// PlaceEntityRelation puts one relation row at a position among the rows of
// its relation that the filter can see, adding the row when it is new, and
// renumbers the visible rows in one transaction. Rows the filter hides are
// not rewritten; a position at or past the visible count places the row after
// the last visible row. Existing rows keep
// their attributes; only a new row takes relation.Attributes (CORE-2913).
func (s *Store) PlaceEntityRelation(ctx context.Context, relation EntityRelation, position int, filter LibraryFilter) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if relation.FromEntityID == "" || relation.Name == "" {
		return errors.New("relation source entity and name are required")
	}
	if (relation.ToEntityID == "") == (relation.ToMediaID == "") {
		return errors.New("relation requires exactly one target")
	}
	if position < 0 {
		return errors.New("relation position must not be negative")
	}
	attributes, _, err := entityRelationRowValues(relation)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.relationWritePlaneLocked(ctx, relation)
	if err != nil {
		return err
	}
	visible, visibleArgs, err := s.entityRelationVisibleSQLLocked(ctx, plane, filter)
	if err != nil {
		return err
	}
	if visible == "" {
		visible = "0"
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Recheck inside the transaction that the filter still sees the source
	// and the target: one hidden or trashed since the resolver's check must
	// not be moved.
	seen := func(toEntity, toMedia string) (bool, error) {
		var ok bool
		seenArgs := append(append([]any{}, visibleArgs...), nullableString(toEntity), nullableString(toMedia))
		// #nosec G202 -- interpolated fragments are internally generated placeholders and closed certification SQL.
		err := tx.QueryRowContext(ctx, `
			SELECT `+visible+`
			FROM (SELECT ? AS to_entity_id, ? AS to_media_id) r
			LEFT JOIN entity e ON e.id = r.to_entity_id AND e.trashed_at_ms IS NULL
			LEFT JOIN media m ON m.id = r.to_media_id AND m.trashed_at_ms IS NULL
		`, seenArgs...).Scan(&ok)
		return ok, err
	}
	for _, target := range [][2]string{
		{string(relation.FromEntityID), ""},
		{string(relation.ToEntityID), string(relation.ToMediaID)},
	} {
		if ok, err := seen(target[0], target[1]); err != nil {
			return err
		} else if !ok {
			return errVisibleEntityNotFound
		}
	}
	type placed struct {
		toEntity, toMedia sql.NullString
		sortOrder         sql.NullInt64
		visible           bool
	}
	args := append([]any{}, visibleArgs...)
	args = append(args, relation.FromEntityID, relation.Name)
	// #nosec G202 -- interpolated fragments are internally generated placeholders and closed certification SQL.
	rows, err := tx.QueryContext(ctx, `
		SELECT r.to_entity_id, r.to_media_id, r.sort_order, `+visible+`
		FROM entity_relation r
		LEFT JOIN entity e ON e.id = r.to_entity_id AND e.trashed_at_ms IS NULL
		LEFT JOIN media m ON m.id = r.to_media_id AND m.trashed_at_ms IS NULL
		WHERE r.from_entity_id = ? AND r.relation = ?
		ORDER BY (r.sort_order IS NULL), r.sort_order, r.to_entity_id, r.to_media_id
	`, args...)
	if err != nil {
		return err
	}
	var order []placed
	var target sql.NullInt64
	for rows.Next() {
		var row placed
		if err := rows.Scan(&row.toEntity, &row.toMedia, &row.sortOrder, &row.visible); err != nil {
			_ = rows.Close()
			return err
		}
		if (relation.ToEntityID != "" && row.toEntity.String == string(relation.ToEntityID)) ||
			(relation.ToMediaID != "" && row.toMedia.String == string(relation.ToMediaID)) {
			target = row.sortOrder
			continue
		}
		order = append(order, row)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// Renumber only the rows the filter sees: they take back the numbers they
	// already hold, made strictly increasing, in their new order, and rows
	// without a number (the placed row when new) take the next ones. Hidden
	// rows keep their numbers, so nothing written counts them (CORE-2913).
	var visibleRows []placed
	var slots []int64
	if target.Valid {
		slots = append(slots, target.Int64)
	}
	for _, row := range order {
		if !row.visible {
			continue
		}
		visibleRows = append(visibleRows, row)
		if row.sortOrder.Valid {
			slots = append(slots, row.sortOrder.Int64)
		}
	}
	at := min(position, len(visibleRows))
	placedRow := placed{
		toEntity:  sql.NullString{String: string(relation.ToEntityID), Valid: relation.ToEntityID != ""},
		toMedia:   sql.NullString{String: string(relation.ToMediaID), Valid: relation.ToMediaID != ""},
		sortOrder: target,
	}
	visibleRows = append(visibleRows[:at], append([]placed{placedRow}, visibleRows[at:]...)...)
	slices.Sort(slots)
	if len(slots) > 0 && slots[len(slots)-1] > math.MaxInt32 {
		return errors.New("relation sort order is too large to place rows after it")
	}
	for len(slots) < len(visibleRows) {
		next := int64(0)
		if len(slots) > 0 {
			next = slots[len(slots)-1] + 1
		}
		slots = append(slots, next)
	}
	for i := 1; i < len(slots); i++ {
		slots[i] = max(slots[i], slots[i-1]+1)
	}
	var slot int64
	for i, row := range visibleRows {
		if i == at {
			slot = slots[i]
			continue
		}
		if row.sortOrder.Valid && row.sortOrder.Int64 == slots[i] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE entity_relation SET sort_order = ?
			WHERE from_entity_id = ? AND relation = ?
			  AND to_entity_id IS ? AND to_media_id IS ?
		`, slots[i], relation.FromEntityID, relation.Name, row.toEntity, row.toMedia); err != nil {
			return err
		}
	}
	relation.KeepAttributes = true
	if relation.ToEntityID != "" {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO entity_relation(from_entity_id, relation, to_entity_id, attributes, sort_order)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(from_entity_id, relation, to_entity_id) WHERE to_entity_id IS NOT NULL
			DO UPDATE SET sort_order = excluded.sort_order
		`, relation.FromEntityID, relation.Name, relation.ToEntityID, attributes, slot)
	} else {
		err = insertMediaRelationTx(ctx, tx, relation, attributes, slot)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func libraryIDPlaceholders(ids []LibraryID) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return strings.Join(placeholders, ","), args
}

// MediaFavorites resolves a page of already-visible Media IDs against one
// Profile's favorite set in bounded batches. Callers own visibility; this is
// only the cross-database hydration step used after a catalog page is fixed.
func (s *Store) MediaFavorites(ctx context.Context, profileID ProfileID, ids []MediaID) (map[MediaID]bool, error) {
	favorites := make(map[MediaID]bool, len(ids))
	if len(ids) == 0 {
		return favorites, nil
	}
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	for _, batch := range mediaIDBatches(ids, entityIDBatchSize) {
		placeholders, idArgs := mediaIDPlaceholders(batch)
		args := make([]any, 0, 1+len(idArgs))
		args = append(args, profileID)
		args = append(args, idArgs...)
		// #nosec G202 -- placeholders is generated solely from the bounded ID batch length.
		rows, err := s.eventsR.QueryContext(ctx, `
			SELECT media_id FROM media_favorite
			WHERE profile_id = ? AND media_id IN (`+placeholders+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			if err := rows.Scan(&id); err != nil {
				return nil, errors.Join(err, rows.Close())
			}
			favorites[id] = true
		}
		if err := rows.Err(); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return favorites, nil
}

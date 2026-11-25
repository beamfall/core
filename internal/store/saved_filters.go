package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrSavedFilterNotFound means no SavedFilter exists with the given ID.
var ErrSavedFilterNotFound = errors.New("saved filter not found")

// ErrSmartCollectionNotFound means no SmartCollection exists with the given ID.
var ErrSmartCollectionNotFound = errors.New("smart collection not found")

// SavedFilterID is a stable SavedFilter identifier.
type SavedFilterID string

// SmartCollectionID is a stable SmartCollection identifier.
type SmartCollectionID string

// SavedFilter is a named, per-Profile, per-Library declarative filter.
// FilterJSON is an opaque client expression stored and
// returned verbatim — the server never interprets it.
type SavedFilter struct {
	ID         SavedFilterID
	ProfileID  ProfileID
	LibraryID  LibraryID
	Name       string
	FilterJSON string
	Pinned     bool
	CreatedAt  time.Time
}

// SmartCollection is a Library-level Collection whose membership is a
// persisted Source expression evaluated live on each request.
// SourceExpr is opaque client JSON stored and returned verbatim.
type SmartCollection struct {
	ID         SmartCollectionID
	LibraryID  LibraryID
	Name       string
	SourceExpr string
	SortExpr   string
	// CreatedByProfile records which Profile authored this Collection (ADR-0036
	// §2.4). Set at create time so My-Data export surfaces the Profile's
	// authored (per-Profile) Collections and My-Data delete DISOWNs them (clears
	// the column) rather than deleting a shared row. Empty for operator-created
	// or legacy Collections, which have no authoring Profile.
	CreatedByProfile ProfileID
	CreatedAt        time.Time
}

// savedFilterColumns and smartCollectionColumns are the read shapes shared by
// catalog.db and an Encrypted Library's protected plane (CORE-0769).
const (
	savedFilterColumns     = `id, profile_id, library_id, name, filter_json, pinned, created_at_ms`
	smartCollectionColumns = `id, library_id, name, source_expr, sort_expr, created_at_ms`
)

func scanSavedFilter(row rowScanner) (SavedFilter, error) {
	var f SavedFilter
	var pinnedInt int
	var createdAtMS int64
	if err := row.Scan(&f.ID, &f.ProfileID, &f.LibraryID, &f.Name, &f.FilterJSON, &pinnedInt, &createdAtMS); err != nil {
		return SavedFilter{}, err
	}
	f.Pinned = pinnedInt != 0
	f.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	return f, nil
}

func scanSmartCollection(row rowScanner) (SmartCollection, error) {
	var c SmartCollection
	var createdAtMS int64
	if err := row.Scan(&c.ID, &c.LibraryID, &c.Name, &c.SourceExpr, &c.SortExpr, &createdAtMS); err != nil {
		return SmartCollection{}, err
	}
	c.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	return c, nil
}

// SavedFilters returns the Profile's saved filters for a Library, newest first.
// An Encrypted Library's filters live in its protected plane; while it is
// Sealed the list is empty.
func (s *Store) SavedFilters(ctx context.Context, profileID ProfileID, libraryID LibraryID) ([]SavedFilter, error) {
	if profileID == "" || libraryID == "" {
		return nil, ValidationError("profile ID and library ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	filters := []SavedFilter{}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		return filters, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT `+savedFilterColumns+`
		FROM saved_filter
		WHERE profile_id = ? AND library_id = ?
		ORDER BY created_at_ms DESC
	`, profileID, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		f, err := scanSavedFilter(rows)
		if err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
	return filters, rows.Err()
}

// CreateSavedFilter inserts a new SavedFilter for a Profile+Library pair. The
// Library must exist. Name is trimmed; empty name is rejected. An Encrypted
// Library's filter is written to its protected plane, and creating one while
// the Library is Sealed fails with ErrEncryptedLibraryDBDisabled.
func (s *Store) CreateSavedFilter(ctx context.Context, f SavedFilter, now time.Time) (SavedFilter, error) {
	f.Name = strings.TrimSpace(f.Name)
	if f.ProfileID == "" {
		return SavedFilter{}, ValidationError("profile ID is required")
	}
	if f.LibraryID == "" {
		return SavedFilter{}, ValidationError("library ID is required")
	}
	if f.Name == "" {
		return SavedFilter{}, ValidationError("name is required")
	}
	if f.FilterJSON == "" {
		f.FilterJSON = "{}"
	}
	id, err := randomSavedFilterID()
	if err != nil {
		return SavedFilter{}, err
	}
	f.ID = id
	f.CreatedAt = now.UTC()
	createdAtMS := f.CreatedAt.UnixMilli()
	pinnedInt := 0
	if f.Pinned {
		pinnedInt = 1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SavedFilter{}, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, f.LibraryID)
	if err != nil {
		return SavedFilter{}, err
	}
	if plane.encrypted {
		if err := s.requireProfileLocked(ctx, f.ProfileID); err != nil {
			return SavedFilter{}, err
		}
	}
	_, err = plane.writer.ExecContext(ctx, `
		INSERT INTO saved_filter (id, profile_id, library_id, name, filter_json, pinned, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, f.ID, f.ProfileID, f.LibraryID, f.Name, f.FilterJSON, pinnedInt, createdAtMS)
	if err != nil {
		return SavedFilter{}, err
	}
	return f, nil
}

// SavedFilterByID fetches a SavedFilter by its ID without Profile restriction.
// Used by the graph layer to check ownership before deletion.
func (s *Store) SavedFilterByID(ctx context.Context, id SavedFilterID) (SavedFilter, error) {
	if id == "" {
		return SavedFilter{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SavedFilter{}, err
	}
	_, f, err := s.savedFilterLocked(ctx, id)
	return f, err
}

func (s *Store) savedFilterLocked(ctx context.Context, id SavedFilterID) (catalogPlane, SavedFilter, error) {
	plane, err := s.expressionPlaneLocked(ctx, "saved_filter", string(id), ErrSavedFilterNotFound)
	if err != nil {
		return catalogPlane{}, SavedFilter{}, err
	}
	f, err := scanSavedFilter(plane.reader.QueryRowContext(ctx, `SELECT `+savedFilterColumns+` FROM saved_filter WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return catalogPlane{}, SavedFilter{}, ErrSavedFilterNotFound
	}
	return plane, f, err
}

// DeleteSavedFilter removes a SavedFilter by ID and returns the deleted row.
func (s *Store) DeleteSavedFilter(ctx context.Context, id SavedFilterID) (SavedFilter, error) {
	if id == "" {
		return SavedFilter{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SavedFilter{}, err
	}
	plane, f, err := s.savedFilterLocked(ctx, id)
	if err != nil {
		return SavedFilter{}, err
	}
	if _, err := plane.writer.ExecContext(ctx, `DELETE FROM saved_filter WHERE id = ?`, id); err != nil {
		return SavedFilter{}, err
	}
	return f, nil
}

// SmartCollections returns all SmartCollections for a Library, newest first.
// An Encrypted Library's smart Collections live in its protected plane; while
// it is Sealed the list is empty.
func (s *Store) SmartCollections(ctx context.Context, libraryID LibraryID) ([]SmartCollection, error) {
	if libraryID == "" {
		return nil, ValidationError("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	collections := []SmartCollection{}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		return collections, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT `+smartCollectionColumns+`
		FROM smart_collection
		WHERE library_id = ?
		ORDER BY created_at_ms DESC
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanSmartCollection(rows)
		if err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

// CreateSmartCollection inserts a new SmartCollection for a Library. The
// Library must exist. Name is trimmed; empty name is rejected. An Encrypted
// Library's smart Collection is written to its protected plane, and creating
// one while the Library is Sealed fails with ErrEncryptedLibraryDBDisabled.
func (s *Store) CreateSmartCollection(ctx context.Context, c SmartCollection, now time.Time) (SmartCollection, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.LibraryID == "" {
		return SmartCollection{}, ValidationError("library ID is required")
	}
	if c.Name == "" {
		return SmartCollection{}, ValidationError("name is required")
	}
	if c.SourceExpr == "" {
		c.SourceExpr = "{}"
	}
	if c.SortExpr == "" {
		c.SortExpr = "{}"
	}
	if err := validateSmartRuleExprs(c.SourceExpr, c.SortExpr); err != nil {
		return SmartCollection{}, err
	}
	id, err := randomSmartCollectionID()
	if err != nil {
		return SmartCollection{}, err
	}
	c.ID = id
	c.CreatedAt = now.UTC()
	createdAtMS := c.CreatedAt.UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SmartCollection{}, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, c.LibraryID)
	if err != nil {
		return SmartCollection{}, err
	}
	if plane.encrypted && c.CreatedByProfile != "" {
		if err := s.requireProfileLocked(ctx, c.CreatedByProfile); err != nil {
			return SmartCollection{}, err
		}
	}
	_, err = plane.writer.ExecContext(ctx, `
		INSERT INTO smart_collection (id, library_id, name, source_expr, sort_expr, created_by_profile_id, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, c.ID, c.LibraryID, c.Name, c.SourceExpr, c.SortExpr, nullString(string(c.CreatedByProfile)), createdAtMS)
	if err != nil {
		return SmartCollection{}, err
	}
	return c, nil
}

// SmartCollectionByID fetches a SmartCollection by its ID. Used by the graph
// layer to check library visibility before deletion.
func (s *Store) SmartCollectionByID(ctx context.Context, id SmartCollectionID) (SmartCollection, error) {
	if id == "" {
		return SmartCollection{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SmartCollection{}, err
	}
	_, c, err := s.smartCollectionLocked(ctx, id)
	return c, err
}

func (s *Store) smartCollectionLocked(ctx context.Context, id SmartCollectionID) (catalogPlane, SmartCollection, error) {
	plane, err := s.expressionPlaneLocked(ctx, "smart_collection", string(id), ErrSmartCollectionNotFound)
	if err != nil {
		return catalogPlane{}, SmartCollection{}, err
	}
	c, err := scanSmartCollection(plane.reader.QueryRowContext(ctx, `SELECT `+smartCollectionColumns+` FROM smart_collection WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return catalogPlane{}, SmartCollection{}, ErrSmartCollectionNotFound
	}
	return plane, c, err
}

// UpdateSmartCollection updates editable SmartCollection metadata and source
// expressions for the Source editor.
func (s *Store) UpdateSmartCollection(ctx context.Context, c SmartCollection) (SmartCollection, error) {
	if c.ID == "" {
		return SmartCollection{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SmartCollection{}, err
	}
	plane, existing, err := s.smartCollectionLocked(ctx, c.ID)
	if err != nil {
		return SmartCollection{}, err
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = existing.Name
	}
	sourceExpr := c.SourceExpr
	if sourceExpr == "" {
		sourceExpr = existing.SourceExpr
	}
	sortExpr := c.SortExpr
	if sortExpr == "" {
		sortExpr = existing.SortExpr
	}
	if err := validateSmartRuleExprs(sourceExpr, sortExpr); err != nil {
		return SmartCollection{}, err
	}
	if _, err := plane.writer.ExecContext(ctx, `
		UPDATE smart_collection SET name = ?, source_expr = ?, sort_expr = ? WHERE id = ?
	`, name, sourceExpr, sortExpr, c.ID); err != nil {
		return SmartCollection{}, err
	}
	c.LibraryID = existing.LibraryID
	c.Name = name
	c.SourceExpr = sourceExpr
	c.SortExpr = sortExpr
	c.CreatedByProfile = existing.CreatedByProfile
	c.CreatedAt = existing.CreatedAt
	return c, nil
}

// DeleteSmartCollection removes a SmartCollection by ID and returns the
// deleted row.
func (s *Store) DeleteSmartCollection(ctx context.Context, id SmartCollectionID) (SmartCollection, error) {
	if id == "" {
		return SmartCollection{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SmartCollection{}, err
	}
	plane, c, err := s.smartCollectionLocked(ctx, id)
	if err != nil {
		return SmartCollection{}, err
	}
	if _, err := plane.writer.ExecContext(ctx, `DELETE FROM smart_collection WHERE id = ?`, id); err != nil {
		return SmartCollection{}, err
	}
	return c, nil
}

func randomSavedFilterID() (SavedFilterID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return SavedFilterID("filter-" + hex.EncodeToString(raw[:])), nil
}

func randomSmartCollectionID() (SmartCollectionID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return SmartCollectionID("collection-" + hex.EncodeToString(raw[:])), nil
}

func decodeSmartCollectionSourceExpr(expr string) (SourceQuery, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return SourceQuery{Kind: "all-items"}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(expr), &raw); err != nil {
		return SourceQuery{}, ValidationError("sourceExpr is not valid JSON")
	}
	query := SourceQuery{Kind: stringFromRaw(raw["kind"])}
	if query.Kind == "" {
		query.Kind = "all-items"
	}
	if query.Kind == "smart-collection" {
		return SourceQuery{}, ValidationError("smart collection source cannot reference another smart collection")
	}
	if query.Kind == smartRuleSourceKind {
		// Rule sources evaluate only through smartRuleItemsLocked; never let
		// them fall through as an unknown kind.
		return SourceQuery{}, ValidationError("rule sources are evaluated by their smart collection")
	}
	query.EntityKind = stringFromRaw(raw["entityKind"])
	query.EntityID = EntityID(stringFromRaw(raw["entityID"]))
	query.LibraryID = LibraryID(stringFromRaw(raw["libraryID"]))
	query.Seed = stringFromRaw(raw["seed"])
	query.Value = strings.TrimSpace(stringFromRaw(raw["value"]))
	if fields, ok := raw["fieldFilters"].(map[string]any); ok {
		query.FieldFilters = fields
	}
	if entityRaw, ok := raw["entityFilter"].(map[string]any); ok {
		kind := strings.TrimSpace(stringFromRaw(entityRaw["kind"]))
		name := strings.TrimSpace(stringFromRaw(entityRaw["name"]))
		if kind == "" || name == "" {
			return SourceQuery{}, ValidationError("entityFilter requires kind and name")
		}
		query.EntityFilter = &EntityFilter{Kind: kind, Name: name}
	}
	return query, nil
}

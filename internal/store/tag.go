package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

// TagID is a stable, first-class Core Tag identifier.
type TagID string

// TagTargetKind is the closed set of assignable Tag targets.
type TagTargetKind string

const (
	TagTargetMedia  TagTargetKind = "media"
	TagTargetEntity TagTargetKind = "entity"
	TagTargetMarker TagTargetKind = "marker"
)

// TagBulkMode is the closed set of selected-target bulk assignment behaviors.
type TagBulkMode string

const (
	TagBulkAdd     TagBulkMode = "add"
	TagBulkRemove  TagBulkMode = "remove"
	TagBulkReplace TagBulkMode = "replace"
)

// Tag is a freeform label attachable to Media, Entities, and Markers. ParentID
// is optional and creates a same-table hierarchy edge. AssignmentCount is the
// live total of assignments across all target kinds (media + entity + marker);
// it is populated by Tags() for the manager tree and left zero elsewhere.
type Tag struct {
	ID              TagID
	Name            string
	ParentID        TagID
	TrashedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	AssignmentCount int
}

// TagAssignment is one target's current first-class Tag set.
type TagAssignment struct {
	TargetKind TagTargetKind
	TargetID   string
	LibraryID  LibraryID
	Tags       []Tag
}

// TagBulkAssignmentResult summarizes a selected-target bulk Tag mutation.
type TagBulkAssignmentResult struct {
	Affected int
	Tags     []Tag
}

func validateTagName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ValidationError("tag name is required")
	}
	return name, nil
}

// Tags lists first-class Tags, sorted by lowercased name. Trashed Tags are
// omitted unless includeTrashed is true.
func (s *Store) Tags(ctx context.Context, includeTrashed bool) ([]Tag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	where := "WHERE trashed_at_ms IS NULL"
	if includeTrashed {
		where = ""
	}
	// AssignmentCount is the live total across all three target kinds. The
	// correlated sub-selects keep it a single statement (the manager tree is
	// small); per-kind tables are the same ones tagAssignmentTable maps.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, name, parent_tag_id, trashed_at_ms, created_at_ms, updated_at_ms,
			(SELECT COUNT(*) FROM media_tag mt WHERE mt.tag_id = tag.id)
			+ (SELECT COUNT(*) FROM entity_tag et WHERE et.tag_id = tag.id)
			+ (SELECT COUNT(*) FROM marker_core_tag kt WHERE kt.tag_id = tag.id) AS assignment_count
		FROM tag `+where+`
		ORDER BY lower(name), id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []Tag{}
	for rows.Next() {
		tag, err := scanTagWithCount(rows.Scan)
		if err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// CreateTag inserts one live Tag.
func (s *Store) CreateTag(ctx context.Context, name string, parentID TagID, now time.Time) (Tag, error) {
	name, err := validateTagName(name)
	if err != nil {
		return Tag{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Tag{}, err
	}
	if parentID != "" {
		if _, err := s.liveTagSQL(ctx, parentID); err != nil {
			return Tag{}, err
		}
	}
	id, err := randomTagID()
	if err != nil {
		return Tag{}, err
	}
	tag := Tag{ID: id, Name: name, ParentID: parentID, CreatedAt: normalizeTime(now), UpdatedAt: normalizeTime(now)}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO tag(id, name, parent_tag_id, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?)
	`, tag.ID, tag.Name, nullString(string(tag.ParentID)), timeToMillis(tag.CreatedAt), timeToMillis(tag.UpdatedAt))
	if err != nil {
		if isUniqueConstraint(err) {
			return Tag{}, ValidationError("tag name already exists")
		}
		return Tag{}, err
	}
	return tag, nil
}

// UpdateTag renames and reparents one live Tag. parentID == "" clears the
// parent; cycles are rejected before write.
func (s *Store) UpdateTag(ctx context.Context, id TagID, name string, parentID TagID, now time.Time) (Tag, error) {
	if id == "" {
		return Tag{}, ValidationError("tag id is required")
	}
	name, err := validateTagName(name)
	if err != nil {
		return Tag{}, err
	}
	if parentID == id {
		return Tag{}, ValidationError("tag cannot parent itself")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Tag{}, err
	}
	if _, err := s.liveTagSQL(ctx, id); err != nil {
		return Tag{}, err
	}
	if parentID != "" {
		if _, err := s.liveTagSQL(ctx, parentID); err != nil {
			return Tag{}, err
		}
		if err := s.checkTagParentCycle(ctx, id, parentID); err != nil {
			return Tag{}, err
		}
	}
	updatedAt := normalizeTime(now)
	_, err = s.catalogW.ExecContext(ctx, `
		UPDATE tag SET name = ?, parent_tag_id = ?, updated_at_ms = ?
		WHERE id = ? AND trashed_at_ms IS NULL
	`, name, nullString(string(parentID)), timeToMillis(updatedAt), id)
	if err != nil {
		if isUniqueConstraint(err) {
			return Tag{}, ValidationError("tag name already exists")
		}
		return Tag{}, err
	}
	return s.liveTagSQL(ctx, id)
}

// DeleteTag soft-deletes one live Tag; assignments stay in place so restore
// makes the Tag meaningful again.
func (s *Store) DeleteTag(ctx context.Context, id TagID, now time.Time) (Tag, error) {
	if id == "" {
		return Tag{}, ValidationError("tag id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Tag{}, err
	}
	tag, err := s.liveTagSQL(ctx, id)
	if err != nil {
		return Tag{}, err
	}
	stamp := normalizeTime(now)
	if _, err := s.catalogW.ExecContext(ctx, `
		UPDATE tag SET trashed_at_ms = ?, parent_tag_id = NULL, updated_at_ms = ?
		WHERE id = ? AND trashed_at_ms IS NULL
	`, timeToMillis(stamp), timeToMillis(stamp), id); err != nil {
		return Tag{}, err
	}
	tag.TrashedAt = &stamp
	tag.ParentID = ""
	tag.UpdatedAt = stamp
	return tag, nil
}

// RestoreTag returns a soft-deleted Tag to the live set.
func (s *Store) RestoreTag(ctx context.Context, id TagID, now time.Time) (Tag, error) {
	if id == "" {
		return Tag{}, ValidationError("tag id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Tag{}, err
	}
	if _, err := s.anyTagSQL(ctx, id); err != nil {
		return Tag{}, err
	}
	updatedAt := normalizeTime(now)
	_, err := s.catalogW.ExecContext(ctx, `
		UPDATE tag SET trashed_at_ms = NULL, updated_at_ms = ?
		WHERE id = ?
	`, timeToMillis(updatedAt), id)
	if err != nil {
		if isUniqueConstraint(err) {
			return Tag{}, ValidationError("tag name already exists")
		}
		return Tag{}, err
	}
	return s.liveTagSQL(ctx, id)
}

// AssignTag attaches one live Tag to one visible target.
func (s *Store) AssignTag(ctx context.Context, tagID TagID, kind TagTargetKind, targetID string, filter LibraryFilter) (TagAssignment, error) {
	return s.changeOneTagAssignment(ctx, tagID, kind, targetID, filter, true)
}

// UnassignTag detaches one Tag from one visible target.
func (s *Store) UnassignTag(ctx context.Context, tagID TagID, kind TagTargetKind, targetID string, filter LibraryFilter) (TagAssignment, error) {
	return s.changeOneTagAssignment(ctx, tagID, kind, targetID, filter, false)
}

// TagAssignments returns the live first-class Tags assigned to one visible target.
func (s *Store) TagAssignments(ctx context.Context, kind TagTargetKind, targetID string, filter LibraryFilter) (TagAssignment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return TagAssignment{}, err
	}
	target, err := s.visibleTagTarget(ctx, kind, targetID, filter)
	if err != nil {
		return TagAssignment{}, err
	}
	tags, err := s.tagsForTargetSQL(ctx, target.Kind, target.ID)
	if err != nil {
		return TagAssignment{}, err
	}
	return TagAssignment{TargetKind: target.Kind, TargetID: target.ID, LibraryID: target.LibraryID, Tags: tags}, nil
}

// BulkAssignTags mutates selected targets by mode: add, remove, or replace.
func (s *Store) BulkAssignTags(ctx context.Context, tagIDs []TagID, kind TagTargetKind, targetIDs []string, mode TagBulkMode, filter LibraryFilter) (TagBulkAssignmentResult, error) {
	tagIDs = uniqueTagIDs(tagIDs)
	targetIDs = uniqueStrings(targetIDs)
	if len(targetIDs) == 0 {
		return TagBulkAssignmentResult{}, ValidationError("target ids are required")
	}
	if mode != TagBulkAdd && mode != TagBulkRemove && mode != TagBulkReplace {
		return TagBulkAssignmentResult{}, ValidationError("unknown tag bulk mode")
	}
	if mode != TagBulkRemove && len(tagIDs) == 0 {
		return TagBulkAssignmentResult{}, ValidationError("tag ids are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return TagBulkAssignmentResult{}, err
	}
	tags := make([]Tag, 0, len(tagIDs))
	for _, id := range tagIDs {
		tag, err := s.liveTagSQL(ctx, id)
		if err != nil {
			return TagBulkAssignmentResult{}, err
		}
		tags = append(tags, tag)
	}
	targets := make([]tagTarget, 0, len(targetIDs))
	for _, id := range targetIDs {
		target, err := s.visibleTagTarget(ctx, kind, id, filter)
		if err != nil {
			return TagBulkAssignmentResult{}, err
		}
		targets = append(targets, target)
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return TagBulkAssignmentResult{}, err
	}
	defer tx.Rollback()
	table, targetColumn, err := tagAssignmentTable(kind)
	if err != nil {
		return TagBulkAssignmentResult{}, err
	}
	for _, target := range targets {
		if mode == TagBulkReplace {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+targetColumn+` = ?`, target.ID); err != nil {
				return TagBulkAssignmentResult{}, err
			}
		}
		for _, tag := range tags {
			switch mode {
			case TagBulkAdd, TagBulkReplace:
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+table+`(`+targetColumn+`, tag_id) VALUES (?, ?)`, target.ID, tag.ID); err != nil {
					return TagBulkAssignmentResult{}, err
				}
			case TagBulkRemove:
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+targetColumn+` = ? AND tag_id = ?`, target.ID, tag.ID); err != nil {
					return TagBulkAssignmentResult{}, err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return TagBulkAssignmentResult{}, err
	}
	return TagBulkAssignmentResult{Affected: len(targets), Tags: tags}, nil
}

func (s *Store) changeOneTagAssignment(ctx context.Context, tagID TagID, kind TagTargetKind, targetID string, filter LibraryFilter, assign bool) (TagAssignment, error) {
	if tagID == "" {
		return TagAssignment{}, ValidationError("tag id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return TagAssignment{}, err
	}
	if _, err := s.liveTagSQL(ctx, tagID); err != nil {
		return TagAssignment{}, err
	}
	target, err := s.visibleTagTarget(ctx, kind, targetID, filter)
	if err != nil {
		return TagAssignment{}, err
	}
	table, targetColumn, err := tagAssignmentTable(kind)
	if err != nil {
		return TagAssignment{}, err
	}
	if assign {
		_, err = s.catalogW.ExecContext(ctx, `INSERT OR IGNORE INTO `+table+`(`+targetColumn+`, tag_id) VALUES (?, ?)`, target.ID, tagID)
	} else {
		_, err = s.catalogW.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+targetColumn+` = ? AND tag_id = ?`, target.ID, tagID)
	}
	if err != nil {
		return TagAssignment{}, err
	}
	tags, err := s.tagsForTargetSQL(ctx, target.Kind, target.ID)
	if err != nil {
		return TagAssignment{}, err
	}
	return TagAssignment{TargetKind: target.Kind, TargetID: target.ID, LibraryID: target.LibraryID, Tags: tags}, nil
}

type tagTarget struct {
	Kind      TagTargetKind
	ID        string
	LibraryID LibraryID
}

func (s *Store) visibleTagTarget(ctx context.Context, kind TagTargetKind, id string, filter LibraryFilter) (tagTarget, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return tagTarget{}, ValidationError("target id is required")
	}
	switch kind {
	case TagTargetMedia:
		media, err := s.visibleMediaSQL(ctx, MediaID(id), filter)
		if err != nil {
			return tagTarget{}, err
		}
		return tagTarget{Kind: kind, ID: string(media.ID), LibraryID: media.LibraryID}, nil
	case TagTargetEntity:
		entity, err := s.visibleEntitySQL(ctx, EntityID(id), filter)
		if err != nil {
			return tagTarget{}, err
		}
		return tagTarget{Kind: kind, ID: string(entity.ID), LibraryID: entity.LibraryID}, nil
	case TagTargetMarker:
		marker, err := s.visibleMarkerSQL(ctx, MarkerID(id), filter)
		if err != nil {
			return tagTarget{}, err
		}
		media, err := s.visibleMediaSQL(ctx, marker.MediaID, filter)
		if err != nil {
			return tagTarget{}, err
		}
		return tagTarget{Kind: kind, ID: string(marker.ID), LibraryID: media.LibraryID}, nil
	default:
		return tagTarget{}, ValidationError("unknown tag target kind")
	}
}

func tagAssignmentTable(kind TagTargetKind) (table string, targetColumn string, err error) {
	switch kind {
	case TagTargetMedia:
		return "media_tag", "media_id", nil
	case TagTargetEntity:
		return "entity_tag", "entity_id", nil
	case TagTargetMarker:
		return "marker_core_tag", "marker_id", nil
	default:
		return "", "", ValidationError("unknown tag target kind")
	}
}

func (s *Store) tagsForTargetSQL(ctx context.Context, kind TagTargetKind, targetID string) ([]Tag, error) {
	table, targetColumn, err := tagAssignmentTable(kind)
	if err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT t.id, t.name, t.parent_tag_id, t.trashed_at_ms, t.created_at_ms, t.updated_at_ms
		FROM tag t JOIN `+table+` a ON a.tag_id = t.id
		WHERE a.`+targetColumn+` = ? AND t.trashed_at_ms IS NULL
		ORDER BY lower(t.name), t.id
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []Tag{}
	for rows.Next() {
		tag, err := scanTag(rows.Scan)
		if err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (s *Store) liveTagSQL(ctx context.Context, id TagID) (Tag, error) {
	tag, err := scanTag(s.catalogR.QueryRowContext(ctx, `
		SELECT id, name, parent_tag_id, trashed_at_ms, created_at_ms, updated_at_ms
		FROM tag WHERE id = ? AND trashed_at_ms IS NULL
	`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ValidationError("tag not found")
	}
	return tag, err
}

func (s *Store) anyTagSQL(ctx context.Context, id TagID) (Tag, error) {
	tag, err := scanTag(s.catalogR.QueryRowContext(ctx, `
		SELECT id, name, parent_tag_id, trashed_at_ms, created_at_ms, updated_at_ms
		FROM tag WHERE id = ?
	`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ValidationError("tag not found")
	}
	return tag, err
}

func (s *Store) checkTagParentCycle(ctx context.Context, id, parentID TagID) error {
	rows, err := s.catalogR.QueryContext(ctx, `
		WITH RECURSIVE ancestors(id, parent_tag_id) AS (
		  SELECT id, parent_tag_id FROM tag WHERE id = ?
		  UNION ALL
		  SELECT t.id, t.parent_tag_id
		  FROM tag t JOIN ancestors a ON t.id = a.parent_tag_id
		  WHERE t.trashed_at_ms IS NULL
		)
		SELECT id FROM ancestors
	`, parentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ancestor TagID
		if err := rows.Scan(&ancestor); err != nil {
			return err
		}
		if ancestor == id {
			return ValidationError("tag parent cycle")
		}
	}
	return rows.Err()
}

func scanTag(scan func(dest ...any) error) (Tag, error) {
	var tag Tag
	var parent sql.NullString
	var trashed sql.NullInt64
	var created, updated int64
	if err := scan(&tag.ID, &tag.Name, &parent, &trashed, &created, &updated); err != nil {
		return Tag{}, err
	}
	tag.ParentID = TagID(parent.String)
	if trashed.Valid {
		t := millisToTime(trashed.Int64)
		tag.TrashedAt = &t
	}
	tag.CreatedAt = millisToTime(created)
	tag.UpdatedAt = millisToTime(updated)
	return tag, nil
}

// scanTagWithCount scans the tag columns plus the trailing assignment_count
// aggregate used by Tags() for the manager tree.
func scanTagWithCount(scan func(dest ...any) error) (Tag, error) {
	var tag Tag
	var parent sql.NullString
	var trashed sql.NullInt64
	var created, updated int64
	if err := scan(&tag.ID, &tag.Name, &parent, &trashed, &created, &updated, &tag.AssignmentCount); err != nil {
		return Tag{}, err
	}
	tag.ParentID = TagID(parent.String)
	if trashed.Valid {
		t := millisToTime(trashed.Int64)
		tag.TrashedAt = &t
	}
	tag.CreatedAt = millisToTime(created)
	tag.UpdatedAt = millisToTime(updated)
	return tag, nil
}

func randomTagID() (TagID, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return TagID("tag-" + hex.EncodeToString(b[:])), nil
}

func uniqueTagIDs(ids []TagID) []TagID {
	out := make([]TagID, 0, len(ids))
	seen := map[TagID]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func uniqueStrings(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

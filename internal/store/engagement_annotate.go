package store

import (
	"context"
	"database/sql"
	"strings"
)

// SourceEngagement is the acting Profile's playback state for one surfaced
// SourceItem, so a card can display progress and completion without a second
// round trip. A media-kind item carries its own position; an entity-kind item
// that is an ordered container carries counts over its visible ordered
// children. Generic by invariant #1: the shape names positions and counts,
// never a domain.
type SourceEngagement struct {
	// PositionSeconds and DurationSeconds are set while the item is resumable
	// (past the resume floor and not completed).
	PositionSeconds int
	DurationSeconds int
	// Completed is true once the Profile finished the item; for a container,
	// once every visible ordered child is finished.
	Completed bool
	// TotalCount and RemainingCount count a container's visible ordered
	// children and how many of them the Profile has not finished.
	TotalCount     int
	RemainingCount int
}

const engagementChunkSize = 500

// AnnotateEngagement sets Engagement on each surfaced SourceItem the acting
// Profile has state for. An empty profileID is a no-op (a profile-less rail
// has no playback state). Container counts only ever include children the
// filter lets this scope see, so a restricted or certification-hidden child
// never moves a count (ADR-0004). Safe to call on any SourceItem slice.
func (s *Store) AnnotateEngagement(ctx context.Context, profileID ProfileID, filter LibraryFilter, items []SourceItem) error {
	if profileID == "" || len(items) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}

	var mediaIDs []MediaID
	var containerIDs []string
	for i := range items {
		if items[i].Kind == "entity" {
			containerIDs = append(containerIDs, string(items[i].ID))
		} else {
			mediaIDs = append(mediaIDs, items[i].ID)
		}
	}
	children, err := s.orderedChildrenLocked(ctx, containerIDs, filter)
	if err != nil {
		return err
	}
	lookup := append([]MediaID{}, mediaIDs...)
	for _, ids := range children {
		lookup = append(lookup, ids...)
	}
	progress, err := s.progressByMediaLocked(ctx, profileID, lookup)
	if err != nil {
		return err
	}

	for i := range items {
		if items[i].Kind == "entity" {
			ids := children[string(items[i].ID)]
			if len(ids) == 0 {
				continue
			}
			remaining := 0
			for _, id := range ids {
				if !progress[id].Completed {
					remaining++
				}
			}
			items[i].Engagement = &SourceEngagement{
				Completed:      remaining == 0,
				TotalCount:     len(ids),
				RemainingCount: remaining,
			}
			continue
		}
		if state, ok := progress[items[i].ID]; ok && (state.Completed || state.PositionSeconds > 0) {
			items[i].Engagement = &state
		}
	}
	return nil
}

// orderedChildrenLocked returns, per container entity, the visible non-extra
// Media reached through ordered relations: direct ordered children, plus the
// ordered children of its ordered sub-containers (one level) that are neither
// trashed nor in a Library the scope cannot see. An entity with
// no ordered children is absent from the result. An open Encrypted Library
// keeps its relations in its own database, so every open plane is read.
func (s *Store) orderedChildrenLocked(ctx context.Context, containerIDs []string, filter LibraryFilter) (map[string][]MediaID, error) {
	raw := map[string][]MediaID{}
	seen := map[[2]string]struct{}{}
	var all []MediaID
	planes := s.openCatalogReadersLocked()
	// Whether a sub-container's Library is in scope, loaded on first need.
	var libraries map[LibraryID]Library
	visibleVia := func(id LibraryID) (bool, error) {
		if libraries == nil {
			loaded, err := loadLibraries(ctx, s.catalogR)
			if err != nil {
				return false, err
			}
			libraries = make(map[LibraryID]Library, len(loaded))
			for _, library := range loaded {
				libraries[library.ID] = library
			}
		}
		library, ok := libraries[id]
		return ok && libraryVisible(library, filter), nil
	}
	for start := 0; start < len(containerIDs); start += engagementChunkSize {
		chunk := containerIDs[start:min(start+engagementChunkSize, len(containerIDs))]
		placeholders := sqlPlaceholders(len(chunk))
		args := make([]any, 0, 2*len(chunk))
		for range 2 {
			for _, id := range chunk {
				args = append(args, id)
			}
		}
		for _, db := range planes {
			rows, err := db.QueryContext(ctx, `
				SELECT r.from_entity_id, r.to_media_id, ''
				FROM entity_relation r
				JOIN media m ON m.id = r.to_media_id AND m.extra = 0
				WHERE r.sort_order IS NOT NULL
				  AND r.from_entity_id IN (`+placeholders+`)
				UNION
				SELECT parent.from_entity_id, child.to_media_id, sub.library_id
				FROM entity_relation parent
				JOIN entity sub ON sub.id = parent.to_entity_id AND sub.trashed_at_ms IS NULL
				JOIN entity_relation child ON child.from_entity_id = parent.to_entity_id
				JOIN media m ON m.id = child.to_media_id AND m.extra = 0
				WHERE parent.sort_order IS NOT NULL AND parent.to_entity_id IS NOT NULL
				  AND child.sort_order IS NOT NULL
				  AND parent.from_entity_id IN (`+placeholders+`)
			`, args...)
			if err != nil {
				return nil, err
			}
			found, err := scanChildRows(rows)
			if err != nil {
				return nil, err
			}
			for _, row := range found {
				if row.viaLibrary != "" {
					ok, err := visibleVia(row.viaLibrary)
					if err != nil {
						return nil, err
					}
					if !ok {
						continue
					}
				}
				// One Media can be reached directly and through a sub-container.
				key := [2]string{row.containerID, string(row.mediaID)}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				raw[row.containerID] = append(raw[row.containerID], row.mediaID)
				all = append(all, row.mediaID)
			}
		}
	}
	if len(all) == 0 {
		return raw, nil
	}
	visible, err := s.visibleMediaBatchSQL(ctx, all, filter)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]MediaID, len(raw))
	for containerID, ids := range raw {
		for _, id := range ids {
			if _, ok := visible[id]; ok {
				out[containerID] = append(out[containerID], id)
			}
		}
	}
	return out, nil
}

// openCatalogReadersLocked lists the catalog reader and the reader of every
// open Encrypted Library. A sealed Library contributes nothing.
func (s *Store) openCatalogReadersLocked() []*sql.DB {
	readers := []*sql.DB{s.catalogR}
	for _, db := range s.encryptedDBs {
		if db != nil && db.state == "unsealed-registered" && db.reader != nil {
			readers = append(readers, db.reader)
		}
	}
	return readers
}

// childRow is one ordered child of a container. viaLibrary is the Library of
// the sub-container it was reached through, empty for a direct child.
type childRow struct {
	containerID string
	mediaID     MediaID
	viaLibrary  LibraryID
}

func scanChildRows(rows *sql.Rows) ([]childRow, error) {
	defer rows.Close()
	var out []childRow
	for rows.Next() {
		var row childRow
		if err := rows.Scan(&row.containerID, &row.mediaID, &row.viaLibrary); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// progressByMediaLocked reads the Profile's playback state for the given
// Media. A row below the resume floor that is not completed reports no
// position, matching what Continue Watching treats as started. So does a
// partial play made through a Library Channel, which resume reads exclude;
// its completed mark still counts.
func (s *Store) progressByMediaLocked(ctx context.Context, profileID ProfileID, ids []MediaID) (map[MediaID]SourceEngagement, error) {
	out := make(map[MediaID]SourceEngagement, len(ids))
	for start := 0; start < len(ids); start += engagementChunkSize {
		chunk := ids[start:min(start+engagementChunkSize, len(ids))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, profileID)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.eventsR.QueryContext(ctx, `
			SELECT media_id, position_seconds, duration_seconds, watched, channel_context
			FROM playback_progress
			WHERE profile_id = ? AND media_id IN (`+sqlPlaceholders(len(chunk))+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		if err := scanProgressRows(rows, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanProgressRows(rows *sql.Rows, into map[MediaID]SourceEngagement) error {
	defer rows.Close()
	for rows.Next() {
		var id MediaID
		var position, duration, watched, channel int
		if err := rows.Scan(&id, &position, &duration, &watched, &channel); err != nil {
			return err
		}
		state := SourceEngagement{Completed: watched != 0}
		if !state.Completed && channel == 0 && position >= resumeFloorSeconds && duration > 0 {
			state.PositionSeconds = position
			state.DurationSeconds = duration
		}
		into[id] = state
	}
	return rows.Err()
}

func sqlPlaceholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

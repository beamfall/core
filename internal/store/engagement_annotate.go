package store

import (
	"context"
	"database/sql"
	"maps"
	"slices"
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

// EngagementContainerRelations identifies the declared links from a nested
// container to its parent and to the Media it holds.
type EngagementContainerRelations interface {
	Holds(kind, relation string) bool
	Nests(kind, relation, targetKind string) bool
}

// AnnotateEngagement sets Engagement on each surfaced SourceItem the acting
// Profile has state for. An empty profileID is a no-op (a profile-less rail
// has no playback state). Container counts only ever include children the
// filter lets this scope see, so a restricted or certification-hidden child
// never moves a count (ADR-0004). containers supplies plugin-declared
// membership rules keyed by container kind.
func (s *Store) AnnotateEngagement(ctx context.Context, profileID ProfileID, filter LibraryFilter, items []SourceItem, containers map[string]EngagementContainerRelations) error {
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
	children, err := s.orderedChildrenLocked(ctx, containerIDs, filter, containers)
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
			// #nosec G602 -- i ranges over items.
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
// Media reached through ordered relations. A declared container counts its
// direct ordered Media and the ordered Media of its immediate parts in the
// same Library, using declared child-to-parent relations. Other kinds retain
// their outgoing ordered relation semantics. Each open catalog plane is read.
func (s *Store) orderedChildrenLocked(ctx context.Context, containerIDs []string, filter LibraryFilter, containers map[string]EngagementContainerRelations) (map[string][]MediaID, error) {
	if len(containerIDs) == 0 {
		return nil, nil
	}
	raw := map[string][]MediaID{}
	seen := map[[2]string]struct{}{}
	var all []MediaID
	planes := s.openCatalogReadersLocked()
	kinds := slices.Sorted(maps.Keys(containers))
	kindPlaceholders := sqlPlaceholders(len(kinds))
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
		args := make([]any, 0, len(chunk)+2*len(kinds))
		for _, id := range chunk {
			args = append(args, id)
		}
		for range 2 {
			for _, kind := range kinds {
				args = append(args, kind)
			}
		}
		for _, db := range planes {
			// #nosec G202 -- only generated placeholders are interpolated; IDs are bound args.
			rows, err := db.QueryContext(ctx, `
				WITH roots AS (
					SELECT id, kind, library_id FROM entity
					WHERE id IN (`+placeholders+`) AND trashed_at_ms IS NULL
				), parts(root_id, root_kind, id, kind, library_id, parent_relation) AS (
					SELECT root.id, root.kind, sub.id, sub.kind, sub.library_id, ''
					FROM roots root
					JOIN entity_relation link ON link.from_entity_id = root.id
					JOIN entity sub ON sub.id = link.to_entity_id AND sub.trashed_at_ms IS NULL
					WHERE link.sort_order IS NOT NULL AND root.kind NOT IN (`+kindPlaceholders+`)
					UNION ALL
					SELECT root.id, root.kind, sub.id, sub.kind, sub.library_id, link.relation
					FROM roots root
					JOIN entity_relation link ON link.to_entity_id = root.id
					JOIN entity sub ON sub.id = link.from_entity_id AND sub.trashed_at_ms IS NULL
					WHERE root.kind IN (`+kindPlaceholders+`) AND sub.library_id = root.library_id
				)
				SELECT root.id, r.to_media_id, '', '', '', '', ''
				FROM roots root
				JOIN entity_relation r ON r.from_entity_id = root.id
				JOIN media m ON m.id = r.to_media_id AND m.extra = 0
				WHERE r.sort_order IS NOT NULL
				UNION ALL
				SELECT part.root_id, child.to_media_id, part.library_id,
				       part.root_kind, part.kind, part.parent_relation, child.relation
				FROM parts part
				JOIN entity_relation child ON child.from_entity_id = part.id
				JOIN media m ON m.id = child.to_media_id AND m.extra = 0
				WHERE child.sort_order IS NOT NULL
				  AND (part.parent_relation = '' OR m.library_id = part.library_id)
			`, args...)
			if err != nil {
				return nil, err
			}
			found, err := scanChildRows(rows)
			if err != nil {
				return nil, err
			}
			for _, row := range found {
				if row.parentRelation != "" {
					relations := containers[row.partKind]
					if relations == nil || !relations.Nests(row.partKind, row.parentRelation, row.rootKind) || !relations.Holds(row.partKind, row.mediaRelation) {
						continue
					}
				}
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
	containerID    string
	mediaID        MediaID
	viaLibrary     LibraryID
	rootKind       string
	partKind       string
	parentRelation string
	mediaRelation  string
}

func scanChildRows(rows *sql.Rows) ([]childRow, error) {
	defer rows.Close()
	var out []childRow
	for rows.Next() {
		var row childRow
		if err := rows.Scan(&row.containerID, &row.mediaID, &row.viaLibrary,
			&row.rootKind, &row.partKind, &row.parentRelation, &row.mediaRelation); err != nil {
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
		// #nosec G202 -- only generated placeholders are interpolated; IDs are bound args.
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

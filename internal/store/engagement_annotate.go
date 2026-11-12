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
// ordered children of its ordered sub-containers (one level). An entity with
// no ordered children is absent from the result.
func (s *Store) orderedChildrenLocked(ctx context.Context, containerIDs []string, filter LibraryFilter) (map[string][]MediaID, error) {
	raw := map[string][]MediaID{}
	var all []MediaID
	for start := 0; start < len(containerIDs); start += engagementChunkSize {
		chunk := containerIDs[start:min(start+engagementChunkSize, len(containerIDs))]
		placeholders := sqlPlaceholders(len(chunk))
		args := make([]any, 0, 2*len(chunk))
		for range 2 {
			for _, id := range chunk {
				args = append(args, id)
			}
		}
		rows, err := s.catalogR.QueryContext(ctx, `
			SELECT from_entity_id, to_media_id
			FROM entity_relation
			WHERE sort_order IS NOT NULL AND to_media_id IS NOT NULL
			  AND from_entity_id IN (`+placeholders+`)
			UNION
			SELECT parent.from_entity_id, child.to_media_id
			FROM entity_relation parent
			JOIN entity_relation child ON child.from_entity_id = parent.to_entity_id
			WHERE parent.sort_order IS NOT NULL AND parent.to_entity_id IS NOT NULL
			  AND child.sort_order IS NOT NULL AND child.to_media_id IS NOT NULL
			  AND parent.from_entity_id IN (`+placeholders+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		if err := scanChildRows(rows, raw, &all); err != nil {
			return nil, err
		}
	}
	if len(all) == 0 {
		return raw, nil
	}
	visible, err := s.visibleMediaBatchSQL(ctx, all, filter)
	if err != nil {
		return nil, err
	}
	extras, err := s.extraMediaIDsSQL(ctx, all)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]MediaID, len(raw))
	for containerID, ids := range raw {
		for _, id := range ids {
			if _, ok := visible[id]; ok && !extras[id] {
				out[containerID] = append(out[containerID], id)
			}
		}
	}
	return out, nil
}

func scanChildRows(rows *sql.Rows, into map[string][]MediaID, all *[]MediaID) error {
	defer rows.Close()
	for rows.Next() {
		var containerID string
		var mediaID MediaID
		if err := rows.Scan(&containerID, &mediaID); err != nil {
			return err
		}
		into[containerID] = append(into[containerID], mediaID)
		*all = append(*all, mediaID)
	}
	return rows.Err()
}

// progressByMediaLocked reads the Profile's playback state for the given
// Media. A row below the resume floor that is not completed reports no
// position, matching what Continue Watching treats as started.
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
			SELECT media_id, position_seconds, duration_seconds, watched
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
		var position, duration, watched int
		if err := rows.Scan(&id, &position, &duration, &watched); err != nil {
			return err
		}
		state := SourceEngagement{Completed: watched != 0}
		if !state.Completed && position >= resumeFloorSeconds && duration > 0 {
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

package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// prePlayClipsKey is the server_config key holding the Owner's pre-play clip
// list: a JSON array of Media IDs played, in order, before an eligible feature.
const prePlayClipsKey = "preplay.clips"

// MaxPrePlayClips caps the Owner's pre-play clip list.
const MaxPrePlayClips = 10

// maxPrePlayExtraLinks bounds one MediaExtraLinks read so a Library with a
// very large Extras set cannot turn a pre-play plan into an unbounded scan.
const maxPrePlayExtraLinks = 2000

// MediaExtraLink is one Extra-of relation inside a Library: the Extra, the
// Media it is an Extra of, and the Extra's file path ("" when it has none).
type MediaExtraLink struct {
	ExtraID  MediaID
	ParentID MediaID
	Path     string
}

// PrePlayClips returns the Owner's pre-play clip list in play order. An unset
// or unreadable value is an empty list: a corrupt row never blocks playback.
func (s *Store) PrePlayClips(ctx context.Context) ([]MediaID, error) {
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return nil, err
	}
	raw := values[prePlayClipsKey]
	if raw == "" {
		return []MediaID{}, nil
	}
	var ids []MediaID
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return []MediaID{}, nil
	}
	clips, err := normalizePrePlayClips(ids)
	if err != nil {
		return []MediaID{}, nil
	}
	return clips, nil
}

// SetPrePlayClips replaces the Owner's pre-play clip list. IDs are trimmed and
// de-duplicated keeping first-seen order; every ID must name an existing,
// untrashed Media. An empty list clears the setting.
func (s *Store) SetPrePlayClips(ctx context.Context, ids []MediaID, now time.Time) ([]MediaID, error) {
	clips, err := normalizePrePlayClips(ids)
	if err != nil {
		return nil, err
	}
	if len(clips) == 0 {
		return []MediaID{}, s.clearServerConfigValues(ctx, []string{prePlayClipsKey})
	}
	for _, id := range clips {
		if _, err := s.MediaLibrary(ctx, id); err != nil {
			return nil, ValidationError("unknown pre-play clip " + string(id))
		}
	}
	encoded, err := json.Marshal(clips)
	if err != nil {
		return nil, err
	}
	if err := s.setServerConfigValues(ctx, map[string]string{prePlayClipsKey: string(encoded)}, now); err != nil {
		return nil, err
	}
	return clips, nil
}

func normalizePrePlayClips(ids []MediaID) ([]MediaID, error) {
	out := make([]MediaID, 0, len(ids))
	seen := map[MediaID]bool{}
	for _, id := range ids {
		id = MediaID(strings.TrimSpace(string(id)))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > MaxPrePlayClips {
		return nil, ValidationError("too many pre-play clips")
	}
	return out, nil
}

// MediaExtraLinks returns the Extra-of relations inside the Library that holds
// featureID, ordered by Extra ID and capped at a fixed bound. The feature must
// be visible under filter; Extras and parents must both sit in that Library,
// be untrashed, and the Extra must pass the filter's certification ceiling.
func (s *Store) MediaExtraLinks(ctx context.Context, featureID MediaID, filter LibraryFilter) ([]MediaExtraLink, error) {
	if featureID == "" {
		return nil, errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, featureID)
	if err != nil {
		return nil, err
	}
	feature, err := s.visibleMediaSQLFrom(ctx, plane.reader, featureID, filter)
	if err != nil {
		return nil, err
	}
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
	args := make([]any, 0, 3+len(certArgs)+1)
	args = append(args, MediaRelationExtraOf, feature.LibraryID, feature.LibraryID)
	args = append(args, certArgs...)
	args = append(args, maxPrePlayExtraLinks)
	// #nosec G202 -- the interpolated fragment is closed certification SQL.
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT r.from_media_id, r.to_media_id, COALESCE(f.path, '')
		FROM media_relation r
		JOIN media m ON m.id = r.from_media_id
		JOIN media p ON p.id = r.to_media_id
		LEFT JOIN media_file f ON f.media_id = m.id
		WHERE r.relation = ? AND m.library_id = ? AND p.library_id = ?
		  AND m.trashed_at_ms IS NULL AND p.trashed_at_ms IS NULL`+certClause+`
		ORDER BY r.from_media_id, r.to_media_id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaExtraLink{}
	for rows.Next() {
		var link MediaExtraLink
		if err := rows.Scan(&link.ExtraID, &link.ParentID, &link.Path); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

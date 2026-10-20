package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SubtitleChoiceRecord is a viewer's pinned subtitle choice for one
// (profile, group) — the ADR-0177 per-item choice record widened to SERIES
// scope. The track identity is the format-independent
// language+kind+role tuple, never a stream index, so the pin survives renditions
// and remuxes. Selected distinguishes a pinned track (true) from a deliberate
// subtitles-off pin (false). AudioLanguage carries the audio track changed in
// the same interaction, if any ("" = the subtitle pick stood alone).
type SubtitleChoiceRecord struct {
	GroupKey          string
	Selected          bool
	SubLanguage       string
	SubKind           string
	SubForced         bool
	SubSDH            bool
	SubVisualImpaired bool
	SubCommentary     bool
	AudioLanguage     string
	UpdatedAt         time.Time
}

// subtitleGroupPrefixes distinguish a series-scoped group key from a per-item
// one. Both are opaque to the resolver; the prefix keeps an entity id and a
// media id from ever colliding in the same key space.
const (
	subtitleGroupEntityPrefix = "entity:"
	subtitleGroupMediaPrefix  = "media:"
)

// MediaSubtitleGroup returns the pinned-choice group key for a Media (ADR-0194
// §3). Episodic content — a Media that is an ORDERED child of a container entity
// (a series grouping, sort_order non-null) — keys on that container so a pick
// propagates series-wide across every item in that container. Standalone
// content keys on the item itself, preserving the original ADR-0177 per-item
// scope. The grouping is entirely generic: it names an entity or a media id,
// never a domain concept (invariants #1/#2).
func (s *Store) MediaSubtitleGroup(ctx context.Context, mediaID MediaID) (string, error) {
	if mediaID == "" {
		return "", ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	var containerID string
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT from_entity_id
		FROM entity_relation
		WHERE to_media_id = ? AND sort_order IS NOT NULL
		ORDER BY from_entity_id
		LIMIT 1
	`, mediaID).Scan(&containerID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return subtitleGroupMediaPrefix + string(mediaID), nil
	case err != nil:
		return "", err
	default:
		return subtitleGroupEntityPrefix + containerID, nil
	}
}

// PutSubtitleChoice upserts the pinned subtitle choice for (profile, group). It
// never writes back to profile prefs (ADR-0194 §3 pinned semantic 2) — this row
// is the only home of the pin. A record is replaced wholesale so a later pick,
// or a switch to subtitles-off, supersedes the prior one for the whole group.
func (s *Store) PutSubtitleChoice(ctx context.Context, profileID ProfileID, rec SubtitleChoiceRecord, now time.Time) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	if rec.GroupKey == "" {
		return ValidationError("subtitle choice group key is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if exists, err := s.profileExistsSQL(ctx, profileID); err != nil {
		return err
	} else if !exists {
		return ValidationError("unknown profile")
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO subtitle_choice_record(
		  profile_id, group_key, selected, sub_language, sub_kind,
		  sub_forced, sub_sdh, sub_visual_impaired, sub_commentary,
		  audio_language, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, group_key) DO UPDATE SET
		  selected = excluded.selected,
		  sub_language = excluded.sub_language,
		  sub_kind = excluded.sub_kind,
		  sub_forced = excluded.sub_forced,
		  sub_sdh = excluded.sub_sdh,
		  sub_visual_impaired = excluded.sub_visual_impaired,
		  sub_commentary = excluded.sub_commentary,
		  audio_language = excluded.audio_language,
		  updated_at_ms = excluded.updated_at_ms
	`, profileID, rec.GroupKey, boolInt(rec.Selected), rec.SubLanguage, rec.SubKind,
		boolInt(rec.SubForced), boolInt(rec.SubSDH), boolInt(rec.SubVisualImpaired), boolInt(rec.SubCommentary),
		rec.AudioLanguage, now.UTC().UnixMilli())
	return err
}

// SubtitleChoice returns the pinned subtitle choice for (profile, group),
// reporting found=false when the group carries no pin. The resolver treats a
// found record as the highest-precedence input (ADR-0194 §3 pinned semantic 4).
func (s *Store) SubtitleChoice(ctx context.Context, profileID ProfileID, groupKey string) (SubtitleChoiceRecord, bool, error) {
	if profileID == "" {
		return SubtitleChoiceRecord{}, false, ValidationError("profile ID is required")
	}
	if groupKey == "" {
		return SubtitleChoiceRecord{}, false, ValidationError("group key is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SubtitleChoiceRecord{}, false, err
	}
	rec := SubtitleChoiceRecord{GroupKey: groupKey}
	var selected, forced, sdh, vi, commentary int
	var updatedMS int64
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT selected, sub_language, sub_kind, sub_forced, sub_sdh,
		       sub_visual_impaired, sub_commentary, audio_language, updated_at_ms
		FROM subtitle_choice_record
		WHERE profile_id = ? AND group_key = ?
	`, profileID, groupKey).Scan(&selected, &rec.SubLanguage, &rec.SubKind, &forced, &sdh,
		&vi, &commentary, &rec.AudioLanguage, &updatedMS)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return SubtitleChoiceRecord{}, false, nil
	case err != nil:
		return SubtitleChoiceRecord{}, false, err
	}
	rec.Selected = selected != 0
	rec.SubForced = forced != 0
	rec.SubSDH = sdh != 0
	rec.SubVisualImpaired = vi != 0
	rec.SubCommentary = commentary != 0
	rec.UpdatedAt = time.UnixMilli(updatedMS).UTC()
	return rec, true, nil
}

package store

import (
	"context"
	"strings"
	"time"
)

// MuteKind is the target species of a per-Profile Mute: any
// Entity or any Tag. Individual Media are not mutable in v1.
type MuteKind string

const (
	MuteKindEntity MuteKind = "entity"
	MuteKindTag    MuteKind = "tag"
)

// MuteTarget is one "Not interested" entry a Profile has authored for itself.
type MuteTarget struct {
	Kind               MuteKind
	TargetID           string
	IncludeDescendants bool
	MutedAt            time.Time
}

// SetMute records or clears a Profile's Mute of one Entity or Tag.
// A Mute is always self-authored and self-cleared; it carries no safety weight,
// so — unlike a destructive curation op — it needs no MANAGER role and no Reveal
// visibility gate here. Reveal applies where the list is *read*: the negative
// Source predicate that consumes it never surfaces a restricted target to a
// relocked session (§2.5). Managed Profiles may mute and unmute freely (§2.4).
func (s *Store) SetMute(ctx context.Context, profileID ProfileID, kind MuteKind, targetID string, includeDescendants, muted bool) error {
	if profileID == "" || targetID == "" {
		return ValidationError("profile ID and target ID are required")
	}
	if kind != MuteKindEntity && kind != MuteKindTag {
		return ValidationError("mute kind must be entity or tag")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if muted {
		_, err := s.eventsW.ExecContext(ctx, `
			INSERT INTO profile_mute(profile_id, target_kind, target_id, include_descendants, muted_at_ms)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(profile_id, target_kind, target_id) DO UPDATE SET
			  include_descendants = excluded.include_descendants,
			  muted_at_ms = excluded.muted_at_ms
		`, profileID, string(kind), targetID, boolToInt(includeDescendants), timeToMillis(time.Now().UTC()))
		return err
	}
	_, err := s.eventsW.ExecContext(ctx, `
		DELETE FROM profile_mute
		WHERE profile_id = ? AND target_kind = ? AND target_id = ?
	`, profileID, string(kind), targetID)
	return err
}

// MutedTargets returns every target profileID has muted, newest first. The list
// is small and Profile-private: it travels to clients with the Snapshot and is a
// My-Data export/clear citizen. It is the input the negative
// Source predicate will consume; it does not itself apply Reveal.
func (s *Store) MutedTargets(ctx context.Context, profileID ProfileID) ([]MuteTarget, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT target_kind, target_id, include_descendants, muted_at_ms
		FROM profile_mute
		WHERE profile_id = ?
		ORDER BY muted_at_ms DESC, target_kind, target_id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MuteTarget
	for rows.Next() {
		var (
			kind     string
			targetID string
			incDesc  int
			mutedMS  int64
		)
		if err := rows.Scan(&kind, &targetID, &incDesc, &mutedMS); err != nil {
			return nil, err
		}
		out = append(out, MuteTarget{
			Kind:               MuteKind(kind),
			TargetID:           targetID,
			IncludeDescendants: incDesc != 0,
			MutedAt:            millisToTime(mutedMS),
		})
	}
	return out, rows.Err()
}

// muteDescendantDepthCap bounds the entity-graph walk that resolves an
// include-descendants Mute. The entity_relation table carries
// no acyclicity guarantee, so the walk is breadth-first with a visited set
// (cycle-safe) and additionally capped here as defence-in-depth against a
// pathological hierarchy. Real Genre/Tag trees are shallow; 32 is far past any
// plausible depth.
const muteDescendantDepthCap = 32

// applyMuteFilterLocked is the negative Source predicate of ADR-0054 §2.5–2.6.
// It removes, from one passive-discovery rail's already-resolved items, every
// Media the Profile has muted, honoring "specificity wins":
//
//   - A DIRECT mute (a Mute whose target carries no include-descendants, i.e. a
//     single specific entity) wins over everything — the Media drops even from
//     continue-watching and even when favorited (§2.6 bullet 1).
//   - A BROAD/category mute (include-descendants — the hierarchical-category
//     case of §2.3) yields to an explicit positive signal on the specific
//     Media: a Favorite or active continue-watching keeps it visible (§2.6
//     bullet 2). Collection membership is the third named signal but is not
//     server-resolvable in the current schema (SmartCollection membership is an
//     opaque client-evaluated source_expr, ADR-0011 §10 — there is no static
//     Collection→Media table), so it cannot participate without new storage;
//     this is recorded, not worked around, since the schema is frozen.
//
// The mute↔favorite "last action wins" toggle (§2.6 bullet 3) is enforced at the
// write layer (favoriting/unmuting are separate store mutations); here we simply
// read whichever state currently stands. A restricted target is never surfaced
// by this predicate: it operates by *removal* over an already reveal-filtered
// rail, so it can only ever shrink the visible set, never add to it (§2.5).
//
// Runs under the read lock SourceItems already holds: it uses the unexported
// *SQL/*Locked read helpers, never the public lock-taking accessors.
// muteReach is the resolved reach of a Profile's mute list over one candidate
// batch, computed once and shared by both the mute *suppression* predicate
// (applyMuteFilterLocked) and the mute *badge* annotator (annotateMutedLocked).
//
//   - directlyMuted / broadlyMuted are MEDIA-keyed and id-pooled across BOTH
//     target kinds (entity + tag), exactly as the historical suppressor computed
//     them via entity_relation.to_media_id — the media reach axis is unchanged.
//   - directEntities / broadEntities are the ENTITY-kind direct and expanded
//     target id sets, used only to badge entity-kind SourceItems (SearchEntities
//     rows carry an entity id in .ID). Keeping them kind-split means a same-id
//     muted Tag never falsely badges an entity — an entity-only refinement that
//     does not touch any media path.
type muteReach struct {
	directlyMuted  map[MediaID]bool
	broadlyMuted   map[MediaID]bool
	directEntities map[string]struct{}
	broadEntities  map[string]struct{}
}

// mutedReachLocked resolves the Profile's mute list against the candidate items'
// Media ids. It runs under the read lock the caller already holds. An empty mute
// list (or empty items) yields an all-empty reach — the callers treat that as
// "nothing muted" and short-circuit.
func (s *Store) mutedReachLocked(ctx context.Context, profileID ProfileID, items []SourceItem) (muteReach, error) {
	reach := muteReach{
		directlyMuted:  map[MediaID]bool{},
		broadlyMuted:   map[MediaID]bool{},
		directEntities: map[string]struct{}{},
		broadEntities:  map[string]struct{}{},
	}
	if len(items) == 0 {
		return reach, nil
	}
	mutes, err := s.mutedTargetsLocked(ctx, profileID)
	if err != nil {
		return reach, err
	}
	if len(mutes) == 0 {
		return reach, nil
	}

	// Split the muted targets into the two specificity tiers. A Tag and an
	// Entity both resolve through the same entity_relation.to_media_id edge, so
	// the only axis that matters for MEDIA reach is include-descendants: a direct
	// (no descendants) Mute is the specific case that beats everything; a
	// descendants Mute is the broad/category case that yields to a positive
	// signal. The entity-kind sets are tracked separately for entity-item badges.
	directTargets := map[string]struct{}{}
	broadSeeds := []string{}
	for _, m := range mutes {
		if m.IncludeDescendants {
			broadSeeds = append(broadSeeds, m.TargetID)
			if m.Kind == MuteKindEntity {
				reach.broadEntities[m.TargetID] = struct{}{}
			}
		} else {
			directTargets[m.TargetID] = struct{}{}
			if m.Kind == MuteKindEntity {
				reach.directEntities[m.TargetID] = struct{}{}
			}
		}
	}

	// Expand each broad seed to itself plus its transitive entity descendants
	// (entity_relation.from_entity_id -> to_entity_id), cycle-safe and
	// depth-capped.
	broadTargets := map[string]struct{}{}
	if len(broadSeeds) > 0 {
		expanded, err := s.entityDescendantsLocked(ctx, broadSeeds)
		if err != nil {
			return reach, err
		}
		broadTargets = expanded
	}
	// The expansion also grows the entity-kind broad set: any entity-seeded broad
	// mute reaches its entity descendants, so a descendant entity row must badge.
	for _, seed := range broadSeeds {
		if _, ok := reach.broadEntities[seed]; !ok {
			continue
		}
		descendants, err := s.entityDescendantsLocked(ctx, []string{seed})
		if err != nil {
			return reach, err
		}
		for id := range descendants {
			reach.broadEntities[id] = struct{}{}
		}
	}

	candidates := make([]MediaID, len(items))
	for i := range items {
		candidates[i] = items[i].ID
	}

	// For each candidate Media, which muted entity ids reach it (via
	// entity_relation.to_media_id)? One batch query covers both tiers.
	allTargets := make(map[string]struct{}, len(directTargets)+len(broadTargets))
	for id := range directTargets {
		allTargets[id] = struct{}{}
	}
	for id := range broadTargets {
		allTargets[id] = struct{}{}
	}
	if len(allTargets) == 0 {
		return reach, nil
	}
	relatedByMedia, err := s.entitiesRelatingMediaLocked(ctx, candidates, allTargets)
	if err != nil {
		return reach, err
	}

	for media, related := range relatedByMedia {
		for entityID := range related {
			if _, ok := directTargets[entityID]; ok {
				reach.directlyMuted[media] = true
			}
			if _, ok := broadTargets[entityID]; ok {
				reach.broadlyMuted[media] = true
			}
		}
	}
	return reach, nil
}

func (s *Store) applyMuteFilterLocked(ctx context.Context, profileID ProfileID, filter LibraryFilter, items []SourceItem) ([]SourceItem, error) {
	if len(items) == 0 {
		return items, nil
	}
	reach, err := s.mutedReachLocked(ctx, profileID, items)
	if err != nil {
		return nil, err
	}
	directlyMuted := reach.directlyMuted
	broadlyMuted := reach.broadlyMuted
	if len(directlyMuted) == 0 && len(broadlyMuted) == 0 {
		return items, nil
	}

	// A Media hit by a direct mute drops unconditionally. A Media hit only by a
	// broad mute drops unless protected by a positive signal. Compute the
	// protection set lazily — only for Media that need it.
	needProtection := make([]MediaID, 0, len(broadlyMuted))
	for media := range broadlyMuted {
		if directlyMuted[media] {
			continue // direct mute already decides it; no override possible
		}
		needProtection = append(needProtection, media)
	}
	protected := map[MediaID]bool{}
	if len(needProtection) > 0 {
		protected, err = s.positiveSignalMediaLocked(ctx, profileID, filter, needProtection)
		if err != nil {
			return nil, err
		}
	}

	out := items[:0:0]
	for _, item := range items {
		if directlyMuted[item.ID] {
			continue
		}
		if broadlyMuted[item.ID] && !protected[item.ID] {
			continue
		}
		out = append(out, item)
	}
	if out == nil {
		out = []SourceItem{}
	}
	return out, nil
}

// AnnotateMuted marks, on each surfaced SourceItem, whether the acting Profile's
// Per-Profile Mute reaches it — the wire signal for the "Muted" badge
// + inline Unmute control (§2.7). It is the deliberate-surface counterpart to the
// passive-rail suppressor applyMuteFilterLocked: badge, don't remove. Badging is
// by mute REACH, NOT the suppression drop set — §2.7 requires a Favorite-
// overridden broad-mute card to still carry the badge, so no positive-signal
// protection is applied here. An empty profileID is a no-op (a profile-less rail
// has no mute list). Safe to call on any SourceItem slice from search, direct
// navigation, or a browse rail.
func (s *Store) AnnotateMuted(ctx context.Context, profileID ProfileID, filter LibraryFilter, items []SourceItem) error {
	if profileID == "" || len(items) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.annotateMutedLocked(ctx, profileID, items)
}

// annotateMutedLocked is AnnotateMuted's in-lock body. Kind-aware: an entity-kind
// item badges when its entity id is a direct or broad ENTITY mute target; a
// media-kind item badges when any mute target (entity or tag) reaches its Media,
// mirroring the id-pooled suppression reach exactly.
func (s *Store) annotateMutedLocked(ctx context.Context, profileID ProfileID, items []SourceItem) error {
	reach, err := s.mutedReachLocked(ctx, profileID, items)
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].Kind == "entity" {
			id := string(items[i].ID)
			_, direct := reach.directEntities[id]
			_, broad := reach.broadEntities[id]
			if direct || broad {
				items[i].Muted = true
			}
			continue
		}
		if reach.directlyMuted[items[i].ID] || reach.broadlyMuted[items[i].ID] {
			items[i].Muted = true
		}
	}
	return nil
}

// mutedTargetsLocked is MutedTargets without the lock — the read helper the
// in-lock predicate path uses.
func (s *Store) mutedTargetsLocked(ctx context.Context, profileID ProfileID) ([]MuteTarget, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT target_kind, target_id, include_descendants, muted_at_ms
		FROM profile_mute
		WHERE profile_id = ?
		ORDER BY muted_at_ms DESC, target_kind, target_id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MuteTarget
	for rows.Next() {
		var (
			kind     string
			targetID string
			incDesc  int
			mutedMS  int64
		)
		if err := rows.Scan(&kind, &targetID, &incDesc, &mutedMS); err != nil {
			return nil, err
		}
		out = append(out, MuteTarget{
			Kind:               MuteKind(kind),
			TargetID:           targetID,
			IncludeDescendants: incDesc != 0,
			MutedAt:            millisToTime(mutedMS),
		})
	}
	return out, rows.Err()
}

// entityDescendantsLocked returns the seed entity ids plus every entity reachable
// from them by following entity_relation.from_entity_id -> to_entity_id edges
// (the generic hierarchy walk for include-descendants, ADR-0054 §2.3). It is
// cycle-safe (visited set) and depth-capped (muteDescendantDepthCap). The walk is
// domain-blind: it follows the generic entity graph regardless of relation name,
// so no plugin domain vocabulary leaks into Core.
func (s *Store) entityDescendantsLocked(ctx context.Context, seeds []string) (map[string]struct{}, error) {
	visited := map[string]struct{}{}
	frontier := make([]string, 0, len(seeds))
	for _, id := range seeds {
		if id == "" {
			continue
		}
		if _, ok := visited[id]; !ok {
			visited[id] = struct{}{}
			frontier = append(frontier, id)
		}
	}
	for depth := 0; depth < muteDescendantDepthCap && len(frontier) > 0; depth++ {
		placeholders := make([]string, len(frontier))
		args := make([]any, len(frontier))
		for i, id := range frontier {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := s.catalogR.QueryContext(ctx, `
			SELECT DISTINCT to_entity_id
			FROM entity_relation
			WHERE to_entity_id IS NOT NULL
			  AND from_entity_id IN (`+joinComma(placeholders)+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		next := frontier[:0:0]
		for rows.Next() {
			var child string
			if err := rows.Scan(&child); err != nil {
				rows.Close()
				return nil, err
			}
			if _, ok := visited[child]; !ok {
				visited[child] = struct{}{}
				next = append(next, child)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		frontier = next
	}
	return visited, nil
}

// entitiesRelatingMediaLocked maps each candidate Media to the subset of target
// entity ids that relate to it via entity_relation.to_media_id. Only the given
// target ids are considered, so the result is exactly "which muted entities
// reach this Media." Media with no muted relation are absent from the map.
func (s *Store) entitiesRelatingMediaLocked(ctx context.Context, media []MediaID, targets map[string]struct{}) (map[MediaID]map[string]struct{}, error) {
	out := map[MediaID]map[string]struct{}{}
	if len(media) == 0 || len(targets) == 0 {
		return out, nil
	}
	targetList := make([]string, 0, len(targets))
	for id := range targets {
		targetList = append(targetList, id)
	}
	const chunkSize = 400
	for start := 0; start < len(media); start += chunkSize {
		chunk := media[start:min(start+chunkSize, len(media))]
		args := make([]any, 0, len(chunk)+len(targetList))
		mediaPlaceholders := make([]string, len(chunk))
		for i, id := range chunk {
			mediaPlaceholders[i] = "?"
			args = append(args, id)
		}
		targetPlaceholders := make([]string, len(targetList))
		for i, id := range targetList {
			targetPlaceholders[i] = "?"
			args = append(args, id)
		}
		rows, err := s.catalogR.QueryContext(ctx, `
			SELECT DISTINCT to_media_id, from_entity_id
			FROM entity_relation
			WHERE to_media_id IN (`+joinComma(mediaPlaceholders)+`)
			  AND from_entity_id IN (`+joinComma(targetPlaceholders)+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var mediaID MediaID
			var entityID string
			if err := rows.Scan(&mediaID, &entityID); err != nil {
				rows.Close()
				return nil, err
			}
			set := out[mediaID]
			if set == nil {
				set = map[string]struct{}{}
				out[mediaID] = set
			}
			set[entityID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// positiveSignalMediaLocked returns the subset of the given Media that carry an
// explicit positive signal for the Profile strong enough to override a broad
// category Mute: a Favorite (media_favorite) or active
// continue-watching (an in-progress playback_progress row past the resume
// floor). Both live in the events DB. Collection membership is the third named
// signal but is not server-resolvable in the current schema (see
// applyMuteFilterLocked) and is therefore not consulted.
func (s *Store) positiveSignalMediaLocked(ctx context.Context, profileID ProfileID, filter LibraryFilter, media []MediaID) (map[MediaID]bool, error) {
	out := map[MediaID]bool{}
	if len(media) == 0 {
		return out, nil
	}
	const chunkSize = 400
	for start := 0; start < len(media); start += chunkSize {
		chunk := media[start:min(start+chunkSize, len(media))]
		placeholders := make([]string, len(chunk))
		favArgs := make([]any, 0, len(chunk)+1)
		favArgs = append(favArgs, profileID)
		for i, id := range chunk {
			placeholders[i] = "?"
			favArgs = append(favArgs, id)
		}
		favRows, err := s.eventsR.QueryContext(ctx, `
			SELECT media_id FROM media_favorite
			WHERE profile_id = ? AND media_id IN (`+joinComma(placeholders)+`)
		`, favArgs...)
		if err != nil {
			return nil, err
		}
		for favRows.Next() {
			var id MediaID
			if err := favRows.Scan(&id); err != nil {
				favRows.Close()
				return nil, err
			}
			out[id] = true
		}
		if err := favRows.Err(); err != nil {
			favRows.Close()
			return nil, err
		}
		favRows.Close()

		cwArgs := make([]any, 0, len(chunk)+2)
		cwArgs = append(cwArgs, profileID, resumeFloorSeconds)
		for _, id := range chunk {
			cwArgs = append(cwArgs, id)
		}
		cwRows, err := s.eventsR.QueryContext(ctx, `
			SELECT media_id FROM playback_progress
			WHERE profile_id = ? AND watched = 0 AND position_seconds >= ?
			  AND media_id IN (`+joinComma(placeholders)+`)
		`, cwArgs...)
		if err != nil {
			return nil, err
		}
		for cwRows.Next() {
			var id MediaID
			if err := cwRows.Scan(&id); err != nil {
				cwRows.Close()
				return nil, err
			}
			out[id] = true
		}
		if err := cwRows.Err(); err != nil {
			cwRows.Close()
			return nil, err
		}
		cwRows.Close()
	}
	return out, nil
}

// joinComma joins placeholder fragments with ", " — the local helper the mute
// predicate's batch queries share.
func joinComma(parts []string) string {
	return strings.Join(parts, ", ")
}

// ClearMutes removes every Mute for a Profile — the My-Data "clear all" reset.
func (s *Store) ClearMutes(ctx context.Context, profileID ProfileID) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.eventsW.ExecContext(ctx, `DELETE FROM profile_mute WHERE profile_id = ?`, profileID)
	return err
}

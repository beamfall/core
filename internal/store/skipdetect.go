package store

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"
)

// Skip-segment detection store surface (ENR-7 / ADR-0048 Decision 3/4/6). The
// detector is Core's byte-level job that emits `detected` skip-segments; this
// file holds the three store primitives it needs that markers.go and the
// generic relation graph did not already provide:
//
//   - the per-Library detection toggle (default OFF), held in the existing
//     server_config key-value table so it needs NO new column/migration;
//   - the generic "ordered sibling Media" walk (the comparison group for
//     cross-Media fingerprinting) over entity_relation — named by shape
//     (a common ordered container), never domain (invariant #1);
//   - the idempotent, manual-respecting `detected` segment write the detector
//     uses so a re-run replaces only its own prior detected output.

// skipDetectionLibraryKeyPrefix namespaces the per-Library detection toggle
// inside the shared server_config table. The full key is the prefix plus the
// Library ID; an absent row means the default (OFF) — the operator opts a
// Library in, matching the ADR-0048 Decision 6 "default off" stance and the
// other opt-in features (Enrichment, update-check) that key off this table.
// Keying by Library inside one table (rather than a new library column) is what
// keeps the FROZEN schema untouched.
const skipDetectionLibraryKeyPrefix = "skipdetection.library."

func skipDetectionLibraryKey(id LibraryID) string {
	return skipDetectionLibraryKeyPrefix + string(id)
}

// SkipDetectionEnabled reports whether the Library has opted into skip-segment
// detection. Default OFF: an absent row, or any value
// other than "1", means the operator has not enabled detection for this
// Library, so the detector skips every Media in it.
func (s *Store) SkipDetectionEnabled(ctx context.Context, id LibraryID) (bool, error) {
	if id == "" {
		return false, ValidationError("library ID is required")
	}
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return false, err
	}
	return values[skipDetectionLibraryKey(id)] == "1", nil
}

// SetSkipDetectionEnabled flips the per-Library detection toggle (ADR-0048
// Decision 6). Turning it off does NOT remove already-detected segments — they
// are harmless at rest and the operator can suppress individually; it only
// stops the detector from processing the Library on the next run. The Library
// must exist (an unknown ID is a ValidationError) so a typo can never silently
// write a dangling toggle row.
func (s *Store) SetSkipDetectionEnabled(ctx context.Context, id LibraryID, enabled bool, now time.Time) error {
	if id == "" {
		return ValidationError("library ID is required")
	}
	if err := s.libraryExists(ctx, id); err != nil {
		return err
	}
	return s.setServerConfigValues(ctx, map[string]string{
		skipDetectionLibraryKey(id): boolToConfig(enabled),
	}, now)
}

// libraryExists returns nil when a Library with the given ID exists, else a
// ValidationError — the guard SetSkipDetectionEnabled uses so a toggle row
// never precedes or outlives its Library.
func (s *Store) libraryExists(ctx context.Context, id LibraryID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	var one int
	if err := s.catalogR.QueryRowContext(ctx, `SELECT 1 FROM library WHERE id = ?`, id).Scan(&one); err != nil {
		return ValidationError("library not found")
	}
	return nil
}

// OrderedSiblingMedia returns the other Media that share an ordered container
// with the given Media — the comparison group cross-Media fingerprinting needs.
// It is deliberately domain-agnostic: an "ordered
// container" is any source entity that relates to this Media with a non-null
// sort_order (the same generic shape next-up and the entity-media Source read,
// engagement.go); the siblings are the OTHER Media that entity orders. Whether
// that entity models a TV-style container, an ordered Collection, or anything
// else is plugin knowledge Core never holds (invariant #1). The given Media is
// excluded; the result is de-duplicated and stably sorted by Media ID. A Media
// with no ordered container (e.g. a standalone film) returns an empty slice —
// fingerprinting simply has no siblings to compare, and chapter-import still
// applies.
func (s *Store) OrderedSiblingMedia(ctx context.Context, mediaID MediaID) ([]MediaID, error) {
	if mediaID == "" {
		return nil, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	// Sibling = a Media that shares a (container entity, relation) pairing with
	// this Media, where this Media's row carries a sort_order (the ordered-
	// container marker). The self-join keeps both rows in the same ordered
	// container; the sibling row need not itself be ordered, but in practice an
	// ordered container orders all its children.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT DISTINCT sibling.to_media_id
		FROM entity_relation self
		JOIN entity_relation sibling
		  ON sibling.from_entity_id = self.from_entity_id
		 AND sibling.relation = self.relation
		WHERE self.to_media_id = ?
		  AND self.sort_order IS NOT NULL
		  AND sibling.to_media_id IS NOT NULL
		  AND sibling.to_media_id <> ?
	`, mediaID, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var siblings []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		siblings = append(siblings, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(siblings, func(i, j int) bool { return siblings[i] < siblings[j] })
	return siblings, nil
}

// PriorOrderedSiblingMedia returns the Media positioned before mediaID in any
// shared ordered container. Unlike OrderedSiblingMedia, this preserves the
// generic relation order needed by reverse fingerprinting: only earlier
// members are eligible evidence. Results are de-duplicated when two containers
// relate the same pair.
func (s *Store) PriorOrderedSiblingMedia(ctx context.Context, mediaID MediaID) ([]MediaID, error) {
	if mediaID == "" {
		return nil, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT sibling.to_media_id
		FROM entity_relation self
		JOIN entity_relation sibling
		  ON sibling.from_entity_id = self.from_entity_id
		 AND sibling.relation = self.relation
		WHERE self.to_media_id = ?
		  AND self.sort_order IS NOT NULL
		  AND sibling.to_media_id IS NOT NULL
		  AND sibling.sort_order IS NOT NULL
		  AND sibling.sort_order < self.sort_order
		ORDER BY sibling.sort_order, sibling.to_media_id
	`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := make(map[MediaID]bool)
	var siblings []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		siblings = append(siblings, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return siblings, nil
}

// ReplaceDetectedSegment idempotently writes one `detected` skip-segment for
// (mediaID, kind), enforcing the ADR-0048 Decision 5 reconciliation rules so
// the detector can call it blindly:
//
//   - manual always wins: if a `manual` segment of this kind exists, the write
//     is a no-op (the user's segment stands) and returns false, nil;
//   - suppression wins: if (mediaID, kind) is suppressed, the write is a no-op
//     and returns false, nil — re-detection never resurrects a deleted segment;
//   - idempotent over its own output: any prior `detected` segment of this kind
//     is replaced (deleted then re-inserted), so a re-run neither duplicates nor
//     drifts; an unchanged re-run lands the same row.
//
// kind must be a writable SegmentKind; start/end define the range (end >
// start). method and confidence are recorded verbatim (confidence in [0,1]).
// Returns true when a detected row was written, false when a guard suppressed
// it. The caller (the detector) owns the confidence GATE — it only calls this
// for segments at or above the auto-apply threshold; below-threshold segments
// are dropped before reaching here (Decision 5: "below it, drop").
func (s *Store) ReplaceDetectedSegment(ctx context.Context, mediaID MediaID, kind string, startSeconds, endSeconds float64, method string, confidence float64, flavor SegmentFlavor, now time.Time) (bool, error) {
	if mediaID == "" {
		return false, ValidationError("media ID is required")
	}
	if !slices.Contains(SegmentKinds, kind) {
		return false, ValidationError("unknown segment kind " + kind)
	}
	if startSeconds < 0 {
		return false, ValidationError("segment start must be >= 0")
	}
	if endSeconds <= startSeconds {
		return false, ValidationError("segment end must be after start")
	}
	if confidence < 0 || confidence > 1 {
		return false, ValidationError("segment confidence must be in [0, 1]")
	}
	if strings.TrimSpace(method) == "" {
		return false, ValidationError("detected segment requires a method")
	}
	if !slices.Contains(segmentFlavors, flavor) {
		return false, ValidationError("unknown segment flavor " + string(flavor))
	}
	if err := validateDetectedFlavor(kind, flavor); err != nil {
		return false, err
	}
	// A host-read ad is NOT an error here — it is the "admit nothing" outcome.
	// Returning early would skip the replace below, leaving an earlier, live,
	// auto-skippable `inserted` row for the same (media, kind) when a re-run
	// reclassifies it. So host-read takes the same path, minus the INSERT.
	admit := flavor != FlavorHostRead

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	// Suppression lock: a (mediaID, kind) the user deleted-and-suppressed is
	// never resurrected by detection (Decision 5).
	var suppressed int
	if err := s.catalogR.QueryRowContext(ctx,
		`SELECT 1 FROM marker_suppression WHERE media_id = ? AND segment_kind = ?`,
		mediaID, kind).Scan(&suppressed); err == nil {
		return false, nil
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	// Manual always wins: if any manual segment of this kind exists, detection
	// stands aside entirely (Decision 5). Detected rows of this kind are the
	// detector's own prior output — replace them.
	var manualCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM marker WHERE media_id = ? AND segment_kind = ? AND provenance = ?`,
		mediaID, kind, ProvenanceManual).Scan(&manualCount); err != nil {
		return false, err
	}
	if manualCount > 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM marker WHERE media_id = ? AND segment_kind = ? AND provenance = ?`,
		mediaID, kind, ProvenanceDetected); err != nil {
		return false, err
	}
	if !admit {
		return false, tx.Commit()
	}
	id, err := randomMarkerID()
	if err != nil {
		return false, err
	}
	nowMS := now.UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO marker(id, media_id, title, start_seconds, end_seconds, segment_kind, provenance, method, confidence, flavor, created_by_profile_id, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, mediaID, "", startSeconds, endSeconds, kind,
		string(ProvenanceDetected), method, confidence, string(flavor), nil, nowMS, nowMS); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// SegmentDetection is one internal detection record: what the detector saw,
// independent of what became a served Marker. Sub-bar
// `host-read` ads never become Markers, but their flavor and confidence are
// still "recorded for eval" — this is where they live. Nothing serializes this
// shape: no graph field, no wire schema, no dispatch entry reads it.
type SegmentDetection struct {
	MediaID      MediaID
	SegmentKind  string
	StartSeconds float64
	EndSeconds   float64
	// Method names the detector that produced this record (the ADR-0193 audio
	// ad detector, for an `ad`). Free-form: ADR-0048's chapter-import and
	// fingerprint methods and ADR-0179's classifier share this seam.
	Method string
	// Flavor is FlavorNone for every kind except `ad`, where it is required.
	Flavor SegmentFlavor
	// Confidence is the detector's [0,1] score for this flavor.
	Confidence float64
}

// RecordSegmentDetection stores one detection as internal evaluation evidence.
// It is deliberately separate from ReplaceDetectedSegment: this seam records
// WHAT WAS MEASURED (both ad flavors), that one decides WHAT IS SERVED (never a
// host-read ad). The detector calls both; neither calls the other, so the two
// contracts stay independently testable. Reveal-gated like every Media-keyed
// write.
func (s *Store) RecordSegmentDetection(ctx context.Context, detection SegmentDetection, now time.Time) error {
	if err := validateSegmentDetection(detection); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.visibleMediaSQL(ctx, detection.MediaID, LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		return ErrMarkerNotFound
	}
	id, err := randomMarkerID()
	if err != nil {
		return err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO segment_detection(id, media_id, segment_kind, start_seconds, end_seconds, method, flavor, confidence, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, string(id), detection.MediaID, detection.SegmentKind, detection.StartSeconds,
		detection.EndSeconds, detection.Method, string(detection.Flavor), detection.Confidence,
		now.UTC().UnixMilli())
	return err
}

// validateSegmentDetection checks one detection record's fields. Each guard is
// one condition; the flavor rules are the same two-way `ad` correspondence the
// Marker path enforces.
func validateSegmentDetection(detection SegmentDetection) error {
	if detection.MediaID == "" {
		return ValidationError("media ID is required")
	}
	if !slices.Contains(SegmentKinds, detection.SegmentKind) {
		return ValidationError("unknown segment kind " + detection.SegmentKind)
	}
	if detection.StartSeconds < 0 {
		return ValidationError("segment start must be >= 0")
	}
	if detection.EndSeconds <= detection.StartSeconds {
		return ValidationError("segment end must be after start")
	}
	if strings.TrimSpace(detection.Method) == "" {
		return ValidationError("detected segment requires a method")
	}
	if !slices.Contains(segmentFlavors, detection.Flavor) {
		return ValidationError("unknown segment flavor " + string(detection.Flavor))
	}
	if detection.Confidence < 0 || detection.Confidence > 1 {
		return ValidationError("segment confidence must be in [0, 1]")
	}
	return validateDetectedFlavor(detection.SegmentKind, detection.Flavor)
}

// SegmentDetections returns one Media's internal detection records, ordered by
// start time then ID so the result is deterministic rather than dependent on
// SQLite's incidental row order. Internal/eval read only — no wire path reaches
// it.
func (s *Store) SegmentDetections(ctx context.Context, mediaID MediaID) ([]SegmentDetection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT media_id, segment_kind, start_seconds, end_seconds, method, flavor, confidence
		FROM segment_detection WHERE media_id = ? ORDER BY start_seconds, id
	`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	detections := []SegmentDetection{}
	for rows.Next() {
		var detection SegmentDetection
		var flavor string
		if err := rows.Scan(&detection.MediaID, &detection.SegmentKind, &detection.StartSeconds,
			&detection.EndSeconds, &detection.Method, &flavor, &detection.Confidence); err != nil {
			return nil, err
		}
		detection.Flavor = SegmentFlavor(flavor)
		detections = append(detections, detection)
	}
	return detections, rows.Err()
}

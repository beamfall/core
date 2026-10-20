package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
)

// ErrMarkerNotFound means no Marker exists with the given ID — or none the
// caller can see; hidden and missing are deliberately indistinguishable.
var ErrMarkerNotFound = errors.New("marker not found")

// ErrManualSegmentWins reports a rejected "detected" write that would have
// overwritten a user-authored/edited "manual" skip-segment (ADR-0048 Decision
// 5: manual always wins). The detector treats this as a no-op, not a failure.
var ErrManualSegmentWins = errors.New("manual segment wins; detection cannot overwrite it")

// ErrHostReadNotSurfaced reports a rejected write of a `host-read` ad segment
// into the served Marker table. A surfaced `ad` marker is
// one the client WILL auto-skip — auto-skip keys on segment kind alone and the
// served shape carries no per-marker gate — so a host-read ad, whose boundaries
// are too imprecise to clear the auto-apply bar, is "dropped, not surfaced" in
// v1. Its flavor and confidence still survive, in segment_detection.
var ErrHostReadNotSurfaced = errors.New("host-read ad segments are recorded internally, never served as markers")

// MarkerID is a stable Marker identifier.
type MarkerID string

// SegmentKindAd is the ADR-0193 Decision 2 skip-segment kind for an
// advertisement break on an item's timeline. Named by shape, never by domain
// (invariant #1). It is Core-DETECTED only: no external caller may author one.
const SegmentKindAd = "ad"

// SegmentKinds is the closed skip-segment vocabulary — ADR-0048 Decision 1
// narrowed the ADR-0027 five-kind set to the three auto-skippable shapes
// (intro / recap / credits), and ADR-0193 Decision 2 added a fourth, `ad`.
// Named by shape, never domain (invariant #1); distinct from free-form marker
// tags. Grown rarely by ADR amendment; clients ignore unknown kinds. The set is
// SCHEMA-enforced: the segment_kind CHECK (migrations 0068, 0099) rejects
// anything outside it. `commercial` stays deferred, and `sponsor` is reserved,
// not a live kind; the amendment that ships it will bind its name and migration.
var SegmentKinds = []string{"intro", "recap", "credits", SegmentKindAd}

// ExternallyWritableSegmentKinds is the subset of SegmentKinds a client may
// AUTHOR. It stays the three ADR-0048 kinds: `ad` is "Core-detected only, not
// externally writable" (ADR-0193 / PODAD epic), so the external-write
// "no new kinds" rule of ADR-0191 still holds even though the
// readable vocabulary is now four. Detection writes `ad` through
// ReplaceDetectedSegment; editing an existing detected `ad` still promotes it to
// manual, which is why the guard keys on the kind of the
// row being replaced, not on provenance alone.
var ExternallyWritableSegmentKinds = []string{"intro", "recap", "credits"}

// detectorAuthoredKind reports whether a segment kind may only be authored by
// Core's detector. Written as the positive test rather than as "not externally
// writable": the negated form is true for "", and an empty SegmentKind is valid
// for a title-only or tag-only Marker, so it would refuse ordinary creates.
func detectorAuthoredKind(kind string) bool {
	return kind == SegmentKindAd
}

// validateDetectedFlavor enforces the two-way correspondence ADR-0193 Decision 2
// draws between the `ad` kind and detection flavor: a flavor belongs only to an
// `ad`, and an `ad` detection always carries one (its per-flavor confidence is
// meaningless otherwise). The caller has already checked the flavor is in the
// closed vocabulary.
func validateDetectedFlavor(kind string, flavor SegmentFlavor) error {
	if flavor != FlavorNone && kind != SegmentKindAd {
		return ValidationError("segment flavor is only valid on the " + SegmentKindAd + " kind")
	}
	if flavor == FlavorNone && kind == SegmentKindAd {
		return ValidationError("detected " + SegmentKindAd + " segments require a flavor")
	}
	return nil
}

// Provenance records where a skip-segment Marker came from (ADR-0048 Decision
// 5): user-authored/edited vs Core's byte-level detector. Manual always wins —
// a detected write never overwrites a manual segment.
type Provenance string

const (
	// ProvenanceManual is a user-authored or user-edited segment; certain by
	// definition (confidence 0, no method) and locked from detection.
	ProvenanceManual Provenance = "manual"
	// ProvenanceDetected is a Core-detected segment carrying its method and a
	// [0,1] confidence; idempotently replaceable by its own re-detection.
	ProvenanceDetected Provenance = "detected"
)

// SegmentFlavor is the INTERNAL detection provenance of an `ad` skip-segment:
// whether the break was dynamically inserted or read
// aloud by the host. It is detector bookkeeping, NEVER client wire — the served
// marker shape carries no flavor, and auto-skip keys on the segment KIND alone
// (one `ad` kind, one `autoskip.ad` boolean). Confidence on a flavored marker is
// the detector's score FOR THAT FLAVOR; one flavor per segment, so the existing
// confidence field already is the per-flavor confidence.
type SegmentFlavor string

const (
	// FlavorNone is the absence of a flavor — every kind except `ad`.
	FlavorNone SegmentFlavor = ""
	// FlavorInserted is a dynamically-inserted ad. Its boundaries are sharp, so
	// it clears the auto-apply bar and IS served as an `ad` marker.
	FlavorInserted SegmentFlavor = "inserted"
	// FlavorHostRead is a host-read ad. Its boundaries are gradual, so in v1 it
	// falls below the bar and is dropped, not surfaced:
	// it is recorded in segment_detection for evaluation and never becomes a
	// Marker. The amendment that promotes host-read reverses only that rule.
	FlavorHostRead SegmentFlavor = "host-read"
)

// segmentFlavors is the closed flavor vocabulary, for validation.
var segmentFlavors = []SegmentFlavor{FlavorNone, FlavorInserted, FlavorHostRead}

// Marker is a named point or range on one Media item's canonical timeline,
// carrying free-form tags. StartSeconds alone is a point marker; EndSeconds
// makes it a range. A marker needs at least one display handle: a title, a
// segment kind, or a tag (an untitled marker displays its first tag).
type Marker struct {
	ID           MarkerID
	MediaID      MediaID
	Title        string
	StartSeconds float64
	// EndSeconds is nil for point markers; ranges require end > start.
	EndSeconds *float64
	// SegmentKind is "" or one of SegmentKinds; the segment_kind CHECK
	// (migration 0068) enforces the same narrowed set at the schema layer.
	SegmentKind string
	// Provenance is "manual" or "detected". Empty on an
	// incoming write defaults to manual — every CRUD write is user-authored;
	// only the detector writes "detected". Manual always wins.
	Provenance Provenance
	// Method names the detector that produced a "detected" segment (e.g.
	// chapter-import, fingerprint); "" for manual. Confidence is the detector's
	// [0,1] score; 0 for manual.
	Method     string
	Confidence float64
	// Flavor is the internal detection provenance of a detected `ad` segment.
	// FlavorNone for every other kind and for manual
	// segments. It never reaches a client: no served marker shape carries it.
	Flavor SegmentFlavor
	Tags   []string
	// CreatedByProfile records which Profile authored this Marker (ADR-0036
	// §2.4). It is set at create time so My-Data export can surface the
	// Profile's authored Markers and My-Data delete can DISOWN them (clear the
	// column) rather than delete the shared row. Empty for operator-created or
	// legacy Markers, which have no authoring Profile.
	CreatedByProfile ProfileID
	CreatedAt        time.Time
	UpdatedAt        time.Time
	TrashedAt        time.Time
}

// validateMarker checks one Marker write's user-supplied fields, normalizing
// title and tags in place (trimmed, sorted, no blanks or duplicates).
func validateMarker(marker *Marker) error {
	marker.Title = strings.TrimSpace(marker.Title)
	if marker.StartSeconds < 0 {
		return ValidationError("marker start must be >= 0")
	}
	if marker.EndSeconds != nil && *marker.EndSeconds <= marker.StartSeconds {
		return ValidationError("marker end must be after start")
	}
	if marker.SegmentKind != "" && !slices.Contains(SegmentKinds, marker.SegmentKind) {
		// The ADR-0027 preview/commercial kinds were deferred by ADR-0048 and
		// removed from the schema (migration 0068); they are now just unknown.
		return ValidationError("unknown segment kind " + marker.SegmentKind)
	}
	if marker.Provenance == "" {
		marker.Provenance = ProvenanceManual
	}
	if marker.Provenance != ProvenanceManual && marker.Provenance != ProvenanceDetected {
		return ValidationError("unknown segment provenance " + string(marker.Provenance))
	}
	if !slices.Contains(segmentFlavors, marker.Flavor) {
		return ValidationError("unknown segment flavor " + string(marker.Flavor))
	}
	if marker.Provenance == ProvenanceManual {
		// A hand-authored segment is certain by definition; clear detector fields
		// so a manual write can never masquerade as carrying a method/score.
		// Flavor clears with them: editing a detected `ad` promotes it to manual
		// and the incoming struct still carries the
		// detector's flavor, so rejecting here would fail a legitimate user edit.
		marker.Method = ""
		marker.Confidence = 0
		marker.Flavor = FlavorNone
	} else {
		if marker.Confidence < 0 || marker.Confidence > 1 {
			return ValidationError("segment confidence must be in [0, 1]")
		}
		if err := validateDetectedFlavor(marker.SegmentKind, marker.Flavor); err != nil {
			return err
		}
		if marker.Flavor == FlavorHostRead {
			return ErrHostReadNotSurfaced
		}
	}
	tags := make([]string, 0, len(marker.Tags))
	seen := map[string]bool{}
	for _, tag := range marker.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return ValidationError("marker tags must not be blank")
		}
		if seen[tag] {
			return ValidationError("duplicate marker tag " + tag)
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	marker.Tags = tags
	if marker.Title == "" && marker.SegmentKind == "" && len(marker.Tags) == 0 {
		return ValidationError("marker needs a title, a segment kind, or a tag")
	}
	return nil
}

// CreateMarker inserts a Marker on a Media item the caller can see — the
// write is reveal-gated the same way the read is (a hidden item's timeline
// must not accept annotations). The ID is server-issued.
func (s *Store) CreateMarker(ctx context.Context, marker Marker, filter LibraryFilter, now time.Time) (Marker, error) {
	if marker.MediaID == "" {
		return Marker{}, ValidationError("media ID is required")
	}
	if detectorAuthoredKind(marker.SegmentKind) {
		// ADR-0193 / PODAD: `ad` is Core-detected only, never externally
		// authored. createMarker forwards a client-supplied segmentKind here.
		return Marker{}, ValidationError("segment kind " + marker.SegmentKind + " is detector-authored only")
	}
	if err := validateMarker(&marker); err != nil {
		return Marker{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Marker{}, err
	}
	if _, err := s.visibleMediaSQL(ctx, marker.MediaID, filter); err != nil {
		return Marker{}, err
	}
	id, err := randomMarkerID()
	if err != nil {
		return Marker{}, err
	}
	marker.ID = id
	marker.CreatedAt = now.UTC()
	marker.UpdatedAt = marker.CreatedAt

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Marker{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO marker(id, media_id, title, start_seconds, end_seconds, segment_kind, provenance, method, confidence, flavor, created_by_profile_id, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, marker.ID, marker.MediaID, marker.Title, marker.StartSeconds,
		nullFloat(marker.EndSeconds), nullString(marker.SegmentKind),
		string(marker.Provenance), marker.Method, marker.Confidence, string(marker.Flavor),
		nullString(string(marker.CreatedByProfile)),
		marker.CreatedAt.UnixMilli(), marker.UpdatedAt.UnixMilli()); err != nil {
		return Marker{}, err
	}
	if err := insertMarkerTags(ctx, tx, marker.ID, marker.Tags); err != nil {
		return Marker{}, err
	}
	if err := tx.Commit(); err != nil {
		return Marker{}, err
	}
	return marker, nil
}

// UpdateMarker fully replaces a Marker's mutable fields (title, start, end,
// segment kind, tags) — the Marker's Media binding never changes. Gated on
// the current visibility of the Marker's Media, like every Marker access.
func (s *Store) UpdateMarker(ctx context.Context, marker Marker, filter LibraryFilter, now time.Time) (Marker, error) {
	if marker.ID == "" {
		return Marker{}, ValidationError("marker ID is required")
	}
	if err := validateMarker(&marker); err != nil {
		return Marker{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Marker{}, err
	}
	existing, err := s.visibleMarkerSQL(ctx, marker.ID, filter)
	if err != nil {
		return Marker{}, err
	}
	// Manual always wins: the detector ("detected" write)
	// must never clobber a user-authored/edited ("manual") segment. A manual
	// write — every CRUD edit — always proceeds and, by editing a detected
	// segment, promotes it to manual (the default provenance on an unset write).
	if marker.Provenance == ProvenanceDetected && existing.Provenance == ProvenanceManual {
		return Marker{}, ErrManualSegmentWins
	}
	// `ad` is detector-authored: an edit may keep an existing `ad`
	// (that is the ADR-0048 D5 promote-to-manual path) but may never re-label
	// another kind INTO one.
	if detectorAuthoredKind(marker.SegmentKind) && existing.SegmentKind != marker.SegmentKind {
		return Marker{}, ValidationError("segment kind " + marker.SegmentKind + " is detector-authored only")
	}
	marker.MediaID = existing.MediaID
	marker.CreatedAt = existing.CreatedAt
	marker.UpdatedAt = now.UTC()

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Marker{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE marker SET title = ?, start_seconds = ?, end_seconds = ?, segment_kind = ?, provenance = ?, method = ?, confidence = ?, flavor = ?, updated_at_ms = ?,
			detection_run_id = CASE WHEN ? = 'manual' THEN NULL ELSE detection_run_id END,
			detection_candidate_ordinal = CASE WHEN ? = 'manual' THEN NULL ELSE detection_candidate_ordinal END
		WHERE id = ?
	`, marker.Title, marker.StartSeconds, nullFloat(marker.EndSeconds),
		nullString(marker.SegmentKind), string(marker.Provenance), marker.Method, marker.Confidence,
		string(marker.Flavor), marker.UpdatedAt.UnixMilli(), string(marker.Provenance),
		string(marker.Provenance), marker.ID); err != nil {
		return Marker{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM marker_tag WHERE marker_id = ?`, marker.ID); err != nil {
		return Marker{}, err
	}
	if err := insertMarkerTags(ctx, tx, marker.ID, marker.Tags); err != nil {
		return Marker{}, err
	}
	if err := tx.Commit(); err != nil {
		return Marker{}, err
	}
	return marker, nil
}

// MarkersForMedia returns one visible Media item's Markers ordered by start
// time. Reveal-filtered like every read.
func (s *Store) MarkersForMedia(ctx context.Context, id MediaID, filter LibraryFilter) ([]Marker, error) {
	if id == "" {
		return nil, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQL(ctx, id, filter); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx,
		markerColumns+` WHERE media_id = ? AND trashed_at_ms IS NULL ORDER BY start_seconds, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	markers := []Marker{}
	for rows.Next() {
		marker, err := scanMarkerRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		markers = append(markers, marker)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range markers {
		if markers[i].Tags, err = s.markerTagsSQL(ctx, markers[i].ID); err != nil {
			return nil, err
		}
	}
	return markers, nil
}

// TrashedMarkersForMedia returns soft-deleted Markers for one visible Media
// item, newest deleted first.
func (s *Store) TrashedMarkersForMedia(ctx context.Context, id MediaID, filter LibraryFilter) ([]Marker, error) {
	if id == "" {
		return nil, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQL(ctx, id, filter); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx,
		markerColumns+` WHERE media_id = ? AND trashed_at_ms IS NOT NULL ORDER BY trashed_at_ms DESC, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	markers := []Marker{}
	for rows.Next() {
		marker, err := scanMarkerRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		markers = append(markers, marker)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range markers {
		if markers[i].Tags, err = s.markerTagsSQL(ctx, markers[i].ID); err != nil {
			return nil, err
		}
	}
	return markers, nil
}

// visibleMarkerSQL loads one Marker (with tags) only when its Media is
// visible under the filter; hidden and missing both read as not found.
func (s *Store) visibleMarkerSQL(ctx context.Context, id MarkerID, filter LibraryFilter) (Marker, error) {
	marker, err := scanMarkerRow(s.catalogR.QueryRowContext(ctx, markerColumns+` WHERE id = ? AND trashed_at_ms IS NULL`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Marker{}, ErrMarkerNotFound
	}
	if err != nil {
		return Marker{}, err
	}
	if _, err := s.visibleMediaSQL(ctx, marker.MediaID, filter); err != nil {
		return Marker{}, ErrMarkerNotFound
	}
	if marker.Tags, err = s.markerTagsSQL(ctx, marker.ID); err != nil {
		return Marker{}, err
	}
	return marker, nil
}

func (s *Store) markerTagsSQL(ctx context.Context, id MarkerID) ([]string, error) {
	rows, err := s.catalogR.QueryContext(ctx, `SELECT tag FROM marker_tag WHERE marker_id = ? ORDER BY tag`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func insertMarkerTags(ctx context.Context, tx *sql.Tx, id MarkerID, tags []string) error {
	for _, tag := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO marker_tag(marker_id, tag) VALUES (?, ?)`, id, tag); err != nil {
			return err
		}
	}
	return nil
}

const markerColumns = `SELECT id, media_id, title, start_seconds, end_seconds, segment_kind, provenance, method, confidence, flavor, created_at_ms, updated_at_ms, trashed_at_ms FROM marker`

func scanMarkerRow(scan func(...any) error) (Marker, error) {
	var marker Marker
	var end sql.NullFloat64
	var segmentKind sql.NullString
	var trashedMS sql.NullInt64
	var provenance string
	var flavor string
	var createdMS, updatedMS int64
	if err := scan(&marker.ID, &marker.MediaID, &marker.Title, &marker.StartSeconds,
		&end, &segmentKind, &provenance, &marker.Method, &marker.Confidence, &flavor, &createdMS, &updatedMS, &trashedMS); err != nil {
		return Marker{}, err
	}
	if end.Valid {
		value := end.Float64
		marker.EndSeconds = &value
	}
	marker.SegmentKind = segmentKind.String
	marker.Provenance = Provenance(provenance)
	marker.Flavor = SegmentFlavor(flavor)
	marker.CreatedAt = time.UnixMilli(createdMS).UTC()
	marker.UpdatedAt = time.UnixMilli(updatedMS).UTC()
	if trashedMS.Valid {
		marker.TrashedAt = time.UnixMilli(trashedMS.Int64).UTC()
	}
	return marker, nil
}

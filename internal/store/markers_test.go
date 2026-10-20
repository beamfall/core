package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func float64Ptr(value float64) *float64 { return &value }

func TestMarkerRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	created, err := st.CreateMarker(ctx, Marker{
		MediaID:      "media-alpha",
		Title:        "Opening Theme",
		StartSeconds: 12.5,
		EndSeconds:   float64Ptr(74),
		SegmentKind:  "intro",
		Tags:         []string{"theme", "music"},
	}, revealed, now)
	if err != nil {
		t.Fatalf("CreateMarker returned error: %v", err)
	}
	if created.ID == "" || !strings.HasPrefix(string(created.ID), "marker-") {
		t.Fatalf("created.ID = %q, want marker- prefix", created.ID)
	}
	if !created.CreatedAt.Equal(now) || !created.UpdatedAt.Equal(now) {
		t.Fatalf("created times = %v / %v, want %v", created.CreatedAt, created.UpdatedAt, now)
	}
	if len(created.Tags) != 2 || created.Tags[0] != "music" || created.Tags[1] != "theme" {
		t.Fatalf("created.Tags = %v, want sorted [music theme]", created.Tags)
	}

	// A point marker after the range, listed second by start time.
	point, err := st.CreateMarker(ctx, Marker{
		MediaID:      "media-alpha",
		StartSeconds: 90,
		Tags:         []string{"highlight"},
	}, revealed, now)
	if err != nil {
		t.Fatalf("CreateMarker point returned error: %v", err)
	}
	if point.EndSeconds != nil {
		t.Fatalf("point.EndSeconds = %v, want nil", *point.EndSeconds)
	}

	listed, err := st.MarkersForMedia(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("MarkersForMedia returned error: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != created.ID || listed[1].ID != point.ID {
		t.Fatalf("listed = %+v, want range then point by start time", listed)
	}
	if listed[0].EndSeconds == nil || *listed[0].EndSeconds != 74 || listed[0].SegmentKind != "intro" {
		t.Fatalf("listed[0] = %+v", listed[0])
	}

	later := now.Add(time.Hour)
	updated, err := st.UpdateMarker(ctx, Marker{
		ID:           created.ID,
		Title:        "Cold Open",
		StartSeconds: 10,
		Tags:         []string{"theme"},
	}, revealed, later)
	if err != nil {
		t.Fatalf("UpdateMarker returned error: %v", err)
	}
	if updated.MediaID != "media-alpha" || updated.Title != "Cold Open" || updated.SegmentKind != "" || updated.EndSeconds != nil {
		t.Fatalf("updated = %+v", updated)
	}
	if !updated.CreatedAt.Equal(now) || !updated.UpdatedAt.Equal(later) {
		t.Fatalf("updated times = %v / %v", updated.CreatedAt, updated.UpdatedAt)
	}
	listed, err = st.MarkersForMedia(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("MarkersForMedia after update: %v", err)
	}
	if len(listed) != 2 || listed[0].Title != "Cold Open" || len(listed[0].Tags) != 1 || listed[0].Tags[0] != "theme" {
		t.Fatalf("listed after update = %+v", listed)
	}

	removed, err := st.DeleteMarker(ctx, created.ID, revealed)
	if err != nil {
		t.Fatalf("DeleteMarker returned error: %v", err)
	}
	if removed.ID != created.ID {
		t.Fatalf("removed.ID = %q, want %q", removed.ID, created.ID)
	}
	if removed.TrashedAt.IsZero() {
		t.Fatal("removed.TrashedAt is zero, want soft-delete timestamp")
	}
	if _, err := st.DeleteMarker(ctx, created.ID, revealed); !errors.Is(err, ErrMarkerNotFound) {
		t.Fatalf("second delete = %v, want ErrMarkerNotFound", err)
	}
	listed, err = st.MarkersForMedia(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("MarkersForMedia after delete: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != point.ID {
		t.Fatalf("listed after delete = %+v, want point only", listed)
	}
	trashed, err := st.TrashedMarkersForMedia(ctx, "media-alpha", revealed)
	if err != nil {
		t.Fatalf("TrashedMarkersForMedia: %v", err)
	}
	if len(trashed) != 1 || trashed[0].ID != created.ID || len(trashed[0].Tags) != 1 || trashed[0].Tags[0] != "theme" {
		t.Fatalf("trashed markers = %+v, want deleted marker with tags", trashed)
	}
	restored, err := st.RestoreMarker(ctx, created.ID, revealed, later.Add(time.Hour))
	if err != nil {
		t.Fatalf("RestoreMarker returned error: %v", err)
	}
	if restored.ID != created.ID || !restored.TrashedAt.IsZero() || len(restored.Tags) != 1 || restored.Tags[0] != "theme" {
		t.Fatalf("restored = %+v, want live marker with tags", restored)
	}
	listed, err = st.MarkersForMedia(ctx, "media-alpha", revealed)
	if err != nil {
		t.Fatalf("MarkersForMedia after restore: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != created.ID || listed[1].ID != point.ID {
		t.Fatalf("listed after restore = %+v, want both markers", listed)
	}
}

func TestMarkerValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	cases := []struct {
		name   string
		marker Marker
		want   string
	}{
		{"missing media", Marker{Title: "x"}, "media ID is required"},
		{"negative start", Marker{MediaID: "media-alpha", Title: "x", StartSeconds: -1}, "marker start must be >= 0"},
		{"end before start", Marker{MediaID: "media-alpha", Title: "x", StartSeconds: 10, EndSeconds: float64Ptr(5)}, "marker end must be after start"},
		{"end equals start", Marker{MediaID: "media-alpha", Title: "x", StartSeconds: 10, EndSeconds: float64Ptr(10)}, "marker end must be after start"},
		{"unknown segment kind", Marker{MediaID: "media-alpha", Title: "x", SegmentKind: "outro"}, "unknown segment kind outro"},
		{"no display handle", Marker{MediaID: "media-alpha", Title: "  "}, "marker needs a title, a segment kind, or a tag"},
		{"blank tag", Marker{MediaID: "media-alpha", Tags: []string{" "}}, "marker tags must not be blank"},
		{"duplicate tag", Marker{MediaID: "media-alpha", Tags: []string{"a", "a"}}, "duplicate marker tag a"},
		{"unknown media", Marker{MediaID: "media-missing", Title: "x"}, "media not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.CreateMarker(ctx, tc.marker, LibraryFilter{Revealed: true, AllLibraries: true}, now)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("CreateMarker error = %v, want %q", err, tc.want)
			}
		})
	}

	// Each EXTERNALLY WRITABLE segment kind is accepted, alone. `ad` is in the
	// closed vocabulary but is detector-authored, so it is excluded
	// here and covered by TestAdSegmentKindIsDetectorAuthoredOnly.
	for _, kind := range ExternallyWritableSegmentKinds {
		if _, err := st.CreateMarker(ctx, Marker{MediaID: "media-alpha", SegmentKind: kind}, LibraryFilter{AllLibraries: true}, now); err != nil {
			t.Fatalf("CreateMarker segment kind %q: %v", kind, err)
		}
	}
}

// TestSegmentKindVocabularyNarrowed proves ADR-0048 Decision 1 (ENR-5,
// hard-narrowed 2026-06-16) as amended by ADR-0193 Decision 2: the EXTERNALLY
// WRITABLE set is exactly intro/recap/credits, the readable/detectable set adds
// `ad`, and the deferred preview/commercial are rejected on write AND rejected
// at the schema layer (the migration-0068/0099 segment_kind CHECK), so no row
// can carry them — the narrowing is enforced by the database, not just
// application code.
func TestSegmentKindVocabularyNarrowed(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	// The readable/detectable set is the three ADR-0048 kinds plus ADR-0193's
	// `ad`; the externally writable set stays the ADR-0048 three.
	if len(SegmentKinds) != 4 || !slices.Contains(SegmentKinds, SegmentKindAd) {
		t.Fatalf("SegmentKinds = %v, want the three ADR-0048 kinds plus %q", SegmentKinds, SegmentKindAd)
	}
	if len(ExternallyWritableSegmentKinds) != 3 || slices.Contains(ExternallyWritableSegmentKinds, SegmentKindAd) {
		t.Fatalf("ExternallyWritableSegmentKinds = %v, want the three ADR-0048 kinds only", ExternallyWritableSegmentKinds)
	}
	for _, kind := range ExternallyWritableSegmentKinds {
		if _, err := st.CreateMarker(ctx, Marker{MediaID: "media-alpha", SegmentKind: kind}, revealed, now); err != nil {
			t.Fatalf("CreateMarker kind %q: %v", kind, err)
		}
	}

	// preview/commercial were deferred by ADR-0048 and are now removed from the
	// schema (migration 0068) — rejected on a new write as unknown kinds.
	for _, kind := range []string{"preview", "commercial", "sponsor"} {
		_, err := st.CreateMarker(ctx, Marker{MediaID: "media-alpha", SegmentKind: kind}, revealed, now)
		want := "unknown segment kind " + kind
		if err == nil || err.Error() != want {
			t.Fatalf("CreateMarker deferred kind %q error = %v, want %q", kind, err, want)
		}
	}

	// The narrowing is now SCHEMA-enforced (migration 0068): the segment_kind
	// CHECK rejects a direct INSERT of a deferred kind, so a preview/commercial
	// row can never exist — there is no readable-but-not-writable set anymore.
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO marker(id, media_id, title, start_seconds, segment_kind, provenance, method, confidence, created_at_ms, updated_at_ms)
		VALUES ('marker-reject-preview', 'media-alpha', '', 5, 'preview', 'manual', '', 0, ?, ?)
	`, now.UnixMilli(), now.UnixMilli()); err == nil {
		t.Fatal("segment_kind CHECK accepted a 'preview' row; the hard-narrow must reject it at the schema layer")
	}
}

// TestSegmentProvenance proves ADR-0048 Decision 5: a write records
// provenance (manual default; detected with method + confidence), and a manual
// write clears any stray detector fields.
func TestSegmentProvenance(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	// Unset provenance defaults to manual; method/confidence forced to zero-values.
	manual, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", SegmentKind: "intro", StartSeconds: 1, EndSeconds: float64Ptr(10),
		Method: "stray", Confidence: 0.9, // must be cleared on a manual write
	}, revealed, now)
	if err != nil {
		t.Fatalf("CreateMarker manual: %v", err)
	}
	if manual.Provenance != ProvenanceManual || manual.Method != "" || manual.Confidence != 0 {
		t.Fatalf("manual = %+v, want provenance=manual, method='', confidence=0", manual)
	}

	// A detected write records method + confidence.
	detected, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", SegmentKind: "credits", StartSeconds: 100, EndSeconds: float64Ptr(120),
		Provenance: ProvenanceDetected, Method: "fingerprint", Confidence: 0.82,
	}, revealed, now)
	if err != nil {
		t.Fatalf("CreateMarker detected: %v", err)
	}
	if detected.Provenance != ProvenanceDetected || detected.Method != "fingerprint" || detected.Confidence != 0.82 {
		t.Fatalf("detected = %+v", detected)
	}

	// Round-trips through a read.
	listed, err := st.MarkersForMedia(ctx, "media-alpha", revealed)
	if err != nil {
		t.Fatalf("MarkersForMedia: %v", err)
	}
	for _, m := range listed {
		if m.ID == detected.ID && (m.Provenance != ProvenanceDetected || m.Method != "fingerprint" || m.Confidence != 0.82) {
			t.Fatalf("detected round-trip = %+v", m)
		}
	}

	// Out-of-range confidence on a detected write is rejected.
	if _, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", SegmentKind: "intro", StartSeconds: 200,
		Provenance: ProvenanceDetected, Confidence: 1.5,
	}, revealed, now); err == nil || err.Error() != "segment confidence must be in [0, 1]" {
		t.Fatalf("out-of-range confidence error = %v", err)
	}
}

// TestManualSegmentWins proves ADR-0048 Decision 5: a detected write never
// overwrites a manual segment, while a manual write (a user edit) freely
// updates — and promotes — a detected one.
func TestManualSegmentWins(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	// A manual segment.
	manual, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", SegmentKind: "intro", StartSeconds: 1, EndSeconds: float64Ptr(10),
	}, revealed, now)
	if err != nil {
		t.Fatalf("CreateMarker: %v", err)
	}

	// A detected write against it is rejected — manual wins.
	if _, err := st.UpdateMarker(ctx, Marker{
		ID: manual.ID, SegmentKind: "intro", StartSeconds: 2, EndSeconds: float64Ptr(9),
		Provenance: ProvenanceDetected, Method: "fingerprint", Confidence: 0.9,
	}, revealed, now); !errors.Is(err, ErrManualSegmentWins) {
		t.Fatalf("detected-over-manual = %v, want ErrManualSegmentWins", err)
	}

	// The manual row is untouched.
	got, err := st.MarkersForMedia(ctx, "media-alpha", revealed)
	if err != nil || len(got) != 1 || got[0].StartSeconds != 1 || got[0].Provenance != ProvenanceManual {
		t.Fatalf("after rejected write = %+v, %v", got, err)
	}

	// A detected segment, then a user edit (manual, the default) promotes it.
	detected, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", SegmentKind: "credits", StartSeconds: 50, EndSeconds: float64Ptr(60),
		Provenance: ProvenanceDetected, Method: "chapter-import", Confidence: 1,
	}, revealed, now)
	if err != nil {
		t.Fatalf("CreateMarker detected: %v", err)
	}
	promoted, err := st.UpdateMarker(ctx, Marker{
		ID: detected.ID, SegmentKind: "credits", StartSeconds: 51, EndSeconds: float64Ptr(60),
	}, revealed, now)
	if err != nil {
		t.Fatalf("user edit of detected: %v", err)
	}
	if promoted.Provenance != ProvenanceManual || promoted.Method != "" || promoted.Confidence != 0 {
		t.Fatalf("promoted = %+v, want manual with cleared detector fields", promoted)
	}
}

func TestReplaceDetectedMarkersPreservesManualMarkers(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	if _, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", Title: "Bookmark", StartSeconds: 12, Tags: []string{"note"},
	}, revealed, now); err != nil {
		t.Fatalf("CreateMarker manual: %v", err)
	}
	if err := st.ReplaceDetectedMarkers(ctx, "media-alpha", "chapter-import", []Marker{
		{Title: "Opening", StartSeconds: 0, EndSeconds: float64Ptr(64.4)},
		{Title: "The Door", StartSeconds: 64.4, EndSeconds: float64Ptr(128.9)},
	}, revealed, now); err != nil {
		t.Fatalf("ReplaceDetectedMarkers initial: %v", err)
	}
	if err := st.ReplaceDetectedMarkers(ctx, "media-alpha", "chapter-import", []Marker{
		{Title: "Opening", StartSeconds: 0, EndSeconds: float64Ptr(70)},
	}, revealed, now.Add(time.Minute)); err != nil {
		t.Fatalf("ReplaceDetectedMarkers replacement: %v", err)
	}

	markers, err := st.MarkersForMedia(ctx, "media-alpha", revealed)
	if err != nil {
		t.Fatalf("MarkersForMedia: %v", err)
	}
	if len(markers) != 2 {
		t.Fatalf("len(markers) = %d, want manual + one detected chapter: %+v", len(markers), markers)
	}
	if markers[0].Title != "Opening" || markers[0].Provenance != ProvenanceDetected || markers[0].Method != "chapter-import" || *markers[0].EndSeconds != 70 {
		t.Fatalf("detected marker = %+v, want replaced chapter marker", markers[0])
	}
	if markers[1].Title != "Bookmark" || markers[1].Provenance != ProvenanceManual {
		t.Fatalf("manual marker = %+v, want preserved manual bookmark", markers[1])
	}
}

// TestSegmentSuppressVsReset proves ADR-0048 Decision 5's two delete intents
// behave distinctly: delete-and-suppress establishes a reversible (Media, kind)
// lock; reset-to-auto-detection clears it.
func TestSegmentSuppressVsReset(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	if _, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", SegmentKind: "intro", StartSeconds: 1, EndSeconds: float64Ptr(10),
	}, revealed, now); err != nil {
		t.Fatalf("CreateMarker: %v", err)
	}

	// Initially not suppressed.
	if suppressed, err := st.SegmentSuppressed(ctx, "media-alpha", "intro"); err != nil || suppressed {
		t.Fatalf("pre-suppress = %v, %v, want false", suppressed, err)
	}

	// Delete-and-suppress removes the marker AND sets the lock.
	if err := st.SuppressSegment(ctx, "media-alpha", "intro", now); err != nil {
		t.Fatalf("SuppressSegment: %v", err)
	}
	listed, err := st.MarkersForMedia(ctx, "media-alpha", revealed)
	if err != nil {
		t.Fatalf("MarkersForMedia: %v", err)
	}
	for _, m := range listed {
		if m.SegmentKind == "intro" {
			t.Fatalf("intro marker survived SuppressSegment: %+v", m)
		}
	}
	if suppressed, err := st.SegmentSuppressed(ctx, "media-alpha", "intro"); err != nil || !suppressed {
		t.Fatalf("post-suppress = %v, %v, want true", suppressed, err)
	}

	// Reset-to-auto clears the lock — the distinct second intent.
	if err := st.ResetSegmentToAuto(ctx, "media-alpha", "intro"); err != nil {
		t.Fatalf("ResetSegmentToAuto: %v", err)
	}
	if suppressed, err := st.SegmentSuppressed(ctx, "media-alpha", "intro"); err != nil || suppressed {
		t.Fatalf("post-reset = %v, %v, want false", suppressed, err)
	}

	// Reset is idempotent on an unsuppressed pair.
	if err := st.ResetSegmentToAuto(ctx, "media-alpha", "credits"); err != nil {
		t.Fatalf("idempotent reset: %v", err)
	}

	// A non-writable kind is rejected by both intents.
	if err := st.SuppressSegment(ctx, "media-alpha", "preview", now); err == nil {
		t.Fatal("SuppressSegment preview succeeded, want unknown segment kind")
	}
}

func TestMarkersRevealFiltering(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	// Creating on restricted media requires a revealed scope.
	if _, err := st.CreateMarker(ctx, Marker{MediaID: "media-archive", Title: "x"}, LibraryFilter{AllLibraries: true}, now); err == nil {
		t.Fatal("CreateMarker on hidden media succeeded, want media not found")
	}
	created, err := st.CreateMarker(ctx, Marker{MediaID: "media-archive", Title: "Archive Intro", SegmentKind: "intro"}, revealed, now)
	if err != nil {
		t.Fatalf("CreateMarker revealed: %v", err)
	}

	// Unrevealed reads see nothing — indistinguishable from missing media.
	if _, err := st.MarkersForMedia(ctx, "media-archive", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("MarkersForMedia unrevealed succeeded, want media not found")
	}
	listed, err := st.MarkersForMedia(ctx, "media-archive", revealed)
	if err != nil || len(listed) != 1 {
		t.Fatalf("MarkersForMedia revealed = %v, %v", listed, err)
	}

	// Unrevealed update/delete read as marker not found.
	if _, err := st.UpdateMarker(ctx, Marker{ID: created.ID, Title: "y"}, LibraryFilter{AllLibraries: true}, now); !errors.Is(err, ErrMarkerNotFound) {
		t.Fatalf("unrevealed update = %v, want ErrMarkerNotFound", err)
	}
	if _, err := st.DeleteMarker(ctx, created.ID, LibraryFilter{AllLibraries: true}); !errors.Is(err, ErrMarkerNotFound) {
		t.Fatalf("unrevealed delete = %v, want ErrMarkerNotFound", err)
	}
	if _, err := st.TrashedMarkersForMedia(ctx, "media-archive", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("TrashedMarkersForMedia unrevealed succeeded, want media not found")
	}
	if _, err := st.RestoreMarker(ctx, created.ID, LibraryFilter{AllLibraries: true}, now); !errors.Is(err, ErrMarkerNotFound) {
		t.Fatalf("unrevealed restore = %v, want ErrMarkerNotFound", err)
	}
}

func TestMarkersRideMediaThroughTrash(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := st.CreateMarker(ctx, Marker{MediaID: "media-alpha", Title: "Recap", SegmentKind: "recap"}, LibraryFilter{AllLibraries: true}, now); err != nil {
		t.Fatalf("CreateMarker: %v", err)
	}
	if err := st.TrashMedia(ctx, "media-alpha", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}
	// Trashed media reads as missing, so its markers are hidden with it.
	if _, err := st.MarkersForMedia(ctx, "media-alpha", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("MarkersForMedia on trashed media succeeded")
	}
	if err := st.RestoreMedia(ctx, "media-alpha", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("RestoreMedia: %v", err)
	}
	listed, err := st.MarkersForMedia(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil || len(listed) != 1 {
		t.Fatalf("markers after restore = %v, %v, want the marker back", listed, err)
	}
}

// --- ADR-0193: the `ad` kind, detection flavor, and their invariants.

// TestAdSegmentKindIsDetectorAuthoredOnly proves the PODAD rule that `ad` is
// "Core-detected only, not externally writable" (so ADR-0191/WRITEAPI-9's
// external-write "no new kinds" guarantee still holds at four kinds), WITHOUT
// closing the ADR-0048 Decision 5 promotion path: editing a detected `ad`
// promotes it to manual and it stays an `ad`.
func TestAdSegmentKindIsDetectorAuthoredOnly(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	if _, err := st.CreateMarker(ctx, Marker{MediaID: "media-alpha", SegmentKind: SegmentKindAd}, revealed, now); err == nil {
		t.Fatal("CreateMarker authored an `ad` segment; `ad` is detector-authored only")
	}

	// An external re-label of another kind INTO `ad` is refused too.
	intro, err := st.CreateMarker(ctx, Marker{MediaID: "media-alpha", SegmentKind: "intro"}, revealed, now)
	if err != nil {
		t.Fatalf("seed intro: %v", err)
	}
	if _, err := st.UpdateMarker(ctx, Marker{ID: intro.ID, SegmentKind: SegmentKindAd, StartSeconds: 0}, revealed, now); err == nil {
		t.Fatal("UpdateMarker re-labelled an intro as `ad`; that is external authoring of a detector-only kind")
	}

	// The detector may write one, and a user edit of it still promotes to manual.
	wrote, err := st.ReplaceDetectedSegment(ctx, "media-archive", SegmentKindAd, 30, 90,
		"podcast-ad-audio", 0.91, FlavorInserted, now)
	if err != nil || !wrote {
		t.Fatalf("detector `ad` write: wrote=%v err=%v", wrote, err)
	}
	markers, err := st.MarkersForMedia(ctx, "media-archive", revealed)
	if err != nil || len(markers) != 1 {
		t.Fatalf("MarkersForMedia after detected ad: %d markers, err=%v", len(markers), err)
	}
	end := 95.0
	promoted, err := st.UpdateMarker(ctx, Marker{
		ID: markers[0].ID, SegmentKind: SegmentKindAd, StartSeconds: 30, EndSeconds: &end,
	}, revealed, now)
	if err != nil {
		t.Fatalf("edit-promotes-to-manual on an `ad`: %v", err)
	}
	if promoted.Provenance != ProvenanceManual || promoted.SegmentKind != SegmentKindAd {
		t.Fatalf("promoted marker = %+v, want manual `ad`", promoted)
	}
	if promoted.Flavor != FlavorNone {
		t.Fatalf("promoted marker kept flavor %q; a manual segment carries no detector provenance", promoted.Flavor)
	}
}

// TestMarkerCreationUnaffectedForOrdinaryMarkers is the detectorAuthoredKind
// regression: the predicate must not be written as "not externally writable",
// which is true for the empty kind a title-only or tag-only Marker carries.
func TestMarkerCreationUnaffectedForOrdinaryMarkers(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	if _, err := st.CreateMarker(ctx, Marker{MediaID: "media-alpha", Title: "Title only"}, revealed, now); err != nil {
		t.Fatalf("title-only marker: %v", err)
	}
	if _, err := st.CreateMarker(ctx, Marker{MediaID: "media-alpha", Tags: []string{"tag-only"}}, revealed, now); err != nil {
		t.Fatalf("tag-only marker: %v", err)
	}
}

// TestMarkerFlavorIsDetectorOnly covers the ADR-0193 Decision 2 flavor rules at
// the Go layer: the closed vocabulary, the two-way `ad` correspondence, and the
// manual-promotion clear.
func TestMarkerFlavorIsDetectorOnly(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	if _, err := st.ReplaceDetectedSegment(ctx, "media-alpha", SegmentKindAd, 10, 40,
		"podcast-ad-audio", 0.88, FlavorInserted, now); err != nil {
		t.Fatalf("detected inserted ad: %v", err)
	}
	markers, err := st.MarkersForMedia(ctx, "media-alpha", revealed)
	if err != nil || len(markers) != 1 {
		t.Fatalf("MarkersForMedia: %d markers, err=%v", len(markers), err)
	}
	if markers[0].Flavor != FlavorInserted || markers[0].Confidence != 0.88 {
		t.Fatalf("stored ad = flavor %q confidence %v, want inserted/0.88", markers[0].Flavor, markers[0].Confidence)
	}

	cases := []struct {
		name   string
		marker Marker
	}{
		{"unknown flavor", Marker{MediaID: "media-archive", SegmentKind: SegmentKindAd, Provenance: ProvenanceDetected, Method: "podcast-ad-audio", Confidence: 0.5, Flavor: SegmentFlavor("sponsor")}},
		{"flavor on a detected intro", Marker{MediaID: "media-archive", SegmentKind: "intro", Provenance: ProvenanceDetected, Method: "fingerprint", Confidence: 0.5, Flavor: FlavorInserted}},
		{"detected ad without a flavor", Marker{MediaID: "media-archive", SegmentKind: SegmentKindAd, Provenance: ProvenanceDetected, Method: "podcast-ad-audio", Confidence: 0.5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marker := tc.marker
			if err := validateMarker(&marker); err == nil {
				t.Fatalf("validateMarker accepted %s", tc.name)
			}
		})
	}

	// A manual write normalizes a stray flavor away rather than erroring — this
	// is the ADR-0048 D5 promotion path, not leniency.
	manual := Marker{MediaID: "media-archive", SegmentKind: "intro", Provenance: ProvenanceManual, Flavor: FlavorInserted}
	if err := validateMarker(&manual); err != nil {
		t.Fatalf("manual marker carrying a flavor: %v", err)
	}
	if manual.Flavor != FlavorNone {
		t.Fatalf("manual marker kept flavor %q, want it cleared", manual.Flavor)
	}
}

// TestHostReadAdCannotBecomeALiveMarkerByAnyPath is the ADR-0193 Decision 5
// fail-closed proof at the Go layer: `host-read` never reaches the served
// Marker table through ANY write path. markersQuery serves every live marker
// unfiltered, so a stored host-read ad IS a surfaced, auto-skipped ad.
func TestHostReadAdCannotBecomeALiveMarkerByAnyPath(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	// CreateMarker: refused by the detector-authored guard before flavor even
	// matters, so `ad` cannot be authored host-read or otherwise.
	if _, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", SegmentKind: SegmentKindAd, Provenance: ProvenanceDetected,
		Method: "podcast-ad-audio", Confidence: 0.4, Flavor: FlavorHostRead,
	}, revealed, now); err == nil {
		t.Fatal("CreateMarker accepted a host-read ad")
	}

	// UpdateMarker: promoting an existing detected `inserted` ad to host-read.
	if _, err := st.ReplaceDetectedSegment(ctx, "media-alpha", SegmentKindAd, 10, 40,
		"podcast-ad-audio", 0.9, FlavorInserted, now); err != nil {
		t.Fatalf("seed inserted ad: %v", err)
	}
	markers, err := st.MarkersForMedia(ctx, "media-alpha", revealed)
	if err != nil || len(markers) != 1 {
		t.Fatalf("seed read back: %d markers, err=%v", len(markers), err)
	}
	end := 40.0
	if _, err := st.UpdateMarker(ctx, Marker{
		ID: markers[0].ID, SegmentKind: SegmentKindAd, StartSeconds: 10, EndSeconds: &end,
		Provenance: ProvenanceDetected, Method: "podcast-ad-audio", Confidence: 0.4, Flavor: FlavorHostRead,
	}, revealed, now); !errors.Is(err, ErrHostReadNotSurfaced) {
		t.Fatalf("UpdateMarker to host-read error = %v, want ErrHostReadNotSurfaced", err)
	}

	// ReplaceDetectedMarkers force-clears SegmentKind, so it can never write an
	// `ad` at all; what it CAN be handed is a flavor, and that is refused.
	if err := st.ReplaceDetectedMarkers(ctx, "media-alpha", "chapter-import", []Marker{
		{Title: "chapter", StartSeconds: 1, Flavor: FlavorHostRead},
	}, revealed, now); err == nil {
		t.Fatal("ReplaceDetectedMarkers accepted a flavored kindless marker")
	}

	for _, marker := range mustMarkers(t, st, "media-alpha") {
		if marker.Flavor == FlavorHostRead {
			t.Fatalf("a host-read marker reached the served table: %+v", marker)
		}
	}
}

func mustMarkers(t *testing.T, st *Store, mediaID MediaID) []Marker {
	t.Helper()
	markers, err := st.MarkersForMedia(context.Background(), mediaID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("MarkersForMedia: %v", err)
	}
	return markers
}

// TestMarkerFlavorSchemaInvariants proves the flavor rules are enforced BELOW
// the Go layer too: direct SQL cannot create the states validateMarker calls
// unrepresentable. IFNULL in the CHECKs is what makes the NULL-segment_kind case
// fail — SQLite treats a NULL CHECK result as a pass.
func TestMarkerFlavorSchemaInvariants(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()

	cases := []struct {
		name        string
		segmentKind any
		provenance  string
		flavor      string
	}{
		{"host-read is not a marker flavor", "ad", "detected", "host-read"},
		{"flavor on a non-ad kind", "intro", "detected", "inserted"},
		{"flavor on a manual row", "ad", "manual", "inserted"},
		{"flavor with a NULL segment kind", nil, "detected", "inserted"},
		{"detected ad without a flavor", "ad", "detected", ""},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.catalogW.ExecContext(ctx, `
				INSERT INTO marker(id, media_id, title, start_seconds, segment_kind, provenance, method, confidence, flavor, created_at_ms, updated_at_ms)
				VALUES (?, 'media-alpha', '', 5, ?, ?, 'podcast-ad-audio', 0.5, ?, ?, ?)
			`, "marker-schema-case-"+strconv.Itoa(i), tc.segmentKind, tc.provenance, tc.flavor, now, now)
			if err == nil {
				t.Fatalf("the marker CHECKs accepted %s; the schema must be the floor, not the Go guards", tc.name)
			}
		})
	}
}

// TestSuppressSegmentAcceptsAd proves migration 0099 widened the
// marker_suppression CHECK too: delete-and-suppress and reset-to-auto-detection
// work for the new kind instead of failing at the schema.
func TestSuppressSegmentAcceptsAd(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := st.ReplaceDetectedSegment(ctx, "media-alpha", SegmentKindAd, 10, 40,
		"podcast-ad-audio", 0.9, FlavorInserted, now); err != nil {
		t.Fatalf("seed detected ad: %v", err)
	}
	if err := st.SuppressSegment(ctx, "media-alpha", SegmentKindAd, now); err != nil {
		t.Fatalf("SuppressSegment ad: %v", err)
	}
	suppressed, err := st.SegmentSuppressed(ctx, "media-alpha", SegmentKindAd)
	if err != nil || !suppressed {
		t.Fatalf("SegmentSuppressed = %v, err=%v, want true", suppressed, err)
	}
	if len(mustMarkers(t, st, "media-alpha")) != 0 {
		t.Fatal("suppress left the ad marker in place")
	}
	// Re-detection must not resurrect it.
	wrote, err := st.ReplaceDetectedSegment(ctx, "media-alpha", SegmentKindAd, 10, 40,
		"podcast-ad-audio", 0.9, FlavorInserted, now)
	if err != nil || wrote {
		t.Fatalf("re-detection over a suppressed ad: wrote=%v err=%v, want false/nil", wrote, err)
	}
	if err := st.ResetSegmentToAuto(ctx, "media-alpha", SegmentKindAd); err != nil {
		t.Fatalf("ResetSegmentToAuto ad: %v", err)
	}
}

// TestSkipSegmentAdMigrationPreservesMarkerRows is the data-preservation proof
// for migration 0099's table rebuild. `marker` and `marker_suppression` are both
// recreated (SQLite cannot ALTER a CHECK), and `marker` has two ON DELETE
// CASCADE children — a cascade fired by DROP TABLE would silently empty them
// while PRAGMA foreign_key_check stayed clean, so nothing but a test can catch
// it. Migrating a pre-0099 database with real rows is that test.
func TestSkipSegmentAdMigrationPreservesMarkerRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backupDir := filepath.Join(t.TempDir(), "backups")
	now := time.Now().UTC().UnixMilli()

	beforeAd := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0099")
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", beforeAd, "migrate/catalog", backupDir); err != nil {
		t.Fatalf("pre-0099 migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO library(id, title) VALUES ('library-local', 'Local');
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec)
		  VALUES ('media-old', 'library-local', 'Old', 600, 'mp4', 'h264');
		INSERT INTO marker(id, media_id, title, start_seconds, end_seconds, segment_kind, provenance, method, confidence, created_at_ms, updated_at_ms)
		  VALUES ('marker-live', 'media-old', 'Live', 10, 40, 'intro', 'detected', 'fingerprint', 0.8, ?, ?);
		INSERT INTO marker(id, media_id, title, start_seconds, segment_kind, provenance, method, confidence, created_at_ms, updated_at_ms, trashed_at_ms)
		  VALUES ('marker-trashed', 'media-old', 'Trashed', 90, 'credits', 'manual', '', 0, ?, ?, ?);
		INSERT INTO marker_tag(marker_id, tag) VALUES ('marker-live', 'theme');
		INSERT INTO tag(id, name, created_at_ms, updated_at_ms) VALUES ('tag-core', 'Core Tag', ?, ?);
		INSERT INTO marker_core_tag(marker_id, tag_id) VALUES ('marker-live', 'tag-core');
		INSERT INTO marker_suppression(media_id, segment_kind, created_at_ms) VALUES ('media-old', 'recap', ?);
	`, now, now, now, now, now, now, now, now); err != nil {
		t.Fatalf("seed pre-0099 rows: %v", err)
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", catalogMigrations, "migrate/catalog", backupDir); err != nil {
		t.Fatalf("full migrate: %v", err)
	}

	var liveKind, trashedKind string
	var trashedAt sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT segment_kind FROM marker WHERE id = 'marker-live'`).Scan(&liveKind); err != nil {
		t.Fatalf("live marker lost: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT segment_kind, trashed_at_ms FROM marker WHERE id = 'marker-trashed'`).Scan(&trashedKind, &trashedAt); err != nil {
		t.Fatalf("trashed marker lost: %v", err)
	}
	if !trashedAt.Valid || trashedAt.Int64 != now {
		t.Fatalf("trashed_at_ms = %v, want %d preserved across the rebuild", trashedAt, now)
	}

	counts := map[string]string{
		"marker_tag":          `SELECT COUNT(*) FROM marker_tag WHERE marker_id = 'marker-live'`,
		"marker_core_tag":     `SELECT COUNT(*) FROM marker_core_tag WHERE marker_id = 'marker-live'`,
		"marker_suppression":  `SELECT COUNT(*) FROM marker_suppression WHERE media_id = 'media-old' AND segment_kind = 'recap'`,
		"rebuild backup left": `SELECT COUNT(*) FROM sqlite_master WHERE name LIKE '%_rebuild_backup'`,
	}
	for name, query := range counts {
		var got int
		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil {
			t.Fatalf("%s count: %v", name, err)
		}
		want := 1
		if name == "rebuild backup left" {
			want = 0
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", name, got, want)
		}
	}

	for _, index := range []string{"marker_media_start_idx", "idx_marker_trashed", "segment_detection_media_idx"} {
		var got int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != 1 {
			t.Fatalf("index %q missing after the rebuild", index)
		}
	}

	// The widened vocabulary is live; `sponsor` still is not.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO marker(id, media_id, title, start_seconds, segment_kind, provenance, method, confidence, flavor, created_at_ms, updated_at_ms)
		VALUES ('marker-ad', 'media-old', '', 120, 'ad', 'detected', 'podcast-ad-audio', 0.9, 'inserted', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("migration 0099 did not widen segment_kind to accept `ad`: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO marker(id, media_id, title, start_seconds, segment_kind, provenance, method, confidence, created_at_ms, updated_at_ms)
		VALUES ('marker-sponsor', 'media-old', '', 150, 'sponsor', 'manual', '', 0, ?, ?)
	`, now, now); err == nil {
		t.Fatal("segment_kind CHECK accepted 'sponsor'; it is a reserved placeholder, not a live kind")
	}
}

// TestSegmentDetectionSchemaConstraints pins the new table's CHECKs and its
// Media cascade directly, independent of the Go guards above it.
func TestSegmentDetectionSchemaConstraints(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()

	insert := func(id, kind string, start, end float64, flavor string, confidence float64) error {
		_, err := st.catalogW.ExecContext(ctx, `
			INSERT INTO segment_detection(id, media_id, segment_kind, start_seconds, end_seconds, method, flavor, confidence, created_at_ms)
			VALUES (?, 'media-alpha', ?, ?, ?, 'podcast-ad-audio', ?, ?, ?)
		`, id, kind, start, end, flavor, confidence, now)
		return err
	}

	if err := insert("detection-ok", "ad", 30, 90, "host-read", 0.42); err != nil {
		t.Fatalf("a host-read ad detection must be storable as internal evidence: %v", err)
	}
	rejections := []struct {
		name       string
		id         string
		kind       string
		start, end float64
		flavor     string
		confidence float64
	}{
		{"unknown kind", "detection-kind", "sponsor", 30, 90, "", 0.5},
		{"unknown flavor", "detection-flavor", "ad", 30, 90, "mid-roll", 0.5},
		{"flavored non-ad", "detection-nonad", "intro", 30, 90, "inserted", 0.5},
		{"flavourless ad", "detection-noflavor", "ad", 30, 90, "", 0.5},
		{"end before start", "detection-range", "ad", 90, 30, "inserted", 0.5},
		{"negative start", "detection-negative", "ad", -1, 90, "inserted", 0.5},
		{"confidence above one", "detection-confidence", "ad", 30, 90, "inserted", 1.5},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			if err := insert(tc.id, tc.kind, tc.start, tc.end, tc.flavor, tc.confidence); err == nil {
				t.Fatalf("segment_detection accepted %s", tc.name)
			}
		})
	}

	if _, err := st.catalogW.ExecContext(ctx, `DELETE FROM media WHERE id = 'media-alpha'`); err != nil {
		t.Fatalf("delete media: %v", err)
	}
	var remaining int
	if err := st.catalogR.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM segment_detection WHERE media_id = 'media-alpha'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("%d detection rows survived their Media; the cascade must clean them", remaining)
	}
}

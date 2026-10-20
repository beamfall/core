package graph

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestMetadataExportAdminGatedAndRevealFiltered is the FEAT-5 vertical slice:
// the full metadata JSON export is operator-gated (Admin Session,
// like Library Health) and reveal-filtered (a non-revealed export OMITS
// restricted-Library curation; a revealed one includes it). It also asserts the
// export carries every curation category for visible content — Media + fields,
// entities, entity relations, Media→Media relations, collections, markers, and
// pinned artwork.
//
// feature:metadata-export
func TestMetadataExportAdminGatedAndRevealFiltered(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	revealed := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local", "library-archive"}, Revealed: true}
	now := time.Unix(1000, 0).UTC()

	// A second Media in library-local so we can seed a Media→Media relation
	// (LinkMediaRelation rejects self-links).
	extra, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/m/alpha-extra.mp4", Title: "Alpha Extra",
		Container: "mp4", Codec: "h264", ContentFingerprint: "sha256:alpha-extra",
	})
	if err != nil {
		t.Fatalf("import extra: %v", err)
	}

	// ── Visible curation (library-local / media-alpha) ──
	if err := st.UpsertMediaFields(ctx, "media-alpha", []store.MediaField{
		{Name: "rating", Type: store.EntityFieldNumber, Number: 8},
	}); err != nil {
		t.Fatalf("media-alpha fields: %v", err)
	}
	if _, err := st.CreateMarker(ctx, store.Marker{
		MediaID: "media-alpha", Title: "Chapter 1", StartSeconds: 5, Tags: []string{"chapter"},
	}, revealed, now); err != nil {
		t.Fatalf("media-alpha marker: %v", err)
	}
	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", "poster", "https://art/local.jpg", "plugin-x", "scraper-y"); err != nil {
		t.Fatalf("media-alpha artwork candidate: %v", err)
	}
	if err := st.MarkArtworkCandidateApplied(ctx, store.ArtworkCandidateID("media-alpha", "poster", "https://art/local.jpg")); err != nil {
		t.Fatalf("media-alpha artwork pin: %v", err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: "entity-local", LibraryID: "library-local", Kind: "widget", Title: "Local Widget",
		Fields: []store.EntityField{{Name: "role", Type: store.EntityFieldText, Text: "lead"}},
	}); err != nil {
		t.Fatalf("local entity: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-local", Name: "linked", ToMediaID: "media-alpha",
		Attributes: map[string]string{"credit": "star"},
	}); err != nil {
		t.Fatalf("local entity relation: %v", err)
	}
	if err := st.LinkMediaRelation(ctx, extra.ID, store.MediaRelationExtraOf, "media-alpha"); err != nil {
		t.Fatalf("media relation: %v", err)
	}
	if _, err := st.CreateSmartCollection(ctx, store.SmartCollection{
		LibraryID: "library-local", Name: "Local Picks", SourceExpr: `{"kind":"all-items"}`,
	}, now); err != nil {
		t.Fatalf("local collection: %v", err)
	}

	// ── Restricted curation (library-archive / media-archive) ──
	if err := st.UpsertMediaFields(ctx, "media-archive", []store.MediaField{
		{Name: "secret", Type: store.EntityFieldText, Text: "classified"},
	}); err != nil {
		t.Fatalf("media-archive fields: %v", err)
	}
	if _, err := st.CreateMarker(ctx, store.Marker{
		MediaID: "media-archive", Title: "Restricted Marker", StartSeconds: 10, Tags: []string{"secret"},
	}, revealed, now); err != nil {
		t.Fatalf("media-archive marker: %v", err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: "entity-archive", LibraryID: "library-archive", Kind: "widget", Title: "Archive Widget",
	}); err != nil {
		t.Fatalf("archive entity: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-archive", Name: "linked", ToMediaID: "media-archive",
	}); err != nil {
		t.Fatalf("archive entity relation: %v", err)
	}
	if _, err := st.CreateSmartCollection(ctx, store.SmartCollection{
		LibraryID: "library-archive", Name: "Archive Picks", SourceExpr: `{"kind":"all-items"}`,
	}, now); err != nil {
		t.Fatalf("archive collection: %v", err)
	}

	handler := newTestHandlerWithStore(st)
	const query = `{"query":"query { metadataExport { schema libraries { id restricted } media { id libraryID fields { name value } markers { title tags } artworkPins { slot sourceURL } } entities { id kind } relations { fromEntityID toMediaID attributes } mediaLinks { fromMediaID relation toMediaID } collections { id libraryID name } } }"}`

	// (c) Operator gate: a manager WITHOUT an open Admin Session is rejected.
	_, errs := post(t, handler, query, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin metadataExport errors = %#v, want admin gate", errs)
	}

	// (c) A viewer (no manager device, no admin) is rejected too.
	_, errs = post(t, handler, query, authorizeViewer)
	if len(errs) == 0 {
		t.Fatal("viewer metadataExport unexpectedly succeeded; want rejection")
	}

	// Open the Admin Session window for the non-revealed manager.
	openManagerAdminSession(t, st)

	// (a)+(b) Non-revealed operator export: every visible category present,
	// restricted content OMITTED.
	data, errs := post(t, handler, query, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("metadataExport(non-revealed) errors = %#v", errs)
	}
	var nonRevealed MetadataExportInfo
	mustUnmarshal(t, data["metadataExport"], &nonRevealed)
	if nonRevealed.Schema != metadataExportSchema {
		t.Fatalf("schema = %q, want %q", nonRevealed.Schema, metadataExportSchema)
	}
	assertExportVisible(t, "non-revealed", nonRevealed)
	assertExportOmitsRestricted(t, nonRevealed)

	// (b) Revealed operator export: restricted content now PRESENT. Enroll a
	// session that is both admin and revealed (the seed has no such row), the
	// same pattern the jobs reveal-redaction test uses.
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-mgr2", Name: "Manager 2", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("mgr2-secret")},
		store.Session{ID: "session-mgr-revealed", DeviceID: "device-mgr2", ProfileID: "profile-default", Revealed: true},
	); err != nil {
		t.Fatal(err)
	}
	openAdminSession(t, st, "session-mgr-revealed")
	authorizeRevealedManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-mgr-revealed")
		req.Header.Set("X-Beamfall-Device", "device-mgr2")
		req.Header.Set("X-Beamfall-Device-Secret", "mgr2-secret")
	}
	data, errs = post(t, handler, query, authorizeRevealedManager)
	if len(errs) != 0 {
		t.Fatalf("metadataExport(revealed) errors = %#v", errs)
	}
	var revealedExport MetadataExportInfo
	mustUnmarshal(t, data["metadataExport"], &revealedExport)
	assertExportVisible(t, "revealed", revealedExport)
	assertExportIncludesRestricted(t, revealedExport)
}

func hasMediaID(export MetadataExportInfo, id string) bool {
	for _, m := range export.Media {
		if m.ID == id {
			return true
		}
	}
	return false
}

func mediaByID(export MetadataExportInfo, id string) (ExportMediaInfo, bool) {
	for _, m := range export.Media {
		if m.ID == id {
			return m, true
		}
	}
	return ExportMediaInfo{}, false
}

// assertExportVisible checks that the always-visible (library-local) curation is
// fully present in any operator export, revealed or not.
func assertExportVisible(t *testing.T, label string, export MetadataExportInfo) {
	t.Helper()
	alpha, ok := mediaByID(export, "media-alpha")
	if !ok {
		t.Fatalf("%s: media-alpha missing from export", label)
	}
	if len(alpha.Fields) == 0 || alpha.Fields[0].Name != "rating" {
		t.Fatalf("%s: media-alpha fields = %#v, want rating", label, alpha.Fields)
	}
	if len(alpha.Markers) == 0 || alpha.Markers[0].Title != "Chapter 1" {
		t.Fatalf("%s: media-alpha markers = %#v, want Chapter 1", label, alpha.Markers)
	}
	if len(alpha.ArtworkPins) == 0 || alpha.ArtworkPins[0].Slot != "poster" {
		t.Fatalf("%s: media-alpha artwork pins = %#v, want poster", label, alpha.ArtworkPins)
	}
	foundEntity, foundRel, foundMediaLink, foundCollection := false, false, false, false
	for _, e := range export.Entities {
		if e.ID == "entity-local" {
			foundEntity = true
		}
	}
	for _, r := range export.Relations {
		if r.FromEntityID == "entity-local" && r.ToMediaID == "media-alpha" && r.Attributes["credit"] == "star" {
			foundRel = true
		}
	}
	for _, l := range export.MediaLinks {
		if l.ToMediaID == "media-alpha" && l.Relation == string(store.MediaRelationExtraOf) {
			foundMediaLink = true
		}
	}
	for _, c := range export.Collections {
		if c.LibraryID == "library-local" && c.Name == "Local Picks" {
			foundCollection = true
		}
	}
	if !foundEntity || !foundRel || !foundMediaLink || !foundCollection {
		t.Fatalf("%s: visible categories entity=%v rel=%v mediaLink=%v collection=%v, want all true",
			label, foundEntity, foundRel, foundMediaLink, foundCollection)
	}
}

// assertExportOmitsRestricted checks that NO restricted (library-archive) row
// leaks into a non-revealed export, across every category.
func assertExportOmitsRestricted(t *testing.T, export MetadataExportInfo) {
	t.Helper()
	for _, lib := range export.Libraries {
		if lib.ID == "library-archive" {
			t.Fatal("non-revealed export leaked restricted library-archive")
		}
	}
	if hasMediaID(export, "media-archive") {
		t.Fatal("non-revealed export leaked media-archive")
	}
	for _, e := range export.Entities {
		if e.ID == "entity-archive" || e.LibraryID == "library-archive" {
			t.Fatalf("non-revealed export leaked restricted entity %+v", e)
		}
	}
	for _, r := range export.Relations {
		if r.FromEntityID == "entity-archive" || r.ToMediaID == "media-archive" {
			t.Fatalf("non-revealed export leaked restricted relation %+v", r)
		}
	}
	for _, c := range export.Collections {
		if c.LibraryID == "library-archive" {
			t.Fatalf("non-revealed export leaked restricted collection %+v", c)
		}
	}
}

// assertExportIncludesRestricted checks that a REVEALED export carries the
// restricted curation across every category.
func assertExportIncludesRestricted(t *testing.T, export MetadataExportInfo) {
	t.Helper()
	foundLib := false
	for _, lib := range export.Libraries {
		if lib.ID == "library-archive" && lib.Restricted {
			foundLib = true
		}
	}
	archive, ok := mediaByID(export, "media-archive")
	if !ok {
		t.Fatal("revealed export missing media-archive")
	}
	if len(archive.Fields) == 0 || archive.Fields[0].Name != "secret" {
		t.Fatalf("revealed export media-archive fields = %#v, want secret", archive.Fields)
	}
	if len(archive.Markers) == 0 || archive.Markers[0].Title != "Restricted Marker" {
		t.Fatalf("revealed export media-archive markers = %#v", archive.Markers)
	}
	foundEntity, foundRel, foundCollection := false, false, false
	for _, e := range export.Entities {
		if e.ID == "entity-archive" {
			foundEntity = true
		}
	}
	for _, r := range export.Relations {
		if r.FromEntityID == "entity-archive" && r.ToMediaID == "media-archive" {
			foundRel = true
		}
	}
	for _, c := range export.Collections {
		if c.LibraryID == "library-archive" && c.Name == "Archive Picks" {
			foundCollection = true
		}
	}
	if !foundLib || !foundEntity || !foundRel || !foundCollection {
		t.Fatalf("revealed export restricted categories lib=%v entity=%v rel=%v collection=%v, want all true",
			foundLib, foundEntity, foundRel, foundCollection)
	}
}

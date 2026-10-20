package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestExportItemPlaneAwareCustodialLibrary proves the export reads — the
// per-Library Media walk and the per-Media ExportItem assembly — work against a
// real Custodial Library plane, returning every file of a multi-file Media plus
// its fields and generic Entity memberships (album membership).
func TestExportItemPlaneAwareCustodialLibrary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const lib = LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, lib, true, false); err != nil {
		t.Fatalf("SetLibraryBacking custodial: %v", err)
	}

	writeFile := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// A plain still image.
	stillMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib, Path: writeFile("still.jpg", "still-bytes"),
		Title: "Sunset", Container: "jpg", Class: MediaClassImage,
		ContentFingerprint: "sha256:still",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile still: %v", err)
	}

	// A Live Photo: one Media, still Part 0 + motion Part 1.
	liveMedia, liveFiles, err := st.ImportMediaFileParts(ctx, []ImportMediaFilePart{
		{SourceOrdinal: 1, File: ImportMediaFile{
			LibraryID: lib, Path: writeFile("live.heic", "live-still"),
			Title: "Beach", Container: "heic", Class: MediaClassImage,
			ContentFingerprint: "sha256:livestill",
		}},
		{SourceOrdinal: 2, File: ImportMediaFile{
			LibraryID: lib, Path: writeFile("live.mov", "live-motion"),
			Title: "Beach", Container: "mov", Codec: "h264", Class: MediaClassVideo,
			ContentFingerprint: "sha256:livemotion",
		}},
	})
	if err != nil {
		t.Fatalf("ImportMediaFileParts live: %v", err)
	}
	if len(liveFiles) != 2 {
		t.Fatalf("live import returned %d files, want 2", len(liveFiles))
	}

	// EXIF/Location field on the still.
	if err := st.SetMediaFields(ctx, stillMedia.ID, []MediaField{
		{Name: "place", Type: EntityFieldText, Text: "Big Sur"},
	}); err != nil {
		t.Fatalf("SetMediaFields: %v", err)
	}

	// Album membership: an album Entity that relates to the still.
	if err := st.UpsertEntity(ctx, Entity{ID: "entity-album-vac", LibraryID: lib, Kind: "album", Title: "Vacation"}); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-album-vac", Name: "member", ToMediaID: stillMedia.ID,
	}); err != nil {
		t.Fatalf("UpsertEntityRelation: %v", err)
	}

	// MediaIDsForLibrary lists both Media on the Custodial plane.
	ids, err := st.MediaIDsForLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("MediaIDsForLibrary: %v", err)
	}
	if !contains(ids, stillMedia.ID) || !contains(ids, liveMedia.ID) {
		t.Fatalf("library media ids = %v, missing seeded items", ids)
	}

	// ExportItem on the Live Photo returns both parts in order.
	liveItem, err := st.ExportItem(ctx, liveMedia.ID)
	if err != nil {
		t.Fatalf("ExportItem live: %v", err)
	}
	if liveItem.Encrypted {
		t.Fatal("plaintext library item marked encrypted")
	}
	if len(liveItem.Files) != 2 || liveItem.Files[0].PartOrdinal != 0 || liveItem.Files[1].PartOrdinal != 1 {
		t.Fatalf("live item files = %+v, want two ordered parts", liveItem.Files)
	}

	// ExportItem on the still carries its field and album membership.
	stillItem, err := st.ExportItem(ctx, stillMedia.ID)
	if err != nil {
		t.Fatalf("ExportItem still: %v", err)
	}
	if len(stillItem.Fields) != 1 || stillItem.Fields[0].Name != "place" || stillItem.Fields[0].Text != "Big Sur" {
		t.Fatalf("still item fields = %+v", stillItem.Fields)
	}
	if len(stillItem.Memberships) != 1 {
		t.Fatalf("still item memberships = %+v, want one album membership", stillItem.Memberships)
	}
	m := stillItem.Memberships[0]
	if m.Relation != "member" || m.Kind != "album" || m.Title != "Vacation" {
		t.Fatalf("membership = %+v", m)
	}
}

func contains(ids []MediaID, id MediaID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

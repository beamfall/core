package store

import (
	"testing"
)

// TestEncryptedRelationOwnershipStaysOnPlane keeps a writer-owned relation of
// an Encrypted Library's Media, with its attributes and Writer, on the
// Library's own plane (CORE-0706): the shared catalog holds none of it,
// writer-scoped cleanup removes only that writer's row, and a sealed plane
// refuses the write instead of parking it in the catalog.
func TestEncryptedRelationOwnershipStaysOnPlane(t *testing.T) {
	ctx := t.Context()
	st, media := seededEncryptedMedia(t, "relation-owner")
	defer func() { _ = st.Close() }()
	for _, entity := range []Entity{
		{ID: "private-holder-scraped", Kind: "widget", Title: "Scraped Holder 9e2a", LibraryID: media.LibraryID},
		{ID: "private-holder-curated", Kind: "widget", Title: "Curated Holder 9e2a", LibraryID: media.LibraryID},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity(%s): %v", entity.ID, err)
		}
	}
	const writer = "scraper:fixture/holder"
	scraped := EntityRelation{FromEntityID: "private-holder-scraped", ToMediaID: media.ID, Name: "member", Writer: writer, Attributes: map[string]string{"role": "private-role-9e2a"}}
	curated := EntityRelation{FromEntityID: "private-holder-curated", ToMediaID: media.ID, Name: "member"}
	if _, err := st.UpsertHydratedEntityRelationWritten(ctx, scraped); err != nil {
		t.Fatalf("scraped relation: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, curated); err != nil {
		t.Fatalf("curated relation: %v", err)
	}

	plane := st.encryptedDBs[media.LibraryID]
	if plane == nil || plane.writer == nil {
		t.Fatal("encrypted plane is not open")
	}
	var planeWriter, attributes string
	if err := plane.writer.QueryRowContext(ctx, `SELECT writer, attributes FROM entity_relation WHERE from_entity_id = ? AND to_media_id = ?`, scraped.FromEntityID, media.ID).Scan(&planeWriter, &attributes); err != nil {
		t.Fatalf("plane relation row: %v", err)
	}
	if planeWriter != writer || attributes != `{"role":"private-role-9e2a"}` {
		t.Fatalf("plane relation writer=%q attributes=%q", planeWriter, attributes)
	}
	var leaked int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM entity_relation WHERE to_media_id = ? OR writer = ? OR attributes LIKE '%9e2a%')
		     + (SELECT COUNT(*) FROM entity WHERE title LIKE '%9e2a%')
	`, media.ID, writer).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("shared catalog relation rows = %d, %v", leaked, err)
	}

	if err := st.RemoveWrittenMediaRelations(ctx, media.ID, writer, []WrittenRelation{{Kind: "widget", Name: "member"}}); err != nil {
		t.Fatalf("RemoveWrittenMediaRelations: %v", err)
	}
	var remaining []string
	rows, err := plane.writer.QueryContext(ctx, `SELECT from_entity_id FROM entity_relation WHERE to_media_id = ? ORDER BY from_entity_id`, media.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		remaining = append(remaining, id)
	}
	_ = rows.Close()
	if len(remaining) != 1 || remaining[0] != string(curated.FromEntityID) {
		t.Fatalf("after writer cleanup = %v, want only the curated row", remaining)
	}

	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertHydratedEntityRelationWritten(ctx, scraped); err == nil {
		t.Fatal("a sealed plane accepted a relation write")
	}
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM entity_relation WHERE to_media_id = ? OR writer = ?`, media.ID, writer).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("sealed relation write reached the shared catalog: %d, %v", leaked, err)
	}
}

package store

import (
	"context"
	"testing"
	"time"
)

// openEncryptedPackLibrary returns a store whose library-local is an unsealed
// Encrypted Library holding one parent Entity, one child Entity that points
// at it, and one Media item the child contains, all in the Library's own
// database.
func openEncryptedPackLibrary(t *testing.T) (*Store, LibraryID, Entity, Entity, MediaID) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	library := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, library, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(library, "sha256:enc-pack-item", "/enc/pack-item.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	parent := Entity{ID: "entity-enc-parent", LibraryID: library, Kind: "group-kind", Title: "Group"}
	child := Entity{ID: "entity-enc-child", LibraryID: library, Kind: "member-kind", Title: "Member"}
	for _, entity := range []Entity{parent, child} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	writer := st.encryptedDBs[library].writer
	if _, err := writer.ExecContext(ctx, `INSERT INTO entity_relation (from_entity_id, relation, to_entity_id) VALUES (?, 'part-of', ?)`, child.ID, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, `INSERT INTO entity_relation (from_entity_id, relation, to_media_id, sort_order) VALUES (?, 'contains', ?, 1)`, child.ID, media.ID); err != nil {
		t.Fatal(err)
	}
	return st, library, parent, child, media.ID
}

// TestEncryptedLibraryPackTargetsResolveLikePublicOnes pins CORE-1742: the
// reads a grouped pack target resolves through (the children pointing at a
// parent, then each child's Media) run on the Entity's own plane, so an
// unsealed Encrypted Library answers as a public one does, and a pack may
// freeze its Media.
func TestEncryptedLibraryPackTargetsResolveLikePublicOnes(t *testing.T) {
	st, _, parent, child, media := openEncryptedPackLibrary(t)
	ctx := context.Background()
	filter := LibraryFilter{AllLibraries: true}

	related, err := st.EntitiesRelatingTo(ctx, parent.ID, child.Kind, "part-of", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0] != child.ID {
		t.Fatalf("EntitiesRelatingTo = %v, want [%s]", related, child.ID)
	}
	items, err := st.MediaForEntity(ctx, child.ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != media {
		t.Fatalf("MediaForEntity = %+v, want [%s]", items, media)
	}

	pack, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID: "profile-default", TargetKind: "media", TargetRef: string(media), State: OfflinePackReady,
	}, []OfflinePackItem{{MediaID: media, PlaintextSize: 7, SHA256: "digest"}}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateOfflinePack with encrypted Media: %v", err)
	}
	frozen, err := st.OfflinePackItems(ctx, pack.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen) != 1 || frozen[0].MediaID != media {
		t.Fatalf("OfflinePackItems = %+v, want the encrypted Media", frozen)
	}
}

// TestSealedEncryptedLibraryPackTargetsRefuse pins that sealing the Library
// takes its Entities, Media and pack membership out of reach again.
func TestSealedEncryptedLibraryPackTargetsRefuse(t *testing.T) {
	st, library, parent, child, media := openEncryptedPackLibrary(t)
	ctx := context.Background()
	if err := st.SealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}

	related, err := st.EntitiesRelatingTo(ctx, parent.ID, child.Kind, "part-of", 0, 10)
	if err != nil || len(related) != 0 {
		t.Fatalf("sealed EntitiesRelatingTo = %v, %v; want nothing", related, err)
	}
	if items, err := st.MediaForEntity(ctx, child.ID, LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatalf("sealed MediaForEntity = %+v, want an error", items)
	}
	if _, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID: "profile-default", TargetKind: "media", TargetRef: string(media),
	}, []OfflinePackItem{{MediaID: media}}, time.Now().UTC()); err == nil {
		t.Fatal("CreateOfflinePack froze Media of a sealed Library")
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM offline_pack`); got != 0 {
		t.Fatalf("packs after refused create = %d, want 0", got)
	}
}

// TestOfflinePackItemsNeedReadableMedia pins the check that replaced the
// catalog media foreign key: an item naming Media on no readable plane is
// refused, and deleting catalog Media still removes its pack items.
func TestOfflinePackItemsNeedReadableMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	if _, err := st.CreateOfflinePack(ctx, OfflinePack{ProfileID: "profile-default", TargetKind: "media"},
		[]OfflinePackItem{{MediaID: "media-missing"}}, time.Now().UTC()); err == nil {
		t.Fatal("CreateOfflinePack with missing item media must error")
	}
	pack, err := st.CreateOfflinePack(ctx, OfflinePack{ProfileID: "profile-default", TargetKind: "media"},
		[]OfflinePackItem{{MediaID: "media-alpha"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `DELETE FROM media WHERE id = 'media-alpha'`); err != nil {
		t.Fatal(err)
	}
	items, err := st.OfflinePackItems(ctx, pack.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("pack items after catalog Media delete = %+v, want none", items)
	}
}

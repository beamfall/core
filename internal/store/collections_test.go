package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCollectionNestMembershipTrashRestore(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	parent, err := st.CreateCollection(ctx, Collection{LibraryID: "library-local", Name: "Favorites"}, now)
	if err != nil {
		t.Fatalf("CreateCollection parent: %v", err)
	}
	child, err := st.CreateCollection(ctx, Collection{LibraryID: "library-local", ParentID: parent.ID, Name: "Weekend"}, now)
	if err != nil {
		t.Fatalf("CreateCollection child: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Fatalf("child parent = %q, want %q", child.ParentID, parent.ID)
	}

	withMedia, err := st.AddCollectionMedia(ctx, parent.ID, []MediaID{"media-alpha", "media-alpha"}, filter, now)
	if err != nil {
		t.Fatalf("AddCollectionMedia: %v", err)
	}
	if withMedia.MemberCount != 1 {
		t.Fatalf("member count = %d, want 1", withMedia.MemberCount)
	}
	if _, err := st.AddCollectionMedia(ctx, parent.ID, []MediaID{"media-archive"}, filter, now); err == nil {
		t.Fatal("AddCollectionMedia accepted hidden/restricted media")
	}

	trashed, err := st.TrashCollection(ctx, parent.ID, now)
	if err != nil {
		t.Fatalf("TrashCollection: %v", err)
	}
	if trashed.TrashedAt == nil {
		t.Fatal("TrashCollection did not set TrashedAt")
	}
	visible, err := st.Collections(ctx, "library-local", false)
	if err != nil {
		t.Fatalf("Collections visible: %v", err)
	}
	for _, c := range visible {
		if c.ID == parent.ID {
			t.Fatalf("trashed collection %s returned without includeTrashed", parent.ID)
		}
	}
	restored, err := st.RestoreCollection(ctx, parent.ID, now)
	if err != nil {
		t.Fatalf("RestoreCollection: %v", err)
	}
	if restored.TrashedAt != nil {
		t.Fatal("RestoreCollection left TrashedAt set")
	}
}

func TestImportCollectionSnapshotPersistsExactOrderAndReplaysIdempotently(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ids := []MediaID{"media-alpha"}
	for i := 1; i < 3; i++ {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/media/local/import-member-%d.mkv", i),
			Title:              fmt.Sprintf("Import Member %d", i),
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("import-%d-member-fingerprint", i),
			OSHash:             fmt.Sprintf("%016x", i+100),
		})
		if err != nil {
			t.Fatalf("seed member %d: %v", i, err)
		}
		ids = append(ids, media.ID)
	}
	collection := Collection{
		ID:        "collection-import-stable",
		LibraryID: "library-local",
		Name:      "Imported Snapshot",
	}

	first, err := st.ImportCollectionSnapshot(ctx, collection, ids, now)
	if err != nil {
		t.Fatalf("ImportCollectionSnapshot first: %v", err)
	}
	if first.ID != collection.ID || first.MemberCount != len(ids) {
		t.Fatalf("first import = %#v, want ID %q with %d members", first, collection.ID, len(ids))
	}
	members, err := st.ResolveCollectionMembers(ctx, collection.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	for i, member := range members {
		if member.MediaID != ids[i] || member.SortOrder != i {
			t.Fatalf("member %d = %#v, want media %q at order %d", i, member, ids[i], i)
		}
	}
	firstMembers := append([]CollectionMember(nil), members...)

	replayed, err := st.ImportCollectionSnapshot(ctx, Collection{
		ID:        collection.ID,
		LibraryID: collection.LibraryID,
		Name:      "Source Rename Must Not Overwrite",
	}, ids, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ImportCollectionSnapshot replay: %v", err)
	}
	if replayed != first {
		t.Fatalf("replay = %#v, want unchanged %#v", replayed, first)
	}
	replayedMembers, err := st.ResolveCollectionMembers(ctx, collection.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(replayedMembers) != len(firstMembers) {
		t.Fatalf("replay members = %#v, want unchanged %#v", replayedMembers, firstMembers)
	}
	for i := range firstMembers {
		if replayedMembers[i] != firstMembers[i] {
			t.Fatalf("replay member %d = %#v, want unchanged %#v", i, replayedMembers[i], firstMembers[i])
		}
	}
}

func TestImportCollectionSnapshotChangedReplayFillsMembershipGaps(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	ids := []MediaID{"media-alpha"}
	for i := 1; i < 3; i++ {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/media/local/changed-replay-member-%d.mkv", i),
			Title:              fmt.Sprintf("Changed Replay Member %d", i),
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("changed-replay-%d-member-fingerprint", i),
			OSHash:             fmt.Sprintf("%016x", i+600),
		})
		if err != nil {
			t.Fatalf("seed member %d: %v", i, err)
		}
		ids = append(ids, media.ID)
	}
	collection := Collection{
		ID:        "collection-import-changed-replay",
		LibraryID: "library-local",
		Name:      "Changed Imported Snapshot",
	}

	if _, err := st.ImportCollectionSnapshot(ctx, collection, []MediaID{ids[0], ids[2]}, now); err != nil {
		t.Fatalf("ImportCollectionSnapshot first: %v", err)
	}
	if _, err := st.ImportCollectionSnapshot(ctx, collection, ids, now.Add(time.Hour)); err != nil {
		t.Fatalf("ImportCollectionSnapshot replay: %v", err)
	}
	members, err := st.ResolveCollectionMembers(ctx, collection.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []MediaID{ids[0], ids[2], ids[1]}
	if len(members) != len(want) {
		t.Fatalf("replay members = %#v, want %v", members, want)
	}
	for i, member := range members {
		if member.MediaID != want[i] || member.SortOrder != i {
			t.Fatalf("replay member %d = %#v, want media %q at distinct contiguous order %d", i, member, want[i], i)
		}
	}
}

func TestImportCollectionSnapshotReplayPreservesLocallyEditedOrder(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	ids := []MediaID{"media-alpha"}
	for i := 1; i < 3; i++ {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/media/local/local-reorder-member-%d.mkv", i),
			Title:              fmt.Sprintf("Local Reorder Member %d", i),
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("local-reorder-%d-member-fingerprint", i),
			OSHash:             fmt.Sprintf("%016x", i+700),
		})
		if err != nil {
			t.Fatalf("seed member %d: %v", i, err)
		}
		ids = append(ids, media.ID)
	}
	collection := Collection{
		ID:        "collection-import-local-reorder",
		LibraryID: "library-local",
		Name:      "Locally Reordered Imported Snapshot",
	}

	if _, err := st.ImportCollectionSnapshot(ctx, collection, ids, now); err != nil {
		t.Fatalf("ImportCollectionSnapshot first: %v", err)
	}
	localOrder := []MediaID{ids[2], ids[0], ids[1]}
	if _, err := st.ReorderCollectionMedia(ctx, collection.ID, localOrder, LibraryFilter{AllLibraries: true}, now.Add(time.Hour)); err != nil {
		t.Fatalf("ReorderCollectionMedia: %v", err)
	}
	beforeReplay, err := st.ResolveCollectionMembers(ctx, collection.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeReplay) != len(localOrder) {
		t.Fatalf("local members = %#v, want %v", beforeReplay, localOrder)
	}
	for i, member := range beforeReplay {
		if member.MediaID != localOrder[i] {
			t.Fatalf("local member %d = %#v, want media %q", i, member, localOrder[i])
		}
	}

	if _, err := st.ImportCollectionSnapshot(ctx, collection, ids, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("ImportCollectionSnapshot replay: %v", err)
	}
	afterReplay, err := st.ResolveCollectionMembers(ctx, collection.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(afterReplay) != len(beforeReplay) {
		t.Fatalf("replay members = %#v, want byte-identical %#v", afterReplay, beforeReplay)
	}
	for i := range beforeReplay {
		if afterReplay[i] != beforeReplay[i] {
			t.Fatalf("replay member %d = %#v, want byte-identical %#v", i, afterReplay[i], beforeReplay[i])
		}
	}
}

func TestImportCollectionSnapshotRollsBackAfterEveryWriteFailpoint(t *testing.T) {
	tests := []struct {
		name           string
		memberPosition int
	}{
		{name: "collection", memberPosition: -1},
		{name: "first member", memberPosition: 0},
		{name: "middle member", memberPosition: 1},
		{name: "last member", memberPosition: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openSeeded(t)
			ctx := t.Context()
			ids := []MediaID{"media-alpha"}
			for i := 1; i < 3; i++ {
				media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
					LibraryID:          "library-local",
					Path:               fmt.Sprintf("/media/local/failpoint-member-%d.mkv", i),
					Title:              fmt.Sprintf("Failpoint Member %d", i),
					Container:          "mkv",
					Codec:              "h264",
					ContentFingerprint: fmt.Sprintf("failpoint-%d-member-fingerprint", i),
					OSHash:             fmt.Sprintf("%016x", i+200),
				})
				if err != nil {
					t.Fatalf("seed member %d: %v", i, err)
				}
				ids = append(ids, media.ID)
			}
			collectionID := CollectionID("collection-import-failpoint")
			var trigger string
			if tt.memberPosition < 0 {
				trigger = `
					CREATE TEMP TRIGGER fail_import_collection
					AFTER INSERT ON collection
					WHEN NEW.id = 'collection-import-failpoint'
					BEGIN
						SELECT RAISE(ABORT, 'injected collection import failure');
					END;
				`
			} else {
				trigger = fmt.Sprintf(`
					CREATE TEMP TRIGGER fail_import_collection_member
					AFTER INSERT ON collection_media
					WHEN NEW.collection_id = 'collection-import-failpoint' AND NEW.sort_order = %d
					BEGIN
						SELECT RAISE(ABORT, 'injected collection member import failure');
					END;
				`, tt.memberPosition)
			}
			if _, err := st.catalogW.ExecContext(ctx, trigger); err != nil {
				t.Fatalf("install failpoint: %v", err)
			}

			_, err := st.ImportCollectionSnapshot(ctx, Collection{
				ID:        collectionID,
				LibraryID: "library-local",
				Name:      "Must Roll Back",
			}, ids, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
			if err == nil || !strings.Contains(err.Error(), "injected collection") {
				t.Fatalf("ImportCollectionSnapshot err = %v, want injected failure", err)
			}
			if _, err := st.CollectionByID(ctx, collectionID, true); !errors.Is(err, ErrCollectionNotFound) {
				t.Fatalf("collection after rollback err = %v, want ErrCollectionNotFound", err)
			}
			var memberCount int
			if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection_media WHERE collection_id = ?`, collectionID).Scan(&memberCount); err != nil {
				t.Fatal(err)
			}
			if memberCount != 0 {
				t.Fatalf("members after rollback = %d, want 0", memberCount)
			}
		})
	}
}

func TestImportCollectionSnapshotRollsBackAcrossBatchBoundary(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	ids := make([]MediaID, 0, bulkWriteChunkSize+1)
	ids = append(ids, "media-alpha")
	for i := 1; i <= bulkWriteChunkSize; i++ {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/media/local/batch-failpoint-member-%d.mkv", i),
			Title:              fmt.Sprintf("Batch Failpoint Member %d", i),
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("batch-failpoint-%d-member-fingerprint", i),
			OSHash:             fmt.Sprintf("%016x", i+300),
		})
		if err != nil {
			t.Fatalf("seed member %d: %v", i, err)
		}
		ids = append(ids, media.ID)
	}
	collectionID := CollectionID("collection-import-batch-failpoint")
	//nolint:gosec // G201: the interpolated sort order is the internal fixed batch-size constant, not external input.
	if _, err := st.catalogW.ExecContext(ctx, fmt.Sprintf(`
		CREATE TEMP TRIGGER fail_import_collection_second_batch
		AFTER INSERT ON collection_media
		WHEN NEW.collection_id = 'collection-import-batch-failpoint' AND NEW.sort_order = %d
		BEGIN
			SELECT RAISE(ABORT, 'injected second-batch collection member failure');
		END;
	`, bulkWriteChunkSize)); err != nil {
		t.Fatalf("install second-batch failpoint: %v", err)
	}

	_, err := st.ImportCollectionSnapshot(ctx, Collection{
		ID:        collectionID,
		LibraryID: "library-local",
		Name:      "Must Roll Back Across Batches",
	}, ids, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "injected second-batch") {
		t.Fatalf("ImportCollectionSnapshot err = %v, want second-batch injected failure", err)
	}
	if _, err := st.CollectionByID(ctx, collectionID, true); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("collection after rollback err = %v, want ErrCollectionNotFound", err)
	}
	var memberCount int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection_media WHERE collection_id = ?`, collectionID).Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if memberCount != 0 {
		t.Fatalf("members after second-batch rollback = %d, want 0", memberCount)
	}
}

func TestImportCollectionSnapshotGapFillRollsBackWhenFinalCollectionWriteFails(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	collection := Collection{ID: "collection-import-gap-failpoint", LibraryID: "library-local", Name: "Gap Fill"}
	if _, err := st.ImportCollectionSnapshot(ctx, collection, []MediaID{"media-alpha"}, now); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/local/gap-failpoint-member.mkv",
		Title:              "Gap Failpoint Member",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "gap-failpoint-member-fingerprint",
	})
	if err != nil {
		t.Fatalf("seed gap member: %v", err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TEMP TRIGGER fail_import_collection_update
		AFTER UPDATE ON collection
		WHEN NEW.id = 'collection-import-gap-failpoint'
		BEGIN
			SELECT RAISE(ABORT, 'injected final collection import failure');
		END;
	`); err != nil {
		t.Fatalf("install final-write failpoint: %v", err)
	}

	_, err = st.ImportCollectionSnapshot(ctx, collection, []MediaID{"media-alpha", media.ID}, now.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "injected final collection import failure") {
		t.Fatalf("gap-fill err = %v, want injected final collection failure", err)
	}
	persisted, err := st.CollectionByID(ctx, collection.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.MemberCount != 1 {
		t.Fatalf("member count after rollback = %d, want original 1", persisted.MemberCount)
	}
	members, err := st.ResolveCollectionMembers(ctx, collection.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].MediaID != "media-alpha" {
		t.Fatalf("members after rollback = %#v, want original media-alpha only", members)
	}
}

// TestResolveCollectionMembersRevealFiltering verifies ADR-0098 area 4: ordered
// static membership reads apply Reveal/Sealed filtering before counting/ordering,
// so an inaccessible/restricted-unrevealed member is dropped (not gapped) and the
// visibility-filtered count differs from the raw MemberCount.
func TestResolveCollectionMembersRevealFiltering(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true}
	unrevealed := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}}

	// Collection lives in the restricted Archive library; its sole member
	// (media-archive) is only visible while the session is revealed.
	col, err := st.CreateCollection(ctx, Collection{LibraryID: "library-archive", Name: "Sealed Set"}, now)
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	added, err := st.AddCollectionMedia(ctx, col.ID, []MediaID{"media-archive"}, revealed, now)
	if err != nil {
		t.Fatalf("AddCollectionMedia (revealed): %v", err)
	}
	if added.MemberCount != 1 {
		t.Fatalf("raw MemberCount = %d, want 1", added.MemberCount)
	}

	// Revealed read returns the member.
	revealedMembers, err := st.ResolveCollectionMembers(ctx, col.ID, revealed)
	if err != nil {
		t.Fatalf("ResolveCollectionMembers (revealed): %v", err)
	}
	if len(revealedMembers) != 1 || revealedMembers[0].MediaID != "media-archive" {
		t.Fatalf("revealed members = %#v, want [media-archive]", revealedMembers)
	}

	// Unrevealed read must drop the restricted member — count must not leak.
	hiddenMembers, err := st.ResolveCollectionMembers(ctx, col.ID, unrevealed)
	if err != nil {
		t.Fatalf("ResolveCollectionMembers (unrevealed): %v", err)
	}
	if len(hiddenMembers) != 0 {
		t.Fatalf("unrevealed members = %#v, want empty (restricted member leaked)", hiddenMembers)
	}

	// The pack-target/seed convenience mirrors the filtered ordering.
	ids, err := st.CollectionMemberMediaIDs(ctx, col.ID, revealed)
	if err != nil {
		t.Fatalf("CollectionMemberMediaIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "media-archive" {
		t.Fatalf("CollectionMemberMediaIDs = %#v, want [media-archive]", ids)
	}
	hiddenIDs, err := st.CollectionMemberMediaIDs(ctx, col.ID, unrevealed)
	if err != nil {
		t.Fatalf("CollectionMemberMediaIDs (unrevealed): %v", err)
	}
	if len(hiddenIDs) != 0 {
		t.Fatalf("CollectionMemberMediaIDs unrevealed = %#v, want empty", hiddenIDs)
	}
}

// TestResolveCollectionMembersSealedFiltering pins the MUSIC-4 leak fix: a
// Collection member whose Media lives in a SEALED Library must be dropped from
// the resolved membership, its count, and the ordered ID list — even though the
// Library is NOT restricted and the session is fully allowed/revealed. library-local
// is unrestricted; sealing it via filter.SealedIDs must hide its member.
func TestResolveCollectionMembersSealedFiltering(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	unsealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	sealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, SealedIDs: []LibraryID{"library-local"}}

	col, err := st.CreateCollection(ctx, Collection{LibraryID: "library-local", Name: "Mix"}, now)
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if _, err := st.AddCollectionMedia(ctx, col.ID, []MediaID{"media-alpha"}, unsealed, now); err != nil {
		t.Fatalf("AddCollectionMedia: %v", err)
	}

	// Unsealed: member present (baseline — the fix must not break legitimate reads).
	members, err := st.ResolveCollectionMembers(ctx, col.ID, unsealed)
	if err != nil {
		t.Fatalf("ResolveCollectionMembers (unsealed): %v", err)
	}
	if len(members) != 1 || members[0].MediaID != "media-alpha" {
		t.Fatalf("unsealed members = %#v, want [media-alpha]", members)
	}

	// Sealed: member must be absent from the ordered membership AND the count.
	sealedMembers, err := st.ResolveCollectionMembers(ctx, col.ID, sealed)
	if err != nil {
		t.Fatalf("ResolveCollectionMembers (sealed): %v", err)
	}
	if len(sealedMembers) != 0 {
		t.Fatalf("sealed members = %#v, want empty (sealed member leaked)", sealedMembers)
	}
	sealedIDs, err := st.CollectionMemberMediaIDs(ctx, col.ID, sealed)
	if err != nil {
		t.Fatalf("CollectionMemberMediaIDs (sealed): %v", err)
	}
	if len(sealedIDs) != 0 {
		t.Fatalf("sealed CollectionMemberMediaIDs = %#v, want empty", sealedIDs)
	}
}

func TestCollectionRejectsParentCycle(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	parent, err := st.CreateCollection(ctx, Collection{LibraryID: "library-local", Name: "Parent"}, now)
	if err != nil {
		t.Fatalf("CreateCollection parent: %v", err)
	}
	child, err := st.CreateCollection(ctx, Collection{LibraryID: "library-local", ParentID: parent.ID, Name: "Child"}, now)
	if err != nil {
		t.Fatalf("CreateCollection child: %v", err)
	}
	_, err = st.UpdateCollection(ctx, parent.ID, CollectionPatch{ParentID: &child.ID}, now)
	if err == nil {
		t.Fatal("UpdateCollection accepted descendant parent cycle")
	}
}

func TestUpdateSmartCollectionSourceAndSort(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	created, err := st.CreateSmartCollection(ctx, SmartCollection{
		LibraryID:  "library-local",
		Name:       "Recent",
		SourceExpr: `{"kind":"all-items"}`,
	}, now)
	if err != nil {
		t.Fatalf("CreateSmartCollection: %v", err)
	}
	updated, err := st.UpdateSmartCollection(ctx, SmartCollection{
		ID:         created.ID,
		Name:       "Recent first",
		SourceExpr: `{"kind":"recently-added"}`,
		SortExpr:   `{"field":"added","direction":"desc"}`,
	})
	if err != nil {
		t.Fatalf("UpdateSmartCollection: %v", err)
	}
	if updated.Name != "Recent first" || updated.SourceExpr != `{"kind":"recently-added"}` || updated.SortExpr != `{"field":"added","direction":"desc"}` {
		t.Fatalf("updated smart collection = %#v", updated)
	}
}

func TestSmartCollectionSourceItemsUsesSavedExpression(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	if err := st.SetMediaFields(ctx, "media-alpha", []MediaField{{Name: "genre", Type: EntityFieldText, Text: "sci-fi"}}); err != nil {
		t.Fatalf("SetMediaFields: %v", err)
	}
	collection, err := st.CreateSmartCollection(ctx, SmartCollection{
		LibraryID:  "library-local",
		Name:       "Recent",
		SourceExpr: `{"kind":"all-items","fieldFilters":{"genre":"sci-fi"}}`,
	}, now)
	if err != nil {
		t.Fatalf("CreateSmartCollection: %v", err)
	}

	items := mustSourceItems(t, st, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}, SourceQuery{
		Kind:  "smart-collection",
		Value: string(collection.ID),
	})

	if got, want := sourceItemIDs(items), []MediaID{"media-alpha"}; !equalMediaIDs(got, want) {
		t.Fatalf("smart collection rail = %v, want %v", got, want)
	}
}

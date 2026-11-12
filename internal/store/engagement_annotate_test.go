package store

import (
	"context"
	"testing"
)

func openDemoSeeded(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), t.TempDir(), Options{SeedProfile: SeedProfileDemo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func engagementFixture() []SourceItem {
	return []SourceItem{
		{ID: "media-demo-sintel", Kind: "media"},
		{ID: "media-demo-bunny", Kind: "media"},
		{ID: "media-demo-series-2", Kind: "media"},
		{ID: "entity-demo-sequence", Kind: "entity"},
	}
}

func TestAnnotateEngagementReportsProgressCompletionAndContainerCounts(t *testing.T) {
	st := openDemoSeeded(t)
	items := engagementFixture()
	if err := st.AnnotateEngagement(context.Background(), "profile-demo-default", LibraryFilter{AllLibraries: true}, items); err != nil {
		t.Fatalf("AnnotateEngagement: %v", err)
	}

	partial := items[0].Engagement
	if partial == nil || partial.PositionSeconds != 240 || partial.DurationSeconds != 888 || partial.Completed {
		t.Fatalf("in-progress item engagement = %+v, want position 240 of 888, not completed", partial)
	}
	done := items[1].Engagement
	if done == nil || !done.Completed || done.PositionSeconds != 0 {
		t.Fatalf("finished item engagement = %+v, want completed with no position", done)
	}
	if items[2].Engagement != nil {
		t.Fatalf("untouched item engagement = %+v, want nil", items[2].Engagement)
	}
	container := items[3].Engagement
	if container == nil || container.TotalCount != 2 || container.RemainingCount != 1 || container.Completed {
		t.Fatalf("container engagement = %+v, want 1 remaining of 2", container)
	}
}

func TestAnnotateEngagementWithoutProfileIsNoOp(t *testing.T) {
	st := openDemoSeeded(t)
	items := engagementFixture()
	if err := st.AnnotateEngagement(context.Background(), "", LibraryFilter{AllLibraries: true}, items); err != nil {
		t.Fatalf("AnnotateEngagement: %v", err)
	}
	for _, item := range items {
		if item.Engagement != nil {
			t.Fatalf("%s engagement = %+v, want nil without a Profile", item.ID, item.Engagement)
		}
	}
}

// A container's counts must only ever include children the scope can see
// (ADR-0004): a scope without the children's Library gets no counts at all.
func TestAnnotateEngagementContainerCountsRespectVisibility(t *testing.T) {
	st := openDemoSeeded(t)
	items := []SourceItem{{ID: "entity-demo-sequence", Kind: "entity"}}
	hidden := LibraryFilter{AllowedIDs: []LibraryID{"library-no-such"}}
	if err := st.AnnotateEngagement(context.Background(), "profile-demo-default", hidden, items); err != nil {
		t.Fatalf("AnnotateEngagement: %v", err)
	}
	if items[0].Engagement != nil {
		t.Fatalf("container engagement = %+v, want nil when no child is visible", items[0].Engagement)
	}
}

// An open Encrypted Library keeps its relations in its own database; its
// containers still report counts, and stop once the Library is sealed.
func TestAnnotateEngagementCountsAnEncryptedLibrarysChildren(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	library := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, library, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	first, _, err := st.ImportMediaFile(ctx, encryptedImport(library, "sha256:annotate-encrypted-a", "/enc/annotate-a.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := st.ImportMediaFile(ctx, encryptedImport(library, "sha256:annotate-encrypted-b", "/enc/annotate-b.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	entity := Entity{ID: "entity-annotate-encrypted", LibraryID: library, Kind: "container", Title: "Sealed"}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	for order, id := range []MediaID{first.ID, second.ID} {
		if _, err := st.encryptedDBs[library].writer.ExecContext(ctx, `
			INSERT INTO entity_relation(from_entity_id, relation, to_media_id, sort_order)
			VALUES (?, 'contains', ?, ?)
		`, entity.ID, id, order+1); err != nil {
			t.Fatal(err)
		}
	}
	filter := LibraryFilter{AllLibraries: true}
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{MediaID: first.ID}, true, filter); err != nil {
		t.Fatal(err)
	}

	items := []SourceItem{{ID: MediaID(entity.ID), Kind: "entity"}}
	if err := st.AnnotateEngagement(ctx, "profile-default", filter, items); err != nil {
		t.Fatalf("AnnotateEngagement: %v", err)
	}
	got := items[0].Engagement
	if got == nil || got.TotalCount != 2 || got.RemainingCount != 1 || got.Completed {
		t.Fatalf("encrypted container engagement = %+v, want 1 remaining of 2", got)
	}

	if err := st.SealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	sealed := []SourceItem{{ID: MediaID(entity.ID), Kind: "entity"}}
	if err := st.AnnotateEngagement(ctx, "profile-default", filter, sealed); err != nil {
		t.Fatalf("AnnotateEngagement sealed: %v", err)
	}
	if sealed[0].Engagement != nil {
		t.Fatalf("sealed container engagement = %+v, want nil", sealed[0].Engagement)
	}
}

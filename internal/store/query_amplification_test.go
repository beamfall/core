package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestEntityRelationRowsStatementCountDoesNotScaleWithPage(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, Entity{
		ID: "entity-count-source", LibraryID: "library-local", Kind: "container", Title: "Source",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		id := EntityID(fmt.Sprintf("entity-count-%02d", i))
		if err := st.UpsertEntity(ctx, Entity{
			ID: id, LibraryID: "library-local", Kind: "item", Title: string(id),
			Fields: []EntityField{{Name: "label", Type: EntityFieldText, Text: string(id)}},
		}); err != nil {
			t.Fatal(err)
		}
		order := i
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: "entity-count-source", Name: "children", ToEntityID: id, SortOrder: &order,
		}); err != nil {
			t.Fatal(err)
		}
	}
	installCountingCatalogReader(t, st)
	filter := LibraryFilter{AllLibraries: true}

	restrictedQueryCount.Store(0)
	if _, err := st.EntityRelationRows(ctx, "entity-count-source", "children", filter, 1, 0); err != nil {
		t.Fatal(err)
	}
	one := restrictedQueryCount.Load()
	restrictedQueryCount.Store(0)
	if _, err := st.EntityRelationRows(ctx, "entity-count-source", "children", filter, 20, 0); err != nil {
		t.Fatal(err)
	}
	twenty := restrictedQueryCount.Load()
	if one != twenty {
		t.Fatalf("statement count scaled with page: one=%d twenty=%d", one, twenty)
	}
}

func TestUnmatchedItemsStatementCountDoesNotScaleWithPage(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: "library-local", Path: fmt.Sprintf("/unmatched/count-%02d.mp4", i),
			Title: fmt.Sprintf("Count %02d", i), Container: "mp4", Codec: "h264",
			ContentFingerprint: fmt.Sprintf("unmatched-count-%02d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.QueueUnmatched(ctx, UnmatchedItem{
			MediaID: media.ID, PluginID: "test", ScraperID: "test", QueuedAt: time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatal(err)
		}
	}
	installCountingCatalogReader(t, st)
	filter := LibraryFilter{AllLibraries: true}

	restrictedQueryCount.Store(0)
	if _, err := st.UnmatchedItems(ctx, filter, 1, 0); err != nil {
		t.Fatal(err)
	}
	one := restrictedQueryCount.Load()
	restrictedQueryCount.Store(0)
	if _, err := st.UnmatchedItems(ctx, filter, 20, 0); err != nil {
		t.Fatal(err)
	}
	twenty := restrictedQueryCount.Load()
	if one != twenty {
		t.Fatalf("statement count scaled with page: one=%d twenty=%d", one, twenty)
	}
}

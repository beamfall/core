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

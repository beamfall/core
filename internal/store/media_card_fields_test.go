package store

import (
	"context"
	"testing"
)

// TestMediaCardFieldValuesReadsNamedTextAndNumberFields pins CORE-1226: the
// bulk card read returns only the named text and number fields, keyed by
// Media. Card annotation preserves provenance from the browse read.
func TestMediaCardFieldValuesReadsNamedTextAndNumberFields(t *testing.T) {
	st := openDemo(t)
	ctx := context.Background()
	all := mustLibraryBrowse(t, st, LibraryBrowseQuery{Limit: 2})
	if len(all.Items) < 2 {
		t.Fatalf("demo library holds %d items, want 2", len(all.Items))
	}
	first, second := all.Items[0].ID, all.Items[1].ID
	if err := st.SetMediaFields(ctx, first, []MediaField{
		{Name: "sequence", Type: EntityFieldNumber, Number: 5},
		{Name: "index", Type: EntityFieldNumber, Number: 3},
		{Name: "part-title", Type: EntityFieldText, Text: " Reunion "},
		{Name: "other", Type: EntityFieldText, Text: "unread"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, second, nil); err != nil {
		t.Fatal(err)
	}
	values, err := st.MediaCardFieldValues(ctx, []MediaID{first, second}, []string{"sequence", "index", "part-title"})
	if err != nil {
		t.Fatal(err)
	}
	got := values[first]
	if len(got) != 3 || got["sequence"].Number != 5 || got["index"].Number != 3 || got["part-title"].Text != "Reunion" {
		t.Fatalf("first values = %+v", got)
	}
	if _, ok := values[second]; ok {
		t.Fatalf("second values = %+v, want absent", values[second])
	}
	if empty, err := st.MediaCardFieldValues(ctx, []MediaID{first}, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no names = %v, %v", empty, err)
	}

	items := []SourceItem{all.Items[0], {ID: first, Kind: "media"}}
	if err := st.AnnotateMediaClass(ctx, items); err != nil {
		t.Fatal(err)
	}
	if items[0].LibraryID != "library-demo-open" {
		t.Fatalf("browse LibraryID = %q, want library-demo-open", items[0].LibraryID)
	}
	if items[1].LibraryID != "" {
		t.Fatalf("annotation assigned owner %q to a row without catalog provenance", items[1].LibraryID)
	}
}

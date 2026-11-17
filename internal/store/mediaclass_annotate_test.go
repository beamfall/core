package store

import (
	"context"
	"testing"
)

// TestAnnotateMediaClassSetsMediaClasses pins each Media item's class and
// year; the demo sequence Entity takes the class its video Media share and
// never a year (CORE-2362).
func TestAnnotateMediaClassSetsMediaClasses(t *testing.T) {
	st := openDemoSeeded(t)
	ctx := context.Background()
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET media_class = 'audio' WHERE id = 'media-demo-bunny'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET media_class = 'video', year = 2010 WHERE id = 'media-demo-sintel'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET year = NULL WHERE id = 'media-demo-bunny'`); err != nil {
		t.Fatal(err)
	}
	items := []SourceItem{
		{ID: "media-demo-sintel", Kind: "media"},
		{ID: "media-demo-bunny", Kind: "media"},
		{ID: "media-does-not-exist", Kind: "media"},
		{ID: "entity-demo-sequence", Kind: "entity"},
	}
	if err := st.AnnotateMediaClass(ctx, items); err != nil {
		t.Fatalf("AnnotateMediaClass: %v", err)
	}
	want := []MediaClass{MediaClassVideo, MediaClassAudio, "", MediaClassVideo}
	// Only the item with a stored year carries one (CORE-2269).
	wantYear := []int{2010, 0, 0, 0}
	for i, item := range items {
		if item.MediaClass != want[i] {
			t.Errorf("%s MediaClass = %q, want %q", item.ID, item.MediaClass, want[i])
		}
		if item.Year != wantYear[i] {
			t.Errorf("%s Year = %d, want %d", item.ID, item.Year, wantYear[i])
		}
	}
	if err := st.AnnotateMediaClass(ctx, nil); err != nil {
		t.Fatalf("AnnotateMediaClass(nil): %v", err)
	}
}

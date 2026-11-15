package store

import (
	"context"
	"testing"
)

func TestAnnotateMediaClassSetsMediaClassesAndSkipsEntities(t *testing.T) {
	st := openDemoSeeded(t)
	ctx := context.Background()
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET media_class = 'audio' WHERE id = 'media-demo-bunny'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET media_class = 'video' WHERE id = 'media-demo-sintel'`); err != nil {
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
	want := []MediaClass{MediaClassVideo, MediaClassAudio, "", ""}
	for i, item := range items {
		if item.MediaClass != want[i] {
			t.Errorf("%s MediaClass = %q, want %q", item.ID, item.MediaClass, want[i])
		}
	}
	if err := st.AnnotateMediaClass(ctx, nil); err != nil {
		t.Fatalf("AnnotateMediaClass(nil): %v", err)
	}
}

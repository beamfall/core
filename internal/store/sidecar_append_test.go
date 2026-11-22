package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestAddMediaFileSidecarCaptionUpsertsByPath(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const fp = "fp-subtitle-append"
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/media/clip.mkv", Title: "Clip",
		Container: "mkv", Codec: "h264", ContentFingerprint: fp,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}
	caption := store.MediaFileSidecarCaption{RelPath: "clip.pt-BR.sdh.srt", Format: "srt", Language: "pt-BR", SDH: true}
	if err := st.AddMediaFileSidecarCaption(ctx, fp, caption); err != nil {
		t.Fatalf("add: %v", err)
	}
	// A second add of the same path updates the row rather than duplicating it.
	caption.SDH = false
	if err := st.AddMediaFileSidecarCaption(ctx, fp, caption); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	got, err := st.MediaFileSidecarCaptions(ctx, fp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].RelPath != "clip.pt-BR.sdh.srt" || got[0].Language != "pt-BR" || got[0].SDH || got[0].Kind != store.SidecarKindCaption {
		t.Fatalf("sidecars = %+v", got)
	}
}

func TestAddMediaFileSidecarCaptionRejectsPathsOutsideTheFolder(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	for _, rel := range []string{"", "../x.srt", "sub/x.srt", `a\b.srt`} {
		err := st.AddMediaFileSidecarCaption(ctx, "fp", store.MediaFileSidecarCaption{RelPath: rel, Format: "srt"})
		var verr store.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("rel %q: err = %v, want ValidationError", rel, err)
		}
	}
}

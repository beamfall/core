package store_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func importClassed(t *testing.T, st *store.Store, title string, class store.MediaClass) (store.MediaID, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), title+".flac")
	if err := os.WriteFile(path, []byte("a-"+title), 0o600); err != nil {
		t.Fatal(err)
	}
	fp, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: title, Container: "flac", Codec: "flac",
		Class: class, ContentFingerprint: fp, DurationSecondsHint: 240,
	})
	if err != nil {
		t.Fatalf("import %s: %v", title, err)
	}
	return media.ID, fp
}

func TestSonicAnalysisToggleDefaultOff(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	enabled, err := st.SonicAnalysisEnabled(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("sonic analysis must default OFF")
	}
	if err := st.SetSonicAnalysisEnabled(ctx, "library-local", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if enabled, err = st.SonicAnalysisEnabled(ctx, "library-local"); err != nil || !enabled {
		t.Fatalf("enabled = %v, %v; want true", enabled, err)
	}
	if err := st.SetSonicAnalysisEnabled(ctx, "library-missing", true, time.Now().UTC()); err == nil {
		t.Fatal("enabling an unknown Library should error")
	}
}

func TestSonicAnalysisCandidatesHonourToggleClassAndExistingVectors(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	first, firstFP := importClassed(t, st, "first", store.MediaClassAudio)
	second, _ := importClassed(t, st, "second", store.MediaClassAudio)
	video, _ := importClassed(t, st, "picture", store.MediaClassVideo)

	ids, err := st.SonicAnalysisCandidatesAfter(ctx, store.SonicEmbeddingKind, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("toggle off: candidates = %v, want none", ids)
	}

	if err := st.SetSonicAnalysisEnabled(ctx, "library-local", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ids, err = st.SonicAnalysisCandidatesAfter(ctx, store.SonicEmbeddingKind, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ids, first) || !slices.Contains(ids, second) || slices.Contains(ids, video) {
		t.Fatalf("candidates = %v, want both audio Media and not %s", ids, video)
	}
	if !slices.IsSorted(ids) {
		t.Fatalf("candidates %v are not in Media ID order", ids)
	}

	if err := st.UpsertEmbedding(ctx, firstFP, store.SonicEmbeddingKind, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	ids, err = st.SonicAnalysisCandidatesAfter(ctx, store.SonicEmbeddingKind, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ids, first) {
		t.Fatalf("analysed Media %s is still a candidate", first)
	}
	// A vector of another kind does not count as analysed.
	other, err := st.SonicAnalysisCandidatesAfter(ctx, "other-kind", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(other, first) {
		t.Fatalf("other-kind candidates = %v, want %s", other, first)
	}
	// The cursor is exclusive.
	after, err := st.SonicAnalysisCandidatesAfter(ctx, "other-kind", min(first, second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(after, min(first, second)) {
		t.Fatalf("cursor %s returned itself: %v", min(first, second), after)
	}
}

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	books "github.com/beamfall/plugin-audiobook"
)

// seedThreePartBook turns media-alpha into an audio book of three Parts.
func seedThreePartBook(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	paths := make([]string, 3)
	for i := range paths {
		paths[i] = filepath.Join(dir, "part"+string(rune('0'+i))+".flac")
		if err := os.WriteFile(paths[i], []byte(strings.Repeat("p", 10+i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetPrimaryMediaFile(ctx, "media-alpha", paths[0]); err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForMedia(ctx, "media-alpha", revealedPackFilter())
	if err != nil || len(files) != 1 {
		t.Fatalf("MediaFilesForMedia = %d, %v", len(files), err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: paths[0], Title: "Sample Book", Container: "flac", Codec: "flac",
		Class: store.MediaClassAudio, ContentFingerprint: files[0].ContentFingerprint, DurationSecondsHint: 120,
	}); err != nil {
		t.Fatal(err)
	}
	for part := 1; part < len(paths); part++ {
		if err := st.AddMediaFileVersion(ctx, "media-alpha", store.MediaFile{
			Path: paths[part], ContentFingerprint: store.HashMediaFilePath(paths[part]), PartOrdinal: part,
		}, nil); err != nil {
			t.Fatalf("AddMediaFileVersion part %d: %v", part, err)
		}
	}
	end := 60.0
	for _, marker := range []store.Marker{
		{MediaID: "media-alpha", Title: "Chapter 1", StartSeconds: 0, EndSeconds: &end},
		{MediaID: "media-alpha", Title: "Chapter 2", StartSeconds: 60},
		{MediaID: "media-alpha", Title: "Opening credits", StartSeconds: 0, EndSeconds: &end, SegmentKind: "intro"},
	} {
		if _, err := st.CreateMarker(ctx, marker, revealedPackFilter(), time.Now().UTC()); err != nil {
			t.Fatalf("CreateMarker %q: %v", marker.Title, err)
		}
	}
}

// manifestPayload decodes the signed manifest's claims without verifying it;
// the offline pack tests cover verification.
func manifestPayload(t *testing.T, token string) offlinepack.ManifestClaims {
	t.Helper()
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("manifest has %d segments, want 3", len(segments))
	}
	raw, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims offlinepack.ManifestClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestAudiobookPackResolverPacksEveryPartAndItsChapters(t *testing.T) {
	st := storetest.Open(t)
	seedThreePartBook(t, st)
	service := newMusicPackSvc(t, st)
	inner, ok := books.PackResolvers()[books.PackKindBook]
	if !ok {
		t.Fatalf("audiobook plugin has no %q resolver", books.PackKindBook)
	}
	service.RegisterResolver(books.PackKindBook, audiobookPackResolver{store: st, inner: inner})

	pack, err := service.BuildPack(context.Background(), packAuthScope(), offlinepack.BuildPackRequest{
		TargetKind: books.PackKindBook, TargetRef: "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if pack.ItemCount != 1 {
		t.Fatalf("pack item count = %d, want 1 book", pack.ItemCount)
	}
	items, err := st.OfflinePackItems(context.Background(), pack.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("pack items = %d, want 3 Parts", len(items))
	}
	for i, item := range items {
		if item.MediaID != "media-alpha" || item.PartOrdinal != i || item.PlaintextSize != int64(10+i) {
			t.Fatalf("item %d = %+v, want media-alpha Part %d of %d bytes", i, item, i, 10+i)
		}
	}
	manifest := manifestPayload(t, pack.Manifest)
	if len(manifest.Items) != 3 {
		t.Fatalf("manifest items = %d, want 3", len(manifest.Items))
	}
	for i, item := range manifest.Items {
		if item.MediaID != "media-alpha" || item.PartOrdinal != i {
			t.Fatalf("manifest item %d = %+v, want media-alpha Part %d", i, item, i)
		}
		if i > 0 && len(item.Markers) != 0 {
			t.Fatalf("manifest Part %d carries %d Markers, want none", i, len(item.Markers))
		}
	}
	chapters := manifest.Items[0].Markers
	if len(chapters) != 2 || chapters[0].Title != "Chapter 1" || chapters[0].EndSeconds == nil ||
		*chapters[0].EndSeconds != 60 || chapters[1].Title != "Chapter 2" || chapters[1].StartSeconds != 60 ||
		chapters[1].EndSeconds != nil {
		t.Fatalf("Part 0 chapters = %+v, want Chapter 1 [0,60) and Chapter 2 at 60, no intro", chapters)
	}

	// A book with a missing Part is refused rather than packed half-complete.
	if err := st.MarkMediaFileMissing(context.Background(), "library-local", missingPart(t, st)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BuildPack(context.Background(), packAuthScope(), offlinepack.BuildPackRequest{
		TargetKind: books.PackKindBook, TargetRef: "media-alpha",
	}); err == nil || !strings.Contains(err.Error(), "missing Part") {
		t.Fatalf("BuildPack with a missing Part = %v, want a missing Part refusal", err)
	}

	resolver := audiobookPackResolver{store: st, inner: inner}
	localOnly := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	if _, err := resolver.Resolve(context.Background(), "media-archive", localOnly); err == nil {
		t.Fatal("resolver packed a book outside the caller's Libraries")
	}
}

// missingPart returns the path of the book's last Part.
func missingPart(t *testing.T, st *store.Store) string {
	t.Helper()
	files, err := st.MediaFilesForMedia(context.Background(), "media-alpha", revealedPackFilter())
	if err != nil || len(files) != 3 {
		t.Fatalf("MediaFilesForMedia = %d, %v", len(files), err)
	}
	return files[2].Path
}

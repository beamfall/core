package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestEncryptedLibraryPlaybackRenditionsStayInTheLibraryPlane pins CORE-1963:
// a rendition row for an Encrypted Library is stored only in that Library's
// own DB (never the shared catalog, which would leak the Media-to-content
// join) and is unreachable once the Library is Sealed.
func TestEncryptedLibraryPlaybackRenditionsStayInTheLibraryPlane(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:encrypted-rendition", "/enc/rendition.mkv"))
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	cache := filepath.Join(t.TempDir(), "enc-rendition")
	rendition := PlaybackRendition{
		ID:                 "precompute-enc:rendition-test:720p",
		MediaID:            media.ID,
		ContentFingerprint: "sha256:encrypted-rendition",
		Kind:               PlaybackRenditionKindHLS,
		Status:             PlaybackRenditionReady,
		RungID:             "720p",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		Path:               cache,
		SizeBytes:          2048,
		CreatedAt:          time.Unix(300, 0).UTC(),
		LastUsedAt:         time.Unix(301, 0).UTC(),
	}
	if err := st.UpsertPlaybackRendition(ctx, rendition); err != nil {
		t.Fatalf("UpsertPlaybackRendition: %v", err)
	}

	var catalogRows, planeRows int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM playback_rendition WHERE id = ?`, string(rendition.ID)).Scan(&catalogRows); err != nil {
		t.Fatalf("catalog count: %v", err)
	}
	if err := st.encryptedDBs[libraryID].reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM playback_rendition WHERE id = ?`, string(rendition.ID)).Scan(&planeRows); err != nil {
		t.Fatalf("plane count: %v", err)
	}
	if catalogRows != 0 || planeRows != 1 {
		t.Fatalf("rendition rows catalog=%d plane=%d, want 0 and 1", catalogRows, planeRows)
	}

	filter := LibraryFilter{AllowedIDs: []LibraryID{libraryID}}
	got, err := st.ReadyPlaybackRenditions(ctx, media.ID, filter)
	if err != nil || len(got) != 1 || got[0].Path != cache {
		t.Fatalf("ReadyPlaybackRenditions = (%+v, %v), want the plane row", got, err)
	}
	paths, err := st.ReadyPlaybackRenditionPaths(ctx)
	if err != nil || !slices.Contains(paths, cache) {
		t.Fatalf("ReadyPlaybackRenditionPaths = (%v, %v), want %q", paths, err, cache)
	}
	health, err := st.PlaybackRenditionCacheHealth(ctx)
	if err != nil || health.Ready < 1 || health.ReadyBytes < rendition.SizeBytes {
		t.Fatalf("PlaybackRenditionCacheHealth = (%+v, %v), want the plane row counted", health, err)
	}
	if err := st.RecordPlaybackRenditionUse(ctx, rendition.ID, time.Unix(400, 0).UTC()); err != nil {
		t.Fatalf("RecordPlaybackRenditionUse: %v", err)
	}

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if got, _ := st.ReadyPlaybackRenditions(ctx, media.ID, filter); len(got) != 0 {
		t.Fatalf("sealed ReadyPlaybackRenditions = %+v, want none", got)
	}
	paths, err = st.ReadyPlaybackRenditionPaths(ctx)
	if err != nil || slices.Contains(paths, cache) {
		t.Fatalf("sealed ReadyPlaybackRenditionPaths = (%v, %v), want %q absent", paths, err, cache)
	}
	if err := st.UpsertPlaybackRendition(ctx, rendition); err == nil {
		t.Fatal("sealed UpsertPlaybackRendition succeeded, want refusal")
	}
}

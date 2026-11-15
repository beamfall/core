package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	filmtv "github.com/beamfall/plugin-filmtv"
)

// copyLibraryDBCipher stands in for the at-rest cipher: the encrypted
// Library database is copied between its sealed and working paths.
type copyLibraryDBCipher struct{}

func (copyLibraryDBCipher) UnsealDBFile(_ context.Context, _ store.LibraryID, ciphertextPath, workingPath string) error {
	if _, err := os.Stat(ciphertextPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return copyLibraryDBFile(ciphertextPath, workingPath)
}

func (copyLibraryDBCipher) SealDBFile(_ context.Context, _ store.LibraryID, workingPath, ciphertextPath string) error {
	if err := os.MkdirAll(filepath.Dir(ciphertextPath), 0o700); err != nil {
		return err
	}
	return copyLibraryDBFile(workingPath, ciphertextPath)
}

func copyLibraryDBFile(from, to string) error {
	src, err := os.Open(from) //nolint:gosec // test-owned temp path
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // test-owned temp path
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// TestFilmTVPackResolversReadAnUnsealedEncryptedLibrary pins CORE-1742:
// season and series packs resolve from an unsealed Encrypted Library's own
// database as they do from the catalog, and refuse once it is sealed.
func TestFilmTVPackResolversReadAnUnsealedEncryptedLibrary(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	library := store.LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, library, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, library, copyLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: library, Path: "/enc/episode-1.mkv", ContentFingerprint: "sha256:enc-episode-1",
		Title: "Episode One", Container: "matroska", Codec: "h264", DurationSecondsHint: 60, Class: store.MediaClassVideo,
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := newFilmTVHydrationMapper()
	for _, entity := range []store.Entity{
		{ID: "enc-series", LibraryID: library, Kind: kinds.parentKind, Title: "Series One"},
		{ID: "enc-season", LibraryID: library, Kind: kinds.groupKind, Title: "Season One", Fields: []store.EntityField{
			{Name: "number", Type: store.EntityFieldNumber, Number: 1},
		}},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity %s: %v", entity.ID, err)
		}
	}
	for _, relation := range []store.EntityRelation{
		{FromEntityID: "enc-season", Name: filmtv.SeasonOfRelation, ToEntityID: "enc-series"},
		{FromEntityID: "enc-season", Name: filmtv.SeasonEpisodeRelation, ToMediaID: media.ID},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatalf("UpsertEntityRelation %s: %v", relation.Name, err)
		}
	}

	filter := revealedPackFilter()
	refs := map[string]string{filmtv.PackKindSeason: "enc-season", filmtv.PackKindSeries: "enc-series"}
	for kind, ref := range refs {
		got, err := mustFilmTVPackResolver(t, st, kind).Resolve(ctx, ref, filter)
		if err != nil {
			t.Fatalf("%s Resolve: %v", kind, err)
		}
		if !reflect.DeepEqual(got.MediaIDs, []store.MediaID{media.ID}) {
			t.Fatalf("%s MediaIDs = %v, want [%s]", kind, got.MediaIDs, media.ID)
		}
	}

	if err := st.SealEncryptedLibraryDB(ctx, library, copyLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	for kind, ref := range refs {
		if got, err := mustFilmTVPackResolver(t, st, kind).Resolve(ctx, ref, filter); err == nil && len(got.MediaIDs) != 0 {
			t.Fatalf("sealed %s Resolve = %v, want a refusal", kind, got.MediaIDs)
		}
	}
}

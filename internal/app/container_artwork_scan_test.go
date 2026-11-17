package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// copyArtworkTools scales an image by copying its bytes, so an imported
// sidecar stays comparable.
type copyArtworkTools struct{}

func (copyArtworkTools) FrameGrab(_ context.Context, _ string, _ float64, _ int, dst string) error {
	return os.WriteFile(dst, []byte("frame"), 0o600)
}

func (copyArtworkTools) ScaleImage(_ context.Context, src string, _ int, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- the test's own sidecar under t.TempDir().
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- the artwork service's slot under t.TempDir().
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// showSeasonHydrator writes the container Entities local hydration writes for
// an episode file: a season holding the Media and a show holding the season.
type showSeasonHydrator struct {
	st *store.Store
}

func (showSeasonHydrator) Capability() scanpkg.MetadataHydrationCapability {
	return scanpkg.MetadataHydrationCapability{MediaClasses: []store.MediaClass{store.MediaClassVideo}}
}

func (h showSeasonHydrator) HydrateMetadata(ctx context.Context, input scanpkg.MetadataHydrationInput) error {
	one := 1
	for _, entity := range []store.Entity{
		{ID: "entity-show", LibraryID: input.LibraryID, Kind: "show", Title: "Show"},
		{ID: "entity-season-1", LibraryID: input.LibraryID, Kind: "season", Title: "Season 1"},
	} {
		if err := h.st.UpsertEntity(ctx, entity); err != nil {
			return err
		}
	}
	for _, relation := range []store.EntityRelation{
		{FromEntityID: "entity-season-1", Name: "season-episode", ToMediaID: input.MediaID, Cardinality: "one", SortOrder: &one},
		{FromEntityID: "entity-season-1", Name: "season-of", ToEntityID: "entity-show", SortOrder: &one},
	} {
		if err := h.st.UpsertEntityRelation(ctx, relation); err != nil {
			return err
		}
	}
	return nil
}

func showSeasonBundle() singleBundle {
	return singleBundle{
		ID: "demo",
		EntityKinds: []plugin.EntityKind{
			{Name: "show", Archetype: "container", OrderedChildren: true},
			{Name: "season", Archetype: "container", OrderedChildren: true, Relations: []plugin.RelationSpec{
				{Name: "season-episode", ToKind: plugin.MediaKindName, Cardinality: "one"},
				{Name: "season-of", ToKind: "show"},
			}},
		},
	}
}

// jpegMagic opens a sidecar so the local-artwork import accepts it as a JPEG.
const jpegMagic = "\xFF\xD8\xFF\xE0"

func writeSettledFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// scanContainerArtwork scans root into a fresh Library of the demo plugin with
// identification's container resolver wired, as the composition root wires
// it, and returns the artwork service.
func scanContainerArtwork(t *testing.T, root string) *artwork.Service {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	library, err := st.CreateLibrary(ctx, "Shows", false, "demo", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	service := identify.NewService(scrape.New(), showSeasonBundle(), st)
	service.SetEntities(st)
	svc := artwork.NewService(st, copyArtworkTools{}, t.TempDir())
	_, err = scanpkg.ScanWithWorkerGovernor(ctx, st, nil, nil, nil, nil, &scanIdentifier{service: service}, nil,
		map[string]scanpkg.MetadataHydrator{"demo": showSeasonHydrator{st: st}}, nil,
		scanpkg.Request{LibraryID: library.ID, Root: root}, svc)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// TestScanImportsContainerArtworkThroughIdentification (CORE-1488): with the
// real identification resolver, the season's numbered poster beside the
// season folder lands on the season Entity and the show folder's poster on the
// show Entity, both locked against scraped art.
func TestScanImportsContainerArtworkThroughIdentification(t *testing.T) {
	root := t.TempDir()
	writeSettledFile(t, root, "Show/Season 01/S01E01.mkv", "video")
	writeSettledFile(t, root, "Show/season01-poster.jpg", jpegMagic+"season poster")
	writeSettledFile(t, root, "Show/poster.jpg", jpegMagic+"show poster")

	svc := scanContainerArtwork(t, root)
	for id, want := range map[store.EntityID]string{"entity-season-1": jpegMagic + "season poster", "entity-show": jpegMagic + "show poster"} {
		got, err := os.ReadFile(svc.EntityPosterPath(id))
		if err != nil || string(got) != want {
			t.Fatalf("%s poster = %q, %v; want %q", id, got, err, want)
		}
		if err := svc.ImportEntityPoster(context.Background(), id, []byte("scraped")); err == nil {
			t.Fatalf("scraped poster replaced the local %s poster; want the slot locked", id)
		}
	}
}

// TestScanContainerArtworkStaysInsideRoot (CORE-1488): a library rooted at the
// season folder reads no sidecar above its root, so neither container gets a
// poster.
func TestScanContainerArtworkStaysInsideRoot(t *testing.T) {
	show := t.TempDir()
	writeSettledFile(t, show, "Season 01/S01E01.mkv", "video")
	writeSettledFile(t, show, "season01-poster.jpg", jpegMagic+"season poster")
	writeSettledFile(t, show, "poster.jpg", jpegMagic+"show poster")

	svc := scanContainerArtwork(t, filepath.Join(show, "Season 01"))
	for _, id := range []store.EntityID{"entity-season-1", "entity-show"} {
		if _, err := os.Stat(svc.EntityPosterPath(id)); !os.IsNotExist(err) {
			t.Fatalf("%s poster from outside the root: stat = %v, want not exist", id, err)
		}
	}
}

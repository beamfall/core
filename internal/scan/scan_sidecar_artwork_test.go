package scan

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// copyingArtworkTools is a frame grabber that counts grabs and a scaler that
// copies bytes verbatim, so imported sidecar bytes stay comparable.
type copyingArtworkTools struct {
	mu    sync.Mutex
	grabs int
}

func (c *copyingArtworkTools) FrameGrab(_ context.Context, _ string, _ float64, _ int, dst string) error {
	c.mu.Lock()
	c.grabs++
	c.mu.Unlock()
	return os.WriteFile(dst, []byte("grabbed frame"), 0o600)
}

func (c *copyingArtworkTools) ScaleImage(_ context.Context, src string, _ int, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func jpegBytes(label string) []byte {
	return append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, label...)
}

func scanWithArtwork(t *testing.T, st *store.Store, root string, identifier Identifier, svc *artwork.Service) Result {
	t.Helper()
	result, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, identifier, nil, nil, fixedGovernor(4), Request{
		LibraryID: "library-local",
		Root:      root,
	}, svc)
	if err != nil {
		t.Fatalf("ScanWithWorkerGovernor: %v", err)
	}
	return result
}

func mediaIDForPath(t *testing.T, result Result, suffix string) store.MediaID {
	t.Helper()
	for _, file := range result.Files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), suffix) {
			return file.MediaID
		}
	}
	t.Fatalf("no imported file ends with %q in %#v", suffix, result.Files)
	return ""
}

func slotBytes(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading slot %s: %v", path, err)
	}
	return string(data)
}

func TestLocalArtworkFolderSidecarsFillSlotsWithoutFrameGrab(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Movie (2020)/Movie (2020).mkv", []byte("video bytes"))
	writeScanFile(t, root, "Movie (2020)/poster.jpg", jpegBytes("poster"))
	writeScanFile(t, root, "Movie (2020)/fanart.jpg", jpegBytes("fanart"))
	st := storetest.Open(t)
	tools := &copyingArtworkTools{}
	svc := artwork.NewService(st, tools, t.TempDir())

	result := scanWithArtwork(t, st, root, nil, svc)
	id := mediaIDForPath(t, result, "Movie (2020).mkv")
	if got := slotBytes(t, svc.ArtworkPath(id, artwork.SlotPoster)); got != string(jpegBytes("poster")) {
		t.Fatalf("poster = %q, want poster.jpg bytes", got)
	}
	if got := slotBytes(t, svc.ArtworkPath(id, artwork.SlotBackdrop)); got != string(jpegBytes("fanart")) {
		t.Fatalf("backdrop = %q, want fanart.jpg bytes", got)
	}
	if err := svc.GeneratePoster(context.Background(), id); err != nil {
		t.Fatalf("GeneratePoster: %v", err)
	}
	if tools.grabs != 0 {
		t.Fatalf("frame grabs = %d, want 0 with a local poster", tools.grabs)
	}
	if got := slotBytes(t, svc.ArtworkPath(id, artwork.SlotPoster)); got != string(jpegBytes("poster")) {
		t.Fatalf("poster after GeneratePoster = %q, want local bytes", got)
	}
}

func TestLocalArtworkFileLevelSidecarWinsOverFolder(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Clip/Clip.mkv", []byte("video bytes"))
	writeScanFile(t, root, "Clip/Clip-thumb.jpg", jpegBytes("file thumb"))
	writeScanFile(t, root, "Clip/thumb.jpg", jpegBytes("folder thumb"))
	st := storetest.Open(t)
	svc := artwork.NewService(st, &copyingArtworkTools{}, t.TempDir())

	result := scanWithArtwork(t, st, root, nil, svc)
	id := mediaIDForPath(t, result, "Clip/Clip.mkv")
	if got := slotBytes(t, svc.ArtworkPath(id, artwork.SlotThumb)); got != string(jpegBytes("file thumb")) {
		t.Fatalf("thumb = %q, want <stem>-thumb.jpg bytes", got)
	}
}

func TestLocalArtworkFolderSidecarSkipsSharedFolders(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Shared/one.mkv", []byte("video one"))
	writeScanFile(t, root, "Shared/two.mkv", []byte("video two"))
	writeScanFile(t, root, "Shared/poster.jpg", jpegBytes("poster"))
	writeScanFile(t, root, "Shared/two-poster.png", []byte("\x89PNG\r\n\x1a\ntwo"))
	st := storetest.Open(t)
	svc := artwork.NewService(st, &copyingArtworkTools{}, t.TempDir())

	result := scanWithArtwork(t, st, root, nil, svc)
	if svc.HasPoster(mediaIDForPath(t, result, "Shared/one.mkv")) {
		t.Fatal("folder poster applied in a folder with two media files")
	}
	if got := slotBytes(t, svc.PosterPath(mediaIDForPath(t, result, "Shared/two.mkv"))); got != "\x89PNG\r\n\x1a\ntwo" {
		t.Fatalf("two poster = %q, want its file-level png", got)
	}
}

func TestLocalArtworkLocksScrapesFollowsEditsAndReleasesOnRemoval(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeScanFile(t, root, "Movie (2020)/Movie (2020).mkv", []byte("video bytes"))
	poster := writeScanFile(t, root, "Movie (2020)/poster.jpg", jpegBytes("first"))
	st := storetest.Open(t)
	svc := artwork.NewService(st, &copyingArtworkTools{}, t.TempDir())

	id := mediaIDForPath(t, scanWithArtwork(t, st, root, nil, svc), "Movie (2020).mkv")
	if err := svc.ImportArtwork(ctx, id, artwork.SlotPoster, jpegBytes("scraped")); !errors.Is(err, artwork.ErrSlotLocked) {
		t.Fatalf("scrape over local poster = %v, want ErrSlotLocked", err)
	}
	if got := slotBytes(t, svc.PosterPath(id)); got != string(jpegBytes("first")) {
		t.Fatalf("poster after scrape = %q, want local bytes", got)
	}

	// Edit the sidecar only; the media file is unchanged.
	if err := os.WriteFile(poster, jpegBytes("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(poster, edited, edited); err != nil {
		t.Fatal(err)
	}
	scanWithArtwork(t, st, root, nil, svc)
	if got := slotBytes(t, svc.PosterPath(id)); got != string(jpegBytes("edited")) {
		t.Fatalf("poster after edit = %q, want edited sidecar bytes", got)
	}

	if err := os.Remove(poster); err != nil {
		t.Fatal(err)
	}
	scanWithArtwork(t, st, root, nil, svc)
	if svc.IsArtworkLocked(id, artwork.SlotPoster) {
		t.Fatal("lock survived the sidecar's removal")
	}
	if got := slotBytes(t, svc.PosterPath(id)); got != string(jpegBytes("edited")) {
		t.Fatalf("poster after removal = %q, want art kept", got)
	}
	if err := svc.ImportArtwork(ctx, id, artwork.SlotPoster, jpegBytes("scraped")); err != nil {
		t.Fatalf("scrape after release = %v", err)
	}
}

func TestLocalArtworkSealedLibraryImportsNothing(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Movie (2020)/Movie (2020).mkv", []byte("video bytes"))
	writeScanFile(t, root, "Movie (2020)/poster.jpg", jpegBytes("poster"))
	writeScanFile(t, root, "Movie (2020)/Movie (2020)-fanart.jpg", jpegBytes("fanart"))
	st := storetest.Open(t)
	svc := artwork.NewService(st, &copyingArtworkTools{}, t.TempDir())
	svc.SetSealed(func(id store.LibraryID) bool { return id == "library-local" })

	result := scanWithArtwork(t, st, root, nil, svc)
	id := mediaIDForPath(t, result, "Movie (2020).mkv")
	for _, slot := range []string{artwork.SlotPoster, artwork.SlotBackdrop, artwork.SlotThumb} {
		path := svc.ArtworkPath(id, slot)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Sealed Library wrote %s: %v", path, err)
		}
		if svc.IsArtworkLocked(id, slot) {
			t.Fatalf("Sealed Library wrote a %s lock", slot)
		}
	}
}

// containerIdentifier resolves Media under one folder to an ordered container.
type containerIdentifier struct {
	folder   string
	entityID store.EntityID
	ordinal  int
	st       *store.Store
}

func (c *containerIdentifier) AutoIdentify(context.Context, store.MediaID, string, string, ParsedMedia) error {
	return nil
}

func (c *containerIdentifier) ContainerEntityForMedia(ctx context.Context, id store.MediaID) (store.EntityID, int, bool, error) {
	file, err := c.st.PrimaryMediaFile(ctx, id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		return "", 0, false, err
	}
	if filepath.Base(filepath.Dir(file.Path)) != c.folder {
		return "", 0, false, nil
	}
	return c.entityID, c.ordinal, true, nil
}

func TestLocalArtworkContainerPosterLandsOnContainerEntity(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Show/season01-poster.jpg", jpegBytes("container one"))
	writeScanFile(t, root, "Show/season02-poster.jpg", jpegBytes("container two"))
	writeScanFile(t, root, "Show/Season 1/Show S01E01.mkv", []byte("video one"))
	writeScanFile(t, root, "Show/Season 1/Show S01E02.mkv", []byte("video two"))
	st := storetest.Open(t)
	svc := artwork.NewService(st, &copyingArtworkTools{}, t.TempDir())
	identifier := &containerIdentifier{folder: "Season 1", entityID: "entity-container-1", ordinal: 1, st: st}

	scanWithArtwork(t, st, root, identifier, svc)
	if got := slotBytes(t, svc.EntityPosterPath("entity-container-1")); got != string(jpegBytes("container one")) {
		t.Fatalf("container poster = %q, want season01-poster.jpg bytes", got)
	}
	if err := svc.ImportEntityPoster(context.Background(), "entity-container-1", jpegBytes("scraped")); !errors.Is(err, artwork.ErrSlotLocked) {
		t.Fatalf("scrape over container poster = %v, want ErrSlotLocked", err)
	}
}

func TestLocalArtworkVocabularyTargetsKnownSlots(t *testing.T) {
	for _, row := range localArtworkVocabulary {
		if row.Slot != "" && !artwork.ValidSlot(row.Slot) {
			t.Fatalf("vocabulary row %+v targets unknown slot", row)
		}
		switch row.Name {
		case "logo", "clearlogo", "banner":
			if row.Slot != "" {
				t.Fatalf("vocabulary row %+v imports a name that has no slot yet", row)
			}
		}
	}
	for _, ext := range localArtworkExts {
		switch ext {
		case "jpg", "jpeg", "png", "webp":
		default:
			t.Fatalf("unexpected local artwork format %q", ext)
		}
	}
	if slots := localArtworkMediaSlots(); strings.Join(slots, ",") != "poster,backdrop,thumb" {
		t.Fatalf("media slots = %v, want poster,backdrop,thumb", slots)
	}
}

func TestLocalArtworkContainerPosterReleasesAtLibraryRoot(t *testing.T) {
	root := t.TempDir()
	poster := writeScanFile(t, root, "season01-poster.jpg", jpegBytes("container one"))
	writeScanFile(t, root, "Show S01E01.mkv", []byte("video one"))
	st := storetest.Open(t)
	svc := artwork.NewService(st, &copyingArtworkTools{}, t.TempDir())
	identifier := &containerIdentifier{folder: filepath.Base(root), entityID: "entity-container-1", ordinal: 1, st: st}

	scanWithArtwork(t, st, root, identifier, svc)
	if !svc.IsEntityArtworkLocked("entity-container-1", artwork.SlotPoster) {
		t.Fatal("root-level container poster was not imported and locked")
	}
	if err := os.Remove(poster); err != nil {
		t.Fatal(err)
	}
	scanWithArtwork(t, st, root, identifier, svc)
	if svc.IsEntityArtworkLocked("entity-container-1", artwork.SlotPoster) {
		t.Fatal("root-level container lock survived its sidecar's removal")
	}
	if got := slotBytes(t, svc.EntityPosterPath("entity-container-1")); got != string(jpegBytes("container one")) {
		t.Fatalf("container poster after removal = %q, want art kept", got)
	}
}

func TestLocalArtworkReleasesWhenFolderSidecarStopsMatching(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Movie (2020)/Movie (2020).mkv", []byte("video bytes"))
	writeScanFile(t, root, "Movie (2020)/poster.jpg", jpegBytes("folder"))
	st := storetest.Open(t)
	svc := artwork.NewService(st, &copyingArtworkTools{}, t.TempDir())

	id := mediaIDForPath(t, scanWithArtwork(t, st, root, nil, svc), "Movie (2020).mkv")
	if !svc.IsArtworkLocked(id, artwork.SlotPoster) {
		t.Fatal("folder poster was not imported and locked")
	}
	// A second media file makes poster.jpg ambiguous; it still exists but no
	// longer matches the first Media, so the lock goes and the art stays.
	writeScanFile(t, root, "Movie (2020)/Sequel (2022).mkv", []byte("other video"))
	scanWithArtwork(t, st, root, nil, svc)
	if svc.IsArtworkLocked(id, artwork.SlotPoster) {
		t.Fatal("lock survived although no sidecar matches the slot any more")
	}
	if got := slotBytes(t, svc.PosterPath(id)); got != string(jpegBytes("folder")) {
		t.Fatalf("poster after release = %q, want art kept", got)
	}
}

// recordingLocalArtwork records releases for the deferred-release tests.
type recordingLocalArtwork struct {
	released map[localArtSlot]time.Time
}

func (r *recordingLocalArtwork) ImportLocalArtwork(context.Context, store.MediaID, string, string, string) (bool, error) {
	return false, nil
}

func (r *recordingLocalArtwork) ReleaseLocalArtwork(_ context.Context, id store.MediaID, slot string, observedAt time.Time) error {
	r.released[localArtSlot{media: id, slot: slot}] = observedAt
	return nil
}

func TestDeferredLocalReleaseSkipsSlotsAnotherPartMatched(t *testing.T) {
	recorder := &recordingLocalArtwork{released: map[localArtSlot]time.Time{}}
	started := time.Now().Add(-time.Minute)
	s := &scanWalkState{
		ctx:           context.Background(),
		localArtwork:  recorder,
		containerArt:  map[store.EntityID]*containerArtChoice{},
		localReleases: map[localArtSlot]bool{},
		localMatched:  map[localArtSlot]bool{},
		startedAt:     started,
	}
	matched := localArtSlot{media: "media-parts", slot: artwork.SlotPoster}
	unmatched := localArtSlot{media: "media-parts", slot: artwork.SlotBackdrop}
	// One part of the Media found no poster, another part did.
	s.localReleases[matched] = true
	s.localMatched[matched] = true
	s.localReleases[unmatched] = true

	s.applyDeferredArtwork()
	if _, ok := recorder.released[matched]; ok {
		t.Fatal("released a slot another file of the same Media matched")
	}
	if got, ok := recorder.released[unmatched]; !ok || !got.Equal(started) {
		t.Fatalf("unmatched release = %v, %v; want the walk start time", got, ok)
	}
	if len(s.localReleases) != 0 || len(s.localMatched) != 0 {
		t.Fatal("deferred release state was not cleared")
	}
}

package artwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

var (
	testJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'l', 'o', 'c', 'a', 'l'}
	testPNG  = []byte("\x89PNG\r\n\x1a\nlocal-png")
	testWEBP = []byte("RIFF\x00\x00\x00\x00WEBPVP8 local")
)

func writeSidecar(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readSlot(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestImportLocalArtworkLocksSlotAgainstScrapes(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	sidecar := writeSidecar(t, t.TempDir(), "poster.jpg", testJPEG)

	changed, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, filepath.Dir(sidecar), sidecar)
	if err != nil || !changed {
		t.Fatalf("ImportLocalArtwork = %v, %v; want true, nil", changed, err)
	}
	if got := readSlot(t, svc.PosterPath("media-alpha")); got != string(testJPEG) {
		t.Fatalf("poster = %q, want sidecar bytes", got)
	}
	if !svc.IsArtworkLocked("media-alpha", SlotPoster) {
		t.Fatal("local import did not lock the slot")
	}
	info, err := os.Stat(lockMarkerPath(svc.PosterPath("media-alpha")))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock marker mode = %v, want 0600", info.Mode().Perm())
	}

	err = svc.ImportArtwork(ctx, "media-alpha", SlotPoster, []byte("scraped"))
	var locked *SlotLockedError
	if !errors.As(err, &locked) || !errors.Is(err, ErrSlotLocked) || locked.Source != SourceLocal || locked.Slot != SlotPoster {
		t.Fatalf("scrape import error = %v, want SlotLockedError from local", err)
	}
	if got := readSlot(t, svc.PosterPath("media-alpha")); got != string(testJPEG) {
		t.Fatalf("scrape replaced locked poster with %q", got)
	}
	// Other slots stay open to scrapes.
	if err := svc.ImportArtwork(ctx, "media-alpha", SlotBackdrop, []byte("scraped")); err != nil {
		t.Fatalf("unlocked slot import error = %v", err)
	}
}

func TestImportLocalArtworkIsNoOpUntilSidecarChanges(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	sidecar := writeSidecar(t, t.TempDir(), "poster.jpg", testJPEG)
	if _, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, filepath.Dir(sidecar), sidecar); err != nil {
		t.Fatal(err)
	}
	changed, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, filepath.Dir(sidecar), sidecar)
	if err != nil || changed {
		t.Fatalf("unchanged sidecar re-import = %v, %v; want false, nil", changed, err)
	}

	writeSidecar(t, filepath.Dir(sidecar), "poster.jpg", testPNG)
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(sidecar, later, later); err != nil {
		t.Fatal(err)
	}
	changed, err = svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, filepath.Dir(sidecar), sidecar)
	if err != nil || !changed {
		t.Fatalf("edited sidecar re-import = %v, %v; want true, nil", changed, err)
	}
	if got := readSlot(t, svc.PosterPath("media-alpha")); got != string(testPNG) {
		t.Fatalf("poster = %q, want edited sidecar bytes", got)
	}
}

func TestManualArtworkBypassesLocalLock(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	sidecar := writeSidecar(t, t.TempDir(), "poster.jpg", testJPEG)
	if _, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, filepath.Dir(sidecar), sidecar); err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportManualArtwork(ctx, "media-alpha", SlotPoster, []byte("picked")); err != nil {
		t.Fatalf("ImportManualArtwork = %v", err)
	}
	if got := readSlot(t, svc.PosterPath("media-alpha")); got != "picked" {
		t.Fatalf("poster = %q, want manual pick", got)
	}
	// An unchanged sidecar does not take the slot back from a manual pick.
	if changed, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, filepath.Dir(sidecar), sidecar); err != nil || changed {
		t.Fatalf("unchanged sidecar after manual pick = %v, %v; want false, nil", changed, err)
	}
	if got := readSlot(t, svc.PosterPath("media-alpha")); got != "picked" {
		t.Fatalf("poster = %q, want manual pick kept", got)
	}
}

func TestReleaseLocalArtworkKeepsArt(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	sidecar := writeSidecar(t, t.TempDir(), "fanart.webp", testWEBP)
	if _, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotBackdrop, filepath.Dir(sidecar), sidecar); err != nil {
		t.Fatal(err)
	}
	// A stale release, from a caller that looked before the import, keeps the
	// lock.
	if err := svc.ReleaseLocalArtwork(ctx, "media-alpha", SlotBackdrop, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !svc.IsArtworkLocked("media-alpha", SlotBackdrop) {
		t.Fatal("stale release unlocked a slot a newer import locked")
	}
	// A current release removes the lock even though the recorded sidecar
	// still exists: the caller found no sidecar matching the slot.
	if err := svc.ReleaseLocalArtwork(ctx, "media-alpha", SlotBackdrop, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if svc.IsArtworkLocked("media-alpha", SlotBackdrop) {
		t.Fatal("release left the lock in place")
	}
	if got := readSlot(t, svc.ArtworkPath("media-alpha", SlotBackdrop)); got != string(testWEBP) {
		t.Fatalf("backdrop = %q, want art kept after release", got)
	}
	if err := svc.ImportArtwork(ctx, "media-alpha", SlotBackdrop, []byte("scraped")); err != nil {
		t.Fatalf("scrape after release = %v", err)
	}
	// Releasing a slot that was never locked is a no-op.
	if err := svc.ReleaseLocalArtwork(ctx, "media-alpha", SlotThumb, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestImportLocalArtworkRejectsUnsafeSidecars(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	dir := t.TempDir()
	target := writeSidecar(t, t.TempDir(), "elsewhere.jpg", testJPEG)
	link := filepath.Join(dir, "poster.jpg")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	notImage := writeSidecar(t, dir, "cover.jpg", []byte("<html>not an image</html>"))
	empty := writeSidecar(t, dir, "thumb.jpg", nil)
	for name, path := range map[string]string{"symlink": link, "not an image": notImage, "empty": empty, "relative": "poster.jpg"} {
		if changed, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, dir, path); err == nil || changed {
			t.Fatalf("%s sidecar import = %v, %v; want refusal", name, changed, err)
		}
	}
	if svc.HasPoster("media-alpha") || svc.IsArtworkLocked("media-alpha", SlotPoster) {
		t.Fatal("refused sidecar wrote artwork or a lock")
	}
}

func TestImportLocalArtworkWritesNothingForProtectedLibrary(t *testing.T) {
	ctx := context.Background()
	for name, configure := range map[string]func(*Service, store.LibraryID){
		"sealed":    func(s *Service, id store.LibraryID) { s.SetSealed(func(l store.LibraryID) bool { return l == id }) },
		"encrypted": func(s *Service, id store.LibraryID) { s.SetEncrypted(func(l store.LibraryID) bool { return l == id }) },
	} {
		t.Run(name, func(t *testing.T) {
			st := storetest.Open(t)
			svc := NewService(st, &fakeGrabber{}, t.TempDir())
			libraryID, err := st.MediaLibraryID(ctx, "media-alpha")
			if err != nil {
				t.Fatal(err)
			}
			configure(svc, libraryID)
			sidecar := writeSidecar(t, t.TempDir(), "poster.jpg", testJPEG)
			changed, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, filepath.Dir(sidecar), sidecar)
			if err != nil || changed {
				t.Fatalf("ImportLocalArtwork = %v, %v; want false, nil", changed, err)
			}
			if _, err := os.Stat(filepath.Dir(svc.PosterPath("media-alpha"))); !os.IsNotExist(err) {
				t.Fatalf("artwork directory exists for a %s Library: %v", name, err)
			}
		})
	}
}

func TestGeneratePosterKeepsLocalPoster(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	grabber := &fakeGrabber{embedded: true}
	svc := NewService(st, grabber, t.TempDir())
	sidecar := writeSidecar(t, t.TempDir(), "poster.jpg", testJPEG)
	if _, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotPoster, filepath.Dir(sidecar), sidecar); err != nil {
		t.Fatal(err)
	}
	if err := svc.GeneratePoster(ctx, "media-alpha"); err != nil {
		t.Fatal(err)
	}
	if found, err := svc.ImportEmbeddedPoster(ctx, "media-alpha"); err != nil || found {
		t.Fatalf("ImportEmbeddedPoster = %v, %v; want false, nil", found, err)
	}
	if len(grabber.calls) != 0 || len(grabber.embeddedCalls) != 0 {
		t.Fatalf("grabs = %v, embedded = %v; want none for a local poster", grabber.calls, grabber.embeddedCalls)
	}
	if got := readSlot(t, svc.PosterPath("media-alpha")); got != string(testJPEG) || svc.IsGeneratedArtwork("media-alpha", SlotPoster) {
		t.Fatalf("poster = %q generated=%v, want local bytes", got, svc.IsGeneratedArtwork("media-alpha", SlotPoster))
	}
}

func TestEntityLocalArtworkLocksEntityPoster(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	if err := st.UpsertEntity(ctx, store.Entity{ID: "entity-group", LibraryID: "library-local", Kind: "gizmo", Title: "Group"}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, &fakeGrabber{}, t.TempDir())
	sidecar := writeSidecar(t, t.TempDir(), "season01-poster.jpg", testJPEG)
	if changed, err := svc.ImportEntityLocalArtwork(ctx, "entity-group", SlotPoster, filepath.Dir(sidecar), sidecar); err != nil || !changed {
		t.Fatalf("ImportEntityLocalArtwork = %v, %v; want true, nil", changed, err)
	}
	if err := svc.ImportEntityPoster(ctx, "entity-group", []byte("scraped")); !errors.Is(err, ErrSlotLocked) {
		t.Fatalf("entity scrape error = %v, want ErrSlotLocked", err)
	}
	if got := readSlot(t, svc.EntityPosterPath("entity-group")); got != string(testJPEG) {
		t.Fatalf("entity poster = %q, want sidecar bytes", got)
	}
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReleaseEntityLocalArtwork(ctx, "entity-group", SlotPoster, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportEntityPoster(ctx, "entity-group", []byte("scraped")); err != nil {
		t.Fatalf("entity scrape after release = %v", err)
	}
	sidecar = writeSidecar(t, filepath.Dir(sidecar), "season01-poster.jpg", testJPEG)

	svc.SetSealed(func(store.LibraryID) bool { return true })
	if changed, err := svc.ImportEntityLocalArtwork(ctx, "entity-group", SlotPoster, filepath.Dir(sidecar), sidecar); err != nil || changed {
		t.Fatalf("sealed entity import = %v, %v; want false, nil", changed, err)
	}
	if svc.IsEntityArtworkLocked("entity-group", SlotPoster) {
		t.Fatal("sealed entity import wrote a lock")
	}
	if err := svc.ImportEntityPoster(ctx, "entity-group", []byte("sealed scrape")); err != nil {
		t.Fatalf("sealed entity scrape = %v, want nil no-op", err)
	}
	if got := readSlot(t, svc.EntityPosterPath("entity-group")); got != "scraped" {
		t.Fatalf("sealed entity poster = %q, want the earlier scrape untouched", got)
	}
	svc.SetSealed(func(store.LibraryID) bool { return false })
	if changed, err := svc.ImportEntityLocalArtwork(ctx, "entity-missing", SlotPoster, filepath.Dir(sidecar), sidecar); err != nil || changed {
		t.Fatalf("unresolvable entity import = %v, %v; want fail-closed false, nil", changed, err)
	}
}

func TestImportLocalArtworkStaysInsideLibraryRoot(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	root := t.TempDir()
	outside := t.TempDir()
	escaped := writeSidecar(t, outside, "fanart.jpg", testJPEG)
	if err := os.Symlink(outside, filepath.Join(root, "extrafanart")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for name, path := range map[string]string{
		"directory symlink": filepath.Join(root, "extrafanart", "fanart.jpg"),
		"outside the root":  escaped,
		"dot-dot":           filepath.Join(root, "..", filepath.Base(outside), "fanart.jpg"),
	} {
		if changed, err := svc.ImportLocalArtwork(ctx, "media-alpha", SlotBackdrop, root, path); err == nil || changed {
			t.Fatalf("%s import = %v, %v; want refusal", name, changed, err)
		}
	}
	if svc.IsArtworkLocked("media-alpha", SlotBackdrop) {
		t.Fatal("refused sidecar wrote a lock")
	}
}

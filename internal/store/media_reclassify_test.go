package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAttachedPictureReclassify (CORE-1262): the repair lists only live,
// non-Extra video-class Media with an available file, moves one to audio only
// while it is still video (a resumed repair is a no-op), and records a
// per-Library marker.
func TestAttachedPictureReclassify(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	library, err := st.CreateLibrary(ctx, "Mixed", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	importFile := func(path, fingerprint string, class MediaClass, codec string, extra bool) Media {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: library.ID, Path: path, ContentFingerprint: fingerprint, Title: path,
			Container: "mp3", Codec: codec, Class: class, DurationSecondsHint: 200,
			VideoWidthHint: 600, VideoHeightHint: 600, Extra: extra,
		})
		if err != nil {
			t.Fatalf("import %s: %v", path, err)
		}
		return media
	}
	coverArt := importFile("/m/song-with-cover.mp3", "sha256:cover", MediaClassVideo, "mjpeg", false)
	film := importFile("/m/film.mkv", "sha256:film", MediaClassVideo, "h264", false)
	camera := importFile("/m/camera.avi", "sha256:camera", MediaClassVideo, "mjpeg", false)
	importFile("/m/song.flac", "sha256:flac", MediaClassAudio, "flac", false)
	importFile("/m/trailer.mkv", "sha256:trailer", MediaClassVideo, "mjpeg", true)
	trashed := importFile("/m/trashed.mp3", "sha256:trashed", MediaClassVideo, "mjpeg", false)
	if err := st.TrashMedia(ctx, trashed.ID, LibraryFilter{AllLibraries: true, Revealed: true}); err != nil {
		t.Fatal(err)
	}

	candidates, err := st.AttachedPictureCandidatesInLibrary(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[MediaID]string{}
	for _, c := range candidates {
		got[c.MediaID] = c.Path
	}
	if len(got) != 2 || got[coverArt.ID] != "/m/song-with-cover.mp3" || got[camera.ID] != "/m/camera.avi" {
		t.Fatalf("candidates = %v, want only the live non-Extra video Media with a still-image codec", got)
	}
	if _, listed := got[film.ID]; listed {
		t.Fatalf("an h264 video is a candidate: %v", got)
	}

	changed, err := st.ReclassifyVideoMediaAsAudio(ctx, coverArt.ID, "mp3")
	if err != nil || !changed {
		t.Fatalf("reclassify = (%v, %v), want changed", changed, err)
	}
	if class, err := st.MediaClass(ctx, coverArt.ID); err != nil || class != MediaClassAudio {
		t.Fatalf("class after reclassify = (%q, %v), want audio", class, err)
	}
	var codec string
	var width, height int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT codec, video_width, video_height FROM media WHERE id = ?`, coverArt.ID).Scan(&codec, &width, &height); err != nil {
		t.Fatal(err)
	}
	if codec != "mp3" || width != 0 || height != 0 {
		t.Fatalf("reclassified row codec=%q size=%dx%d, want mp3 0x0", codec, width, height)
	}
	if changed, err := st.ReclassifyVideoMediaAsAudio(ctx, coverArt.ID, "mp3"); err != nil || changed {
		t.Fatalf("repeat reclassify = (%v, %v), want no-op", changed, err)
	}
	if _, err := st.ReclassifyVideoMediaAsAudio(ctx, camera.ID, ""); err == nil {
		t.Fatal("reclassify without an audio codec accepted")
	}
	if candidates, err := st.AttachedPictureCandidatesInLibrary(ctx, library.ID); err != nil || len(candidates) != 1 || candidates[0].MediaID != camera.ID {
		t.Fatalf("candidates after repair = %+v (%v), want only the camera clip", candidates, err)
	}

	state := func() string {
		t.Helper()
		got, err := st.AttachedPictureReclassState(ctx, library.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := state(); got != "" {
		t.Fatalf("state before any write = %q, want empty", got)
	}
	if err := st.SetAttachedPictureReclassState(ctx, library.ID, AttachedPictureReclassPending, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != AttachedPictureReclassPending {
		t.Fatalf("state = %q, want pending", got)
	}
	if err := st.SetAttachedPictureReclassState(ctx, library.ID, AttachedPictureReclassDone, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAttachedPictureReclassState(ctx, library.ID, AttachedPictureReclassPending, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != AttachedPictureReclassDone {
		t.Fatalf("state after a late pending = %q, want done to stay final", got)
	}
	if err := st.SetAttachedPictureReclassState(ctx, library.ID, "bogus", time.Now().UTC()); err == nil {
		t.Fatal("unknown state accepted")
	}
}

// TestAttachedPictureReclassifyEncryptedLibraryUsesProtectedPlane: a sealed
// encrypted Library refuses the listing, and once unsealed the repair reads
// and writes its protected plane.
func TestAttachedPictureReclassifyEncryptedLibraryUsesProtectedPlane(t *testing.T) {
	ctx := context.Background()
	libraryID := LibraryID("library-local")
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AttachedPictureCandidatesInLibrary(ctx, libraryID); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed listing error = %v, want encrypted DB disabled", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	item := encryptedImport(libraryID, "sha256:encrypted-cover", "/encrypted/cover.mp3")
	item.Container, item.Codec = "mp3", "mjpeg"
	media, _, err := st.ImportMediaFile(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := st.AttachedPictureCandidatesInLibrary(ctx, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range candidates {
		found = found || (c.MediaID == media.ID && c.Path == "/encrypted/cover.mp3")
	}
	if !found {
		t.Fatalf("encrypted candidates = %+v, want %s", candidates, media.ID)
	}
	if changed, err := st.ReclassifyVideoMediaAsAudio(ctx, media.ID, "mp3"); err != nil || !changed {
		t.Fatalf("encrypted reclassify = (%v, %v), want changed", changed, err)
	}
	if class, err := st.MediaClass(ctx, media.ID); err != nil || class != MediaClassAudio {
		t.Fatalf("encrypted class = (%q, %v), want audio", class, err)
	}
}

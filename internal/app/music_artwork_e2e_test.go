package app

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

func requireFFmpegTools(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed on PATH")
	}
}

func solidJPEG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func solidPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// dominantRGBChannel inspects the center pixel of an artwork file so tests can
// distinguish red embedded covers, blue folder covers, and yellow CAA posters
// after FFmpeg scaling/normalization.
func dominantRGBChannel(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test artwork file under t.TempDir().
	if err != nil {
		t.Fatalf("reading artwork %s: %v", path, err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding artwork %s: %v", path, err)
	}
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2).RGBA()
	r8, g8, b8 := r>>8, g>>8, bl>>8
	switch {
	case r8 > 180 && g8 < 80 && b8 < 80:
		return "red"
	case g8 > 180 && r8 < 80 && b8 < 80:
		return "green"
	case b8 > 180 && r8 < 80 && g8 < 80:
		return "blue"
	case r8 > 180 && g8 > 180 && b8 < 80:
		return "yellow"
	default:
		return fmt.Sprintf("rgb(%d,%d,%d)", r8, g8, b8)
	}
}

func writeAudioFixture(t *testing.T, root, relPath, artist, album, title string, attachedCover []byte) string {
	t.Helper()
	dst := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatal(err)
	}
	ext := strings.ToLower(filepath.Ext(dst))
	args := []string{"-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=1"}
	var coverTmp string
	if len(attachedCover) > 0 {
		coverExt := ".jpg"
		if bytes.HasPrefix(attachedCover, []byte("\x89PNG")) {
			coverExt = ".png"
		}
		coverTmp = filepath.Join(t.TempDir(), "cover"+coverExt)
		if err := os.WriteFile(coverTmp, attachedCover, 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-i", coverTmp, "-map", "0:0", "-map", "1:0")
	} else {
		args = append(args, "-map", "0:0")
	}
	switch ext {
	case ".mp3":
		args = append(args, "-c:a", "libmp3lame", "-id3v2_version", "3")
		if coverTmp != "" {
			args = append(args, "-c:v", "copy", "-disposition:v:0", "attached_pic")
		}
		args = append(args, "-metadata", "artist="+artist, "-metadata", "album="+album, "-metadata", "title="+title, "-f", "mp3")
	case ".m4a":
		args = append(args, "-c:a", "aac")
		if coverTmp != "" {
			args = append(args, "-c:v", "copy", "-disposition:v:0", "attached_pic")
		}
		args = append(args, "-metadata", "artist="+artist, "-metadata", "album="+album, "-metadata", "title="+title, "-f", "ipod")
	default:
		t.Fatalf("unsupported fixture ext %q", ext)
	}
	args = append(args, dst)
	out, err := exec.Command("ffmpeg", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg fixture %s failed: %v\n%s", relPath, err, string(out))
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dst, old, old); err != nil {
		t.Fatal(err)
	}
	return dst
}

func testMusicBundle() singleBundle {
	return singleBundle{
		ID:           "music",
		MediaClasses: []string{"audio"},
		MediaFields: []plugin.FieldSpec{
			{Name: "hydrated", Type: "flag"},
		},
		EntityKinds: []plugin.EntityKind{
			{Name: "artist", Archetype: "person"},
			{
				Name:            "album",
				Archetype:       "container",
				OrderedChildren: true,
				Relations: []plugin.RelationSpec{
					{Name: "album-track", ToKind: plugin.MediaKindName, Cardinality: "one"},
					{Name: "album-artist", ToKind: "artist"},
				},
			},
		},
		Scrapers: []plugin.ScraperSpec{{
			ID:              "musicbrainz",
			Kind:            "http-json",
			SearchTemplate:  "/ws/2/recording?query={query}",
			CandidateIDPath: "id",
			ArtworkMap: map[string]string{
				artwork.SlotPoster: "releases.0.id",
				artwork.SlotThumb:  "releases.0.id",
			},
			ArtworkURLTemplates: map[string]string{
				artwork.SlotPoster: "https://coverartarchive.org/release/{value}/front",
				artwork.SlotThumb:  "https://coverartarchive.org/release/{value}/front-500",
			},
		}},
	}
}

func testMusicPosterRules() []EntityPosterRule {
	return []EntityPosterRule{
		{PluginID: "music", Kind: "album", Sources: []EntityPosterSource{{Relation: "album-track"}}},
		{PluginID: "music", Kind: "artist", Sources: []EntityPosterSource{{Relation: "album-artist", FromKind: "album"}}},
	}
}

type testMusicHydrator struct {
	st *store.Store
}

func (testMusicHydrator) Capability() scanpkg.MetadataHydrationCapability {
	return scanpkg.MetadataHydrationCapability{
		MediaClasses: []store.MediaClass{store.MediaClassAudio},
	}
}

func (h testMusicHydrator) HydrateMetadata(ctx context.Context, input scanpkg.MetadataHydrationInput) error {
	artist := input.Metadata["artist"]
	if artist == "" {
		artist = input.Metadata["ARTIST"]
	}
	album := input.Metadata["album"]
	if album == "" {
		album = input.Metadata["ALBUM"]
	}
	if album == "" {
		return nil
	}
	albumID := identify.EntityIDForName(input.LibraryID, "album", album)
	if err := h.st.UpsertEntity(ctx, store.Entity{ID: albumID, LibraryID: input.LibraryID, Kind: "album", Title: album}); err != nil {
		return err
	}
	order := 1
	if err := h.st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: albumID, Name: "album-track", ToMediaID: input.MediaID, Cardinality: "one", SortOrder: &order,
	}); err != nil {
		return err
	}
	if artist != "" {
		artistID := identify.EntityIDForName(input.LibraryID, "artist", artist)
		if err := h.st.UpsertEntity(ctx, store.Entity{ID: artistID, LibraryID: input.LibraryID, Kind: "artist", Title: artist}); err != nil {
			return err
		}
		if err := h.st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: albumID, Name: "album-artist", ToEntityID: artistID,
		}); err != nil {
			return err
		}
	}
	return nil
}

type musicCAATestEngine struct {
	match bool
}

type alwaysMatchScorer struct{}

func (alwaysMatchScorer) Score(_ identify.Candidate, _ identify.ParsedMedia) float64 {
	return 1.0
}

func newTestMusicIdentifyService(st *store.Store, engine identify.Engine, fetcher identify.Fetcher, svc *artwork.Service) *identify.Service {
	idSvc := identify.NewService(engine, testMusicBundle(), st)
	idSvc.SetEntities(st)
	idSvc.SetArtwork(fetcher, svc)
	idSvc.SetArtworkCandidates(st)
	idSvc.SetAutoStores(st, st)
	idSvc.RegisterScorer("music", alwaysMatchScorer{}, 0.5)
	return idSvc
}

func (e musicCAATestEngine) Search(_ context.Context, _ plugin.ScraperSpec, query string, _ ...scrape.Variables) ([]scrape.Result, error) {
	if !e.match {
		return nil, nil
	}
	return []scrape.Result{{ID: "rec-1", Fields: map[string]any{"title": query}}}, nil
}

func (e musicCAATestEngine) Lookup(_ context.Context, _ plugin.ScraperSpec, _ string, _ ...scrape.Variables) (scrape.Result, error) {
	if !e.match {
		return scrape.Result{}, nil
	}
	return scrape.Result{
		ID:     "rec-1",
		Fields: map[string]any{"title": "Matched Track"},
		Artwork: map[string]string{
			artwork.SlotPoster: "https://coverartarchive.org/release/rel-1/front",
			artwork.SlotThumb:  "https://coverartarchive.org/release/rel-1/front-500",
		},
	}, nil
}

type recordingCAATestFetcher struct {
	mu      sync.Mutex
	payload []byte
	urls    []string
}

func (f *recordingCAATestFetcher) FetchResource(_ context.Context, rawURL string) ([]byte, error) {
	f.mu.Lock()
	f.urls = append(f.urls, rawURL)
	f.mu.Unlock()
	return append([]byte(nil), f.payload...), nil
}

func (f *recordingCAATestFetcher) fetchedURLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

func runMusicArtworkSweep(t *testing.T, ctx context.Context, st *store.Store, svc *artwork.Service) {
	t.Helper()
	now := time.Now().UTC()
	job, err := artwork.NewSweepJob(now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.EnqueueJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(artwork.SweepJobKind, artwork.SweepHandler(svc))
	for i := 0; i < 16; i++ {
		if _, err := registry.RunOne(ctx, nil, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("sweep RunOne: %v", err)
		}
		loaded, err := st.Job(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == store.JobStatusCompleted {
			return
		}
	}
	t.Fatal("artwork sweep did not complete within 16 iterations")
}

func TestMusicE2EEmbeddedAPICAndCovrWithoutMBIDGetPosters(t *testing.T) {
	requireFFmpegTools(t)
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	lib, err := st.CreateLibrary(ctx, "Music", false, "music", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	redPNG := solidPNG(t, color.RGBA{R: 230, G: 20, B: 20, A: 255})
	greenJPG := solidJPEG(t, color.RGBA{R: 20, G: 220, B: 30, A: 255})
	mp3Path := writeAudioFixture(t, root, "RedArtist/RedAlbum/01 - Red.mp3", "RedArtist", "RedAlbum", "Red Track", redPNG)
	m4aPath := writeAudioFixture(t, root, "GreenArtist/GreenAlbum/01 - Green.m4a", "GreenArtist", "GreenAlbum", "Green Track", greenJPG)
	plainPath := writeAudioFixture(t, root, "PlainArtist/PlainAlbum/01 - Plain.mp3", "PlainArtist", "PlainAlbum", "Plain Track", nil)

	runner := ffmpeg.New()
	svc := artwork.NewService(st, runner, t.TempDir())
	svc.SetEntityPosterRules(testMusicPosterRules())
	idSvc := newTestMusicIdentifyService(st, musicCAATestEngine{match: false}, &recordingCAATestFetcher{}, svc)

	res, err := scanpkg.ScanWithWorkerGovernor(ctx, st, runner, runner, nil, nil, &scanIdentifier{service: idSvc}, nil,
		map[string]scanpkg.MetadataHydrator{"music": testMusicHydrator{st: st}},
		nil, scanpkg.Request{LibraryID: lib.ID, Root: root}, svc)
	if err != nil {
		t.Fatalf("ScanWithWorkerGovernor: %v", err)
	}
	runMusicArtworkSweep(t, ctx, st, svc)

	trackIDs := map[string]store.MediaID{}
	for _, f := range res.Files {
		trackIDs[f.Path] = f.MediaID
	}
	mp3ID := trackIDs[mp3Path]
	m4aID := trackIDs[m4aPath]
	plainID := trackIDs[plainPath]

	if !svc.HasPoster(mp3ID) || !svc.IsEmbeddedArtwork(mp3ID, artwork.SlotPoster) {
		t.Fatalf("MP3 with APIC missing embedded poster: has=%v embedded=%v", svc.HasPoster(mp3ID), svc.IsEmbeddedArtwork(mp3ID, artwork.SlotPoster))
	}
	if got := dominantRGBChannel(t, svc.PosterPath(mp3ID)); got != "red" {
		t.Fatalf("MP3 APIC poster color = %s, want red", got)
	}
	if !svc.HasPoster(m4aID) || !svc.IsEmbeddedArtwork(m4aID, artwork.SlotPoster) {
		t.Fatalf("M4A with covr missing embedded poster: has=%v embedded=%v", svc.HasPoster(m4aID), svc.IsEmbeddedArtwork(m4aID, artwork.SlotPoster))
	}
	if got := dominantRGBChannel(t, svc.PosterPath(m4aID)); got != "green" {
		t.Fatalf("M4A covr poster color = %s, want green", got)
	}
	if svc.HasPoster(plainID) {
		t.Fatal("MP3 without APIC unexpectedly got a poster")
	}

	redAlbumID := identify.EntityIDForName(lib.ID, "album", "RedAlbum")
	redArtistID := identify.EntityIDForName(lib.ID, "artist", "RedArtist")
	greenAlbumID := identify.EntityIDForName(lib.ID, "album", "GreenAlbum")
	if !svc.HasEntityPoster(redAlbumID) || dominantRGBChannel(t, svc.EntityPosterPath(redAlbumID)) != "red" {
		t.Fatalf("RedAlbum failed to inherit embedded APIC poster")
	}
	if !svc.HasEntityPoster(redArtistID) || dominantRGBChannel(t, svc.EntityPosterPath(redArtistID)) != "red" {
		t.Fatalf("RedArtist failed to inherit RedAlbum poster")
	}
	if !svc.HasEntityPoster(greenAlbumID) || dominantRGBChannel(t, svc.EntityPosterPath(greenAlbumID)) != "green" {
		t.Fatalf("GreenAlbum failed to inherit embedded covr poster")
	}
}

func TestMusicE2EAlbumCoverWinsOverEmbeddedPictureAndCAA(t *testing.T) {
	requireFFmpegTools(t)
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	lib, err := st.CreateLibrary(ctx, "Music", false, "music", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	redJPG := solidJPEG(t, color.RGBA{R: 230, G: 20, B: 20, A: 255})
	blueJPG := solidJPEG(t, color.RGBA{R: 20, G: 30, B: 230, A: 255})
	yellowJPG := solidJPEG(t, color.RGBA{R: 235, G: 225, B: 20, A: 255})

	writeSettledFile(t, root, "BlueArtist/BlueAlbum/cover.jpg", string(blueJPG))
	t1Path := writeAudioFixture(t, root, "BlueArtist/BlueAlbum/01 - One.mp3", "BlueArtist", "BlueAlbum", "Track One", redJPG)
	t2Path := writeAudioFixture(t, root, "BlueArtist/BlueAlbum/02 - Two.mp3", "BlueArtist", "BlueAlbum", "Track Two", redJPG)
	writeSettledFile(t, root, "BlueArtist/DiscAlbum/folder.jpg", string(blueJPG))
	d1Path := writeAudioFixture(t, root, "BlueArtist/DiscAlbum/CD 1/01 - D1.mp3", "BlueArtist", "DiscAlbum", "Disc One", redJPG)
	d2Path := writeAudioFixture(t, root, "BlueArtist/DiscAlbum/CD 2/01 - D2.mp3", "BlueArtist", "DiscAlbum", "Disc Two", redJPG)

	runner := ffmpeg.New()
	svc := artwork.NewService(st, runner, t.TempDir())
	svc.SetEntityPosterRules(testMusicPosterRules())
	fetcher := &recordingCAATestFetcher{payload: yellowJPG}
	idSvc := newTestMusicIdentifyService(st, musicCAATestEngine{match: true}, fetcher, svc)

	res, err := scanpkg.ScanWithWorkerGovernor(ctx, st, runner, runner, nil, nil, &scanIdentifier{service: idSvc}, nil,
		map[string]scanpkg.MetadataHydrator{"music": testMusicHydrator{st: st}},
		nil, scanpkg.Request{LibraryID: lib.ID, Root: root}, svc)
	if err != nil {
		t.Fatalf("ScanWithWorkerGovernor: %v", err)
	}
	runMusicArtworkSweep(t, ctx, st, svc)

	if fetched := fetcher.fetchedURLs(); len(fetched) != 0 {
		t.Fatalf("CAA artwork fetched despite local cover/folder.jpg: %v", fetched)
	}
	trackIDs := map[string]store.MediaID{}
	for _, f := range res.Files {
		trackIDs[f.Path] = f.MediaID
	}
	for _, p := range []string{t1Path, t2Path, d1Path, d2Path} {
		id := trackIDs[p]
		if !svc.IsArtworkLocked(id, artwork.SlotPoster) {
			t.Fatalf("track %s poster not locally locked", p)
		}
		if got := dominantRGBChannel(t, svc.PosterPath(id)); got != "blue" {
			t.Fatalf("track %s poster color = %s, want blue cover/folder.jpg", p, got)
		}
		if svc.HasArtwork(id, artwork.SlotThumb) {
			t.Fatalf("track %s thumb populated instead of falling back to local poster", p)
		}
		thumbSlot, thumbOK := svc.ResolveSlot(id, artwork.SlotThumb)
		if !thumbOK || thumbSlot != artwork.SlotPoster {
			t.Fatalf("track %s ResolveSlot(thumb) = (%q, %v), want (%q, true)", p, thumbSlot, thumbOK, artwork.SlotPoster)
		}
	}
	for _, album := range []string{"BlueAlbum", "DiscAlbum"} {
		albumID := identify.EntityIDForName(lib.ID, "album", album)
		if !svc.IsEntityArtworkLocked(albumID, artwork.SlotPoster) {
			t.Fatalf("%s Entity poster not locally locked", album)
		}
		if got := dominantRGBChannel(t, svc.EntityPosterPath(albumID)); got != "blue" {
			t.Fatalf("%s Entity poster color = %s, want blue cover/folder.jpg", album, got)
		}
	}
}

func TestMusicE2EEmbeddedAPICWinsOverCAAAndAddedCoverReplacesCAA(t *testing.T) {
	requireFFmpegTools(t)
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	lib, err := st.CreateLibrary(ctx, "Music", false, "music", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	redJPG := solidJPEG(t, color.RGBA{R: 230, G: 20, B: 20, A: 255})
	blueJPG := solidJPEG(t, color.RGBA{R: 20, G: 30, B: 230, A: 255})
	yellowJPG := solidJPEG(t, color.RGBA{R: 235, G: 225, B: 20, A: 255})

	embeddedPath := writeAudioFixture(t, root, "Artist/EmbAlbum/01 - Emb.mp3", "Artist", "EmbAlbum", "Emb Track", redJPG)
	plainPath := writeAudioFixture(t, root, "Artist/CAAAlbum/01 - CAA.mp3", "Artist", "CAAAlbum", "CAA Track", nil)

	runner := ffmpeg.New()
	svc := artwork.NewService(st, runner, t.TempDir())
	svc.SetEntityPosterRules(testMusicPosterRules())
	fetcher := &recordingCAATestFetcher{payload: yellowJPG}
	idSvc := newTestMusicIdentifyService(st, musicCAATestEngine{match: true}, fetcher, svc)

	res, err := scanpkg.ScanWithWorkerGovernor(ctx, st, runner, runner, nil, nil, &scanIdentifier{service: idSvc}, nil,
		map[string]scanpkg.MetadataHydrator{"music": testMusicHydrator{st: st}},
		nil, scanpkg.Request{LibraryID: lib.ID, Root: root}, svc)
	if err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	runMusicArtworkSweep(t, ctx, st, svc)

	trackIDs := map[string]store.MediaID{}
	for _, f := range res.Files {
		trackIDs[f.Path] = f.MediaID
	}
	embID := trackIDs[embeddedPath]
	plainID := trackIDs[plainPath]

	if !svc.IsEmbeddedArtwork(embID, artwork.SlotPoster) || dominantRGBChannel(t, svc.PosterPath(embID)) != "red" {
		t.Fatalf("embedded track poster failed to beat CAA")
	}
	if svc.HasArtwork(embID, artwork.SlotThumb) {
		t.Fatal("CAA thumb leaked past embedded audio poster")
	}
	embAlbumID := identify.EntityIDForName(lib.ID, "album", "EmbAlbum")
	if dominantRGBChannel(t, svc.EntityPosterPath(embAlbumID)) != "red" {
		t.Fatalf("EmbAlbum failed to inherit embedded poster")
	}
	if dominantRGBChannel(t, svc.PosterPath(plainID)) != "yellow" || !svc.HasArtwork(plainID, artwork.SlotThumb) {
		t.Fatal("track without local/embedded cover did not receive CAA poster+thumb")
	}

	// Adding cover.jpg later must replace prior CAA artwork and clear stale CAA thumb on rescan.
	writeSettledFile(t, root, "Artist/CAAAlbum/cover.jpg", string(blueJPG))
	if _, err := scanpkg.ScanWithWorkerGovernor(ctx, st, runner, runner, nil, nil, &scanIdentifier{service: idSvc}, nil,
		map[string]scanpkg.MetadataHydrator{"music": testMusicHydrator{st: st}},
		nil, scanpkg.Request{LibraryID: lib.ID, Root: root}, svc); err != nil {
		t.Fatalf("rescan after adding cover.jpg: %v", err)
	}
	if dominantRGBChannel(t, svc.PosterPath(plainID)) != "blue" {
		t.Fatalf("rescan did not replace CAA poster with added cover.jpg")
	}
	if svc.HasArtwork(plainID, artwork.SlotThumb) {
		t.Fatal("rescan left stale CAA thumb on audio track after cover.jpg was added")
	}
	caaAlbumID := identify.EntityIDForName(lib.ID, "album", "CAAAlbum")
	if dominantRGBChannel(t, svc.EntityPosterPath(caaAlbumID)) != "blue" {
		t.Fatalf("CAAAlbum poster did not update to added cover.jpg")
	}
}

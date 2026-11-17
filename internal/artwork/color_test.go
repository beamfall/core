package artwork

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// twoToneImage is a 30x20 image whose left two thirds are main and whose right
// third is rest.
func twoToneImage(main, rest color.NRGBA) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 30, 20))
	for y := range 20 {
		for x := range 30 {
			if x < 20 {
				img.SetNRGBA(x, y, main)
			} else {
				img.SetNRGBA(x, y, rest)
			}
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func solidPNG(t *testing.T, c color.NRGBA) []byte {
	t.Helper()
	return encodePNG(t, twoToneImage(c, c))
}

var (
	testRed   = color.NRGBA{R: 255, A: 255}
	testBlue  = color.NRGBA{B: 255, A: 255}
	testWhite = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
)

func writePosterBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDominantColorOfFixedImages(t *testing.T) {
	var jpegTeal bytes.Buffer
	if err := jpeg.Encode(&jpegTeal, twoToneImage(color.NRGBA{R: 16, G: 112, B: 144, A: 255}, testWhite), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	half := image.NewNRGBA(image.Rect(0, 0, 30, 20))
	for y := range 20 {
		for x := range 5 {
			half.SetNRGBA(x, y, testBlue)
		}
	}
	cases := []struct {
		name string
		data []byte
		want color.NRGBA
		// slack is the per-channel distance a lossy encoding may move the colour.
		slack int
	}{
		{name: "solid png", data: solidPNG(t, testRed), want: testRed},
		{name: "most common colour wins", data: encodePNG(t, twoToneImage(testBlue, testWhite)), want: testBlue},
		{name: "jpeg", data: jpegTeal.Bytes(), want: color.NRGBA{R: 16, G: 112, B: 144}, slack: 6},
		{name: "transparent pixels are ignored", data: encodePNG(t, half), want: testBlue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A poster file is named .jpg whatever its bytes are.
			path := filepath.Join(t.TempDir(), "poster.jpg")
			writePosterBytes(t, path, tc.data)
			got, ok := dominantColorOf(path)
			if !ok {
				t.Fatal("dominantColorOf found no colour")
			}
			if len(got) != 7 || got[0] != '#' {
				t.Fatalf("colour = %q, want #rrggbb", got)
			}
			for i, want := range []uint8{tc.want.R, tc.want.G, tc.want.B} {
				channel, err := strconv.ParseUint(got[1+2*i:3+2*i], 16, 8)
				if err != nil {
					t.Fatalf("colour = %q: %v", got, err)
				}
				if diff := int(channel) - int(want); diff > tc.slack || diff < -tc.slack {
					t.Fatalf("colour = %q, want within %d of %v", got, tc.slack, tc.want)
				}
			}
		})
	}
}

func TestDominantColorOfIsLowercaseHex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poster.jpg")
	writePosterBytes(t, path, solidPNG(t, color.NRGBA{R: 0xAB, G: 0xCD, B: 0xEF, A: 255}))
	if got, ok := dominantColorOf(path); !ok || got != "#abcdef" {
		t.Fatalf("dominantColorOf = %q, %v; want #abcdef", got, ok)
	}
}

func TestDominantColorOfRejectsImagesWithNoColour(t *testing.T) {
	cases := map[string][]byte{
		"not an image":      []byte("not an image"),
		"fully transparent": encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 8, 8))),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "poster.jpg")
			writePosterBytes(t, path, data)
			if got, ok := dominantColorOf(path); ok {
				t.Fatalf("dominantColorOf = %q, want none", got)
			}
		})
	}
}

func TestImportPosterRecordsDominantColor(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	ctx := context.Background()
	if err := svc.ImportPoster(ctx, "media-alpha", solidPNG(t, testRed)); err != nil {
		t.Fatal(err)
	}
	// The import records the colour itself; a read does not have to.
	if !regularFileExists(dominantColorPath(svc.PosterPath("media-alpha"))) {
		t.Fatal("import recorded no dominant colour beside the poster")
	}
	if got, ok := svc.DominantColor("media-alpha"); !ok || got != "#ff0000" {
		t.Fatalf("DominantColor = %q, %v; want #ff0000", got, ok)
	}

	if err := svc.ImportPoster(ctx, "media-alpha", solidPNG(t, testBlue)); err != nil {
		t.Fatal(err)
	}
	if got, ok := svc.DominantColor("media-alpha"); !ok || got != "#0000ff" {
		t.Fatalf("DominantColor after a new poster = %q, %v; want #0000ff", got, ok)
	}
}

func TestImportEntityPosterRecordsDominantColor(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	if err := svc.ImportEntityPoster(context.Background(), "entity-maker", solidPNG(t, testBlue)); err != nil {
		t.Fatal(err)
	}
	if !regularFileExists(dominantColorPath(svc.EntityPosterPath("entity-maker"))) {
		t.Fatal("import recorded no dominant colour beside the Entity poster")
	}
	if got, ok := svc.EntityDominantColor("entity-maker"); !ok || got != "#0000ff" {
		t.Fatalf("EntityDominantColor = %q, %v; want #0000ff", got, ok)
	}
}

func TestImportOtherSlotRecordsNoDominantColor(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	if err := svc.ImportArtwork(context.Background(), "media-alpha", SlotBackdrop, solidPNG(t, testRed)); err != nil {
		t.Fatal(err)
	}
	if regularFileExists(dominantColorPath(svc.ArtworkPath("media-alpha", SlotBackdrop))) {
		t.Fatal("a backdrop import recorded a dominant colour")
	}
	if got, ok := svc.DominantColor("media-alpha"); ok {
		t.Fatalf("DominantColor without a poster = %q, want none", got)
	}
}

func TestDominantColorIsRecordedOnFirstRead(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	poster := svc.PosterPath("media-alpha")
	writePosterBytes(t, poster, solidPNG(t, testRed))
	if got, ok := svc.DominantColor("media-alpha"); !ok || got != "#ff0000" {
		t.Fatalf("DominantColor = %q, %v; want #ff0000", got, ok)
	}
	if !regularFileExists(dominantColorPath(poster)) {
		t.Fatal("first read recorded no dominant colour beside the poster")
	}
}

func TestDominantColorFollowsReplacedPosterBytes(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	poster := svc.PosterPath("media-alpha")
	writePosterBytes(t, poster, solidPNG(t, testRed))
	if got, _ := svc.DominantColor("media-alpha"); got != "#ff0000" {
		t.Fatalf("DominantColor = %q, want #ff0000", got)
	}
	// A writer that does not record a colour leaves the red one behind.
	writePosterBytes(t, poster, solidPNG(t, testBlue))
	if got, ok := svc.DominantColor("media-alpha"); !ok || got != "#0000ff" {
		t.Fatalf("DominantColor after the poster changed = %q, %v; want #0000ff", got, ok)
	}
}

func TestDominantColorReadsCurrentSmallBucket(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	poster := svc.PosterPath("media-alpha")
	small := filepath.Join(filepath.Dir(poster), "poster.w320.jpg")
	// The colours differ only so the test can tell which file was decoded.
	writeAged(t, poster, string(solidPNG(t, testRed)), time.Hour)
	writePosterBytes(t, small, solidPNG(t, testBlue))
	if got, ok := svc.DominantColor("media-alpha"); !ok || got != "#0000ff" {
		t.Fatalf("DominantColor = %q, %v; want the current bucket's #0000ff", got, ok)
	}

	// A bucket older than its poster shows the previous picture.
	other := svc.PosterPath("media-archive")
	writeAged(t, filepath.Join(filepath.Dir(other), "poster.w320.jpg"), string(solidPNG(t, testBlue)), time.Hour)
	writePosterBytes(t, other, solidPNG(t, testRed))
	if got, ok := svc.DominantColor("media-archive"); !ok || got != "#ff0000" {
		t.Fatalf("DominantColor = %q, %v; want the poster's #ff0000", got, ok)
	}

	// An undecodable bucket falls back to the poster.
	third := svc.PosterPath("media-third")
	writeAged(t, third, string(solidPNG(t, testRed)), time.Hour)
	writePosterBytes(t, filepath.Join(filepath.Dir(third), "poster.w320.jpg"), []byte("not an image"))
	if got, ok := svc.DominantColor("media-third"); !ok || got != "#ff0000" {
		t.Fatalf("DominantColor = %q, %v; want the poster's #ff0000", got, ok)
	}
}

func TestDominantColorOmittedForUndecodablePoster(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	writePoster(t, svc, "media-alpha")
	if got, ok := svc.DominantColor("media-alpha"); ok {
		t.Fatalf("DominantColor = %q, want none", got)
	}
	// The failure is recorded against these poster bytes, so the next read
	// does not decode them again.
	record := dominantColorPath(svc.PosterPath("media-alpha"))
	before, err := os.Stat(record)
	if err != nil {
		t.Fatalf("no record of the undecodable poster: %v", err)
	}
	if got, ok := svc.DominantColor("media-alpha"); ok {
		t.Fatalf("second DominantColor = %q, want none", got)
	}
	after, err := os.Stat(record)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("the second read rewrote the record instead of using it")
	}
}

func TestDominantColorOmittedWithoutPoster(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	if got, ok := svc.DominantColor("media-alpha"); ok {
		t.Fatalf("DominantColor = %q, want none", got)
	}
	if got, ok := svc.EntityDominantColor("entity-maker"); ok {
		t.Fatalf("EntityDominantColor = %q, want none", got)
	}
}

func TestSweepRecordsDominantColorForExistingPoster(t *testing.T) {
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	svc := NewService(st, &fakeGrabber{}, t.TempDir())
	poster := svc.PosterPath("media-alpha")
	writePosterBytes(t, poster, solidPNG(t, testRed))

	now := time.Unix(100, 0).UTC()
	job, err := NewSelectedSweepJob([]store.MediaID{"media-alpha"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(SweepJobKind, SweepHandler(svc))
	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}

	data, err := os.ReadFile(dominantColorPath(poster)) // #nosec G304 -- test-owned file under t.TempDir().
	if err != nil {
		t.Fatalf("sweep recorded no dominant colour: %v", err)
	}
	if !bytes.Contains(data, []byte("#ff0000")) {
		t.Fatalf("recorded colour = %q, want #ff0000", data)
	}
}

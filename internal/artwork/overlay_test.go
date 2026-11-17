package artwork

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store/storetest"
)

// formatScaler stands in for the FFmpeg scaler's format rule: a destination
// ending in .png is a PNG that keeps alpha, anything else is a JPEG.
type formatScaler struct{ fakeGrabber }

func (*formatScaler) ScaleImage(_ context.Context, src string, _ int, dst string) error {
	data, err := os.ReadFile(src) // #nosec G304 -- test-owned source under t.TempDir().
	if err != nil {
		return err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if filepath.Ext(dst) == ".png" {
		err = png.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, nil)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, out.Bytes(), 0o600)
}

// transparentPNG is a PNG whose left half is fully transparent and whose right
// half is opaque red.
func transparentPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 8))
	for y := range 8 {
		for x := 8; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeFile(t *testing.T, path string) (image.Image, string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- slot file under this test's t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return img, format
}

var allBuckets = []string{"", BucketOriginal, "w320", "w400", "w640", "w1280", "w1920"}

func TestOverlaySlotsKeepAlphaAtEveryBucket(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &formatScaler{}, t.TempDir())
	for _, slot := range []string{SlotLogo, SlotClearart, SlotDisc} {
		if err := svc.ImportArtwork(ctx, "media-alpha", slot, transparentPNG(t)); err != nil {
			t.Fatalf("ImportArtwork(%q): %v", slot, err)
		}
		if served, ok := svc.ResolveSlot("media-alpha", slot); !ok || served != slot {
			t.Fatalf("ResolveSlot(%q) = (%q, %v)", slot, served, ok)
		}
		for _, bucket := range allBuckets {
			path, err := svc.ArtworkDerivative(ctx, "media-alpha", slot, bucket)
			if err != nil {
				t.Fatalf("ArtworkDerivative(%q, %q): %v", slot, bucket, err)
			}
			want := slot + ".png"
			if bucket != "" && bucket != BucketOriginal {
				want = slot + "." + bucket + ".png"
			}
			if filepath.Base(path) != want {
				t.Fatalf("ArtworkDerivative(%q, %q) = %q, want file %q", slot, bucket, path, want)
			}
			img, format := decodeFile(t, path)
			if format != "png" {
				t.Fatalf("%s/%s is %s, want png", slot, bucket, format)
			}
			if _, _, _, a := img.At(1, 1).RGBA(); a != 0 {
				t.Fatalf("%s/%s lost transparency: alpha %#x at a transparent source pixel", slot, bucket, a)
			}
			if _, _, _, a := img.At(14, 1).RGBA(); a != 0xffff {
				t.Fatalf("%s/%s opaque source pixel has alpha %#x", slot, bucket, a)
			}
		}
	}
}

func TestOpaqueSlotsStayJPEG(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &formatScaler{}, t.TempDir())
	for _, slot := range []string{SlotPoster, SlotBackdrop, SlotThumb, SlotBanner, SlotContainerPoster} {
		if err := svc.ImportArtwork(ctx, "media-alpha", slot, transparentPNG(t)); err != nil {
			t.Fatalf("ImportArtwork(%q): %v", slot, err)
		}
		for _, bucket := range allBuckets {
			path, err := svc.ArtworkDerivative(ctx, "media-alpha", slot, bucket)
			if err != nil {
				t.Fatalf("ArtworkDerivative(%q, %q): %v", slot, bucket, err)
			}
			if filepath.Ext(path) != ".jpg" {
				t.Fatalf("ArtworkDerivative(%q, %q) = %q, want a .jpg file", slot, bucket, path)
			}
			if _, format := decodeFile(t, path); format != "jpeg" {
				t.Fatalf("%s/%s is %s, want jpeg", slot, bucket, format)
			}
		}
	}
}

// writeLegacyOverlay plants an overlay slot the way it was stored before
// overlays became PNG: a JPEG at <slot>.jpg.
func writeLegacyOverlay(t *testing.T, dir, slot string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 8)), nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, slot+".jpg")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyJPEGOverlayIsServedUntilReimported(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &formatScaler{}, t.TempDir())
	dir := filepath.Dir(svc.PosterPath("media-alpha"))
	legacy := writeLegacyOverlay(t, dir, SlotLogo)

	if served, ok := svc.ResolveSlot("media-alpha", SlotLogo); !ok || served != SlotLogo {
		t.Fatalf("ResolveSlot(logo) with a legacy JPEG = (%q, %v), want (logo, true)", served, ok)
	}
	legacyVersion, ok := svc.ArtworkVersion("media-alpha", SlotLogo)
	if !ok {
		t.Fatal("ArtworkVersion(logo) absent for a legacy JPEG")
	}
	if path, err := svc.ArtworkDerivative(ctx, "media-alpha", SlotLogo, BucketOriginal); err != nil || path != legacy {
		t.Fatalf("ArtworkDerivative(logo, original) = %q, %v; want the legacy JPEG", path, err)
	}
	bucket, err := svc.ArtworkDerivative(ctx, "media-alpha", SlotLogo, "w320")
	if err != nil || bucket != filepath.Join(dir, "logo.w320.jpg") {
		t.Fatalf("ArtworkDerivative(logo, w320) = %q, %v; want logo.w320.jpg", bucket, err)
	}
	if _, format := decodeFile(t, bucket); format != "jpeg" {
		t.Fatalf("legacy bucket is %s, want jpeg", format)
	}

	if err := svc.ImportArtwork(ctx, "media-alpha", SlotLogo, transparentPNG(t)); err != nil {
		t.Fatalf("ImportArtwork(logo): %v", err)
	}
	for _, gone := range []string{legacy, bucket} {
		if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived the PNG import (stat err = %v)", filepath.Base(gone), err)
		}
	}
	path, err := svc.ArtworkDerivative(ctx, "media-alpha", SlotLogo, "w320")
	if err != nil || path != filepath.Join(dir, "logo.w320.png") {
		t.Fatalf("ArtworkDerivative(logo, w320) after import = %q, %v; want logo.w320.png", path, err)
	}
	if version, ok := svc.ArtworkVersion("media-alpha", SlotLogo); !ok || version == legacyVersion {
		t.Fatalf("ArtworkVersion(logo) after import = %q, %v; want a new version", version, ok)
	}
}

func TestLegacyJPEGEntityOverlayIsServed(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &formatScaler{}, t.TempDir())
	legacy := writeLegacyOverlay(t, filepath.Dir(svc.EntityPosterPath("entity-one")), SlotClearart)

	if served, ok := svc.ResolveEntitySlot("entity-one", SlotClearart); !ok || served != SlotClearart {
		t.Fatalf("ResolveEntitySlot(clearart) = (%q, %v), want (clearart, true)", served, ok)
	}
	if _, ok := svc.EntityArtworkVersion("entity-one", SlotClearart); !ok {
		t.Fatal("EntityArtworkVersion(clearart) absent for a legacy JPEG")
	}
	if path, err := svc.EntityArtworkDerivative(ctx, "entity-one", SlotClearart, ""); err != nil || path != legacy {
		t.Fatalf("EntityArtworkDerivative(clearart) = %q, %v; want the legacy JPEG", path, err)
	}
}

func TestLegacyJPEGOverlayLockOutlivesPNGImport(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &formatScaler{}, t.TempDir())
	legacy := writeLegacyOverlay(t, filepath.Dir(svc.PosterPath("media-alpha")), SlotLogo)
	if err := writeSlotLock(legacy, slotLock{Source: SourceLocal, Path: "/library/logo.png"}); err != nil {
		t.Fatal(err)
	}

	if !svc.IsArtworkLocked("media-alpha", SlotLogo) {
		t.Fatal("a legacy JPEG overlay lost its lock")
	}
	if err := svc.ImportArtwork(ctx, "media-alpha", SlotLogo, transparentPNG(t)); !errors.Is(err, ErrSlotLocked) {
		t.Fatalf("scrape import over a locked legacy overlay = %v, want ErrSlotLocked", err)
	}
	// A manual pick replaces the art and keeps the slot locked.
	if err := svc.ImportManualArtwork(ctx, "media-alpha", SlotLogo, transparentPNG(t)); err != nil {
		t.Fatalf("ImportManualArtwork(logo): %v", err)
	}
	if _, format := decodeFile(t, svc.ArtworkPath("media-alpha", SlotLogo)); format != "png" {
		t.Fatalf("manual pick stored as %s, want png", format)
	}
	if !svc.IsArtworkLocked("media-alpha", SlotLogo) {
		t.Fatal("the manual pick dropped the legacy lock")
	}
	if _, err := os.Stat(lockMarkerPath(legacy)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy lock marker survived the PNG import (stat err = %v)", err)
	}
	if err := svc.ReleaseLocalArtwork(ctx, "media-alpha", SlotLogo, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if svc.IsArtworkLocked("media-alpha", SlotLogo) {
		t.Fatal("release left the slot locked")
	}
}

func TestReleaseLocalArtworkUnlocksLegacyJPEGOverlay(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &formatScaler{}, t.TempDir())
	legacy := writeLegacyOverlay(t, filepath.Dir(svc.PosterPath("media-alpha")), SlotDisc)
	if err := writeSlotLock(legacy, slotLock{Source: SourceLocal, Path: "/library/disc.png"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReleaseLocalArtwork(ctx, "media-alpha", SlotDisc, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if svc.IsArtworkLocked("media-alpha", SlotDisc) {
		t.Fatal("release left a legacy JPEG overlay locked")
	}
	if _, ok := svc.ResolveSlot("media-alpha", SlotDisc); !ok {
		t.Fatal("release removed the legacy overlay art")
	}
}

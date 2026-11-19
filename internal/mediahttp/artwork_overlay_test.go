package mediahttp

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// formatArtworkScaler stands in for the FFmpeg scaler's format rule: a
// destination ending in .png is a PNG that keeps alpha, anything else a JPEG.
type formatArtworkScaler struct{ fakeArtworkScaler }

func (formatArtworkScaler) ScaleImage(_ context.Context, src string, _ int, dst string) error {
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

func newOverlayArtworkTestHandler(t *testing.T) (*Handler, *artwork.Service) {
	t.Helper()
	st := storetest.Open(t)
	tokens := streamtoken.NewService([]byte("test-token-key"), time.Now)
	svc := artwork.NewService(st, formatArtworkScaler{}, t.TempDir())
	return NewHandler(st, auth.NewService(st), tokens, svc, sessiontest.New(t, nil, t.TempDir(), nil)), svc
}

// transparentTestPNG is a PNG whose left half is fully transparent and whose
// right half is opaque red.
func transparentTestPNG(t *testing.T) []byte {
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

var artworkBucketQueries = []string{"", "?bucket=original", "?bucket=w320", "?bucket=w400", "?bucket=w640", "?bucket=w1280", "?bucket=w1920"}

func TestArtworkServesOverlaySlotsAsPNGWithAlphaAtEveryBucket(t *testing.T) {
	handler, svc := newOverlayArtworkTestHandler(t)
	for _, slot := range []string{artwork.SlotLogo, artwork.SlotClearart, artwork.SlotDisc} {
		if err := svc.ImportArtwork(context.Background(), "media-alpha", slot, transparentTestPNG(t)); err != nil {
			t.Fatalf("ImportArtwork(%q): %v", slot, err)
		}
		for _, query := range artworkBucketQueries {
			rec := httptest.NewRecorder()
			handler.Artwork(rec, artworkRequest("media-alpha", slot+query))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s%s: status = %d: %s", slot, query, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "image/png" {
				t.Fatalf("%s%s: Content-Type = %q, want image/png", slot, query, got)
			}
			img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
			if err != nil {
				t.Fatalf("%s%s: body is not a PNG: %v", slot, query, err)
			}
			if _, _, _, a := img.At(1, 1).RGBA(); a != 0 {
				t.Fatalf("%s%s: transparent source pixel served with alpha %#x", slot, query, a)
			}
			if _, _, _, a := img.At(14, 1).RGBA(); a != 0xffff {
				t.Fatalf("%s%s: opaque source pixel served with alpha %#x", slot, query, a)
			}
		}
	}
}

func TestArtworkServesOpaqueSlotsAsUnchangedJPEG(t *testing.T) {
	handler, svc := newOverlayArtworkTestHandler(t)
	ctx := context.Background()
	for _, slot := range []string{artwork.SlotPoster, artwork.SlotBackdrop, artwork.SlotThumb, artwork.SlotBanner, artwork.SlotContainerPoster} {
		if err := svc.ImportArtwork(ctx, "media-alpha", slot, transparentTestPNG(t)); err != nil {
			t.Fatalf("ImportArtwork(%q): %v", slot, err)
		}
		// The stored layout is the one every earlier release wrote.
		stored := filepath.Join(filepath.Dir(svc.PosterPath("media-alpha")), slot+".jpg")
		want, err := os.ReadFile(stored) // #nosec G304 -- slot file under this test's t.TempDir().
		if err != nil {
			t.Fatalf("%s is not stored as %s.jpg: %v", slot, slot, err)
		}
		rec := httptest.NewRecorder()
		handler.Artwork(rec, artworkRequest("media-alpha", slot))
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
			t.Fatalf("%s: status %d, body differs from the stored JPEG", slot, rec.Code)
		}
		for _, query := range artworkBucketQueries {
			rec := httptest.NewRecorder()
			handler.Artwork(rec, artworkRequest("media-alpha", slot+query))
			if got := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || got != "image/jpeg" {
				t.Fatalf("%s%s: status %d Content-Type %q, want 200 image/jpeg", slot, query, rec.Code, got)
			}
			if _, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
				t.Fatalf("%s%s: body is not a JPEG: %v", slot, query, err)
			}
		}
		bucket, err := os.ReadFile(filepath.Join(filepath.Dir(stored), slot+".w640.jpg")) // #nosec G304 -- as above.
		if err != nil {
			t.Fatalf("%s bucket is not stored as %s.w640.jpg: %v", slot, slot, err)
		}
		rec = httptest.NewRecorder()
		handler.Artwork(rec, artworkRequest("media-alpha", slot+"?bucket=w640"))
		if !bytes.Equal(rec.Body.Bytes(), bucket) {
			t.Fatalf("%s w640: body differs from the stored bucket", slot)
		}
	}
}

func TestArtworkServesLegacyJPEGOverlay(t *testing.T) {
	handler, svc := newOverlayArtworkTestHandler(t)
	var legacy bytes.Buffer
	if err := jpeg.Encode(&legacy, image.NewRGBA(image.Rect(0, 0, 16, 8)), nil); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{artwork.SlotLogo, artwork.SlotClearart, artwork.SlotDisc} {
		// Stored before overlay slots became PNG: a JPEG at <slot>.jpg.
		path := filepath.Join(filepath.Dir(svc.PosterPath("media-alpha")), slot+".jpg")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, legacy.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		handler.Artwork(rec, artworkRequest("media-alpha", slot))
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), legacy.Bytes()) {
			t.Fatalf("%s: status %d, want the legacy JPEG bytes", slot, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
			t.Fatalf("%s: Content-Type = %q, want image/jpeg", slot, got)
		}
		rec = httptest.NewRecorder()
		handler.Artwork(rec, artworkRequest("media-alpha", slot+"?bucket=w320"))
		if got := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || got != "image/jpeg" {
			t.Fatalf("%s bucket: status %d Content-Type %q, want 200 image/jpeg", slot, rec.Code, got)
		}
		if _, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
			t.Fatalf("%s bucket: body is not a JPEG: %v", slot, err)
		}
	}
}

func TestEntityArtworkServesOverlaySlotAsPNG(t *testing.T) {
	handler, svc := newOverlayArtworkTestHandler(t)
	upsertTestEntity(t, handler, "library-local", "entity-maker")
	path := svc.EntityArtworkPath("entity-maker", artwork.SlotLogo)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, transparentTestPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "?bucket=w320"} {
		rec := httptest.NewRecorder()
		handler.EntityArtwork(rec, entityArtworkRequest("entity-maker", artwork.SlotLogo+query))
		if got := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || got != "image/png" {
			t.Fatalf("entity logo%s: status %d Content-Type %q, want 200 image/png", query, rec.Code, got)
		}
		img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatalf("entity logo%s: body is not a PNG: %v", query, err)
		}
		if _, _, _, a := img.At(1, 1).RGBA(); a != 0 {
			t.Fatalf("entity logo%s: transparent source pixel served with alpha %#x", query, a)
		}
	}
}

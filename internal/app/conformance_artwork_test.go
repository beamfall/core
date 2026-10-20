package app

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/store"
)

// TestConformanceArtworkServesVendoredBytesDeterministically locks the two
// load-bearing facts that make the UI-catalog's conformance artwork capture
// byte-stable run-to-run, but which only the heavy Playwright `make ui-catalog`
// exercises end-to-end (so a regression in either slips through `make gate`):
//
//  1. The conformance seed lands the embedded internal/store/seedart/*.png at
//     <dataDir>/artwork/<id>/<slot>.jpg with its mtime pinned to
//     seedArtworkModTime (Unix 0). A regression that stops pinning the mtime
//     would let the live write time leak into derivative-freshness decisions
//     and the catalog's cache key.
//  2. Conformance mode disables the artwork scaler (app.go: conformanceMode →
//     nil grabber → nil scaler), so every derivative bucket honestly falls back
//     to the vendored full-size bytes instead of an FFmpeg re-encode (which is
//     machine-dependent JPEG and would break byte-stable capture).
//
// The test boots a conformance-seeded store and an artwork.Service with a nil
// grabber — exactly the wiring app.go produces in conformance mode — without
// the cost of a full app.New / HTTP listener.
func TestConformanceArtworkServesVendoredBytesDeterministically(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, store.Options{
		SeedProfile: store.SeedProfileConformance,
		Logger:      discardLogger(),
	})
	if err != nil {
		t.Fatalf("open conformance store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// A nil grabber is the conformance-mode wiring: NewService finds no
	// ImageScaler on it, so the derivative path falls back to the original.
	svc := artwork.NewService(st, nil, dir)

	// The vendored source bytes the seed embeds and lands on disk. Read from the
	// repo source (the embed is private to package store) — this is the exact
	// byte sequence every conformance artwork URL must serve.
	srcPath := filepath.Join("..", "store", "seedart", "media-alpha.poster.png")
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read seedart source: %v", err)
	}
	srcSum := sha256.Sum256(srcBytes)

	// (a) The version token is deterministic and derived from the vendored
	// bytes, so it is stable run-to-run. (The tests.md note predates the
	// mtime→content-hash switch in artwork.go: ArtworkVersion is now the content
	// hash of the bytes, not the file mtime, so the live token is the value
	// asserted below rather than "0". The determinism contract is unchanged: a
	// fixed input yields a fixed token. The mtime-pinning fact is asserted
	// separately below, since it still gates derivative freshness.)
	version, ok := svc.ArtworkVersion("media-alpha", artwork.SlotPoster)
	if !ok {
		t.Fatal(`ArtworkVersion("media-alpha","poster"): no artwork found`)
	}
	if want := "1x59wkl0vk76c"; version != want {
		t.Fatalf("ArtworkVersion = %q, want %q (deterministic content hash of vendored poster bytes)", version, want)
	}

	// The mtime of the seeded file is pinned to Unix 0, not the live write time.
	posterPath := filepath.Join(dir, "artwork", "media-alpha", "poster.jpg")
	info, err := os.Stat(posterPath)
	if err != nil {
		t.Fatalf("stat seeded poster: %v", err)
	}
	if got := info.ModTime().UTC(); !got.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("seeded poster mtime = %v, want Unix 0 (pinned)", got)
	}

	// (b) Every derivative bucket serves the vendored bytes byte-identically
	// (sha256 equal): with no scaler, w320 and w1280 both resolve to the
	// full-size poster.jpg rather than a re-encoded derivative.
	for _, bucket := range []string{"w320", "w1280"} {
		served, err := svc.PosterDerivative(context.Background(), "media-alpha", bucket)
		if err != nil {
			t.Fatalf("PosterDerivative(%s): %v", bucket, err)
		}
		servedBytes, err := os.ReadFile(served)
		if err != nil {
			t.Fatalf("read served bucket %s: %v", bucket, err)
		}
		if got := sha256.Sum256(servedBytes); got != srcSum {
			t.Fatalf("bucket %s served bytes differ from vendored source (sha256 %x != %x); scaler re-enabled?", bucket, got, srcSum)
		}
	}
}

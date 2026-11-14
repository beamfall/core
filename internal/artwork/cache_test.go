package artwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// countHashes routes the service's version hashing through a counter.
func countHashes(svc *Service) *atomic.Int64 {
	var n atomic.Int64
	svc.versions.hash = func(path string) (string, error) {
		n.Add(1)
		return contentVersion(path)
	}
	return &n
}

// writeAged writes body to path and backdates its mtime past the racy window,
// so the version cache may memoise it.
func writeAged(t *testing.T, path, body string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func mustContentVersion(t *testing.T, path string) string {
	t.Helper()
	token, err := contentVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestArtworkVersionHashesUnchangedFileOnce(t *testing.T) {
	svc := NewService(storetest.Open(t), nil, t.TempDir())
	hashes := countHashes(svc)
	poster := svc.PosterPath("media-alpha")
	writeAged(t, poster, "poster bytes", time.Hour)

	want := mustContentVersion(t, poster)
	for i := 0; i < 5; i++ {
		got, ok := svc.ArtworkVersion("media-alpha", SlotPoster)
		if !ok || got != want {
			t.Fatalf("ArtworkVersion = (%q, %v), want (%q, true)", got, ok, want)
		}
		if !svc.HasPoster("media-alpha") {
			t.Fatal("HasPoster = false, want true")
		}
	}
	if got := hashes.Load(); got != 1 {
		t.Fatalf("hashes = %d, want 1 for an unchanged file", got)
	}

	// New bytes change the stat, so the token is recomputed — and stays a
	// pure content hash.
	writeAged(t, poster, "replacement poster bytes", 30*time.Minute)
	changed, ok := svc.ArtworkVersion("media-alpha", SlotPoster)
	if !ok || changed != mustContentVersion(t, poster) || changed == want {
		t.Fatalf("ArtworkVersion after change = (%q, %v), want fresh content token", changed, ok)
	}
	if got := hashes.Load(); got != 2 {
		t.Fatalf("hashes = %d, want 2 after one content change", got)
	}

	// A removed file reports absent and drops its entry.
	if err := os.Remove(poster); err != nil {
		t.Fatal(err)
	}
	if v, ok := svc.ArtworkVersion("media-alpha", SlotPoster); ok {
		t.Fatalf("ArtworkVersion after removal = (%q, true), want absent", v)
	}
	if len(svc.versions.entries) != 0 {
		t.Fatalf("cache entries after removal = %d, want 0", len(svc.versions.entries))
	}
}

func TestArtworkVersionRehashesAfterReimport(t *testing.T) {
	svc := NewService(storetest.Open(t), nil, t.TempDir())
	hashes := countHashes(svc)
	poster := svc.PosterPath("media-alpha")
	writeAged(t, poster, "old poster", time.Hour)
	first, _ := svc.ArtworkVersion("media-alpha", SlotPoster)

	// Same length as the old bytes: the import itself must invalidate.
	if err := svc.ImportArtwork(context.Background(), "media-alpha", SlotPoster, []byte("new poster")); err != nil {
		t.Fatal(err)
	}
	if _, cached := svc.versions.entries[poster]; cached {
		t.Fatal("import left the stale version entry cached")
	}
	got, ok := svc.ArtworkVersion("media-alpha", SlotPoster)
	if !ok || got == first || got != mustContentVersion(t, poster) {
		t.Fatalf("ArtworkVersion after re-import = (%q, %v), want the new content token (old %q)", got, ok, first)
	}
	if hashes.Load() != 2 {
		t.Fatalf("hashes = %d, want 2", hashes.Load())
	}
}

func TestArtworkVersionDoesNotMemoiseRacilyFreshFiles(t *testing.T) {
	svc := NewService(storetest.Open(t), nil, t.TempDir())
	hashes := countHashes(svc)
	poster := svc.PosterPath("media-alpha")
	writeAged(t, poster, "fresh bytes", 0)

	for i := 0; i < 3; i++ {
		if _, ok := svc.ArtworkVersion("media-alpha", SlotPoster); !ok {
			t.Fatal("ArtworkVersion = absent, want present")
		}
	}
	if got := hashes.Load(); got != 3 {
		t.Fatalf("hashes = %d, want 3: a file inside the racy window is re-verified", got)
	}
}

func TestArtworkPurgeAndRemoveForgetVersions(t *testing.T) {
	svc := NewService(storetest.Open(t), nil, t.TempDir())
	writeAged(t, svc.ArtworkPath("media-alpha", SlotPoster), "a", time.Hour)
	writeAged(t, svc.ArtworkPath("media-alpha", SlotBackdrop), "b", time.Hour)
	writeAged(t, svc.ArtworkPath("media-alphabet", SlotPoster), "c", time.Hour)
	writeAged(t, svc.EntityPosterPath("entity-one"), "d", time.Hour)
	for _, slot := range []string{SlotPoster, SlotBackdrop} {
		svc.ArtworkVersion("media-alpha", slot)
	}
	svc.ArtworkVersion("media-alphabet", SlotPoster)
	svc.EntityArtworkVersion("entity-one", SlotPoster)

	if err := svc.PurgeMediaArtwork("media-alpha"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveEntityArtwork("entity-one"); err != nil {
		t.Fatal(err)
	}
	if len(svc.versions.entries) != 1 {
		t.Fatalf("cache entries = %d, want only the sibling media-alphabet entry", len(svc.versions.entries))
	}
	if _, ok := svc.versions.entries[svc.ArtworkPath("media-alphabet", SlotPoster)]; !ok {
		t.Fatal("purge of media-alpha dropped the media-alphabet entry (prefix must stop at the directory)")
	}
}

func TestVersionCacheIsBounded(t *testing.T) {
	var c versionCache
	for i := 0; i < versionCacheCap+10; i++ {
		c.store(&versionEntry{path: "/art/" + strconv.Itoa(i), token: "t"})
	}
	if len(c.entries) != versionCacheCap || c.lru.Len() != versionCacheCap {
		t.Fatalf("entries = %d / lru = %d, want %d", len(c.entries), c.lru.Len(), versionCacheCap)
	}
	if _, ok := c.entries["/art/0"]; ok {
		t.Fatal("least recently used entry survived eviction")
	}
}

func TestArtworkVersionConcurrentUse(t *testing.T) {
	svc := NewService(storetest.Open(t), nil, t.TempDir())
	ids := []string{"media-a", "media-b", "media-c"}
	valid := map[string]bool{}
	for _, id := range ids {
		for _, body := range []string{"one " + id, "two " + id} {
			path := filepath.Join(t.TempDir(), "probe")
			writeAged(t, path, body, 0)
			valid[mustContentVersion(t, path)] = true
		}
		writeAged(t, svc.ArtworkPath(store.MediaID(id), SlotPoster), "one "+id, time.Hour)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := ids[(g+i)%len(ids)]
				if token, ok := svc.ArtworkVersion(store.MediaID(id), SlotPoster); ok && !valid[token] {
					errs <- fmt.Errorf("token %q for %s matches no written content", token, id)
					return
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			id := ids[i%len(ids)]
			body := "one " + id
			if i%2 == 1 {
				body = "two " + id
			}
			if err := svc.ImportArtwork(context.Background(), store.MediaID(id), SlotPoster, []byte(body)); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// scalerFunc adapts a function to FrameGrabber + ImageScaler.
type scalerFunc func(ctx context.Context, src string, width int, dst string) error

func (scalerFunc) FrameGrab(context.Context, string, float64, int, string) error { return nil }
func (f scalerFunc) ScaleImage(ctx context.Context, src string, width int, dst string) error {
	return f(ctx, src, width, dst)
}

func TestDerivativeAttemptsFailingOriginalOnce(t *testing.T) {
	scaler := &fakeScaler{scaleErr: errors.New("corrupt original")}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	poster := svc.PosterPath("media-alpha")
	writeAged(t, poster, "not a jpeg", time.Hour)

	for _, bucket := range []string{"w320", "w320", "w640"} {
		path, err := svc.PosterDerivative(context.Background(), "media-alpha", bucket)
		if err != nil || path != poster {
			t.Fatalf("PosterDerivative(%s) = %q, %v; want full-size fallback", bucket, path, err)
		}
	}
	if len(scaler.scaleCalls) != 1 {
		t.Fatalf("scaleCalls = %v, want one attempt for an unchanged failing original", scaler.scaleCalls)
	}

	// A replaced original is a new file version and is attempted again.
	writeAged(t, poster, "fixed jpeg", 30*time.Minute)
	scaler.scaleErr = nil
	path, err := svc.PosterDerivative(context.Background(), "media-alpha", "w320")
	if err != nil || path == poster {
		t.Fatalf("PosterDerivative after replacement = %q, %v; want the derivative", path, err)
	}
	if len(scaler.scaleCalls) != 2 {
		t.Fatalf("scaleCalls = %v, want a retry after the original changed", scaler.scaleCalls)
	}
}

func TestDerivativeDoesNotRememberCancelledAttempts(t *testing.T) {
	var calls atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(storetest.Open(t), scalerFunc(func(ctx context.Context, _ string, _ int, dst string) error {
		if calls.Add(1) == 1 {
			cancel()
			return ctx.Err()
		}
		return os.WriteFile(dst, []byte("scaled"), 0o600)
	}), t.TempDir())
	writeAged(t, svc.PosterPath("media-alpha"), "full jpeg", time.Hour)

	if _, err := svc.PosterDerivative(ctx, "media-alpha", "w320"); err != nil {
		t.Fatal(err)
	}
	path, err := svc.PosterDerivative(context.Background(), "media-alpha", "w320")
	if err != nil || path == svc.PosterPath("media-alpha") {
		t.Fatalf("PosterDerivative after a cancelled attempt = %q, %v; want the derivative", path, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("scale calls = %d, want 2", calls.Load())
	}
}

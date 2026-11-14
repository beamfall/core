package mediahttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// countingArtworkScaler counts ScaleImage calls, the scaler spawn a derivative
// costs in production.
type countingArtworkScaler struct {
	scales atomic.Int64
}

func (*countingArtworkScaler) FrameGrab(context.Context, string, float64, int, string) error {
	return nil
}

func (s *countingArtworkScaler) ScaleImage(_ context.Context, _ string, _ int, dst string) error {
	s.scales.Add(1)
	return os.WriteFile(dst, []byte("scaled jpeg bytes"), 0o600)
}

func newCountingArtworkHandler(t *testing.T) (*Handler, *artwork.Service, *countingArtworkScaler) {
	t.Helper()
	st := storetest.Open(t)
	scaler := &countingArtworkScaler{}
	svc := artwork.NewService(st, scaler, t.TempDir())
	tokens := streamtoken.NewService([]byte("test-token-key"), time.Now)
	return NewHandler(st, auth.NewService(st), tokens, svc, playback.NewSessions(nil, t.TempDir(), nil)), svc, scaler
}

func TestArtworkRevalidationSkipsDerivativeWork(t *testing.T) {
	handler, svc, scaler := newCountingArtworkHandler(t)
	writeTestPoster(t, svc, "media-alpha")
	version, ok := svc.ArtworkVersion("media-alpha", artwork.SlotPoster)
	if !ok {
		t.Fatal("ArtworkVersion = absent, want poster version")
	}

	cond := artworkRequest("media-alpha", "poster?bucket=w320&v="+version)
	cond.Header.Set("If-None-Match", `"`+version+`"`)
	rec := httptest.NewRecorder()
	handler.Artwork(rec, cond)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", rec.Code)
	}
	if n := scaler.scales.Load(); n != 0 {
		t.Fatalf("scaler calls = %d, want 0 for a revalidation", n)
	}

	// A full request still produces the derivative.
	rec = httptest.NewRecorder()
	handler.Artwork(rec, artworkRequest("media-alpha", "poster?bucket=w320&v="+version))
	if rec.Code != http.StatusOK || scaler.scales.Load() != 1 {
		t.Fatalf("full request = %d with %d scaler calls, want 200 with 1", rec.Code, scaler.scales.Load())
	}
}

func TestEntityArtworkRevalidationSkipsDerivativeWork(t *testing.T) {
	handler, svc, scaler := newCountingArtworkHandler(t)
	upsertTestEntity(t, handler, "library-local", "entity-maker")
	writeTestEntityPoster(t, svc, "entity-maker")
	version, ok := svc.EntityArtworkVersion("entity-maker", artwork.SlotPoster)
	if !ok {
		t.Fatal("EntityArtworkVersion = absent, want poster version")
	}

	cond := entityArtworkRequest("entity-maker", "poster?bucket=w320&v="+version)
	cond.Header.Set("If-None-Match", `"`+version+`"`)
	rec := httptest.NewRecorder()
	handler.EntityArtwork(rec, cond)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", rec.Code)
	}
	if n := scaler.scales.Load(); n != 0 {
		t.Fatalf("scaler calls = %d, want 0 for a revalidation", n)
	}
}

// failingResponseWriter accepts limit bytes and then fails every write, as a
// connection to a client that went away does. Its ReadFrom records the source
// net/http would hand to sendfile.
type failingResponseWriter struct {
	header  http.Header
	limit   int
	written int
	source  io.Reader
}

var errClientGone = errors.New("client gone")

func (w *failingResponseWriter) Header() http.Header { return w.header }
func (w *failingResponseWriter) WriteHeader(int)     {}

func (w *failingResponseWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.written; len(p) > room {
		w.written += max(room, 0)
		return max(room, 0), errClientGone
	}
	w.written += len(p)
	return len(p), nil
}

func (w *failingResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	w.source = r
	return io.Copy(struct{ io.Writer }{w}, r)
}

func TestDirectPlainPathHandsTheFileToSendfileAndStopsOnFailedWrite(t *testing.T) {
	const size = 8 << 20
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", bytes.Repeat([]byte{'x'}, size))
	w := &failingResponseWriter{header: http.Header{}, limit: 64 << 10}

	handler.Direct(w, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))

	limited, ok := w.source.(*io.LimitedReader)
	if !ok {
		t.Fatalf("copy source = %T, want *io.LimitedReader over the file", w.source)
	}
	if _, ok := limited.R.(*os.File); !ok {
		t.Fatalf("copy source wraps %T, want *os.File so net/http can use sendfile", limited.R)
	}
	if w.written != w.limit {
		t.Fatalf("written = %d, want %d", w.written, w.limit)
	}
	// ServeContent returned after the first failed write rather than reading
	// the rest of the file.
	if remaining := limited.N; remaining < size-int64(w.limit)-(1<<20) {
		t.Fatalf("copy read %d bytes past a failed write, want it to stop", size-remaining)
	}
}

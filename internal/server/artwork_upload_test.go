package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// newArtworkUploadTestServer builds the same dev-seeded server
// newTestServerWithStore builds, but additionally wires the graph.Handler's
// ArtworkCandidateImporter to the same *artwork.Service instance the media
// HTTP handler serves from — exactly how internal/app/app.go wires production
// (graphHandler.SetArtworkCandidateServices(scrapeEngine, artworkService)) —
// so an uploaded slot is both importable and servable in one test server.
func newArtworkUploadTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st := storetest.Open(t)
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	if err := st.SetSessionAdminUntil(context.Background(), "session-manager", clock().Add(time.Hour)); err != nil {
		t.Fatalf("SetSessionAdminUntil: %v", err)
	}
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, nil)
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	tokens.SetRecorder(st)
	artworkService := artwork.NewService(st, nil, t.TempDir())
	graphHandler := graph.NewHandler(manifests, authService, st, nil)
	graphHandler.SetArtworkCandidateServices(nil, artworkService)
	srv := New(
		manifests,
		playback.NewService(st, tokens, clock),
		beam.NewService(st, authService, clock),
		pairing.NewService(st, clock),
		graphHandler,
		mediahttp.NewHandler(st, authService, tokens, artworkService, playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		st,
	)
	srv.SetEvents(st)
	srv.SetV1Store(st)
	srv.SetV1ScanEnqueuer(testV1ScanEnqueuer(st))
	return srv, st
}

// pngArtwork returns deterministic, fully encoded PNG bytes. A header-only
// fixture would exercise DecodeConfig without proving the upload can be fully
// decoded and served.
func pngArtwork(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.SetNRGBA(0, 0, color.NRGBA{R: 17, G: 29, B: 11, A: 255})
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return body.Bytes()
}

func gifArtwork(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	img.SetColorIndex(0, 0, 1)
	var body bytes.Buffer
	if err := gif.Encode(&body, img, nil); err != nil {
		t.Fatalf("encode GIF: %v", err)
	}
	return body.Bytes()
}

func createArtworkUploadMedia(t *testing.T, st *store.Store, title string) store.MediaID {
	t.Helper()
	mediaID, err := st.CreateManualMedia(context.Background(), store.ManualMedia{
		LibraryID: "library-local",
		Title:     title,
	})
	if err != nil {
		t.Fatalf("CreateManualMedia: %v", err)
	}
	return mediaID
}

func assertOneAppliedArtworkCandidate(t *testing.T, st *store.Store, mediaID store.MediaID, slot string) {
	t.Helper()
	candidates, err := st.ArtworkCandidates(
		context.Background(),
		mediaID,
		slot,
		store.LibraryFilter{AllLibraries: true},
	)
	if err != nil {
		t.Fatalf("ArtworkCandidates: %v", err)
	}
	if len(candidates) != 1 || !candidates[0].Applied {
		t.Fatalf("candidates = %#v, want exactly one applied candidate", candidates)
	}
}

func artworkMultipartBody(t *testing.T, mediaID, slot, filename string, fileBody []byte, contentType string) ([]byte, string) {
	t.Helper()
	return backupMultipartBody(t, map[string]string{"mediaID": mediaID, "slot": slot}, filename, fileBody, contentType)
}

func uploadArtworkRequest(t *testing.T, mediaID, slot, filename string, fileBody []byte, contentType string) *http.Request {
	t.Helper()
	body, ct := artworkMultipartBody(t, mediaID, slot, filename, fileBody, contentType)
	// The literal path (not the artworkUploadPattern constant) is required
	// here: TestEveryCommandRouteHasBehaviorTest (internal/conformance) detects
	// route coverage by scanning for each route's pattern as a string literal.
	req := httptest.NewRequest(http.MethodPost, "/api/media/artwork/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	return req
}

// TestUploadArtworkStoresAndServesTheSlot is the ticket's core e2e claim: a
// multipart upload during create-Media lands through to the slot a normal GET
// artwork request serves — not just a 200 from the upload route.
func TestUploadArtworkStoresAndServesTheSlot(t *testing.T) {
	srv, st := newArtworkUploadTestServer(t)
	mediaID := createArtworkUploadMedia(t, st, "Artwork Upload Success")
	png := pngArtwork(t, 32, 24)
	req := uploadArtworkRequest(t, string(mediaID), artwork.SlotPoster, "poster.png", png, "image/png")
	authorizeManager(req)
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"uploaded":true`)) {
		t.Fatalf("upload body = %s, want uploaded:true", rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/media/artwork/"+string(mediaID)+"/poster", nil)
	authorizeManager(getReq)
	getRec := httptest.NewRecorder()
	srv.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET artwork status = %d, want 200: %s", getRec.Code, getRec.Body.String())
	}
	if !bytes.Equal(getRec.Body.Bytes(), png) {
		t.Fatalf("served artwork = %d bytes, want the %d uploaded bytes verbatim", getRec.Body.Len(), len(png))
	}
	assertOneAppliedArtworkCandidate(t, st, mediaID, artwork.SlotPoster)
}

// TestUploadArtworkRequiresCSRFHeader mirrors
// TestCookieGraphQLMutationRequiresCSRFHeader for the multipart route: the
// generic ContentJSON POST wrapper is deliberately bypassed for this route
// (it isn't JSON), so uploadArtwork's own CSRF check is the only gate.
func TestUploadArtworkRequiresCSRFHeader(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	req := uploadArtworkRequest(t, "media-alpha", artwork.SlotPoster, "poster.png", pngArtwork(t, 8, 8), "image/png")
	authorizeManager(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 without a CSRF header", rec.Code)
	}
}

// TestUploadArtworkRequiresManagerRole matches pinArtworkCandidate's
// roleSession|roleManager gate: a plain viewer session is rejected the same
// way moderationScope rejects a non-manager caller.
func TestUploadArtworkRequiresManagerRole(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	req := uploadArtworkRequest(t, "media-alpha", artwork.SlotPoster, "poster.png", pngArtwork(t, 8, 8), "image/png")
	authorizeViewer(req)
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-manager session", rec.Code)
	}
}

// TestUploadArtworkRejectsNonImagePayload is the ticket's fail-closed
// non-image acceptance criterion: bytes that don't sniff as a real image
// (image.DecodeConfig) are rejected before anything reaches storage.
func TestUploadArtworkRejectsNonImagePayload(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	req := uploadArtworkRequest(t, "media-alpha", artwork.SlotPoster, "poster.txt", []byte("not an image"), "text/plain")
	authorizeManager(req)
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-image payload: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadArtworkRejectsIncompletePNGAndRegisteredGIF(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	encoded := pngArtwork(t, 8, 8)
	tests := []struct {
		name        string
		filename    string
		contentType string
		body        []byte
	}{
		{
			name:        "header only",
			filename:    "header.png",
			contentType: "image/png",
			body:        hugePNGHeader(t, 8, 8),
		},
		{
			name:        "truncated",
			filename:    "truncated.png",
			contentType: "image/png",
			body:        encoded[:len(encoded)/2],
		},
		{
			name:        "registered GIF",
			filename:    "poster.gif",
			contentType: "image/gif",
			body:        gifArtwork(t),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := uploadArtworkRequest(t, "media-alpha", artwork.SlotPoster, tt.filename, tt.body, tt.contentType)
			authorizeManager(req)
			req.Header.Set(auth.CSRFHeader, "1")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUploadArtworkIdenticalRequestRetryKeepsOneCandidate(t *testing.T) {
	srv, st := newArtworkUploadTestServer(t)
	mediaID := createArtworkUploadMedia(t, st, "Artwork Upload Identical Retry")
	body, contentType := artworkMultipartBody(
		t,
		string(mediaID),
		artwork.SlotPoster,
		"poster.png",
		pngArtwork(t, 12, 10),
		"image/png",
	)
	for attempt := 1; attempt <= 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/media/artwork/upload", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		authorizeManager(req)
		req.Header.Set(auth.CSRFHeader, "1")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200: %s", attempt, rec.Code, rec.Body.String())
		}
	}
	assertOneAppliedArtworkCandidate(t, st, mediaID, artwork.SlotPoster)
}

func TestUploadArtworkLostResponseRetryKeepsOneCandidate(t *testing.T) {
	srv, st := newArtworkUploadTestServer(t)
	mediaID := createArtworkUploadMedia(t, st, "Artwork Upload Lost Response")
	png := pngArtwork(t, 14, 11)

	firstReq := uploadArtworkRequest(t, string(mediaID), artwork.SlotPoster, "poster.png", png, "image/png")
	authorizeManager(firstReq)
	firstReq.Header.Set(auth.CSRFHeader, "1")
	discarded := httptest.NewRecorder()
	srv.ServeHTTP(discarded, firstReq)
	if discarded.Code != http.StatusOK {
		t.Fatalf("discarded response status = %d, want server-side success: %s", discarded.Code, discarded.Body.String())
	}

	retryReq := uploadArtworkRequest(t, string(mediaID), artwork.SlotPoster, "poster.png", png, "image/png")
	authorizeManager(retryReq)
	retryReq.Header.Set(auth.CSRFHeader, "1")
	retryRec := httptest.NewRecorder()
	srv.ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200: %s", retryRec.Code, retryRec.Body.String())
	}
	assertOneAppliedArtworkCandidate(t, st, mediaID, artwork.SlotPoster)
}

// TestUploadArtworkRejectsOversizedBody is the ticket's fail-closed oversized
// acceptance criterion: a body past maxArtworkSlotUploadBody is rejected by
// http.MaxBytesReader before ParseMultipartForm decodes it.
func TestUploadArtworkRejectsOversizedBody(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	oversized := bytes.Repeat([]byte("x"), maxArtworkSlotUploadBody+1)
	req := uploadArtworkRequest(t, "media-alpha", artwork.SlotPoster, "poster.png", oversized, "image/png")
	authorizeManager(req)
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 for an oversized body: %s", rec.Code, rec.Body.String())
	}
}

// TestUploadArtworkRejectsUnknownSlot covers the mediaID/slot validation gate
// shared with graph.Handler.PrepareUploadedArtwork (artwork.ValidSlot).
func TestUploadArtworkRejectsUnknownSlot(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	req := uploadArtworkRequest(t, "media-alpha", "not-a-real-slot", "poster.png", pngArtwork(t, 8, 8), "image/png")
	authorizeManager(req)
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown slot: %s", rec.Code, rec.Body.String())
	}
}

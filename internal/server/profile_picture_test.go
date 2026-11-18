package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

func profilePictureUploadRequest(t *testing.T, profileID string, fileBody []byte, contentType string) *http.Request {
	t.Helper()
	body, ct := backupMultipartBody(t, map[string]string{"profileID": profileID}, "picture", fileBody, contentType)
	// Literal path: TestEveryCommandRouteHasBehaviorTest scans for it.
	req := httptest.NewRequest(http.MethodPost, "/api/profiles/picture", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set(auth.CSRFHeader, "1")
	return req
}

func servePicture(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// wideSplitPNG is a 1024x600 PNG: left half red, right half blue, with a
// transparent band at the top.
func wideSplitPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 600))
	for y := range 600 {
		for x := range 1024 {
			c := color.NRGBA{R: 220, A: 255}
			if x >= 512 {
				c = color.NRGBA{B: 220, A: 255}
			}
			if y < 40 {
				c.A = 0
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return body.Bytes()
}

func TestProfilePictureUploadNormalizesStoresAndServes(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	req := profilePictureUploadRequest(t, "profile-default", wideSplitPNG(t), "image/png")
	authorizeViewer(req) // a Profile may set its own picture
	rec := servePicture(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp profilePictureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if !strings.HasPrefix(resp.PictureURL, "/api/profiles/picture/profile-default?v=") {
		t.Fatalf("pictureURL = %q, want the versioned picture route", resp.PictureURL)
	}

	get := httptest.NewRequest(http.MethodGet, resp.PictureURL, nil)
	authorizeViewer(get)
	got := servePicture(srv, get)
	if got.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", got.Code, got.Body.String())
	}
	if ct := got.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", ct)
	}
	if cc := got.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("Cache-Control = %q, want immutable for the versioned URL", cc)
	}
	img, err := jpeg.Decode(bytes.NewReader(got.Body.Bytes()))
	if err != nil {
		t.Fatalf("served picture is not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != profilePictureSize || b.Dy() != profilePictureSize {
		t.Fatalf("served picture is %v, want %dx%d", b, profilePictureSize, profilePictureSize)
	}
	// Center crop keeps both halves; the transparent band is the backdrop.
	assertNear(t, "left", img.At(100, 300), color.RGBA{R: 220})
	assertNear(t, "right", img.At(400, 300), color.RGBA{B: 220})
	assertNear(t, "transparent band", img.At(100, 5), profilePictureBackdrop)

	etag := got.Header().Get("ETag")
	revalidate := httptest.NewRequest(http.MethodGet, "/api/profiles/picture/profile-default", nil)
	authorizeManager(revalidate)
	revalidate.Header.Set("If-None-Match", etag)
	if rec := servePicture(srv, revalidate); rec.Code != http.StatusNotModified {
		t.Fatalf("revalidate status = %d, want 304", rec.Code)
	}
}

func assertNear(t *testing.T, name string, got color.Color, want color.RGBA) {
	t.Helper()
	r, g, b, _ := got.RGBA()
	diff := func(a uint32, b uint8) int { return int(a>>8) - int(b) }
	for _, d := range []int{diff(r, want.R), diff(g, want.G), diff(b, want.B)} {
		if d < -12 || d > 12 {
			t.Fatalf("%s pixel = (%d,%d,%d), want near (%d,%d,%d)", name, r>>8, g>>8, b>>8, want.R, want.G, want.B)
		}
	}
}

func TestProfilePictureWriteAuthorization(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	picture := pngArtwork(t, 16, 16)

	other := profilePictureUploadRequest(t, "profile-managed", picture, "image/png")
	authorizeViewer(other)
	if rec := servePicture(srv, other); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer setting another Profile's picture: status = %d, want 403", rec.Code)
	}

	noCSRF := profilePictureUploadRequest(t, "profile-default", picture, "image/png")
	noCSRF.Header.Del(auth.CSRFHeader)
	authorizeViewer(noCSRF)
	if rec := servePicture(srv, noCSRF); rec.Code != http.StatusForbidden {
		t.Fatalf("upload without CSRF header: status = %d, want 403", rec.Code)
	}

	anonymous := profilePictureUploadRequest(t, "profile-default", picture, "image/png")
	if rec := servePicture(srv, anonymous); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous upload: status = %d, want 401", rec.Code)
	}

	admin := profilePictureUploadRequest(t, "profile-managed", picture, "image/png")
	authorizeManager(admin) // holds an Admin Session in this test server
	if rec := servePicture(srv, admin); rec.Code != http.StatusOK {
		t.Fatalf("admin setting another Profile's picture: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	anonGet := httptest.NewRequest(http.MethodGet, "/api/profiles/picture/profile-managed", nil)
	if rec := servePicture(srv, anonGet); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET: status = %d, want 401", rec.Code)
	}
}

// A scoped developer key is refused on the picture endpoint whatever its
// grants: it is private UI surface, not part of the keyed API.
func TestProfilePictureReadRefusesScopedKeys(t *testing.T) {
	srv, st := newArtworkUploadTestServer(t)
	upload := profilePictureUploadRequest(t, "profile-managed", pngArtwork(t, 16, 16), "image/png")
	authorizeManager(upload)
	if rec := servePicture(srv, upload); rec.Code != http.StatusOK {
		t.Fatalf("upload: status = %d: %s", rec.Code, rec.Body.String())
	}

	viewer := httptest.NewRequest(http.MethodGet, "/api/profiles/picture/profile-managed", nil)
	authorizeViewer(viewer)
	if rec := servePicture(srv, viewer); rec.Code != http.StatusOK {
		t.Fatalf("session GET: status = %d, want 200", rec.Code)
	}

	for _, key := range []string{
		mintKey(t, st, "ingest", "", store.ScopeIngest),
		mintKey(t, st, "catalog", "profile-default", store.ScopeReadCatalog),
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/profiles/picture/profile-managed", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		if rec := servePicture(srv, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("scoped key GET: status = %d, want 401", rec.Code)
		}
	}
}

func TestProfilePictureRejectsUnsupportedImages(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	for name, body := range map[string][]byte{
		"gif":  gifArtwork(t),
		"text": []byte("not an image"),
	} {
		req := profilePictureUploadRequest(t, "profile-default", body, "image/png")
		authorizeViewer(req)
		if rec := servePicture(srv, req); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	unknown := profilePictureUploadRequest(t, "profile-nobody", pngArtwork(t, 8, 8), "image/png")
	authorizeManager(unknown)
	if rec := servePicture(srv, unknown); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown Profile: status = %d, want 400", rec.Code)
	}
}

func TestProfilePictureDeleteClearsIt(t *testing.T) {
	srv, _ := newArtworkUploadTestServer(t)
	upload := profilePictureUploadRequest(t, "profile-default", pngArtwork(t, 16, 16), "image/png")
	authorizeViewer(upload)
	if rec := servePicture(srv, upload); rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", rec.Code, rec.Body.String())
	}
	del := httptest.NewRequest(http.MethodDelete, "/api/profiles/picture/profile-default", nil)
	authorizeViewer(del)
	del.Header.Set(auth.CSRFHeader, "1")
	if rec := servePicture(srv, del); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}
	get := httptest.NewRequest(http.MethodGet, "/api/profiles/picture/profile-default", nil)
	authorizeViewer(get)
	if rec := servePicture(srv, get); rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete status = %d, want 404", rec.Code)
	}
}

// withEXIFOrientation inserts an APP1 Exif segment carrying orientation o
// right after the SOI marker of a baseline JPEG.
func withEXIFOrientation(jpg []byte, o uint16, order binary.ByteOrder) []byte {
	tiff := make([]byte, 8+2+12+4)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8)
	order.PutUint16(tiff[8:], 1)
	order.PutUint16(tiff[10:], 0x0112)
	order.PutUint16(tiff[12:], 3)
	order.PutUint32(tiff[14:], 1)
	order.PutUint16(tiff[18:], o)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	n := len(payload) + 2
	if n > 0xffff {
		panic("EXIF segment too long")
	}
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(n))
	segment = append(segment, payload...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, segment...)
	return append(out, jpg[2:]...)
}

func TestNormalizeProfilePictureAppliesEXIFOrientation(t *testing.T) {
	// A square whose top half is red and bottom half blue. Orientation 6
	// (rotate 90 clockwise) puts red on the right.
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			c := color.RGBA{R: 220, A: 255}
			if y >= 32 {
				c = color.RGBA{B: 220, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		tagged := withEXIFOrientation(buf.Bytes(), 6, order)
		if got := jpegOrientation(tagged); got != 6 {
			t.Fatalf("%v: jpegOrientation = %d, want 6", order, got)
		}
		out, err := normalizeProfilePicture(bytes.NewReader(tagged))
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		if bytes.Contains(out, []byte("Exif")) {
			t.Fatalf("normalized picture still carries EXIF metadata")
		}
		decoded, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		assertNear(t, "right after rotation", decoded.At(56, 32), color.RGBA{R: 220})
		assertNear(t, "left after rotation", decoded.At(8, 32), color.RGBA{B: 220})
	}
	if got := jpegOrientation(buf.Bytes()); got != 1 {
		t.Fatalf("untagged jpegOrientation = %d, want 1", got)
	}
	if got := jpegOrientation([]byte{0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}); got != 1 {
		t.Fatalf("truncated segment jpegOrientation = %d, want 1", got)
	}
}

package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// recordingArtworkCreator stands in for the graph command capability so the
// handler's audit bookkeeping around a failed create can be observed.
type recordingArtworkCreator struct {
	createErr error
	created   int
	outcomes  []AuditOutcome
}

func (c *recordingArtworkCreator) CreateMediaFromUnmatchedArtwork(
	context.Context, auth.Scope, string, string, string, int, []byte,
) (string, error) {
	c.created++
	if c.createErr != nil {
		return "", c.createErr
	}
	return "media-created", nil
}

func (c *recordingArtworkCreator) BeginAuditedCommand(context.Context, auth.Scope, string) (string, error) {
	return "operation-1", nil
}

func (c *recordingArtworkCreator) FinishAuditedCommand(
	_ context.Context, _ auth.Scope, _ string, _ string, outcome AuditOutcome,
) error {
	c.outcomes = append(c.outcomes, outcome)
	return nil
}

// pngWithDimensions rewrites a real PNG's IHDR to claim the given size, so
// the pixel cap can be exercised without allocating the decoded image.
func pngWithDimensions(t *testing.T, width, height uint32) []byte {
	t.Helper()
	data := pngArtwork(t, 1, 1)
	// 8-byte signature, then IHDR: length(4) type(4) data(13) crc(4).
	const ihdr = 8
	binary.BigEndian.PutUint32(data[ihdr+8:], width)
	binary.BigEndian.PutUint32(data[ihdr+12:], height)
	binary.BigEndian.PutUint32(data[ihdr+8+13:], crc32.ChecksumIEEE(data[ihdr+4:ihdr+8+13]))
	return data
}

func serveArtworkUpload(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestCreateMediaArtworkUploadRejectsMalformedRequests(t *testing.T) {
	srv, _, _, _ := newArtworkUploadServer(t)
	jpeg := jpegArtwork(t)
	tests := []struct {
		name     string
		build    func(t *testing.T) *http.Request
		want     int
		wantBody string
	}{
		{
			name: "no session",
			build: func(t *testing.T) *http.Request {
				return artworkMultipartRequest(t, "record-1", jpeg, "poster.jpg")
			},
			want: http.StatusUnauthorized, wantBody: "invalid session",
		},
		{
			name: "not multipart",
			build: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/media/artwork-upload/record-1", strings.NewReader("{}"))
				req.Header.Set("Content-Type", "application/json")
				authorizeManager(req)
				return req
			},
			want: http.StatusUnsupportedMediaType, wantBody: "multipart/form-data",
		},
		{
			name: "broken multipart body",
			build: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/media/artwork-upload/record-1", strings.NewReader("not a multipart body"))
				req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
				authorizeManager(req)
				return req
			},
			want: http.StatusBadRequest, wantBody: "invalid multipart body",
		},
		{
			name: "body over the transport cap",
			build: func(t *testing.T) *http.Request {
				req := artworkMultipartRequest(t, "record-1", bytes.Repeat([]byte{0xff}, int(maxArtworkUploadBody)+1), "poster.jpg")
				authorizeManager(req)
				return req
			},
			want: http.StatusRequestEntityTooLarge, wantBody: "artwork is too large",
		},
		{
			name: "missing title",
			build: func(t *testing.T) *http.Request {
				req := artworkMultipartRequestWithFields(t, "record-1", "library-local", " ", "", jpeg, "poster.jpg")
				authorizeManager(req)
				return req
			},
			want: http.StatusBadRequest, wantBody: "recordID, libraryID, and title are required",
		},
		{
			name: "negative year",
			build: func(t *testing.T) *http.Request {
				req := artworkMultipartRequestWithFields(t, "record-1", "library-local", "Home Movie", "-1", jpeg, "poster.jpg")
				authorizeManager(req)
				return req
			},
			want: http.StatusBadRequest, wantBody: "recordID, libraryID, and title are required",
		},
		{
			name: "missing artwork part",
			build: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/media/artwork-upload/record-1",
					strings.NewReader("--xyz\r\nContent-Disposition: form-data; name=\"libraryID\"\r\n\r\nlibrary-local\r\n"+
						"--xyz\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nHome Movie\r\n--xyz--\r\n"))
				req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
				authorizeManager(req)
				return req
			},
			want: http.StatusBadRequest, wantBody: "artwork is required",
		},
		{
			name: "truncated PNG",
			build: func(t *testing.T) *http.Request {
				req := artworkMultipartRequest(t, "record-1", []byte("\x89PNG\r\n\x1a\nnot-an-ihdr"), "poster.png")
				authorizeManager(req)
				return req
			},
			want: http.StatusUnsupportedMediaType, wantBody: "valid JPEG or PNG",
		},
		{
			name: "pixel bomb",
			build: func(t *testing.T) *http.Request {
				req := artworkMultipartRequest(t, "record-1", pngWithDimensions(t, 10_000, 10_000), "poster.png")
				authorizeManager(req)
				return req
			},
			want: http.StatusUnsupportedMediaType, wantBody: "valid JPEG or PNG",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveArtworkUpload(srv, tc.build(t))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestCreateMediaArtworkUploadUnavailableWithoutCreator(t *testing.T) {
	srv := newTestServer(t)
	req := artworkMultipartRequest(t, "record-1", jpegArtwork(t), "poster.jpg")
	authorizeManager(req)
	rec := serveArtworkUpload(srv, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "artwork upload unavailable") {
		t.Fatalf("status = %d body=%s, want 503 artwork upload unavailable", rec.Code, rec.Body.String())
	}
}

func TestCreateMediaArtworkUploadConflictAuditsError(t *testing.T) {
	srv, _, _, _ := newArtworkUploadServer(t)
	creator := &recordingArtworkCreator{createErr: store.ErrUnmatchedCreateConflict}
	srv.unmatchedArtwork = creator

	// An empty year is optional and must still reach the creator.
	req := artworkMultipartRequestWithFields(t, "record-1", "library-local", "Home Movie", "", pngArtwork(t, 4, 3), "poster.png")
	authorizeManager(req)
	rec := serveArtworkUpload(srv, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if creator.created != 1 {
		t.Fatalf("creator calls = %d, want 1", creator.created)
	}
	if len(creator.outcomes) != 1 || creator.outcomes[0] != AuditError {
		t.Fatalf("audit outcomes = %v, want exactly [%s]", creator.outcomes, AuditError)
	}
}

func TestOptionalArtworkYear(t *testing.T) {
	for raw, want := range map[string]int{"": 0, "  ": 0, " 1994 ": 1994, "0": 0} {
		got, err := optionalArtworkYear(raw)
		if err != nil || got != want {
			t.Fatalf("optionalArtworkYear(%q) = (%d, %v), want (%d, nil)", raw, got, err, want)
		}
	}
	for _, raw := range []string{"-1", "nineteen", "19.5"} {
		if _, err := optionalArtworkYear(raw); err == nil {
			t.Fatalf("optionalArtworkYear(%q) accepted an invalid year", raw)
		}
	}
}

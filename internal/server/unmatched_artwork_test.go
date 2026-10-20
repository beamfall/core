package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

const artworkUploadBehaviorRoutePattern = "/api/media/artwork-upload/{recordID}"

func newArtworkUploadServer(t *testing.T) (*Server, *store.Store, *artwork.Service, *graph.Handler) {
	t.Helper()
	st := storetest.Open(t)
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, nil)
	tokens := streamtoken.NewService([]byte("artwork-upload-test-key"), clock)
	tokens.SetRecorder(st)
	artworkService := artwork.NewService(st, nil, t.TempDir())
	graphHandler := graph.NewHandler(manifests, authService, st, nil)
	graphHandler.SetArtwork(artworkService)
	graphHandler.SetArtworkCandidateServices(nil, artworkService)
	graphServer := testServerGraphHandler{Handler: graphHandler}
	srv := New(
		manifests,
		playback.NewService(st, tokens, clock),
		beam.NewService(st, authService, clock),
		pairing.NewService(st, clock),
		http.HandlerFunc(graphServer.ServeHTTP),
		mediahttp.NewHandler(
			st,
			authService,
			tokens,
			artworkService,
			playback.NewSessions(nil, t.TempDir(), nil),
		),
		authService,
		st,
		WithUnmatchedArtworkCreator(graphServer),
	)
	return srv, st, artworkService, graphHandler
}

func queueArtworkUploadRecord(t *testing.T, st *store.Store) string {
	t.Helper()
	id, err := st.QueueUnmatchedRecord(context.Background(), store.UnmatchedRecord{
		Origin:        store.UnmatchedOriginScan,
		LibraryID:     "library-local",
		ImporterID:    "manual-test",
		SourceHandle:  "/media/home-movie.mkv",
		FailureReason: store.UnmatchedReasonNoCandidates,
	})
	if err != nil {
		t.Fatalf("QueueUnmatchedRecord: %v", err)
	}
	return id
}

func jpegArtwork(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	if err := jpeg.Encode(&body, img, nil); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return body.Bytes()
}

func artworkMultipartRequest(
	t *testing.T,
	recordID string,
	artworkData []byte,
	filename string,
) *http.Request {
	return artworkMultipartRequestWithFields(
		t, recordID, "library-local", "Home Movie", "1994", artworkData, filename,
	)
}

func artworkMultipartRequestWithFields(
	t *testing.T,
	recordID, libraryID, title, year string,
	artworkData []byte,
	filename string,
) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{
		"libraryID": libraryID,
		"title":     title,
		"year":      year,
	} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%s): %v", key, err)
		}
	}
	file, err := writer.CreateFormFile("artwork", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := file.Write(artworkData); err != nil {
		t.Fatalf("write artwork: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/media/artwork-upload/"+recordID,
		&body,
	)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestCreateMediaArtworkUploadPinsAndRendersPoster(t *testing.T) {
	srv, st, artworkService, _ := newArtworkUploadServer(t)
	recordID := queueArtworkUploadRecord(t, st)
	req := artworkMultipartRequest(t, recordID, jpegArtwork(t), "poster.jpg")
	authorizeManager(req)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var response createMediaArtworkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.MediaID == "" {
		t.Fatal("response mediaID is empty")
	}
	activity, err := st.ActivityEntries(context.Background(), store.ActivityQuery{
		Filter:                   store.LibraryFilter{AllLibraries: true, Revealed: true},
		Limit:                    20,
		IncludeMutationLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var attempt, finished store.ActivityEntry
	for _, entry := range activity {
		switch {
		case entry.Action == store.ActivityMutationAttempt &&
			entry.TargetType == "http:"+createMediaArtworkRouteKey:
			attempt = entry
		case entry.Action == store.ActivityMutationFinished &&
			entry.TargetType == "http:"+createMediaArtworkRouteKey+":success":
			finished = entry
		}
	}
	if attempt.TargetID == "" || finished.TargetID != attempt.TargetID ||
		attempt.ProfileID == "" || finished.ProfileID != attempt.ProfileID {
		t.Fatalf("multipart mutation lifecycle attempt=%+v finished=%+v", attempt, finished)
	}
	retryReq := artworkMultipartRequest(t, recordID, jpegArtwork(t), "poster.jpg")
	authorizeManager(retryReq)
	retryRec := httptest.NewRecorder()
	srv.ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusCreated {
		t.Fatalf("lost-response retry status = %d, want 201; body=%s", retryRec.Code, retryRec.Body.String())
	}
	var retryResponse createMediaArtworkResponse
	if err := json.Unmarshal(retryRec.Body.Bytes(), &retryResponse); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if retryResponse.MediaID != response.MediaID {
		t.Fatalf("lost-response retry mediaID = %q, want %q", retryResponse.MediaID, response.MediaID)
	}
	conflictReq := artworkMultipartRequestWithFields(
		t, recordID, "library-local", "Different Identity", "1994", jpegArtwork(t), "poster.jpg",
	)
	authorizeManager(conflictReq)
	conflictRec := httptest.NewRecorder()
	srv.ServeHTTP(conflictRec, conflictReq)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("conflicting replay status = %d, want 409; body=%s", conflictRec.Code, conflictRec.Body.String())
	}
	candidates, err := st.ArtworkCandidates(
		context.Background(),
		store.MediaID(response.MediaID),
		artwork.SlotPoster,
		store.LibraryFilter{AllLibraries: true},
	)
	if err != nil {
		t.Fatalf("ArtworkCandidates: %v", err)
	}
	if len(candidates) != 1 || !candidates[0].Applied {
		t.Fatalf("candidates = %#v, want one applied poster", candidates)
	}
	if _, ok := artworkService.ArtworkVersion(store.MediaID(response.MediaID), artwork.SlotPoster); !ok {
		t.Fatal("uploaded poster was not stored in the Media artwork slot")
	}
	records, err := st.UnmatchedRecords(
		context.Background(),
		store.LibraryFilter{AllLibraries: true},
	)
	if err != nil {
		t.Fatalf("UnmatchedRecords: %v", err)
	}
	for _, record := range records {
		if record.ID == recordID {
			t.Fatal("successfully uploaded record remains unmatched")
		}
	}

	renderReq := httptest.NewRequest(
		http.MethodGet,
		"/api/media/artwork/"+response.MediaID+"/"+artwork.SlotPoster,
		nil,
	)
	authorizeManager(renderReq)
	renderRec := httptest.NewRecorder()
	srv.ServeHTTP(renderRec, renderReq)
	if renderRec.Code != http.StatusOK || renderRec.Body.Len() == 0 {
		t.Fatalf("rendered artwork status=%d bytes=%d", renderRec.Code, renderRec.Body.Len())
	}
}

type failingArtworkAuditRecorder struct{}

func (failingArtworkAuditRecorder) RecordAudit(context.Context, observability.AuditRecord) error {
	return errors.New("injected audit failure")
}

type failingFinalArtworkAuditRecorder struct{}

func (failingFinalArtworkAuditRecorder) RecordAudit(
	_ context.Context,
	record observability.AuditRecord,
) error {
	if record.Outcome == observability.AuditSuccess {
		return errors.New("injected final audit failure")
	}
	return nil
}

func TestCreateMediaArtworkUploadAuditFailurePreventsMutation(t *testing.T) {
	srv, st, _, graphHandler := newArtworkUploadServer(t)
	graphHandler.SetAuditRecorder(failingArtworkAuditRecorder{})
	recordID := queueArtworkUploadRecord(t, st)
	before, err := st.LibraryMediaTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req := artworkMultipartRequest(t, recordID, jpegArtwork(t), "poster.jpg")
	authorizeManager(req)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("audit failure status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	after, err := st.LibraryMediaTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("Media total after failed audit = %d, want unchanged %d", after, before)
	}
	record, err := st.UnmatchedRecordByID(
		context.Background(),
		recordID,
		store.LibraryFilter{AllLibraries: true, Revealed: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.Resolved() {
		t.Fatalf("record resolved despite failed audit: %+v", record)
	}
}

func TestCreateMediaArtworkUploadFinalAuditFailurePreservesCommittedResponse(t *testing.T) {
	srv, st, _, graphHandler := newArtworkUploadServer(t)
	graphHandler.SetAuditRecorder(failingFinalArtworkAuditRecorder{})
	recordID := queueArtworkUploadRecord(t, st)
	before, err := st.LibraryMediaTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req := artworkMultipartRequest(t, recordID, jpegArtwork(t), "poster.jpg")
	authorizeManager(req)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("final audit failure status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	after, err := st.LibraryMediaTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("Media total after committed response = %d, want %d", after, before+1)
	}
	record, err := st.UnmatchedRecordByID(
		context.Background(),
		recordID,
		store.LibraryFilter{AllLibraries: true, Revealed: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Resolved() {
		t.Fatalf("record remained pending despite committed response: %+v", record)
	}
}

func TestCreateMediaArtworkUploadRejectsInvalidPayloads(t *testing.T) {
	srv, _, _, _ := newArtworkUploadServer(t)
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{name: "non-image", data: []byte("not an image"), want: http.StatusUnsupportedMediaType},
		{
			name: "oversized",
			data: bytes.Repeat([]byte{0xff}, int(maxArtworkUploadBytes)+1),
			want: http.StatusRequestEntityTooLarge,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := artworkMultipartRequest(t, "record-1", tc.data, "poster.jpg")
			authorizeManager(req)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestCreateMediaArtworkUploadRequiresManager(t *testing.T) {
	srv, _, _, _ := newArtworkUploadServer(t)
	req := artworkMultipartRequest(t, "record-1", jpegArtwork(t), "poster.jpg")
	authorizeViewer(req)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "manager role required") {
		t.Fatalf("viewer response = %q", rec.Body.String())
	}
}

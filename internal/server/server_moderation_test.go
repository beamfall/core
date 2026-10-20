package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/moderation"
)

// withModeration wires a fresh file-backed moderation service into the test
// server so the /api/moderation/ routes are live rather than degrading to 503.
func withModeration(t *testing.T, srv *Server) *moderation.Service {
	t.Helper()
	m, err := moderation.Open(filepath.Join(t.TempDir(), "moderation", "feed.json"),
		moderation.ContactInfo{Email: "abuse@example.test", Note: "contact us"})
	if err != nil {
		t.Fatalf("moderation.Open: %v", err)
	}
	srv.SetModeration(m)
	return m
}

func TestModerationReportRequiresSession(t *testing.T) {
	srv := newTestServer(t)
	withModeration(t, srv)
	req := newJSONRequest(http.MethodPost, "/api/moderation/report",
		bytes.NewReader([]byte(`{"targetType":"media","targetID":"m1","reason":"spam"}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestModerationReportSucceedsForViewer(t *testing.T) {
	srv := newTestServer(t)
	withModeration(t, srv)
	req := newJSONRequest(http.MethodPost, "/api/moderation/report",
		bytes.NewReader([]byte(`{"targetType":"media","targetID":"m1","reason":"spam"}`)))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestModerationBlockSucceedsAndPersistsForViewer(t *testing.T) {
	srv := newTestServer(t)
	withModeration(t, srv)

	blockReq := newJSONRequest(http.MethodPost, "/api/moderation/block",
		bytes.NewReader([]byte(`{"targetType":"media","targetID":"m9"}`)))
	authorizeViewer(blockReq)
	blockRec := httptest.NewRecorder()
	srv.ServeHTTP(blockRec, blockReq)
	if blockRec.Code != http.StatusOK {
		t.Fatalf("block status = %d, want 200: %s", blockRec.Code, blockRec.Body.String())
	}

	// The block round-trips through GET /api/moderation/blocks for the same session.
	listReq := httptest.NewRequest(http.MethodGet, "/api/moderation/blocks", nil)
	authorizeViewer(listReq)
	listRec := httptest.NewRecorder()
	srv.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("blocks status = %d, want 200: %s", listRec.Code, listRec.Body.String())
	}
	var got struct {
		Blocks []moderation.Block `json:"blocks"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode blocks: %v", err)
	}
	if len(got.Blocks) != 1 || got.Blocks[0].TargetID != "m9" {
		t.Fatalf("blocks = %+v, want one block of m9", got.Blocks)
	}
}

func TestModerationContactIsPublic(t *testing.T) {
	srv := newTestServer(t)
	withModeration(t, srv)
	// No session header: contact is public.
	req := httptest.NewRequest(http.MethodGet, "/api/moderation/contact", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("contact status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var c moderation.ContactInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode contact: %v", err)
	}
	if c.Email != "abuse@example.test" {
		t.Fatalf("contact email = %q, want abuse@example.test", c.Email)
	}
}

func TestModerationDegradesTo503WhenUnwired(t *testing.T) {
	srv := newTestServer(t) // no SetModeration
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/moderation/report", `{"targetType":"media","targetID":"m1"}`},
		{http.MethodPost, "/api/moderation/block", `{"targetType":"media","targetID":"m1"}`},
		{http.MethodGet, "/api/moderation/blocks", ""},
		{http.MethodGet, "/api/moderation/contact", ""},
	} {
		var req *http.Request
		if tc.body == "" {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		} else {
			req = newJSONRequest(tc.method, tc.path, bytes.NewReader([]byte(tc.body)))
		}
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s status = %d, want 503", tc.method, tc.path, rec.Code)
		}
	}
}

func TestModerationReportRejectsEmptyTarget(t *testing.T) {
	srv := newTestServer(t)
	withModeration(t, srv)
	req := newJSONRequest(http.MethodPost, "/api/moderation/report",
		bytes.NewReader([]byte(`{"targetType":"","targetID":""}`)))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

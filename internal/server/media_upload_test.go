package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

type fakeMediaUploadPlanner struct {
	root      string
	committed []string
}

func (f *fakeMediaUploadPlanner) ProposeMediaUploads(_ context.Context, _ store.LibraryFilter, _ store.LibraryID, uploads []store.MediaUpload) ([]MediaUploadProposal, error) {
	out := make([]MediaUploadProposal, 0, len(uploads))
	for _, u := range uploads {
		out = append(out, MediaUploadProposal{UploadID: u.ID, FileName: u.FileName, Destination: filepath.Join(f.root, "Proposed")})
	}
	return out, nil
}

func (f *fakeMediaUploadPlanner) SearchMediaUploadTargets(context.Context, store.LibraryFilter, store.LibraryID, string) ([]MediaUploadTarget, error) {
	return nil, nil
}

func (f *fakeMediaUploadPlanner) MediaUploadCommitted(_ context.Context, _ store.LibraryID, root string) error {
	f.committed = append(f.committed, root)
	return nil
}

func mediaUploadRequest(t *testing.T, method, path string, body []byte, offset int64) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if method == http.MethodPut {
		req.Header.Set(MediaUploadOffsetHeader, strconv.FormatInt(offset, 10))
		req.Header.Set("Content-Type", "application/octet-stream")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(auth.CSRFHeader, "1")
	return req
}

func serveMediaUpload(t *testing.T, srv *Server, req *http.Request, want int, out any) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", req.Method, req.URL.Path, rec.Code, want, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
}

func TestMediaUploadRoutesStageProposeAndCommit(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(root)
	planner := &fakeMediaUploadPlanner{root: canonical}
	srv.mediaUploadPlanner = planner
	data := []byte("media-bytes")

	create, _ := json.Marshal(map[string]any{"libraryID": "library-local", "fileName": "clip.mkv", "size": len(data)})
	var status mediaUploadStatus
	req := mediaUploadRequest(t, http.MethodPost, "/api/media/uploads", create, 0)
	authorizeManager(req)
	serveMediaUpload(t, srv, req, http.StatusOK, &status)
	if status.UploadID == "" || status.Offset != 0 || status.ChunkSize != MediaUploadChunkBytes {
		t.Fatalf("create = %+v", status)
	}
	chunk := "/api/media/uploads/" + status.UploadID

	req = mediaUploadRequest(t, http.MethodPut, chunk, data[:4], 0)
	authorizeManager(req)
	serveMediaUpload(t, srv, req, http.StatusOK, &status)
	req = mediaUploadRequest(t, http.MethodPut, chunk, data[4:], 0)
	authorizeManager(req)
	serveMediaUpload(t, srv, req, http.StatusConflict, &status)
	if status.Offset != 4 {
		t.Fatalf("conflict offset = %d, want 4", status.Offset)
	}
	req = mediaUploadRequest(t, http.MethodPut, chunk, data[4:], 4)
	authorizeManager(req)
	serveMediaUpload(t, srv, req, http.StatusOK, &status)

	var proposals struct{ Proposals []MediaUploadProposal }
	body, _ := json.Marshal(map[string]any{"libraryID": "library-local", "uploadIDs": []string{status.UploadID}})
	req = mediaUploadRequest(t, http.MethodPost, "/api/media/uploads/proposals", body, 0)
	authorizeManager(req)
	serveMediaUpload(t, srv, req, http.StatusOK, &proposals)
	if len(proposals.Proposals) != 1 || proposals.Proposals[0].UploadID != status.UploadID {
		t.Fatalf("proposals = %+v", proposals)
	}

	var commit struct{ Results []mediaUploadCommitResult }
	body, _ = json.Marshal(map[string]any{"items": []map[string]string{
		{"uploadID": status.UploadID, "destination": proposals.Proposals[0].Destination},
		{"uploadID": "00000000000000000000000000000000", "destination": canonical},
	}})
	req = mediaUploadRequest(t, http.MethodPost, "/api/media/uploads/commit", body, 0)
	authorizeManager(req)
	serveMediaUpload(t, srv, req, http.StatusOK, &commit)
	if len(commit.Results) != 2 || commit.Results[0].Error != "" || commit.Results[1].Error == "" {
		t.Fatalf("commit = %+v", commit)
	}
	placed := filepath.Join(canonical, "Proposed", "clip.mkv")
	if commit.Results[0].Path != placed {
		t.Fatalf("placed at %q, want %q", commit.Results[0].Path, placed)
	}
	if got, err := os.ReadFile(placed); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("placed bytes = %q, %v", got, err)
	}
	if len(planner.committed) != 1 || planner.committed[0] != canonical {
		t.Fatalf("scan roots = %v", planner.committed)
	}
	activity, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{AllLibraries: true}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range activity {
		if entry.Action == store.ActivityManagedFileCreate && entry.TargetID == placed {
			found = true
		}
	}
	if !found {
		t.Fatalf("no managed_file.create activity for %s", placed)
	}
}

func TestMediaUploadRoutesRequireAdminSession(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	create, _ := json.Marshal(map[string]any{"libraryID": "library-local", "fileName": "clip.mkv", "size": 1})

	req := mediaUploadRequest(t, http.MethodPost, "/api/media/uploads", create, 0)
	authorizeViewer(req)
	serveMediaUpload(t, srv, req, http.StatusForbidden, nil)

	req = mediaUploadRequest(t, http.MethodPost, "/api/media/uploads", create, 0)
	req.Header.Del(auth.CSRFHeader)
	authorizeManager(req)
	serveMediaUpload(t, srv, req, http.StatusForbidden, nil)
}

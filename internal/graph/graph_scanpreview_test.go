package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type scanPreviewResponse struct {
	Data struct {
		ScanPreview       ScanPreviewInfo `json:"scanPreview"`
		CommitScanPreview JobInfo         `json:"commitScanPreview"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

const (
	scanPreviewQuery = `mutation ScanPreview($libraryID: String!) { scanPreview(libraryID: $libraryID) { previewToken auditEventID libraryID roots expiresAt adds updates missing unchanged changes { kind path } truncated missingSkipped } }`
	scanCommitQuery  = `mutation CommitScanPreview($previewToken: String!) { commitScanPreview(previewToken: $previewToken) { id kind status } }`
)

func postScanPreviewGraph(t *testing.T, handler *Handler, query string, variables map[string]any) scanPreviewResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp scanPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return resp
}

// seedScanPreviewLibrary catalogues two settled files under a fresh root and
// returns the root, the store and a handler with an open Admin Session.
func seedScanPreviewLibrary(t *testing.T) (string, *store.Store, *Handler) {
	t.Helper()
	st := storetest.Open(t)
	root := t.TempDir()
	for _, name := range []string{"clip-one.mp4", "clip-two.mp4"} {
		writeSettledScanFile(t, root, name)
	}
	ctx := context.Background()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Scan(ctx, st, nil, nil, nil, nil, nil, scan.Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("seed scan: %v", err)
	}
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	return root, st, handler
}

func writeSettledScanFile(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("bytes of "+name), 0o600); err != nil {
		t.Fatal(err)
	}
	settled := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, settled, settled); err != nil {
		t.Fatal(err)
	}
	return path
}

func queuedScanJobs(t *testing.T, st *store.Store) int {
	t.Helper()
	jobs, err := st.ListJobs(context.Background(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, job := range jobs {
		if job.Kind == scan.JobKind {
			count++
		}
	}
	return count
}

func scanPreviewActivity(t *testing.T, st *store.Store, operationID string) (store.ActivityEntry, bool) {
	t.Helper()
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.OperationID == operationID {
			return entry, true
		}
	}
	return store.ActivityEntry{}, false
}

func previewScan(t *testing.T, handler *Handler) ScanPreviewInfo {
	t.Helper()
	resp := postScanPreviewGraph(t, handler, scanPreviewQuery, map[string]any{"libraryID": "library-local"})
	if len(resp.Errors) != 0 {
		t.Fatalf("scanPreview errors = %#v", resp.Errors)
	}
	return resp.Data.ScanPreview
}

func commitScan(t *testing.T, handler *Handler, token string) scanPreviewResponse {
	t.Helper()
	return postScanPreviewGraph(t, handler, scanCommitQuery, map[string]any{"previewToken": token})
}

// TestScanPreviewPlansWithoutWriting is the CORE-1054 preview acceptance:
// planned counts and a bounded list, a single-use token and an audit id that
// names a real activity row, and no job or catalog write.
func TestScanPreviewPlansWithoutWriting(t *testing.T) {
	root, st, handler := seedScanPreviewLibrary(t)
	added := writeSettledScanFile(t, root, "clip-three.mp4")
	before, err := scan.LibraryStateDigest(context.Background(), st, "library-local", []string{root})
	if err != nil {
		t.Fatal(err)
	}

	preview := previewScan(t, handler)

	if preview.Adds != 1 || preview.Updates != 0 || preview.Missing != 0 || preview.Unchanged != 2 {
		t.Fatalf("preview counts = %+v, want 1 add and 2 unchanged", preview)
	}
	if len(preview.Changes) != 1 || preview.Changes[0] != (ScanPreviewChangeInfo{Kind: "add", Path: added}) {
		t.Fatalf("preview changes = %+v, want the added file", preview.Changes)
	}
	if len(preview.PreviewToken) < 32 || !strings.HasPrefix(preview.AuditEventID, "scan-preview-") {
		t.Fatalf("token %q / audit id %q, want a long token and a scan-preview audit id", preview.PreviewToken, preview.AuditEventID)
	}
	if preview.LibraryID != "library-local" || len(preview.Roots) != 1 || preview.Roots[0] != root || preview.ExpiresAt == "" {
		t.Fatalf("preview binding = %+v", preview)
	}
	entry, ok := scanPreviewActivity(t, st, preview.AuditEventID)
	if !ok || entry.Action != store.ActivityScanPreview || entry.TargetID != "library-local" {
		t.Fatalf("audit row = %+v (found %v), want a scan.preview row for the library", entry, ok)
	}
	if jobs := queuedScanJobs(t, st); jobs != 0 {
		t.Fatalf("preview queued %d scan jobs, want none", jobs)
	}
	after, err := scan.LibraryStateDigest(context.Background(), st, "library-local", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("preview changed the catalog")
	}
}

// TestCommitScanPreviewRunsOnceForAValidToken enqueues the previewed scan,
// audits the commit against the preview's audit id, and refuses the same
// token a second time.
func TestCommitScanPreviewRunsOnceForAValidToken(t *testing.T) {
	_, st, handler := seedScanPreviewLibrary(t)
	preview := previewScan(t, handler)

	resp := commitScan(t, handler, preview.PreviewToken)
	if len(resp.Errors) != 0 {
		t.Fatalf("commit errors = %#v", resp.Errors)
	}
	if resp.Data.CommitScanPreview.Kind != "scan" || resp.Data.CommitScanPreview.Status != "queued" {
		t.Fatalf("commit = %#v, want a queued scan job", resp.Data.CommitScanPreview)
	}
	if jobs := queuedScanJobs(t, st); jobs != 1 {
		t.Fatalf("queued scan jobs = %d, want 1", jobs)
	}
	if entry, ok := scanPreviewActivity(t, st, preview.AuditEventID+".commit"); !ok || entry.Action != store.ActivityScanPreviewCommit {
		t.Fatalf("commit audit row = %+v (found %v)", entry, ok)
	}

	reused := commitScan(t, handler, preview.PreviewToken)
	if len(reused.Errors) == 0 || reused.Errors[0].Message != "scan preview token was already used" {
		t.Fatalf("reused token errors = %#v, want already used", reused.Errors)
	}
}

// TestCommitScanPreviewRefusesStaleTokens refuses a token whose Library
// changed after the preview (a scan refreshed the catalog, or its roots
// changed) and a token past its expiry, and enqueues nothing for any of them.
func TestCommitScanPreviewRefusesStaleTokens(t *testing.T) {
	cases := []struct {
		name    string
		change  func(t *testing.T, root string, st *store.Store, handler *Handler)
		wantErr string
	}{
		{name: "catalog changed", wantErr: "scan preview is stale because the library changed", change: func(t *testing.T, root string, st *store.Store, _ *Handler) {
			writeSettledScanFile(t, root, "clip-three.mp4")
			if _, err := scan.Scan(context.Background(), st, nil, nil, nil, nil, nil, scan.Request{LibraryID: "library-local", Root: root}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "roots changed", wantErr: "scan preview is stale because the library changed", change: func(t *testing.T, root string, st *store.Store, _ *Handler) {
			if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root, t.TempDir()}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "expired", wantErr: "scan preview token has expired", change: func(_ *testing.T, _ string, _ *store.Store, handler *Handler) {
			later := time.Now().Add(scanPreviewTTL + time.Minute)
			handler.scanPreviews.mu.Lock()
			handler.scanPreviews.now = func() time.Time { return later }
			handler.scanPreviews.mu.Unlock()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, st, handler := seedScanPreviewLibrary(t)
			preview := previewScan(t, handler)
			tc.change(t, root, st, handler)

			resp := commitScan(t, handler, preview.PreviewToken)
			if len(resp.Errors) == 0 || resp.Errors[0].Message != tc.wantErr {
				t.Fatalf("commit errors = %#v, want %q", resp.Errors, tc.wantErr)
			}
			if jobs := queuedScanJobs(t, st); jobs != 0 {
				t.Fatalf("refused commit queued %d scan jobs", jobs)
			}
		})
	}
}

// TestCommitScanPreviewRefusesUnknownToken does not distinguish a forged
// token from one it never issued.
func TestCommitScanPreviewRefusesUnknownToken(t *testing.T) {
	_, _, handler := seedScanPreviewLibrary(t)
	resp := commitScan(t, handler, strings.Repeat("0", 64))
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "unknown scan preview token" {
		t.Fatalf("errors = %#v, want unknown scan preview token", resp.Errors)
	}
}

// TestScanPreviewRequiresAdminSession keeps the preview behind the same
// Admin Session gate as scanLibrary.
func TestScanPreviewRequiresAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	resp := postScanPreviewGraph(t, handler, scanPreviewQuery, map[string]any{"libraryID": "library-local"})
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", resp.Errors)
	}
}

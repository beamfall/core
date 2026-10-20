//go:build io_e2e

package ioe2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/apptest"
	"github.com/beamfall/core/internal/ffmpeg/ffmpegtest"
)

// requireNoGraphQLErrors fails loud if a GraphQL response carries an errors
// array, surfacing the messages.
func requireNoGraphQLErrors(t *testing.T, resp *apptest.Response) {
	t.Helper()
	var out struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	resp.JSON(&out)
	if len(out.Errors) != 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", out.Errors)
	}
}

// scanRealMediaRoot drives the real library-scan happy-path end to end: boot a
// manager+admin harness, register an on-disk library root holding a real media
// file, trigger the scan over the wire (setLibraryRoots → scanLibrary), then
// drain the enqueued scan job (and its follow-on sweeps) through the
// App.RunPendingJobs seam. Returns the harness and the captured DataDir. The
// scan handler's filepath.WalkDir over the root IS the fs-scan-walk operation;
// a job that completes without error over a real-media tree proves the walk ran
// against real bytes (the handler probes every file it walks via ffmpeg).
func scanRealMediaRoot(t *testing.T) (*apptest.Harness, string) {
	t.Helper()
	ffmpegtest.Require(t) // the scan handler + artwork sweep probe/frame-grab real files
	const adminPassword = "scan-walk-admin"
	h, client, dataDir := bootFS(t, adminPassword)
	openAdminSession(t, client, adminPassword)

	root := t.TempDir()
	src, err := os.ReadFile(fixtures(t).Path("av_basic"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), src, 0o644); err != nil {
		t.Fatalf("seed library root: %v", err)
	}

	setRoots := `mutation($libraryID:String!,$roots:[String!]!){ setLibraryRoots(libraryID:$libraryID, roots:$roots){ id roots } }`
	resp := client.POST("/api/graphql", "application/json",
		graphqlBody(t, setRoots, map[string]any{"libraryID": "library-local", "roots": []string{root}}))
	requireNoGraphQLErrors(t, resp)

	scan := `mutation($libraryID:String!,$root:String!){ scanLibrary(libraryID:$libraryID, root:$root){ id kind status } }`
	resp = client.POST("/api/graphql", "application/json",
		graphqlBody(t, scan, map[string]any{"libraryID": "library-local", "root": root}))
	requireNoGraphQLErrors(t, resp)

	n, err := h.App.RunPendingJobs(ctx(t))
	if err != nil {
		t.Fatalf("RunPendingJobs (scan pipeline failed): %v", err)
	}
	if n < 1 {
		t.Fatalf("RunPendingJobs ran %d jobs, want >=1 (the scan job)", n)
	}
	return h, dataDir
}

// TestScanWalkJourney covers the real filesystem library walk.
//
// io:fs-scan-walk
func TestScanWalkJourney(t *testing.T) {
	scanRealMediaRoot(t)
}

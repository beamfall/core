package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store/storetest"
)

// writeMediaFile drops a file under root, backdating its mtime so it is past
// the scan's stability window (irrelevant to the estimate, which ignores the
// window, but keeps the fixture honest about what would import).
func writeMediaFile(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// TestScanEstimateOperatorGated asserts the first-scan estimate is a
// Console operation: a non-admin manager session is refused with the Admin
// Session gate, exactly like the other wizard fields. (The registry-driven
// TestConsoleFieldsRequireAdminSession enforces this for every adminFields
// entry too; this is the explicit OPS-4 acceptance check.)
func TestScanEstimateOperatorGated(t *testing.T) {
	// feature:first-scan-estimate
	handler := newTestHandler(t)
	_, errs := post(t, handler,
		`{"query":"query E($root:String!){ scanEstimate(root:$root){ estimatedFiles } }","variables":{"root":"/tmp"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("scanEstimate without admin session errors = %#v, want admin-session gate", errs)
	}
}

// TestScanEstimateSplitsInlineFromDeferrable is the OPS-4 end-to-end acceptance:
// over an admin-gated query the estimate distinguishes the inline,
// playability-gating scan work from the deferrable background asset backlog
// ("play now; sprites overnight"), and the deferrable counts scale with the
// file class split.
func TestScanEstimateSplitsInlineFromDeferrable(t *testing.T) {
	// feature:first-scan-estimate
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	root := t.TempDir()
	writeMediaFile(t, root, "movie.mkv")
	writeMediaFile(t, root, "show/ep1.mp4")
	writeMediaFile(t, root, "music/song.flac")
	writeMediaFile(t, root, "notes.txt") // excluded — non-media

	data, errs := post(t, handler,
		`{"query":"query E($root:String!){ scanEstimate(root:$root){ estimatedFiles estimatedVideoFiles estimatedAudioFiles estimatedInlineSeconds inlineWorkGatesPlayability deferrableWork { kind estimatedCount playabilityGating } } }","variables":{"root":"`+root+`"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("scanEstimate errors = %#v", errs)
	}
	var est ScanEstimateInfo
	mustUnmarshal(t, data["scanEstimate"], &est)

	if est.EstimatedFiles != 3 || est.EstimatedVideoFiles != 2 || est.EstimatedAudioFiles != 1 {
		t.Fatalf("estimate = %d files (%dv/%da), want 3 (2v/1a)", est.EstimatedFiles, est.EstimatedVideoFiles, est.EstimatedAudioFiles)
	}
	if !est.InlineWorkGatesPlayability {
		t.Error("inlineWorkGatesPlayability = false, want true (the inline scan gates playability)")
	}
	if est.EstimatedInlineSeconds <= 0 {
		t.Errorf("estimatedInlineSeconds = %d, want > 0", est.EstimatedInlineSeconds)
	}
	if len(est.DeferrableWork) == 0 {
		t.Fatal("deferrableWork empty, want the Generated Asset breakdown")
	}
	for _, w := range est.DeferrableWork {
		if w.PlayabilityGating {
			t.Errorf("deferrable kind %q gates playability, want false (sprites overnight)", w.Kind)
		}
	}
}

// TestScanEstimateRequiresRootOrLibrary asserts the estimate refuses a request
// with neither a root path nor a libraryID — there is nothing to walk.
func TestScanEstimateRequiresRootOrLibrary(t *testing.T) {
	// feature:first-scan-estimate
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	_, errs := post(t, handler, `{"query":"query { scanEstimate { estimatedFiles } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "root or libraryID is required") {
		t.Fatalf("scanEstimate with no args errors = %#v, want refusal", errs)
	}
}

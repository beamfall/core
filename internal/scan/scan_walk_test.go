package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

func TestScanAndEstimateSkipHiddenSidecarDirectories(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "movie.mp4", []byte("video bytes"))
	writeScanFile(t, root, ".HomeTheater/Backdrops/artwork.jpg", []byte("derived artwork"))
	writeScanFile(t, root, "@eaDir/thumbnail.jpg", []byte("NAS thumbnail"))
	writeScanFile(t, root, ".hidden/nested.mp4", []byte("hidden video"))

	estimate, err := EstimateRoots(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("EstimateRoots: %v", err)
	}

	for _, tc := range []struct {
		name    string
		workers WorkerGovernor
	}{
		{name: "sequential"},
		{name: "worker pool", workers: fixedGovernor(4)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.Open(t)
			result, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, tc.workers, Request{
				LibraryID: "library-local",
				Root:      root,
			})
			if err != nil {
				t.Fatalf("ScanWithWorkerGovernor: %v", err)
			}
			if result.MediaImported != 1 || result.FilesSeen != 1 {
				t.Fatalf("scan result = %d Media / %d files, want 1/1", result.MediaImported, result.FilesSeen)
			}
			if len(result.Files) != 1 || filepath.Base(result.Files[0].Path) != "movie.mp4" {
				t.Fatalf("imported files = %#v, want movie.mp4 only", result.Files)
			}
			if estimate.EstimatedFiles != result.FilesSeen {
				t.Fatalf("estimate = %d files, scan = %d", estimate.EstimatedFiles, result.FilesSeen)
			}
		})
	}
}

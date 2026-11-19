package scan

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestRescanTrashedImageDoesNotSuppressMissingFiles(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			dataDir := storetest.MigratedDataDir(t)
			st := storetest.OpenAt(t, dataDir)
			libraryID, identifier := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassVideo})
			kept := writeScanFile(t, root, "kept.mp4", []byte("kept"))
			removed := writeScanFile(t, root, "removed.mp4", []byte("removed"))
			scanImageRuleLibrary(t, st, identifier, libraryID, root, workers)
			image := seedImageRow(t, st, libraryID, writeScanFile(t, root, "existing.jpg", jpegBytes("existing")))
			filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{libraryID}}
			if err := st.TrashMedia(ctx, image, filter); err != nil {
				t.Fatal(err)
			}
			// Reopen the store to exercise persisted trash state, not cached state.
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st = storetest.OpenAt(t, dataDir)
			if got := libraryFilePaths(t, st, libraryID); !slices.Equal(got, []string{kept, removed}) {
				t.Fatalf("seeded live files = %v", got)
			}
			if err := os.Remove(removed); err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Millisecond)
			result, err := ScanWithWorkerGovernor(ctx, st, nil, nil, nil, nil, identifier, nil, nil, fixedGovernor(workers), Request{LibraryID: libraryID, Root: root})
			if err != nil || result.FilesMissing != 1 {
				t.Fatalf("rescan FilesMissing = %d, err = %v; want 1 missing", result.FilesMissing, err)
			}
			if got := libraryFilePaths(t, st, libraryID); !slices.Equal(got, []string{kept}) {
				t.Fatalf("live files after rescan = %v; want only %q", got, kept)
			}
			trashed, err := st.TrashedMediaList(ctx, filter)
			if err != nil || len(trashed) != 1 || trashed[0].ID != image {
				t.Fatalf("trashed image changed: %+v, %v", trashed, err)
			}
		})
	}
}

func TestRescanUndeclaredMediaStillValidatesRoot(t *testing.T) {
	for _, workers := range []int{1, 4} {
		for _, failure := range []string{"removed", "not a directory", "permission denied"} {
			t.Run(fmt.Sprintf("workers=%d/%s", workers, failure), func(t *testing.T) {
				ctx := context.Background()
				root := t.TempDir()
				st := storetest.Open(t)
				libraryID, identifier := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassVideo})
				path := writeScanFile(t, root, "existing.jpg", jpegBytes("existing"))
				image := seedImageRow(t, st, libraryID, path)
				scanImageRuleLibrary(t, st, identifier, libraryID, root, workers)
				undeclared, err := st.LibraryHasUndeclaredMedia(ctx, libraryID, []store.MediaClass{store.MediaClassVideo})
				if err != nil || !undeclared {
					t.Fatalf("retention guard fixture = %v, %v; want undeclared Media", undeclared, err)
				}
				wantErr := "is unavailable"
				if failure == "permission denied" {
					if os.Geteuid() == 0 {
						t.Skip("root ignores directory permissions")
					}
					if err := os.Chmod(root, 0); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(root, 0o700) }) //nolint:gosec // G302: restore owner access for TempDir cleanup.
					wantErr = "cannot be read"
				} else {
					if err := os.RemoveAll(root); err != nil {
						t.Fatal(err)
					}
					if failure == "not a directory" {
						if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
							t.Fatal(err)
						}
						wantErr = "is not a directory"
					}
				}
				_, err = ScanWithWorkerGovernor(ctx, st, nil, nil, nil, nil, identifier, nil, nil, fixedGovernor(workers), Request{LibraryID: libraryID, Root: root})
				if err == nil || !strings.Contains(err.Error(), wantErr) {
					t.Errorf("scan error = %v; want %q", err, wantErr)
				}
				files, err := st.MediaFilesForMedia(ctx, image, store.LibraryFilter{AllowedIDs: []store.LibraryID{libraryID}})
				if err != nil || len(files) != 1 || !files[0].MissingAt.IsZero() {
					t.Fatalf("unavailable root retired Media: %+v, %v", files, err)
				}
			})
		}
	}
}

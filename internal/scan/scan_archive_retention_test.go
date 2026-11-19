package scan

import (
	"context"
	"fmt"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	familyarchive "github.com/beamfall/plugin-familyarchive"
)

// CORE-2364 restores recoverable, non-destructive rescan semantics for photos
// in mixed archives. Use the shipped bundle so a missing class cannot hide
// behind a fixture that independently declares image support.
func TestRescanPreservesArchivePhotos(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			photo := writeScanFile(t, root, "Trip/portrait.jpg", jpegBytes("archive-photo"))
			writeScanFile(t, root, "Trip/clip.mp4", []byte("archive-video"))
			st := storetest.Open(t)
			bundle := familyarchive.Bundle()
			library, err := st.CreateLibrary(ctx, "Archive", false, string(bundle.ID), "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, err := store.HashMediaFileContent(photo)
			if err != nil {
				t.Fatal(err)
			}
			original, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{LibraryID: library.ID, Path: photo, Title: "Portrait", Container: "jpg", Class: store.MediaClassImage, ContentFingerprint: fingerprint})
			if err != nil {
				t.Fatal(err)
			}
			classes := make([]store.MediaClass, len(bundle.MediaClasses))
			for i, class := range bundle.MediaClasses {
				classes[i] = store.MediaClass(class)
			}
			identifier := &classDeclaringIdentifier{classes: map[string][]store.MediaClass{string(bundle.ID): classes}}
			for range 2 {
				if _, err := ScanWithWorkerGovernor(ctx, st, nil, nil, nil, nil, identifier, nil, nil, fixedGovernor(workers), Request{LibraryID: library.ID, Root: root}, nil); err != nil {
					t.Fatal(err)
				}
				files, err := st.MediaFilesForLibrary(ctx, library.ID)
				if err != nil {
					t.Fatal(err)
				}
				retained := false
				for _, file := range files {
					if file.Path == photo && file.MediaID == original.ID {
						retained = true
					}
				}
				if !retained {
					t.Fatalf("rescan retired archive photo %s; live files: %+v", original.ID, files)
				}
			}
		})
	}
}

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CORE-1831: a scan import landing between ConvertManagedFile's on-disk
// admission of the converted output and its managed catalog admission — the
// window CORE-1089 closed for ImportManagedFile. The convertStepHook "import"
// fence fires inside that window, so the scan-shaped import runs at exactly
// that point.
//
// Before the fix the scan cataloged the output as an ordinary item, and a
// conversion whose output bytes were already cataloged at another path
// SUCCEEDED: the scan repointed the existing row to the output path, and the
// managed admission then found its own path and passed the duplicate refusal.

// liveRowsAt counts the live media_file rows in library-local naming path.
func liveRowsAt(t *testing.T, st *Store, path string) []MediaFile {
	t.Helper()
	files, err := st.MediaFilesForLibrary(context.Background(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	var rows []MediaFile
	for _, file := range files {
		if file.Path == path {
			rows = append(rows, file)
		}
	}
	return rows
}

func TestConvertScanInWindowSkipsAndManagedAdmissionCatalogs(t *testing.T) {
	ctx := context.Background()
	fx := newInternalConvertFixture(t)
	lease := fx.claimLease(t, "worker-a")
	scanErr := errors.New("scan hook did not run")
	fx.st.convertStepHook = func(step string) {
		if step == "import" {
			scanErr = scanImport(ctx, fx.st, fx.dst)
		}
	}
	result, err := fx.st.ConvertManagedFile(ctx, "library-local", fx.src, "h264-mp4", &internalConverter{}, fx.filter, lease, time.Now().UTC())
	fx.st.convertStepHook = nil
	if err != nil {
		t.Fatalf("ConvertManagedFile: %v", err)
	}
	if !errors.Is(scanErr, ErrManagedAdmissionInFlight) {
		t.Fatalf("scan import in the conversion admission window err = %v, want ErrManagedAdmissionInFlight", scanErr)
	}
	rows := liveRowsAt(t, fx.st, fx.dst)
	if len(rows) != 1 {
		t.Fatalf("live rows at the converted output = %#v, want exactly one", rows)
	}
	if rows[0].MediaID != result.Converted.ID {
		t.Fatalf("output row media = %q, want the managed admission's %q", rows[0].MediaID, result.Converted.ID)
	}
	media, err := fx.st.Media(ctx, result.Converted.ID, fx.filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "clip" {
		t.Fatalf("output media title = %q, want the managed record's %q", media.Title, "clip")
	}
	// The reservation ends with the admission: a later scan re-imports the
	// now-cataloged output as usual.
	if err := scanImport(ctx, fx.st, fx.dst); err != nil {
		t.Fatalf("scan import after the managed admission: %v", err)
	}
}

func TestConvertScanInWindowCannotBypassDuplicateRefusal(t *testing.T) {
	ctx := context.Background()
	fx := newInternalConvertFixture(t)
	// internalConverter writes "converted:" + the source bytes, so these are
	// exactly the bytes the conversion will produce, already cataloged.
	other := filepath.Join(fx.root, "other.mp4")
	if err := os.WriteFile(other, []byte("converted:clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanImport(ctx, fx.st, other); err != nil {
		t.Fatalf("seed scan import: %v", err)
	}
	before := liveRowsAt(t, fx.st, other)
	if len(before) != 1 {
		t.Fatalf("seeded rows at other.mp4 = %#v, want one", before)
	}

	lease := fx.claimLease(t, "worker-a")
	scanErr := errors.New("scan hook did not run")
	fx.st.convertStepHook = func(step string) {
		if step == "import" {
			scanErr = scanImport(ctx, fx.st, fx.dst)
		}
	}
	_, err := fx.st.ConvertManagedFile(ctx, "library-local", fx.src, "h264-mp4", &internalConverter{}, fx.filter, lease, time.Now().UTC())
	fx.st.convertStepHook = nil

	if !errors.Is(scanErr, ErrManagedAdmissionInFlight) {
		t.Fatalf("scan import in the conversion admission window err = %v, want ErrManagedAdmissionInFlight", scanErr)
	}
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("conversion to already cataloged content err = %v, want ValidationError", err)
	}
	if _, statErr := os.Lstat(fx.dst); !os.IsNotExist(statErr) {
		t.Fatalf("refused conversion left %s on disk: %v", fx.dst, statErr)
	}
	if rows := liveRowsAt(t, fx.st, fx.dst); len(rows) != 0 {
		t.Fatalf("catalog has rows at the refused output: %#v", rows)
	}
	after := liveRowsAt(t, fx.st, other)
	if len(after) != 1 || after[0].MediaID != before[0].MediaID || after[0].ContentFingerprint != before[0].ContentFingerprint {
		t.Fatalf("existing row at other.mp4 = %#v, want unchanged %#v", after, before)
	}
	if _, statErr := os.Stat(fx.src); statErr != nil {
		t.Fatalf("refused conversion disturbed the original: %v", statErr)
	}
}

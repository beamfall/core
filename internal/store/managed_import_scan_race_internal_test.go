package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CORE-1089: a scan import landing between ImportManagedFile's on-disk
// admission and its managed catalog admission. The managedImportStepHook runs
// the scan-shaped import at that exact point, which wall-clock scheduling
// cannot hit deterministically.
//
// Before the fix the interleaving reproduced both suspected defects: a managed
// import of content already cataloged at another path SUCCEEDED (the scan
// import repointed the existing row to the new path, then the managed
// admission found its own path and passed the duplicate refusal), and a managed
// admission that failed after the scan cataloged the file deleted that
// cataloged file in its cleanup.

type managedScanRaceFixture struct {
	st     *Store
	root   string
	filter LibraryFilter
}

func newManagedScanRaceFixture(t *testing.T) managedScanRaceFixture {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	return managedScanRaceFixture{st: st, root: canonical, filter: LibraryFilter{AllLibraries: true}}
}

// token registers an out-of-Library source holding payload.
func (fx managedScanRaceFixture) token(t *testing.T, name, payload string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, name)
	if err := os.WriteFile(src, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := fx.st.RegisterImportSource(src)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (fx managedScanRaceFixture) importManaged(ctx context.Context, token string) (ManagedPath, error) {
	return fx.st.ImportManagedFile(ctx, "library-local", token, fx.root, "mkv", "h264", MediaClassVideo, fx.filter)
}

// scanImport is what a scan does with a file it finds: hash the bytes and
// import them through the ordinary (non-managed) path.
func scanImport(ctx context.Context, st *Store, path string) error {
	fingerprint, err := SampleMediaFileContent(ctx, path)
	if err != nil {
		return err
	}
	_, _, err = st.ImportMediaFile(ctx, scanShapedImport(path, fingerprint))
	return err
}

func scanShapedImport(path, fingerprint string) ImportMediaFile {
	return ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "scanned",
		Container:          "mkv",
		Codec:              "h264",
		Class:              MediaClassVideo,
		ContentFingerprint: fingerprint,
	}
}

// liveRows maps each live media_file path in library-local to its row.
func (fx managedScanRaceFixture) liveRows(t *testing.T) map[string]MediaFile {
	t.Helper()
	files, err := fx.st.MediaFilesForLibrary(context.Background(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]MediaFile{}
	for _, file := range files {
		rows[file.Path] = file
	}
	return rows
}

func TestManagedImportScanInWindowCannotBypassDuplicateRefusal(t *testing.T) {
	ctx := context.Background()
	fx := newManagedScanRaceFixture(t)
	first := filepath.Join(fx.root, "first.mkv")
	if err := os.WriteFile(first, []byte("shared-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanImport(ctx, fx.st, first); err != nil {
		t.Fatalf("seed scan import: %v", err)
	}
	before := fx.liveRows(t)[first]

	second := filepath.Join(fx.root, "second.mkv")
	var scanErr error
	fx.st.managedImportStepHook = func(step string) {
		if step == "catalog" {
			scanErr = scanImport(ctx, fx.st, second)
		}
	}
	_, err := fx.importManaged(ctx, fx.token(t, "second.mkv", "shared-bytes"))
	fx.st.managedImportStepHook = nil

	if !errors.Is(scanErr, ErrManagedAdmissionInFlight) {
		t.Fatalf("scan import in the admission window err = %v, want ErrManagedAdmissionInFlight", scanErr)
	}
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("managed import of already cataloged content err = %v, want ValidationError", err)
	}
	if _, statErr := os.Lstat(second); !os.IsNotExist(statErr) {
		t.Fatalf("refused managed import left %s on disk: %v", second, statErr)
	}
	rows := fx.liveRows(t)
	if got, ok := rows[first]; !ok || got.MediaID != before.MediaID || got.ContentFingerprint != before.ContentFingerprint {
		t.Fatalf("existing row at first.mkv = %#v (present %v), want unchanged %#v", got, ok, before)
	}
	if _, ok := rows[second]; ok {
		t.Fatalf("catalog has a row at the refused path %s: %#v", second, rows)
	}
}

func TestManagedImportScanInWindowSkipsAndManagedAdmissionCatalogs(t *testing.T) {
	ctx := context.Background()
	fx := newManagedScanRaceFixture(t)
	target := filepath.Join(fx.root, "fresh.mkv")
	scanErrs := map[string]error{}
	fx.st.managedImportStepHook = func(step string) {
		if step != "catalog" {
			return
		}
		fingerprint, err := SampleMediaFileContent(ctx, target)
		if err != nil {
			t.Errorf("hash admitted file: %v", err)
			return
		}
		imported := scanShapedImport(target, fingerprint)
		_, _, scanErrs["file"] = fx.st.ImportMediaFile(ctx, imported)
		_, _, scanErrs["parts"] = fx.st.ImportMediaFileParts(ctx, []ImportMediaFilePart{{File: imported, SourceOrdinal: 1}})
		_, scanErrs["version"] = fx.st.ImportMediaFileVersion(ctx, MediaVersionImport{MediaID: "media-any", File: imported, Label: "alt"})
	}
	imported, err := fx.importManaged(ctx, fx.token(t, "fresh.mkv", "fresh-bytes"))
	fx.st.managedImportStepHook = nil
	if err != nil {
		t.Fatalf("ImportManagedFile: %v", err)
	}
	for _, entry := range []string{"file", "parts", "version"} {
		if !errors.Is(scanErrs[entry], ErrManagedAdmissionInFlight) {
			t.Fatalf("scan %s import in the admission window err = %v, want ErrManagedAdmissionInFlight", entry, scanErrs[entry])
		}
	}
	if imported.Path != target {
		t.Fatalf("imported path = %q, want %q", imported.Path, target)
	}
	if _, ok := fx.liveRows(t)[target]; !ok {
		t.Fatalf("managed admission did not catalog %s", target)
	}
	// The reservation ends with the admission: a later scan re-imports the
	// now-cataloged file as usual.
	if err := scanImport(ctx, fx.st, target); err != nil {
		t.Fatalf("scan import after the managed admission: %v", err)
	}
}

func TestManagedImportFailureCleanupKeepsFileAnotherAdmissionCataloged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fx := newManagedScanRaceFixture(t)
	target := filepath.Join(fx.root, "contested.mkv")
	fx.st.managedImportStepHook = func(step string) {
		if step != "catalog" {
			return
		}
		// An admission the in-flight guard does not cover catalogs the file
		// (written straight through the catalog SQL), then the managed
		// admission fails.
		fingerprint, err := SampleMediaFileContent(ctx, target)
		if err != nil {
			t.Errorf("hash admitted file: %v", err)
			return
		}
		plane, err := fx.st.catalogPlaneForLibraryLocked(context.Background(), "library-local")
		if err != nil {
			t.Errorf("catalog plane: %v", err)
			return
		}
		if _, _, _, err := fx.st.importMediaFileSQL(context.Background(), plane, scanShapedImport(target, fingerprint), time.Now().UTC()); err != nil {
			t.Errorf("foreign admission: %v", err)
		}
		cancel()
	}
	_, err := fx.importManaged(ctx, fx.token(t, "contested.mkv", "contested-bytes"))
	fx.st.managedImportStepHook = nil
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("managed admission err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Lstat(target); statErr != nil {
		t.Fatalf("failure cleanup removed a file another admission cataloged: %v", statErr)
	}
	if _, ok := fx.liveRows(t)[target]; !ok {
		t.Fatalf("foreign admission row at %s is gone", target)
	}
}

func TestManagedImportFailureCleanupRemovesUncatalogedFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fx := newManagedScanRaceFixture(t)
	target := filepath.Join(fx.root, "orphan.mkv")
	fx.st.managedImportStepHook = func(step string) {
		if step == "catalog" {
			cancel()
		}
	}
	_, err := fx.importManaged(ctx, fx.token(t, "orphan.mkv", "orphan-bytes"))
	fx.st.managedImportStepHook = nil
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("managed admission err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("failed managed admission left an uncataloged file: %v", statErr)
	}
	if _, ok := fx.liveRows(t)[target]; ok {
		t.Fatalf("failed managed admission left a catalog row at %s", target)
	}
}

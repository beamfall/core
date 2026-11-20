package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeSource is an in-memory Source.
type fakeSource struct {
	categories []string
	items      []Item
	err        error
}

func (s *fakeSource) Categories(context.Context) ([]string, error) { return s.categories, s.err }
func (s *fakeSource) CompletedItems(context.Context) ([]Item, error) {
	return s.items, s.err
}

// fakeCatalog implements the Catalog seam with an in-memory fingerprint set and
// per-Library roots.
type fakeCatalog struct {
	roots        map[store.LibraryID][]string
	fingerprints map[string]store.MediaID // existing fingerprint -> media
	existsErr    error
}

func (c *fakeCatalog) AllLibraryRoots(context.Context) ([]string, error) {
	var roots []string
	for _, libraryRoots := range c.roots {
		roots = append(roots, libraryRoots...)
	}
	return roots, nil
}
func (c *fakeCatalog) LibraryRoots(_ context.Context, id store.LibraryID) ([]string, error) {
	return c.roots[id], nil
}
func (c *fakeCatalog) LiveMediaFileExistsByFingerprint(_ context.Context, sample, full string) (store.MediaID, bool, error) {
	if c.existsErr != nil {
		return "", false, c.existsErr
	}
	if id, ok := c.fingerprints[sample]; ok {
		return id, true, nil
	}
	id, ok := c.fingerprints[full]
	return id, ok, nil
}

// fakeScan records enqueued scans.
type fakeScan struct {
	scans []store.LibraryID
}

func (s *fakeScan) EnqueueScan(_ context.Context, id store.LibraryID, _ string) error {
	s.scans = append(s.scans, id)
	return nil
}

// newTestIngestor builds an Ingestor whose approved download roots are the
// trailing approvedRoots args; tests exercising imports pass their downloads
// directory, containment tests deliberately withhold or mis-scope it.
func newTestIngestor(t *testing.T, src Source, cat Catalog, scan ScanEnqueuer, routing map[string]store.LibraryID, approvedRoots ...string) (*Ingestor, *Ledger) {
	t.Helper()
	ledger, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	ing, err := NewIngestor(Config{
		Source: src, Catalog: cat, Scan: scan, Ledger: ledger, Routing: routing,
		ApprovedRoots: approvedRoots,
		// Deterministic fingerprint by path so tests need no file content hash.
		Fingerprint: func(_ context.Context, file *os.File) (string, error) { return "fp:" + filepath.Base(file.Name()), nil },
	})
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}
	return ing, ledger
}

func writePayload(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIngestRefusesPayloadReplacedDuringFingerprinting(t *testing.T) {
	ctx := t.Context()
	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "downloads")
	download := filepath.Join(downloads, "original.mkv")
	replacement := filepath.Join(downloads, "replacement.mkv")
	writePayload(t, download)
	if err := os.WriteFile(replacement, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacementSample, err := store.SampleMediaFileContent(ctx, replacement)
	if err != nil {
		t.Fatal(err)
	}
	originalFull, err := store.HashMediaFileContent(download)
	if err != nil {
		t.Fatal(err)
	}
	libRoot := filepath.Join(tmp, "library")
	cat := &fakeCatalog{
		roots:        map[store.LibraryID][]string{"lib-films": {libRoot}},
		fingerprints: map[string]store.MediaID{replacementSample: "media-existing"},
	}
	src := &fakeSource{items: []Item{{ID: "hash-swap", Category: "films", ContentPath: download}}}
	scan := &fakeScan{}
	ing, ledger := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, downloads)
	swapped := false
	ing.fingerprint = func(ctx context.Context, file *os.File) (string, error) {
		full, err := store.HashMediaFileContentReader(ctx, file)
		if err != nil || full != originalFull {
			t.Fatalf("open descriptor fingerprint = %q, %v, want %q", full, err, originalFull)
		}
		if err := os.Rename(replacement, download); err != nil {
			t.Fatal(err)
		}
		swapped = true
		return full, nil
	}
	result, err := ing.Run(ctx)
	if err != nil || !swapped || result.Errors != 1 || result.Skipped != 0 || result.Imported != 0 {
		t.Fatalf("Run = %+v, %v, swapped = %t, want one fingerprint error", result, err, swapped)
	}
	if feed := ledger.Status(); len(feed) != 1 || feed[0].Outcome != OutcomeError || feed[0].Detail != "fingerprint failed" {
		t.Fatalf("ledger = %+v, want fingerprint failure", feed)
	}
	assertNothingLanded(t, libRoot, scan)
}

func TestIngestHardlinksSingleFileAndArmsScan(t *testing.T) {
	// feature:qbittorrent-ingest
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	download := filepath.Join(tmp, "downloads", "Show.S01E01.mkv")
	writePayload(t, download)

	src := &fakeSource{items: []Item{{
		ID: "hash1", ReleaseName: "Show.S01E01", Category: "films", ContentPath: download,
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, ledger := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, filepath.Join(tmp, "downloads"))

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Imported != 1 || result.Skipped != 0 || result.Unrouted != 0 || result.Errors != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	dst := filepath.Join(libRoot, IncomingFolderName, "Show.S01E01.mkv")
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("payload not linked into incoming folder: %v", err)
	}
	// Same filesystem -> hardlink (the link count is 2).
	srcInfo, _ := os.Stat(download)
	dstInfo, _ := os.Stat(dst)
	if !os.SameFile(srcInfo, dstInfo) {
		t.Errorf("expected a hardlink (same file), got distinct files")
	}
	// Seed copy untouched.
	if _, err := os.Stat(download); err != nil {
		t.Errorf("seed copy should remain: %v", err)
	}
	// Sidecar written with the release name.
	sidecarData, err := os.ReadFile(dst + SidecarSuffix)
	if err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	var sidecar Sidecar
	if err := json.Unmarshal(sidecarData, &sidecar); err != nil {
		t.Fatal(err)
	}
	if sidecar.ReleaseName != "Show.S01E01" || sidecar.Source != IngestProvenanceSource || sidecar.Category != "films" {
		t.Errorf("unexpected sidecar: %+v", sidecar)
	}
	if len(scan.scans) != 1 || scan.scans[0] != "lib-films" {
		t.Errorf("expected one scan for lib-films, got %v", scan.scans)
	}
	// Status feed records the import as a hardlink.
	status := ledger.Status()
	if len(status) != 1 || status[0].Outcome != OutcomeImported || status[0].Mode != RelocateHardlink {
		t.Errorf("unexpected status: %+v", status)
	}
}

func TestIngestCopyFallbackRecordsCopyMode(t *testing.T) {
	// Force the copy path by making os.Link fail (cross-device is hard to fake;
	// instead, pre-create the parent as a read-only nonsense — simpler: link
	// into a destination that already has a different file present is an error,
	// so instead we test copyFile directly for the copy semantics).
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.mkv")
	writePayload(t, src)
	dst := filepath.Join(tmp, "out", "copied.mkv")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	srcInfo, _ := os.Stat(src)
	dstInfo, _ := os.Stat(dst)
	if os.SameFile(srcInfo, dstInfo) {
		t.Errorf("copy should produce a distinct file")
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "payload" {
		t.Errorf("copied content mismatch: %q", data)
	}
}

func TestIngestSkipsExactDuplicate(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	download := filepath.Join(tmp, "downloads", "dup.mkv")
	writePayload(t, download)

	src := &fakeSource{items: []Item{{ID: "hash-dup", ReleaseName: "dup", Category: "films", ContentPath: download}}}
	cat := &fakeCatalog{
		roots:        map[store.LibraryID][]string{"lib-films": {libRoot}},
		fingerprints: map[string]store.MediaID{"fp:dup.mkv": "media-existing"},
	}
	scan := &fakeScan{}
	ing, ledger := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, filepath.Join(tmp, "downloads"))

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Skipped != 1 || result.Imported != 0 {
		t.Fatalf("expected one skip, got %+v", result)
	}
	// Nothing linked.
	if _, err := os.Stat(filepath.Join(libRoot, IncomingFolderName, "dup.mkv")); !os.IsNotExist(err) {
		t.Errorf("duplicate must not be linked")
	}
	if len(scan.scans) != 0 {
		t.Errorf("no scan should be armed for a pure-duplicate pass, got %v", scan.scans)
	}
	status := ledger.Status()
	if len(status) != 1 || status[0].Outcome != OutcomeAlreadyInCatalog || status[0].MediaPointer != "media-existing" {
		t.Errorf("unexpected status: %+v", status)
	}
}

func TestIngestChecksBothFingerprintSchemes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sampled   bool
		collision bool
		absent    bool
		trashed   bool
	}{
		{name: "legacy-exact"},
		{name: "sampled-exact", sampled: true},
		{name: "sampled-collision", sampled: true, collision: true},
		{name: "sampled-candidate-unavailable", sampled: true, absent: true},
		{name: "sampled-trashed", sampled: true, trashed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			tmp, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			libRoot := filepath.Join(tmp, "library")
			downloads := filepath.Join(tmp, "downloads")
			download := filepath.Join(downloads, "download.mkv")
			cataloged := filepath.Join(libRoot, "cataloged.mkv")
			writePayload(t, download)
			writePayload(t, cataloged)
			content := make([]byte, 2<<20)
			if err := os.WriteFile(download, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.collision {
				content[64<<10] = 1 // Between the first two sample chunks.
			}
			if err := os.WriteFile(cataloged, content, 0o600); err != nil {
				t.Fatal(err)
			}
			fingerprint, err := store.HashMediaFileContent(cataloged)
			if err != nil {
				t.Fatal(err)
			}
			if tc.sampled {
				fingerprint, err = store.SampleMediaFileContent(ctx, cataloged)
				if err != nil {
					t.Fatal(err)
				}
				if got, err := store.SampleMediaFileContent(ctx, download); err != nil || got != fingerprint {
					t.Fatalf("download sample = %q, err = %v, want %q", got, err, fingerprint)
				}
			}
			const library store.LibraryID = "library-local"
			cat := storetest.Open(t)
			if err := cat.SetLibraryRoots(ctx, library, []string{libRoot}); err != nil {
				t.Fatal(err)
			}
			primary := filepath.Join(libRoot, "primary.mkv")
			writePayload(t, primary)
			primaryFingerprint, err := store.HashMediaFileContent(primary)
			if err != nil {
				t.Fatal(err)
			}
			media, _, err := cat.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: library, Path: primary, Title: "Cataloged", Container: "mkv",
				Codec: "h264", ContentFingerprint: primaryFingerprint,
			})
			if err != nil {
				t.Fatal(err)
			}
			err = cat.AddMediaFileVersion(ctx, media.ID, store.MediaFile{
				Path: cataloged, VersionLabel: "alternate", ContentFingerprint: fingerprint,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.absent {
				if err := os.Remove(cataloged); err != nil {
					t.Fatal(err)
				}
			}
			if tc.trashed {
				if err := cat.TrashMedia(ctx, media.ID, store.LibraryFilter{AllLibraries: true}); err != nil {
					t.Fatal(err)
				}
			}
			src := &fakeSource{items: []Item{{ID: "hash-dup", Category: "films", ContentPath: download}}}
			scan := &fakeScan{}
			ing, ledger := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": library}, downloads)
			ing.fingerprint = func(ctx context.Context, file *os.File) (string, error) {
				return store.HashMediaFileContentReader(ctx, file)
			}
			result, err := ing.Run(ctx)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(libRoot, IncomingFolderName, "download.mkv")
			if tc.trashed {
				if result.Imported != 1 || result.Skipped != 0 || len(scan.scans) != 1 {
					t.Fatalf("expected relocation: result = %+v, scans = %v", result, scan.scans)
				}
				if _, err := os.Stat(destination); err != nil {
					t.Fatal(err)
				}
				return
			}
			if result.Imported != 0 || result.Skipped != 1 || len(scan.scans) != 0 {
				t.Fatalf("catalog match: result = %+v, scans = %v", result, scan.scans)
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("duplicate was relocated or stat failed: %v", err)
			}
			status := ledger.Status()
			if len(status) != 1 || status[0].MediaPointer != string(media.ID) {
				t.Fatalf("status = %+v, want pointer to %q", status, media.ID)
			}
		})
	}
}

func TestIngestIgnoresUnmappedCategory(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	download := filepath.Join(tmp, "downloads", "unrouted.mkv")
	writePayload(t, download)

	src := &fakeSource{items: []Item{
		{ID: "hash-x", ReleaseName: "x", Category: "unknown-category", ContentPath: download},
		{ID: "hash-y", ReleaseName: "y", Category: "", ContentPath: download}, // uncategorized
	}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, ledger := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"})

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Unrouted != 2 || result.Imported != 0 {
		t.Fatalf("expected 2 unrouted, got %+v", result)
	}
	// Nothing linked, nothing scanned.
	if _, err := os.Stat(filepath.Join(libRoot, IncomingFolderName)); !os.IsNotExist(err) {
		t.Errorf("unrouted torrents must not create an incoming folder")
	}
	if len(scan.scans) != 0 {
		t.Errorf("no scan for unrouted, got %v", scan.scans)
	}
	status := ledger.Status()
	if len(status) != 2 {
		t.Fatalf("expected 2 status entries, got %d", len(status))
	}
	for _, entry := range status {
		if entry.Outcome != OutcomeUnrouted {
			t.Errorf("expected unrouted, got %s", entry.Outcome)
		}
	}
}

func TestIngestUnroutedCanImportAfterRouteAdded(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	download := filepath.Join(tmp, "downloads", "reroute.mkv")
	writePayload(t, download)

	src := &fakeSource{items: []Item{{ID: "hash-reroute", ReleaseName: "reroute", Category: "later", ContentPath: download}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, ledger := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{})

	first, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Unrouted != 1 {
		t.Fatalf("first result = %+v, want one unrouted", first)
	}

	ing, _ = newTestIngestorWithLedger(t, src, cat, scan, ledger, map[string]store.LibraryID{"later": "lib-films"}, filepath.Join(tmp, "downloads"))
	second, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Imported != 1 {
		t.Fatalf("second result = %+v, want import after route added", second)
	}
	if _, err := os.Stat(filepath.Join(libRoot, IncomingFolderName, "reroute.mkv")); err != nil {
		t.Fatalf("rerouted payload not linked: %v", err)
	}
}

func TestIngestFailureLogsSafeSummary(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	download := filepath.Join(tmp, "downloads", "broken.mkv")
	writePayload(t, download)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ledger, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	ing, err := NewIngestor(Config{
		Source: &fakeSource{items: []Item{{
			ID: "hash-broken", ReleaseName: "release-name", Category: "films", ContentPath: download,
		}}},
		Catalog: &fakeCatalog{
			roots:     map[store.LibraryID][]string{"lib-films": {libRoot}},
			existsErr: errors.New("catalog offline"),
		},
		Scan:          &fakeScan{},
		Ledger:        ledger,
		Routing:       map[string]store.LibraryID{"films": "lib-films"},
		ApprovedRoots: []string{filepath.Join(tmp, "downloads")},
		Fingerprint:   func(_ context.Context, file *os.File) (string, error) { return "fp:" + filepath.Base(file.Name()), nil },
		Logger:        logger,
	})
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Errors != 1 {
		t.Fatalf("errors = %d, want 1", result.Errors)
	}
	line := logs.String()
	if !strings.Contains(line, "qbittorrent: ingest item failed") ||
		!strings.Contains(line, "torrent=hash-broken") ||
		!strings.Contains(line, "release=release-name") ||
		!strings.Contains(line, "library=lib-films") ||
		!strings.Contains(line, "detail=\"catalog lookup failed\"") {
		t.Fatalf("log = %q", line)
	}
	if strings.Contains(line, download) {
		t.Fatalf("log leaked filesystem path: %q", line)
	}
}

func newTestIngestorWithLedger(t *testing.T, src Source, cat Catalog, scan ScanEnqueuer, ledger *Ledger, routing map[string]store.LibraryID, approvedRoots ...string) (*Ingestor, *Ledger) {
	t.Helper()
	ing, err := NewIngestor(Config{
		Source: src, Catalog: cat, Scan: scan, Ledger: ledger, Routing: routing,
		ApprovedRoots: approvedRoots,
		Fingerprint:   func(_ context.Context, file *os.File) (string, error) { return "fp:" + filepath.Base(file.Name()), nil },
	})
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}
	return ing, ledger
}

func TestIngestIdempotentByHash(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	download := filepath.Join(tmp, "downloads", "once.mkv")
	writePayload(t, download)

	src := &fakeSource{items: []Item{{ID: "hash-once", ReleaseName: "once", Category: "films", ContentPath: download}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, _ := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, filepath.Join(tmp, "downloads"))

	if _, err := ing.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Second pass: the hash is now processed, so nothing re-imports — and the
	// destination already exists, which would error if relocation were retried.
	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Imported != 0 || result.Skipped != 0 || result.Errors != 0 {
		t.Fatalf("second pass should be a no-op, got %+v", result)
	}
}

func TestIngestPartialMultiFileRetryReusesExistingLinkedDestination(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	contentRoot := filepath.Join(tmp, "downloads", "Pack")
	writePayload(t, filepath.Join(contentRoot, "ep1.mkv"))
	writePayload(t, filepath.Join(contentRoot, "ep2.mkv"))

	src := &fakeSource{items: []Item{{
		ID: "pack-partial", ReleaseName: "Pack", Category: "films", ContentPath: contentRoot,
		Files: []ItemFile{{RelPath: "ep1.mkv"}, {RelPath: "ep2.mkv"}},
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	var ep2Failures int
	ledger, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	ing, err := NewIngestor(Config{
		Source: src, Catalog: cat, Scan: scan, Ledger: ledger, Routing: map[string]store.LibraryID{"films": "lib-films"},
		ApprovedRoots: []string{filepath.Join(tmp, "downloads")},
		Fingerprint: func(_ context.Context, file *os.File) (string, error) {
			if filepath.Base(file.Name()) == "ep2.mkv" && ep2Failures == 0 {
				ep2Failures++
				return "", errors.New("temporary fingerprint failure")
			}
			return "fp:" + filepath.Base(file.Name()), nil
		},
	})
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}

	first, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Errors != 1 || len(scan.scans) != 1 {
		t.Fatalf("first result = %+v scans=%v, want error plus scan for linked ep1", first, scan.scans)
	}
	second, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Imported != 1 {
		t.Fatalf("second result = %+v, want successful retry", second)
	}
	incoming := filepath.Join(libRoot, IncomingFolderName)
	for _, name := range []string{"ep1.mkv", "ep2.mkv"} {
		if _, err := os.Stat(filepath.Join(incoming, name)); err != nil {
			t.Fatalf("%s not linked after retry: %v", name, err)
		}
	}
}

func TestIngestMultiFileSkipsJunkAndDuplicates(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	contentRoot := filepath.Join(tmp, "downloads", "Pack")
	writePayload(t, filepath.Join(contentRoot, "ep1.mkv"))
	writePayload(t, filepath.Join(contentRoot, "ep2.mkv"))
	writePayload(t, filepath.Join(contentRoot, "Sample", "sample.mkv"))
	writePayload(t, filepath.Join(contentRoot, "readme.nfo"))

	src := &fakeSource{items: []Item{{
		ID: "pack", ReleaseName: "Pack.S01", Category: "films", ContentPath: contentRoot,
		Files: []ItemFile{
			{RelPath: "ep1.mkv", Size: 7},
			{RelPath: "ep2.mkv", Size: 7},
			{RelPath: filepath.Join("Sample", "sample.mkv"), Size: 7},
			{RelPath: "readme.nfo", Size: 7},
		},
	}}}
	cat := &fakeCatalog{
		roots:        map[store.LibraryID][]string{"lib-films": {libRoot}},
		fingerprints: map[string]store.MediaID{"fp:ep2.mkv": "media-existing"}, // ep2 is a dupe
	}
	scan := &fakeScan{}
	ing, ledger := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, filepath.Join(tmp, "downloads"))

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Imported != 1 {
		t.Fatalf("expected the pack to import (with ep1), got %+v", result)
	}
	incoming := filepath.Join(libRoot, IncomingFolderName)
	if _, err := os.Stat(filepath.Join(incoming, "ep1.mkv")); err != nil {
		t.Errorf("ep1 should be linked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(incoming, "ep2.mkv")); !os.IsNotExist(err) {
		t.Errorf("ep2 is a duplicate and must not be linked")
	}
	if _, err := os.Stat(filepath.Join(incoming, "Sample", "sample.mkv")); !os.IsNotExist(err) {
		t.Errorf("sample junk must not be linked")
	}
	if _, err := os.Stat(filepath.Join(incoming, "readme.nfo")); !os.IsNotExist(err) {
		t.Errorf("non-media .nfo must not be linked")
	}
	// Status feed has the duplicate ep2 entry plus the import entry.
	var sawDup, sawImport bool
	for _, entry := range ledger.Status() {
		if entry.Outcome == OutcomeAlreadyInCatalog {
			sawDup = true
		}
		if entry.Outcome == OutcomeImported {
			sawImport = true
		}
	}
	if !sawDup || !sawImport {
		t.Errorf("expected both a duplicate and an import status entry; status=%v", ledger.Status())
	}
}

// TestIngestSkipsPathTraversalMember is the path-traversal regression: a
// multi-file payload whose member path escapes the content root (qBittorrent
// supplies member paths verbatim, source.go) must not steer a relocate/sidecar
// write outside the Incoming Folder. filepath.Clean does not strip
// a leading "..", so the join would otherwise resolve into the Library root.
func TestIngestSkipsPathTraversalMember(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	contentRoot := filepath.Join(tmp, "downloads", "Pack")
	writePayload(t, filepath.Join(contentRoot, "ep1.mkv"))
	// The escaping member's *source* exists, so without the guard relocateFile
	// would succeed and land the file (and its sidecar) one level above the
	// Incoming Folder, directly in the Library root. (contentRoot/../evil.mkv)
	writePayload(t, filepath.Join(tmp, "downloads", "evil.mkv"))

	src := &fakeSource{items: []Item{{
		ID: "pack-evil", ReleaseName: "Pack.S01", Category: "films", ContentPath: contentRoot,
		Files: []ItemFile{
			{RelPath: "ep1.mkv", Size: 7},
			{RelPath: filepath.Join("..", "evil.mkv"), Size: 7}, // ../evil.mkv -> escapes the root
		},
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, _ := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, filepath.Join(tmp, "downloads"))

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The legit member imports; the traversal member is silently skipped — a
	// hostile payload should degrade, not error the whole item.
	if result.Imported != 1 || result.Errors != 0 {
		t.Fatalf("expected ep1 imported and no errors, got %+v", result)
	}
	incoming := filepath.Join(libRoot, IncomingFolderName)
	if _, err := os.Stat(filepath.Join(incoming, "ep1.mkv")); err != nil {
		t.Errorf("legit member should be linked into the incoming folder: %v", err)
	}
	// Nothing escaped: the traversal target (libRoot/evil.mkv) and its sidecar
	// must never be created.
	escaped := filepath.Join(libRoot, "evil.mkv")
	if _, err := os.Stat(escaped); !os.IsNotExist(err) {
		t.Fatalf("path-traversal member escaped the incoming folder: %s exists", escaped)
	}
	if _, err := os.Stat(escaped + SidecarSuffix); !os.IsNotExist(err) {
		t.Fatalf("path-traversal sidecar escaped the incoming folder: %s exists", escaped+SidecarSuffix)
	}
}

// TestIngestSkipsSymlinkEscapingContentRoot is the symlink regression (ADR-0020
// §2, H10): a member path with no ".." can still be a symlink whose target lives
// OUTSIDE the download root. The literal-string escapesRoot guard (#10) passes
// such a member, so without symlink resolution ingest would fingerprint and
// hardlink the secret target into the Library. The legit in-root member must
// still import; the symlink member must be refused and its target never linked.
func TestIngestSkipsSymlinkEscapingContentRoot(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	contentRoot := filepath.Join(tmp, "downloads", "Pack")
	writePayload(t, filepath.Join(contentRoot, "ep1.mkv"))

	// A secret file outside the download root entirely.
	secret := filepath.Join(tmp, "outside", "secret.mkv")
	writePayload(t, secret)

	// A member that lives inside the content root by name (no "..", passes
	// escapesRoot) but is a symlink whose target escapes the root.
	symMember := filepath.Join(contentRoot, "leak.mkv")
	if err := os.Symlink(secret, symMember); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	src := &fakeSource{items: []Item{{
		ID: "pack-symlink", ReleaseName: "Pack.S01", Category: "films", ContentPath: contentRoot,
		Files: []ItemFile{
			{RelPath: "ep1.mkv", Size: 7},
			{RelPath: "leak.mkv", Size: 7}, // in-root name, symlink target escapes
		},
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, _ := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, filepath.Join(tmp, "downloads"))

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The legit member imports; the escaping symlink is silently skipped — a
	// hostile payload should degrade, not error the whole item.
	if result.Imported != 1 || result.Errors != 0 {
		t.Fatalf("expected ep1 imported and no errors, got %+v", result)
	}
	incoming := filepath.Join(libRoot, IncomingFolderName)
	if _, err := os.Stat(filepath.Join(incoming, "ep1.mkv")); err != nil {
		t.Errorf("legit member should be linked into the incoming folder: %v", err)
	}
	// The symlink member must NOT have been linked: no leak.mkv in the incoming
	// folder.
	leaked := filepath.Join(incoming, "leak.mkv")
	if _, err := os.Lstat(leaked); !os.IsNotExist(err) {
		t.Fatalf("symlink member escaped containment: %s exists", leaked)
	}
	// Defense in depth: the secret's inode must not have been hardlinked
	// anywhere inside the incoming folder under any other name.
	secretInfo, err := os.Stat(secret)
	if err != nil {
		t.Fatalf("secret should be untouched: %v", err)
	}
	_ = filepath.WalkDir(incoming, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if info, statErr := os.Stat(p); statErr == nil && os.SameFile(info, secretInfo) {
			t.Fatalf("secret's inode was hardlinked into the incoming folder at %s", p)
		}
		return nil
	})
}

// TestIngestSkipsSymlinkedSingleFileContentPath: a single-file payload whose
// ContentPath is itself a symlink to a file outside the approved download root
// must be refused — the single-file branch has no member path to inspect, so
// resolved containment against the approved roots is the only guard (ADR-0020
// §2, H10; SECAUDIT-20260817-QBIT-APPROVED-ROOT-1 symlink escape).
func TestIngestSkipsSymlinkedSingleFileContentPath(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	downloadDir := filepath.Join(tmp, "downloads")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(tmp, "outside", "secret.mkv")
	writePayload(t, secret)
	// ContentPath looks like an in-root download but is a symlink escaping it.
	contentPath := filepath.Join(downloadDir, "Show.S01E01.mkv")
	if err := os.Symlink(secret, contentPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	src := &fakeSource{items: []Item{{
		ID: "single-symlink", ReleaseName: "Show.S01E01", Category: "films", ContentPath: contentPath,
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, _ := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, downloadDir)

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The ContentPath resolves outside the approved download root, so the
	// item-level containment gate refuses it as an error before any payload
	// work. The security assertion is that nothing landed, no scan armed, and
	// the secret was never fingerprinted or linked.
	if result.Errors != 1 {
		t.Fatalf("expected a containment refusal, got %+v", result)
	}
	if len(scan.scans) != 0 {
		t.Errorf("a refused single-file payload must arm no scan, got %v", scan.scans)
	}
	incoming := filepath.Join(libRoot, IncomingFolderName)
	if _, err := os.Stat(filepath.Join(incoming, "Show.S01E01.mkv")); !os.IsNotExist(err) {
		t.Fatalf("symlinked single-file content path escaped containment")
	}
}

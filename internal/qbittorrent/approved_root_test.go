package qbittorrent

// SECAUDIT-20260817-QBIT-APPROVED-ROOT-1 acceptance tests: every
// endpoint-nominated payload path is untrusted and must resolve — no-follow at
// the leaf — beneath a canonical operator-approved download root and outside
// every Library root before any fingerprint/link/copy/sidecar work.
// Red-first evidence (observed failure lines with the guard deliberately
// broken) is recorded at the bottom of this file.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// refusalIngestor builds an ingestor whose fingerprint function fails the test
// if it is ever invoked: a refused payload must see ZERO payload work, not
// merely fail to land.
func refusalIngestor(t *testing.T, src Source, cat Catalog, scan ScanEnqueuer, routing map[string]store.LibraryID, approvedRoots ...string) (*Ingestor, *Ledger) {
	t.Helper()
	ledger, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	ing, err := NewIngestor(Config{
		Source: src, Catalog: cat, Scan: scan, Ledger: ledger, Routing: routing,
		ApprovedRoots: approvedRoots,
		Fingerprint: func(path string) (string, error) {
			t.Errorf("refused payload was fingerprinted: %s", path)
			return "fp:" + filepath.Base(path), nil
		},
	})
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}
	return ing, ledger
}

// assertNothingLanded asserts the refusal left no inode, sidecar, or scan: the
// Library root holds no incoming folder at all and no scan was armed.
func assertNothingLanded(t *testing.T, libRoot string, scan *fakeScan) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(libRoot, IncomingFolderName)); !os.IsNotExist(err) {
		t.Fatalf("refused payload created an incoming folder under %s", libRoot)
	}
	if len(scan.scans) != 0 {
		t.Fatalf("refused payload armed a scan: %v", scan.scans)
	}
}

// TestIngestRefusesSingleFileOutsideApprovedRoots is the self-parent-bypass
// regression: single-file ingest used to contain ContentPath against its OWN
// parent directory — a tautology every readable path satisfies — so a
// compromised or MITM'd endpoint could nominate any readable media file on
// the machine. Now the file must resolve beneath an operator-approved
// download root; anything else is refused with no fingerprint, no inode, no
// sidecar, no scan.
func TestIngestRefusesSingleFileOutsideApprovedRoots(t *testing.T) {
	// feature:qbittorrent-ingest
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	approved := filepath.Join(tmp, "downloads")
	if err := os.MkdirAll(approved, 0o750); err != nil {
		t.Fatal(err)
	}
	// A readable media file the operator never approved for ingest.
	stolen := filepath.Join(tmp, "private", "home-video.mkv")
	writePayload(t, stolen)

	src := &fakeSource{items: []Item{{
		ID: "hostile-single", ReleaseName: "Title.S01E01", Category: "films", ContentPath: stolen,
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, ledger := refusalIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, approved)

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Errors != 1 || result.Imported != 0 {
		t.Fatalf("expected one refusal, got %+v", result)
	}
	assertNothingLanded(t, libRoot, scan)
	status := ledger.Status()
	if len(status) != 1 || status[0].Outcome != OutcomeError || !strings.Contains(status[0].Detail, "approved download roots") {
		t.Fatalf("unexpected status: %+v", status)
	}
}

// TestIngestRefusesEverythingWithoutApprovedRoots: with no approved roots the
// gate is fail-closed — even a payload that looks perfectly ordinary is
// refused until the operator's configuration supplies roots.
func TestIngestRefusesEverythingWithoutApprovedRoots(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	download := filepath.Join(tmp, "downloads", "plain.mkv")
	writePayload(t, download)

	src := &fakeSource{items: []Item{{ID: "no-roots", ReleaseName: "plain", Category: "films", ContentPath: download}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, ledger := refusalIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"})

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Errors != 1 || result.Imported != 0 {
		t.Fatalf("expected fail-closed refusal, got %+v", result)
	}
	assertNothingLanded(t, libRoot, scan)
	status := ledger.Status()
	if len(status) != 1 || status[0].Detail != "no approved download roots configured" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

// TestIngestRefusesLibraryRootTarget: a payload path inside a routed Library
// root is refused even when it also sits beneath an approved download root
// (roots can drift after config-time overlap validation). Ingesting Library
// content back into the Library would double-import curated files.
func TestIngestRefusesLibraryRootTarget(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	inLibrary := filepath.Join(libRoot, "curated.mkv")
	writePayload(t, inLibrary)

	src := &fakeSource{items: []Item{{
		ID: "library-target", ReleaseName: "curated", Category: "films", ContentPath: inLibrary,
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	// The approved root (tmp) CONTAINS the Library root — the drift scenario
	// config-time validation cannot rule out forever.
	ing, ledger := refusalIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, tmp)

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Errors != 1 || result.Imported != 0 {
		t.Fatalf("expected Library-root refusal, got %+v", result)
	}
	assertNothingLanded(t, libRoot, scan)
	status := ledger.Status()
	if len(status) != 1 || status[0].Outcome != OutcomeError {
		t.Fatalf("unexpected status: %+v", status)
	}
}

// TestIngestRefusesUnroutedLibraryRootTarget proves the live exclusion covers
// every Library, not only Libraries targeted by this qBittorrent routing map.
func TestIngestRefusesUnroutedLibraryRootTarget(t *testing.T) {
	tmp := t.TempDir()
	routedRoot := filepath.Join(tmp, "routed-library")
	unroutedRoot := filepath.Join(tmp, "downloads", "unrouted-library")
	inUnroutedLibrary := filepath.Join(unroutedRoot, "curated.mkv")
	writePayload(t, inUnroutedLibrary)

	src := &fakeSource{items: []Item{{
		ID: "unrouted-library-target", ReleaseName: "curated", Category: "films", ContentPath: inUnroutedLibrary,
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{
		"lib-films":  {routedRoot},
		"lib-series": {unroutedRoot},
	}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, ledger := refusalIngestor(
		t, src, cat, scan,
		map[string]store.LibraryID{"films": "lib-films"},
		filepath.Join(tmp, "downloads"),
	)

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Errors != 1 || result.Imported != 0 {
		t.Fatalf("expected unrouted Library-root refusal, got %+v", result)
	}
	assertNothingLanded(t, routedRoot, scan)
	status := ledger.Status()
	if len(status) != 1 || status[0].Outcome != OutcomeError {
		t.Fatalf("unexpected status: %+v", status)
	}
}

// TestIngestRefusesStaleRootAfterReconfigure: Reconfigure replaces the
// approved roots wholesale, so a root from the PREVIOUS configuration stops
// admitting imports the moment the swap commits — the endpoint cannot keep
// nominating paths under a root the operator no longer approves.
func TestIngestRefusesStaleRootAfterReconfigure(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	oldRoot := filepath.Join(tmp, "old-downloads")
	newRoot := filepath.Join(tmp, "new-downloads")
	oldFile := filepath.Join(oldRoot, "stale.mkv")
	newFile := filepath.Join(newRoot, "fresh.mkv")
	writePayload(t, oldFile)
	writePayload(t, newFile)

	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	src := &fakeSource{
		categories: []string{"films"},
		items: []Item{
			{ID: "stale-root", ReleaseName: "stale", Category: "films", ContentPath: oldFile},
			{ID: "fresh-root", ReleaseName: "fresh", Category: "films", ContentPath: newFile},
		},
	}
	ing, ledger := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, oldRoot)

	// The operator re-approves ingest with newRoot only: oldRoot is stale.
	if err := ing.Reconfigure(context.Background(), src, map[string]store.LibraryID{"films": "lib-films"}, []string{newRoot}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Errors != 1 || result.Imported != 1 {
		t.Fatalf("expected stale-root refusal plus fresh-root import, got %+v", result)
	}
	if _, err := os.Stat(filepath.Join(libRoot, IncomingFolderName, "fresh.mkv")); err != nil {
		t.Fatalf("in-root payload should import after reconfigure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(libRoot, IncomingFolderName, "stale.mkv")); !os.IsNotExist(err) {
		t.Fatalf("stale-root payload landed despite refusal")
	}
	var sawRefusal bool
	for _, entry := range ledger.Status() {
		if entry.TorrentHash == "stale-root" && entry.Outcome == OutcomeError {
			sawRefusal = true
		}
	}
	if !sawRefusal {
		t.Fatalf("stale-root refusal missing from status feed: %+v", ledger.Status())
	}
}

// TestIngestRefusesMultiFilePayloadOutsideApprovedRoots: a hostile multi-file
// item whose content root the endpoint points outside the approved roots is
// refused whole — member relative paths never get a chance to resolve, and no
// inode, sidecar, or scan lands.
func TestIngestRefusesMultiFilePayloadOutsideApprovedRoots(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	approved := filepath.Join(tmp, "downloads")
	if err := os.MkdirAll(approved, 0o750); err != nil {
		t.Fatal(err)
	}
	hostileRoot := filepath.Join(tmp, "elsewhere", "Pack")
	writePayload(t, filepath.Join(hostileRoot, "ep1.mkv"))
	writePayload(t, filepath.Join(hostileRoot, "ep2.mkv"))

	src := &fakeSource{items: []Item{{
		ID: "hostile-pack", ReleaseName: "Pack.S01", Category: "films", ContentPath: hostileRoot,
		Files: []ItemFile{{RelPath: "ep1.mkv", Size: 7}, {RelPath: "ep2.mkv", Size: 7}},
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, ledger := refusalIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, approved)

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Errors != 1 || result.Imported != 0 {
		t.Fatalf("expected whole-item refusal, got %+v", result)
	}
	assertNothingLanded(t, libRoot, scan)
	status := ledger.Status()
	if len(status) != 1 || status[0].Outcome != OutcomeError {
		t.Fatalf("unexpected status: %+v", status)
	}
}

// TestIngestRefusesSymlinkMemberLeaf is the no-follow leaf check: a member
// whose path IS a symlink is refused even when its target stays inside the
// approved root, because a link can be swapped between the containment check
// and the hardlink — only a regular file may be fingerprinted/linked. The
// legitimate sibling member still imports.
func TestIngestRefusesSymlinkMemberLeaf(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	contentRoot := filepath.Join(tmp, "downloads", "Pack")
	real := filepath.Join(contentRoot, "ep1.mkv")
	writePayload(t, real)
	linked := filepath.Join(contentRoot, "ep2.mkv")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	src := &fakeSource{items: []Item{{
		ID: "pack-leaf-symlink", ReleaseName: "Pack.S01", Category: "films", ContentPath: contentRoot,
		Files: []ItemFile{{RelPath: "ep1.mkv", Size: 7}, {RelPath: "ep2.mkv", Size: 7}},
	}}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-films": {libRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	ing, _ := newTestIngestor(t, src, cat, scan, map[string]store.LibraryID{"films": "lib-films"}, filepath.Join(tmp, "downloads"))

	result, err := ing.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Imported != 1 {
		t.Fatalf("expected the regular member to import, got %+v", result)
	}
	incoming := filepath.Join(libRoot, IncomingFolderName)
	if _, err := os.Stat(filepath.Join(incoming, "ep1.mkv")); err != nil {
		t.Fatalf("regular member should be linked: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(incoming, "ep2.mkv")); !os.IsNotExist(err) {
		t.Fatalf("symlink leaf member was linked despite no-follow refusal")
	}
}

// Evidence — SECAUDIT-20260817-QBIT-APPROVED-ROOT-1 (2026-08-20, UTC ~11:40)
//
// Red-first proof: with the containment guard deliberately broken (confinesPath
// and confinesFile short-circuited to `return true` in confine.go — the moral
// equivalent of the pre-fix self-parent check), the refusal tests in this file
// and the updated symlink test observed the vulnerability live. Command:
//
//	go test ./internal/qbittorrent/ -run 'TestIngestRefuses|TestIngestSkipsSymlinkedSingleFileContentPath'
//	(worktree at base f0a4db6e3d7b07c33561cd3d65b4ef33ddc7acd9 + this change, guard disabled; exit code 1, ~0.4s)
//
// Observed failure lines (paths abbreviated to the per-test temp dir):
//
//	--- FAIL: TestIngestRefusesSingleFileOutsideApprovedRoots
//	    approved_root_test.go:33: refused payload was fingerprinted: .../private/home-video.mkv
//	    approved_root_test.go:86: expected one refusal, got {Imported:1 ... ScansArmed:map[lib-films:true]}
//	--- FAIL: TestIngestRefusesEverythingWithoutApprovedRoots
//	    approved_root_test.go:114: expected fail-closed refusal, got {Imported:1 ...}
//	--- FAIL: TestIngestRefusesLibraryRootTarget
//	    approved_root_test.go:33: refused payload was fingerprinted: .../library/curated.mkv
//	--- FAIL: TestIngestRefusesStaleRootAfterReconfigure
//	    approved_root_test.go:190: expected stale-root refusal plus fresh-root import, got {Imported:2 ...}
//	--- FAIL: TestIngestRefusesMultiFilePayloadOutsideApprovedRoots
//	    approved_root_test.go:33: refused payload was fingerprinted: .../elsewhere/Pack/ep1.mkv
//	--- FAIL: TestIngestRefusesSymlinkMemberLeaf
//	    approved_root_test.go:282: symlink leaf member was linked despite no-follow refusal
//	--- FAIL: TestIngestSkipsSymlinkedSingleFileContentPath
//	    ingest_test.go:634: expected a containment refusal, got {Imported:1 ...}
//
// Boot-carry proof: with app.go's ApprovedRoots wiring deliberately dropped
// (`approvedRoots[:0]`), the composition-root test observed:
//
//	--- FAIL: TestBuildQBittorrentIngestorCapturesApprovedRootsAtBoot
//	    app_qbittorrent_boot_test.go:58: approved roots = [], want [/downloads /downloads/films]
//
// Both deliberate breaks were then reverted; with the guard restored:
//
//	go test -count=1 ./internal/qbittorrent/   -> ok (0.99s, exit 0)
//	go test -count=1 ./internal/app/ -run TestBuildQBittorrentIngestor -> ok (0.68s, exit 0)
//	go vet ./internal/qbittorrent/ ./internal/app/ -> clean (exit 0)

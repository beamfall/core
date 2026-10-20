package qbittorrent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/store"
)

type blockedCompletedSource struct {
	started chan struct{}
	release chan struct{}
	items   []Item
	once    sync.Once
}

type crashingCompletedSource struct{ err error }

func (s *crashingCompletedSource) Categories(context.Context) ([]string, error) {
	return []string{"films"}, nil
}

func (s *crashingCompletedSource) CompletedItems(context.Context) ([]Item, error) {
	return nil, s.err
}

func (s *blockedCompletedSource) Categories(context.Context) ([]string, error) {
	return []string{"films"}, nil
}

func (s *blockedCompletedSource) CompletedItems(context.Context) ([]Item, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return s.items, nil
}

func TestIngestorGenerationFenceABAReconfigureDisableCrashRestart(t *testing.T) {
	tmp := t.TempDir()
	libraryRoot := filepath.Join(tmp, "library")
	downloadRoot := filepath.Join(tmp, "downloads")
	oldPath := filepath.Join(downloadRoot, "old.mkv")
	currentPath := filepath.Join(downloadRoot, "current.mkv")
	disabledPath := filepath.Join(downloadRoot, "disabled.mkv")
	recoveredPath := filepath.Join(downloadRoot, "recovered.mkv")
	for _, path := range []string{oldPath, currentPath, disabledPath, recoveredPath} {
		writePayload(t, path)
	}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {libraryRoot}}, fingerprints: map[string]store.MediaID{}}
	scan := &fakeScan{}
	stale := &blockedCompletedSource{
		started: make(chan struct{}), release: make(chan struct{}),
		items: []Item{{ID: "old", Category: "films", ContentPath: oldPath}},
	}
	ing, ledger := newTestIngestor(t, stale, cat, scan, map[string]store.LibraryID{"films": "lib-a"}, downloadRoot)
	staleDone := make(chan error, 1)
	go func() {
		_, err := ing.Run(t.Context())
		staleDone <- err
	}()
	<-stale.started

	current := &fakeSource{categories: []string{"films"}, items: []Item{{ID: "current", Category: "films", ContentPath: currentPath}}}
	if err := ing.Reconfigure(t.Context(), current, map[string]store.LibraryID{"films": "lib-a"}, []string{downloadRoot}); err != nil {
		t.Fatalf("reconfigure B: %v", err)
	}
	// Return to an equivalent A configuration. Ownership is a monotonic
	// generation, not source identity, so the original A pass stays stale.
	if err := ing.Reconfigure(t.Context(), current, map[string]store.LibraryID{"films": "lib-a"}, []string{downloadRoot}); err != nil {
		t.Fatalf("reconfigure A: %v", err)
	}
	close(stale.release)
	if err := <-staleDone; err != nil {
		t.Fatalf("stale run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(libraryRoot, IncomingFolderName, "old.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale A generation published old.mkv: %v", err)
	}
	if got := len(ledger.Status()); got != 0 {
		t.Fatalf("stale A generation published %d status rows", got)
	}

	if result, err := ing.Run(t.Context()); err != nil || result.Imported != 1 {
		t.Fatalf("current A generation result = %+v, err = %v", result, err)
	}
	statusBeforeDisable := len(ledger.Status())
	scansBeforeDisable := len(scan.scans)

	disabling := &blockedCompletedSource{
		started: make(chan struct{}), release: make(chan struct{}),
		items: []Item{{ID: "disabled", Category: "films", ContentPath: disabledPath}},
	}
	if err := ing.Reconfigure(t.Context(), disabling, map[string]store.LibraryID{"films": "lib-a"}, []string{downloadRoot}); err != nil {
		t.Fatalf("reconfigure before disable: %v", err)
	}
	disabledDone := make(chan error, 1)
	go func() {
		_, err := ing.Run(t.Context())
		disabledDone <- err
	}()
	<-disabling.started
	ing.Disable()
	close(disabling.release)
	if err := <-disabledDone; err != nil {
		t.Fatalf("disabled run: %v", err)
	}
	if ing.Configured() {
		t.Fatal("disabled ingestor remained configured")
	}
	if got := len(ledger.Status()); got != statusBeforeDisable {
		t.Fatalf("disabled generation published status: got %d, want %d", got, statusBeforeDisable)
	}
	if got := len(scan.scans); got != scansBeforeDisable {
		t.Fatalf("disabled generation published scan: got %d, want %d", got, scansBeforeDisable)
	}

	crashErr := errors.New("source crashed")
	crashed := &crashingCompletedSource{err: crashErr}
	if err := ing.Reconfigure(t.Context(), crashed, map[string]store.LibraryID{"films": "lib-a"}, []string{downloadRoot}); err != nil {
		t.Fatalf("configure crashing source: %v", err)
	}
	if _, err := ing.Run(t.Context()); !errors.Is(err, crashErr) {
		t.Fatalf("crashing generation = %v, want %v", err, crashErr)
	}
	recovered := &fakeSource{categories: []string{"films"}, items: []Item{{ID: "recovered", Category: "films", ContentPath: recoveredPath}}}
	if err := ing.Reconfigure(t.Context(), recovered, map[string]store.LibraryID{"films": "lib-a"}, []string{downloadRoot}); err != nil {
		t.Fatalf("restart after crash: %v", err)
	}
	if result, err := ing.Run(t.Context()); err != nil || result.Imported != 1 {
		t.Fatalf("recovered generation result = %+v, err = %v", result, err)
	}
}

// TestReconfigureConcurrentWithRun exercises the live-swap path against a
// concurrent poll loop under -race: Reconfigure must be safe to call while Run
// reads the source + routing (ADR-0020 §6 — applied without restart, the poll
// job and the Console mutation run concurrently).
func TestReconfigureConcurrentWithRun(t *testing.T) {
	// feature:qbittorrent-ingest
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {t.TempDir()}}}
	ing, _ := newTestIngestor(t, &fakeSource{categories: []string{"films"}}, cat, &fakeScan{}, map[string]store.LibraryID{"films": "lib-a"})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = ing.Run(t.Context())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = ing.Reconfigure(t.Context(), &fakeSource{categories: []string{"films"}}, map[string]store.LibraryID{"films": "lib-a"}, nil)
		}
	}()
	wg.Wait()
	if !ing.Configured() {
		t.Fatalf("after concurrent reconfigure Configured=false")
	}
}

// TestReconfigureSwapsLiveConfig is FEAT-10 acceptance (c): the live ingestor
// reflects new routing + source without a restart, and the swap is visible
// through Routes/Configured immediately.
func TestReconfigureSwapsLiveConfig(t *testing.T) {
	// feature:qbittorrent-ingest
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {t.TempDir()}}}
	// Build inert (nil source): nothing configured at boot.
	ing, _ := newTestIngestor(t, nil, cat, &fakeScan{}, nil)
	if ing.Configured() {
		t.Fatalf("inert ingestor reports Configured=true")
	}
	if got := ing.Routes(); len(got) != 0 {
		t.Fatalf("inert routes = %#v, want empty", got)
	}

	newSource := &fakeSource{categories: []string{"films"}}
	if err := ing.Reconfigure(t.Context(), newSource, map[string]store.LibraryID{"films": "lib-a"}, nil); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	if !ing.Configured() {
		t.Fatalf("after Reconfigure Configured=false")
	}
	routes := ing.Routes()
	if len(routes) != 1 || routes["films"] != "lib-a" {
		t.Fatalf("live routes = %#v, want films->lib-a", routes)
	}

	// A second Reconfigure swaps again — the running instance tracks the latest
	// config with no restart.
	if err := ing.Reconfigure(t.Context(), &fakeSource{categories: []string{"films", "music"}}, map[string]store.LibraryID{"music": "lib-a"}, nil); err != nil {
		t.Fatalf("second Reconfigure: %v", err)
	}
	routes = ing.Routes()
	if len(routes) != 1 || routes["music"] != "lib-a" {
		t.Fatalf("re-swapped routes = %#v, want music->lib-a", routes)
	}
}

// TestReconfigureRejectsInvalidRouting is FEAT-10 acceptance (b): a routed
// category that does not exist in qBittorrent is rejected by ValidateRouting,
// and the rejected config never disturbs the live instance.
func TestReconfigureRejectsInvalidRouting(t *testing.T) {
	// feature:qbittorrent-ingest
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {t.TempDir()}}}
	live := &fakeSource{categories: []string{"films"}}
	ing, _ := newTestIngestor(t, live, cat, &fakeScan{}, map[string]store.LibraryID{"films": "lib-a"})

	// Candidate routes a category the (candidate) source does not know.
	candidate := &fakeSource{categories: []string{"films"}}
	err := ing.Reconfigure(t.Context(), candidate, map[string]store.LibraryID{"typo": "lib-a"}, nil)
	if err == nil {
		t.Fatalf("Reconfigure with unknown category = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want category-existence rejection", err)
	}
	// Live config untouched: still the original films->lib-a routing.
	routes := ing.Routes()
	if len(routes) != 1 || routes["films"] != "lib-a" {
		t.Fatalf("after rejected Reconfigure live routes = %#v, want films->lib-a unchanged", routes)
	}
}

// TestReconfigureRejectsOverlappingSavePath is FEAT-10 acceptance (b), the §2
// guard: a save path inside a routed Library root is rejected before commit.
func TestReconfigureRejectsOverlappingSavePath(t *testing.T) {
	// feature:qbittorrent-ingest
	libRoot := t.TempDir()
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {libRoot}}}
	ing, _ := newTestIngestor(t, nil, cat, &fakeScan{}, nil)

	candidate := &fakeSource{categories: []string{"films"}}
	// Download dir sits INSIDE the Library root — overlap (§2).
	err := ing.Reconfigure(t.Context(), candidate, map[string]store.LibraryID{"films": "lib-a"}, []string{libRoot})
	if err == nil {
		t.Fatalf("Reconfigure with overlapping save path = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("error = %v, want save-path-overlap rejection", err)
	}
	if ing.Configured() {
		t.Fatalf("after rejected Reconfigure Configured=true — bad config went live")
	}
}

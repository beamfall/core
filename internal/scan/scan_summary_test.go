package scan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// queueingIdentifier parks the file with one content fingerprint in the
// Unmatched queue, as the identify pipeline does for a low-confidence match.
type queueingIdentifier struct {
	st          *store.Store
	fingerprint string
}

func (q *queueingIdentifier) AutoIdentify(ctx context.Context, mediaID store.MediaID, pluginID, fingerprint string, _ ParsedMedia) error {
	if fingerprint != q.fingerprint {
		return nil
	}
	return q.st.QueueUnmatched(ctx, store.UnmatchedItem{MediaID: mediaID, PluginID: pluginID, ScraperID: "test-scraper"})
}

// TestScanJobRecordsSummary: a completed scan job records what it added,
// changed, marked missing and left unmatched, so the Console can say more
// than how many files it checked (CORE-2040).
func TestScanJobRecordsSummary(t *testing.T) {
	// feature:scan-pipeline
	ctx := context.Background()
	root := t.TempDir()
	changed := writeScanFile(t, root, "clip-a.mp4", []byte("clip a"))
	unmatched := writeScanFile(t, root, "clip-b.mp4", []byte("clip b"))
	removed := writeScanFile(t, root, "clip-c.mp4", []byte("clip c"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Clips", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := hashFileContent(unmatched)
	if err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, &queueingIdentifier{st: st, fingerprint: fingerprint}))

	first := runSummaryScan(t, st, registry, "job-summary-1", library.ID, root)
	if want := (ScanSummary{FilesChecked: 3, Added: 3, Unmatched: 1}); first != want {
		t.Fatalf("first scan summary = %+v, want %+v", first, want)
	}

	if err := os.WriteFile(changed, []byte("clip a, edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(changed, older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	writeScanFile(t, root, "clip-d.mp4", []byte("clip d"))

	second := runSummaryScan(t, st, registry, "job-summary-2", library.ID, root)
	if want := (ScanSummary{FilesChecked: 3, Added: 1, Changed: 1, Missing: 1, Unmatched: 1}); second != want {
		t.Fatalf("rescan summary = %+v, want %+v", second, want)
	}
}

// TestScanSummarySurvivesWorkerPoolYield: with several workers, files whose
// imports committed before the batch yield are still counted as added, though
// the resumed run takes them through the fast path (CORE-2040).
func TestScanSummarySurvivesWorkerPoolYield(t *testing.T) {
	// feature:scan-pipeline
	const files = scanJobFileBatchSize + 3
	root := t.TempDir()
	for i := 0; i < files; i++ {
		writeScanFile(t, root, "clip-"+zeroPad(i)+".mp4", []byte("clip "+zeroPad(i)))
	}
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Clips", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(4))
	summary := runSummaryScanToCompletion(t, st, handler, "job-summary-yield", library.ID, root, 2)
	if want := (ScanSummary{FilesChecked: files, Added: files}); summary != want {
		t.Fatalf("yielded scan summary = %+v, want %+v", summary, want)
	}
}

// TestScanSummaryCountsRefusedFolderOnce: a folder the parser refuses as
// ambiguous is one item in the Unmatched queue, so the summary counts it once
// however many of its files were refused, across a batch yield too (CORE-2040).
func TestScanSummaryCountsRefusedFolderOnce(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	dir := filepath.Join(root, "ambiguous")
	for i := 0; i < scanJobFileBatchSize+2; i++ {
		writeScanFile(t, dir, "part-"+zeroPad(i)+".mkv", []byte("part "+zeroPad(i)))
	}
	writeScanFile(t, root, "single.mp4", []byte("single"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Contextual", false, "contextual", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(st, nil, nil, map[string]FilenameParser{"contextual": contextualRefusalParser{}}, nil, nil)
	for pass, added := range []int{1, 0} {
		summary := runSummaryScanToCompletion(t, st, handler, "job-summary-refused-"+zeroPad(pass), library.ID, root, 2)
		if want := (ScanSummary{FilesChecked: scanJobFileBatchSize + 3, Added: added, Unmatched: 1}); summary != want {
			t.Fatalf("pass %d summary = %+v, want %+v", pass+1, summary, want)
		}
	}
	// Suppressing the folder's record clears it from the queue, so a rescan
	// that refuses the same folder again leaves nothing Unmatched.
	records, err := st.UnmatchedRecords(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("pending records = %d, want 1", len(records))
	}
	if _, err := st.ResolveUnmatchedRecord(context.Background(), records[0].ID, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	summary := runSummaryScanToCompletion(t, st, handler, "job-summary-refused-suppressed", library.ID, root, 2)
	if want := (ScanSummary{FilesChecked: scanJobFileBatchSize + 3}); summary != want {
		t.Fatalf("after suppress summary = %+v, want %+v", summary, want)
	}
}

func TestScanSummaryKeepsPartGroupAcrossYield(t *testing.T) {
	// feature:scan-pipeline
	// A part group larger than one batch is imported at once, then reported
	// file by file; the yield partway through must not drop the added counts
	// of the parts reported after it.
	root := t.TempDir()
	dir := filepath.Join(root, "Writer", "Long")
	prober := taggedPartsProber{}
	parts := scanJobFileBatchSize + 2
	for i := 1; i <= parts; i++ {
		name := zeroPad(i) + " - Part.mp3"
		writeScanFile(t, dir, name, []byte(name))
		prober[name] = map[string]string{"album": "Long", "track": strconv.Itoa(i)}
	}
	st := storetest.Open(t)
	handler := Handler(st, prober, nil, map[string]FilenameParser{"parts": taggedPartsParser{}}, nil, nil)
	summary := runSummaryScanToCompletion(t, st, handler, "job-summary-parts", partsLibrary(t, st), root, 2)
	if want := (ScanSummary{FilesChecked: parts, Added: parts}); summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
}

func TestScanSummaryCountsImportBeforeCancellation(t *testing.T) {
	// feature:scan-pipeline
	// A run cancelled while deriving its first image has already put the
	// photo in the catalog; the resumed run must still count it as added.
	root := t.TempDir()
	writeScanFile(t, root, "photo-000.jpg", []byte("photo 0"))
	st := storetest.Open(t)
	job, err := NewJob(JobRequest{ID: "job-summary-cancelled", LibraryID: "library-local", Root: root, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	deriver := &cancellableBlockingDeriver{started: make(chan struct{})}
	cancelled := jobs.NewRegistry(st, "summary-worker-cancelled")
	cancelled.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(1), deriver))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := cancelled.RunOne(ctx, nil, time.Now().UTC())
		done <- err
	}()
	<-deriver.started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("cancelled run: %v", err)
	}
	resumed := jobs.NewRegistry(st, "summary-worker-resumed")
	resumed.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(1)))
	if _, err := resumed.RunOne(context.Background(), nil, time.Now().UTC()); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if summary, want := completedScanSummary(t, st, job.ID), (ScanSummary{FilesChecked: 1, Added: 1}); summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
}

func runSummaryScan(t *testing.T, st *store.Store, registry *jobs.Registry, id string, libraryID store.LibraryID, root string) ScanSummary {
	t.Helper()
	ctx := context.Background()
	job, err := NewJob(JobRequest{ID: store.JobID(id), LibraryID: libraryID, Root: root, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RunOne(ctx, nil, time.Now().UTC()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	return completedScanSummary(t, st, job.ID)
}

// runSummaryScanToCompletion runs a scan job through wantRuns runs, each on a
// fresh registry as after a restart, and returns its summary.
func runSummaryScanToCompletion(t *testing.T, st *store.Store, handler jobs.Handler, id string, libraryID store.LibraryID, root string, wantRuns int) ScanSummary {
	t.Helper()
	ctx := context.Background()
	job, err := NewJob(JobRequest{ID: store.JobID(id), LibraryID: libraryID, Root: root, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	for run := 0; run < wantRuns; run++ {
		registry := jobs.NewRegistry(st, "summary-worker-"+zeroPad(run))
		registry.Register(JobKind, handler)
		if _, err := registry.RunOne(ctx, nil, time.Now().UTC()); err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
	}
	return completedScanSummary(t, st, job.ID)
}

func completedScanSummary(t *testing.T, st *store.Store, id store.JobID) ScanSummary {
	t.Helper()
	ctx := context.Background()
	done, err := st.Job(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("status = %q error=%q, want completed", done.Status, done.Error)
	}
	events, err := st.ListJobEvents(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != ScanSummaryEvent {
			continue
		}
		var summary ScanSummary
		if err := json.Unmarshal(event.Payload, &summary); err != nil {
			t.Fatalf("summary payload %s: %v", event.Payload, err)
		}
		return summary
	}
	t.Fatalf("no %s event among %d job events", ScanSummaryEvent, len(events))
	return ScanSummary{}
}

func TestScanSummaryKeepsUnreportedPartsOnCancellation(t *testing.T) {
	// feature:scan-pipeline
	// A scan cancelled while reporting a part group has catalogued every part;
	// the parts not yet reported still count in its resume point.
	root := t.TempDir()
	prober := taggedPartsProber{}
	for i, name := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		writeScanFile(t, root, name, []byte(name))
		prober[name] = map[string]string{"album": "Group", "track": strconv.Itoa(3 - i)}
	}
	st := storetest.Open(t)
	req := Request{LibraryID: partsLibrary(t, st), Root: root}
	parsers := map[string]FilenameParser{"parts": taggedPartsParser{}}
	cancelAtFirstReport := func(jobCheckpoint, store.JobProgress) error { return context.Canceled }
	result, err := scanFromCheckpoint(context.Background(), st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(1), nil, req, jobCheckpoint{}, time.Now().UTC(), cancelAtFirstReport)
	if !errors.Is(err, context.Canceled) || result.interrupted == nil {
		t.Fatalf("cancelled scan: err=%v interrupted=%v", err, result.interrupted)
	}
	assertResumedSummary(t, st, prober, parsers, nil, req, *result.interrupted, 3, 3)
}

// cancellingPartsParser parses 01 and 03 as the parts of one group and
// cancels the scan when it reaches cancelAt.
type cancellingPartsParser struct {
	cancelAt string
	cancel   context.CancelFunc
}

func (p cancellingPartsParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	name := filepath.Base(path)
	if name == p.cancelAt && p.cancel != nil {
		p.cancel()
	}
	switch name {
	case "01.mp3":
		return ParsedMedia{Title: "Group", Class: "collection", PartGroup: "g", Index: 1}, true
	case "03.mp3":
		return ParsedMedia{Title: "Group", Class: "collection", PartGroup: "g", Index: 2}, true
	}
	return ParsedMedia{}, false
}

func TestScanSummaryCountsHeldImageOnceAcrossCancellation(t *testing.T) {
	// feature:scan-pipeline
	// An image completed between pending parts does not move the resume
	// point, so a resume walks it again; it must not count as checked twice.
	root := t.TempDir()
	for _, name := range []string{"01.mp3", "02.jpg", "03.mp3"} {
		writeScanFile(t, root, name, []byte(name))
	}
	st := storetest.Open(t)
	req := Request{LibraryID: partsLibrary(t, st), Root: root}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	parsers := map[string]FilenameParser{"parts": cancellingPartsParser{cancelAt: "03.mp3", cancel: cancel}}
	result, err := scanFromCheckpoint(ctx, st, nil, nil, parsers, nil, nil, nil, nil, fixedGovernor(1), nil, req, jobCheckpoint{}, time.Now().UTC(), nil)
	if !errors.Is(err, context.Canceled) || result.interrupted == nil {
		t.Fatalf("cancelled scan: err=%v interrupted=%v", err, result.interrupted)
	}
	assertResumedSummary(t, st, nil, map[string]FilenameParser{"parts": cancellingPartsParser{}}, nil, req, *result.interrupted, 3, 3)
}

func TestScanSummaryCountsImportCancelledBeforeCompletion(t *testing.T) {
	// feature:scan-pipeline
	// A file imported but cancelled before its completion was queued (here in
	// metadata hydration) is in the catalog; the resume point counts it.
	root := t.TempDir()
	writeScanFile(t, root, "one.jpg", []byte("image"))
	st := storetest.Open(t)
	req := Request{LibraryID: partsLibrary(t, st), Root: root}
	hydrator := &fakeMetadataHydrator{capability: MetadataHydrationCapability{MediaClasses: []store.MediaClass{store.MediaClassImage}}, err: context.Canceled}
	result, err := scanFromCheckpoint(context.Background(), st, nil, nil, nil, nil, nil, nil, map[string]MetadataHydrator{"parts": hydrator}, fixedGovernor(1), nil, req, jobCheckpoint{}, time.Now().UTC(), nil)
	if !errors.Is(err, context.Canceled) || result.interrupted == nil || len(hydrator.calls) != 1 {
		t.Fatalf("cancelled scan: err=%v interrupted=%v hydrations=%d", err, result.interrupted, len(hydrator.calls))
	}
	assertResumedSummary(t, st, nil, nil, nil, req, *result.interrupted, 1, 1)
}

// assertResumedSummary resumes a stopped scan from checkpoint and checks how
// many files the whole scan checked and added.
func assertResumedSummary(t *testing.T, st *store.Store, prober Prober, parsers map[string]FilenameParser, hydrators map[string]MetadataHydrator, req Request, checkpoint jobCheckpoint, wantSeen, wantAdded int) {
	t.Helper()
	resumed, err := scanFromCheckpoint(context.Background(), st, prober, nil, parsers, nil, nil, nil, hydrators, fixedGovernor(1), nil, req, checkpoint, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("resumed scan: %v", err)
	}
	if resumed.FilesSeen != wantSeen || resumed.FilesAdded != wantAdded {
		t.Fatalf("resumed scan seen=%d added=%d, want %d and %d", resumed.FilesSeen, resumed.FilesAdded, wantSeen, wantAdded)
	}
}

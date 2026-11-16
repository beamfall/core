package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// queueAllIdentifier parks every Media it is asked about in the Unmatched
// queue.
type queueAllIdentifier struct{ st *store.Store }

func (q queueAllIdentifier) AutoIdentify(ctx context.Context, mediaID store.MediaID, pluginID, _ string, _ ParsedMedia) error {
	return q.st.QueueUnmatched(ctx, store.UnmatchedItem{MediaID: mediaID, PluginID: pluginID, ScraperID: "test-scraper"})
}

// clearingIdentifier identifies every Media it is asked about, taking it out
// of the Unmatched queue.
type clearingIdentifier struct{ st *store.Store }

func (c clearingIdentifier) AutoIdentify(ctx context.Context, mediaID store.MediaID, _, _ string, _ ParsedMedia) error {
	_, err := c.st.ClearUnmatched(ctx, mediaID)
	return err
}

// markerCancellingProber fails each file's first probe, so the walk imports
// it unprobed, and cancels the scan on the second: the part-boundary marker
// probe, which runs after the group is in the catalog.
type markerCancellingProber struct {
	mu      sync.Mutex
	calls   map[string]int
	cancel  context.CancelFunc
	reached bool
}

func (p *markerCancellingProber) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[path]++
	if p.calls[path] == 1 {
		return ffmpeg.Probe{}, errors.New("probe unavailable")
	}
	p.reached = true
	p.cancel()
	return ffmpeg.Probe{AudioCodec: "mp3", DurationSeconds: 60}, nil
}

func TestScanSummaryCountsPartGroupCancelledBeforeReport(t *testing.T) {
	// feature:scan-pipeline
	// A part group is in the catalog once it is imported; a scan cancelled
	// before reporting it (here in the boundary markers) still counts every
	// part as added or changed when it resumes (CORE-2194).
	for _, workers := range []int{1, 4} {
		for _, edited := range []bool{false, true} {
			t.Run(fmt.Sprintf("workers=%d/edited=%v", workers, edited), func(t *testing.T) {
				root := t.TempDir()
				names := []string{"01.mp3", "03.mp3"}
				for _, name := range names {
					writeScanFile(t, root, name, []byte(name))
				}
				st := storetest.Open(t)
				req := Request{LibraryID: partsLibrary(t, st), Root: root}
				parsers := map[string]FilenameParser{"parts": cancellingPartsParser{}}
				if edited {
					if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, time.Now().UTC(), nil); err != nil {
						t.Fatalf("first scan: %v", err)
					}
					for _, name := range names {
						writeScanFile(t, root, name, []byte(name+" edited"))
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started := time.Now().UTC()
				prober := &markerCancellingProber{calls: map[string]int{}, cancel: cancel}
				result, err := scanFromCheckpoint(ctx, st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, started, nil)
				if !errors.Is(err, context.Canceled) || result.interrupted == nil || !prober.reached {
					t.Fatalf("cancelled scan: err=%v interrupted=%v marker probe reached=%v", err, result.interrupted, prober.reached)
				}
				resumed, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), nil, req, *result.interrupted, started, nil)
				if err != nil {
					t.Fatalf("resumed scan: %v", err)
				}
				wantAdded, wantChanged := 2, 0
				if edited {
					wantAdded, wantChanged = 0, 2
				}
				if resumed.FilesSeen != 2 || resumed.FilesAdded != wantAdded || resumed.FilesChanged != wantChanged {
					t.Fatalf("resumed scan seen=%d added=%d changed=%d, want 2, %d and %d", resumed.FilesSeen, resumed.FilesAdded, resumed.FilesChanged, wantAdded, wantChanged)
				}
			})
		}
	}
}

func TestScanSummaryRefusalCompletesEarlierFiles(t *testing.T) {
	// feature:scan-pipeline
	// A refusal moves the resume point to the refused file, so a file consumed
	// before it and still pending completes first: a resume from the refusal
	// must not skip it (CORE-2194).
	for _, stop := range []error{context.Canceled, jobs.ErrYielded} {
		t.Run(stop.Error(), func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"a/single.mp4", "b/one.mp4", "b/two.mp4"} {
				writeScanFile(t, root, name, []byte(name))
			}
			st := storetest.Open(t)
			req := Request{LibraryID: partsLibrary(t, st), Root: root}
			parsers := map[string]FilenameParser{"parts": contextualRefusalParser{}}
			started := time.Now().UTC()
			var checkpoint jobCheckpoint
			stopAtRefusal := func(c jobCheckpoint, _ store.JobProgress) error {
				if filepath.Base(c.LastPath) != "one.mp4" {
					return nil
				}
				checkpoint = c
				return stop
			}
			result, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, nil, nil, nil, fixedGovernor(4), nil, req, jobCheckpoint{}, started, stopAtRefusal)
			if !errors.Is(err, stop) {
				t.Fatalf("scan did not stop at the refusal: %v", err)
			}
			if result.interrupted != nil {
				checkpoint = *result.interrupted
			}
			resumed, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, nil, nil, nil, fixedGovernor(4), nil, req, checkpoint, started, nil)
			if err != nil {
				t.Fatalf("resumed scan: %v", err)
			}
			if resumed.FilesSeen != 3 || resumed.FilesAdded != 1 {
				t.Fatalf("resumed scan seen=%d added=%d, want 3 and 1", resumed.FilesSeen, resumed.FilesAdded)
			}
		})
	}
}

func TestScanSummaryCountsUnmatchedPartGroupOnce(t *testing.T) {
	// feature:scan-pipeline
	// A part group yielded after its first part was reported re-imports the
	// rest on resume, which identifies the same Media again; the summary
	// counts it in the Unmatched queue once (CORE-2194).
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"01.mp3", "03.mp3"} {
				writeScanFile(t, root, name, []byte(name))
			}
			st := storetest.Open(t)
			req := Request{LibraryID: partsLibrary(t, st), Root: root}
			parsers := map[string]FilenameParser{"parts": cancellingPartsParser{}}
			identifier := queueAllIdentifier{st}
			started := time.Now().UTC()
			var checkpoint jobCheckpoint
			yieldAtFirstReport := func(c jobCheckpoint, _ store.JobProgress) error {
				checkpoint = c
				return jobs.ErrYielded
			}
			if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, started, yieldAtFirstReport); !errors.Is(err, jobs.ErrYielded) {
				t.Fatalf("first pass did not yield: %v", err)
			}
			before, err := st.MediaFilesForLibrary(context.Background(), req.LibraryID)
			if err != nil || len(before) != 2 || before[0].MediaID != before[1].MediaID {
				t.Fatalf("parts are not one Media: %+v %v", before, err)
			}
			resumed, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, checkpoint, started, nil)
			if err != nil {
				t.Fatalf("resumed scan: %v", err)
			}
			// The resume re-imports the unreported parts as a group of their
			// own, on the same Media (CORE-2195 tracks keeping the group whole).
			files, err := st.MediaFilesForLibrary(context.Background(), req.LibraryID)
			if err != nil || len(files) == 0 || files[0].MediaID != before[0].MediaID {
				t.Fatalf("resumed parts left the Media: %+v %v", files, err)
			}
			if queued, err := st.IsUnmatched(context.Background(), before[0].MediaID); err != nil || !queued {
				t.Fatalf("Media not in the Unmatched queue: %v %v", queued, err)
			}
			if resumed.MediaImported != 1 || resumed.MediaUnmatched != 1 {
				t.Fatalf("resumed scan imported=%d unmatched=%d, want 1 and 1", resumed.MediaImported, resumed.MediaUnmatched)
			}
		})
	}
}

func TestScanSummaryCountsUnmatchedVersionsOnce(t *testing.T) {
	// feature:scan-pipeline
	// Two Versions of one unmatched title count once in the Unmatched queue,
	// however they reach the catalog and wherever the scan resumes: on the
	// anchor file, imported or attached (CORE-2194).
	names := []string{"Title (2000) - 1080p.mkv", "Title (2000) - 4K.mkv"}
	for _, workers := range []int{1, 4} {
		for _, mode := range []string{"new", "unchanged", "changed"} {
			t.Run(fmt.Sprintf("workers=%d/%s", workers, mode), func(t *testing.T) {
				root := t.TempDir()
				for _, name := range names {
					writeScanFile(t, root, name, []byte(name))
				}
				st := storetest.Open(t)
				req := Request{LibraryID: partsLibrary(t, st), Root: root}
				parsers := map[string]FilenameParser{"parts": labelledTitleParser{}}
				identifier := queueAllIdentifier{st}
				if mode != "new" {
					if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, time.Now().UTC(), nil); err != nil {
						t.Fatalf("first scan: %v", err)
					}
				}
				if mode == "changed" {
					for _, name := range names {
						writeScanFile(t, root, name, []byte(name+" edited"))
					}
				}
				started := time.Now().UTC()
				cancelAtFirstReport := func(jobCheckpoint, store.JobProgress) error { return context.Canceled }
				result, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, started, cancelAtFirstReport)
				if !errors.Is(err, context.Canceled) || result.interrupted == nil {
					t.Fatalf("cancelled scan: err=%v interrupted=%v", err, result.interrupted)
				}
				resumed, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, *result.interrupted, started, nil)
				if err != nil {
					t.Fatalf("resumed scan: %v", err)
				}
				files, err := st.MediaFilesForLibrary(context.Background(), req.LibraryID)
				if err != nil || len(files) != 2 || files[0].MediaID != files[1].MediaID {
					t.Fatalf("versions are not one Media: %+v %v", files, err)
				}
				if resumed.FilesSeen != 2 || resumed.MediaImported != 1 || resumed.MediaUnmatched != 1 {
					t.Fatalf("resumed scan seen=%d imported=%d unmatched=%d, want 2, 1 and 1", resumed.FilesSeen, resumed.MediaImported, resumed.MediaUnmatched)
				}
			})
		}
	}
}

// reversedPartsParser numbers the parts against walk order: b.mp3 is part 1
// and a.mp3 part 2.
type reversedPartsParser struct{}

func (reversedPartsParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	switch filepath.Base(path) {
	case "b.mp3":
		return ParsedMedia{Title: "Group", Class: "collection", PartGroup: "g", Index: 1}, true
	case "a.mp3":
		return ParsedMedia{Title: "Group", Class: "collection", PartGroup: "g", Index: 2}, true
	}
	return ParsedMedia{}, false
}

func TestScanSummaryCountsUnmatchedPartGroupAgainstWalkOrder(t *testing.T) {
	// feature:scan-pipeline
	// Parts report in part order, which need not be walk order. A scan
	// yielded after reporting the walk-last part never walks the group again,
	// so the group's Unmatched count must ride on that part (CORE-2194).
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"a.mp3", "b.mp3"} {
				writeScanFile(t, root, name, []byte(name))
			}
			st := storetest.Open(t)
			req := Request{LibraryID: partsLibrary(t, st), Root: root}
			parsers := map[string]FilenameParser{"parts": reversedPartsParser{}}
			identifier := queueAllIdentifier{st}
			started := time.Now().UTC()
			var checkpoint jobCheckpoint
			yieldAtFirstReport := func(c jobCheckpoint, _ store.JobProgress) error {
				checkpoint = c
				return jobs.ErrYielded
			}
			if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, started, yieldAtFirstReport); !errors.Is(err, jobs.ErrYielded) {
				t.Fatalf("first pass did not yield: %v", err)
			}
			resumed, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, checkpoint, started, nil)
			if err != nil {
				t.Fatalf("resumed scan: %v", err)
			}
			if resumed.FilesSeen != 2 || resumed.MediaImported != 1 || resumed.MediaUnmatched != 1 {
				t.Fatalf("resumed scan seen=%d imported=%d unmatched=%d, want 2, 1 and 1", resumed.FilesSeen, resumed.MediaImported, resumed.MediaUnmatched)
			}
		})
	}
}

func TestScanSummaryCountsUnmatchedVersionAddedBeforeAnchor(t *testing.T) {
	// feature:scan-pipeline
	// A new Version that sorts before its cataloged, unmatched title joins it
	// and carries the count. A resume re-seeds the anchor from the catalog,
	// now label-first, so the anchor cannot decide who counts: the title
	// still counts once (CORE-2194).
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			writeScanFile(t, root, "Title (2000) - 4K.mkv", []byte("4K"))
			st := storetest.Open(t)
			req := Request{LibraryID: partsLibrary(t, st), Root: root}
			parsers := map[string]FilenameParser{"parts": labelledTitleParser{}}
			identifier := queueAllIdentifier{st}
			if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, time.Now().UTC(), nil); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			writeScanFile(t, root, "Title (2000) - 1080p.mkv", []byte("1080p"))
			started := time.Now().UTC()
			var checkpoint jobCheckpoint
			yieldAtFirstReport := func(c jobCheckpoint, _ store.JobProgress) error {
				checkpoint = c
				return jobs.ErrYielded
			}
			if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, started, yieldAtFirstReport); !errors.Is(err, jobs.ErrYielded) {
				t.Fatalf("scan did not yield: %v", err)
			}
			resumed, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, checkpoint, started, nil)
			if err != nil {
				t.Fatalf("resumed scan: %v", err)
			}
			files, err := st.MediaFilesForLibrary(context.Background(), req.LibraryID)
			if err != nil || len(files) != 2 || files[0].MediaID != files[1].MediaID {
				t.Fatalf("versions are not one Media: %+v %v", files, err)
			}
			if resumed.FilesSeen != 2 || resumed.MediaImported != 1 || resumed.MediaUnmatched != 1 {
				t.Fatalf("resumed scan seen=%d imported=%d unmatched=%d, want 2, 1 and 1", resumed.FilesSeen, resumed.MediaImported, resumed.MediaUnmatched)
			}
		})
	}
}

func TestScanSummaryCountsVersionQueuedAfterEarlierAttach(t *testing.T) {
	// feature:scan-pipeline
	// A new Version attaching to a cataloged title that is not yet Unmatched
	// records nothing, so it must not take the count: the title's own file
	// then retries identification, enters the queue and counts it (CORE-2194).
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			writeScanFile(t, root, "Title (2000) - 4K.mkv", []byte("4K"))
			st := storetest.Open(t)
			req := Request{LibraryID: partsLibrary(t, st), Root: root}
			parsers := map[string]FilenameParser{"parts": labelledTitleParser{}}
			if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, time.Now().UTC(), nil); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			writeScanFile(t, root, "Title (2000) - 1080p.mkv", []byte("1080p"))
			result, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, queueAllIdentifier{st}, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, time.Now().UTC(), nil)
			if err != nil {
				t.Fatalf("rescan: %v", err)
			}
			files, err := st.MediaFilesForLibrary(context.Background(), req.LibraryID)
			if err != nil || len(files) != 2 || files[0].MediaID != files[1].MediaID {
				t.Fatalf("versions are not one Media: %+v %v", files, err)
			}
			if queued, err := st.IsUnmatched(context.Background(), files[0].MediaID); err != nil || !queued {
				t.Fatalf("Media is not Unmatched: %v %v", queued, err)
			}
			if result.MediaUnmatched != 1 {
				t.Fatalf("rescan unmatched=%d, want 1", result.MediaUnmatched)
			}
		})
	}
}

func TestScanSummaryCountsVersionsAcrossDirectoriesOnceOnResume(t *testing.T) {
	// feature:scan-pipeline
	// Versions of one Media can sit in different directories once a file
	// moves. A resume that starts in the second directory still knows the
	// first directory's file carried the count (CORE-2194).
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			writeScanFile(t, root, filepath.Join("a", "Title (2000) - 1080p.mkv"), []byte("1080p"))
			writeScanFile(t, root, filepath.Join("a", "Title (2000) - 4K.mkv"), []byte("4K"))
			st := storetest.Open(t)
			req := Request{LibraryID: partsLibrary(t, st), Root: root}
			parsers := map[string]FilenameParser{"parts": labelledTitleParser{}}
			identifier := queueAllIdentifier{st}
			scan := func(checkpoint jobCheckpoint, started time.Time, progress func(jobCheckpoint, store.JobProgress) error) (Result, error) {
				return scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, checkpoint, started, progress)
			}
			if _, err := scan(jobCheckpoint{}, time.Now().UTC(), nil); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(root, "b"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(root, "a", "Title (2000) - 4K.mkv"), filepath.Join(root, "b", "Title (2000) - 4K.mkv")); err != nil {
				t.Fatal(err)
			}
			whole, err := scan(jobCheckpoint{}, time.Now().UTC(), nil)
			if err != nil {
				t.Fatalf("move scan: %v", err)
			}
			files, err := st.MediaFilesForLibrary(context.Background(), req.LibraryID)
			if err != nil || len(files) != 2 || files[0].MediaID != files[1].MediaID || filepath.Dir(files[0].Path) == filepath.Dir(files[1].Path) {
				t.Fatalf("moved Version is not on the same Media in another directory: %+v %v", files, err)
			}
			if whole.MediaUnmatched != 1 {
				t.Fatalf("move scan unmatched=%d, want 1", whole.MediaUnmatched)
			}
			started := time.Now().UTC()
			var checkpoint jobCheckpoint
			yieldAtFirstReport := func(c jobCheckpoint, _ store.JobProgress) error {
				checkpoint = c
				return jobs.ErrYielded
			}
			if _, err := scan(jobCheckpoint{}, started, yieldAtFirstReport); !errors.Is(err, jobs.ErrYielded) {
				t.Fatalf("scan did not yield: %v", err)
			}
			resumed, err := scan(checkpoint, started, nil)
			if err != nil {
				t.Fatalf("resumed scan: %v", err)
			}
			if resumed.FilesSeen != 2 || resumed.MediaImported != 1 || resumed.MediaUnmatched != 1 {
				t.Fatalf("resumed scan seen=%d imported=%d unmatched=%d, want 2, 1 and 1", resumed.FilesSeen, resumed.MediaImported, resumed.MediaUnmatched)
			}
		})
	}
}

func TestScanSummaryDropsVersionClearedByLaterIdentify(t *testing.T) {
	// feature:scan-pipeline
	// A new Version attaching to an Unmatched title must not count it when the
	// title's own changed file is identified later in the same scan and leaves
	// the queue; a stopped and resumed scan counts the queue state it leaves
	// (CORE-2194).
	for _, workers := range []int{1, 4} {
		for _, stop := range []string{"none", "yield", "cancel"} {
			t.Run(fmt.Sprintf("workers=%d/%s", workers, stop), func(t *testing.T) {
				root := t.TempDir()
				anchor := writeScanFile(t, root, "Title (2000) - 4K.mkv", []byte("4K"))
				st := storetest.Open(t)
				req := Request{LibraryID: partsLibrary(t, st), Root: root}
				parsers := map[string]FilenameParser{"parts": labelledTitleParser{}}
				if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, queueAllIdentifier{st}, nil, nil, fixedGovernor(workers), nil, req, jobCheckpoint{}, time.Now().UTC(), nil); err != nil {
					t.Fatalf("first scan: %v", err)
				}
				writeScanFile(t, root, "Title (2000) - 1080p.mkv", []byte("1080p"))
				changed := time.Now().Add(-30 * time.Minute)
				if err := os.Chtimes(anchor, changed, changed); err != nil {
					t.Fatal(err)
				}
				identifier := clearingIdentifier{st}
				scan := func(checkpoint jobCheckpoint, started time.Time, progress func(jobCheckpoint, store.JobProgress) error) (Result, error) {
					return scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, checkpoint, started, progress)
				}
				started := time.Now().UTC()
				var result Result
				var err error
				switch stop {
				case "none":
					result, err = scan(jobCheckpoint{}, started, nil)
				case "yield":
					var checkpoint jobCheckpoint
					yieldAtFirstReport := func(c jobCheckpoint, _ store.JobProgress) error {
						checkpoint = c
						return jobs.ErrYielded
					}
					if _, err := scan(jobCheckpoint{}, started, yieldAtFirstReport); !errors.Is(err, jobs.ErrYielded) {
						t.Fatalf("scan did not yield: %v", err)
					}
					result, err = scan(checkpoint, started, nil)
				case "cancel":
					cancelAtFirstReport := func(jobCheckpoint, store.JobProgress) error { return context.Canceled }
					cancelled, cancelErr := scan(jobCheckpoint{}, started, cancelAtFirstReport)
					if !errors.Is(cancelErr, context.Canceled) || cancelled.interrupted == nil {
						t.Fatalf("cancelled scan: err=%v interrupted=%v", cancelErr, cancelled.interrupted)
					}
					result, err = scan(*cancelled.interrupted, started, nil)
				}
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
				files, err := st.MediaFilesForLibrary(context.Background(), req.LibraryID)
				if err != nil || len(files) != 2 || files[0].MediaID != files[1].MediaID {
					t.Fatalf("versions are not one Media: %+v %v", files, err)
				}
				// A resume re-seeds the anchor label-first, so the 4K may then
				// attach as a Version without being identified again: the count
				// follows whatever queue state the scan leaves.
				queued, err := st.IsUnmatched(context.Background(), files[0].MediaID)
				if err != nil || (stop == "none" && queued) {
					t.Fatalf("Media is still Unmatched: %v %v", queued, err)
				}
				want := 0
				if queued {
					want = 1
				}
				if result.MediaImported != 1 || result.MediaUnmatched != want {
					t.Fatalf("scan imported=%d unmatched=%d, want 1 and %d", result.MediaImported, result.MediaUnmatched, want)
				}
			})
		}
	}
}

func TestScanSummaryKeepsVersionCountOnCompleteCheckpointRecovery(t *testing.T) {
	// feature:scan-pipeline
	// A scan job saves its complete checkpoint before the job completes. A job
	// reclaimed after a crash in between resumes from that checkpoint and must
	// not count its Unmatched Version Media again (CORE-2194).
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"Title (2000) - 1080p.mkv", "Title (2000) - 4K.mkv"} {
				writeScanFile(t, root, name, []byte(name))
			}
			st := storetest.Open(t)
			req := Request{LibraryID: partsLibrary(t, st), Root: root}
			parsers := map[string]FilenameParser{"parts": labelledTitleParser{}}
			identifier := queueAllIdentifier{st}
			started := time.Now().UTC()
			scan := func(checkpoint jobCheckpoint) (Result, error) {
				return scanFromCheckpoint(context.Background(), st, nil, nil, parsers, nil, identifier, nil, nil, fixedGovernor(workers), nil, req, checkpoint, started, nil)
			}
			whole, err := scan(jobCheckpoint{})
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if whole.FilesSeen != 2 || whole.MediaImported != 1 || whole.MediaUnmatched != 1 {
				t.Fatalf("scan seen=%d imported=%d unmatched=%d, want 2, 1 and 1", whole.FilesSeen, whole.MediaImported, whole.MediaUnmatched)
			}
			data, err := marshalScanCheckpoint(whole.checkpoint("complete"))
			if err != nil {
				t.Fatal(err)
			}
			saved, err := checkpointFromJob(data)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := scan(saved)
			if err != nil {
				t.Fatalf("recovered scan: %v", err)
			}
			if recovered.FilesSeen != 2 || recovered.MediaImported != 1 || recovered.MediaUnmatched != 1 {
				t.Fatalf("recovered scan seen=%d imported=%d unmatched=%d, want 2, 1 and 1", recovered.FilesSeen, recovered.MediaImported, recovered.MediaUnmatched)
			}
		})
	}
}

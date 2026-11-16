package scan

import (
	"context"
	"encoding/json"
	"os"
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
	done, err := st.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("status = %q error=%q, want completed", done.Status, done.Error)
	}
	events, err := st.ListJobEvents(ctx, job.ID)
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

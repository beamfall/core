package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// outcomeIdentifier reports the same non-applied outcome for every item.
type outcomeIdentifier struct {
	outcome IdentifyOutcome
}

func (o outcomeIdentifier) AutoIdentify(context.Context, store.MediaID, string, string, ParsedMedia) error {
	return nil
}

func (o outcomeIdentifier) AutoIdentifyOutcome(context.Context, store.MediaID, string, string, ParsedMedia) (IdentifyOutcome, error) {
	return o.outcome, nil
}

// captureIdentifyLogs routes slog to a JSON buffer for the test and returns the
// decoded records whose message is msg.
func captureIdentifyLogs(t *testing.T) func(msg string) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return func(msg string) []map[string]any {
		var records []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var record map[string]any
			if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == msg {
				records = append(records, record)
			}
		}
		return records
	}
}

// TestScanJobReportsUnidentifiableItemsOnce: items whose plugin has no
// scraper for them are skipped without a queue row, so the scan reports them:
// one warning for the whole scan, counted across a batch yield, with one
// example path, and the completed job's message names them for the Console.
func TestScanJobReportsUnidentifiableItemsOnce(t *testing.T) {
	// feature:scan-pipeline
	logs := captureIdentifyLogs(t)
	root := t.TempDir()
	files := scanJobFileBatchSize + 3
	for i := 0; i < files; i++ {
		writeScanFile(t, root, "clip-"+zeroPad(i)+".mp4", []byte("clip "+zeroPad(i)))
	}
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Clips", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(st, nil, nil, nil, nil, outcomeIdentifier{outcome: IdentifyOutcome{Reason: IdentifySkippedNoScraper}})
	runSummaryScanToCompletion(t, st, handler, "job-unidentifiable", library.ID, root, 2)

	warnings := logs("scan: items cannot be identified; the plugin has no scraper for them")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d (%v), want one for the scan", len(warnings), warnings)
	}
	warning := warnings[0]
	if warning["level"] != "WARN" || warning["count"] != float64(files) || warning["plugin"] != "filmtv" ||
		warning["reason"] != IdentifySkippedNoScraper || !strings.HasPrefix(warning["example"].(string), root) {
		t.Fatalf("warning = %v, want WARN count=%d plugin=filmtv with an example under the root", warning, files)
	}
	done, err := st.Job(context.Background(), "job-unidentifiable")
	if err != nil {
		t.Fatal(err)
	}
	want := "Scan complete; " + strconv.Itoa(files) + " unclassified items not identified: plugin filmtv has no scraper for them"
	if done.Progress.Message != want {
		t.Fatalf("message = %q, want %q", done.Progress.Message, want)
	}
}

// TestScanLogsQueuedItemsAsInformation: below-threshold items land in the
// Unmatched queue, which is expected, so they log once at information level
// and leave the completion message alone.
func TestScanLogsQueuedItemsAsInformation(t *testing.T) {
	// feature:scan-pipeline
	logs := captureIdentifyLogs(t)
	root := t.TempDir()
	writeScanFile(t, root, "clip-a.mp4", []byte("clip a"))
	writeScanFile(t, root, "clip-b.mp4", []byte("clip b"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Clips", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	identifier := outcomeIdentifier{outcome: IdentifyOutcome{Reason: IdentifyQueuedBelowThreshold, ScraperID: "test-scraper"}}
	result, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, identifier, nil, nil, nil,
		Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	want := []IdentifyGroup{{Plugin: "filmtv", Scraper: "test-scraper", Reason: IdentifyQueuedBelowThreshold, Count: 2, Example: result.Unidentified[0].Example}}
	if len(result.Unidentified) != 1 || result.Unidentified[0] != want[0] || !strings.HasPrefix(want[0].Example, root) {
		t.Fatalf("unidentified = %+v, want %+v", result.Unidentified, want)
	}
	infos := logs("scan: items not identified")
	if len(infos) != 1 || infos[0]["level"] != "INFO" || infos[0]["count"] != float64(2) {
		t.Fatalf("info logs = %v, want one INFO line with count 2", infos)
	}
	if got := unidentifiableMessage(result.Unidentified); got != "" {
		t.Fatalf("message suffix = %q, want none for queued items", got)
	}
}

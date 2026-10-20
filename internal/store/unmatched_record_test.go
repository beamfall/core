package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestUnmatchedRecordRoundTrip covers the Media-INDEPENDENT Unmatched Record
// (ADR-0068 Amendment §F/§G): QueueUnmatchedRecord mints an opaque id and appends
// a reviewable row with no Media required, UnmatchedRecords lists pending rows,
// and ResolveUnmatchedRecord stamps resolution idempotently and drops the row
// from the pending inbox.
func TestUnmatchedRecordRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	opsFilter := LibraryFilter{AllLibraries: true, Revealed: true}

	// A Library-less import record — no Media, no Library (the shape the
	// Media-keyed queue cannot represent).
	id, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{
		Origin:        UnmatchedOriginImport,
		ImportRunID:   "run-1",
		ImporterID:    "filmtv",
		SourceHandle:  "/import/mystery.mkv",
		FailureReason: UnmatchedReasonNoCandidates,
	})
	if err != nil {
		t.Fatalf("QueueUnmatchedRecord: %v", err)
	}
	if !strings.HasPrefix(id, "urec-") {
		t.Fatalf("minted id = %q, want urec- prefix", id)
	}

	records, err := st.UnmatchedRecords(ctx, opsFilter)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("UnmatchedRecords = %d rows, want 1", len(records))
	}
	got := records[0]
	if got.ID != id || got.Origin != UnmatchedOriginImport || got.ImporterID != "filmtv" {
		t.Fatalf("record round-trip lost fields: %#v", got)
	}
	if got.SourceHandle != "/import/mystery.mkv" || got.FailureReason != UnmatchedReasonNoCandidates {
		t.Fatalf("record lost handle/reason: %#v", got)
	}
	if got.MediaID != "" || got.LibraryID != "" || got.Resolved() {
		t.Fatalf("Library-less pending record should be Media/Library-empty and unresolved: %#v", got)
	}

	// A second call appends a distinct row (audit trail, not a Media-keyed UPSERT).
	if _, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{
		Origin:        UnmatchedOriginImport,
		ImportRunID:   "run-1",
		ImporterID:    "filmtv",
		SourceHandle:  "/import/other.mkv",
		FailureReason: UnmatchedReasonExternalIDConflict,
	}); err != nil {
		t.Fatalf("second QueueUnmatchedRecord: %v", err)
	}
	if records, err = st.UnmatchedRecords(ctx, opsFilter); err != nil {
		t.Fatal(err)
	} else if len(records) != 2 {
		t.Fatalf("two distinct records should accumulate, got %d", len(records))
	}

	// Resolve the first record (associate-vs-create: here a pure resolve, no Media).
	if ok, err := st.ResolveUnmatchedRecord(ctx, id, "", time.Now()); err != nil || !ok {
		t.Fatalf("ResolveUnmatchedRecord: ok=%v err=%v", ok, err)
	}
	// Idempotent: a second resolve is a safe no-op that still reports found.
	if ok, err := st.ResolveUnmatchedRecord(ctx, id, "", time.Now()); err != nil || !ok {
		t.Fatalf("ResolveUnmatchedRecord (repeat): ok=%v err=%v", ok, err)
	}
	if ok, err := st.ResolveUnmatchedRecord(ctx, id, "media-alpha", time.Now()); err != nil || ok {
		t.Fatalf("ResolveUnmatchedRecord (conflict): ok=%v err=%v, want false nil", ok, err)
	}
	// Resolving an unknown id reports not-found, not an error.
	if ok, err := st.ResolveUnmatchedRecord(ctx, "urec-nonesuch", "", time.Now()); err != nil || ok {
		t.Fatalf("ResolveUnmatchedRecord(absent): ok=%v err=%v", ok, err)
	}
	resolved, err := st.UnmatchedRecordByID(ctx, id, opsFilter)
	if err != nil {
		t.Fatal(err)
	}
	again, err := st.UnmatchedRecordByID(ctx, id, opsFilter)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != again || resolved.ID != id || !resolved.Resolved() {
		t.Fatalf("resolved record read mutated or lost source row: first=%#v second=%#v", resolved, again)
	}

	// Resolved records drop out of the pending inbox; only the second remains.
	if records, err = st.UnmatchedRecords(ctx, opsFilter); err != nil {
		t.Fatal(err)
	} else if len(records) != 1 || records[0].SourceHandle != "/import/other.mkv" {
		t.Fatalf("pending inbox after resolve = %#v, want only the unresolved record", records)
	}
}

func TestCreateManualMediaFromUnmatchedSerializesConflictingRequests(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	recordID, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{
		Origin:        UnmatchedOriginScan,
		LibraryID:     "library-local",
		SourceHandle:  "/media/concurrent.mkv",
		FailureReason: UnmatchedReasonNoCandidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	type request struct {
		library LibraryID
		title   string
		digest  string
	}
	requests := []request{
		{library: "library-local", title: "First Identity", digest: strings.Repeat("a", 64)},
		{library: "library-archive", title: "Conflicting Identity", digest: strings.Repeat("b", 64)},
	}
	type result struct {
		mediaID MediaID
		err     error
	}
	results := make([]result, len(requests))
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(len(requests))
	var workers sync.WaitGroup
	for i, req := range requests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready.Done()
			<-start
			results[i].mediaID, results[i].err = st.CreateManualMediaFromUnmatched(
				ctx, recordID, req.library, req.title, 2001, req.digest, time.Unix(100, 0),
			)
		}()
	}
	ready.Wait()
	close(start)
	workers.Wait()

	var winningRequest request
	var losingRequest request
	var winningMediaID MediaID
	winnerFound := false
	for i, result := range results {
		switch {
		case result.err == nil:
			if winnerFound {
				t.Fatalf("both conflicting requests succeeded: %#v", results)
			}
			winnerFound = true
			winningRequest = requests[i]
			winningMediaID = result.mediaID
		case !errors.Is(result.err, ErrUnmatchedCreateConflict):
			t.Fatalf("request %d error = %v, want conflict", i, result.err)
		default:
			losingRequest = requests[i]
		}
	}
	if !winnerFound {
		t.Fatalf("no request succeeded: %#v", results)
	}
	replayedID, err := st.CreateManualMediaFromUnmatched(
		ctx,
		recordID,
		winningRequest.library,
		winningRequest.title,
		2001,
		winningRequest.digest,
		time.Unix(200, 0),
	)
	if err != nil || replayedID != winningMediaID {
		t.Fatalf("identical replay media=%q err=%v, want %q", replayedID, err, winningMediaID)
	}
	if _, err := st.CreateManualMediaFromUnmatched(
		ctx,
		recordID,
		losingRequest.library,
		losingRequest.title,
		2001,
		losingRequest.digest,
		time.Unix(200, 0),
	); !errors.Is(err, ErrUnmatchedCreateConflict) {
		t.Fatalf("conflicting replay error = %v, want %v", err, ErrUnmatchedCreateConflict)
	}
	record, err := st.UnmatchedRecordByID(
		ctx,
		recordID,
		LibraryFilter{AllLibraries: true, Revealed: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Resolved() || record.ResolvedMediaID != winningMediaID {
		t.Fatalf("source record was not retained against winning Media: %#v", record)
	}
	if _, err := st.Media(ctx, winningMediaID, LibraryFilter{AllLibraries: true, Revealed: true}); err != nil {
		t.Fatalf("winning Media was not retained: %v", err)
	}
}

// TestUnmatchedRecordLibraryVisibility asserts the fail-closed visibility posture
// (ADR-0068 Amendment §F): a Library-less operator-global record is visible only
// to an all-Libraries scope, and a Library-scoped record obeys the reveal/Library
// filter like every other operator surface.
func TestUnmatchedRecordLibraryVisibility(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// A Library-less (operator-global) record.
	if _, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{
		Origin:        UnmatchedOriginImport,
		SourceHandle:  "/import/global.mkv",
		FailureReason: UnmatchedReasonNoCandidates,
	}); err != nil {
		t.Fatalf("QueueUnmatchedRecord(global): %v", err)
	}
	// A Library-scoped record on the seeded Library.
	if _, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{
		Origin:        UnmatchedOriginScan,
		LibraryID:     "library-local",
		SourceHandle:  "/scan/local.mkv",
		FailureReason: UnmatchedReasonBelowThreshold,
	}); err != nil {
		t.Fatalf("QueueUnmatchedRecord(scoped): %v", err)
	}

	// All-Libraries scope sees both.
	all, err := st.UnmatchedRecords(ctx, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all-Libraries scope = %d records, want 2", len(all))
	}

	// A partial grant on the local Library sees the scoped record but NOT the
	// operator-global one (fail-closed).
	partial, err := st.UnmatchedRecords(ctx, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial) != 1 || partial[0].SourceHandle != "/scan/local.mkv" {
		t.Fatalf("partial grant = %#v, want only the Library-scoped record", partial)
	}

	// A zero-grant session sees nothing (fails closed on both).
	none, err := st.UnmatchedRecords(ctx, LibraryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("zero-grant scope leaked records: %#v", none)
	}
}

func TestImportUnmatchedRecordRunIsolationAndAssociation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllLibraries: true, Revealed: true}
	queue := func(runID, handle string) string {
		t.Helper()
		id, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{
			Origin:        UnmatchedOriginImport,
			ImportRunID:   runID,
			ImporterID:    "demo-importer",
			SourceHandle:  handle,
			FailureReason: UnmatchedReasonNoCandidates,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	runOneID := queue("run-1", "/private/run-one.mkv")
	queue("run-2", "/private/run-two.mkv")

	records, err := st.UnmatchedRecordsForImportRun(ctx, "run-1", filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != runOneID {
		t.Fatalf("run-1 records = %#v, want only %q", records, runOneID)
	}
	if ok, err := st.AssociateImportUnmatchedRecord(ctx, runOneID, "run-2", "media-alpha", filter, time.Now()); err != nil || ok {
		t.Fatalf("cross-run association: ok=%v err=%v, want rejection", ok, err)
	}
	if ok, err := st.AssociateImportUnmatchedRecord(ctx, runOneID, "run-1", "media-alpha", filter, time.Now()); err != nil || !ok {
		t.Fatalf("association: ok=%v err=%v", ok, err)
	}
	if ok, err := st.AssociateImportUnmatchedRecord(ctx, runOneID, "run-1", "media-alpha", filter, time.Now()); err != nil || !ok {
		t.Fatalf("association retry: ok=%v err=%v", ok, err)
	}
	if ok, err := st.AssociateImportUnmatchedRecord(ctx, runOneID, "run-1", "media-archive", filter, time.Now()); err != nil || ok {
		t.Fatalf("conflicting association: ok=%v err=%v, want rejection", ok, err)
	}
	var resolvedMediaID string
	if err := st.catalogR.QueryRowContext(ctx,
		"SELECT resolved_media_id FROM unmatched_record WHERE id = ?", runOneID).Scan(&resolvedMediaID); err != nil {
		t.Fatal(err)
	}
	if resolvedMediaID != "media-alpha" {
		t.Fatalf("resolved_media_id = %q, want media-alpha", resolvedMediaID)
	}
	records, err = st.UnmatchedRecordsForImportRun(ctx, "run-1", filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("resolved record remained pending: %#v", records)
	}
}

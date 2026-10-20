package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestImportStatusUnmatchedRecordsAreRunScopedAndRedacted(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := t.Context()
	runOne, err := st.EnqueueJob(ctx, store.Job{
		Kind: migrationJobKind, DedupeKey: "run-one", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	runTwo, err := st.EnqueueJob(ctx, store.Job{
		Kind: migrationJobKind, DedupeKey: "run-two", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := func(runID, handle string) string {
		t.Helper()
		id, err := st.QueueUnmatchedRecord(ctx, store.UnmatchedRecord{
			Origin:         store.UnmatchedOriginImport,
			ImportRunID:    runID,
			ImporterID:     "demo-importer",
			SourceHandle:   handle,
			CandidateID:    "candidate-1",
			CandidateTitle: "Safe Candidate",
			FailureReason:  store.UnmatchedReasonNoCandidates,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	runOneRecord := queue(string(runOne.ID), "/private/Restricted Archive/run-one.mkv")
	queue(string(runTwo.ID), "/private/Restricted Archive/run-two.mkv")

	status, err := handler.importStatus(ctx, managerScope(), request{
		Variables: map[string]any{"jobID": string(runOne.ID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.UnmatchedRecords) != 1 || status.UnmatchedRecords[0].ID != runOneRecord {
		t.Fatalf("unmatched records = %#v, want only run-one record %q", status.UnmatchedRecords, runOneRecord)
	}
	wire, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"/private/", "Restricted Archive", "library-archive", "sourceHandle"} {
		if strings.Contains(string(wire), secret) {
			t.Fatalf("import status leaked %q: %s", secret, wire)
		}
	}

	hidden, err := handler.importStatus(ctx, auth.Scope{AllLibraries: true}, request{
		Variables: map[string]any{"jobID": string(runOne.ID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.UnmatchedRecords) != 0 {
		t.Fatalf("unrevealed status leaked records: %#v", hidden.UnmatchedRecords)
	}
}

func TestImportStatusRejectsUnauthorizedRequest(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewBufferString(
		`{"query":"query { importStatus(jobID:\"job-nope\") { id unmatchedRecords { id } } }"}`,
	))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

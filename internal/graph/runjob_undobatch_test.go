package graph

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// feature:operations — runJob (graph.go runJob resolver, Console group
// admin-session-gated, ADR-0007 §4 Operations) and undoBatch (graph_bulkedit.go,
// §3 Manage group still manager-gated). These harden the thin happy-path
// coverage: negative auth, an observable completion signal for runJob, and a
// full apply→undo→restore round-trip plus destructive idempotency for undoBatch.

// TestRunJobRejectsNonAdmin confirms a session without an open Admin Session
// cannot drive the job runner. The Console gate covers jobs/job/runJob together,
// so a non-admin runJob must fail with the real "admin session required" string
// and must NOT have invoked the runner.
func TestRunJobRejectsNonAdmin(t *testing.T) {
	handler := newTestHandler(t)
	runner := &recordingJobRunner{job: store.Job{ID: "job-x", Kind: "scan", Status: store.JobStatusCompleted}}
	handler.SetJobRunner(runner)

	body := []byte(`{"query":"mutation { runJob { id status } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", resp.Errors)
	}
	// The gate must short-circuit before the runner is touched.
	if runner.called {
		t.Fatal("job runner was invoked for a non-admin session")
	}
}

// TestRunJobRunsAndSignalsCompletion asserts the effect of an admin runJob: the
// runner actually ran (the completion signal, per the determinism note — NOT a
// wall-clock timestamp) and the resolver returned that job's terminal status.
func TestRunJobRunsAndSignalsCompletion(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	runner := &recordingJobRunner{job: store.Job{ID: "job-done", Kind: "scan", Status: store.JobStatusCompleted}}
	handler.SetJobRunner(runner)

	body := []byte(`{"query":"mutation { runJob { id kind status } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data struct {
			RunJob JobInfo `json:"runJob"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	// Completion signal: the runner was driven exactly through the resolver.
	if !runner.called {
		t.Fatal("runJob did not invoke the job runner")
	}
	if resp.Data.RunJob.ID != "job-done" || resp.Data.RunJob.Status != string(store.JobStatusCompleted) {
		t.Fatalf("runJob = %#v, want job-done completed", resp.Data.RunJob)
	}
}

func TestRunEventsCompactionEnqueuesDurableJob(t *testing.T) {
	// feature:operations
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	body := []byte(`{"query":"mutation { runEventsCompaction { id kind status } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data struct {
			RunEventsCompaction JobInfo `json:"runEventsCompaction"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.RunEventsCompaction.Kind != "events-compaction" || resp.Data.RunEventsCompaction.Status != string(store.JobStatusQueued) {
		t.Fatalf("runEventsCompaction = %#v, want queued events-compaction", resp.Data.RunEventsCompaction)
	}

	events, err := st.ListJobEvents(t.Context(), store.JobID(resp.Data.RunEventsCompaction.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Type != "queued" {
		t.Fatalf("job events = %#v, want durable queued audit row", events)
	}
}

func TestJobCancelRetryAndEventsOverGraphQL(t *testing.T) {
	// feature:operations
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(t.Context(), store.Job{ID: "job-cancel", Kind: "scan", Class: store.JobClassBackground, Payload: []byte(`{"library_id":"library-local"}`), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cancel := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($id:String!){ jobCancel(id:$id){ id status } }","variables":{"id":"job-cancel"}}`)
	var cancelResp struct {
		Data struct {
			JobCancel JobInfo `json:"jobCancel"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(cancel, &cancelResp); err != nil {
		t.Fatalf("decode cancel: %v (%s)", err, cancel)
	}
	if len(cancelResp.Errors) != 0 || cancelResp.Data.JobCancel.Status != string(store.JobStatusCanceled) {
		t.Fatalf("cancel = %#v errors=%#v", cancelResp.Data.JobCancel, cancelResp.Errors)
	}
	retry := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($id:String!){ jobRetry(id:$id){ id kind status } }","variables":{"id":"job-cancel"}}`)
	var retryResp struct {
		Data struct {
			JobRetry JobInfo `json:"jobRetry"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(retry, &retryResp); err != nil {
		t.Fatalf("decode retry: %v (%s)", err, retry)
	}
	if len(retryResp.Errors) != 0 || retryResp.Data.JobRetry.ID == "" || retryResp.Data.JobRetry.ID == "job-cancel" || retryResp.Data.JobRetry.Status != string(store.JobStatusQueued) {
		t.Fatalf("retry = %#v errors=%#v", retryResp.Data.JobRetry, retryResp.Errors)
	}
	history := graphPost(t, handler, authorizeManager,
		`{"query":"query($id:String!){ jobEvents(id:$id){ id jobID occurredAt type payload } }","variables":{"id":"job-cancel"}}`)
	var historyResp struct {
		Data struct {
			JobEvents []JobEventInfo `json:"jobEvents"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(history, &historyResp); err != nil {
		t.Fatalf("decode history: %v (%s)", err, history)
	}
	if len(historyResp.Errors) != 0 || len(historyResp.Data.JobEvents) != 2 {
		t.Fatalf("jobEvents = %#v errors=%#v", historyResp.Data.JobEvents, historyResp.Errors)
	}
	if historyResp.Data.JobEvents[1].Type != "canceled" || historyResp.Data.JobEvents[1].Payload == "" {
		t.Fatalf("last event = %#v, want canceled with payload", historyResp.Data.JobEvents[1])
	}
}

type recordingGovernor struct{}

func (recordingGovernor) GenerationWorkers() int   { return 1 }
func (recordingGovernor) BackgroundCPUBudget() int { return 1 }
func (recordingGovernor) ActiveTranscodes() int    { return 1 }
func (recordingGovernor) ActiveGeneration() int    { return 1 }
func (recordingGovernor) GenerationAllowed() bool  { return false }

func TestOperationsGovernorStatusOverGraphQL(t *testing.T) {
	// feature:resource-governor
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	handler.SetGovernor(recordingGovernor{})
	openManagerAdminSession(t, st)
	body := graphPost(t, handler, authorizeManager,
		`{"query":"query { operationsGovernor { available generationWorkers backgroundCPUBudget activeTranscodes activeGeneration generationAllowed lowPowerMode transcodePreemptingBackground } }"}`)
	var resp struct {
		Data struct {
			OperationsGovernor OperationsGovernorInfo `json:"operationsGovernor"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	got := resp.Data.OperationsGovernor
	if !got.Available || !got.LowPowerMode || !got.TranscodePreemptingBackground || got.GenerationAllowed {
		t.Fatalf("operationsGovernor = %#v", got)
	}
}

// TestUndoBatchRejectsNonManager confirms the shared undoBatch verb is behind
// the group manager gate (graph.go:1091): a viewer is rejected with the real
// error string.
func TestUndoBatchRejectsNonManager(t *testing.T) {
	handler := newTestHandler(t)
	resp := graphPost(t, handler, authorizeViewer,
		`{"query":"mutation($batchID:String!){ undoBatch(batchID:$batchID){ batchID affected } }","variables":{"batchID":"batch-anything"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "manager role required" {
		t.Fatalf("errors = %#v, want manager role required", decoded.Errors)
	}
}

// TestUndoBatchRestoresBulkEditAndIsIdempotent is the dedicated round-trip:
// apply a bulk-edit batch, undo it by the returned id, and assert the ORIGINAL
// field value is fully restored (not merely "no error"). Then re-send the same
// undo — a destructive op must be idempotent: the second undo is rejected with
// "batch already undone" rather than double-reverting.
func TestUndoBatchRestoresBulkEditAndIsIdempotent(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	if err := st.SetMediaFields(t.Context(), "media-alpha", []store.MediaField{
		{Name: "genre", Type: store.EntityFieldText, Text: "original"},
	}); err != nil {
		t.Fatal(err)
	}

	// Apply the edit and capture the batch id.
	apply := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($mediaIDs:[String!]!,$fields:[FieldInput!]!){ bulkEditApply(mediaIDs:$mediaIDs, fields:$fields){ batchID kind affected } }","variables":{"mediaIDs":["media-alpha"],"fields":[{"name":"genre","type":"text","value":"changed"}]}}`)
	var applyResp struct {
		Data struct {
			BulkEditApply BatchResultInfo `json:"bulkEditApply"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(apply, &applyResp); err != nil {
		t.Fatalf("decode apply: %v (%s)", err, apply)
	}
	if len(applyResp.Errors) != 0 || applyResp.Data.BulkEditApply.Affected != 1 {
		t.Fatalf("apply = %#v errors=%#v", applyResp.Data.BulkEditApply, applyResp.Errors)
	}
	batchID := applyResp.Data.BulkEditApply.BatchID
	if batchID == "" {
		t.Fatal("apply returned empty batch id")
	}
	// Sanity: the edit actually landed before we undo it.
	fields, err := st.MediaFields(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Text != "changed" {
		t.Fatalf("after apply fields = %#v, want genre=changed", fields)
	}

	// First undo restores the original value.
	undo := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($batchID:String!){ undoBatch(batchID:$batchID){ batchID affected } }","variables":{"batchID":"`+batchID+`"}}`)
	var undoResp struct {
		Data struct {
			UndoBatch BatchResultInfo `json:"undoBatch"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(undo, &undoResp); err != nil {
		t.Fatalf("decode undo: %v (%s)", err, undo)
	}
	if len(undoResp.Errors) != 0 || undoResp.Data.UndoBatch.Affected != 1 {
		t.Fatalf("undo = %#v errors=%#v", undoResp.Data.UndoBatch, undoResp.Errors)
	}
	fields, err = st.MediaFields(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Text != "original" {
		t.Fatalf("after undo fields = %#v, want genre=original restored", fields)
	}

	// Idempotent re-send: undoing an already-undone batch is rejected, and the
	// restored state is left untouched (no double-revert).
	redo := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($batchID:String!){ undoBatch(batchID:$batchID){ batchID affected } }","variables":{"batchID":"`+batchID+`"}}`)
	var redoResp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(redo, &redoResp); err != nil {
		t.Fatalf("decode re-undo: %v (%s)", err, redo)
	}
	if len(redoResp.Errors) != 1 || !strings.Contains(redoResp.Errors[0].Message, "batch already undone") {
		t.Fatalf("re-undo errors = %#v, want batch already undone", redoResp.Errors)
	}
	fields, err = st.MediaFields(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Text != "original" {
		t.Fatalf("after re-undo fields = %#v, want genre=original unchanged", fields)
	}
}

// TestUndoBatchRestoresMerge round-trips a merge: merge a duplicate (which
// trashes the dropped Media), then undo by the returned batch id and assert the
// dropped Media is visible again — the original two-Media state is restored.
func TestUndoBatchRestoresMerge(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	dup := "0000000033330001"
	keep := importGraphFile(t, st, "library-local", filepath.Join(root, "keep.mp4"), "Keep", dup)
	drop := importGraphFile(t, st, "library-local", filepath.Join(root, "drop.mp4"), "Drop", dup)
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}

	merge := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($libraryID:String!,$keepMediaID:String!){ mergeMedia(libraryID:$libraryID, keepMediaID:$keepMediaID){ batchID kind affected } }","variables":{"libraryID":"library-local","keepMediaID":"`+string(keep)+`"}}`)
	var mergeResp struct {
		Data struct {
			MergeMedia BatchResultInfo `json:"mergeMedia"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(merge, &mergeResp); err != nil {
		t.Fatalf("decode merge: %v (%s)", err, merge)
	}
	if len(mergeResp.Errors) != 0 || mergeResp.Data.MergeMedia.Kind != "merge" || mergeResp.Data.MergeMedia.Affected != 1 {
		t.Fatalf("merge = %#v errors=%#v", mergeResp.Data.MergeMedia, mergeResp.Errors)
	}
	batchID := mergeResp.Data.MergeMedia.BatchID
	if batchID == "" {
		t.Fatal("merge returned empty batch id")
	}
	// Confirm the merge took effect: the dropped duplicate is gone.
	if _, err := st.Media(t.Context(), drop, filter); err == nil {
		t.Fatal("dropped duplicate still visible after merge")
	}

	// Undo restores the original two-Media state.
	undo := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($batchID:String!){ undoBatch(batchID:$batchID){ batchID kind affected } }","variables":{"batchID":"`+batchID+`"}}`)
	var undoResp struct {
		Data struct {
			UndoBatch BatchResultInfo `json:"undoBatch"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(undo, &undoResp); err != nil {
		t.Fatalf("decode undo: %v (%s)", err, undo)
	}
	if len(undoResp.Errors) != 0 {
		t.Fatalf("undo errors = %#v", undoResp.Errors)
	}
	// The dropped Media is restored to visibility (the observable effect).
	if _, err := st.Media(t.Context(), drop, filter); err != nil {
		t.Fatalf("dropped duplicate not restored after undo: %v", err)
	}
	if _, err := st.Media(t.Context(), keep, filter); err != nil {
		t.Fatalf("survivor missing after undo: %v", err)
	}
}

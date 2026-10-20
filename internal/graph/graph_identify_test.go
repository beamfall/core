package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

func TestFixMatchAssociatesImportRecord(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	recordID, err := st.QueueUnmatchedRecord(t.Context(), store.UnmatchedRecord{
		Origin:        store.UnmatchedOriginImport,
		LibraryID:     "library-local",
		ImportRunID:   "run-one",
		ImporterID:    "demo-importer",
		SourceHandle:  "/private/source.mkv",
		FailureReason: store.UnmatchedReasonNoCandidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := func(runID string) request {
		return request{Variables: map[string]any{
			"mediaID": "media-alpha", "recordID": recordID, "importRunID": runID,
		}}
	}

	if _, err := handler.fixMatch(t.Context(), managerScope(), req("run-two")); err == nil || !strings.Contains(err.Error(), "unknown unmatched record") {
		t.Fatalf("cross-run fixMatch error = %v, want unknown unmatched record", err)
	}
	if _, err := handler.fixMatch(t.Context(), auth.Scope{AllLibraries: true}, req("run-one")); err == nil || !strings.Contains(err.Error(), "unknown unmatched record") {
		t.Fatalf("unrevealed fixMatch error = %v, want unknown unmatched record", err)
	}
	if err := st.SetSessionRevealed(t.Context(), "session-manager", true); err != nil {
		t.Fatal(err)
	}
	body := `{"query":"mutation F($mediaID:String!,$recordID:String!,$importRunID:String!){ fixMatch(mediaID:$mediaID,recordID:$recordID,importRunID:$importRunID){ id } }","variables":{"mediaID":"media-alpha","recordID":"` + recordID + `","importRunID":"run-one"}}`
	data, errs := post(t, handler, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("fixMatch errors = %#v", errs)
	}
	var media MediaInfo
	if err := json.Unmarshal(data["fixMatch"], &media); err != nil {
		t.Fatal(err)
	}
	if media.ID != "media-alpha" {
		t.Fatalf("associated media = %q, want media-alpha", media.ID)
	}
	if _, errs := post(t, handler, body, authorizeManager); len(errs) != 0 {
		t.Fatalf("idempotent fixMatch retry errors = %#v", errs)
	}
	pending, err := st.UnmatchedRecordsForImportRun(t.Context(), "run-one", managerScope().LibraryFilter())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("resolved record remained pending: %#v", pending)
	}
}

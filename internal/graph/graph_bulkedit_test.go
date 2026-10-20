package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// feature:bulk-edit — the GraphQL surface: a manager-gated dry-run preview,
// apply returning a batch id, and undo, all routing through the audited store
// engine. These exercise the resolver + dispatch path end to end
// through the HTTP handler.

func TestBulkEditPreviewAndApplyOverGraphQL(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	if err := st.SetMediaFields(t.Context(), "media-alpha", []store.MediaField{
		{Name: "genre", Type: store.EntityFieldText, Text: "old"},
	}); err != nil {
		t.Fatal(err)
	}

	// Preview: returns the old→new change-set without mutating. The hand-rolled
	// resolver reads variables by argument name, so the variable keys match the
	// arguments (mediaIDs / fields) verbatim.
	preview := graphPost(t, handler, authorizeManager,
		`{"query":"query($mediaIDs:[String!]!,$fields:[FieldInput!]!){ bulkEditPreview(mediaIDs:$mediaIDs, fields:$fields){ mediaID old{ name value } new{ name value } } }","variables":{"mediaIDs":["media-alpha"],"fields":[{"name":"genre","type":"text","value":"new"}]}}`)
	var previewResp struct {
		Data struct {
			BulkEditPreview []BulkEditChangeInfo `json:"bulkEditPreview"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(preview, &previewResp); err != nil {
		t.Fatalf("decode preview: %v (%s)", err, preview)
	}
	if len(previewResp.Errors) != 0 {
		t.Fatalf("preview errors = %#v", previewResp.Errors)
	}
	if len(previewResp.Data.BulkEditPreview) != 1 || previewResp.Data.BulkEditPreview[0].MediaID != "media-alpha" {
		t.Fatalf("preview = %#v", previewResp.Data.BulkEditPreview)
	}
	got := previewResp.Data.BulkEditPreview[0]
	if len(got.Old) != 1 || got.Old[0].Value != "old" || len(got.New) != 1 || got.New[0].Value != "new" {
		t.Fatalf("preview change = %#v", got)
	}

	// Apply: returns a batch id and affected count; the value lands.
	apply := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($mediaIDs:[String!]!,$fields:[FieldInput!]!){ bulkEditApply(mediaIDs:$mediaIDs, fields:$fields){ batchID kind affected } }","variables":{"mediaIDs":["media-alpha"],"fields":[{"name":"genre","type":"text","value":"new"}]}}`)
	var applyResp struct {
		Data struct {
			BulkEditApply BatchResultInfo `json:"bulkEditApply"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(apply, &applyResp); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if len(applyResp.Errors) != 0 {
		t.Fatalf("apply errors = %#v", applyResp.Errors)
	}
	result := applyResp.Data.BulkEditApply
	if result.BatchID == "" || result.Kind != "bulk-edit" || result.Affected != 1 {
		t.Fatalf("apply result = %#v", result)
	}
	fields, err := st.MediaFields(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Text != "new" {
		t.Fatalf("after apply fields = %#v, want genre=new", fields)
	}

	// Undo by the returned batch id restores the prior value.
	undo := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($batchID:String!){ undoBatch(batchID:$batchID){ batchID affected } }","variables":{"batchID":"`+result.BatchID+`"}}`)
	var undoResp struct {
		Data struct {
			UndoBatch BatchResultInfo `json:"undoBatch"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(undo, &undoResp); err != nil {
		t.Fatalf("decode undo: %v", err)
	}
	if len(undoResp.Errors) != 0 || undoResp.Data.UndoBatch.Affected != 1 {
		t.Fatalf("undo = %#v errors=%#v", undoResp.Data.UndoBatch, undoResp.Errors)
	}
	fields, err = st.MediaFields(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Text != "old" {
		t.Fatalf("after undo fields = %#v, want genre=old restored", fields)
	}
}

func TestBulkEditRequiresManagerRole(t *testing.T) {
	handler := newTestHandler(t)
	resp := graphPost(t, handler, authorizeViewer,
		`{"query":"mutation($mediaIDs:[String!]!,$fields:[FieldInput!]!){ bulkEditApply(mediaIDs:$mediaIDs, fields:$fields){ batchID } }","variables":{"mediaIDs":["media-alpha"],"fields":[{"name":"genre","type":"text","value":"x"}]}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "manager role required" {
		t.Fatalf("errors = %#v, want manager role required", decoded.Errors)
	}
}

func TestBulkEditPublicPathRejectsEncryptedMedia(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir(), store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	handler := newTestHandlerWithStore(st)
	mgr := libencrypt.NewManagerWithParams(newMemSecretStore(), libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	libID := newEncryptedLibrary(t, st)
	if _, err := mgr.Provision(libID, libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(t.Context(), store.LibraryID(libID), handler.libEncryptDBCipher()); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID:           store.LibraryID(libID),
		Path:                "/encrypted/photo.jpg",
		ContentFingerprint:  "sha256:bulk-edit-public",
		Title:               "Protected title",
		Container:           "jpeg",
		Codec:               "jpeg",
		DurationSecondsHint: 1,
		Class:               store.MediaClassImage,
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, query := range map[string]string{
		"preview": `query($mediaIDs:[String!]!,$fields:[FieldInput!]!){ bulkEditPreview(mediaIDs:$mediaIDs, fields:$fields){ mediaID } }`,
		"apply":   `mutation($mediaIDs:[String!]!,$fields:[FieldInput!]!){ bulkEditApply(mediaIDs:$mediaIDs, fields:$fields){ batchID } }`,
	} {
		rec := postGraphQL(t, handler, authorizeManager, query, map[string]any{
			"mediaIDs": []string{string(media.ID)},
			"fields":   []any{map[string]any{"name": "caption", "type": "text", "value": "plaintext"}},
		})
		var resp response
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 1 || resp.Errors[0].Message != "bulk editing is unavailable for encrypted media" {
			t.Fatalf("%s errors = %#v", name, resp.Errors)
		}
	}
	fields, err := st.MediaFields(t.Context(), media.ID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 0 {
		t.Fatalf("rejected public bulk edit wrote fields: %#v", fields)
	}
	events, err := st.ReadEventOutbox(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.AggregateID == string(media.ID) {
			t.Fatalf("rejected public bulk edit emitted media event: %#v", event)
		}
	}
}

func TestBulkEditApplyRejectsMissingArgs(t *testing.T) {
	handler := newTestHandler(t)
	resp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation { bulkEditApply(mediaIDs:[], fields:[]){ batchID } }"}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Message != "mediaIDs is required" {
		t.Fatalf("errors = %#v, want mediaIDs required", decoded.Errors)
	}
}

// graphPost runs one GraphQL request body through the handler with the given
// authorizer and returns the raw response body.
func graphPost(t *testing.T, handler *Handler, authorize func(*http.Request), body string) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

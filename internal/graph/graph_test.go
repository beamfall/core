package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/apptest/plugintest"
	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
	podcasts "github.com/beamfall/plugin-podcasts"
)

func TestServerInfoQuery(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { serverInfo { name version } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string]ServerInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data["serverInfo"].Name != "Beamfall" {
		t.Fatalf("server name = %q, want Beamfall", resp.Data["serverInfo"].Name)
	}
}

func TestServeHTTPSelectsNamedOperationBeforeAuth(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{
		"operationName":"Public",
		"query":"query Public { serverInfo { name } } query Private { currentSession { id } }"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []graphError               `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if _, ok := resp.Data["serverInfo"]; !ok {
		t.Fatalf("data = %#v, want serverInfo", resp.Data)
	}
	if _, ok := resp.Data["currentSession"]; ok {
		t.Fatalf("unselected currentSession was dispatched: %#v", resp.Data)
	}
}

func TestServeHTTPRejectsMultiOperationWithoutOperationName(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query Public { serverInfo { name } } query Private { currentSession { id } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "operationName is required for multiple operations" {
		t.Fatalf("errors = %#v", resp.Errors)
	}
}

func TestServeHTTPIgnoresUnselectedUnknownField(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{
		"operationName":"Public",
		"query":"query Public { serverInfo { name } } query Bad { noSuchField { id } }"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if _, ok := resp.Data["serverInfo"]; !ok {
		t.Fatalf("data = %#v, want serverInfo", resp.Data)
	}
}

func TestServeHTTPSelectedOperationPreservesFragmentParsing(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{
		"operationName":"Public",
		"query":"query Public { ... on Query { serverInfo { name } } } fragment AdminFields on Query { apiKeys { id } } query Private { currentSession { id } }"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if _, ok := resp.Data["serverInfo"]; !ok {
		t.Fatalf("data = %#v, want serverInfo", resp.Data)
	}
}

func TestServeHTTPRejectsUnknownOperationName(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{
		"operationName":"Missing",
		"query":"query Public { serverInfo { name } } query Private { currentSession { id } }"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != `unknown operationName "Missing"` {
		t.Fatalf("errors = %#v", resp.Errors)
	}
}

func TestNavigationManifestQuery(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { navigationManifest { profileID } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string]manifest.Document `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data["navigationManifest"].ProfileID != "profile-default" {
		t.Fatalf("profileID = %q, want profile-default", resp.Data["navigationManifest"].ProfileID)
	}
}

func TestNavigationManifestQueryRequiresSession(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { navigationManifest { profileID } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNavigationManifestQueryUsesSessionScope(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { navigationManifest { libraries { id } } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeRevealed(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string]manifest.Document `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, want := len(resp.Data["navigationManifest"].Libraries), 2; got != want {
		t.Fatalf("len(libraries) = %d, want %d", got, want)
	}
}

func TestCurrentSessionQuery(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { currentSession { id deviceRole profileID profile revealed } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeRevealed(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string]SessionInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Data["currentSession"].Revealed {
		t.Fatal("currentSession.revealed = false, want true")
	}
	if resp.Data["currentSession"].DeviceRole != "viewer" {
		t.Fatalf("currentSession.deviceRole = %q, want viewer", resp.Data["currentSession"].DeviceRole)
	}
}

func TestSourceItemsQueryReturnsVisibleMedia(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query SourceItems($kind: String!) { sourceItems(source: { kind: $kind }) { id kind title summary } }","variables":{"kind":"recently-added"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string][]SourceItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := resp.Data["sourceItems"]
	if len(got) != 1 {
		t.Fatalf("len(sourceItems) = %d, want 1", len(got))
	}
	if got[0].ID != "media-alpha" || got[0].Kind != "media" || got[0].Title != "Sample Media" {
		t.Fatalf("sourceItems[0] = %#v, want visible media-alpha", got[0])
	}
}

func TestSourceItemsAllItemsQuerySearchesArchiveText(t *testing.T) {
	handler := newTestHandler(t)
	ctx := context.Background()
	library, err := handler.store.CreateLibrary(ctx, "Archive Search", false, "podcasts", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := handler.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          library.ID,
		Path:               "/archive/search.mp4",
		Title:              "Archive Search",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:archive-search",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.store.ReplaceMediaFieldSearchPolicies(ctx, []store.MediaFieldSearchPolicy{{
		PluginID: "podcasts", Field: "transcript", Type: store.EntityFieldText, Searchable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	summary := "episode description about ceramic history"
	if err := handler.store.UpdateMediaMetadata(ctx, media.ID, store.MediaMetadata{Summary: &summary}); err != nil {
		t.Fatal(err)
	}
	if err := handler.store.SetMediaFields(ctx, media.ID, []store.MediaField{
		{Name: "transcript", Type: store.EntityFieldText, Text: "kiln calibration transcript"},
	}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"query":"query SourceItems($kind: String!, $query: String!) { sourceItems(kind: $kind, query: $query) { id kind title } }","variables":{"kind":"all-items","query":"calibration"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data struct {
			SourceItems []SourceItem `json:"sourceItems"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if len(resp.Data.SourceItems) != 1 || resp.Data.SourceItems[0].ID != string(media.ID) {
		t.Fatalf("sourceItems archive search = %#v, want %s", resp.Data.SourceItems, media.ID)
	}
}

func TestPodcastBundleDeclaresArchiveSearchSource(t *testing.T) {
	for _, layout := range podcasts.Bundle().Layouts {
		for _, section := range layout.Sections {
			if section.ID == "archive-search" {
				if section.Source.Kind != "all-items" {
					t.Fatalf("archive-search source = %q, want all-items", section.Source.Kind)
				}
				return
			}
		}
	}
	t.Fatal("podcasts bundle missing archive-search section")
}

func TestSourceItemsQueryAllowsRestrictedMediaOnlyWhenRevealed(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { sourceItems { id kind title } }","variables":{"kind":"recently-added"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeRevealed(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string][]SourceItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := itemIDs(resp.Data["sourceItems"])
	// recently-added is newest-first (ORDER BY m.rowid DESC): media-archive was
	// seeded after media-alpha, so the revealed session sees it first.
	want := []string{"media-archive", "media-alpha"}
	if !equalStringSlices(got, want) {
		t.Fatalf("source item ids = %v, want %v", got, want)
	}
}

func TestSourceItemsQueryUnknownSourceReturnsEmptyList(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { sourceItems { id kind title } }","variables":{"kind":"unknown-source"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string][]SourceItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := len(resp.Data["sourceItems"]); got != 0 {
		t.Fatalf("len(sourceItems) = %d, want 0", got)
	}
}

func TestJobsQueryReturnsManagerVisibleProgress(t *testing.T) {
	// feature:job-management
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(t.Context(), store.Job{
		ID:        "job-scan-1",
		Kind:      "scan",
		Class:     store.JobClassBackground,
		Payload:   []byte(`{"version":1,"library_id":"library-local","root":"/media/local"}`),
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(t.Context(), "worker", nil, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJobCheckpoint(t.Context(), claimed.ID, claimed.LeaseOwner, []byte(`{"cursor":"a"}`), store.JobProgress{
		Current: 7,
		Total:   10,
		Unit:    "files",
		Message: "Scanning",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(t.Context(), store.Job{ID: "job-done", Kind: "scan", CreatedAt: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	done, err := st.ClaimNextJob(t.Context(), "worker", nil, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteJob(t.Context(), done.ID, done.LeaseOwner, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"query":"query { jobs { id kind class status progress { current total unit message } } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string][]JobInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	jobs := resp.Data["jobs"]
	if len(jobs) != 2 {
		t.Fatalf("len(jobs) = %d, want 2: %s", len(jobs), rec.Body.String())
	}
	got := jobs[0]
	if got.ID != "job-scan-1" || got.Kind != "scan" || got.Class != "background" || got.Status != "running" {
		t.Fatalf("active job = %#v", got)
	}
	if got.Progress.Current != 7 || got.Progress.Total != 10 || got.Progress.Unit != "files" || got.Progress.Message != "Scanning" {
		t.Fatalf("progress = %#v", got.Progress)
	}
}

func TestJobsQueryRequiresAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { jobs { id status } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", resp.Errors)
	}
}

func TestJobQueryReturnsOneJobByID(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(t.Context(), store.Job{ID: "job-scan-1", Kind: "scan", Class: store.JobClassBackground, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"query":"query Job($id: String!) { job(id: $id) { id kind status } }","variables":{"id":"job-scan-1"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string]JobInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data["job"].ID != "job-scan-1" {
		t.Fatalf("job = %#v", resp.Data["job"])
	}
}

func TestUnsupportedQueryReturnsGraphError(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { unsupported }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("len(errors) = %d, want 1", len(resp.Errors))
	}
	// H23-C: the unknown field is named explicitly, not silently ignored.
	if !strings.Contains(resp.Errors[0].Message, "unknown top-level field") ||
		!strings.Contains(resp.Errors[0].Message, "unsupported") {
		t.Fatalf("error = %q, want it to name the unknown field", resp.Errors[0].Message)
	}
}

// TestUnknownTopLevelFieldRejectsWholeBatch is the H23-C slice: a query mixing a
// valid top-level field with an unknown one is rejected outright (the unknown
// field named), instead of silently dropping the unknown and serving a partial
// response that masks the client/server contract drift. The depth-1 dispatcher
// only guards top-level fields; nested-shape drift is H23-B (deferred).
func TestUnknownTopLevelFieldRejectsWholeBatch(t *testing.T) {
	handler := newTestHandler(t)
	// currentSession is valid; phantomField is not. The whole batch must error.
	body := []byte(`{"query":"query { currentSession { id } phantomField { id } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// No data — the whole batch is rejected, not partially served.
	if resp.Data != nil {
		t.Fatalf("data = %v, want none (unknown field rejects the whole batch)", resp.Data)
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0].Message, "phantomField") {
		t.Fatalf("errors = %#v, want a single error naming phantomField", resp.Errors)
	}
}

// TestUnknownNestedFieldRuntimeRejectionIsDeferred pins the H23-B contract:
// runtime nested-field validation is intentionally deferred until shipped
// clients conform to NestedSchema. The dispatcher rejects unknown top-level
// fields, but a nonexistent nested selection is surfaced by the committed
// nested schema instead of a runtime graph error.
func TestUnknownNestedFieldRuntimeRejectionIsDeferred(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { sourceItems { id libraryID } }","variables":{"kind":"recently-added"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v, want no runtime nested-field error", resp.Errors)
	}
	for _, child := range topLevel(t, "sourceitems").Children {
		if child.Name == "libraryID" {
			t.Fatal("NestedSchema exposes sourceItems.libraryID; deferral no longer documents this client drift")
		}
	}
}

// TestMultiReadBatchReturnsPartialSuccess is the M2 read-side slice: a batched
// query where one read field errors must still return the other reads under
// data, with the failed field present as null and a {message, path} entry in
// errors[] — one flaky/forbidden read no longer blanks an entire screen.
func TestMultiReadBatchReturnsPartialSuccess(t *testing.T) {
	handler := newTestHandler(t)
	// profiles + currentSession succeed; media errors (no id variable supplied →
	// "id is required" client error). All three are read fields.
	body := []byte(`{"query":"query { profiles { id } currentSession { id } media { id } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []graphError               `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The succeeding reads are present...
	if _, ok := resp.Data["profiles"]; !ok {
		t.Fatalf("profiles missing from partial-success data: %s", rec.Body.String())
	}
	if _, ok := resp.Data["currentSession"]; !ok {
		t.Fatalf("currentSession missing from partial-success data: %s", rec.Body.String())
	}
	// ...the failed read is present as null...
	mediaVal, ok := resp.Data["media"]
	if !ok || string(mediaVal) != "null" {
		t.Fatalf("media = %s (present=%v), want null", mediaVal, ok)
	}
	// ...and exactly one errors[] entry carries the field path + stable message.
	if len(resp.Errors) != 1 {
		t.Fatalf("errors = %#v, want exactly one (for media)", resp.Errors)
	}
	if resp.Errors[0].Message != "id is required" {
		t.Fatalf("error message = %q, want \"id is required\"", resp.Errors[0].Message)
	}
	if len(resp.Errors[0].Path) != 1 || resp.Errors[0].Path[0] != "media" {
		t.Fatalf("error path = %#v, want [media]", resp.Errors[0].Path)
	}
}

// TestMutationErrorAbortsBatch is the M2 mutation-side slice: a mutation error
// stays all-or-nothing — it aborts the whole batch (no partial data), because a
// half-applied mutation batch is worse than none.
func TestMutationErrorAbortsBatch(t *testing.T) {
	handler := newTestHandler(t)
	// profiles (read) would succeed, but setFavorite (mutation) errors with no
	// mediaID. The whole batch must abort: errors-only, no data.
	body := []byte(`{"query":"mutation { first: setFavorite(mediaID: \"media-alpha\", favorite: true) second: setFavorite { id } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []graphError               `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// No data at all — the successful read is discarded with the failed mutation.
	if len(resp.Data) != 0 {
		t.Fatalf("data = %v, want empty (mutation error aborts the batch)", resp.Data)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "mediaID is required" {
		t.Fatalf("errors = %#v, want a single aborting mutation error", resp.Errors)
	}
	// The aborting error carries no path (whole-response error, not per-field).
	if resp.Errors[0].Path != nil {
		t.Fatalf("aborting mutation error has path %#v, want none", resp.Errors[0].Path)
	}
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	return newTestHandlerWithStore(storetest.Open(t))
}

func newTestPluginRegistry(bundles []plugin.Bundle) (*plugin.Registry, error) {
	return plugintest.Registry(bundles)
}

func newTestHandlerWithStore(st *store.Store) *Handler {
	return NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, nil),
		auth.NewService(st),
		st,
		nil,
	)
}

func authorizeViewer(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

func authorizeRevealed(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-revealed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

func authorizeManager(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
}

// openManagerAdminSession stamps the Admin Session window on the seeded
// session-manager row so authorizeManager requests resolve scope.Admin=true.
// Console operations are gated by the Admin Session tier; tests
// exercising them must open the window first, exactly as a real Console client
// does via verifyAdminSession. The window is stamped an hour out so the
// real-time auth clock keeps it open for the test's duration.
func openManagerAdminSession(t *testing.T, st *store.Store) {
	t.Helper()
	openAdminSession(t, st, "session-manager")
}

// openAdminSession stamps the Admin Session window on an arbitrary seeded or
// enrolled session row (the per-session admin state the Console gate reads).
func openAdminSession(t *testing.T, st *store.Store, session store.SessionID) {
	t.Helper()
	if err := st.SetSessionAdminUntil(t.Context(), session, time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("open admin session for %s: %v", session, err)
	}
}

func itemIDs(items []SourceItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestJobsQueryRedactsRestrictedLibraryErrorsUntilRevealed(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	now := time.Unix(100, 0).UTC()
	if _, err := st.EnqueueJob(t.Context(), store.Job{
		ID:        "job-restricted",
		Kind:      "scan",
		Class:     store.JobClassBackground,
		Payload:   []byte(`{"version":1,"library_id":"library-archive","root":"/media/archive"}`),
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(t.Context(), "worker", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	pathBearingError := "open /media/archive/secret-title.mp4: permission denied"
	if err := st.FailJob(t.Context(), claimed.ID, claimed.LeaseOwner, pathBearingError, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// A revealed manager session does not exist in the seed; enroll one. jobs is
	// a Console read (admin-gated), so both sessions need an open Admin Session;
	// only the second is also revealed, isolating the Reveal-redaction behavior.
	if err := st.EnrollDevice(t.Context(),
		store.Device{ID: "device-mgr2", Name: "Manager 2", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("mgr2-secret")},
		store.Session{ID: "session-mgr-revealed", DeviceID: "device-mgr2", ProfileID: "profile-default", Revealed: true},
	); err != nil {
		t.Fatal(err)
	}
	openAdminSession(t, st, "session-mgr-revealed")

	body := []byte(`{"query":"query { jobs { id status error } }"}`)

	// Unrevealed manager: the path-bearing error is withheld.
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string][]JobInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data["jobs"]) != 1 {
		t.Fatalf("len(jobs) = %d, want 1", len(resp.Data["jobs"]))
	}
	got := resp.Data["jobs"][0]
	if got.Error != "error withheld — reveal required" {
		t.Fatalf("unrevealed error = %q, want redacted placeholder", got.Error)
	}
	if strings.Contains(rec.Body.String(), "secret-title") {
		t.Fatalf("unrevealed response leaks restricted path: %s", rec.Body.String())
	}

	// Revealed manager: full error text.
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	req.Header.Set("X-Beamfall-Session", "session-mgr-revealed")
	req.Header.Set("X-Beamfall-Device", "device-mgr2")
	req.Header.Set("X-Beamfall-Device-Secret", "mgr2-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revealed status = %d, want %d", rec.Code, http.StatusOK)
	}
	resp.Data = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if got := resp.Data["jobs"][0].Error; got != pathBearingError {
		t.Fatalf("revealed error = %q, want full error text", got)
	}
}

func TestScanLibraryMutationRequiresAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"mutation { scanLibrary(libraryID: \"library-local\", root: \"/media\") { id } }","variables":{"libraryID":"library-local","root":"/media"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", resp.Errors)
	}
}

func TestScanLibraryMutationEnqueuesScanJob(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"query":"mutation ScanLibrary($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id kind status } }","variables":{"libraryID":"library-local","root":"` + root + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			ScanLibrary JobInfo `json:"scanLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.ScanLibrary.Kind != "scan" || resp.Data.ScanLibrary.Status != "queued" {
		t.Fatalf("scanLibrary = %#v, want queued scan job", resp.Data.ScanLibrary)
	}
}

// TestScanLibraryMutationBackfillFlagEnqueuesBackfillJob asserts the optional
// backfill argument reaches the durable scan job payload.
func TestScanLibraryMutationBackfillFlagEnqueuesBackfillJob(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	root := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"query":"mutation ScanLibrary($libraryID: String!, $root: String!, $backfill: Boolean) { scanLibrary(libraryID: $libraryID, root: $root, backfill: $backfill) { id kind status } }","variables":{"libraryID":"library-local","root":"` + root + `","backfill":true}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			ScanLibrary JobInfo `json:"scanLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.ScanLibrary.Kind != "scan" || resp.Data.ScanLibrary.Status != "queued" {
		t.Fatalf("scanLibrary = %#v, want queued scan job", resp.Data.ScanLibrary)
	}
	job, err := st.Job(context.Background(), store.JobID(resp.Data.ScanLibrary.ID))
	if err != nil {
		t.Fatalf("read enqueued job: %v", err)
	}
	if !bytes.Contains(job.Payload, []byte(`"backfill":true`)) {
		t.Fatalf("enqueued job payload missing backfill flag: %s", job.Payload)
	}
}

func TestScanLibraryMutationRejectsRootOutsideLibraryRoots(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	root := t.TempDir()
	outside := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"query":"mutation ScanLibrary($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id } }","variables":{"libraryID":"library-local","root":"` + outside + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "folder is outside the library roots" {
		t.Fatalf("errors = %#v, want containment error", resp.Errors)
	}
}

func TestBackupMutationRequiresAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"mutation Backup($dest: String!) { backup(dest: $dest) { manifest } }","variables":{"dest":"/tmp/beamfall-backup"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", resp.Errors)
	}
}

func TestBackupMutationWritesBackupAndReturnsManifest(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	// The backup dest must sit within the store's sanctioned path allowlist;
	// register it as a PathPicker root and assert against
	// the returned canonical root so the symlink-resolved manifest path matches.
	root, err := st.AddPathPickerRoot(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("AddPathPickerRoot: %v", err)
	}
	dest := root.Path
	body := []byte(`{"query":"mutation Backup($dest: String!) { backup(dest: $dest) { manifest } }","variables":{"dest":"` + strings.ReplaceAll(dest, `\`, `\\`) + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			Backup struct {
				Manifest string `json:"manifest"`
			} `json:"backup"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.Backup.Manifest == "" || !strings.HasPrefix(resp.Data.Backup.Manifest, dest) {
		t.Fatalf("backup manifest = %q, want path under %q", resp.Data.Backup.Manifest, dest)
	}
}

func TestVerifyBackupMutationVerifiesFreshSet(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	root, err := st.AddPathPickerRoot(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("AddPathPickerRoot: %v", err)
	}
	dest := root.Path
	result, err := st.Backup(context.Background(), dest)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	manifest := strings.ReplaceAll(result.Manifest, `\`, `\\`)
	body := []byte(`{"query":"mutation VerifyBackup($manifest: String!) { verifyBackup(manifest: $manifest) { ok error } }","variables":{"manifest":"` + manifest + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			VerifyBackup VerifyBackupInfo `json:"verifyBackup"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if !resp.Data.VerifyBackup.OK || resp.Data.VerifyBackup.Error != "" {
		t.Fatalf("verifyBackup = %#v, want ok with no error", resp.Data.VerifyBackup)
	}
}

func TestVerifyBackupMutationRequiresAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"mutation VerifyBackup($manifest: String!) { verifyBackup(manifest: $manifest) { ok } }","variables":{"manifest":"/tmp/beamfall-backup/manifest.json"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", resp.Errors)
	}
}

func TestBackupHistoryQueryListsSets(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	root, err := st.AddPathPickerRoot(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("AddPathPickerRoot: %v", err)
	}
	dest := root.Path
	if _, err := st.Backup(context.Background(), dest); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	destVar := strings.ReplaceAll(dest, `\`, `\\`)
	body := []byte(`{"query":"query History($dest: String!) { backupHistory(dest: $dest) { directory valid sizeBytes } }","variables":{"dest":"` + destVar + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			BackupHistory []BackupHistoryEntry `json:"backupHistory"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if len(resp.Data.BackupHistory) != 1 {
		t.Fatalf("backupHistory len = %d, want 1", len(resp.Data.BackupHistory))
	}
	if !resp.Data.BackupHistory[0].Valid || resp.Data.BackupHistory[0].SizeBytes <= 0 {
		t.Fatalf("history entry = %#v, want valid with size", resp.Data.BackupHistory[0])
	}
}

func TestBackupHistoryQueryRequiresAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { backupHistory(dest: \"/tmp/beamfall-backup\") { directory } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", resp.Errors)
	}
}

func TestRunJobMutationRunsOneBackgroundJob(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	runner := &recordingJobRunner{job: store.Job{ID: "job-ran", Kind: "scan", Status: store.JobStatusCompleted}}
	handler.SetJobRunner(runner)
	body := []byte(`{"query":"mutation { runJob { id kind status } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			RunJob JobInfo `json:"runJob"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if !runner.called {
		t.Fatal("job runner was not called")
	}
	if resp.Data.RunJob.ID != "job-ran" || resp.Data.RunJob.Status != "completed" {
		t.Fatalf("runJob = %#v", resp.Data.RunJob)
	}
}

func TestScanLibraryMutationNotifiesWatcher(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	observer := &recordingScanObserver{}
	handler.SetScanObserver(observer)
	root := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"query":"mutation ScanLibrary($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id } }","variables":{"libraryID":"library-local","root":"` + root + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.calls) != 1 {
		t.Fatalf("scan observer calls = %#v, want 1 call", observer.calls)
	}
	if observer.calls[0].libraryID != "library-local" || observer.calls[0].root != root {
		t.Fatalf("scan observer call = %#v", observer.calls[0])
	}
}

func TestScanLibraryMutationRejectsUnknownLibrary(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	body := []byte(`{"query":"mutation ScanLibrary($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id } }","variables":{"libraryID":"library-nope","root":"/media"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "unknown library" {
		t.Fatalf("errors = %#v, want unknown library", resp.Errors)
	}
}

type recordingScanObserver struct {
	mu    sync.Mutex
	calls []scanCall
}

type scanCall struct {
	libraryID string
	root      string
}

func (r *recordingScanObserver) Observe(libraryID store.LibraryID, root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, scanCall{libraryID: string(libraryID), root: root})
}

type recordingJobRunner struct {
	called bool
	job    store.Job
	err    error
}

func (r *recordingJobRunner) RunOne(ctx context.Context, classes []store.JobClass, now time.Time) (store.Job, error) {
	r.called = true
	if len(classes) != 1 || classes[0] != store.JobClassBackground {
		return store.Job{}, errors.New("unexpected job class")
	}
	return r.job, r.err
}

func TestReportPlaybackRecordsResumeAndContinueWatching(t *testing.T) {
	handler := newTestHandler(t)
	report := []byte(`{"query":"mutation ReportPlayback($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int, $finished: Boolean) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, finished: $finished) { mediaID positionSeconds watched } }","variables":{"mediaID":"media-alpha","positionSeconds":45,"durationSeconds":120,"finished":false}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(report))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			ReportPlayback struct {
				MediaID         string
				PositionSeconds int
				Watched         bool
			} `json:"reportPlayback"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.ReportPlayback.PositionSeconds != 45 || resp.Data.ReportPlayback.Watched {
		t.Fatalf("reportPlayback = %#v, want position 45 unwatched", resp.Data.ReportPlayback)
	}

	// The item now appears in continue-watching for the same profile.
	query := []byte(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id title summary } }","variables":{"kind":"continue-watching"}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(query))
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var listResp struct {
		Data struct {
			SourceItems []SourceItem `json:"sourceItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Data.SourceItems) != 1 || listResp.Data.SourceItems[0].ID != "media-alpha" {
		t.Fatalf("continue-watching = %#v, want media-alpha", listResp.Data.SourceItems)
	}
	if listResp.Data.SourceItems[0].Summary != "2 min left" {
		t.Fatalf("summary = %q, want 2 min left", listResp.Data.SourceItems[0].Summary)
	}
}

// TestReportPlaybackEventTimeGuardsAgainstStaleReplay is ABPARITY-1 (ADR-0011
// §3): the reportPlayback wire contract now carries an optional monotonic
// eventTimeMS. A late report whose eventTimeMS predates the profile's
// last-applied event-time for that Media must not rewind the stored position —
// exercising the fix end-to-end through the mutation clients actually call.
func TestReportPlaybackEventTimeGuardsAgainstStaleReplay(t *testing.T) {
	handler := newTestHandler(t)
	mutation := `mutation Report($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int, $eventTimeMS: Int) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, eventTimeMS: $eventTimeMS) { positionSeconds } }`

	send := func(position, eventTimeMS int) int {
		body, _ := json.Marshal(map[string]any{
			"query": mutation,
			"variables": map[string]any{
				"mediaID": "media-alpha", "positionSeconds": position,
				"durationSeconds": 1000, "eventTimeMS": eventTimeMS,
			},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Data struct {
				ReportPlayback struct{ PositionSeconds int } `json:"reportPlayback"`
			} `json:"data"`
			Errors []struct{ Message string } `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("errors = %#v", resp.Errors)
		}
		return resp.Data.ReportPlayback.PositionSeconds
	}

	if got := send(100, 1000); got != 100 {
		t.Fatalf("first report position = %d, want 100", got)
	}
	// Stale replay at an OLDER event-time must not rewind the position.
	if got := send(30, 400); got != 100 {
		t.Fatalf("stale replay position = %d, want 100 (guard failed)", got)
	}
	// A newer event-time still advances.
	if got := send(50, 2000); got != 50 {
		t.Fatalf("newer event-time report position = %d, want 50", got)
	}
}

func TestReportPlaybackFinishedClearsContinueWatching(t *testing.T) {
	handler := newTestHandler(t)
	report := []byte(`{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 118, durationSeconds: 120) { watched } }","variables":{"mediaID":"media-alpha","positionSeconds":118,"durationSeconds":120}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(report))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			ReportPlayback struct {
				PositionSeconds int
				Watched         bool
			} `json:"reportPlayback"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Data.ReportPlayback.Watched || resp.Data.ReportPlayback.PositionSeconds != 0 {
		t.Fatalf("reportPlayback = %#v, want watched with cleared position", resp.Data.ReportPlayback)
	}

	query := []byte(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"continue-watching"}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(query))
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var listResp struct {
		Data struct {
			SourceItems []SourceItem `json:"sourceItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Data.SourceItems) != 0 {
		t.Fatalf("continue-watching = %#v, want empty after watched", listResp.Data.SourceItems)
	}
}

func TestReportPlaybackRejectsHiddenMedia(t *testing.T) {
	handler := newTestHandler(t)
	report := []byte(`{"query":"mutation { reportPlayback(mediaID: \"media-archive\", positionSeconds: 30) { watched } }","variables":{"mediaID":"media-archive","positionSeconds":30}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(report))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "unknown media" {
		t.Fatalf("errors = %#v, want unknown media for restricted target", resp.Errors)
	}
}

func TestQueueMutationsAreSessionProfileOwnedAndRevealFiltered(t *testing.T) {
	handler := newTestHandler(t)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	decodeItems := func(rec *httptest.ResponseRecorder, field string) []SourceItem {
		var resp struct {
			Data   map[string][]SourceItem `json:"data"`
			Errors []graphError            `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("%s errors = %#v", field, resp.Errors)
		}
		return resp.Data[field]
	}

	addLocal := `{"query":"mutation { queueAdd(mediaID: \"media-alpha\") { id } }","variables":{"mediaID":"media-alpha"}}`
	if got := decodeItems(post(addLocal, authorizeViewer), "queueAdd"); len(got) != 1 || got[0].ID != "media-alpha" {
		t.Fatalf("queueAdd local = %#v, want media-alpha", got)
	}
	if got := decodeItems(post(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"queue"}}`, authorizeViewer), "sourceItems"); len(got) != 1 || got[0].ID != "media-alpha" {
		t.Fatalf("viewer queue = %#v, want media-alpha", got)
	}

	// Managed Profiles may own queue state too; their mutations do not touch
	// the adult Profile's queue.
	if got := decodeItems(post(addLocal, authorizeManaged), "queueAdd"); len(got) != 1 || got[0].ID != "media-alpha" {
		t.Fatalf("managed queueAdd = %#v, want media-alpha", got)
	}
	if got := decodeItems(post(`{"query":"mutation { queueClear { id } }"}`, authorizeManaged), "queueClear"); len(got) != 0 {
		t.Fatalf("managed queueClear = %#v, want empty managed queue", got)
	}
	if got := decodeItems(post(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"queue"}}`, authorizeViewer), "sourceItems"); len(got) != 1 || got[0].ID != "media-alpha" {
		t.Fatalf("viewer queue after managed clear = %#v, want media-alpha", got)
	}

	if got := decodeItems(post(`{"query":"mutation { queueAdd(mediaID: \"media-archive\") { id } }","variables":{"mediaID":"media-archive"}}`, authorizeRevealed), "queueAdd"); len(got) != 2 {
		t.Fatalf("revealed queueAdd = %#v, want two visible items", got)
	}
	if got := decodeItems(post(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"queue"}}`, authorizeViewer), "sourceItems"); len(got) != 1 || got[0].ID != "media-alpha" {
		t.Fatalf("unrevealed queue = %#v, want restricted item filtered", got)
	}
	if got := decodeItems(post(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"queue"}}`, authorizeRevealed), "sourceItems"); len(got) != 2 || got[1].ID != "media-archive" {
		t.Fatalf("revealed queue = %#v, want restricted item restored", got)
	}
}

func TestQueueAddRejectsHiddenMedia(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"mutation { queueAdd(mediaID: \"media-archive\") { id } }","variables":{"mediaID":"media-archive"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "unknown media" {
		t.Fatalf("queueAdd errors = %#v, want unknown media", resp.Errors)
	}
}

func TestFavoriteMutationAndMediaQueryAreProfileOwnedAndRevealFiltered(t *testing.T) {
	handler := newTestHandler(t)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	decodeMedia := func(rec *httptest.ResponseRecorder, field string) MediaInfo {
		var resp struct {
			Data   map[string]MediaInfo `json:"data"`
			Errors []graphError         `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("%s errors = %#v", field, resp.Errors)
		}
		return resp.Data[field]
	}
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}

	favorite := decodeMedia(post(`{"query":"mutation { setFavorite(mediaID: \"media-alpha\", favorite: true) { id favorited } }","variables":{"mediaID":"media-alpha","favorite":true}}`, authorizeViewer), "setFavorite")
	if favorite.ID != "media-alpha" || !favorite.Favorited {
		t.Fatalf("setFavorite = %+v, want media-alpha favorited", favorite)
	}
	media := decodeMedia(post(`{"query":"query { media(id: \"media-alpha\") { id favorited } }","variables":{"id":"media-alpha"}}`, authorizeViewer), "media")
	if !media.Favorited {
		t.Fatalf("viewer media favorited = false, want true")
	}
	managed := decodeMedia(post(`{"query":"query { media(id: \"media-alpha\") { id favorited } }","variables":{"id":"media-alpha"}}`, authorizeManaged), "media")
	if managed.Favorited {
		t.Fatalf("managed profile sees adult profile favorite")
	}
}

func TestSetFavoriteRejectsHiddenMediaWithoutDeletingExistingFavorite(t *testing.T) {
	handler := newTestHandler(t)
	mark := []byte(`{"query":"mutation { setFavorite(mediaID: \"media-archive\", favorite: true) { id favorited } }","variables":{"mediaID":"media-archive","favorite":true}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(mark))
	authorizeRevealed(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	unfavoriteHidden := []byte(`{"query":"mutation { setFavorite(mediaID: \"media-archive\", favorite: false) { id favorited } }","variables":{"mediaID":"media-archive","favorite":false}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(unfavoriteHidden))
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "unknown media" {
		t.Fatalf("setFavorite errors = %#v, want unknown media", resp.Errors)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { media(id: \"media-archive\") { id favorited } }","variables":{"id":"media-archive"}}`)))
	authorizeRevealed(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var mediaResp struct {
		Data   map[string]MediaInfo `json:"data"`
		Errors []graphError         `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mediaResp); err != nil {
		t.Fatal(err)
	}
	if len(mediaResp.Errors) != 0 {
		t.Fatalf("media errors = %#v", mediaResp.Errors)
	}
	if !mediaResp.Data["media"].Favorited {
		t.Fatal("hidden unfavorite deleted existing favorite")
	}
}

func TestSearchQueryFiltersReveal(t *testing.T) {
	// feature:search-keyword
	handler := newTestHandler(t)
	body := []byte(`{"query":"query Search($query: String!) { search(query: $query) { id title } }","variables":{"query":"archive"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			Search []SourceItem `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Search) != 0 {
		t.Fatalf("unrevealed search = %#v, want empty for restricted media", resp.Data.Search)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeRevealed(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Search) != 1 || resp.Data.Search[0].ID != "media-archive" {
		t.Fatalf("revealed search = %#v, want media-archive", resp.Data.Search)
	}
}

func TestSearchEntitiesQueryFiltersReveal(t *testing.T) {
	st := storetest.Open(t)
	// The widget kind must opt into FullTextSearchable for its instances to
	// surface in entity search (the KIND-level seam).
	if err := st.RegisterEntityKindPolicies(context.Background(), []store.EntityKindPolicy{
		{Kind: "widget", FullTextSearchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(context.Background(), store.Entity{
		ID:        "entity-archive",
		LibraryID: "library-archive",
		Kind:      "widget",
		Title:     "Archive Widget",
	}); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	body := []byte(`{"query":"query Search($query: String!) { searchEntities(query: $query) { id title kind } }","variables":{"query":"archive"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			SearchEntities []SourceItem `json:"searchEntities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.SearchEntities) != 0 {
		t.Fatalf("unrevealed searchEntities = %#v, want empty for restricted entity", resp.Data.SearchEntities)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeRevealed(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.SearchEntities) != 1 ||
		resp.Data.SearchEntities[0].ID != "entity-archive" ||
		resp.Data.SearchEntities[0].Kind != "entity" {
		t.Fatalf("revealed searchEntities = %#v, want entity-archive", resp.Data.SearchEntities)
	}
}

func TestSearchResultKeywordRanksAcrossMediaAndEntities(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.RegisterEntityKindPolicies(ctx, []store.EntityKindPolicy{
		{Kind: "widget", FullTextSearchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/tmp/beyonxe.flac",
		Title:              "Beyonxe Song",
		Container:          "flac",
		Codec:              "flac",
		ContentFingerprint: "sha256:beyonxe-song",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{
		ID:        "entity-beyonce",
		LibraryID: "library-local",
		Kind:      "widget",
		Title:     "Beyoncé",
	}); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	body := []byte(`{"query":"query Search($query: String!) { searchResult(query: $query, mode: keyword) { items { id title kind mode } } }","variables":{"query":"beyonce"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			SearchResult struct {
				Items []SearchResultItem `json:"items"`
			} `json:"searchResult"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("searchResult errors = %#v", resp.Errors)
	}
	items := resp.Data.SearchResult.Items
	if len(items) < 2 || items[0].ID != "entity-beyonce" || items[1].ID != string(media.ID) {
		t.Fatalf("searchResult items = %#v, want exact entity before fuzzy media", items)
	}
}

func TestTrashRestoreLifecycle(t *testing.T) {
	// feature:trash-review
	handler := newTestHandler(t)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// Viewers cannot trash.
	rec := post(`{"query":"mutation { trashMedia(mediaID: \"media-alpha\") { id } }","variables":{"mediaID":"media-alpha"}}`, authorizeViewer)
	var errResp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatal(err)
	}
	if len(errResp.Errors) == 0 || errResp.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer trash errors = %#v", errResp.Errors)
	}

	// Manager trashes; the item leaves browse/search and lands in trashList.
	rec = post(`{"query":"mutation TrashMedia($mediaID: String!) { trashMedia(mediaID: $mediaID) { id title } }","variables":{"mediaID":"media-alpha"}}`, authorizeManager)
	var trashResp struct {
		Data struct {
			TrashMedia []TrashEntry `json:"trashMedia"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &trashResp); err != nil {
		t.Fatal(err)
	}
	if len(trashResp.Errors) != 0 {
		t.Fatalf("trash errors = %#v", trashResp.Errors)
	}
	if len(trashResp.Data.TrashMedia) != 1 || trashResp.Data.TrashMedia[0].ID != "media-alpha" {
		t.Fatalf("trashMedia = %#v, want media-alpha in trash", trashResp.Data.TrashMedia)
	}

	rec = post(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"recently-added"}}`, authorizeViewer)
	var itemsResp struct {
		Data struct {
			SourceItems []SourceItem `json:"sourceItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &itemsResp); err != nil {
		t.Fatal(err)
	}
	for _, item := range itemsResp.Data.SourceItems {
		if item.ID == "media-alpha" {
			t.Fatalf("trashed media still browsable: %#v", itemsResp.Data.SourceItems)
		}
	}

	// Restore brings it back.
	rec = post(`{"query":"mutation RestoreMedia($mediaID: String!) { restoreMedia(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha"}}`, authorizeManager)
	var restoreResp struct {
		Data struct {
			RestoreMedia []TrashEntry `json:"restoreMedia"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &restoreResp); err != nil {
		t.Fatal(err)
	}
	if len(restoreResp.Errors) != 0 || len(restoreResp.Data.RestoreMedia) != 0 {
		t.Fatalf("restore = %#v errors = %#v, want empty trash", restoreResp.Data.RestoreMedia, restoreResp.Errors)
	}
	rec = post(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"recently-added"}}`, authorizeViewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &itemsResp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range itemsResp.Data.SourceItems {
		if item.ID == "media-alpha" {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored media missing from browse: %#v", itemsResp.Data.SourceItems)
	}
}

func TestRevealLifecycleAndThrottling(t *testing.T) {
	handler := newTestHandler(t)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	type revealResp struct {
		Data struct {
			Reveal *RevealState `json:"reveal"`
			Relock *RevealState `json:"relock"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	decode := func(rec *httptest.ResponseRecorder) revealResp {
		var resp revealResp
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Wrong PIN fails; correct dev PIN reveals; restricted media becomes searchable; relock hides it again.
	resp := decode(post(`{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"9999"}}`, authorizeViewer))
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "reveal PIN mismatch" {
		t.Fatalf("wrong PIN errors = %#v", resp.Errors)
	}
	resp = decode(post(`{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"1234"}}`, authorizeViewer))
	if len(resp.Errors) != 0 || resp.Data.Reveal == nil || !resp.Data.Reveal.Revealed {
		t.Fatalf("reveal = %#v errors = %#v", resp.Data.Reveal, resp.Errors)
	}

	searchBody := `{"query":"query Search($query: String!) { search(query: $query) { id } }","variables":{"query":"archive"}}`
	var searchResp struct {
		Data struct {
			Search []SourceItem `json:"search"`
		} `json:"data"`
	}
	rec := post(searchBody, authorizeViewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &searchResp); err != nil {
		t.Fatal(err)
	}
	if len(searchResp.Data.Search) != 1 {
		t.Fatalf("post-reveal search = %#v, want restricted media visible", searchResp.Data.Search)
	}

	resp = decode(post(`{"query":"mutation { relock { revealed } }"}`, authorizeViewer))
	if resp.Data.Relock == nil || resp.Data.Relock.Revealed {
		t.Fatalf("relock = %#v", resp.Data.Relock)
	}
	rec = post(searchBody, authorizeViewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &searchResp); err != nil {
		t.Fatal(err)
	}
	if len(searchResp.Data.Search) != 0 {
		t.Fatalf("post-relock search = %#v, want hidden", searchResp.Data.Search)
	}

	// The free-attempt budget of wrong PINs spends the lane, so the next attempt
	// is throttled even with the right PIN (ADR-0012 §3 exponential backoff).
	wrong := `{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"0000"}}`
	for i := 0; i < revealBackoff.FreeAttempts; i++ {
		post(wrong, authorizeViewer)
	}
	resp = decode(post(`{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"1234"}}`, authorizeViewer))
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "reveal attempts throttled" {
		t.Fatalf("throttle errors = %#v", resp.Errors)
	}
}

func TestManagedProfileHasNoRevealPath(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"1234"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	req.Header.Set("X-Beamfall-Session", "session-managed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "profile has no reveal path" {
		t.Fatalf("managed reveal errors = %#v", resp.Errors)
	}
}

func TestProfilesQueryListsPickerEntriesHidingDisabled(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	query := `{"query":"query Profiles { profiles { id name kind } }"}`
	var resp struct {
		Data struct {
			Profiles []ProfileSummary `json:"profiles"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	decode := func(rec *httptest.ResponseRecorder) {
		resp.Data.Profiles = nil
		resp.Errors = nil
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}

	// Any session may list the picker: viewers included.
	decode(post(query, authorizeViewer))
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if len(resp.Data.Profiles) != 2 {
		t.Fatalf("profiles = %#v, want both seeded profiles", resp.Data.Profiles)
	}
	if resp.Data.Profiles[0].ID != "profile-default" || resp.Data.Profiles[0].Kind != "adult" {
		t.Fatalf("profiles[0] = %#v, want profile-default (adult, name-ordered)", resp.Data.Profiles[0])
	}
	if resp.Data.Profiles[1].ID != "profile-managed" || resp.Data.Profiles[1].Kind != "managed" {
		t.Fatalf("profiles[1] = %#v, want profile-managed", resp.Data.Profiles[1])
	}

	// Certification ceilings ride along on the picker entry so the Console's
	// Enforced Filters surface can render and edit them without a second round
	// trip. Seed one on profile-default and confirm the profiles
	// query returns it in the same shape setCertificationCeiling does.
	if _, err := st.SetProfileCertificationCeiling(context.Background(), "profile-default",
		store.ProfileCertificationCeiling{PluginID: "starlog", Ceiling: "mid", AllowUnrated: true}); err != nil {
		t.Fatal(err)
	}
	ceilQuery := `{"query":"query { profiles { id name kind certificationCeilings { pluginID ceiling allowUnrated } } }"}`
	decode(post(ceilQuery, authorizeViewer))
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if got := resp.Data.Profiles[0]; got.ID != "profile-default" || len(got.CertificationCeilings) != 1 {
		t.Fatalf("profiles[0] = %#v, want one ceiling on profile-default", got)
	}
	if c := resp.Data.Profiles[0].CertificationCeilings[0]; c.PluginID != "starlog" || c.Ceiling != "mid" || !c.AllowUnrated {
		t.Fatalf("ceiling = %#v, want {starlog mid allowUnrated}", c)
	}
	// Profiles with no ceiling serialise an empty (non-null) list, matching
	// the mutation's contract so the web decode stays consistent.
	if got := resp.Data.Profiles[1]; got.ID != "profile-managed" || got.CertificationCeilings == nil || len(got.CertificationCeilings) != 0 {
		t.Fatalf("profiles[1] = %#v, want profile-managed with empty (non-null) ceilings", got)
	}

	// Disabled Profiles are hidden from pickers.
	disabled := true
	if _, err := st.UpdateProfileSettings(context.Background(), "profile-managed", store.ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	decode(post(query, authorizeViewer))
	if len(resp.Data.Profiles) != 1 || resp.Data.Profiles[0].ID != "profile-default" {
		t.Fatalf("profiles = %#v, want disabled profile hidden", resp.Data.Profiles)
	}
}

func TestSwitchProfileRebindsSessionAndRelocks(t *testing.T) {
	// feature:profile-switch
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	switchBody := func(profileID string) string {
		return `{"query":"mutation SwitchProfile($profileID: String!) { switchProfile(profileID: $profileID) { id profileID profile revealed } }","variables":{"profileID":"` + profileID + `"}}`
	}
	var resp struct {
		Data struct {
			SwitchProfile  *SessionInfo `json:"switchProfile"`
			CurrentSession *SessionInfo `json:"currentSession"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	decode := func(rec *httptest.ResponseRecorder) {
		resp.Data.SwitchProfile = nil
		resp.Data.CurrentSession = nil
		resp.Errors = nil
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}

	// The seeded revealed session switches profiles: the rebind lands and the
	// switch relocks — Reveal never survives a profile switch.
	decode(post(switchBody("profile-managed"), authorizeRevealed))
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	got := resp.Data.SwitchProfile
	if got == nil || got.ProfileID != "profile-managed" || got.Profile != "managed" || got.Revealed {
		t.Fatalf("switchProfile = %#v, want managed binding, relocked", got)
	}
	decode(post(`{"query":"query { currentSession { id profileID revealed } }"}`, authorizeRevealed))
	session := resp.Data.CurrentSession
	if session == nil || session.ProfileID != "profile-managed" || session.Revealed {
		t.Fatalf("currentSession after switch = %#v, want profile-managed, relocked", session)
	}

	// Unknown and disabled Profiles refuse the switch with stable messages.
	decode(post(switchBody("profile-nope"), authorizeRevealed))
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "profile not found" {
		t.Fatalf("unknown profile errors = %#v", resp.Errors)
	}
	disabled := true
	if _, err := st.UpdateProfileSettings(context.Background(), "profile-default", store.ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	decode(post(switchBody("profile-default"), authorizeRevealed))
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "profile is disabled" {
		t.Fatalf("disabled profile errors = %#v", resp.Errors)
	}
}

func TestCreateLibraryGrantsToCreatorProfile(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	body := []byte(`{"query":"mutation CreateLibrary($title: String!) { createLibrary(title: $title) { id title } }","variables":{"title":"Home Videos"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			CreateLibrary LibraryInfo `json:"createLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.CreateLibrary.ID != "library-home-videos" || resp.Data.CreateLibrary.Title != "Home Videos" {
		t.Fatalf("createLibrary = %#v", resp.Data.CreateLibrary)
	}

	// The creating profile sees it in the manifest immediately.
	manifestBody := []byte(`{"query":"query { navigationManifest { libraries { id } } }"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(manifestBody))
	authorizeManager(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte("library-home-videos")) {
		t.Fatalf("manifest missing created library: %s", rec.Body.String())
	}
}

// ADR-0028 §1: createLibrary accepts an optional family scope the bound
// plugin declares, records it, and echoes it on the wire; a scope the plugin
// does not declare is rejected.
func TestCreateLibraryRecordsAndValidatesFamilyScope(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "starlog",
		Version:     "1.0.0",
		DisplayName: plugin.L("Starlog"),
		MediaLabel:  plugin.L("Entry"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		FamilyScopes: []plugin.FamilyScopeSpec{
			{Value: "films", DisplayName: plugin.L("Films")},
			{Value: "mixed", Default: true, DisplayName: plugin.L("Mixed")},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	openManagerAdminSession(t, st)

	// A declared scope is recorded and echoed.
	body := []byte(`{"query":"mutation Create($title: String!, $pluginID: String!, $familyScope: String!) { createLibrary(title: $title, pluginID: $pluginID, familyScope: $familyScope) { id familyScope } }","variables":{"title":"Movies","pluginID":"starlog","familyScope":"films"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Data struct {
			CreateLibrary LibraryInfo `json:"createLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.CreateLibrary.FamilyScope != "films" {
		t.Fatalf("createLibrary familyScope = %q, want films", resp.Data.CreateLibrary.FamilyScope)
	}

	// A scope the plugin does not declare is rejected.
	bad := []byte(`{"query":"mutation Create($title: String!, $pluginID: String!, $familyScope: String!) { createLibrary(title: $title, pluginID: $pluginID, familyScope: $familyScope) { id } }","variables":{"title":"Bogus","pluginID":"starlog","familyScope":"audiobooks"}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(bad))
	authorizeManager(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var badResp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &badResp); err != nil {
		t.Fatal(err)
	}
	if len(badResp.Errors) == 0 || badResp.Errors[0].Message != "unknown family scope" {
		t.Fatalf("errors = %#v, want \"unknown family scope\"", badResp.Errors)
	}
}

func TestCreateLibraryHonorsPluginRestrictedDefault(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:              "adult",
		Version:         "1.0.0",
		DisplayName:     plugin.L("Adult"),
		MediaLabel:      plugin.L("Media"),
		Layouts:         []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		LibraryDefaults: plugin.LibraryDefaults{Restricted: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	library, err := handler.createLibrary(context.Background(), auth.Scope{
		ProfileID: "profile-default",
		Revealed:  true,
	}, request{Variables: map[string]any{
		"title":      "Adult",
		"pluginID":   "adult",
		"restricted": false,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !library.Restricted {
		t.Fatalf("createLibrary restricted = false, want true")
	}
}

func TestCreateRestrictedLibraryRequiresReveal(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// Admin Session passes the Console gate; Reveal is the separate requirement
	// under test (an open Admin Session does not imply Reveal).
	openManagerAdminSession(t, st)
	body := []byte(`{"query":"mutation CreateLibrary($title: String!, $restricted: Boolean!) { createLibrary(title: $title, restricted: $restricted) { id } }","variables":{"title":"Secret","restricted":true}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "reveal required to create a restricted library" {
		t.Fatalf("errors = %#v", resp.Errors)
	}
}

func TestDeleteLibraryIsAdminGatedAndHidesLibraryFromManifest(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// A session without an open Admin Session must be refused (deleteLibrary is a
	// Console operation, ADR-0007 §4).
	viewerBody := []byte(`{"query":"mutation DeleteLibrary($libraryID: String!) { deleteLibrary(libraryID: $libraryID) }","variables":{"libraryID":"library-local"}}`)
	viewerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(viewerBody))
	authorizeManager(viewerReq)
	viewerRec := httptest.NewRecorder()
	handler.ServeHTTP(viewerRec, viewerReq)
	if !bytes.Contains(viewerRec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin deleteLibrary = %s", viewerRec.Body.String())
	}

	// With an open Admin Session, the operator can delete.
	openManagerAdminSession(t, st)
	body := []byte(`{"query":"mutation DeleteLibrary($libraryID: String!) { deleteLibrary(libraryID: $libraryID) }","variables":{"libraryID":"library-local"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("deleteLibrary errors = %#v", resp.Errors)
	}

	// Deleted library must not appear in the navigation manifest.
	manifestBody := []byte(`{"query":"query { navigationManifest { libraries { id } } }"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(manifestBody))
	authorizeManager(req2)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if bytes.Contains(rec2.Body.Bytes(), []byte("library-local")) {
		t.Fatalf("manifest still contains deleted library: %s", rec2.Body.String())
	}

	// Deleting an already-deleted library must return an error.
	req3 := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req3)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	var resp3 struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec3.Body.Bytes(), &resp3); err != nil {
		t.Fatal(err)
	}
	if len(resp3.Errors) == 0 {
		t.Fatalf("re-deleting already-deleted library expected an error, got none: %s", rec3.Body.String())
	}
}

func TestSetLibraryPreviewClipsMutationIsAdminGatedAuditedAndEchoesState(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	body := []byte(`{"query":"mutation SetLibraryPreviewClips($libraryID: String!, $enabled: Boolean!) { setLibraryPreviewClips(libraryID: $libraryID, enabled: $enabled) { id previewClips } }","variables":{"libraryID":"library-local","enabled":false}}`)

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(viewerReq)
	viewerRec := httptest.NewRecorder()
	handler.ServeHTTP(viewerRec, viewerReq)
	var denied struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(viewerRec.Body.Bytes(), &denied); err != nil {
		t.Fatal(err)
	}
	if len(denied.Errors) == 0 || denied.Errors[0].Message != "admin session required" {
		t.Fatalf("non-admin setLibraryPreviewClips = %s", viewerRec.Body.String())
	}

	openManagerAdminSession(t, st)
	managerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(managerReq)
	managerRec := httptest.NewRecorder()
	handler.ServeHTTP(managerRec, managerReq)
	var changed struct {
		Data struct {
			SetLibraryPreviewClips LibraryInfo `json:"setLibraryPreviewClips"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(managerRec.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if len(changed.Errors) != 0 {
		t.Fatalf("setLibraryPreviewClips errors = %#v", changed.Errors)
	}
	if changed.Data.SetLibraryPreviewClips.ID != "library-local" || changed.Data.SetLibraryPreviewClips.PreviewClips {
		t.Fatalf("setLibraryPreviewClips = %#v, want library-local with previewClips=false", changed.Data.SetLibraryPreviewClips)
	}
	enabled, err := st.LibraryPreviewClips(context.Background(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("store preview-clip toggle still enabled after mutation")
	}

	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != store.ActivityLibraryPreviewClips ||
		entries[0].TargetType != "library" || entries[0].TargetID != "library-local" ||
		entries[0].LibraryID != "library-local" {
		t.Fatalf("activity entries = %+v, want audited library preview-clip toggle", entries)
	}

	body = []byte(`{"query":"mutation SetLibraryPreviewClips($libraryID: String!, $enabled: Boolean!) { setLibraryPreviewClips(libraryID: $libraryID, enabled: $enabled) { id previewClips } }","variables":{"libraryID":"library-local","enabled":true}}`)
	managerReq = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(managerReq)
	managerRec = httptest.NewRecorder()
	handler.ServeHTTP(managerRec, managerReq)
	changed = struct {
		Data struct {
			SetLibraryPreviewClips LibraryInfo `json:"setLibraryPreviewClips"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}{}
	if err := json.Unmarshal(managerRec.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if len(changed.Errors) != 0 || !changed.Data.SetLibraryPreviewClips.PreviewClips {
		t.Fatalf("reenable setLibraryPreviewClips = %s", managerRec.Body.String())
	}
}

func TestLibraryRootsMutationIsAdminGatedAuditedAndEchoesState(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	body := []byte(`{"query":"mutation SetLibraryRoots($libraryID: String!, $roots: [String!]!) { setLibraryRoots(libraryID: $libraryID, roots: $roots) { id roots } }","variables":{"libraryID":"library-local","roots":["` + strings.ReplaceAll(root, `\`, `\\`) + `"]}}`)

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(viewerReq)
	viewerRec := httptest.NewRecorder()
	handler.ServeHTTP(viewerRec, viewerReq)
	if !bytes.Contains(viewerRec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin setLibraryRoots = %s", viewerRec.Body.String())
	}

	openManagerAdminSession(t, st)
	managerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(managerReq)
	managerRec := httptest.NewRecorder()
	handler.ServeHTTP(managerRec, managerReq)
	var changed struct {
		Data struct {
			SetLibraryRoots LibraryInfo `json:"setLibraryRoots"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(managerRec.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if len(changed.Errors) != 0 {
		t.Fatalf("setLibraryRoots errors = %#v", changed.Errors)
	}
	if len(changed.Data.SetLibraryRoots.Roots) != 1 || changed.Data.SetLibraryRoots.Roots[0] != root {
		t.Fatalf("setLibraryRoots = %#v, want normalized root %q", changed.Data.SetLibraryRoots, root)
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != store.ActivityLibraryRoots ||
		entries[0].TargetType != "library" || entries[0].TargetID != "library-local" ||
		entries[0].LibraryID != "library-local" {
		t.Fatalf("activity entries = %+v, want audited library root update", entries)
	}
}

func TestScanLibraryOmittedRootUsesPersistedRoots(t *testing.T) {
	st := storetest.Open(t)
	rootA := t.TempDir()
	rootB := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{rootB, rootA}); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	body := []byte(`{"query":"mutation ScanLibrary($libraryID: String!) { scanLibrary(libraryID: $libraryID) { id kind status } }","variables":{"libraryID":"library-local"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"scanLibrary"`)) {
		t.Fatalf("scanLibrary response = %s", rec.Body.String())
	}
	jobs, err := st.ListJobs(context.Background(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	scans := 0
	for _, job := range jobs {
		if job.Kind == scan.JobKind {
			scans++
		}
	}
	if scans != 2 {
		t.Fatalf("queued scan jobs = %d, want 2; jobs=%#v", scans, jobs)
	}
}

func TestSchedulesQueryAndUpdateMutation(t *testing.T) {
	st := storetest.Open(t)
	now := time.Now().UTC().Add(-2 * time.Hour)
	if err := st.CreateSchedule(context.Background(), store.Schedule{
		ID: "database-backup", JobKind: "database-backup",
		Trigger: store.ScheduleTrigger{Kind: store.ScheduleTriggerEvery, N: 1, Unit: store.ScheduleEveryHours},
		Enabled: true, LastCompletedEnqueue: now,
	}); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	update := []byte(`{"query":"mutation UpdateSchedule($id: String!, $trigger: ScheduleTriggerInput, $enabled: Boolean) { updateSchedule(id: $id, trigger: $trigger, enabled: $enabled) { id enabled trigger { kind hour minute } nextDue lastEnqueued } }","variables":{"id":"database-backup","enabled":false,"trigger":{"kind":"daily","hour":3,"minute":0}}}`)

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(update))
	authorizeManager(viewerReq)
	viewerRec := httptest.NewRecorder()
	handler.ServeHTTP(viewerRec, viewerReq)
	if !bytes.Contains(viewerRec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin updateSchedule = %s", viewerRec.Body.String())
	}

	openManagerAdminSession(t, st)
	managerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(update))
	authorizeManager(managerReq)
	managerRec := httptest.NewRecorder()
	handler.ServeHTTP(managerRec, managerReq)
	var changed struct {
		Data struct {
			UpdateSchedule ScheduleInfo `json:"updateSchedule"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(managerRec.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if len(changed.Errors) != 0 {
		t.Fatalf("updateSchedule errors = %#v", changed.Errors)
	}
	if changed.Data.UpdateSchedule.Enabled || changed.Data.UpdateSchedule.Trigger.Kind != "daily" ||
		changed.Data.UpdateSchedule.Trigger.Hour != 3 || changed.Data.UpdateSchedule.NextDue == "" ||
		changed.Data.UpdateSchedule.LastEnqueued == "" {
		t.Fatalf("updateSchedule = %#v", changed.Data.UpdateSchedule)
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != store.ActivityScheduleUpdate ||
		entries[0].TargetType != "schedule" || entries[0].TargetID != "database-backup" {
		t.Fatalf("activity entries = %+v, want audited schedule update", entries)
	}

	queryReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { schedules { id jobKind enabled trigger { kind hour minute } nextDue lastEnqueued } }"}`)))
	authorizeManager(queryReq)
	queryRec := httptest.NewRecorder()
	handler.ServeHTTP(queryRec, queryReq)
	if !bytes.Contains(queryRec.Body.Bytes(), []byte(`"database-backup"`)) {
		t.Fatalf("schedules query = %s", queryRec.Body.String())
	}
}

func TestUpdateScheduleRejectsMissingDailyFields(t *testing.T) {
	st := storetest.Open(t)
	if err := st.CreateSchedule(context.Background(), store.Schedule{
		ID: "database-backup", JobKind: "database-backup",
		Trigger: store.ScheduleTrigger{Kind: store.ScheduleTriggerEvery, N: 1, Unit: store.ScheduleEveryHours},
		Enabled: true, LastCompletedEnqueue: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	body := []byte(`{"query":"mutation UpdateSchedule($id: String!, $trigger: ScheduleTriggerInput) { updateSchedule(id: $id, trigger: $trigger) { id } }","variables":{"id":"database-backup","trigger":{"kind":"daily","minute":0}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte("daily trigger requires hour and minute")) {
		t.Fatalf("updateSchedule missing daily fields = %s", rec.Body.String())
	}
}

func TestCreateProfileManagedNeverGetsRestrictedLibraries(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// Revealed manager creates a managed profile; restricted libraries must
	// still be excluded from the grant (managed = no reveal path, ever).
	reveal := `{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"1234"}}`
	post(reveal, authorizeManager)
	body := `{"query":"mutation CreateProfile($name: String!, $kind: String!) { createProfile(name: $name, kind: $kind) { id name kind } }","variables":{"name":"Kids","kind":"managed"}}`
	rec := post(body, authorizeManager)
	var resp struct {
		Data struct {
			CreateProfile ProfileInfo `json:"createProfile"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.CreateProfile.ID != "profile-kids" || resp.Data.CreateProfile.Kind != "managed" {
		t.Fatalf("createProfile = %#v", resp.Data.CreateProfile)
	}

	// A viewer session (no Admin Session) cannot create profiles.
	rec = post(body, authorizeViewer)
	var errResp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatal(err)
	}
	if len(errResp.Errors) == 0 || errResp.Errors[0].Message != "admin session required" {
		t.Fatalf("viewer createProfile errors = %#v", errResp.Errors)
	}
}

func TestCreateProfileAcceptsPlaybackPolicyFields(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	body := `{"query":"mutation CreateProfile($name: String!, $kind: String!) { createProfile(name: $name, kind: $kind) { id maxConcurrentStreams transcodeAllowed audioLanguages subtitleLanguages subtitleMode preferSDH } }","variables":{"name":"Salon","kind":"managed","maxConcurrentStreams":2,"transcodeAllowed":false,"audioLanguages":["fr-CA","en"],"subtitleLanguages":["fr"],"subtitleMode":"smart","preferSDH":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			CreateProfile ProfileInfo `json:"createProfile"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	profile := resp.Data.CreateProfile
	if profile.MaxConcurrentStreams == nil || *profile.MaxConcurrentStreams != 2 {
		t.Fatalf("MaxConcurrentStreams = %v, want 2", profile.MaxConcurrentStreams)
	}
	if profile.TranscodeAllowed || !profile.PreferSDH || profile.SubtitleMode != "smart" {
		t.Fatalf("policy fields = %#v", profile)
	}
	if len(profile.AudioLanguages) != 2 || profile.AudioLanguages[0] != "fr-CA" {
		t.Fatalf("AudioLanguages = %#v, want ordered [fr-CA en]", profile.AudioLanguages)
	}
	if len(profile.SubtitleLanguages) != 1 || profile.SubtitleLanguages[0] != "fr" {
		t.Fatalf("SubtitleLanguages = %#v, want [fr]", profile.SubtitleLanguages)
	}
}

func TestUpdateProfilePartialWriteEchoesStoredProfile(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// First write sets the identity + cap + audio preference, second flips disabled only:
	// the earlier fields must survive the partial write.
	first := `{"query":"mutation UpdateProfile($profileID: String!) { updateProfile(profileID: $profileID) { id } }","variables":{"profileID":"profile-default","name":"Default Edited","kind":"adult","maxConcurrentStreams":3,"audioLanguages":["en"]}}`
	if rec := post(first, authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("profile-default")) {
		t.Fatalf("first updateProfile = %s", rec.Body.String())
	}
	second := `{"query":"mutation UpdateProfile($profileID: String!) { updateProfile(profileID: $profileID) { id name kind maxConcurrentStreams disabled audioLanguages } }","variables":{"profileID":"profile-default","disabled":true}}`
	rec := post(second, authorizeManager)

	var resp struct {
		Data struct {
			UpdateProfile ProfileInfo `json:"updateProfile"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	profile := resp.Data.UpdateProfile
	if !profile.Disabled {
		t.Fatal("Disabled = false, want true")
	}
	if profile.Name != "Default Edited" || profile.Kind != "adult" {
		t.Fatalf("Profile identity = %q/%q, want edited adult", profile.Name, profile.Kind)
	}
	if profile.MaxConcurrentStreams == nil || *profile.MaxConcurrentStreams != 3 {
		t.Fatalf("MaxConcurrentStreams = %v, want 3 preserved across partial write", profile.MaxConcurrentStreams)
	}
	if len(profile.AudioLanguages) != 1 || profile.AudioLanguages[0] != "en" {
		t.Fatalf("AudioLanguages = %#v, want [en] preserved", profile.AudioLanguages)
	}
}

func TestUpdateProfileRequiresAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	body := `{"query":"mutation UpdateProfile($profileID: String!) { updateProfile(profileID: $profileID) { id } }","variables":{"profileID":"profile-default","disabled":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("non-admin updateProfile errors = %#v", resp.Errors)
	}
}

func TestUpdateProfileManagedKindEmitsInvalidation(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	body := `mutation UpdateProfile($profileID: String!, $kind: String!) { updateProfile(profileID: $profileID, kind: $kind) { id kind } }`

	resp := postGraphJSON(t, handler, authorizeManager, body, map[string]any{
		"profileID": "profile-default",
		"kind":      "managed",
	})
	if len(resp.Errors) != 0 {
		t.Fatalf("updateProfile errors = %#v", resp.Errors)
	}

	events, err := st.ReadEventOutbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	for _, e := range events {
		if e.Kind == store.InvalidationKind && e.AggregateType == store.OutboxAggregateInvalidation && e.AggregateID == "profile-default" {
			return
		}
	}
	t.Fatal("updateProfile managed kind change emitted no client.invalidate event")
}

func TestUpdateProfileRejectsBadInput(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	for name, body := range map[string]string{
		"unknown subtitleMode": `{"query":"mutation UpdateProfile($profileID: String!) { updateProfile(profileID: $profileID) { id } }","variables":{"profileID":"profile-default","subtitleMode":"karaoke"}}`,
		"empty name":           `{"query":"mutation UpdateProfile($profileID: String!) { updateProfile(profileID: $profileID) { id } }","variables":{"profileID":"profile-default","name":"  "}}`,
		"unknown kind":         `{"query":"mutation UpdateProfile($profileID: String!) { updateProfile(profileID: $profileID) { id } }","variables":{"profileID":"profile-default","kind":"owner"}}`,
		"no settings":          `{"query":"mutation UpdateProfile($profileID: String!) { updateProfile(profileID: $profileID) { id } }","variables":{"profileID":"profile-default"}}`,
		"missing profileID":    `{"query":"mutation UpdateProfile { updateProfile { id } }","variables":{"disabled":true}}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorizeManager(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Errors []struct{ Message string } `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(resp.Errors) == 0 {
			t.Fatalf("%s: expected a graph error, got %s", name, rec.Body.String())
		}
	}
}

func TestPluginsQueryListsActivatedBundles(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		LibraryDefaults: plugin.LibraryDefaults{
			Restricted: true,
		},
		MediaClassLabels: map[string]plugin.LocaleString{
			"feature": {"en": "Feature", "fr": "Long métrage"},
			"chapter": plugin.L("Chapter"),
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}, {
		ID:          "plain",
		Version:     "0.1.0",
		DisplayName: plugin.L("Plain"),
		MediaLabel:  plugin.L("Thing"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	body := []byte(`{"query":"query { plugins { id displayName mediaLabel libraryDefaults { restricted } mediaClassLabels } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string][]struct {
			PluginInfo
			LibraryDefaults plugin.LibraryDefaults `json:"libraryDefaults"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := resp.Data["plugins"]
	if len(got) != 2 || got[0].ID != "demo" || got[0].DisplayName.Resolve("") != "Demo" || got[0].MediaLabel.Resolve("") != "Item" {
		t.Fatalf("plugins = %#v, want the demo and plain bundles", got)
	}
	if !got[0].LibraryDefaults.Restricted {
		t.Fatalf("demo libraryDefaults.restricted = false, want true")
	}
	// Class labels ride PluginInfo as locale maps, resolved like every
	// ADR-0034 display string (full map travels as an object, en-only as
	// the shorthand string).
	labels := got[0].MediaClassLabels
	if len(labels) != 2 || labels["feature"].Resolve("fr-ca") != "Long métrage" ||
		labels["feature"].Resolve("") != "Feature" || labels["chapter"].Resolve("") != "Chapter" {
		t.Fatalf("mediaClassLabels = %#v, want the declared locale maps", labels)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"feature":{`)) {
		t.Fatalf("feature label did not travel as a locale map object: %s", rec.Body.String())
	}
	// A bundle with no class labels omits the field cleanly: the key
	// appears once in the body (the demo bundle), never for plain.
	if got[1].MediaClassLabels != nil {
		t.Fatalf("plain bundle mediaClassLabels = %#v, want omitted", got[1].MediaClassLabels)
	}
	if bytes.Count(rec.Body.Bytes(), []byte(`"mediaClassLabels"`)) != 1 {
		t.Fatalf("empty mediaClassLabels should be omitted: %s", rec.Body.String())
	}
}

func TestEntityQueries(t *testing.T) {
	// feature:entity-manager
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	entity := store.Entity{
		ID:        "entity-maker",
		LibraryID: "library-local",
		Kind:      "maker",
		Title:     "Sample Maker",
		Fields: []store.EntityField{
			{Name: "website", Type: store.EntityFieldURL, Text: "https://example.test"},
			{Name: "active", Type: store.EntityFieldFlag, Flag: true},
		},
	}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-maker",
		Name:         "made",
		ToMediaID:    "media-alpha",
		Attributes:   map[string]string{"role": "lead"},
	}); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"query":"query($id:String!) { entity(id: $id) { id kind title fields } }","variables":{"id":"entity-maker"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("entity status = %d: %s", rec.Code, rec.Body.String())
	}
	var entityResp struct {
		Data map[string]EntityInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &entityResp); err != nil {
		t.Fatal(err)
	}
	got := entityResp.Data["entity"]
	if got.ID != "entity-maker" || got.Kind != "maker" || len(got.Fields) != 2 {
		t.Fatalf("entity = %#v", got)
	}

	body = []byte(`{"query":"query($mediaID:String!,$relation:String) { entitiesForMedia(mediaID: $mediaID, relation: $relation) { id title attributes } }","variables":{"mediaID":"media-alpha","relation":"made"}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("entitiesForMedia status = %d: %s", rec.Code, rec.Body.String())
	}
	var relatedResp struct {
		Data map[string][]RelatedEntityInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &relatedResp); err != nil {
		t.Fatal(err)
	}
	related := relatedResp.Data["entitiesForMedia"]
	if len(related) != 1 || related[0].ID != "entity-maker" || related[0].Attributes["role"] != "lead" {
		t.Fatalf("entitiesForMedia = %#v", related)
	}
}

// TestEntityDisplayStringsTravelTheWire pins ADR-0034 §1 on the instance
// reads: a declared kind's DisplayName and field Labels (locale maps)
// decorate entity(id), and the bundle's MediaField labels decorate
// mediaFields(mediaID) — resolved through the item's plugin-bound Library.
func TestEntityDisplayStringsTravelTheWire(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "starlog",
		Version:     "1.0.0",
		DisplayName: plugin.L("Starlog"),
		MediaLabel:  plugin.L("Entry"),
		MediaFields: []plugin.FieldSpec{{Name: "phase", Type: "text",
			Label: plugin.LocaleString{"en": "Phase", "fr": "Phase de vol"}}},
		EntityKinds: []plugin.EntityKind{{
			Name:        "navigator",
			DisplayName: plugin.LocaleString{"en": "Navigator", "fr": "Navigateur"},
			Archetype:   "person",
			Fields: []plugin.FieldSpec{{Name: "rank", Type: "text",
				Label: plugin.LocaleString{"en": "Rank", "fr": "Grade"}}},
		}},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Star Library", false, "starlog", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{
		ID:        "entity-nav",
		LibraryID: library.ID,
		Kind:      "navigator",
		Title:     "Nav One",
		Fields: []store.EntityField{
			{Name: "rank", Type: store.EntityFieldText, Text: "captain"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"query":"query($id:String!) { entity(id: $id) { id kind kindDisplayName title fields } }","variables":{"id":"entity-nav"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("entity status = %d: %s", rec.Code, rec.Body.String())
	}
	var entityResp struct {
		Data map[string]EntityInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &entityResp); err != nil {
		t.Fatal(err)
	}
	entity := entityResp.Data["entity"]
	if entity.KindDisplayName.Resolve("fr-ca") != "Navigateur" || entity.KindDisplayName.Resolve("") != "Navigator" {
		t.Fatalf("kindDisplayName = %#v, want the declared locale map", entity.KindDisplayName)
	}
	if len(entity.Fields) != 1 || entity.Fields[0].Label.Resolve("fr") != "Grade" {
		t.Fatalf("entity fields = %#v, want the rank label locale map", entity.Fields)
	}
	// The full map travels as a JSON object, not the en-only shorthand.
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"kindDisplayName":{`)) {
		t.Fatalf("kindDisplayName did not travel as a locale map object: %s", rec.Body.String())
	}

	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          library.ID,
		Path:               "/srv/star/one.mkv",
		Title:              "Entry One",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "fp-entry-one",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{
		{Name: "phase", Type: store.EntityFieldText, Text: "flight"},
	}); err != nil {
		t.Fatal(err)
	}
	body = []byte(`{"query":"query($mediaID:String!) { mediaFields(mediaID: $mediaID) { name type value label } }","variables":{"mediaID":"` + string(media.ID) + `"}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mediaFields status = %d: %s", rec.Code, rec.Body.String())
	}
	var fieldsResp struct {
		Data map[string][]EntityFieldInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fieldsResp); err != nil {
		t.Fatal(err)
	}
	fields := fieldsResp.Data["mediaFields"]
	if len(fields) != 1 || fields[0].Label.Resolve("fr") != "Phase de vol" {
		t.Fatalf("mediaFields = %#v, want the phase label locale map", fields)
	}
}

func TestSensitivePluginFieldsWithheldAfterReveal(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "privacy",
		Version:     "1.0.0",
		DisplayName: plugin.L("Privacy"),
		MediaLabel:  plugin.L("Item"),
		MediaFields: []plugin.FieldSpec{
			{Name: "public-note", Type: "text"},
			{Name: "private-note", Type: "text", Sensitive: true},
		},
		EntityKinds: []plugin.EntityKind{{
			Name:      "contact",
			Archetype: "person",
			Fields: []plugin.FieldSpec{
				{Name: "public-note", Type: "text"},
				{Name: "private-note", Type: "text", Sensitive: true},
			},
		}},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Private Library", true, "privacy", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          library.ID,
		Path:               "/srv/private/item.mkv",
		Title:              "Private Item",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "fp-private-item",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{
		{Name: "public-note", Type: store.EntityFieldText, Text: "returned"},
		{Name: "private-note", Type: store.EntityFieldText, Text: "withheld"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{
		ID:        "entity-private",
		LibraryID: library.ID,
		Kind:      "contact",
		Title:     "Private Entity",
		Fields: []store.EntityField{
			{Name: "public-note", Type: store.EntityFieldText, Text: "returned"},
			{Name: "private-note", Type: store.EntityFieldText, Text: "withheld"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"query": `query Sensitive($mediaID: String!, $id: String!) {
			mediaFields(mediaID: $mediaID) { name value }
			entity(id: $id) { fields { name value } }
		}`,
		"variables": map[string]any{
			"mediaID": string(media.ID),
			"id":      "entity-private",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeRevealed(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			MediaFields []EntityFieldInfo `json:"mediaFields"`
			Entity      EntityInfo        `json:"entity"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if len(resp.Data.MediaFields) != 1 || resp.Data.MediaFields[0].Name != "public-note" {
		t.Fatalf("mediaFields = %#v, want only public-note", resp.Data.MediaFields)
	}
	if len(resp.Data.Entity.Fields) != 1 || resp.Data.Entity.Fields[0].Name != "public-note" {
		t.Fatalf("entity.fields = %#v, want only public-note", resp.Data.Entity.Fields)
	}
}

func TestMediaFieldsQuery(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	if err := st.SetMediaFields(ctx, "media-alpha", []store.MediaField{
		{Name: "kind-of-thing", Type: store.EntityFieldText, Text: "flight"},
		{Name: "verified", Type: store.EntityFieldFlag, Flag: true},
	}); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"query":"query($mediaID:String!) { mediaFields(mediaID: $mediaID) { name type value } }","variables":{"mediaID":"media-alpha"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mediaFields status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string][]EntityFieldInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	got := resp.Data["mediaFields"]
	if len(got) != 2 {
		t.Fatalf("mediaFields = %#v, want 2 fields", got)
	}
	if got[0].Name != "kind-of-thing" || got[0].Type != "text" || got[0].Value != "flight" {
		t.Fatalf("mediaFields[0] = %#v", got[0])
	}
	if got[1].Name != "verified" || got[1].Type != "flag" || got[1].Value != "true" {
		t.Fatalf("mediaFields[1] = %#v", got[1])
	}

	// media-archive is restricted: hidden until reveal, like every read.
	body = []byte(`{"query":"query($mediaID:String!) { mediaFields(mediaID: $mediaID) { name type value } }","variables":{"mediaID":"media-archive"}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var hidden struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hidden); err != nil {
		t.Fatal(err)
	}
	if len(hidden.Errors) == 0 || hidden.Errors[0].Message != "unknown media" {
		t.Fatalf("hidden mediaFields = %s, want unknown media error", rec.Body.String())
	}
}

// identifyTestEngine fakes the scrape engine behind identify.Engine.
type identifyTestEngine struct{}

func (identifyTestEngine) Search(_ context.Context, _ plugin.ScraperSpec, query string, _ ...scrape.Variables) ([]scrape.Result, error) {
	if query != "blue harvest" {
		return []scrape.Result{}, nil
	}
	return []scrape.Result{
		{
			ID:      "42",
			Fields:  map[string]any{"title": "Blue Harvest", "summary": "A classic.", "year": "1977"},
			Artwork: map[string]string{"poster": "https://img.example.test/42.jpg"},
		},
	}, nil
}

func (identifyTestEngine) Lookup(_ context.Context, _ plugin.ScraperSpec, id string, _ ...scrape.Variables) (scrape.Result, error) {
	if id != "42" {
		return scrape.Result{}, errors.New("not found")
	}
	return scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest", "summary": "A classic.", "year": "1977"},
	}, nil
}

type orderedHydrationEngine struct{}

func (orderedHydrationEngine) Search(_ context.Context, _ plugin.ScraperSpec, _ string, _ ...scrape.Variables) ([]scrape.Result, error) {
	return nil, nil
}

func (orderedHydrationEngine) Lookup(_ context.Context, _ plugin.ScraperSpec, id string, _ ...scrape.Variables) (scrape.Result, error) {
	switch id {
	case "first":
		return scrape.Result{
			Fields: map[string]any{"title": "First"},
			Entities: []scrape.EntityResult{
				{Kind: "container", Relation: "contains", Name: "Sequence", SortOrder: graphIntPtr(0)},
			},
		}, nil
	case "second":
		return scrape.Result{
			Fields: map[string]any{"title": "Second"},
			Entities: []scrape.EntityResult{
				{Kind: "container", Relation: "contains", Name: "Sequence", SortOrder: graphIntPtr(1)},
			},
		}, nil
	default:
		return scrape.Result{}, errors.New("not found")
	}
}

func graphIntPtr(value int) *int {
	return &value
}

func newIdentifyTestHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Scrapers: []plugin.ScraperSpec{{
			ID:              "lookup",
			DisplayName:     plugin.L("Demo Lookup"),
			Kind:            "http-json",
			Endpoint:        "https://demo.example.test",
			SearchTemplate:  "/search?q={query}",
			CandidatesPath:  "results",
			CandidateIDPath: "id",
			LookupTemplate:  "/title/{id}",
			FieldMap:        map[string]string{"title": "name", "summary": "overview", "year": "year"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	handler.SetIdentify(identify.NewService(identifyTestEngine{}, registry, st))
	return handler, st
}

func TestIdentifyMutationsRequireManagerRole(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	for _, body := range []string{
		`{"query":"query IdentifySearch($pluginID: String!, $scraperID: String!, $query: String!) { identifySearch(pluginID: $pluginID, scraperID: $scraperID, query: $query) { id title } }","variables":{"pluginID":"demo","scraperID":"lookup","query":"blue harvest"}}`,
		`{"query":"mutation IdentifyApply($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { identifyApply(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id } }","variables":{"mediaID":"media-alpha","pluginID":"demo","scraperID":"lookup","candidateID":"42"}}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Errors []struct{ Message string } `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) == 0 || resp.Errors[0].Message != "manager role required" {
			t.Fatalf("viewer identify errors = %#v, want manager role required", resp.Errors)
		}
	}
}

func TestIdentifySearchReturnsCandidates(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	body := `{"query":"query IdentifySearch($pluginID: String!, $scraperID: String!, $query: String!) { identifySearch(pluginID: $pluginID, scraperID: $scraperID, query: $query) { id title summary year artwork } }","variables":{"pluginID":"demo","scraperID":"lookup","query":"blue harvest"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			IdentifySearch []IdentifyCandidate `json:"identifySearch"`
			Search         []SourceItem        `json:"search"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	got := resp.Data.IdentifySearch
	if len(got) != 1 || got[0].ID != "42" || got[0].Title != "Blue Harvest" || got[0].Summary != "A classic." || got[0].Year != 1977 {
		t.Fatalf("identifySearch = %#v", got)
	}
	if got[0].Artwork["poster"] != "https://img.example.test/42.jpg" {
		t.Fatalf("candidate artwork = %#v", got[0].Artwork)
	}
	// identifySearch must not also run the Media title search.
	if strings.Contains(rec.Body.String(), `"search"`) {
		t.Fatalf("identifySearch response leaked a media search: %s", rec.Body.String())
	}
}

func TestIdentifyApplyUpdatesVisibleMedia(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	body := `{"query":"mutation IdentifyApply($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { identifyApply(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id title summary year } }","variables":{"mediaID":"media-alpha","pluginID":"demo","scraperID":"lookup","candidateID":"42"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			IdentifyApply MediaInfo `json:"identifyApply"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	got := resp.Data.IdentifyApply
	if got.ID != "media-alpha" || got.Title != "Blue Harvest" || got.Summary != "A classic." || got.Year != 1977 {
		t.Fatalf("identifyApply = %#v", got)
	}
	media, err := st.Media(context.Background(), "media-alpha", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "Blue Harvest" || media.Summary != "A classic." || media.Year != 1977 {
		t.Fatalf("stored media = %#v", media)
	}
}

func TestIdentifyApplyRejectsHiddenMedia(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	// session-manager is unrevealed; media-archive lives in the restricted
	// library, so the metadata write must be refused (Reveal contract).
	body := `{"query":"mutation IdentifyApply($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { identifyApply(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id } }","variables":{"mediaID":"media-archive","pluginID":"demo","scraperID":"lookup","candidateID":"42"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "unknown media" {
		t.Fatalf("errors = %#v, want unknown media for restricted target", resp.Errors)
	}
	media, err := st.Media(context.Background(), "media-archive", store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "Archive Media" || media.Summary != "" || media.Year != 0 {
		t.Fatalf("hidden media was modified: %#v", media)
	}
}

// TestFixMatchReidentifiesAndPins covers PIPE-1's "Fix match" mutation:
// it re-identifies a Media with the operator's chosen candidate,
// pins the resulting match so it survives rescans, and clears the
// item's Unmatched-queue row.
func TestFixMatchReidentifiesAndPins(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	// A Media in the visible library with a backing file (the pin's key) plus a
	// stale Unmatched-queue row the fix should clear.
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/fixtures/fixmatch.mkv",
		Title:              "fixmatch",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:fixmatch",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := st.QueueUnmatched(ctx, store.UnmatchedItem{
		MediaID: media.ID, PluginID: "demo", ScraperID: "lookup", Confidence: 0.5, Threshold: 0.75,
	}); err != nil {
		t.Fatalf("QueueUnmatched: %v", err)
	}

	body := `{"query":"mutation FixMatch($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { fixMatch(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id title summary year } }","variables":{"mediaID":"` + string(media.ID) + `","pluginID":"demo","scraperID":"lookup","candidateID":"42"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			FixMatch MediaInfo `json:"fixMatch"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.FixMatch.ID != string(media.ID) || resp.Data.FixMatch.Title != "Blue Harvest" {
		t.Fatalf("fixMatch = %#v, want re-identified Blue Harvest", resp.Data.FixMatch)
	}
	// The match is pinned so a rescan leaves it alone.
	if pinned, _, err := st.MatchPinned(ctx, "sha256:fixmatch"); err != nil || !pinned {
		t.Fatalf("fixMatch did not pin: pinned=%v err=%v", pinned, err)
	}
	// The Unmatched-queue row is cleared now that the item is resolved.
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("fixMatch left the item queued: queued=%v err=%v", queued, err)
	}
}

// TestDismissUnmatchedClearsQueueRow confirms the dismissUnmatched mutation
// removes the Unmatched-queue entry for the target Media without requiring the
// identify service (WEB-5G / ADR-0003 §4 — store-only path).
func TestDismissUnmatchedClearsQueueRow(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/fixtures/dismiss.mkv",
		Title:              "dismiss-test",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:dismiss-test",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := st.QueueUnmatched(ctx, store.UnmatchedItem{
		MediaID: media.ID, PluginID: "demo", ScraperID: "lookup", Confidence: 0.4, Threshold: 0.75,
	}); err != nil {
		t.Fatalf("QueueUnmatched: %v", err)
	}

	body := `{"query":"mutation DismissUnmatched($mediaID: String!) { dismissUnmatched(mediaID: $mediaID) }","variables":{"mediaID":"` + string(media.ID) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data   map[string]interface{}     `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("dismissUnmatched left the item queued: queued=%v err=%v", queued, err)
	}
}

// TestDismissUnmatchedRequiresManagerRole confirms the viewer role is rejected
// (the mutation is manager-gated via the store-only dispatch block).
func TestDismissUnmatchedRequiresManagerRole(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/fixtures/dismiss-auth.mkv",
		Title:              "dismiss-auth-test",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:dismiss-auth",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := st.QueueUnmatched(ctx, store.UnmatchedItem{
		MediaID: media.ID, PluginID: "demo", ScraperID: "lookup", Confidence: 0.4, Threshold: 0.75,
	}); err != nil {
		t.Fatalf("QueueUnmatched: %v", err)
	}

	body := `{"query":"mutation DismissUnmatched($mediaID: String!) { dismissUnmatched(mediaID: $mediaID) }","variables":{"mediaID":"` + string(media.ID) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "manager role required" {
		t.Fatalf("errors = %#v, want manager role required", resp.Errors)
	}
}

// TestAddMediaExternalIDAttachesIdentifier confirms the addMediaExternalID
// mutation attaches a scheme/value pair to a visible Media item (WEB-5G /
// ADR-0003 §4 — manual external-ID assignment, store-only path).
func TestAddMediaExternalIDAttachesIdentifier(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/fixtures/extid.mkv",
		Title:              "extid-test",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:extid-test",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	body := `{"query":"mutation AddExternalID($mediaID: String!, $scheme: String!, $value: String!) { addMediaExternalID(mediaID: $mediaID, scheme: $scheme, value: $value) { id title } }","variables":{"mediaID":"` + string(media.ID) + `","scheme":"musicbrainz.recording","value":"abc-123"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			AddMediaExternalID MediaInfo `json:"addMediaExternalID"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.AddMediaExternalID.ID != string(media.ID) {
		t.Fatalf("addMediaExternalID returned ID %q, want %q", resp.Data.AddMediaExternalID.ID, media.ID)
	}
	// Confirm the ID is persisted in the store.
	ids, err := st.MediaExternalIDs(ctx, media.ID)
	if err != nil {
		t.Fatalf("MediaExternalIDs: %v", err)
	}
	found := false
	for _, id := range ids {
		if id.Scheme == "musicbrainz.recording" && id.Value == "abc-123" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("external ID not persisted: %#v", ids)
	}
}

// TestUnmatchedQueueDoesNotRequireIdentifyService confirms that unmatchedQueue
// works when no identify service is wired (it is a store-only read, unlike
// identifySearch/fixMatch which require the service).
func TestUnmatchedQueueDoesNotRequireIdentifyService(t *testing.T) {
	handler := newTestHandler(t) // no SetIdentify
	body := `{"query":"query { unmatchedQueue { mediaID title } }"}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			UnmatchedQueue []UnmatchedItemInfo `json:"unmatchedQueue"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v; unmatchedQueue must not require identify service", resp.Errors)
	}
}

func TestIdentifyMutationsUnavailableWithoutService(t *testing.T) {
	handler := newTestHandler(t) // no SetIdentify
	body := `{"query":"query { identifySearch(pluginID: \"demo\", scraperID: \"lookup\", query: \"x\") { id } }","variables":{"pluginID":"demo","scraperID":"lookup","query":"x"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "identification unavailable" {
		t.Fatalf("errors = %#v, want identification unavailable", resp.Errors)
	}
}

func TestClientPrefsRoundTripForSessionProfile(t *testing.T) {
	handler := newTestHandler(t)
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// Viewer-allowed by design: the prefs belong to the session's Profile.
	set := `{"query":"mutation SetClientPref($key: String!, $value: String!) { setClientPref(key: $key, value: $value) { key value updatedAt } }","variables":{"key":"web.subtitle-style","value":"{\"size\":\"large\"}"}}`
	var setResp struct {
		Data struct {
			SetClientPref ClientPrefInfo `json:"setClientPref"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(post(set).Body.Bytes(), &setResp); err != nil {
		t.Fatal(err)
	}
	if len(setResp.Errors) != 0 {
		t.Fatalf("setClientPref errors = %#v", setResp.Errors)
	}
	if setResp.Data.SetClientPref.Key != "web.subtitle-style" || setResp.Data.SetClientPref.UpdatedAt == "" {
		t.Fatalf("setClientPref = %#v", setResp.Data.SetClientPref)
	}

	list := `{"query":"query ClientPrefs { clientPrefs { key value updatedAt } }"}`
	var listResp struct {
		Data struct {
			ClientPrefs []ClientPrefInfo `json:"clientPrefs"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(post(list).Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Errors) != 0 {
		t.Fatalf("clientPrefs errors = %#v", listResp.Errors)
	}
	if len(listResp.Data.ClientPrefs) != 1 || listResp.Data.ClientPrefs[0].Value != `{"size":"large"}` {
		t.Fatalf("clientPrefs = %#v", listResp.Data.ClientPrefs)
	}

	// Empty value clears the preference.
	clear := `{"query":"mutation SetClientPref($key: String!, $value: String!) { setClientPref(key: $key, value: $value) { key } }","variables":{"key":"web.subtitle-style","value":""}}`
	if rec := post(clear); bytes.Contains(rec.Body.Bytes(), []byte("errors")) {
		t.Fatalf("clearing pref failed: %s", rec.Body.String())
	}
	if err := json.Unmarshal(post(list).Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Data.ClientPrefs) != 0 {
		t.Fatalf("after clear clientPrefs = %#v", listResp.Data.ClientPrefs)
	}
}

func TestSetClientPrefRejectsUnnamespacedKey(t *testing.T) {
	handler := newTestHandler(t)
	body := `{"query":"mutation SetClientPref($key: String!, $value: String!) { setClientPref(key: $key, value: $value) { key } }","variables":{"key":"theme","value":"dark"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte("namespaced")) {
		t.Fatalf("expected namespacing error, got %s", rec.Body.String())
	}
}

func TestMediaForEntityListsRelatedItems(t *testing.T) {
	// feature:entity-detail
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, store.Entity{
		ID:        "entity-maker",
		LibraryID: "library-local",
		Kind:      "maker",
		Title:     "Sample Maker",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-maker",
		Name:         "made",
		ToMediaID:    "media-alpha",
	}); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"query":"query($entityID:String!) { mediaForEntity(entityID: $entityID) { id kind title } }","variables":{"entityID":"entity-maker"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mediaForEntity status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			MediaForEntity []store.SourceItem `json:"mediaForEntity"`
			Entity         *EntityInfo        `json:"entity"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	// "mediaforentity(" contains "entity(" — the bare entity dispatch must not co-fire.
	if resp.Data.Entity != nil {
		t.Fatalf("entity dispatch co-fired: %#v", resp.Data.Entity)
	}
	items := resp.Data.MediaForEntity
	if len(items) != 1 || items[0].ID != "media-alpha" || items[0].Kind != "media" {
		t.Fatalf("mediaForEntity = %#v", items)
	}
}

func TestSubscriptionMutationsRequireManagerRole(t *testing.T) {
	handler := newTestHandler(t)
	for _, query := range []string{
		`mutation { addSubscription(libraryID: "library-local", feedURL: "https://feeds.example.com/a", root: "/media") { id } }`,
		`mutation { removeSubscription(id: "subscription-x") { id } }`,
		`query { subscriptions(libraryID: "library-local") { id } }`,
	} {
		payload, err := json.Marshal(map[string]any{"query": query})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(payload))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if !strings.Contains(rec.Body.String(), "manager role required") {
			t.Fatalf("viewer response for %q = %s, want manager gate", query, rec.Body.String())
		}
	}
}

func TestSubscriptionLifecycleOverGraph(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	exec := func(query string, variables map[string]any) map[string]json.RawMessage {
		t.Helper()
		payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(payload))
		authorizeManager(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data   map[string]json.RawMessage `json:"data"`
			Errors []graphError               `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) > 0 {
			t.Fatalf("graph errors for %q: %+v", query, resp.Errors)
		}
		return resp.Data
	}

	data := exec(
		`mutation Add($libraryID: String!, $feedURL: String!, $root: String!, $title: String) { addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root, title: $title) { id libraryID feedURL root title } }`,
		map[string]any{"libraryID": "library-local", "feedURL": "https://feeds.example.com/daily?token=hush", "root": "/media/incoming", "title": "Daily"},
	)
	var added SubscriptionInfo
	if err := json.Unmarshal(data["addSubscription"], &added); err != nil {
		t.Fatal(err)
	}
	if added.ID == "" || added.LibraryID != "library-local" || added.Root != "/media/incoming" || added.Title != "Daily" {
		t.Fatalf("added = %+v", added)
	}
	if added.FeedURL != "https://feeds.example.com/daily?token=hush" {
		t.Fatalf("added.FeedURL = %q (manager surface keeps the URL)", added.FeedURL)
	}

	data = exec(
		`query Subs($libraryID: String!) { subscriptions(libraryID: $libraryID) { id title } }`,
		map[string]any{"libraryID": "library-local"},
	)
	var listed []SubscriptionInfo
	if err := json.Unmarshal(data["subscriptions"], &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != added.ID {
		t.Fatalf("listed = %+v", listed)
	}

	data = exec(
		`mutation Remove($id: String!) { removeSubscription(id: $id) { id } }`,
		map[string]any{"id": added.ID},
	)
	var removed SubscriptionInfo
	if err := json.Unmarshal(data["removeSubscription"], &removed); err != nil {
		t.Fatal(err)
	}
	if removed.ID != added.ID {
		t.Fatalf("removed = %+v", removed)
	}

	data = exec(
		`query Subs($libraryID: String!) { subscriptions(libraryID: $libraryID) { id } }`,
		map[string]any{"libraryID": "library-local"},
	)
	if err := json.Unmarshal(data["subscriptions"], &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("listed after remove = %+v", listed)
	}
}

// fakeSubscriptionSecrets is the test secure store: it records the secrets
// written by ref so a test can assert the private-feed credential lands here
// (invariant #5) and never in the catalog DB.
type fakeSubscriptionSecrets struct {
	secrets map[string]string
}

func (f *fakeSubscriptionSecrets) SetSecret(ref, value string) error {
	if f.secrets == nil {
		f.secrets = map[string]string{}
	}
	if value == "" {
		delete(f.secrets, ref)
		return nil
	}
	f.secrets[ref] = value
	return nil
}

// TestPrivateFeedSecretGoesToSecureStoreNotDB is the invariant-#5 acceptance:
// adding a private feed routes the Authorization header to the secure store
// keyed by a ref, the catalog row holds ONLY the ref (never the secret), and
// the SubscriptionInfo echo never returns the secret — only a hasAuth boolean.
func TestPrivateFeedSecretGoesToSecureStoreNotDB(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	secrets := &fakeSubscriptionSecrets{}
	handler.SetSubscriptionSecrets(secrets)

	const secret = "Bearer super-private-feed-token"
	body, err := json.Marshal(map[string]any{
		"query": `mutation Add($libraryID: String!, $feedURL: String!, $root: String!, $authHeader: String) {
			addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root, authHeader: $authHeader) { id feedURL hasAuth }
		}`,
		"variables": map[string]any{
			"libraryID":  "library-local",
			"feedURL":    "https://feeds.example.com/private",
			"root":       "/media/incoming",
			"authHeader": secret,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	// (b) The echo never returns the secret — only hasAuth true.
	if strings.Contains(raw, secret) || strings.Contains(raw, "super-private-feed-token") {
		t.Fatalf("addSubscription echo leaks the private-feed secret: %s", raw)
	}
	if !strings.Contains(raw, "\"hasAuth\":true") {
		t.Fatalf("addSubscription echo = %s, want hasAuth true", raw)
	}

	var resp struct {
		Data struct {
			AddSubscription SubscriptionInfo `json:"addSubscription"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	added := resp.Data.AddSubscription

	// (a) The secret lives in the secure store under the derived ref…
	stored, err := st.Subscription(context.Background(), store.SubscriptionID(added.ID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.AuthRef == "" {
		t.Fatal("stored subscription has no auth_ref")
	}
	if got := secrets.secrets[stored.AuthRef]; got != secret {
		t.Fatalf("secure-store secret for %q = %q, want the header", stored.AuthRef, got)
	}
	// …and the catalog row holds ONLY the ref, never the literal header.
	if strings.Contains(stored.AuthRef, secret) || stored.AuthRef == secret {
		t.Fatalf("auth_ref carries the secret: %q", stored.AuthRef)
	}
	for _, row := range dumpSubscriptionRow(t, st, added.ID) {
		if strings.Contains(row, "super-private-feed-token") {
			t.Fatalf("catalog DB row carries the plaintext secret: %s", row)
		}
	}

	// Removing the subscription reaps the secret from the secure store.
	rmBody, _ := json.Marshal(map[string]any{
		"query":     `mutation Rm($id: String!) { removeSubscription(id: $id) { id } }`,
		"variables": map[string]any{"id": added.ID},
	})
	rmReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(rmBody))
	authorizeManager(rmReq)
	handler.ServeHTTP(httptest.NewRecorder(), rmReq)
	if _, ok := secrets.secrets[stored.AuthRef]; ok {
		t.Fatalf("secret survived removal under %q", stored.AuthRef)
	}
}

// TestAddPrivateFeedRejectedWithoutSecureStore proves a private-feed credential
// is never persisted plaintext when no secure store is wired: the mutation
// rejects the inbound header rather than dropping it into the DB.
func TestAddPrivateFeedRejectedWithoutSecureStore(t *testing.T) {
	handler := newTestHandler(t) // no SetSubscriptionSecrets
	resp := postGraphJSON(t, handler, authorizeManager,
		`mutation Add($libraryID: String!, $feedURL: String!, $root: String!, $authHeader: String) {
			addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root, authHeader: $authHeader) { id }
		}`,
		map[string]any{
			"libraryID":  "library-local",
			"feedURL":    "https://feeds.example.com/private",
			"root":       "/media/incoming",
			"authHeader": "Bearer leaked-if-stored",
		})
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "private-feed credentials are unavailable") {
		t.Fatalf("errors = %+v, want unavailable rejection", resp.Errors)
	}
	for _, e := range resp.Errors {
		if strings.Contains(e.Message, "leaked-if-stored") {
			t.Fatalf("error text leaks the secret: %s", e.Message)
		}
	}
}

// dumpSubscriptionRow returns every text column of the subscription row as raw
// strings so a test can assert no plaintext secret hides in any column.
func dumpSubscriptionRow(t *testing.T, st *store.Store, id string) []string {
	t.Helper()
	sub, err := st.Subscription(context.Background(), store.SubscriptionID(id))
	if err != nil {
		t.Fatal(err)
	}
	return []string{string(sub.ID), string(sub.LibraryID), sub.FeedURL, sub.AuthRef, sub.Root, sub.Title}
}

func TestFollowMutationAllowsAdultViewerAndRejectsManagedProfile(t *testing.T) {
	handler := newTestHandler(t)
	var added SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`mutation Add($libraryID: String!, $feedURL: String!, $root: String!) { addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root) { id } }`,
		map[string]any{"libraryID": "library-local", "feedURL": "https://feeds.example.com/follow", "root": "/media/follow"},
		"addSubscription", &added)

	var followed SubscriptionInfo
	execGraph(t, handler, authorizeViewer,
		`mutation Follow($id: String!, $on: Boolean!) { setFollow(subscriptionID: $id, on: $on) { id followed } }`,
		map[string]any{"id": added.ID, "on": true},
		"setFollow", &followed)
	if !followed.Followed {
		t.Fatalf("setFollow followed = false, want true")
	}
	var listed []SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`query Subs($libraryID: String!) { subscriptions(libraryID: $libraryID) { id followed } }`,
		map[string]any{"libraryID": "library-local"},
		"subscriptions", &listed)
	if len(listed) != 1 || !listed[0].Followed {
		t.Fatalf("listed = %+v, want followed true for calling profile", listed)
	}

	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	resp := postGraphJSON(t, handler, authorizeManaged,
		`mutation Follow($id: String!, $on: Boolean!) { setFollow(subscriptionID: $id, on: $on) { id followed } }`,
		map[string]any{"id": added.ID, "on": true})
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "managed profiles cannot follow subscriptions") {
		t.Fatalf("managed follow errors = %+v", resp.Errors)
	}
}

func TestSetFollowHidesRestrictedSubscriptionsAndLeaksNoSecrets(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	hiddenSub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-archive",
		FeedURL:   "https://feeds.example.com/secret?token=feed-secret",
		AuthRef:   "subscription-auth-hidden",
		Root:      "/media/archive/pods",
		Title:     "Hidden Feed",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	visibleSub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/visible?token=feed-secret",
		AuthRef:   "subscription-auth-visible",
		Root:      "/media/local/pods",
		Title:     "Visible Feed",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	// A relocked viewer probing a restricted-library subscription gets the
	// exact error an unknown id gets, and no follow row is written.
	resp := postGraphJSON(t, handler, authorizeViewer,
		`mutation Follow($id: String!, $on: Boolean!) { setFollow(subscriptionID: $id, on: $on) { id } }`,
		map[string]any{"id": string(hiddenSub.ID), "on": true})
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "unknown subscription") {
		t.Fatalf("hidden follow errors = %+v, want unknown subscription", resp.Errors)
	}

	// The successful echo is the viewer-safe FollowInfo shape: no secret
	// or server-path bytes anywhere in the raw response.
	body := []byte("{\"query\":\"mutation Follow($id: String!, $on: Boolean!) { setFollow(subscriptionID: $id, on: $on) { id title followed } }\",\"variables\":{\"id\":\"" + string(visibleSub.ID) + "\",\"on\":true}}")
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	raw := rec.Body.String()
	if !strings.Contains(raw, "\"followed\":true") {
		t.Fatalf("follow response = %s, want followed true", raw)
	}
	for _, leak := range []string{"feedURL", "authHeader", "hasAuth", "feed-secret", "subscription-auth-visible", "/media/local/pods", "\"root\""} {
		if strings.Contains(raw, leak) {
			t.Fatalf("setFollow response leaks %q: %s", leak, raw)
		}
	}
}

func TestSetSubscriptionRetentionIsManagerGatedAndAudited(t *testing.T) {
	handler := newTestHandler(t)
	var added SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`mutation Add($libraryID: String!, $feedURL: String!, $root: String!) { addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root) { id } }`,
		map[string]any{"libraryID": "library-local", "feedURL": "https://feeds.example.com/retention", "root": "/media/retention"},
		"addSubscription", &added)

	var updated SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`mutation Retention($id: String!, $kind: String!, $n: Int) { setSubscriptionRetention(id: $id, kind: $kind, n: $n) { id retentionKind retentionN } }`,
		map[string]any{"id": added.ID, "kind": "keep-last-n", "n": 3},
		"setSubscriptionRetention", &updated)
	if updated.RetentionKind != "keep-last-n" || updated.RetentionN == nil || *updated.RetentionN != 3 {
		t.Fatalf("retention update = %+v, want keep-last-n/3", updated)
	}
	var entries []ActivityEntryInfo
	execGraph(t, handler, authorizeManager,
		`query { activityLog { action targetType targetID } }`,
		nil, "activityLog", &entries)
	if len(entries) == 0 || entries[0].Action != "subscription.retention" || entries[0].TargetID != added.ID {
		t.Fatalf("activity entries = %+v, want subscription.retention for %s", entries, added.ID)
	}

	resp := postGraphJSON(t, handler, authorizeViewer,
		`mutation Retention($id: String!, $kind: String!) { setSubscriptionRetention(id: $id, kind: $kind) { id } }`,
		map[string]any{"id": added.ID, "kind": "keep-all"})
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer retention errors = %+v", resp.Errors)
	}
}

func TestAssociateSubscriptionEntryIsManagerGatedRevealFilteredAndAudited(t *testing.T) {
	handler := newTestHandler(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 14, 30, 0, 0, time.UTC)
	var added SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`mutation Add($libraryID: String!, $feedURL: String!, $root: String!) { addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root) { id } }`,
		map[string]any{"libraryID": "library-local", "feedURL": "https://feeds.example.com/archive", "root": "/media/local/archive"},
		"addSubscription", &added)
	media, _, err := handler.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/media/local/archive/episode.mp3", Title: "Archived Episode",
		Container: "mp3", Codec: "mp3", Class: store.MediaClassAudio, ContentFingerprint: "fp-graph-assoc-0001", DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.store.RecordItemStateImport(ctx, store.ItemStateImport{
		ProfileID: "profile-default", SubscriptionID: store.SubscriptionID(added.ID), Source: "import:podcasts",
		ItemGUID: "episode-guid", Played: true,
	}, now); err != nil {
		t.Fatal(err)
	}

	var associated SubscriptionEntryAssociationInfo
	execGraph(t, handler, authorizeManager,
		`mutation Assoc($subscriptionID: String!, $guid: String!, $mediaID: String!) {
			associateSubscriptionEntry(subscriptionID: $subscriptionID, guid: $guid, mediaID: $mediaID) {
				subscriptionID guid mediaID path replayedItemStateSweep
			}
		}`,
		map[string]any{"subscriptionID": added.ID, "guid": "episode-guid", "mediaID": string(media.ID)},
		"associateSubscriptionEntry", &associated)
	if associated.SubscriptionID != added.ID || associated.GUID != "episode-guid" || associated.MediaID != string(media.ID) ||
		associated.Path != "/media/local/archive/episode.mp3" || associated.ReplayedItemStateSweep != 1 {
		t.Fatalf("association = %+v", associated)
	}
	progress, ok, err := handler.store.PlaybackProgressFor(ctx, "profile-default", media.ID)
	if err != nil || !ok || !progress.Watched || progress.Provenance != "import:podcasts" {
		t.Fatalf("associated progress = %+v ok=%v err=%v", progress, ok, err)
	}
	var entries []ActivityEntryInfo
	execGraph(t, handler, authorizeManager,
		`query { activityLog { action targetType targetID } }`,
		nil, "activityLog", &entries)
	if len(entries) == 0 || entries[0].Action != "subscription.entry_association" || entries[0].TargetID != added.ID {
		t.Fatalf("activity entries = %+v, want subscription.entry_association for %s", entries, added.ID)
	}

	resp := postGraphJSON(t, handler, authorizeViewer,
		`mutation Assoc($subscriptionID: String!, $guid: String!, $mediaID: String!) {
			associateSubscriptionEntry(subscriptionID: $subscriptionID, guid: $guid, mediaID: $mediaID) { subscriptionID }
		}`,
		map[string]any{"subscriptionID": added.ID, "guid": "episode-guid", "mediaID": string(media.ID)})
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer association errors = %+v", resp.Errors)
	}

	hiddenMedia, _, err := handler.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-archive", Path: "/media/archive/hidden.mp3", Title: "Hidden",
		Container: "mp3", Codec: "mp3", Class: store.MediaClassAudio, ContentFingerprint: "fp-graph-assoc-0002", DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp = postGraphJSON(t, handler, authorizeManager,
		`mutation Assoc($subscriptionID: String!, $guid: String!, $mediaID: String!) {
			associateSubscriptionEntry(subscriptionID: $subscriptionID, guid: $guid, mediaID: $mediaID) { subscriptionID }
		}`,
		map[string]any{"subscriptionID": added.ID, "guid": "hidden-guid", "mediaID": string(hiddenMedia.ID)})
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "unknown media" {
		t.Fatalf("hidden media association errors = %+v", resp.Errors)
	}
}

func TestAddSubscriptionRejectsUnknownLibrary(t *testing.T) {
	handler := newTestHandler(t)
	payload, err := json.Marshal(map[string]any{
		"query":     `mutation Add($libraryID: String!, $feedURL: String!, $root: String!) { addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root) { id } }`,
		"variables": map[string]any{"libraryID": "library-nope", "feedURL": "https://feeds.example.com/a?token=hush", "root": "/media"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(payload))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "unknown library") {
		t.Fatalf("response = %s, want unknown library", body)
	}
	if strings.Contains(body, "hush") {
		t.Fatalf("error echoes the feed URL: %s", body)
	}
}

func TestAddSubscriptionDefaultsRootFromFirstLibraryRoot(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{"/media/Library Root"}); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	var added SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`mutation Add($libraryID: String!, $feedURL: String!, $title: String!) { addSubscription(libraryID: $libraryID, feedURL: $feedURL, title: $title) { root } }`,
		map[string]any{"libraryID": "library-local", "feedURL": "https://feeds.example.com/show.xml", "title": "Daily News!"},
		"addSubscription", &added)
	if added.Root != "/media/Library Root" {
		t.Fatalf("default root = %q, want first library root", added.Root)
	}
}

func TestAddSubscriptionRequiresRootWhenLibraryHasNoPersistedRoots(t *testing.T) {
	handler := newTestHandler(t)
	resp := postGraphJSON(t, handler, authorizeManager,
		`mutation Add($libraryID: String!, $feedURL: String!) { addSubscription(libraryID: $libraryID, feedURL: $feedURL) { id } }`,
		map[string]any{"libraryID": "library-local", "feedURL": "https://feeds.example.com/show.xml"})
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "subscription root folder is required because the library has no persisted roots" {
		t.Fatalf("errors = %+v, want missing root validation", resp.Errors)
	}
}

func TestSearchFeedsAllowsAdultViewerAndDedupesProviders(t *testing.T) {
	handler := newTestHandler(t)
	handler.SetFeedSearcher(feedSearchFunc(func(ctx context.Context, query string) ([]FeedSearchResult, error) {
		if query != "daily" {
			t.Fatalf("query = %q, want daily", query)
		}
		return []FeedSearchResult{
			{Title: "Daily", FeedURL: "https://feeds.example.com/rss", ArtworkURL: "https://img.example.com/a.jpg", Description: "One"},
			{Title: "Daily Duplicate", FeedURL: "https://feeds.example.com/rss", ArtworkURL: "https://img.example.com/b.jpg", Description: "Two"},
		}, nil
	}))
	var results []FeedSearchResult
	execGraph(t, handler, authorizeViewer,
		`query SearchFeeds($query: String!) { searchFeeds(query: $query) { title feedURL artworkURL description } }`,
		map[string]any{"query": "daily"}, "searchFeeds", &results)
	if len(results) != 1 || results[0].Title != "Daily" {
		t.Fatalf("results = %+v, want deduped first result", results)
	}
}

func TestSourceItemsEntityMediaRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, store.Entity{
		ID:        "entity-maker",
		LibraryID: "library-local",
		Kind:      "maker",
		Title:     "Sample Maker",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-maker",
		Name:         "made",
		ToMediaID:    "media-alpha",
	}); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"query":"query SourceItems($kind: String!, $entityID: String!) { sourceItems(kind: $kind, entityID: $entityID) { id kind title } }","variables":{"kind":"entity-media","entityID":"entity-maker"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data   map[string][]SourceItem    `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	items := resp.Data["sourceItems"]
	if len(items) != 1 || items[0].ID != "media-alpha" || items[0].Kind != "media" {
		t.Fatalf("sourceItems = %#v, want media-alpha", items)
	}
}

func TestSourceItemsEntityMediaRequiresEntityID(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { sourceItems { id } }","variables":{"kind":"entity-media"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "entityID is required" {
		t.Fatalf("errors = %#v, want entityID is required", resp.Errors)
	}
}

// fakeArtworkLocator satisfies ArtworkLocator without touching disk, returning
// a fixed version token ("av1") for present slots so wire URLs stay
// deterministic (the real mtime would make them flaky).
type fakeArtworkLocator struct {
	media    map[store.MediaID]map[string]bool
	entities map[store.EntityID]map[string]bool
}

// fakeArtworkVersion is the deterministic ?v= token the fake locator emits.
const fakeArtworkVersion = "av1"

func (f fakeArtworkLocator) ArtworkVersion(id store.MediaID, slot string) (string, bool) {
	if f.media[id][slot] {
		return fakeArtworkVersion, true
	}
	return "", false
}

func (f fakeArtworkLocator) EntityArtworkVersion(id store.EntityID, slot string) (string, bool) {
	if f.entities[id][slot] {
		return fakeArtworkVersion, true
	}
	return "", false
}

type fakeArtworkCandidateFetcher struct {
	seen []string
	data []byte
}

func (f *fakeArtworkCandidateFetcher) FetchResource(_ context.Context, rawURL string) ([]byte, error) {
	f.seen = append(f.seen, rawURL)
	if f.data == nil {
		return []byte("candidate image bytes"), nil
	}
	return f.data, nil
}

type fakeArtworkImporter struct {
	fakeArtworkLocator
	imports []struct {
		mediaID store.MediaID
		slot    string
		data    string
	}
}

func (f *fakeArtworkImporter) ImportArtwork(_ context.Context, mediaID store.MediaID, slot string, data []byte) error {
	f.imports = append(f.imports, struct {
		mediaID store.MediaID
		slot    string
		data    string
	}{mediaID: mediaID, slot: slot, data: string(data)})
	return nil
}

func TestArtworkCandidatesQueryIsManagerGatedRevealFilteredAndTokenized(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", artwork.SlotPoster, "https://img.example.test/a.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertArtworkCandidate(ctx, "media-archive", artwork.SlotPoster, "https://img.example.test/hidden.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	handler.SetStreamTokens(streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() }))
	body := `{"query":"query Candidates($mediaID: String!, $slot: String!) { artworkCandidates(mediaID: $mediaID, slot: $slot) { id slot applied pluginID scraperID previewURL } }","variables":{"mediaID":"media-alpha","slot":"poster"}}`

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeViewer(viewerReq)
	viewerRec := httptest.NewRecorder()
	handler.ServeHTTP(viewerRec, viewerReq)
	if !bytes.Contains(viewerRec.Body.Bytes(), []byte("manager role required")) {
		t.Fatalf("viewer response = %s, want manager role required", viewerRec.Body.String())
	}

	managerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(managerReq)
	managerRec := httptest.NewRecorder()
	handler.ServeHTTP(managerRec, managerReq)
	var resp struct {
		Data struct {
			ArtworkCandidates []ArtworkCandidateInfo `json:"artworkCandidates"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(managerRec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if len(resp.Data.ArtworkCandidates) != 1 {
		t.Fatalf("candidates = %#v, want one", resp.Data.ArtworkCandidates)
	}
	candidate := resp.Data.ArtworkCandidates[0]
	if candidate.ID == "" || candidate.Slot != artwork.SlotPoster || candidate.PluginID != "demo" || candidate.ScraperID != "lookup" {
		t.Fatalf("candidate = %#v", candidate)
	}
	if !strings.HasPrefix(candidate.PreviewURL, "/api/media/artwork-candidate/") {
		t.Fatalf("previewURL = %q, want candidate preview route", candidate.PreviewURL)
	}
	if strings.Contains(managerRec.Body.String(), "https://img.example.test") {
		t.Fatalf("response leaked raw source URL: %s", managerRec.Body.String())
	}

	hiddenBody := `{"query":"query { artworkCandidates(mediaID: \"media-archive\", slot: \"poster\") { id previewURL } }","variables":{"mediaID":"media-archive","slot":"poster"}}`
	hiddenReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(hiddenBody)))
	authorizeManager(hiddenReq)
	hiddenRec := httptest.NewRecorder()
	handler.ServeHTTP(hiddenRec, hiddenReq)
	var hidden struct {
		Data struct {
			ArtworkCandidates []ArtworkCandidateInfo `json:"artworkCandidates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(hiddenRec.Body.Bytes(), &hidden); err != nil {
		t.Fatal(err)
	}
	if len(hidden.Data.ArtworkCandidates) != 0 {
		t.Fatalf("hidden candidates = %#v, want empty", hidden.Data.ArtworkCandidates)
	}
}

func TestPinArtworkCandidateFetchesImportsMarksAndAudits(t *testing.T) {
	// feature:artwork-picker
	st := storetest.Open(t)
	ctx := context.Background()
	aURL := "https://img.example.test/a.jpg"
	bURL := "https://img.example.test/b.jpg"
	for _, rawURL := range []string{aURL, bURL} {
		if err := st.UpsertArtworkCandidate(ctx, "media-alpha", artwork.SlotPoster, rawURL, "demo", "lookup"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MarkArtworkCandidateApplied(ctx, store.ArtworkCandidateID("media-alpha", artwork.SlotPoster, aURL)); err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeArtworkCandidateFetcher{data: []byte("new poster bytes")}
	importer := &fakeArtworkImporter{}
	handler := newTestHandlerWithStore(st)
	handler.SetArtworkCandidateServices(fetcher, importer)
	candidateID := store.ArtworkCandidateID("media-alpha", artwork.SlotPoster, bURL)
	body := `{"query":"mutation Pin($mediaID: String!, $slot: String!, $candidateID: String!) { pinArtworkCandidate(mediaID: $mediaID, slot: $slot, candidateID: $candidateID) { id applied } }","variables":{"mediaID":"media-alpha","slot":"poster","candidateID":"` + candidateID + `"}}`

	rec := postGraph(t, handler, body, authorizeManager)
	if !bytes.Contains(rec.Body.Bytes(), []byte(candidateID)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"applied":true`)) {
		t.Fatalf("pin response = %s", rec.Body.String())
	}
	if len(fetcher.seen) != 1 || fetcher.seen[0] != bURL {
		t.Fatalf("fetcher saw %#v, want %q", fetcher.seen, bURL)
	}
	if len(importer.imports) != 1 || importer.imports[0].mediaID != "media-alpha" ||
		importer.imports[0].slot != artwork.SlotPoster || importer.imports[0].data != "new poster bytes" {
		t.Fatalf("imports = %#v", importer.imports)
	}
	candidates, err := st.ArtworkCandidates(ctx, "media-alpha", artwork.SlotPoster, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	applied := map[string]bool{}
	for _, candidate := range candidates {
		applied[candidate.ID] = candidate.Applied
	}
	if !applied[candidateID] || applied[store.ArtworkCandidateID("media-alpha", artwork.SlotPoster, aURL)] {
		t.Fatalf("applied flags = %#v", applied)
	}
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != store.ActivityArtworkPin ||
		entries[0].TargetID != "media-alpha" || entries[0].LibraryID != "library-local" {
		t.Fatalf("activity = %#v", entries)
	}
}

func TestPinArtworkCandidateHidesRestrictedCandidateLikeUnknown(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	rawURL := "https://img.example.test/hidden.jpg"
	if err := st.UpsertArtworkCandidate(ctx, "media-archive", artwork.SlotPoster, rawURL, "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	handler.SetArtworkCandidateServices(&fakeArtworkCandidateFetcher{}, &fakeArtworkImporter{})
	hiddenID := store.ArtworkCandidateID("media-archive", artwork.SlotPoster, rawURL)
	mutate := func(candidateID string) string {
		body := `{"query":"mutation { pinArtworkCandidate(mediaID: \"media-archive\", slot: \"poster\", candidateID: \"` + candidateID + `\") { id } }","variables":{"mediaID":"media-archive","slot":"poster","candidateID":"` + candidateID + `"}}`
		rec := postGraph(t, handler, body, authorizeManager)
		return rec.Body.String()
	}
	hidden := mutate(hiddenID)
	unknown := mutate(strings.Repeat("0", 64))
	if hidden != unknown || !strings.Contains(hidden, "unknown artwork candidate") {
		t.Fatalf("hidden response %s differs from unknown %s", hidden, unknown)
	}
}

func TestSourceItemsReportArtworkPresenceHonestly(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// media-alpha has a stored poster; media-archive does not.
	handler.SetArtwork(fakeArtworkLocator{media: map[store.MediaID]map[string]bool{
		"media-alpha": {artwork.SlotPoster: true, artwork.SlotBackdrop: true},
	}})

	body := []byte(`{"query":"query { sourceItems { id artwork { poster } artworkSlots } }","variables":{"kind":"recently-added"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeRevealed(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string][]SourceItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	items := resp.Data["sourceItems"]
	if len(items) != 2 {
		t.Fatalf("len(sourceItems) = %d, want 2", len(items))
	}
	byID := map[string]SourceItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	alpha := byID["media-alpha"]
	if alpha.Artwork == nil || alpha.Artwork.Poster != "/api/media/artwork/media-alpha/poster?v=av1" {
		t.Fatalf("media-alpha artwork = %#v, want canonical versioned poster path", alpha.Artwork)
	}
	wantSlots := map[string]string{
		artwork.SlotPoster:   "/api/media/artwork/media-alpha/poster?v=av1",
		artwork.SlotBackdrop: "/api/media/artwork/media-alpha/backdrop?v=av1",
	}
	if !reflect.DeepEqual(alpha.ArtworkSlots, wantSlots) {
		t.Fatalf("media-alpha artworkSlots = %#v, want %#v", alpha.ArtworkSlots, wantSlots)
	}
	if archive := byID["media-archive"]; archive.Artwork != nil {
		t.Fatalf("media-archive artwork = %#v, want absent (no poster stored)", archive.Artwork)
	} else if archive.ArtworkSlots != nil {
		t.Fatalf("media-archive artworkSlots = %#v, want absent", archive.ArtworkSlots)
	}
}

func TestEntityItemsCarryEntityArtwork(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	for _, entity := range []store.Entity{
		{ID: "entity-maker", LibraryID: "library-local", Kind: "maker", Title: "Sample Maker"},
		{ID: "entity-bare", LibraryID: "library-local", Kind: "maker", Title: "Bare Maker"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	handler.SetArtwork(fakeArtworkLocator{entities: map[store.EntityID]map[string]bool{
		"entity-maker": {artwork.SlotPoster: true, artwork.SlotThumb: true},
	}})

	body := []byte(`{"query":"query { sourceItems { id kind artwork { poster } artworkSlots } }","variables":{"kind":"entity-items","entityKind":"maker"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string][]SourceItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byID := map[string]SourceItem{}
	for _, item := range resp.Data["sourceItems"] {
		if item.Kind != "entity" {
			t.Fatalf("item %s kind = %q, want entity", item.ID, item.Kind)
		}
		byID[item.ID] = item
	}
	if len(byID) != 2 {
		t.Fatalf("len(sourceItems) = %d, want 2", len(byID))
	}
	maker := byID["entity-maker"]
	if maker.Artwork == nil || maker.Artwork.Poster != "/api/entity/artwork/entity-maker/poster?v=av1" {
		t.Fatalf("entity-maker artwork = %#v, want versioned entity poster path", maker.Artwork)
	}
	wantSlots := map[string]string{
		artwork.SlotPoster: "/api/entity/artwork/entity-maker/poster?v=av1",
		artwork.SlotThumb:  "/api/entity/artwork/entity-maker/thumb?v=av1",
	}
	if !reflect.DeepEqual(maker.ArtworkSlots, wantSlots) {
		t.Fatalf("entity-maker artworkSlots = %#v, want %#v", maker.ArtworkSlots, wantSlots)
	}
	if bare := byID["entity-bare"]; bare.Artwork != nil {
		t.Fatalf("entity-bare artwork = %#v, want absent", bare.Artwork)
	} else if bare.ArtworkSlots != nil {
		t.Fatalf("entity-bare artworkSlots = %#v, want absent", bare.ArtworkSlots)
	}
}

// The echoed fieldFilters variable (manifest SourceSpec.fieldFilters) narrows
// the catalog feeds by Media-field equality, end to end: seed field values,
// query sourceItems with the filter object, get only the matching items —
// reveal filtering still applies on top.
func TestSourceItemsQueryAppliesFieldFilters(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	if err := st.SetMediaFields(ctx, "media-alpha", []store.MediaField{
		{Name: "phase", Type: store.EntityFieldText, Text: "flight"},
	}); err != nil {
		t.Fatal(err)
	}
	// The restricted item matches the filter too — it must stay hidden
	// from the unrevealed session.
	if err := st.SetMediaFields(ctx, "media-archive", []store.MediaField{
		{Name: "phase", Type: store.EntityFieldText, Text: "flight"},
	}); err != nil {
		t.Fatal(err)
	}
	docked, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/media/docked.mp4", Title: "Docked",
		Container: "mp4", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: store.HashMediaFilePath("/media/docked.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, docked.ID, []store.MediaField{
		{Name: "phase", Type: store.EntityFieldText, Text: "dock"},
	}); err != nil {
		t.Fatal(err)
	}

	query := func(t *testing.T, authorize func(*http.Request), value string) []string {
		t.Helper()
		body := []byte(`{"query":"query SourceItems($kind: String, $fieldFilters: FieldFilters) { sourceItems(kind: $kind, fieldFilters: $fieldFilters) { id } }","variables":{"kind":"all-items","fieldFilters":{"phase":"` + value + `"}}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		var resp struct {
			Data map[string][]SourceItem `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return itemIDs(resp.Data["sourceItems"])
	}

	if got, want := query(t, authorizeViewer, "flight"), []string{"media-alpha"}; !equalStringSlices(got, want) {
		t.Fatalf("unrevealed phase=flight = %v, want %v", got, want)
	}
	if got, want := query(t, authorizeRevealed, "flight"), []string{"media-archive", "media-alpha"}; !equalStringSlices(got, want) {
		t.Fatalf("revealed phase=flight = %v, want %v", got, want)
	}
	if got, want := query(t, authorizeViewer, "dock"), []string{string(docked.ID)}; !equalStringSlices(got, want) {
		t.Fatalf("phase=dock = %v, want %v", got, want)
	}
}

// The fieldFilters variable travels through the Graph layer unchanged: JSON
// numbers, booleans, date strings, and gte/lte objects survive the request
// variables map and still narrow sourceItems.
func TestSourceItemsQueryAppliesTypedFieldFilters(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	released := time.Date(2020, 7, 4, 12, 0, 0, 0, time.UTC)
	if err := st.SetMediaFields(ctx, "media-alpha", []store.MediaField{
		{Name: "score", Type: store.EntityFieldNumber, Number: 8.5},
		{Name: "verified", Type: store.EntityFieldFlag, Flag: true},
		{Name: "released", Type: store.EntityFieldDate, Date: released},
	}); err != nil {
		t.Fatal(err)
	}
	docked, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/media/docked.mp4", Title: "Docked",
		Container: "mp4", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: store.HashMediaFilePath("/media/docked.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, docked.ID, []store.MediaField{
		{Name: "score", Type: store.EntityFieldNumber, Number: 4},
		{Name: "verified", Type: store.EntityFieldFlag, Flag: false},
		{Name: "released", Type: store.EntityFieldDate, Date: time.Date(2021, 1, 5, 0, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatal(err)
	}

	post := func(t *testing.T, fieldFilters map[string]any) []string {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"query": `query SourceItems($kind: String!, $fieldFilters: FieldFilters) { sourceItems(kind: $kind, fieldFilters: $fieldFilters) { id } }`,
			"variables": map[string]any{
				"kind":         "all-items",
				"fieldFilters": fieldFilters,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		var resp struct {
			Data map[string][]SourceItem `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return itemIDs(resp.Data["sourceItems"])
	}

	if got, want := post(t, map[string]any{"score": 8.5, "verified": true, "released": released.Format(time.RFC3339)}), []string{"media-alpha"}; !equalStringSlices(got, want) {
		t.Fatalf("typed equality = %v, want %v", got, want)
	}
	if got, want := post(t, map[string]any{"score": map[string]any{"gte": 4, "lte": 5}}), []string{string(docked.ID)}; !equalStringSlices(got, want) {
		t.Fatalf("typed range = %v, want %v", got, want)
	}
}

// The echoed entityFilter variable (manifest SourceSpec.entityFilter) narrows
// the catalog feeds to Media related to one named entity, end to end: seed an
// entity with relation rows, query sourceItems with the filter object, get
// only the related items. A filter naming no existing entity answers an empty
// list — hidden and missing entities are indistinguishable — while a partial
// filter object is a malformed echo and errors.
func TestSourceItemsQueryAppliesEntityFilter(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: "entity-acme", LibraryID: "library-local", Kind: "maker", Title: "Acme",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-acme", Name: "made", ToMediaID: "media-alpha",
	}); err != nil {
		t.Fatal(err)
	}

	query := func(t *testing.T, variables string) *httptest.ResponseRecorder {
		t.Helper()
		body := []byte(`{"query":"query SourceItems($kind: String, $entityFilter: EntityFilter) { sourceItems(kind: $kind, entityFilter: $entityFilter) { id } }","variables":` + variables + `}`)
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		return rec
	}
	items := func(t *testing.T, variables string) []string {
		t.Helper()
		var resp struct {
			Data map[string][]SourceItem `json:"data"`
		}
		if err := json.Unmarshal(query(t, variables).Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return itemIDs(resp.Data["sourceItems"])
	}

	got := items(t, `{"kind":"all-items","entityFilter":{"kind":"maker","name":"Acme"}}`)
	if want := []string{"media-alpha"}; !equalStringSlices(got, want) {
		t.Fatalf("maker=Acme = %v, want %v", got, want)
	}

	// No such entity: empty list, no error.
	if got := items(t, `{"kind":"all-items","entityFilter":{"kind":"maker","name":"Nimbus"}}`); len(got) != 0 {
		t.Fatalf("maker=Nimbus = %v, want empty", got)
	}

	// A partial filter object is malformed — silently ignoring it would
	// widen the shelf to the whole catalog.
	rec := query(t, `{"kind":"all-items","entityFilter":{"kind":"maker"}}`)
	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "entityFilter requires kind and name" {
		t.Fatalf("partial entityFilter response = %s, want kind-and-name error", rec.Body.String())
	}
}

func TestMarkerLifecycle(t *testing.T) {
	// feature:markers-skip
	handler := newTestHandler(t)
	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	type markerResp struct {
		Data   map[string]MarkerInfo `json:"data"`
		Errors []graphError          `json:"errors"`
	}

	// Viewers cannot mutate markers.
	rec := post(`{"query":"mutation($mediaID:String!) { createMarker(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha","title":"Intro","startSeconds":5}}`, authorizeViewer)
	var created markerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Errors) == 0 || created.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer createMarker = %s, want manager role required", rec.Body.String())
	}

	// Manager creates a range marker with a segment kind and tags.
	rec = post(`{"query":"mutation($mediaID:String!) { createMarker(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha","title":"Opening","startSeconds":5.5,"endSeconds":60,"segmentKind":"intro","tags":["theme"]}}`, authorizeManager)
	created = markerResp{}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Errors) != 0 {
		t.Fatalf("createMarker errors = %#v", created.Errors)
	}
	marker := created.Data["createMarker"]
	if marker.ID == "" || marker.MediaID != "media-alpha" || marker.StartSeconds != 5.5 ||
		marker.EndSeconds == nil || *marker.EndSeconds != 60 || marker.SegmentKind != "intro" {
		t.Fatalf("createMarker = %#v", marker)
	}

	// Validation runs on the wire path too.
	rec = post(`{"query":"mutation($mediaID:String!) { createMarker(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha","title":"Bad","startSeconds":10,"endSeconds":3}}`, authorizeManager)
	var invalid markerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &invalid); err != nil {
		t.Fatal(err)
	}
	if len(invalid.Errors) == 0 || invalid.Errors[0].Message != "marker end must be after start" {
		t.Fatalf("invalid createMarker = %s", rec.Body.String())
	}

	// Viewers list markers for visible media.
	rec = post(`{"query":"query($mediaID:String!) { markers(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha"}}`, authorizeViewer)
	var listed struct {
		Data struct {
			Markers []MarkerInfo `json:"markers"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Errors) != 0 || len(listed.Data.Markers) != 1 || listed.Data.Markers[0].ID != marker.ID {
		t.Fatalf("markers = %s", rec.Body.String())
	}
	if got := listed.Data.Markers[0].Tags; len(got) != 1 || got[0] != "theme" {
		t.Fatalf("markers tags = %#v", listed.Data.Markers[0])
	}

	// Update replaces the mutable fields.
	rec = post(`{"query":"mutation($markerID:String!) { updateMarker(markerID: $markerID) { id } }","variables":{"markerID":"`+marker.ID+`","title":"Cold Open","startSeconds":2,"tags":[]}}`, authorizeManager)
	var updated markerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Errors) != 0 {
		t.Fatalf("updateMarker errors = %#v", updated.Errors)
	}
	got := updated.Data["updateMarker"]
	if got.Title != "Cold Open" || got.StartSeconds != 2 || got.EndSeconds != nil || got.SegmentKind != "" {
		t.Fatalf("updateMarker = %#v", got)
	}

	// Viewer cannot delete; manager can, and the list empties.
	rec = post(`{"query":"mutation($markerID:String!) { deleteMarker(markerID: $markerID) { id } }","variables":{"markerID":"`+marker.ID+`"}}`, authorizeViewer)
	var denied markerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &denied); err != nil {
		t.Fatal(err)
	}
	if len(denied.Errors) == 0 || denied.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer deleteMarker = %s", rec.Body.String())
	}
	rec = post(`{"query":"mutation($markerID:String!) { deleteMarker(markerID: $markerID) { id } }","variables":{"markerID":"`+marker.ID+`"}}`, authorizeManager)
	var deleted markerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if len(deleted.Errors) != 0 || deleted.Data["deleteMarker"].ID != marker.ID {
		t.Fatalf("deleteMarker = %s", rec.Body.String())
	}
	if deleted.Data["deleteMarker"].TrashedAt == "" {
		t.Fatalf("deleteMarker = %#v, want trashedAt", deleted.Data["deleteMarker"])
	}
	rec = post(`{"query":"query($mediaID:String!) { markers(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha"}}`, authorizeViewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data.Markers) != 0 {
		t.Fatalf("markers after delete = %s", rec.Body.String())
	}

	// Trashed marker listing is manager-only and carries the deleted row.
	rec = post(`{"query":"query($mediaID:String!) { trashedMarkers(mediaID: $mediaID) { id trashedAt tags } }","variables":{"mediaID":"media-alpha"}}`, authorizeViewer)
	var deniedTrash struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &deniedTrash); err != nil {
		t.Fatal(err)
	}
	if len(deniedTrash.Errors) == 0 || deniedTrash.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer trashedMarkers = %s", rec.Body.String())
	}
	rec = post(`{"query":"query($mediaID:String!) { trashedMarkers(mediaID: $mediaID) { id trashedAt tags } }","variables":{"mediaID":"media-alpha"}}`, authorizeManager)
	var trashList struct {
		Data struct {
			TrashedMarkers []MarkerInfo `json:"trashedMarkers"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &trashList); err != nil {
		t.Fatal(err)
	}
	if len(trashList.Errors) != 0 || len(trashList.Data.TrashedMarkers) != 1 ||
		trashList.Data.TrashedMarkers[0].ID != marker.ID || trashList.Data.TrashedMarkers[0].TrashedAt == "" {
		t.Fatalf("trashedMarkers = %s", rec.Body.String())
	}

	// Restore returns the marker to the live list.
	rec = post(`{"query":"mutation($markerID:String!) { restoreMarker(markerID: $markerID) { id trashedAt } }","variables":{"markerID":"`+marker.ID+`"}}`, authorizeViewer)
	var restoreDenied markerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &restoreDenied); err != nil {
		t.Fatal(err)
	}
	if len(restoreDenied.Errors) == 0 || restoreDenied.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer restoreMarker = %s", rec.Body.String())
	}
	rec = post(`{"query":"mutation($markerID:String!) { restoreMarker(markerID: $markerID) { id trashedAt } }","variables":{"markerID":"`+marker.ID+`"}}`, authorizeManager)
	var restored markerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Errors) != 0 || restored.Data["restoreMarker"].ID != marker.ID || restored.Data["restoreMarker"].TrashedAt != "" {
		t.Fatalf("restoreMarker = %s", rec.Body.String())
	}
	rec = post(`{"query":"query($mediaID:String!) { markers(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha"}}`, authorizeViewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data.Markers) != 1 || listed.Data.Markers[0].ID != marker.ID {
		t.Fatalf("markers after restore = %s", rec.Body.String())
	}
}

func TestDeleteMarkerRearmsAssetSweep(t *testing.T) {
	handler := newTestHandler(t)
	sweeps := 0
	handler.SetAssetSweep(func(context.Context) { sweeps++ })
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorizeManager(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"query":"mutation($mediaID:String!) { createMarker(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha","title":"Opening","startSeconds":5}}`)
	var created struct {
		Data   map[string]MarkerInfo `json:"data"`
		Errors []graphError          `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Errors) != 0 {
		t.Fatalf("createMarker errors = %#v", created.Errors)
	}
	sweeps = 0
	marker := created.Data["createMarker"]
	rec = post(`{"query":"mutation($markerID:String!) { deleteMarker(markerID: $markerID) { id } }","variables":{"markerID":"` + marker.ID + `"}}`)
	var deleted struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if len(deleted.Errors) != 0 {
		t.Fatalf("deleteMarker errors = %#v", deleted.Errors)
	}
	if sweeps != 1 {
		t.Fatalf("asset sweep callbacks after delete = %d, want 1", sweeps)
	}
	rec = post(`{"query":"mutation($markerID:String!) { restoreMarker(markerID: $markerID) { id } }","variables":{"markerID":"` + marker.ID + `"}}`)
	var restored struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Errors) != 0 {
		t.Fatalf("restoreMarker errors = %#v", restored.Errors)
	}
	if sweeps != 2 {
		t.Fatalf("asset sweep callbacks after restore = %d, want 2", sweeps)
	}
}

func TestMarkersQueryHidesRestrictedMediaUntilRevealed(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	if _, err := st.CreateMarker(context.Background(), store.Marker{
		MediaID: "media-archive", SegmentKind: "credits",
	}, store.LibraryFilter{Revealed: true, AllLibraries: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	post := func(authorize func(*http.Request)) *httptest.ResponseRecorder {
		body := `{"query":"query($mediaID:String!) { markers(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-archive"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	rec := post(authorizeViewer)
	var hidden struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hidden); err != nil {
		t.Fatal(err)
	}
	if len(hidden.Errors) == 0 || hidden.Errors[0].Message != "unknown media" {
		t.Fatalf("unrevealed markers = %s, want unknown media", rec.Body.String())
	}

	rec = post(authorizeRevealed)
	var visible struct {
		Data struct {
			Markers []MarkerInfo `json:"markers"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &visible); err != nil {
		t.Fatal(err)
	}
	if len(visible.Errors) != 0 || len(visible.Data.Markers) != 1 || visible.Data.Markers[0].SegmentKind != "credits" {
		t.Fatalf("revealed markers = %s", rec.Body.String())
	}
}

// certTestHandler wires a registry whose bundle declares a certification
// scale, with the auth service carrying the scales (mirroring app wiring).
func certTestHandler(t *testing.T, st *store.Store) *Handler {
	t.Helper()
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:                 "starlog",
		Version:            "0.1.0",
		DisplayName:        plugin.L("Starlog"),
		MediaLabel:         plugin.L("Entry"),
		MediaFields:        []plugin.FieldSpec{{Name: "certification", Type: "text"}},
		CertificationScale: []string{"low", "mid", "high"},
		Layouts:            []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(st)
	authService.SetCertificationScales(map[string][]string{"starlog": {"low", "mid", "high"}})
	return NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		authService,
		st,
		registry,
	)
}

func TestSetCertificationCeilingMutationEndToEnd(t *testing.T) {
	// feature:enforced-filters
	st := storetest.Open(t)
	handler := certTestHandler(t, st)
	ctx := context.Background()

	// A starlog-bound Library with one item per rating + one unrated.
	library, err := st.CreateLibrary(ctx, "Entries", false, "starlog", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, cert := range []string{"low", "high", ""} {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:          library.ID,
			Path:               "/entries/" + cert + "item.mp4",
			Title:              "Entry " + cert,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: "fp-" + cert,
		})
		if err != nil {
			t.Fatal(err)
		}
		if cert != "" {
			if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{
				{Name: "certification", Type: store.EntityFieldText, Text: cert},
			}); err != nil {
				t.Fatal(err)
			}
		}
		titles[cert] = "Entry " + cert
	}

	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	setCeiling := `{"query":"mutation($profileID:String!) { setCertificationCeiling(profileID: $profileID) { id certificationCeilings { pluginID ceiling allowUnrated } } }","variables":{"profileID":"profile-default","pluginID":"starlog","ceiling":"mid"}}`

	// A session without an open Admin Session is refused (setCertificationCeiling
	// is a Console operation, ADR-0007 §4 Profiles & access policies).
	if rec := post(setCeiling, authorizeViewer); !bytes.Contains(rec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin setCertificationCeiling = %s", rec.Body.String())
	}
	// Open the Admin Session for the operator path below.
	openManagerAdminSession(t, st)
	// An off-scale ceiling is refused.
	offScale := `{"query":"mutation { setCertificationCeiling { id } }","variables":{"profileID":"profile-default","pluginID":"starlog","ceiling":"colossal"}}`
	if rec := post(offScale, authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("not on the plugin's certification scale")) {
		t.Fatalf("off-scale ceiling = %s", rec.Body.String())
	}

	rec := post(setCeiling, authorizeManager)
	var resp struct {
		Data struct {
			SetCertificationCeiling ProfileInfo `json:"setCertificationCeiling"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	ceilings := resp.Data.SetCertificationCeiling.CertificationCeilings
	if len(ceilings) != 1 || ceilings[0].PluginID != "starlog" || ceilings[0].Ceiling != "mid" || ceilings[0].AllowUnrated {
		t.Fatalf("ceilings = %#v", ceilings)
	}

	// The viewer session (profile-default) now reads a filtered catalog:
	// above-ceiling and unrated entries are gone from sourceItems.
	items := `{"query":"query($kind:String!) { sourceItems(source: { kind: $kind }) { id title } }","variables":{"kind":"recently-added"}}`
	rec = post(items, authorizeViewer)
	body := rec.Body.String()
	if !strings.Contains(body, titles["low"]) {
		t.Fatalf("below-ceiling entry missing: %s", body)
	}
	if strings.Contains(body, titles["high"]) || strings.Contains(body, "Entry \\u003c") || strings.Contains(body, `"Entry "`) {
		t.Fatalf("ceiling leaked entries: %s", body)
	}
	// Search must not leak either.
	search := `{"query":"query($q:String!) { search(query: $q) { id title } }","variables":{"q":"Entry"}}`
	rec = post(search, authorizeViewer)
	if body := rec.Body.String(); strings.Contains(body, titles["high"]) {
		t.Fatalf("search leaked above-ceiling entry: %s", body)
	}

	// Clearing (empty ceiling) restores the full catalog.
	clear := `{"query":"mutation { setCertificationCeiling { id certificationCeilings { pluginID } } }","variables":{"profileID":"profile-default","pluginID":"starlog"}}`
	if rec := post(clear, authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte(`"certificationCeilings":[]`)) {
		t.Fatalf("clear = %s", rec.Body.String())
	}
	rec = post(items, authorizeViewer)
	if body := rec.Body.String(); !strings.Contains(body, titles["high"]) {
		t.Fatalf("cleared ceiling still filtering: %s", body)
	}
}

func TestSetProfileLibrariesMutationEndToEnd(t *testing.T) {
	// feature:profile-library-access
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	mutation := `mutation SetProfileLibraries($profileID:String!,$libraryIDs:[String!]!){ setProfileLibraries(profileID:$profileID, libraryIDs:$libraryIDs){ id libraryIDs } }`
	vars := map[string]any{"profileID": "profile-managed", "libraryIDs": []string{"library-local"}}

	resp := postGraphJSON(t, handler, authorizeManager, mutation, vars)
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "admin session required") {
		t.Fatalf("non-admin setProfileLibraries errors = %+v", resp.Errors)
	}

	openManagerAdminSession(t, st)
	resp = postGraphJSON(t, handler, authorizeManager, mutation, vars)
	if len(resp.Errors) != 0 {
		t.Fatalf("setProfileLibraries errors = %+v", resp.Errors)
	}
	var got ProfileInfo
	if err := json.Unmarshal(resp.Data["setProfileLibraries"], &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "profile-managed" || !equalStringSlices(got.LibraryIDs, []string{"library-local"}) {
		t.Fatalf("setProfileLibraries = %#v", got)
	}

	resp = postGraphJSON(t, handler, authorizeManager, mutation, map[string]any{
		"profileID":  "profile-default",
		"libraryIDs": []string{"library-archive"},
	})
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "reveal required") {
		t.Fatalf("restricted grant without reveal errors = %+v", resp.Errors)
	}
}

// TestNextUpAdvancesAfterFinishingOrderedChild drives the next-up Source end
// to end: an ordered container (sort_order relations), a reportPlayback
// mutation that finishes the first child, and a next-up query that yields the
// second — while continue-watching stays empty (ADR-0028 §5's separate
// shelves: a finished child belongs to neither, the frontier to next-up only).
func TestNextUpAdvancesAfterFinishingOrderedChild(t *testing.T) {
	// feature:queue-next-up
	st := storetest.Open(t)
	ctx := context.Background()
	episode, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/second.mp4",
		Title:              "Second",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:second",
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		EntityKinds: []plugin.EntityKind{{
			Name:      "container",
			Archetype: "container",
			Relations: []plugin.RelationSpec{{
				Name:   "contains",
				ToKind: plugin.MediaKindName,
			}},
		}},
		Scrapers: []plugin.ScraperSpec{{
			ID:             "lookup",
			DisplayName:    plugin.L("Demo Lookup"),
			Kind:           "http-json",
			Endpoint:       "https://demo.example.test",
			LookupTemplate: "/title/{id}",
			FieldMap:       map[string]string{"title": "name"},
			EntityMaps: []plugin.EntityMap{{
				Kind:     "container",
				Relation: "contains",
				Path:     "sequence",
				NamePath: "name",
			}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	identifyService := identify.NewService(orderedHydrationEngine{}, registry, st)
	identifyService.SetEntities(st)
	handler.SetIdentify(identifyService)

	report := []byte(`{"query":"mutation ReportPlayback($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds) { watched } }","variables":{"mediaID":"media-alpha","positionSeconds":120,"durationSeconds":120}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(report))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reportPlayback status = %d: %s", rec.Code, rec.Body.String())
	}

	sourceItems := func(kind string) []SourceItem {
		t.Helper()
		query := []byte(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id title } }","variables":{"kind":"` + kind + `"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(query))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Data struct {
				SourceItems []SourceItem `json:"sourceItems"`
			} `json:"data"`
			Errors []struct{ Message string } `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("sourceItems(%s) errors = %#v", kind, resp.Errors)
		}
		return resp.Data.SourceItems
	}

	if next := sourceItems("next-up"); len(next) != 0 {
		t.Fatalf("next-up before ordered hydration = %#v, want empty", next)
	}
	for _, apply := range []struct {
		mediaID     store.MediaID
		candidateID string
	}{
		{mediaID: "media-alpha", candidateID: "first"},
		{mediaID: episode.ID, candidateID: "second"},
	} {
		if err := identifyService.Apply(ctx, apply.mediaID, "demo", "lookup", apply.candidateID); err != nil {
			t.Fatal(err)
		}
	}
	next := sourceItems("next-up")
	if len(next) != 1 || next[0].ID != string(episode.ID) {
		t.Fatalf("next-up = %#v, want exactly %q", next, episode.ID)
	}
	if cw := sourceItems("continue-watching"); len(cw) != 0 {
		t.Fatalf("continue-watching = %#v, want empty alongside next-up", cw)
	}
}

func TestIdentifySearchMediaIDGating(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	run := func(t *testing.T, variables string) []struct{ Message string } {
		t.Helper()
		body := `{"query":"query IdentifySearch($pluginID: String!, $scraperID: String!, $query: String, $mediaID: String) { identifySearch(pluginID: $pluginID, scraperID: $scraperID, query: $query, mediaID: $mediaID) { id } }","variables":` + variables + `}`
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorizeManager(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Errors []struct{ Message string } `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Errors
	}

	// A visible Media item sources fingerprint variables; the query may be
	// empty for a fingerprint-driven search.
	if errs := run(t, `{"pluginID":"demo","scraperID":"lookup","mediaID":"media-alpha"}`); len(errs) != 0 {
		t.Fatalf("visible mediaID errors = %#v, want none", errs)
	}
	// A Media item outside the session's reveal scope must not leak its
	// fingerprints into outbound queries.
	if errs := run(t, `{"pluginID":"demo","scraperID":"lookup","mediaID":"media-archive"}`); len(errs) == 0 || errs[0].Message != "unknown media" {
		t.Fatalf("restricted mediaID errors = %#v, want unknown media", errs)
	}
	// Neither a query nor a mediaID is a client error.
	if errs := run(t, `{"pluginID":"demo","scraperID":"lookup"}`); len(errs) == 0 || errs[0].Message != "query or mediaID is required" {
		t.Fatalf("missing query+mediaID errors = %#v", errs)
	}
}

type graphTestResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []graphError               `json:"errors"`
}

func postGraphJSON(t *testing.T, handler *Handler, authorize func(*http.Request), query string, variables map[string]any) graphTestResponse {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(payload))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp graphTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func execGraph(t *testing.T, handler *Handler, authorize func(*http.Request), query string, variables map[string]any, field string, out any) {
	t.Helper()
	resp := postGraphJSON(t, handler, authorize, query, variables)
	if len(resp.Errors) != 0 {
		t.Fatalf("%s errors = %+v", field, resp.Errors)
	}
	if err := json.Unmarshal(resp.Data[field], out); err != nil {
		t.Fatal(err)
	}
}

func TestAPITokenLifecycleAndBearerAuth(t *testing.T) {
	// feature:api-keys
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	create := []byte(`{"query":"mutation CreateApiToken($name: String!) { createApiToken(name: $name) { token info { id name revoked } } }","variables":{"name":"ci"}}`)

	// A session without an open Admin Session is refused — admin/ops tokens are a
	// Console operation.
	viewerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(create))
	authorizeManager(viewerReq)
	viewerRec := httptest.NewRecorder()
	handler.ServeHTTP(viewerRec, viewerReq)
	if !bytes.Contains(viewerRec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin createApiToken = %s", viewerRec.Body.String())
	}

	openManagerAdminSession(t, st)
	managerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(create))
	authorizeManager(managerReq)
	managerRec := httptest.NewRecorder()
	handler.ServeHTTP(managerRec, managerReq)
	var created struct {
		Data struct {
			CreateApiToken CreatedAPITokenInfo `json:"createApiToken"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(managerRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Errors) != 0 {
		t.Fatalf("createApiToken errors = %#v", created.Errors)
	}
	token := created.Data.CreateApiToken.Token
	id := created.Data.CreateApiToken.Info.ID
	if token == "" || id == "" {
		t.Fatalf("createApiToken = %#v", created.Data.CreateApiToken)
	}

	// A standing Bearer token can never hold an Admin Session (admin is an
	// interactive per-session re-auth, ADR-0007 §2), so it cannot reach the
	// Console apiTokens surface — a token managing other tokens would be the
	// privilege escalation the Admin Session tier exists to prevent.
	list := []byte(`{"query":"query { apiTokens { id name revoked } }"}`)
	bearerReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(list))
	bearerReq.Header.Set("Authorization", "Bearer "+token)
	bearerRec := httptest.NewRecorder()
	handler.ServeHTTP(bearerRec, bearerReq)
	if !bytes.Contains(bearerRec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("bearer apiTokens reached the Console surface = %s", bearerRec.Body.String())
	}

	// Under the operator's Admin Session the list reads back and never carries
	// secret material.
	adminListReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(list))
	authorizeManager(adminListReq)
	adminListRec := httptest.NewRecorder()
	handler.ServeHTTP(adminListRec, adminListReq)
	if !bytes.Contains(adminListRec.Body.Bytes(), []byte(`"name":"ci"`)) {
		t.Fatalf("admin apiTokens = %s", adminListRec.Body.String())
	}
	if bytes.Contains(adminListRec.Body.Bytes(), []byte("bft_")) {
		t.Fatalf("apiTokens leaks token material: %s", adminListRec.Body.String())
	}

	// A token scope is never revealed: the session query reports the
	// hidden world regardless of any standing state.
	session := []byte(`{"query":"query { session { revealed profileKind } }"}`)
	sessionReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(session))
	sessionReq.Header.Set("Authorization", "Bearer "+token)
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, sessionReq)
	if bytes.Contains(sessionRec.Body.Bytes(), []byte(`"revealed":true`)) {
		t.Fatalf("token scope claims revealed: %s", sessionRec.Body.String())
	}

	// Revoke kills the credential immediately.
	revoke := []byte(`{"query":"mutation RevokeApiToken($id: String!) { revokeApiToken(id: $id) { id revoked } }","variables":{"id":"` + id + `"}}`)
	revokeReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(revoke))
	authorizeManager(revokeReq)
	revokeRec := httptest.NewRecorder()
	handler.ServeHTTP(revokeRec, revokeReq)
	if !bytes.Contains(revokeRec.Body.Bytes(), []byte(`"revoked":true`)) {
		t.Fatalf("revokeApiToken = %s", revokeRec.Body.String())
	}
	// The revoked token no longer authenticates at all (its bearer auth fails
	// before any Console gate is consulted).
	deadReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { currentSession { profileID } }"}`)))
	deadReq.Header.Set("Authorization", "Bearer "+token)
	deadRec := httptest.NewRecorder()
	handler.ServeHTTP(deadRec, deadReq)
	if deadRec.Code == http.StatusOK {
		t.Fatalf("revoked bearer still authenticates: status %d %s", deadRec.Code, deadRec.Body.String())
	}

	// Both lifecycle mutations audited with token attribution columns.
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	var sawCreate, sawRevoke bool
	for _, entry := range entries {
		if entry.Action == store.ActivityTokenCreate && entry.TargetID == id {
			sawCreate = true
		}
		if entry.Action == store.ActivityTokenRevoke && entry.TargetID == id {
			sawRevoke = true
		}
	}
	if !sawCreate || !sawRevoke {
		t.Fatalf("activity missing token lifecycle entries: %+v", entries)
	}
}

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// TestCollectionFeedAnswersAliasedBatch pins the read the web Collections grid
// makes for its poster collages (CORE-2438): one operation carrying an aliased
// sourceItems(kind: "collection") field per Collection, each with its own value
// variable and limit, answered under its alias.
func TestCollectionFeedAnswersAliasedBatch(t *testing.T) {
	handler := newTestHandler(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := auth.Scope{ProfileID: "profile-default", AllLibraries: true, Revealed: true}.LibraryFilter()
	filled, err := handler.store.CreateCollection(ctx, store.Collection{LibraryID: "library-local", Name: "Filled"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.store.AddCollectionMedia(ctx, filled.ID, []store.MediaID{"media-alpha"}, revealed, now); err != nil {
		t.Fatal(err)
	}
	empty, err := handler.store.CreateCollection(ctx, store.Collection{LibraryID: "library-local", Name: "Empty"}, now)
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"query": `query CollectionPosters($v0: String, $v1: String) { ` +
			`c0: sourceItems(kind: "collection", value: $v0, limit: 4) { id artwork { poster } artworkSlots } ` +
			`c1: sourceItems(kind: "collection", value: $v1, limit: 4) { id artwork { poster } artworkSlots } }`,
		"variables": map[string]string{"v0": string(filled.ID), "v1": string(empty.ID)},
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
		Data   map[string][]SourceItem `json:"data"`
		Errors []graphError            `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v, want none", resp.Errors)
	}
	if got := resp.Data["c0"]; len(got) != 1 || got[0].ID != "media-alpha" {
		t.Fatalf("c0 = %#v, want [media-alpha]", got)
	}
	if got := resp.Data["c1"]; len(got) != 0 {
		t.Fatalf("c1 = %#v, want empty", got)
	}
}

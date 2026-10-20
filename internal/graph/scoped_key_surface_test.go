package graph

// SECAUDIT-20260817-SCOPED-KEY-GRAPHQL-1: /api/graphql is the private
// first-party surface; the scoped developer key surface is /v1/.
// The audit proved an admin-scoped read-catalog key resolves to a
// manager-class scope, so GraphQL dispatch (which gates on role, never on
// APIScopes) let it invoke manager mutations. These tests pin the fix: every
// scoped key — admin-scoped or Profile-bound, valid on /v1/ or not — is
// refused at the GraphQL surface before resolver dispatch and before any
// mutation-audit write, with the refusal attributed to the key.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func postScopedKeyGraph(t *testing.T, handler *Handler, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestScopedKeyRefusedOnPrivateGraphQLSurface(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	now := time.Now().UTC()
	_, adminKey, err := st.CreateScopedAPIKey(t.Context(), "surface-admin", []store.APIScope{store.ScopeReadCatalog}, "", now)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey (admin-scoped): %v", err)
	}
	_, profileKey, err := st.CreateScopedAPIKey(t.Context(), "surface-profile", []store.APIScope{store.ScopeReadCatalog}, "profile-default", now)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey (profile-bound): %v", err)
	}

	mutation := `{"query":"mutation C($name:String!){ createTag(name:$name){ id name } }","variables":{"name":"Scoped Key Probe"}}`
	tests := []struct {
		name string
		body string
		key  string
	}{
		{name: "admin-scoped key manager mutation", body: mutation, key: adminKey},
		{name: "profile-bound key manager mutation", body: mutation, key: profileKey},
		{name: "admin-scoped key session read", body: `{"query":"query { profiles { id } }"}`, key: adminKey},
		{name: "admin-scoped key public field", body: `{"query":"query { serverInfo { name } }"}`, key: adminKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postScopedKeyGraph(t, handler, tt.body, tt.key)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), http.StatusForbidden)
			}
		})
	}

	// The refusal happened before resolver dispatch and before the mutation
	// audit lifecycle: no tag exists and no activity entry (attempt, finished,
	// or otherwise) was appended — a refused key leaves zero ambiguity about
	// whether a mutation ran.
	tags, err := st.Tags(t.Context(), true)
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("tags = %+v, want none — a scoped key reached the createTag resolver", tags)
	}
	if entries := allActivity(t, st); len(entries) != 0 {
		t.Fatalf("activity entries = %+v, want none — a scoped-key request reached the mutation audit", entries)
	}

	// The session path through the identical handler is untouched: the same
	// mutation succeeds for the manager session.
	data, errs := post(t, handler, mutation, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("manager session createTag errors = %#v", errs)
	}
	var created TagInfo
	mustUnmarshal(t, data["createTag"], &created)
	if created.ID == "" || created.Name != "Scoped Key Probe" {
		t.Fatalf("manager session createTag = %#v", created)
	}
}

// Evidence — SECAUDIT-20260817-SCOPED-KEY-GRAPHQL-1 (2026-08-20)
//
// Red-first (this test written before the fix, against base main
// f0a4db6e3d7b07c33561cd3d65b4ef33ddc7acd9):
//   cmd: go test ./internal/graph/ -run TestScopedKeyRefusedOnPrivateGraphQLSurface -count=1
//   UTC 2026-08-20T11:33:38Z → 11:33:43Z, rc=1 (FAIL), log streamed to builder session
//   observed: admin-scoped_key_manager_mutation: status = 200 body =
//     {"data":{"createTag":{"id":"tag-ec6bfc7a27f17e12ef10ecd2","name":"Scoped Key Probe",...}}}, want 403
//   observed: tags = [{ID:tag-ec6bfc7a27f17e12ef10ecd2 Name:Scoped Key Probe ...}], want none —
//     the audit finding reproduced: an admin-scoped read-catalog key performed the manager mutation.
//   profile-bound / session-read / public-field subtests likewise returned 200, want 403.
//
// Deliberate break of the sibling boundary test (fix reverted, then restored):
//   cmd: go test ./internal/server/ -run TestScopedKeySurfaceSplit -count=1
//   UTC 2026-08-20T11:37:43Z, rc=1 (FAIL)
//   observed: route_class_test.go:190: scoped key POST /api/graphql mutation = 200, want 403:
//     {"data":{"createTag":{"id":"tag-fd7abedb752a6ce911bea93e","name":"Boundary Probe",...}}}
//
// Green (fix in place, same head as this commit):
//   go test ./internal/graph/ -count=1  → ok, 74.1s elapsed, UTC 2026-08-20T11:34–11:36, rc=0
//   go test ./internal/server/ -count=1 → ok, 45.1s elapsed, UTC 2026-08-20T11:36–11:37, rc=0
//   gofmt -l internal/graph internal/server → empty; go vet ./internal/graph/ ./internal/server/ → rc=0

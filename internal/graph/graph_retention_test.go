package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

// TestRetentionRuleCRUDAndHold exercises the RET-2 graph surface:
// create / list / update / delete of retention rules (Admin-Session-gated) and
// the manager-tier setRetentionCandidateHold (viewer-writable keep flag).
func TestRetentionRuleCRUDAndHold(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// Rules list is empty initially (requires Admin Session).
	openManagerAdminSession(t, st)
	rulesEmpty := retentionRulesViaGraph(t, handler)
	if len(rulesEmpty) != 0 {
		t.Fatalf("initial retentionRules = %v, want empty", rulesEmpty)
	}

	// createRetentionRule mints the rule and its leaving-soon Smart Collection.
	created := postRetentionMutation(t, handler, `mutation {
		createRetentionRule {
			id name sourceExpr targetLibraryID graceDays enabled smartCollectionID
		}
	}`, map[string]any{
		"name":            "Old Acquisitions",
		"sourceExpr":      `{"kind":"all-items"}`,
		"targetLibraryID": "library-local",
		"graceDays":       float64(14),
		"enabled":         true,
	}, "createRetentionRule")

	if created["name"] != "Old Acquisitions" {
		t.Fatalf("createRetentionRule name = %v, want Old Acquisitions", created["name"])
	}
	if created["graceDays"] != float64(14) {
		t.Fatalf("createRetentionRule graceDays = %v, want 14", created["graceDays"])
	}
	if created["enabled"] != true {
		t.Fatalf("createRetentionRule enabled = %v, want true", created["enabled"])
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("createRetentionRule returned empty id")
	}
	// A leaving-soon Smart Collection must have been minted.
	if created["smartCollectionID"] == "" {
		t.Error("createRetentionRule: smartCollectionID is empty, expected a minted collection")
	}

	// Rule appears in the list.
	rules := retentionRulesViaGraph(t, handler)
	if len(rules) != 1 {
		t.Fatalf("after create, retentionRules len = %d, want 1", len(rules))
	}

	// updateRetentionRule patches graceDays and disables the rule.
	updated := postRetentionMutation(t, handler, `mutation {
		updateRetentionRule { id graceDays enabled }
	}`, map[string]any{
		"id":        id,
		"graceDays": float64(30),
		"enabled":   false,
	}, "updateRetentionRule")
	if updated["graceDays"] != float64(30) {
		t.Fatalf("updateRetentionRule graceDays = %v, want 30", updated["graceDays"])
	}
	if updated["enabled"] != false {
		t.Fatalf("updateRetentionRule enabled = %v, want false", updated["enabled"])
	}

	// setRetentionCandidateHold is manager-tier (no Admin Session required)
	// and object-authorized: the target must be a candidate of the rule that
	// the caller's Library filter can resolve. media-alpha sits in the rule's
	// target library and matches the all-items source, so the hold lands.
	holdResult := postRetentionMutation(t, handler, `mutation {
		setRetentionCandidateHold { ruleID mediaID hold }
	}`, map[string]any{
		"ruleID":  id,
		"mediaID": "media-alpha",
		"hold":    true,
	}, "setRetentionCandidateHold")
	if holdResult["hold"] != true {
		t.Fatalf("setRetentionCandidateHold hold = %v, want true", holdResult["hold"])
	}

	// deleteRetentionRule removes the rule.
	deleted := postRetentionMutation(t, handler, `mutation { deleteRetentionRule { id } }`,
		map[string]any{"id": id}, "deleteRetentionRule")
	if deleted["id"] != id {
		t.Fatalf("deleteRetentionRule id = %v, want %v", deleted["id"], id)
	}

	rulesAfter := retentionRulesViaGraph(t, handler)
	if len(rulesAfter) != 0 {
		t.Fatalf("after delete, retentionRules = %v, want empty", rulesAfter)
	}
}

// TestRetentionRuleAdminGated confirms createRetentionRule is Admin-Session-gated
// (complements the bulk H7 TestConsoleFieldsRequireAdminSession test).
func TestRetentionRuleAdminGated(t *testing.T) {
	handler := newTestHandler(t)
	body, _ := json.Marshal(map[string]any{
		"query": `mutation { createRetentionRule { id } }`,
		"variables": map[string]any{
			"name":            "x",
			"sourceExpr":      `{}`,
			"targetLibraryID": "library-local",
		},
	})
	req := httptest.NewRequest("POST", "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Errors) == 0 {
		t.Fatal("createRetentionRule without Admin Session: want error, got success")
	}
}

// TestRetentionCandidateHoldManagerOnlyAndObjectAuthorized locks the ADR-0051
// operator-hold contract: viewer sessions
// are refused by the role gate — viewer rescue is the per-Profile
// Favorite-class keep bit via setFavorite, never this mutation — and manager
// attempts against unknown rules, unknown media, and hidden/cross-Library
// media all refuse. The matching no-new-state half is proven in
// internal/store/retention_rules_test.go, where the candidate cache is
// directly readable.
//
// Red-first evidence (2026-08-20): with the dispatch gate deliberately
// reverted to roleSession, this test failed with "viewer
// setRetentionCandidateHold succeeded, want role refusal"; with the store's
// object-authorization guards deliberately removed instead, it failed with
// "hold with unknown rule succeeded, want refusal". Both guards were then
// restored and the test passed.
func TestRetentionCandidateHoldManagerOnlyAndObjectAuthorized(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	created := postRetentionMutation(t, handler, `mutation {
		createRetentionRule { id }
	}`, map[string]any{
		"name":            "Leaving soon",
		"sourceExpr":      `{"kind":"all-items"}`,
		"targetLibraryID": "library-local",
	}, "createRetentionRule")
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("createRetentionRule returned empty id")
	}

	query := `mutation { setRetentionCandidateHold { ruleID mediaID hold } }`
	holdVars := func(ruleID, mediaID string) map[string]any {
		return map[string]any{"ruleID": ruleID, "mediaID": mediaID, "hold": true}
	}

	// Viewer sessions carry no manager role: the operator hold is refused.
	if errs := postRetentionMutationErrors(t, handler, query, holdVars(id, "media-alpha"), authorizeViewer); len(errs) == 0 {
		t.Fatal("viewer setRetentionCandidateHold succeeded, want role refusal")
	}
	// Unknown rule IDs refuse instead of minting a candidate cache for them.
	if errs := postRetentionMutationErrors(t, handler, query, holdVars("retention-missing", "media-alpha"), authorizeManager); len(errs) == 0 {
		t.Fatal("hold with unknown rule succeeded, want refusal")
	}
	// Unknown media IDs refuse instead of creating retention state.
	if errs := postRetentionMutationErrors(t, handler, query, holdVars(id, "media-missing"), authorizeManager); len(errs) == 0 {
		t.Fatal("hold with unknown media succeeded, want refusal")
	}
	// media-archive sits in the restricted archive Library: hidden from this
	// non-revealed manager session and outside the rule's target Library.
	if errs := postRetentionMutationErrors(t, handler, query, holdVars(id, "media-archive"), authorizeManager); len(errs) == 0 {
		t.Fatal("hold with hidden cross-Library media succeeded, want refusal")
	}
	// The valid candidate still holds after the refusals above.
	holdResult := postRetentionMutation(t, handler, query, holdVars(id, "media-alpha"), "setRetentionCandidateHold")
	if holdResult["hold"] != true {
		t.Fatalf("valid setRetentionCandidateHold hold = %v, want true", holdResult["hold"])
	}
}

// --- helpers ---

func retentionRulesViaGraph(t *testing.T, h *Handler) []any {
	t.Helper()
	data, errs := post(t, h,
		`{"query":"query { retentionRules { id name sourceExpr targetLibraryID graceDays enabled } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("retentionRules: %v", errs)
	}
	var items []any
	if raw, ok := data["retentionRules"]; ok {
		_ = json.Unmarshal(raw, &items)
	}
	return items
}

func postRetentionMutation(t *testing.T, h *Handler, query string, vars map[string]any, field string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp struct {
		Data   map[string]any `json:"data"`
		Errors []graphError   `json:"errors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("%s: decode: %v", field, err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("%s: errors: %v", field, resp.Errors)
	}
	m, _ := resp.Data[field].(map[string]any)
	return m
}

// postRetentionMutationErrors posts one mutation under the given authorizer
// and returns the response's top-level errors (empty when it succeeded).
func postRetentionMutationErrors(t *testing.T, h *Handler, query string, vars map[string]any, authorize func(*http.Request)) []graphError {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/graphql", bytes.NewReader(body))
	authorize(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Errors
}

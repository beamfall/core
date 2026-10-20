package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNavOverrideRequiresManagerRole confirms that viewer-role sessions are
// rejected by the manager-gate on all five nav-override fields.
func TestNavOverrideRequiresManagerRole(t *testing.T) {
	handler := newTestHandler(t)
	for _, q := range []string{
		`{"query":"{ navOverride { hiddenNodeIDs } }"}`,
		`{"query":"{ navOverrideHistory { id } }"}`,
		`{"query":"mutation { setNavOverride(themeAccentColor: \"#FF0000\") { themeAccentColor } }"}`,
		`{"query":"mutation { resetNavOverride { hiddenNodeIDs } }"}`,
		`{"query":"mutation { restoreNavOverride(historyID: 1) { hiddenNodeIDs } }"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(q)))
		req.Header.Set("Content-Type", "application/json")
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct{ Errors []struct{ Message string } }
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal %s: %v", q, err)
		}
		if len(resp.Errors) == 0 || resp.Errors[0].Message != "manager role required" {
			t.Errorf("query %s: errors = %#v, want manager role required", q, resp.Errors)
		}
	}
}

// TestNavOverrideRoundTrip exercises the set → read → reset round-trip via
// the graph resolvers (store-backed with the conformance seed DB).
func TestNavOverrideRoundTrip(t *testing.T) {
	handler := newTestHandler(t)

	// Set partial override. Variables must use the exact names the resolver reads
	// from req.Variables (the hand-rolled system does not resolve $-aliases from
	// the query string — it reads the variables map directly by name).
	body := `{"query":"mutation($hiddenNodeIDs:[String!],$themeAccentColor:String) { setNavOverride(hiddenNodeIDs: $hiddenNodeIDs, themeAccentColor: $themeAccentColor) { hiddenNodeIDs themeAccentColor nodeOrder } }","variables":{"hiddenNodeIDs":["ambient"],"themeAccentColor":"#FF0000"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var setResp struct {
		Data struct {
			SetNavOverride struct {
				HiddenNodeIDs    []string `json:"hiddenNodeIDs"`
				ThemeAccentColor string   `json:"themeAccentColor"`
				NodeOrder        []string `json:"nodeOrder"`
			} `json:"setNavOverride"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setResp); err != nil {
		t.Fatalf("unmarshal setNavOverride: %v", err)
	}
	if len(setResp.Errors) != 0 {
		t.Fatalf("setNavOverride errors: %#v", setResp.Errors)
	}
	ov := setResp.Data.SetNavOverride
	if len(ov.HiddenNodeIDs) != 1 || ov.HiddenNodeIDs[0] != "ambient" {
		t.Errorf("hiddenNodeIDs = %v, want [ambient]", ov.HiddenNodeIDs)
	}
	if ov.ThemeAccentColor != "#FF0000" {
		t.Errorf("themeAccentColor = %q, want #FF0000", ov.ThemeAccentColor)
	}

	// Read back.
	readBody := `{"query":"{ navOverride { hiddenNodeIDs themeAccentColor } }"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(readBody)))
	req2.Header.Set("Content-Type", "application/json")
	authorizeManager(req2)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	var readResp struct {
		Data struct {
			NavOverride struct {
				HiddenNodeIDs    []string `json:"hiddenNodeIDs"`
				ThemeAccentColor string   `json:"themeAccentColor"`
			} `json:"navOverride"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &readResp); err != nil {
		t.Fatalf("unmarshal navOverride: %v", err)
	}
	if len(readResp.Errors) != 0 {
		t.Fatalf("navOverride errors: %#v", readResp.Errors)
	}
	got := readResp.Data.NavOverride
	if len(got.HiddenNodeIDs) != 1 || got.HiddenNodeIDs[0] != "ambient" {
		t.Errorf("read hiddenNodeIDs = %v, want [ambient]", got.HiddenNodeIDs)
	}
	if got.ThemeAccentColor != "#FF0000" {
		t.Errorf("read themeAccentColor = %q, want #FF0000", got.ThemeAccentColor)
	}

	// Reset.
	resetBody := `{"query":"mutation { resetNavOverride { hiddenNodeIDs themeAccentColor } }"}`
	req3 := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(resetBody)))
	req3.Header.Set("Content-Type", "application/json")
	authorizeManager(req3)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	var resetResp struct {
		Data struct {
			ResetNavOverride struct {
				HiddenNodeIDs    []string `json:"hiddenNodeIDs"`
				ThemeAccentColor string   `json:"themeAccentColor"`
			} `json:"resetNavOverride"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(rec3.Body.Bytes(), &resetResp); err != nil {
		t.Fatalf("unmarshal resetNavOverride: %v", err)
	}
	if len(resetResp.Errors) != 0 {
		t.Fatalf("resetNavOverride errors: %#v", resetResp.Errors)
	}
	reset := resetResp.Data.ResetNavOverride
	if len(reset.HiddenNodeIDs) != 0 {
		t.Errorf("after reset hiddenNodeIDs = %v, want empty", reset.HiddenNodeIDs)
	}
	if reset.ThemeAccentColor != "" {
		t.Errorf("after reset themeAccentColor = %q, want empty", reset.ThemeAccentColor)
	}
}

// TestNavOverrideHistoryAndRestore confirms that set records history and
// restoreNavOverride brings back the prior value.
func TestNavOverrideHistoryAndRestore(t *testing.T) {
	handler := newTestHandler(t)

	// Set first override. Variables map is read directly by name — inline GQL
	// args are not parsed by the hand-rolled dispatcher.
	set1 := `{"query":"mutation($themeAccentColor:String) { setNavOverride(themeAccentColor: $themeAccentColor) { themeAccentColor } }","variables":{"themeAccentColor":"#AAAAAA"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(set1)))
	req.Header.Set("Content-Type", "application/json")
	authorizeManager(req)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Set second override (first becomes history).
	set2 := `{"query":"mutation($themeAccentColor:String) { setNavOverride(themeAccentColor: $themeAccentColor) { themeAccentColor } }","variables":{"themeAccentColor":"#BBBBBB"}}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(set2)))
	req2.Header.Set("Content-Type", "application/json")
	authorizeManager(req2)
	handler.ServeHTTP(httptest.NewRecorder(), req2)

	// Read history — should have at least one entry.
	histBody := `{"query":"{ navOverrideHistory { id data { themeAccentColor } } }"}`
	hReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(histBody)))
	hReq.Header.Set("Content-Type", "application/json")
	authorizeManager(hReq)
	hRec := httptest.NewRecorder()
	handler.ServeHTTP(hRec, hReq)

	var histResp struct {
		Data struct {
			NavOverrideHistory []struct {
				ID   float64 `json:"id"`
				Data struct {
					ThemeAccentColor string `json:"themeAccentColor"`
				} `json:"data"`
			} `json:"navOverrideHistory"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(hRec.Body.Bytes(), &histResp); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if len(histResp.Errors) != 0 {
		t.Fatalf("history errors: %#v", histResp.Errors)
	}
	hist := histResp.Data.NavOverrideHistory
	if len(hist) == 0 {
		t.Fatal("navOverrideHistory is empty, expected at least one entry")
	}
	// The newest history entry (index 0 — ordered desc) should be the PREVIOUS
	// value (#AAAAAA) saved when #BBBBBB was written.
	if hist[0].Data.ThemeAccentColor != "#AAAAAA" {
		t.Errorf("history[0].themeAccentColor = %q, want #AAAAAA", hist[0].Data.ThemeAccentColor)
	}

	// Restore the history entry. historyID is the variable name the resolver reads.
	histIDStr := fmt.Sprintf("%g", hist[0].ID)
	restoreBody := `{"query":"mutation($historyID:Int!) { restoreNavOverride(historyID: $historyID) { themeAccentColor } }","variables":{"historyID":` + histIDStr + `}}`
	rReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(restoreBody)))
	rReq.Header.Set("Content-Type", "application/json")
	authorizeManager(rReq)
	rRec := httptest.NewRecorder()
	handler.ServeHTTP(rRec, rReq)

	var restoreResp struct {
		Data struct {
			RestoreNavOverride struct {
				ThemeAccentColor string `json:"themeAccentColor"`
			} `json:"restoreNavOverride"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(rRec.Body.Bytes(), &restoreResp); err != nil {
		t.Fatalf("unmarshal restoreNavOverride: %v", err)
	}
	if len(restoreResp.Errors) != 0 {
		t.Fatalf("restoreNavOverride errors: %#v", restoreResp.Errors)
	}
	restored := restoreResp.Data.RestoreNavOverride.ThemeAccentColor
	if restored != "#AAAAAA" {
		t.Errorf("restored themeAccentColor = %q, want #AAAAAA", restored)
	}
}

// TestNavOverrideAppliedToManifest confirms that a hidden node and a theme
// accent color override are reflected in the manifest document served to clients.
func TestNavOverrideAppliedToManifest(t *testing.T) {
	handler := newTestHandler(t)
	ctx := context.Background()
	_ = ctx // used implicitly through the HTTP handler

	// Set override: hide the ambient node, change accent color. Variables map is
	// read directly by name — inline GQL args are not parsed by the hand-rolled dispatcher.
	setBody := `{"query":"mutation($hiddenNodeIDs:[String!],$themeAccentColor:String) { setNavOverride(hiddenNodeIDs: $hiddenNodeIDs, themeAccentColor: $themeAccentColor) { hiddenNodeIDs } }","variables":{"hiddenNodeIDs":["ambient"],"themeAccentColor":"#123456"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(setBody)))
	req.Header.Set("Content-Type", "application/json")
	authorizeManager(req)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Read the manifest and confirm ambient node is absent and accent matches.
	// The hand-rolled GraphQL system maps the "manifest" query alias to the
	// "navigationManifest" response key (readField dataKey, not the query field name).
	mBody := `{"query":"{ navigationManifest { navigation { id } theme { accentColor } } }"}`
	mReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(mBody)))
	mReq.Header.Set("Content-Type", "application/json")
	authorizeViewer(mReq)
	mRec := httptest.NewRecorder()
	handler.ServeHTTP(mRec, mReq)

	var mResp struct {
		Data struct {
			Manifest struct {
				Navigation []struct{ ID string } `json:"navigation"`
				Theme      struct {
					AccentColor string `json:"accentColor"`
				} `json:"theme"`
			} `json:"navigationManifest"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(mRec.Body.Bytes(), &mResp); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(mResp.Errors) != 0 {
		t.Fatalf("manifest errors: %#v", mResp.Errors)
	}
	for _, node := range mResp.Data.Manifest.Navigation {
		if node.ID == "ambient" {
			t.Error("ambient node still present in manifest after hiding it via override")
		}
	}
	if got := mResp.Data.Manifest.Theme.AccentColor; got != "#123456" {
		t.Errorf("manifest theme.accentColor = %q, want #123456", got)
	}
}

// TestRestoreNavOverrideInvalidID confirms a useful error when the history
// entry ID does not exist.
func TestRestoreNavOverrideInvalidID(t *testing.T) {
	handler := newTestHandler(t)
	body := `{"query":"mutation { restoreNavOverride(historyID: 99999) { hiddenNodeIDs } }"}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct{ Errors []struct{ Message string } }
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Errors) == 0 {
		t.Fatal("expected error for unknown historyID, got none")
	}
}

// anchor unused import
var _ = time.Now

package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store/storetest"
)

// post runs one GraphQL request body with the given authorizer and returns the
// decoded data/errors envelope.
func post(t *testing.T, handler *Handler, body string, authorize func(*http.Request)) (map[string]json.RawMessage, []graphError) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	if authorize != nil {
		authorize(req)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []graphError               `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	return resp.Data, resp.Errors
}

// TestAdminSessionGatesScopedKeys is the admin-session vertical slice: a
// manager session over an adult Profile cannot touch scoped API keys until it
// re-authenticates with the server admin password, after which the short-lived
// Admin Session window authorizes the Console operation.
func TestAdminSessionGatesScopedKeys(t *testing.T) {
	// feature:admin-session
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	// Before re-auth: a manager session is denied the Console operation.
	_, errs := post(t, handler, `{"query":"query { apiKeys { id } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin apiKeys errors = %#v, want admin-session gate", errs)
	}

	// Wrong password is rejected without opening the window.
	data, errs := post(t, handler,
		`{"query":"mutation V($password: String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"wrong"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "rejected") {
		t.Fatalf("wrong-password errors = %#v (data=%v), want generic rejection", errs, data)
	}

	// Correct password opens the window.
	data, errs = post(t, handler,
		`{"query":"mutation V($password: String!){ verifyAdminSession(password:$password){ active expiresAt } }","variables":{"password":"correct horse battery"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("verifyAdminSession errors = %#v", errs)
	}
	var verified AdminSessionInfo
	mustUnmarshal(t, data["verifyAdminSession"], &verified)
	if !verified.Active || verified.ExpiresAt == "" {
		t.Fatalf("verifyAdminSession = %#v, want active with expiry", verified)
	}

	// After re-auth: the Console operation is authorized.
	data, errs = post(t, handler, `{"query":"query { apiKeys { id } adminSession { active expiresAt } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("post-admin apiKeys errors = %#v", errs)
	}
	var session AdminSessionInfo
	mustUnmarshal(t, data["adminSession"], &session)
	if !session.Active || session.ExpiresAt == "" {
		t.Fatalf("adminSession = %#v, want active with expiry after re-auth", session)
	}

	// Ending the admin session re-locks the Console operation immediately.
	if _, errs := post(t, handler, `{"query":"mutation { endAdminSession { active } }"}`, authorizeManager); len(errs) != 0 {
		t.Fatalf("endAdminSession errors = %#v", errs)
	}
	if _, errs := post(t, handler, `{"query":"query { apiKeys { id } }"}`, authorizeManager); len(errs) == 0 {
		t.Fatal("apiKeys allowed after endAdminSession; window not cleared")
	}
}

// TestAdminSessionRejectsManagedProfile asserts a managed (non-adult) Profile
// can never open the Admin Session, even with the right password (ADR-0007 §4:
// PINs never grant admin; admin is an adult-Profile tier).
func TestAdminSessionRejectsManagedProfile(t *testing.T) {
	// feature:admin-session
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	_, errs := post(t, handler,
		`{"query":"mutation V($password: String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`,
		authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "rejected") {
		t.Fatalf("managed-profile verify errors = %#v, want rejection", errs)
	}
}

// TestScopedAPIKeyLifecycle exercises the ADR-0040 scoped-key Console
// operation end-to-end behind an Admin Session: create with the five-scope
// vocabulary, the secret returned exactly once, list metadata without the
// secret, an unknown scope rejected, and revoke.
func TestScopedAPIKeyLifecycle(t *testing.T) {
	// feature:api-keys
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	if _, errs := post(t, handler,
		`{"query":"mutation V($password: String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("admin re-auth errors = %#v", errs)
	}

	// Create with two scopes from the closed vocabulary.
	data, errs := post(t, handler,
		`{"query":"mutation C($name:String!,$scopes:[String!]!){ createApiKey(name:$name,scopes:$scopes){ key info { id name scopes revoked } } }","variables":{"name":"Tautulli","scopes":["read-sessions","events-subscribe"]}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createApiKey errors = %#v", errs)
	}
	var created CreatedScopedAPIKeyInfo
	mustUnmarshal(t, data["createApiKey"], &created)
	if !strings.HasPrefix(created.Key, "bfk_") {
		t.Fatalf("created key = %q, want bfk_ prefix", created.Key)
	}
	if len(created.Info.Scopes) != 2 {
		t.Fatalf("created scopes = %v, want 2", created.Info.Scopes)
	}

	// List returns metadata, never the secret.
	data, errs = post(t, handler, `{"query":"query { apiKeys { id name scopes revoked } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("apiKeys errors = %#v", errs)
	}
	var keys []ScopedAPIKeyInfo
	mustUnmarshal(t, data["apiKeys"], &keys)
	if len(keys) != 1 {
		t.Fatalf("apiKeys len = %d, want 1", len(keys))
	}
	if strings.Contains(string(data["apiKeys"]), created.Key) {
		t.Fatal("apiKeys list leaks the full key secret")
	}

	// An unknown scope is rejected (closed vocabulary, ADR-0040).
	_, errs = post(t, handler,
		`{"query":"mutation C($name:String!,$scopes:[String!]!){ createApiKey(name:$name,scopes:$scopes){ info { id } } }","variables":{"name":"Bad","scopes":["read-everything"]}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown API scope") {
		t.Fatalf("unknown-scope errors = %#v, want rejection", errs)
	}

	// Revoke flips the row.
	data, errs = post(t, handler,
		`{"query":"mutation R($id:String!){ revokeApiKey(id:$id){ id revoked } }","variables":{"id":"`+keys[0].ID+`"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("revokeApiKey errors = %#v", errs)
	}
	var revoked ScopedAPIKeyInfo
	mustUnmarshal(t, data["revokeApiKey"], &revoked)
	if !revoked.Revoked {
		t.Fatal("revokeApiKey did not revoke the row")
	}

	// A revoked key no longer verifies.
	if _, err := st.VerifyScopedAPIKey(t.Context(), created.Key, time.Now().UTC()); err == nil {
		t.Fatal("revoked key still verifies")
	}
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
}

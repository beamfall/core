//go:build !beamfall_appstore

package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// storeCompatController is the graph-side stand-in for the compat settings
// controller: it stores through the real store with no bootstrap defaults.
type storeCompatController struct{ st *store.Store }

func (c storeCompatController) Settings(ctx context.Context) (store.JellyfinCompatConfig, error) {
	return c.st.JellyfinCompatConfig(ctx, store.JellyfinCompatConfig{})
}

func (c storeCompatController) Configure(ctx context.Context, patch store.JellyfinCompatPatch) (store.JellyfinCompatConfig, error) {
	return c.st.SetJellyfinCompatConfig(ctx, patch, store.JellyfinCompatConfig{}, time.Now())
}

type compatResponse struct {
	Data struct {
		JellyfinCompat              JellyfinCompatInfo        `json:"jellyfinCompat"`
		SetJellyfinCompat           JellyfinCompatInfo        `json:"setJellyfinCompat"`
		SetProfileIntegrationSignIn JellyfinCompatProfileInfo `json:"setProfileIntegrationSignIn"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

const compatSelection = `{ enabled allowedCIDRs trustedProxyCIDRs libraries { id title allowed } profiles { id name integrationSignIn } }`

func decodeCompat(t *testing.T, body []byte) compatResponse {
	t.Helper()
	var resp compatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, body)
	}
	return resp
}

// TestJellyfinCompatConsoleFields covers CORE-2683: the settings fields are
// Admin-Session gated, restricted Libraries are never listed or accepted,
// only enabled adult Profiles may opt in, and every write is audited.
func TestJellyfinCompatConsoleFields(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	handler.SetJellyfinCompatController(storeCompatController{st})
	open, err := st.CreateLibrary(ctx, "Open", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := st.CreateLibrary(ctx, "Hidden", true, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	child, err := st.CreateProfile(ctx, "Kid", store.ProfileManaged, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}

	read := `query { jellyfinCompat ` + compatSelection + ` }`
	if rec := postGraphQL(t, handler, authorizeManager, read, nil); !strings.Contains(rec.Body.String(), "admin session required") {
		t.Fatalf("non-admin jellyfinCompat = %s", rec.Body.String())
	}
	openManagerAdminSession(t, st)

	resp := decodeCompat(t, postGraphQL(t, handler, authorizeManager, read, nil).Body.Bytes())
	if len(resp.Errors) != 0 || resp.Data.JellyfinCompat.Enabled {
		t.Fatalf("initial read = %+v", resp)
	}
	for _, library := range resp.Data.JellyfinCompat.Libraries {
		if library.ID == string(restricted.ID) {
			t.Fatalf("restricted library listed: %+v", resp.Data.JellyfinCompat.Libraries)
		}
	}
	for _, profile := range resp.Data.JellyfinCompat.Profiles {
		if profile.ID == string(child.ID) {
			t.Fatalf("managed profile listed: %+v", resp.Data.JellyfinCompat.Profiles)
		}
	}

	set := `mutation S($enabled: Boolean, $allowedCIDRs: [String!], $libraryIDs: [String!]) { setJellyfinCompat(enabled: $enabled, allowedCIDRs: $allowedCIDRs, libraryIDs: $libraryIDs) ` + compatSelection + ` }`
	for name, vars := range map[string]map[string]any{
		"restricted library": {"libraryIDs": []string{string(restricted.ID)}},
		"overlay network":    {"allowedCIDRs": []string{"100.64.0.0/10"}},
		"empty change":       {},
	} {
		if resp := decodeCompat(t, postGraphQL(t, handler, authorizeManager, set, vars).Body.Bytes()); len(resp.Errors) == 0 {
			t.Fatalf("%s accepted: %+v", name, resp.Data.SetJellyfinCompat)
		}
	}
	resp = decodeCompat(t, postGraphQL(t, handler, authorizeManager, set, map[string]any{
		"enabled": true, "allowedCIDRs": []string{"192.168.1.9/24"}, "libraryIDs": []string{string(open.ID)},
	}).Body.Bytes())
	got := resp.Data.SetJellyfinCompat
	if len(resp.Errors) != 0 || !got.Enabled || len(got.AllowedCIDRs) != 1 || got.AllowedCIDRs[0] != "192.168.1.0/24" {
		t.Fatalf("setJellyfinCompat = %+v", resp)
	}
	var allowed bool
	for _, library := range got.Libraries {
		allowed = allowed || (library.ID == string(open.ID) && library.Allowed)
	}
	if !allowed {
		t.Fatalf("allowlisted library not marked allowed: %+v", got.Libraries)
	}

	optIn := `mutation O($profileID: String!, $enabled: Boolean!) { setProfileIntegrationSignIn(profileID: $profileID, enabled: $enabled) { id name integrationSignIn } }`
	if resp := decodeCompat(t, postGraphQL(t, handler, authorizeManager, optIn, map[string]any{"profileID": string(child.ID), "enabled": true}).Body.Bytes()); len(resp.Errors) == 0 {
		t.Fatalf("managed profile opt-in accepted: %+v", resp.Data.SetProfileIntegrationSignIn)
	}
	resp = decodeCompat(t, postGraphQL(t, handler, authorizeManager, optIn, map[string]any{"profileID": "profile-default", "enabled": true}).Body.Bytes())
	if len(resp.Errors) != 0 || !resp.Data.SetProfileIntegrationSignIn.IntegrationSignIn {
		t.Fatalf("adult opt-in = %+v", resp)
	}
	// An omitted enabled argument is refused rather than read as an opt-out.
	omitted := decodeCompat(t, postGraphQL(t, handler, authorizeManager, `mutation { setProfileIntegrationSignIn(profileID: "profile-default") { id integrationSignIn } }`, nil).Body.Bytes())
	if len(omitted.Errors) == 0 || !strings.Contains(omitted.Errors[0].Message, "enabled is required") {
		t.Fatalf("opt-in without enabled = %+v", omitted)
	}

	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	audited := map[string]bool{}
	for _, entry := range entries {
		if entry.Action == store.ActivityConfigUpdate {
			audited[entry.TargetType] = true
		}
	}
	if !audited["jellyfin-compat"] || !audited["profile-integration-sign-in"] {
		t.Fatalf("audit targets = %v; want both compat writes", audited)
	}
}

func TestJellyfinCompatFieldsUnavailableWithoutController(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	resp := decodeCompat(t, postGraphQL(t, handler, authorizeManager, `query { jellyfinCompat { enabled } }`, nil).Body.Bytes())
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "unavailable") {
		t.Fatalf("read without controller = %+v", resp)
	}
}

type compatKeyResponse struct {
	Data struct {
		JellyfinCompatKeys      []JellyfinCompatKeyInfo      `json:"jellyfinCompatKeys"`
		CreateJellyfinCompatKey CreatedJellyfinCompatKeyInfo `json:"createJellyfinCompatKey"`
		RevokeJellyfinCompatKey JellyfinCompatKeyInfo        `json:"revokeJellyfinCompatKey"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

func decodeCompatKeys(t *testing.T, body []byte) compatKeyResponse {
	t.Helper()
	var resp compatKeyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, body)
	}
	return resp
}

// TestJellyfinCompatKeyConsoleFields covers CORE-2725: minting, listing and
// revoking integration keys is Admin-Session gated and audited, a key holds
// only the read and refresh scopes, the token is returned once and never
// listed, and only an opted-in adult Profile can hold one.
func TestJellyfinCompatKeyConsoleFields(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	const keyFields = `{ id profileID profileName name scopes createdAt lastUsedAt revokedAt }`
	list := `query { jellyfinCompatKeys ` + keyFields + ` }`
	mint := `mutation M($profileID: String!, $name: String!) { createJellyfinCompatKey(profileID: $profileID, name: $name) { token key ` + keyFields + ` } }`
	revoke := `mutation R($id: String!) { revokeJellyfinCompatKey(id: $id) ` + keyFields + ` }`

	if rec := postGraphQL(t, handler, authorizeManager, mint, map[string]any{"profileID": "profile-default", "name": "Seerr"}); !strings.Contains(rec.Body.String(), "admin session required") {
		t.Fatalf("non-admin mint = %s", rec.Body.String())
	}
	openManagerAdminSession(t, st)
	if resp := decodeCompatKeys(t, postGraphQL(t, handler, authorizeManager, mint, map[string]any{"profileID": "profile-default", "name": "Seerr"}).Body.Bytes()); len(resp.Errors) == 0 {
		t.Fatalf("mint before opt-in accepted: %+v", resp.Data.CreateJellyfinCompatKey.Key)
	}
	child, err := st.CreateProfile(ctx, "Kid", store.ProfileManaged, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp := decodeCompatKeys(t, postGraphQL(t, handler, authorizeManager, mint, map[string]any{"profileID": string(child.ID), "name": "Seerr"}).Body.Bytes()); len(resp.Errors) == 0 {
		t.Fatalf("managed profile mint accepted: %+v", resp.Data.CreateJellyfinCompatKey.Key)
	}
	if _, err := st.SetProfileIntegrationSignIn(ctx, "profile-default", true); err != nil {
		t.Fatal(err)
	}
	resp := decodeCompatKeys(t, postGraphQL(t, handler, authorizeManager, mint, map[string]any{"profileID": "profile-default", "name": "Seerr"}).Body.Bytes())
	created := resp.Data.CreateJellyfinCompatKey
	if len(resp.Errors) != 0 || !strings.HasPrefix(created.Token, store.CompatKeyPrefix) || created.Key.Name != "Seerr" || created.Key.RevokedAt != "" {
		t.Fatalf("mint = %+v", resp)
	}
	if strings.Join(created.Key.Scopes, ",") != "compat.read,compat.refresh" {
		t.Fatalf("minted scopes = %v; want read and refresh only", created.Key.Scopes)
	}

	listed := postGraphQL(t, handler, authorizeManager, list, nil).Body
	if strings.Contains(listed.String(), created.Token) || strings.Contains(listed.String(), strings.TrimPrefix(created.Token, store.CompatKeyPrefix)) {
		t.Fatal("key list carries the token")
	}
	resp = decodeCompatKeys(t, listed.Bytes())
	if len(resp.Errors) != 0 || len(resp.Data.JellyfinCompatKeys) != 1 || resp.Data.JellyfinCompatKeys[0].ID != created.Key.ID || resp.Data.JellyfinCompatKeys[0].ProfileName == "" {
		t.Fatalf("list = %+v", resp)
	}

	resp = decodeCompatKeys(t, postGraphQL(t, handler, authorizeManager, revoke, map[string]any{"id": created.Key.ID}).Body.Bytes())
	if len(resp.Errors) != 0 || resp.Data.RevokeJellyfinCompatKey.RevokedAt == "" {
		t.Fatalf("revoke = %+v", resp)
	}
	if _, err := st.CompatIntegrationKeyByToken(ctx, created.Token, time.Now()); err == nil {
		t.Fatal("revoked key still resolves")
	}
	if resp := decodeCompatKeys(t, postGraphQL(t, handler, authorizeManager, revoke, map[string]any{"id": "missing"}).Body.Bytes()); len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "key not found") {
		t.Fatalf("revoke missing = %+v", resp)
	}

	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	audited := map[string]bool{}
	for _, entry := range entries {
		if entry.TargetType == "integration-key" && entry.TargetID == created.Key.ID {
			audited[entry.Action] = true
		}
	}
	if !audited[store.ActivityKeyCreate] || !audited[store.ActivityKeyRevoke] {
		t.Fatalf("key audit = %v; want create and revoke", audited)
	}
}

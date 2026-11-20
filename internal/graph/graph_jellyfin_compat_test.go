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

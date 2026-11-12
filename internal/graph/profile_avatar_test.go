package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

// A Profile chooses its own avatar through setClientPref; every session then
// sees it in the picker payload, and only a short token is accepted
// (CORE-1196).
func TestProfilesQueryCarriesChosenAvatar(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	post := func(body string) []byte {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Body.Bytes()
	}
	setAvatar := func(value string) []struct{ Message string } {
		body, err := json.Marshal(map[string]any{
			"query":     "mutation S($key: String!, $value: String!) { setClientPref(key: $key, value: $value) { key } }",
			"variables": map[string]string{"key": "profile.avatar", "value": value},
		})
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Errors []struct{ Message string } `json:"errors"`
		}
		if err := json.Unmarshal(post(string(body)), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Errors
	}
	avatars := func() map[string]string {
		var resp struct {
			Data struct {
				Profiles []ProfileSummary `json:"profiles"`
			} `json:"data"`
			Errors []struct{ Message string } `json:"errors"`
		}
		if err := json.Unmarshal(post(`{"query":"query P { profiles { id avatar } }"}`), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("profiles errors = %#v", resp.Errors)
		}
		got := map[string]string{}
		for _, profile := range resp.Data.Profiles {
			got[profile.ID] = profile.Avatar
		}
		return got
	}

	for id, avatar := range avatars() {
		if avatar != "" {
			t.Fatalf("profile %s starts with avatar %q, want none", id, avatar)
		}
	}
	if errs := setAvatar("rocket.3"); len(errs) != 0 {
		t.Fatalf("setting the avatar failed: %#v", errs)
	}
	chosen := 0
	for _, avatar := range avatars() {
		if avatar == "rocket.3" {
			chosen++
		} else if avatar != "" {
			t.Fatalf("unexpected avatar %q", avatar)
		}
	}
	if chosen != 1 {
		t.Fatalf("%d profiles carry the avatar, want only the session's own", chosen)
	}
	if errs := setAvatar("https://example.test/a.png"); len(errs) == 0 {
		t.Fatal("a URL was accepted as an avatar")
	}
	if errs := setAvatar(""); len(errs) != 0 {
		t.Fatalf("clearing the avatar failed: %#v", errs)
	}
	for id, avatar := range avatars() {
		if avatar != "" {
			t.Fatalf("profile %s still has avatar %q after clear", id, avatar)
		}
	}
}

package conformance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestPluginDeclaredLibraryDefaultsDriveCoreBehavior(t *testing.T) {
	tests := []struct {
		name   string
		bundle plugin.Bundle
		query  string
		assert func(*testing.T, graphResponse)
	}{{
		name: "restricted",
		bundle: plugin.Bundle{
			ID:              "restricted-default",
			Version:         "1.0.0",
			DisplayName:     plugin.L("Restricted Default"),
			MediaLabel:      plugin.L("Item"),
			Layouts:         []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
			LibraryDefaults: plugin.LibraryDefaults{Restricted: true},
		},
		query: `{"query":"mutation Create($title: String!, $pluginID: String!, $restricted: Boolean!) { createLibrary(title: $title, pluginID: $pluginID, restricted: $restricted) { restricted } }","variables":{"title":"Restricted Default","pluginID":"restricted-default","restricted":false}}`,
		assert: func(t *testing.T, resp graphResponse) {
			t.Helper()
			create, ok := resp.Data["createLibrary"].(map[string]any)
			if !ok {
				t.Fatalf("createLibrary payload = %#v", resp.Data["createLibrary"])
			}
			if create["restricted"] != true {
				t.Fatalf("createLibrary.restricted = %#v, want true", create["restricted"])
			}
		},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := storetest.Open(t)
			registry, err := newTestPluginRegistry([]plugin.Bundle{tt.bundle})
			if err != nil {
				t.Fatal(err)
			}
			handler := graph.NewHandler(
				manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
				auth.NewService(st),
				st,
				registry,
			)
			if err := st.SetSessionAdminUntil(t.Context(), store.SessionID("session-revealed"), time.Now().Add(time.Hour).UTC()); err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(tt.query)))
			req.Header.Set("X-Beamfall-Session", "session-revealed")
			req.Header.Set("X-Beamfall-Device", "device-tv")
			req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			resp := assertGraphOK(t, rec.Body.Bytes())
			tt.assert(t, resp)
		})
	}
}

type graphResponse struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func assertGraphOK(t *testing.T, raw []byte) graphResponse {
	t.Helper()
	var body graphResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode graph response: %v\n%s", err, raw)
	}
	if len(body.Errors) != 0 {
		t.Fatalf("graphql errors = %#v", body.Errors)
	}
	return body
}

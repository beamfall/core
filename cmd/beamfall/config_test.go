package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeConfigServer is a minimal stand-in for the manager-gated GraphQL config
// surface. It holds the live seedable state in memory and answers the
// consoleConfig query plus the setConsoleConfig / setPluginActive mutations,
// recording every mutation so a test can assert idempotency.
type fakeConfigServer struct {
	t                 *testing.T
	backgroundWorkers int
	plugins           map[string]bool
	mutations         int
	gotAuth           string
}

func newFakeConfigServer(t *testing.T) *fakeConfigServer {
	return &fakeConfigServer{
		t:                 t,
		backgroundWorkers: 2,
		plugins:           map[string]bool{"film-tv": true, "music": false},
	}
}

func (f *fakeConfigServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/graphql" {
			f.t.Fatalf("path = %q, want /api/graphql", r.URL.Path)
		}
		f.gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Fatal(err)
		}
		switch {
		case strings.Contains(body.Query, "setConsoleConfig"):
			f.mutations++
			f.backgroundWorkers = int(body.Variables["backgroundWorkers"].(float64))
			f.writeConsoleConfig(w)
		case strings.Contains(body.Query, "setPluginActive"):
			f.mutations++
			id, _ := body.Variables["pluginID"].(string)
			active, _ := body.Variables["active"].(bool)
			if _, ok := f.plugins[id]; !ok {
				_, _ = w.Write([]byte(`{"errors":[{"message":"unknown plugin"}]}`))
				return
			}
			f.plugins[id] = active
			f.writeConsoleConfig(w)
		case strings.Contains(body.Query, "consoleConfig"):
			f.writeConsoleConfig(w)
		default:
			f.t.Fatalf("unexpected query %q", body.Query)
		}
	}
}

func (f *fakeConfigServer) writeConsoleConfig(w http.ResponseWriter) {
	type pluginWire struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	}
	plugins := make([]pluginWire, 0, len(f.plugins))
	for id, active := range f.plugins {
		plugins = append(plugins, pluginWire{ID: id, Active: active})
	}
	payload := map[string]any{
		"data": map[string]any{
			"consoleConfig": map[string]any{
				"backgroundWorkers": f.backgroundWorkers,
				"plugins":           plugins,
			},
		},
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func TestExportConfigPostsBearerAndOmitsSecrets(t *testing.T) {
	fake := newFakeConfigServer(t)
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	var out bytes.Buffer

	if err := exportConfig(context.Background(), []string{
		"--server", srv.URL,
		"--token", "export-token",
	}, &out); err != nil {
		t.Fatal(err)
	}

	if fake.gotAuth != "Bearer export-token" {
		t.Fatalf("Authorization = %q, want bearer token", fake.gotAuth)
	}
	dump := out.String()
	// Stable shape: version + backgroundWorkers + sorted plugins.
	var doc configDocument
	if err := json.Unmarshal([]byte(dump), &doc); err != nil {
		t.Fatalf("export output is not valid document JSON: %v\n%s", err, dump)
	}
	if doc.Version != configDocumentVersion {
		t.Fatalf("version = %d, want %d", doc.Version, configDocumentVersion)
	}
	if doc.BackgroundWorkers != 2 {
		t.Fatalf("backgroundWorkers = %d, want 2", doc.BackgroundWorkers)
	}
	if len(doc.Plugins) != 2 || doc.Plugins[0].ID != "film-tv" || doc.Plugins[1].ID != "music" {
		t.Fatalf("plugins not stably sorted: %#v", doc.Plugins)
	}
	// Invariant #5: no secret-shaped field may appear in the export.
	for _, banned := range []string{"password", "secret", "token", "key", "hash"} {
		if strings.Contains(strings.ToLower(dump), banned) {
			t.Fatalf("export leaked a secret-shaped field %q:\n%s", banned, dump)
		}
	}
}

func TestApplyConfigRoundTripIsIdempotent(t *testing.T) {
	fake := newFakeConfigServer(t)
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	// Export the live config, then apply it straight back: no values differ, so
	// no mutation should be sent (a true no-op round trip).
	var exported bytes.Buffer
	if err := exportConfig(context.Background(), []string{"--server", srv.URL, "--token", "tok"}, &exported); err != nil {
		t.Fatal(err)
	}

	var firstOut bytes.Buffer
	if err := applyConfig(context.Background(), []string{"--server", srv.URL, "--token", "tok"}, bytes.NewReader(exported.Bytes()), &firstOut); err != nil {
		t.Fatal(err)
	}
	if fake.mutations != 0 {
		t.Fatalf("applying the exported config sent %d mutations, want 0 (no-op)", fake.mutations)
	}
	if !strings.Contains(firstOut.String(), "already up to date") {
		t.Fatalf("first apply output = %q, want no-op message", firstOut.String())
	}

	// A document with a real change applies exactly the differing fields.
	changeDoc := []byte(`{"version":1,"backgroundWorkers":4,"plugins":[{"id":"film-tv","active":true},{"id":"music","active":true}]}`)
	var changeOut bytes.Buffer
	if err := applyConfig(context.Background(), []string{"--server", srv.URL, "--token", "tok"}, bytes.NewReader(changeDoc), &changeOut); err != nil {
		t.Fatal(err)
	}
	// backgroundWorkers 2->4 and music false->true differ; film-tv unchanged.
	if fake.mutations != 2 {
		t.Fatalf("changed apply sent %d mutations, want 2", fake.mutations)
	}
	if fake.backgroundWorkers != 4 || !fake.plugins["music"] {
		t.Fatalf("apply did not converge: workers=%d music=%v", fake.backgroundWorkers, fake.plugins["music"])
	}

	// Re-applying the same change is now a no-op (idempotent).
	fake.mutations = 0
	var secondOut bytes.Buffer
	if err := applyConfig(context.Background(), []string{"--server", srv.URL, "--token", "tok"}, bytes.NewReader(changeDoc), &secondOut); err != nil {
		t.Fatal(err)
	}
	if fake.mutations != 0 {
		t.Fatalf("re-applying same document sent %d mutations, want 0", fake.mutations)
	}
}

func TestApplyConfigRejectsUnknownPluginWithoutHalfApplying(t *testing.T) {
	fake := newFakeConfigServer(t)
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	doc := []byte(`{"version":1,"backgroundWorkers":8,"plugins":[{"id":"ghost","active":true}]}`)
	err := applyConfig(context.Background(), []string{"--server", srv.URL, "--token", "tok"}, bytes.NewReader(doc), nil)
	if err == nil {
		t.Fatal("apply accepted a document naming an unloaded plugin")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("error = %q, want it to name the unknown plugin", err.Error())
	}
	// The validation happens before any write, so the backgroundWorkers change
	// must NOT have been applied (no half-apply).
	if fake.mutations != 0 {
		t.Fatalf("invalid document half-applied: %d mutations sent", fake.mutations)
	}
	if fake.backgroundWorkers != 2 {
		t.Fatalf("backgroundWorkers changed despite invalid document: %d", fake.backgroundWorkers)
	}
}

func TestApplyConfigRejectsInvalidDocuments(t *testing.T) {
	cases := map[string]string{
		"bad version":       `{"version":99,"backgroundWorkers":2,"plugins":[]}`,
		"zero workers":      `{"version":1,"backgroundWorkers":0,"plugins":[]}`,
		"unknown field":     `{"version":1,"backgroundWorkers":2,"plugins":[],"adminPassword":"x"}`,
		"duplicate plugin":  `{"version":1,"backgroundWorkers":2,"plugins":[{"id":"a","active":true},{"id":"a","active":false}]}`,
		"empty plugin id":   `{"version":1,"backgroundWorkers":2,"plugins":[{"id":"","active":true}]}`,
		"not json":          `{not json`,
		"trailing document": `{"version":1,"backgroundWorkers":2,"plugins":[]} {"version":1,"backgroundWorkers":3,"plugins":[]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfigDocument([]byte(doc)); err == nil {
				t.Fatalf("parseConfigDocument accepted invalid document: %s", doc)
			}
		})
	}
}

func TestApplyConfigRequiresInput(t *testing.T) {
	if err := applyConfig(context.Background(), []string{"--server", "http://127.0.0.1:1", "--token", "tok"}, nil, nil); err == nil {
		t.Fatal("apply accepted missing document with no stdin")
	}
}

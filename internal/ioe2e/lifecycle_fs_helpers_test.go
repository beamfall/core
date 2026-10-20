//go:build io_e2e

package ioe2e

import (
	"encoding/json"
	"testing"

	"github.com/beamfall/core/internal/apptest"
	"github.com/beamfall/core/internal/config"
)

// devManagerHeaders attaches the development-seeded manager device credentials
// (internal/store/sqlite.go demo seed: device-mac / session-manager, Manager
// role) so a Client speaks as the Console operator. The same headers the
// internal/app HTTP tests use.
func devManagerHeaders(c *apptest.Client) *apptest.Client {
	return c.
		WithHeader("X-Beamfall-Session", "session-manager").
		WithHeader("X-Beamfall-Device", "device-mac").
		WithHeader("X-Beamfall-Device-Secret", "dev-manager-secret")
}

// bootFS boots a dev-seeded harness with a manager-authenticated client and
// returns the harness, that client, and the captured DataDir. DataDir is read
// via WithConfig: boot() assigns cfg.DataDir = t.TempDir() before running the
// config mutators, so the mutator observes the live temp data dir the app uses
// for catalog.db, secrets/, backups/, artwork/, etc.
//
// adminPassword, when non-empty, is installed as the first-run admin password
// via BEAMFALL_ADMIN_PASSWORD (internal/app/secrets.go bootstrapAdminPassword,
// called from app.New) so the journey can open an Admin Session over HTTP with
// verifyAdminSession — the only way to reach the Console (admin-gated)
// mutations purely over the wire.
func bootFS(t *testing.T, adminPassword string) (*apptest.Harness, *apptest.Client, string) {
	t.Helper()
	if adminPassword != "" {
		t.Setenv("BEAMFALL_ADMIN_PASSWORD", adminPassword)
	}
	var dataDir string
	h := apptest.Boot(t, apptest.WithConfig(func(c *config.Config) {
		dataDir = c.DataDir
	}))
	if dataDir == "" {
		t.Fatal("bootFS: DataDir not captured from config")
	}
	return h, devManagerHeaders(h.Client), dataDir
}

// openAdminSession re-authenticates the manager session with the server admin
// password (verifyAdminSession), opening the short-lived Admin Session window
// that authorizes Console operations (internal/graph/graph.go: the
// `scope.Admin` gate). Fails loud on any GraphQL error or an inactive window.
func openAdminSession(t *testing.T, c *apptest.Client, password string) {
	t.Helper()
	q := `mutation($password:String!){ verifyAdminSession(password:$password){ active } }`
	resp := c.POST("/api/graphql", "application/json", graphqlBody(t, q, map[string]any{"password": password}))
	var out struct {
		Data struct {
			VerifyAdminSession struct {
				Active bool `json:"active"`
			} `json:"verifyAdminSession"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	resp.JSON(&out)
	if len(out.Errors) != 0 {
		t.Fatalf("verifyAdminSession errors: %+v", out.Errors)
	}
	if !out.Data.VerifyAdminSession.Active {
		t.Fatal("verifyAdminSession: window not active after correct password")
	}
}

// graphqlBody marshals a GraphQL query + variables envelope. The Client's
// GraphQL helper omits variables, so fs journeys that bind variables build the
// envelope here and POST it themselves.
func graphqlBody(t *testing.T, query string, variables map[string]any) string {
	t.Helper()
	body, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}{Query: query, Variables: variables})
	if err != nil {
		t.Fatalf("graphqlBody: marshal: %v", err)
	}
	return string(body)
}

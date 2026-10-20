package apptest_test

import (
	"net/http"
	"testing"

	"github.com/beamfall/core/internal/apptest"
)

// seededManager returns a Client carrying the seeded dev-manager credentials
// (present in both the development and conformance seed worlds), the same
// headers internal/app/app_test.go uses to reach /api/manifest.
func seededManager(c *apptest.Client) *apptest.Client {
	return c.
		WithHeader("X-Beamfall-Session", "session-manager").
		WithHeader("X-Beamfall-Device", "device-mac").
		WithHeader("X-Beamfall-Device-Secret", "dev-manager-secret")
}

func TestBootServesManifest(t *testing.T) {
	h := apptest.Boot(t)
	resp := seededManager(h.Client).GET("/api/manifest")
	h.Assert.OK(resp)
	if resp.Body() == "" {
		t.Fatal("empty manifest body")
	}
}

func TestBootSeededServesManifest(t *testing.T) {
	h := apptest.BootSeeded(t)
	resp := seededManager(h.Client).GET("/api/manifest")
	h.Assert.OK(resp)
}

func TestBootGraphQLAnswers(t *testing.T) {
	h := apptest.Boot(t)
	resp := seededManager(h.Client).GraphQL("{ profiles { id } }")
	h.Assert.OK(resp)
	var body struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	resp.JSON(&body)
	if len(body.Errors) != 0 {
		t.Fatalf("graphql errors: %+v", body.Errors)
	}
}

func TestBootUnauthenticatedManifestIsRejected(t *testing.T) {
	h := apptest.Boot(t)
	resp := h.Client.GET("/api/manifest")
	h.Assert.Status(resp, http.StatusUnauthorized)
}

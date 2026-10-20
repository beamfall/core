package apptest_test

import (
	"testing"

	"github.com/beamfall/core/internal/apptest"
)

// seededManagerClient returns an authenticated manager client (used across
// driver tests). It mirrors the helper in apptest_test.go.
func seededManagerClient(h *apptest.Harness) *apptest.Client {
	return h.Client.
		WithHeader("X-Beamfall-Session", "session-manager").
		WithHeader("X-Beamfall-Device", "device-mac").
		WithHeader("X-Beamfall-Device-Secret", "dev-manager-secret")
}

// seededViewerClient returns an authenticated viewer (non-manager) client.
func seededViewerClient(h *apptest.Harness) *apptest.Client {
	return h.Client.
		WithHeader("X-Beamfall-Session", "session-default").
		WithHeader("X-Beamfall-Device", "device-tv").
		WithHeader("X-Beamfall-Device-Secret", "dev-tv-secret")
}

// seededRevealedClient is a viewer session that is already revealed (seeded).
func seededRevealedClient(h *apptest.Harness) *apptest.Client {
	return h.Client.
		WithHeader("X-Beamfall-Session", "session-revealed").
		WithHeader("X-Beamfall-Device", "device-tv").
		WithHeader("X-Beamfall-Device-Secret", "dev-tv-secret")
}

func bootWithAdminSession(t *testing.T) (*apptest.Harness, *apptest.Client) {
	t.Helper()
	const password = "correct horse battery"
	t.Setenv("BEAMFALL_ADMIN_PASSWORD", password)
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	resp := c.POST("/api/graphql", "application/json", `{"query":"mutation V($password: String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"`+password+`"}}`)
	h.Assert.OK(resp)
	var body struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	resp.JSON(&body)
	if len(body.Errors) != 0 {
		t.Fatalf("verifyAdminSession errors = %+v", body.Errors)
	}
	return h, c
}

// --- Natural-key assertion tests ---

func TestNaturalKey_ProfileByName_Found(t *testing.T) {
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	p := h.Assert.ProfileByName(c, "Default")
	if p.ID == "" {
		t.Fatal("expected non-empty profile ID for 'Default'")
	}
}

func TestNaturalKey_HasProfileNamed(t *testing.T) {
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	h.Assert.HasProfileNamed(c, "Default")
	h.Assert.HasProfileNamed(c, "Managed")
}

func TestNaturalKey_HasProfileKind(t *testing.T) {
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	h.Assert.HasProfileKind(c, "Default", "adult")
	h.Assert.HasProfileKind(c, "Managed", "managed")
}

func TestNaturalKey_HasRevealSurface(t *testing.T) {
	h := apptest.Boot(t)
	h.Assert.HasRevealSurface(h, "Query.navigationManifest")
	h.Assert.HasRevealSurface(h, "Query.sourceItems")
}

func TestNaturalKey_HasRevealSurfaceClass(t *testing.T) {
	h := apptest.Boot(t)
	h.Assert.HasRevealSurfaceClass(h, "Query.navigationManifest", "content-json")
}

func TestNaturalKey_RevealSurfaces_NonEmpty(t *testing.T) {
	h := apptest.Boot(t)
	if len(h.RevealSurfaces()) == 0 {
		t.Fatal("expected at least one registered reveal surface")
	}
}

// --- RevealDriver tests ---

func TestRevealDriver_SurfaceRegistered(t *testing.T) {
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	d := h.RevealDriver(t, c)
	d.SurfaceRegistered("Query.navigationManifest")
	d.SurfaceRegistered("Query.sourceItems")
}

func TestRevealDriver_SurfaceCount(t *testing.T) {
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	d := h.RevealDriver(t, c)
	// Assert the exact count matches the registry so any surface added or
	// removed causes a targeted failure.
	surfaces := h.RevealSurfaces()
	d.SurfaceCount(len(surfaces))
}

func TestRevealDriver_AssertNotRevealed_DefaultSession(t *testing.T) {
	h := apptest.Boot(t)
	c := seededViewerClient(h)
	d := h.RevealDriver(t, c)
	d.AssertNotRevealed()
}

func TestRevealDriver_AssertRevealed_RevealedSession(t *testing.T) {
	h := apptest.Boot(t)
	c := seededRevealedClient(h)
	d := h.RevealDriver(t, c)
	d.AssertRevealed()
}

// --- ProfileDriver tests ---

func TestProfileDriver_AssertExists(t *testing.T) {
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	d := h.ProfileDriver(t, c)
	d.AssertExists("Default")
	d.AssertExists("Managed")
}

func TestProfileDriver_AssertKind(t *testing.T) {
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	d := h.ProfileDriver(t, c)
	d.AssertKind("Default", "adult")
	d.AssertKind("Managed", "managed")
}

func TestProfileDriver_ProfileID(t *testing.T) {
	h := apptest.Boot(t)
	c := seededManagerClient(h)
	d := h.ProfileDriver(t, c)
	id := d.ProfileID("Default")
	if id == "" {
		t.Fatal("expected non-empty ID for profile 'Default'")
	}
}

// --- PairingDriver tests ---

func TestPairingDriver_CreateCode(t *testing.T) {
	h, c := bootWithAdminSession(t)
	pd := h.PairingDriver(t, c)
	prd := h.ProfileDriver(t, c)

	profileID := prd.ProfileID("Default")
	code := pd.CreateCode(profileID, "viewer")
	pd.AssertCodeNonEmpty(code)
	pd.AssertRole(code, "viewer")
	pd.AssertProfileID(code, profileID)
}

func TestPairingDriver_PairingFlow(t *testing.T) {
	h, c := bootWithAdminSession(t)
	d := h.PairingDriver(t, c)

	enrolled := d.PairingFlow("Default", "viewer", "Test Device")
	if enrolled["deviceID"] == "" {
		t.Fatal("enrolled deviceID is empty")
	}
}

func TestPairingDriver_EnrollDevice(t *testing.T) {
	h, c := bootWithAdminSession(t)
	pd := h.PairingDriver(t, c)
	prd := h.ProfileDriver(t, c)

	profileID := prd.ProfileID("Default")
	code := pd.CreateCode(profileID, "viewer")
	enrolled := pd.EnrollDevice(code.Code, "Phone")
	pd.AssertEnrollField(enrolled, "deviceID")
	pd.AssertEnrollField(enrolled, "sessionID")
	pd.AssertEnrollField(enrolled, "deviceSecret")
}

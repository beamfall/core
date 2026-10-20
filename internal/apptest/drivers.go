package apptest

import (
	"encoding/json"
	"net/http"
	"testing"
)

// RevealDriver drives reveal-flow test sequences. Acquire one with
// Harness.RevealDriver; pass the authenticated client for the session under
// test.
type RevealDriver struct {
	t       testing.TB
	harness *Harness
	client  *Client
}

// RevealDriver returns a flow driver scoped to client for reveal assertions.
// The supplied client must carry session headers (use Client.WithHeader).
func (h *Harness) RevealDriver(t testing.TB, client *Client) *RevealDriver {
	t.Helper()
	return &RevealDriver{t: t, harness: h, client: client}
}

// SurfaceRegistered asserts that surfaceID appears in the app's reveal surface
// registry — i.e. the surface participates in the Reveal contract.
func (d *RevealDriver) SurfaceRegistered(surfaceID string) {
	d.t.Helper()
	d.harness.Assert.HasRevealSurface(d.harness, surfaceID)
}

// SurfaceCount asserts that exactly n surfaces are registered.
func (d *RevealDriver) SurfaceCount(n int) {
	d.t.Helper()
	got := len(d.harness.RevealSurfaces())
	if got != n {
		d.t.Fatalf("apptest: reveal surface count = %d, want %d", got, n)
	}
}

// CurrentSession fetches and returns the current session info via GraphQL.
// It fails the test on a non-OK response or decode error.
func (d *RevealDriver) CurrentSession() map[string]any {
	d.t.Helper()
	resp := d.client.GraphQL(`{ currentSession { id profileID profile revealed deviceRole } }`)
	d.harness.Assert.OK(resp)
	var body struct {
		Data struct {
			CurrentSession map[string]any `json:"currentSession"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	resp.JSON(&body)
	if len(body.Errors) != 0 {
		d.t.Fatalf("apptest: currentSession graphql errors: %+v", body.Errors)
	}
	return body.Data.CurrentSession
}

// AssertRevealed fails unless the current session reports revealed == true.
func (d *RevealDriver) AssertRevealed() {
	d.t.Helper()
	sess := d.CurrentSession()
	revealed, _ := sess["revealed"].(bool)
	if !revealed {
		d.t.Fatalf("apptest: session revealed = false, want true")
	}
}

// AssertNotRevealed fails unless the current session reports revealed == false.
func (d *RevealDriver) AssertNotRevealed() {
	d.t.Helper()
	sess := d.CurrentSession()
	revealed, _ := sess["revealed"].(bool)
	if revealed {
		d.t.Fatalf("apptest: session revealed = true, want false")
	}
}

// ProfileDriver drives profile-flow test sequences. Acquire one with
// Harness.ProfileDriver; pass the authenticated client for the session.
type ProfileDriver struct {
	t       testing.TB
	harness *Harness
	client  *Client
}

// ProfileDriver returns a flow driver scoped to client for profile assertions.
func (h *Harness) ProfileDriver(t testing.TB, client *Client) *ProfileDriver {
	t.Helper()
	return &ProfileDriver{t: t, harness: h, client: client}
}

// AssertExists fails unless a profile named name is listed (natural-key assert).
func (d *ProfileDriver) AssertExists(name string) {
	d.t.Helper()
	d.harness.Assert.HasProfileNamed(d.client, name)
}

// AssertKind fails unless the profile named name has the given kind.
func (d *ProfileDriver) AssertKind(name, kind string) {
	d.t.Helper()
	d.harness.Assert.HasProfileKind(d.client, name, kind)
}

// ProfileID returns the opaque server-side ID for the profile named name,
// failing the test if not found. Useful when a downstream call needs the ID
// but the test author wants to drive by human-readable name.
func (d *ProfileDriver) ProfileID(name string) string {
	d.t.Helper()
	return d.harness.Assert.ProfileByName(d.client, name).ID
}

// PairingDriver drives device-pairing flow test sequences. Acquire one with
// Harness.PairingDriver; pass an authenticated manager client.
type PairingDriver struct {
	t       testing.TB
	harness *Harness
	client  *Client
}

// PairingDriver returns a flow driver for pairing operations. client must
// carry manager-device credentials; pairing-code creation is forbidden to
// non-manager devices.
func (h *Harness) PairingDriver(t testing.TB, client *Client) *PairingDriver {
	t.Helper()
	return &PairingDriver{t: t, harness: h, client: client}
}

// PairingCodeResponse is the decoded body of a POST /api/pairing-codes response.
type PairingCodeResponse struct {
	Code       string `json:"code"`
	ExpiresAt  string `json:"expiresAt"`
	ProfileID  string `json:"profileID"`
	DeviceRole string `json:"deviceRole"`
}

// CreateCode POSTs to /api/pairing-codes with the given profileID and role
// ("viewer" or "manager"), asserts a 200 or 201 response, and returns the
// decoded code response. It fails the test on any HTTP or decode error.
func (d *PairingDriver) CreateCode(profileID, deviceRole string) PairingCodeResponse {
	d.t.Helper()
	body, err := json.Marshal(map[string]string{
		"profileID":  profileID,
		"deviceRole": deviceRole,
	})
	if err != nil {
		d.t.Fatalf("apptest: marshal pairing-code request: %v", err)
	}
	resp := d.client.POST("/api/pairing-codes", "application/json", string(body))
	// The server returns 201 Created on a successful code mint.
	if resp.Status != http.StatusOK && resp.Status != http.StatusCreated {
		d.t.Fatalf("apptest: pairing-codes returned status %d, want 200 or 201\n%s", resp.Status, resp.Body())
	}

	var code PairingCodeResponse
	resp.JSON(&code)
	return code
}

// AssertCodeNonEmpty fails unless the code string in resp is non-empty.
func (d *PairingDriver) AssertCodeNonEmpty(resp PairingCodeResponse) {
	d.t.Helper()
	if resp.Code == "" {
		d.t.Fatal("apptest: pairing code is empty")
	}
}

// AssertRole fails unless resp.DeviceRole equals role.
func (d *PairingDriver) AssertRole(resp PairingCodeResponse, role string) {
	d.t.Helper()
	if resp.DeviceRole != role {
		d.t.Fatalf("apptest: pairing code deviceRole = %q, want %q", resp.DeviceRole, role)
	}
}

// AssertProfileID fails unless resp.ProfileID equals profileID.
func (d *PairingDriver) AssertProfileID(resp PairingCodeResponse, profileID string) {
	d.t.Helper()
	if resp.ProfileID != profileID {
		d.t.Fatalf("apptest: pairing code profileID = %q, want %q", resp.ProfileID, profileID)
	}
}

// EnrollDevice redeems code by POST /api/enroll with the given deviceName,
// asserts a 200 or 201 response, and returns the decoded enroll response.
func (d *PairingDriver) EnrollDevice(code, deviceName string) map[string]any {
	d.t.Helper()
	body, err := json.Marshal(map[string]string{
		"code":       code,
		"deviceName": deviceName,
	})
	if err != nil {
		d.t.Fatalf("apptest: marshal enroll request: %v", err)
	}
	resp := d.client.POST("/api/enroll", "application/json", string(body))
	if resp.Status != http.StatusOK && resp.Status != http.StatusCreated {
		d.t.Fatalf("apptest: enroll returned status %d, want 200 or 201\n%s", resp.Status, resp.Body())
	}
	var enrolled map[string]any
	resp.JSON(&enrolled)
	return enrolled
}

// AssertEnrollField fails unless the enrollment response contains field with a
// non-empty string value. Useful for asserting deviceID, sessionID, etc.
func (d *PairingDriver) AssertEnrollField(enrolled map[string]any, field string) {
	d.t.Helper()
	v, ok := enrolled[field]
	if !ok {
		d.t.Fatalf("apptest: enroll response missing field %q", field)
	}
	s, _ := v.(string)
	if s == "" {
		d.t.Fatalf("apptest: enroll response field %q is empty (got %v)", field, v)
	}
}

// PairingFlow is a convenience that runs the full CreateCode → EnrollDevice
// round-trip and returns the enrollment result. It asserts along the way:
// code is non-empty, role matches, enrollment returns the expected fields.
// profileName is resolved to its server ID via a GraphQL profiles lookup.
func (d *PairingDriver) PairingFlow(profileName, deviceRole, deviceName string) map[string]any {
	d.t.Helper()

	pd := d.harness.ProfileDriver(d.t, d.client)
	profileID := pd.ProfileID(profileName)

	code := d.CreateCode(profileID, deviceRole)
	d.AssertCodeNonEmpty(code)
	d.AssertRole(code, deviceRole)
	d.AssertProfileID(code, profileID)

	enrolled := d.EnrollDevice(code.Code, deviceName)
	for _, f := range []string{"deviceID", "sessionID", "deviceSecret"} {
		d.AssertEnrollField(enrolled, f)
	}
	return enrolled
}

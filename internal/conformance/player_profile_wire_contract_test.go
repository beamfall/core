package conformance

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/graph"
)

// TestPlayerProfileWireContract keeps contracts/player-profile/v1/fixtures.json
// in lockstep with core's Player Profile response types (ADR-0240 D11). Each
// fixture must decode strictly into its Go type and re-encode byte-identically,
// so a renamed, added or dropped field fails here before a client SDK sees it.
func TestPlayerProfileWireContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts/player-profile/v1/fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ContractVersion string   `json:"contractVersion"`
		Capability      string   `json:"capability"`
		Presence        []string `json:"presence"`
		Fixtures        []struct {
			Name  string          `json:"name"`
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"fixtures"`
	}
	strictDecode(t, "fixtures.json", data, &doc)
	if doc.ContractVersion != "1.0.0" {
		t.Fatalf("contractVersion = %q, want 1.0.0", doc.ContractVersion)
	}
	if doc.Capability != string(capabilities.CapabilityPlayerControl) {
		t.Fatalf("capability = %q, want %q", doc.Capability, capabilities.CapabilityPlayerControl)
	}
	if want := []string{"offline", "ready", "playing"}; !slices.Equal(doc.Presence, want) {
		t.Fatalf("presence = %v, want %v", doc.Presence, want)
	}

	types := map[string]func() any{
		"PlayerBindingInfo":            func() any { return new(graph.PlayerBindingInfo) },
		"PlayerControllersInfo":        func() any { return new(graph.PlayerControllersInfo) },
		"PlayerDisplayInfo":            func() any { return new(graph.PlayerDisplayInfo) },
		"PartyAvailableDisplayInfo":    func() any { return new(graph.PartyAvailableDisplayInfo) },
		"PartyScreenInfo":              func() any { return new(graph.PartyScreenInfo) },
		"ControlRendererSessionResult": func() any { return new(graph.ControlRendererSessionResult) },
	}
	covered := map[string]bool{}
	for _, f := range doc.Fixtures {
		newValue, ok := types[f.Type]
		if !ok {
			t.Fatalf("%s: unknown type %q", f.Name, f.Type)
		}
		covered[f.Type] = true
		value := newValue()
		strictDecode(t, f.Name, f.Value, value)
		got, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s: marshal: %v", f.Name, err)
		}
		var want bytes.Buffer
		if err := json.Compact(&want, f.Value); err != nil {
			t.Fatalf("%s: compact: %v", f.Name, err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Fatalf("%s drifted from %s:\n got  %s\n want %s", f.Name, f.Type, got, want.Bytes())
		}
		if display, ok := value.(*graph.PlayerDisplayInfo); ok && !slices.Contains(doc.Presence, display.Presence) {
			t.Fatalf("%s: presence %q outside the vocabulary", f.Name, display.Presence)
		}
	}
	for name := range types {
		if !covered[name] {
			t.Fatalf("no fixture covers %s", name)
		}
	}
}

func strictDecode(t *testing.T, name string, data []byte, into any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: strict decode: %v", name, err)
	}
}

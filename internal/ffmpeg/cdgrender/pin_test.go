package cdgrender

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// Pin is the release record of the embedded presenter (pin.json).
type pin struct {
	Release string `json:"release"`
	Rev     string `json:"rev"`
	License string `json:"license"`
	Source  string `json:"source"`
	Assets  map[string]struct {
		SHA256 string `json:"sha256"`
	} `json:"assets"`
}

// TestEmbeddedModuleMatchesItsPin proves the embedded module is the pinned
// Oscillux release asset, byte for byte.
func TestEmbeddedModuleMatchesItsPin(t *testing.T) {
	raw, err := os.ReadFile("pin.json")
	if err != nil {
		t.Fatal(err)
	}
	var p pin
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.License != "AGPL-3.0-only" || p.Source != "https://github.com/beamfall/oscillux/releases/tag/"+p.Release || len(p.Rev) != 40 {
		t.Fatalf("pin.json does not name an Oscillux release: %+v", p)
	}
	asset, ok := p.Assets["oscillux-cdg-render.wasm"]
	if !ok {
		t.Fatal("pin.json has no oscillux-cdg-render.wasm asset")
	}
	sum := sha256.Sum256(module)
	if got := hex.EncodeToString(sum[:]); got != asset.SHA256 {
		t.Fatalf("embedded module sha256 %s, pinned %s", got, asset.SHA256)
	}
}

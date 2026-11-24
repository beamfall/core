package conformance

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
	setOptional := map[string]bool{}
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
		markSetOptionalFields(reflect.ValueOf(value), setOptional)
		if display, ok := value.(*graph.PlayerDisplayInfo); ok && !slices.Contains(doc.Presence, display.Presence) {
			t.Fatalf("%s: presence %q outside the vocabulary", f.Name, display.Presence)
		}
	}
	for name := range types {
		if !covered[name] {
			t.Fatalf("no fixture covers %s", name)
		}
	}
	// Every omitempty field must appear in at least one fixture, or a client
	// decoder can mis-type or drop it while the round trip above stays green.
	for _, newValue := range types {
		for _, field := range optionalFields(reflect.TypeOf(newValue()).Elem()) {
			if !setOptional[field] {
				t.Errorf("no fixture sets optional field %s", field)
			}
		}
	}
}

// optionalFields lists the omitempty JSON fields reachable from typ as
// "Type.field" keys, descending into nested structs, slices and pointers.
func optionalFields(typ reflect.Type) []string {
	var out []string
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := range rt.NumField() {
			f := rt.Field(i)
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" || !f.IsExported() {
				continue
			}
			if strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero") {
				out = append(out, rt.Name()+"."+name)
			}
			walk(f.Type)
		}
	}
	walk(typ)
	return out
}

// markSetOptionalFields records every non-zero JSON field in v, nested values included.
func markSetOptionalFields(v reflect.Value, set map[string]bool) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice:
		for i := range v.Len() {
			markSetOptionalFields(v.Index(i), set)
		}
	case reflect.Struct:
		rt := v.Type()
		for i := range rt.NumField() {
			f := rt.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" || !f.IsExported() {
				continue
			}
			if !v.Field(i).IsZero() {
				set[rt.Name()+"."+name] = true
			}
			markSetOptionalFields(v.Field(i), set)
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

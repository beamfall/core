package capabilities

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapabilityManifestFixtureValidates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Namespace != ManifestNamespaceFlat {
		t.Fatalf("namespace = %q, want flat", manifest.Namespace)
	}
}

func TestCompanionKeyingFixtureUsesManifestIDs(t *testing.T) {
	manifestData, err := os.ReadFile(filepath.Join("testdata", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifest(manifestData)
	if err != nil {
		t.Fatal(err)
	}
	keyingData, err := os.ReadFile(filepath.Join("testdata", "companion-keying.json"))
	if err != nil {
		t.Fatal(err)
	}
	var keying struct {
		Namespace string   `json:"namespace"`
		KeyField  string   `json:"keyField"`
		Keys      []string `json:"keys"`
	}
	if err := json.Unmarshal(keyingData, &keying); err != nil {
		t.Fatal(err)
	}
	if keying.Namespace != manifest.Namespace || keying.KeyField != "id" {
		t.Fatalf("Companion keying contract = %#v, want flat manifest id", keying)
	}
	if len(keying.Keys) != len(manifest.Capabilities) {
		t.Fatalf("Companion keys = %v, manifest = %v", keying.Keys, manifest.Capabilities)
	}
	for i, entry := range manifest.Capabilities {
		if keying.Keys[i] != entry.ID {
			t.Fatalf("Companion key %q does not use manifest ID %q", keying.Keys[i], entry.ID)
		}
	}
}

func TestCapabilityManifestRejectsDuplicateIDFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "manifest-duplicate-id.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseManifest(data)
	if err == nil || !strings.Contains(err.Error(), "duplicate ID") {
		t.Fatalf("duplicate-id fixture returned %v", err)
	}
}

func TestRuntimeDescriptorsSatisfyFlatManifest(t *testing.T) {
	states := Resolve(nil)
	entries := make([]ManifestEntry, 0, len(states))
	for _, state := range states {
		entries = append(entries, ManifestEntry{ID: state.ID, Version: state.Version})
	}
	if _, err := NewManifest(entries, nil); err != nil {
		t.Fatal(err)
	}
}

// feature:capability-manifest
func TestCapabilityManifestBuildTagsRoundTrip(t *testing.T) {
	buildTags := []string{"release", "beamfall_no_powertools"}
	manifest, err := NewManifest(
		[]ManifestEntry{{ID: "example-capability", Version: 1}},
		buildTags,
	)
	if err != nil {
		t.Fatal(err)
	}

	buildTags[0] = "mutated"
	wantBuildTags := []string{"beamfall_no_powertools", "release"}
	if len(manifest.BuildTags) != len(wantBuildTags) {
		t.Fatalf("build tags = %v, want %v", manifest.BuildTags, wantBuildTags)
	}
	for i := range wantBuildTags {
		if manifest.BuildTags[i] != wantBuildTags[i] {
			t.Fatalf("build tags = %v, want %v", manifest.BuildTags, wantBuildTags)
		}
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.BuildTags) != len(wantBuildTags) {
		t.Fatalf("parsed build tags = %v, want %v", parsed.BuildTags, wantBuildTags)
	}
	for i := range wantBuildTags {
		if parsed.BuildTags[i] != wantBuildTags[i] {
			t.Fatalf("parsed build tags = %v, want %v", parsed.BuildTags, wantBuildTags)
		}
	}
}

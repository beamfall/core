package capabilities

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	ManifestSchemaVersion = 1
	ManifestNamespaceFlat = "flat"
	CapabilityVersion     = 1
)

var manifestIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ManifestEntry is one stable capability identity carried by a plugin package.
// Version is an integer so clients can compare it without parsing build labels.
type ManifestEntry struct {
	ID      string `json:"id"`
	Version uint   `json:"version"`
}

// Manifest is the capability discovery contract used by `beamfall
// capabilities` and Companion activation. Namespace "flat" means Domain
// plugin, Workflow, Integration, and host-probe IDs share one global registry;
// tier prefixes are not part of identity.
type Manifest struct {
	SchemaVersion uint            `json:"schemaVersion"`
	Namespace     string          `json:"namespace"`
	BuildTags     []string        `json:"buildTags"`
	Capabilities  []ManifestEntry `json:"capabilities"`
}

// NewManifest builds a deterministic flat-registry manifest.
func NewManifest(entries []ManifestEntry, buildTags []string) (Manifest, error) {
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Namespace:     ManifestNamespaceFlat,
		BuildTags:     append([]string{}, buildTags...),
		Capabilities:  append([]ManifestEntry{}, entries...),
	}
	sort.Strings(manifest.BuildTags)
	sort.Slice(manifest.Capabilities, func(i, j int) bool {
		return manifest.Capabilities[i].ID < manifest.Capabilities[j].ID
	})
	return manifest, ValidateManifest(manifest)
}

// ParseManifest strictly decodes and validates one capability manifest fixture.
func ParseManifest(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("capability manifest: decode: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("capability manifest: trailing JSON document")
		}
		return Manifest{}, fmt.Errorf("capability manifest: trailing data: %w", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// ValidateManifest validates the schema discriminator and flat ID registry.
func ValidateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("capability manifest: schemaVersion %d, want %d", manifest.SchemaVersion, ManifestSchemaVersion)
	}
	if manifest.Namespace != ManifestNamespaceFlat {
		return fmt.Errorf("capability manifest: namespace %q, want %q", manifest.Namespace, ManifestNamespaceFlat)
	}
	if err := ValidateManifestEntries(manifest.Capabilities); err != nil {
		return err
	}
	seenTags := map[string]bool{}
	for _, tag := range manifest.BuildTags {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("capability manifest: blank build tag")
		}
		if seenTags[tag] {
			return fmt.Errorf("capability manifest: duplicate build tag %q", tag)
		}
		seenTags[tag] = true
	}
	return nil
}

// ValidateManifestEntries enforces the global, tier-independent ID namespace.
func ValidateManifestEntries(entries []ManifestEntry) error {
	seen := map[string]bool{}
	for _, entry := range entries {
		if !manifestIDPattern.MatchString(entry.ID) {
			return fmt.Errorf("capability manifest: invalid ID %q", entry.ID)
		}
		if entry.Version == 0 {
			return fmt.Errorf("capability manifest: capability %q version must be positive", entry.ID)
		}
		if seen[entry.ID] {
			return fmt.Errorf("capability manifest: duplicate ID %q in flat namespace", entry.ID)
		}
		seen[entry.ID] = true
	}
	return nil
}

package plugin

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/beamfall/core/internal/capabilities"
	sdk "github.com/beamfall/plugin-sdk"
	"golang.org/x/mod/semver"
)

const (
	PackageSchemaVersion = 1
	TierDomain           = "domain"
	TierWorkflow         = "workflow"
	TierIntegration      = "integration"
	UpdatePinHash        = "hash"
	UpdatePinSigningKey  = "signing-key"
)

// PackageManifest is the shared packaging envelope for Domain, Workflow, and
// Integration plugins. Tier-specific runtime payloads remain in their existing
// contracts; this envelope standardizes the artifacts every package declares.
type PackageManifest struct {
	SchemaVersion uint                         `json:"schemaVersion"`
	ID            string                       `json:"id"`
	Tier          string                       `json:"tier"`
	Version       string                       `json:"version"`
	Capabilities  []capabilities.ManifestEntry `json:"capabilities"`
	Source        *SourceArtifact              `json:"source,omitempty"`
	UpdatePin     *UpdatePin                   `json:"updatePin,omitempty"`
	Permissions   []PermissionSpec             `json:"permissions,omitempty"`
	Outbound      []string                     `json:"outbound,omitempty"`
	Actions       []ActionSpec                 `json:"actions,omitempty"`
	ConfigFields  []ConfigFieldSpec            `json:"configFields,omitempty"`
	Status        []StatusFieldSpec            `json:"status,omitempty"`
}

// SourceArtifact binds a conveyed package to its source.json sidecar.
type SourceArtifact struct {
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
	OfferURL string `json:"offerURL"`
}

// UpdatePin binds a package update source by artifact hash or signing-key ID.
type UpdatePin struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// StatusFieldSpec is one typed field in the server-rendered plugin status.
type StatusFieldSpec struct {
	Name  string       `json:"name"`
	Type  string       `json:"type"`
	Label LocaleString `json:"label"`
}

// ParsePackageManifest decodes one strict JSON package artifact and validates it.
func ParsePackageManifest(data []byte) (PackageManifest, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return PackageManifest{}, fmt.Errorf("plugin package: decode: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest PackageManifest
	if err := decoder.Decode(&manifest); err != nil {
		return PackageManifest{}, fmt.Errorf("plugin package: decode: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return PackageManifest{}, err
	}
	if err := ValidatePackageManifest(manifest); err != nil {
		return PackageManifest{}, err
	}
	return manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("plugin package: trailing JSON document")
		}
		return fmt.Errorf("plugin package: trailing data: %w", err)
	}
	return nil
}

// ValidatePackageManifest applies the same packaging rules to every plugin tier.
func ValidatePackageManifest(manifest PackageManifest) error {
	if manifest.SchemaVersion != PackageSchemaVersion {
		return fmt.Errorf("plugin package: schemaVersion %d, want %d", manifest.SchemaVersion, PackageSchemaVersion)
	}
	if !slugPattern.MatchString(manifest.ID) {
		return fmt.Errorf("plugin package: invalid ID %q", manifest.ID)
	}
	if !semver.IsValid(normalizeSemver(manifest.Version)) {
		return fmt.Errorf("plugin package %s: version %q must be semver", manifest.ID, manifest.Version)
	}
	if manifest.Tier != TierDomain && manifest.Tier != TierWorkflow && manifest.Tier != TierIntegration {
		return fmt.Errorf("plugin package %s: unknown tier %q", manifest.ID, manifest.Tier)
	}
	if err := validatePackageCapabilities(manifest); err != nil {
		return err
	}
	if err := validatePackageDescriptors(manifest); err != nil {
		return err
	}
	if err := validatePackageOutbound(manifest); err != nil {
		return err
	}
	if err := validateSourceArtifact(manifest.Source); err != nil {
		return fmt.Errorf("plugin package %s: %w", manifest.ID, err)
	}
	if err := validateUpdatePin(manifest.UpdatePin); err != nil {
		return fmt.Errorf("plugin package %s: %w", manifest.ID, err)
	}
	return validateStatus(manifest)
}

func validatePackageCapabilities(manifest PackageManifest) error {
	if len(manifest.Capabilities) == 0 {
		return fmt.Errorf("plugin package %s: at least one capability is required", manifest.ID)
	}
	if err := capabilities.ValidateManifestEntries(manifest.Capabilities); err != nil {
		return fmt.Errorf("plugin package %s: %w", manifest.ID, err)
	}
	return nil
}

func validatePackageDescriptors(manifest PackageManifest) error {
	bundle := Bundle{
		ID:           ID(manifest.ID),
		Version:      manifest.Version,
		SDKFloor:     sdk.Version,
		SDKCeiling:   sdk.Version,
		DisplayName:  L("Package"),
		MediaLabel:   L("Media"),
		MediaClasses: []string{"video"},
		Layouts:      []LayoutSpec{{ID: "home", Role: "browse"}},
		ConfigFields: manifest.ConfigFields,
		Permissions:  manifest.Permissions,
		Actions:      manifest.Actions,
	}
	if err := Validate(bundle); err != nil {
		return fmt.Errorf("plugin package %s descriptors: %w", manifest.ID, err)
	}
	declared := map[string]bool{}
	for _, permission := range manifest.Permissions {
		declared[permission.Kind] = true
	}
	for _, action := range manifest.Actions {
		for _, permission := range action.RequiredPermissions {
			if !declared[permission] {
				return fmt.Errorf("plugin package %s: action %q requires undeclared permission %q", manifest.ID, action.ID, permission)
			}
		}
	}
	return nil
}

func validatePackageOutbound(manifest PackageManifest) error {
	if manifest.Tier == TierWorkflow && (len(manifest.Permissions) > 0 || len(manifest.Outbound) > 0) {
		return fmt.Errorf("plugin package %s: Workflow tier cannot declare Integration permissions or outbound endpoints", manifest.ID)
	}
	_, err := NewOutboundAllowlist([]OutboundDeclaration{{PluginID: ID(manifest.ID), Origins: manifest.Outbound}})
	return err
}

func validateSourceArtifact(source *SourceArtifact) error {
	if source == nil {
		return nil
	}
	if source.File != "source.json" {
		return fmt.Errorf("source artifact file %q, want source.json", source.File)
	}
	if !validSHA256(source.SHA256) {
		return fmt.Errorf("source artifact sha256 must be lowercase hex")
	}
	parsed, err := url.Parse(source.OfferURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("source artifact offerURL must be https")
	}
	return nil
}

func validateUpdatePin(pin *UpdatePin) error {
	if pin == nil {
		return nil
	}
	switch pin.Kind {
	case UpdatePinHash:
		if !validSHA256(pin.Value) {
			return fmt.Errorf("update hash pin must be lowercase sha256")
		}
	case UpdatePinSigningKey:
		if strings.TrimSpace(pin.Value) == "" {
			return fmt.Errorf("update signing-key pin is blank")
		}
	default:
		return fmt.Errorf("unknown update pin kind %q", pin.Kind)
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateStatus(manifest PackageManifest) error {
	seen := map[string]bool{}
	for _, field := range manifest.Status {
		if !slugPattern.MatchString(field.Name) {
			return fmt.Errorf("plugin package %s: invalid status field %q", manifest.ID, field.Name)
		}
		if !contains(FieldTypes(), field.Type) {
			return fmt.Errorf("plugin package %s: status field %q has unknown type %q", manifest.ID, field.Name, field.Type)
		}
		if strings.TrimSpace(field.Label.Resolve("")) == "" {
			return fmt.Errorf("plugin package %s: status field %q label is required", manifest.ID, field.Name)
		}
		if seen[field.Name] {
			return fmt.Errorf("plugin package %s: duplicate status field %q", manifest.ID, field.Name)
		}
		seen[field.Name] = true
	}
	return nil
}

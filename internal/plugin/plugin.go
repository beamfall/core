// Package plugin defines the declarative plugin contract: plugins
// contribute unbounded data against small fixed vocabularies — archetypes,
// artwork slots, section kinds, source kinds — and never schema types, query
// strings, or rendering code (CLAUDE.md hard invariant 3). Bundles are pure
// data; compiled hooks are a later, first-party-only layer.
package plugin

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	sdk "github.com/beamfall/plugin-sdk"
	"golang.org/x/mod/semver"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidateSource checks one Source reference against the fixed vocabulary
// (the one validator, ARCHITECTURE V-12).
func ValidateSource(source SourceSpec) error {
	if !contains(SourceKinds(), source.Kind) {
		return fmt.Errorf("unknown source kind %q", source.Kind)
	}
	if source.Kind == "entity-items" && source.EntityKind == "" {
		return fmt.Errorf("entity-items source requires an entity kind")
	}
	if source.Kind != "entity-items" && source.EntityKind != "" {
		return fmt.Errorf("source kind %q does not take an entity kind", source.Kind)
	}
	if source.Seed != "" && source.Kind != "random" {
		return fmt.Errorf("source kind %q does not take a seed (allowed on: random)", source.Kind)
	}
	if allowed, valued := ValuedSourceKinds()[source.Kind]; valued {
		if !contains(allowed, source.Value) {
			return fmt.Errorf("source kind %q requires a value in {%s}", source.Kind, strings.Join(allowed, ", "))
		}
	} else if source.Value != "" {
		return fmt.Errorf("source kind %q does not take a value", source.Kind)
	}
	if len(source.FieldFilters) > 0 && !contains(FilterableSourceKinds(), source.Kind) {
		return fmt.Errorf("source kind %q does not take field filters (allowed on: %s)", source.Kind, strings.Join(FilterableSourceKinds(), ", "))
	}
	for field, value := range source.FieldFilters {
		if !slugPattern.MatchString(field) {
			return fmt.Errorf("field filter key %q must be a lowercase slug", field)
		}
		if value == nil {
			return fmt.Errorf("field filter %q requires a value", field)
		}
	}
	if filter := source.EntityFilter; filter != nil {
		if !contains(FilterableSourceKinds(), source.Kind) {
			return fmt.Errorf("source kind %q does not take an entity filter (allowed on: %s)", source.Kind, strings.Join(FilterableSourceKinds(), ", "))
		}
		if !slugPattern.MatchString(filter.Kind) {
			return fmt.Errorf("entity filter kind %q must be a lowercase slug", filter.Kind)
		}
		if strings.TrimSpace(filter.Name) == "" {
			return fmt.Errorf("entity filter on kind %q requires a name", filter.Kind)
		}
	}
	return nil
}

// Validate checks a Bundle against the SDK-owned declarative validator.
func Validate(bundle Bundle) error {
	if bundle.Version == "" {
		bundle.Version = "0.0.0"
	}
	if err := sdk.ValidateBundle(bundle); err != nil {
		return err
	}
	if err := validateBundleCredentialRefs(bundle); err != nil {
		return err
	}
	for _, field := range bundle.ConfigFields {
		if field.Name == ReservedOutboundPolicyConfigKey {
			return fmt.Errorf("plugin %s: config field %q is reserved by Core", bundle.ID, field.Name)
		}
	}
	return validateSDKCompatibility(bundle)
}

func validateSDKCompatibility(bundle Bundle) error {
	if bundle.SDKFloor == "" {
		return fmt.Errorf("plugin %s: SDK floor is required", bundle.ID)
	}
	if bundle.SDKCeiling == "" {
		return fmt.Errorf("plugin %s: SDK ceiling is required", bundle.ID)
	}
	floor := normalizeSemver(bundle.SDKFloor)
	ceiling := normalizeSemver(bundle.SDKCeiling)
	if !semver.IsValid(floor) || !semver.IsValid(ceiling) {
		return fmt.Errorf("plugin %s: SDK range [%s, %s] must contain semver bounds", bundle.ID, bundle.SDKFloor, bundle.SDKCeiling)
	}
	if semver.Major(floor) != semver.Major(ceiling) {
		return fmt.Errorf("plugin %s: SDK range [%s, %s] must declare one supported major", bundle.ID, bundle.SDKFloor, bundle.SDKCeiling)
	}
	if !sdk.CompatibleWithSDK(bundle, sdk.Version) {
		return fmt.Errorf("plugin %s: SDK range [%s, %s] excludes embedded SDK %s", bundle.ID, bundle.SDKFloor, bundle.SDKCeiling, sdk.Version)
	}
	return nil
}

func normalizeSemver(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

// Registry holds the activated bundles for one App instance.
type Registry struct {
	bundles  map[ID]Bundle
	standing map[ID]PluginStanding
	order    []ID
}

// NewRegistry validates and activates unsigned bundles from the generic
// sideload path. Callers cannot acquire first-party standing through Bundle
// contents.
func NewRegistry(bundles []Bundle) (*Registry, error) {
	return newRegistry(bundles, loadPathSideloaded)
}

// NewBuiltinRegistry validates and activates bundles compiled into the server
// at its executable composition root.
func NewBuiltinRegistry(bundles []Bundle) (*Registry, error) {
	return newRegistry(bundles, loadPathBuiltin)
}

// NewInventoryRegistry validates and activates the inventory-derived bundle
// set. Bundles whose ID is in executableIDs keep builtin standing; every other
// bundle receives only the loader-owned standing recorded when it was staged.
func NewInventoryRegistry(bundles []Bundle, executableIDs map[ID]bool, inventoryStandings map[ID]PluginStanding) (*Registry, error) {
	registry, err := newRegistryWithPaths(bundles, func(id ID) pluginLoadPath {
		if executableIDs[id] {
			return loadPathBuiltin
		}
		return loadPathSideloaded
	})
	if err != nil {
		return nil, err
	}
	for _, bundle := range bundles {
		if executableIDs[bundle.ID] {
			continue
		}
		standing, ok := inventoryStandings[bundle.ID]
		if !ok {
			return nil, fmt.Errorf("plugin %s: inventory standing is missing", bundle.ID)
		}
		registry.standing[bundle.ID] = standing
	}
	return registry, nil
}

// normalizedBundleVersions applies Validate's missing-version rule to a bundle
// set: an executable-owned bundle may omit Version, and Core reads that as
// 0.0.0. Bundles decoded from a distributed artifact are validated strictly at
// decode time, so this never relaxes the marketplace semver requirement.
func normalizedBundleVersions(bundles []Bundle) []Bundle {
	normalized := append([]Bundle(nil), bundles...)
	for i := range normalized {
		if normalized[i].Version == "" {
			normalized[i].Version = "0.0.0"
		}
	}
	return normalized
}

func newRegistry(bundles []Bundle, path pluginLoadPath) (*Registry, error) {
	return newRegistryWithPaths(bundles, func(ID) pluginLoadPath { return path })
}

func newRegistryWithPaths(bundles []Bundle, pathFor func(ID) pluginLoadPath) (*Registry, error) {
	// Per-bundle validation only, as on the builtin path: the SDK's cross-bundle
	// set rules (global entity-kind namespace) are enforced where a bundle set is
	// actually composed from distributed artifacts — see
	// validateInventoryAdditions. Applying them here would retroactively reject
	// the executable-owned set Core ships.
	normalized := normalizedBundleVersions(bundles)
	r := &Registry{
		bundles:  map[ID]Bundle{},
		standing: map[ID]PluginStanding{},
	}
	for _, bundle := range normalized {
		if err := Validate(bundle); err != nil {
			return nil, err
		}
		if _, exists := r.bundles[bundle.ID]; exists {
			return nil, fmt.Errorf("duplicate plugin ID %q", bundle.ID)
		}
		owned, err := cloneBundle(bundle)
		if err != nil {
			return nil, fmt.Errorf("clone plugin %q: %w", bundle.ID, err)
		}
		scopeBundleCredentialRefs(&owned)
		trust := AssessPluginTrust(nil, nil, PluginTrustRoots{}, nil)
		r.bundles[bundle.ID] = owned
		r.standing[bundle.ID] = PluginStanding{
			Provenance: derivePluginProvenance(pathFor(bundle.ID), trust.State),
			TrustState: trust.State,
		}
		r.order = append(r.order, bundle.ID)
	}
	return r, nil
}

// Bundle returns an activated bundle by ID.
func (r *Registry) Bundle(id ID) (Bundle, bool) {
	if r == nil {
		return Bundle{}, false
	}
	bundle, ok := r.bundles[id]
	if !ok {
		return Bundle{}, false
	}
	cloned, err := cloneBundle(bundle)
	if err != nil {
		return Bundle{}, false
	}
	return cloned, true
}

// Standing returns loader-owned authorship and integrity standing for one
// activated plugin.
func (r *Registry) Standing(id ID) (PluginStanding, bool) {
	if r == nil {
		return PluginStanding{}, false
	}
	standing, ok := r.standing[id]
	return standing, ok
}

func cloneBundle(bundle Bundle) (Bundle, error) {
	data, err := json.Marshal(bundle)
	if err != nil {
		return Bundle{}, err
	}
	var cloned Bundle
	if err := json.Unmarshal(data, &cloned); err != nil {
		return Bundle{}, err
	}
	return cloned, nil
}

// IDs returns the activated bundle IDs in activation order.
func (r *Registry) IDs() []ID {
	if r == nil {
		return nil
	}
	return append([]ID(nil), r.order...)
}

// FeatureEligiblePluginIDs returns activated plugin IDs that declare one
// feature-eligibility capability.
func (r *Registry) FeatureEligiblePluginIDs(capability string) []ID {
	if r == nil || capability == "" {
		return nil
	}
	var ids []ID
	for _, id := range r.order {
		if contains(r.bundles[id].FeatureEligibility, capability) {
			ids = append(ids, id)
		}
	}
	return ids
}

// InteractiveSidecarExts returns the union of every activated bundle's declared
// interactive-sidecar extensions, lowercased and de-duplicated.
// Scan recognises a sibling file with one of these extensions as an interactive
// sidecar track. Empty when no activated plugin declares any.
func (r *Registry) InteractiveSidecarExts() []string {
	if r == nil {
		return nil
	}
	seen := map[string]bool{}
	var exts []string
	for _, id := range r.order {
		for _, ext := range r.bundles[id].InteractiveSidecars {
			ext = strings.ToLower(strings.TrimPrefix(ext, "."))
			if ext == "" || seen[ext] {
				continue
			}
			seen[ext] = true
			exts = append(exts, ext)
		}
	}
	return exts
}

// HasEngagementEventKind reports whether any activated bundle declares the given
// engagement-event kind (Engagement.EventKinds). It is the validation Core uses
// to accept a client-supplied kind on the generic engagement-event surface
// without ever hardcoding a kind string: a kind is recordable only if a plugin
// declared it, so the kind vocabulary stays plugin-owned (invariant #1). A blank
// kind, or one no activated plugin declares, returns false.
func (r *Registry) HasEngagementEventKind(kind string) bool {
	if r == nil || kind == "" {
		return false
	}
	for _, id := range r.order {
		if contains(r.bundles[id].Engagement.EventKinds, kind) {
			return true
		}
	}
	return false
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	sdk "github.com/beamfall/plugin-sdk"
	"github.com/beamfall/plugin-sdk/catalogindex"
)

const inventorySchemaVersion = 1

var (
	ErrInventoryConfirmation = errors.New("plugin inventory requires explicit confirmation")
	ErrInventoryIdentity     = errors.New("plugin inventory identity mismatch")
	ErrInventoryIncompatible = errors.New("plugin inventory SDK incompatible")
)

type BundleOrigin string

const (
	BundleOriginRegistry   BundleOrigin = "registry"
	BundleOriginSideloaded BundleOrigin = "sideloaded"
)

type StageConfirmation struct {
	Origin         BundleOrigin
	PluginID       ID
	Version        string
	ArtifactSHA256 string
}

type StageBundleRequest struct {
	Artifact            []byte
	Envelope            []byte
	Roots               PluginTrustRoots
	RevokedAuthorKeyIDs []string
	Catalog             *catalogindex.Entry
	Origin              BundleOrigin
	Confirmation        *StageConfirmation
}

type InventoryEntry struct {
	Artifact       []byte                `json:"artifact"`
	Envelope       []byte                `json:"envelope,omitempty"`
	ArtifactSHA256 string                `json:"artifact_sha256"`
	Trust          PluginTrustAssessment `json:"trust"`
	Provenance     PluginProvenance      `json:"provenance"`
	Origin         BundleOrigin          `json:"origin"`
	Catalog        *catalogindex.Entry   `json:"catalog,omitempty"`
}

type inventoryState struct {
	SchemaVersion int              `json:"schema_version"`
	Entries       []InventoryEntry `json:"entries"`
}

type Inventory struct {
	dir        string
	executable []Bundle
	active     inventoryState
}

func OpenInventory(dir string, executable []Bundle) (*Inventory, error) {
	state, _, err := readInventoryState(filepath.Join(dir, "last-good.json"))
	if err != nil {
		return nil, fmt.Errorf("load last-good plugin inventory: %w", err)
	}
	inventory := &Inventory{
		dir:        dir,
		executable: append([]Bundle(nil), executable...),
		active:     state,
	}
	if _, err := inventory.validate(state); err != nil {
		return nil, fmt.Errorf("validate last-good plugin inventory: %w", err)
	}
	return inventory, nil
}

func (i *Inventory) Stage(req StageBundleRequest) (InventoryEntry, error) {
	bundle, err := decodeInventoryBundle(req.Artifact)
	if err != nil {
		return InventoryEntry{}, err
	}
	digest := sha256.Sum256(req.Artifact)
	digestHex := hex.EncodeToString(digest[:])
	if err := validateStageIdentity(bundle, digestHex, req); err != nil {
		return InventoryEntry{}, err
	}
	assessment := AssessPluginTrust(req.Artifact, req.Envelope, req.Roots, req.RevokedAuthorKeyIDs)
	if err := requireStageConfirmation(bundle, digestHex, assessment, req); err != nil {
		return InventoryEntry{}, err
	}
	entry := InventoryEntry{
		Artifact:       append([]byte(nil), req.Artifact...),
		Envelope:       append([]byte(nil), req.Envelope...),
		ArtifactSHA256: digestHex,
		Trust:          assessment,
		Provenance:     inventoryProvenance(req, assessment),
		Origin:         req.Origin,
		Catalog:        cloneCatalogEntry(req.Catalog),
	}
	target, found, err := readInventoryState(i.stagedPath())
	if err != nil {
		return InventoryEntry{}, fmt.Errorf("load staged plugin inventory: %w", err)
	}
	if !found {
		target = cloneInventoryState(i.active)
	}
	target.Entries = upsertInventoryEntry(target.Entries, bundle.ID, entry)
	if _, err := i.validate(target); err != nil {
		return InventoryEntry{}, err
	}
	if err := saveInventoryState(i.stagedPath(), target); err != nil {
		return InventoryEntry{}, fmt.Errorf("stage plugin inventory: %w", err)
	}
	return entry, nil
}

// Apply validates and promotes staged declarative bundles. Any candidate error
// returns the prior last-good bundle set plus a warning, so startup can continue.
func (i *Inventory) Apply() ([]Bundle, bool, error) {
	current, err := i.validate(i.active)
	if err != nil {
		return nil, false, err
	}
	staged, found, err := readInventoryState(i.stagedPath())
	if err != nil {
		return current, false, fmt.Errorf("reject staged plugin inventory: %w", err)
	}
	if !found {
		return current, false, nil
	}
	candidate, err := i.validate(staged)
	if err != nil {
		return current, false, fmt.Errorf("reject staged plugin inventory: %w", err)
	}
	if err := saveInventoryState(i.activePath(), staged); err != nil {
		return current, false, fmt.Errorf("promote staged plugin inventory: %w", err)
	}
	i.active = cloneInventoryState(staged)
	if err := os.Remove(i.stagedPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return candidate, true, fmt.Errorf("remove applied plugin inventory: %w", err)
	}
	return candidate, true, nil
}

func (i *Inventory) ActiveStandings() (map[ID]PluginStanding, error) {
	standings := make(map[ID]PluginStanding, len(i.active.Entries))
	for _, entry := range i.active.Entries {
		bundle, err := validateInventoryEntry(entry)
		if err != nil {
			return nil, err
		}
		standings[bundle.ID] = PluginStanding{Provenance: entry.Provenance, TrustState: entry.Trust.State}
	}
	return standings, nil
}

func (i *Inventory) validate(state inventoryState) ([]Bundle, error) {
	if state.SchemaVersion != inventorySchemaVersion {
		return nil, fmt.Errorf("plugin inventory schema %d, want %d", state.SchemaVersion, inventorySchemaVersion)
	}
	executable := normalizedBundleVersions(i.executable)
	staged := make([]Bundle, 0, len(state.Entries))
	for _, entry := range state.Entries {
		bundle, err := validateInventoryEntry(entry)
		if err != nil {
			return nil, err
		}
		staged = append(staged, bundle)
	}
	if err := validateInventoryAdditions(executable, staged); err != nil {
		return nil, err
	}
	bundles := append(executable, staged...)
	for _, bundle := range bundles {
		if !sdk.CompatibleWithSDK(bundle, sdk.Version) {
			return nil, fmt.Errorf("%w: plugin %s supports SDK [%s,%s], Core embeds %s",
				ErrInventoryIncompatible, bundle.ID, bundle.SDKFloor, bundle.SDKCeiling, sdk.Version)
		}
	}
	if _, err := NewRegistry(bundles); err != nil {
		return nil, err
	}
	return bundles, nil
}

// validateInventoryAdditions enforces the SDK's cross-bundle rules over the
// bundles the inventory ADDS: staged bundles are validated as a set, and each
// one must not take a plugin ID or an entity-kind name the executable-owned set
// already owns (kind names are a global namespace). The executable-owned set is
// deliberately not re-validated as a set: it is fixed at the composition root,
// Core has always admitted it as-is, and inventory contents cannot change it.
func validateInventoryAdditions(executable, staged []Bundle) error {
	if err := sdk.ValidateBundles(staged); err != nil {
		return err
	}
	executableIDs := make(map[ID]bool, len(executable))
	kindOwner := map[string]ID{}
	for _, bundle := range executable {
		executableIDs[bundle.ID] = true
		for _, kind := range bundle.EntityKinds {
			if _, taken := kindOwner[kind.Name]; !taken {
				kindOwner[kind.Name] = bundle.ID
			}
		}
	}
	for _, bundle := range staged {
		if executableIDs[bundle.ID] {
			return fmt.Errorf("plugin %s: duplicate plugin ID across bundles", bundle.ID)
		}
		for _, kind := range bundle.EntityKinds {
			if owner, taken := kindOwner[kind.Name]; taken {
				return fmt.Errorf("entity kind %q is declared by both plugin %s and plugin %s; kind names are a global namespace", kind.Name, owner, bundle.ID)
			}
		}
	}
	return nil
}

func validateInventoryEntry(entry InventoryEntry) (Bundle, error) {
	bundle, err := decodeInventoryBundle(entry.Artifact)
	if err != nil {
		return Bundle{}, err
	}
	digest := sha256.Sum256(entry.Artifact)
	if entry.ArtifactSHA256 != hex.EncodeToString(digest[:]) {
		return Bundle{}, fmt.Errorf("%w: stored artifact digest", ErrInventoryIdentity)
	}
	if entry.Origin != BundleOriginRegistry && entry.Origin != BundleOriginSideloaded {
		return Bundle{}, fmt.Errorf("plugin inventory has unknown origin %q", entry.Origin)
	}
	if entry.Trust.State != PluginTrusted && entry.Trust.State != PluginTampered &&
		entry.Trust.State != PluginRevoked && entry.Trust.State != PluginUnrecognized &&
		entry.Trust.State != PluginUnsigned {
		return Bundle{}, fmt.Errorf("plugin inventory has unknown trust state %q", entry.Trust.State)
	}
	if entry.Trust != trustAssessment(entry.Trust.State) {
		return Bundle{}, fmt.Errorf("plugin inventory trust diagnostic does not match state %q", entry.Trust.State)
	}
	if !validInventoryProvenance(entry) {
		return Bundle{}, fmt.Errorf("plugin inventory provenance %q is invalid for %s origin with %s trust", entry.Provenance, entry.Origin, entry.Trust.State)
	}
	return bundle, nil
}

func decodeInventoryBundle(raw []byte) (Bundle, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode serialized plugin bundle: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Bundle{}, errors.New("decode serialized plugin bundle: trailing JSON")
	}
	if err := sdk.ValidateBundle(bundle); err != nil {
		return Bundle{}, err
	}
	if !sdk.CompatibleWithSDK(bundle, sdk.Version) {
		return Bundle{}, fmt.Errorf("%w: plugin %s supports SDK [%s,%s], Core embeds %s",
			ErrInventoryIncompatible, bundle.ID, bundle.SDKFloor, bundle.SDKCeiling, sdk.Version)
	}
	return bundle, nil
}

func validateStageIdentity(bundle Bundle, digest string, req StageBundleRequest) error {
	if req.Origin != BundleOriginRegistry && req.Origin != BundleOriginSideloaded {
		return fmt.Errorf("plugin inventory has unknown origin %q", req.Origin)
	}
	if req.Origin == BundleOriginRegistry {
		if req.Catalog == nil {
			return fmt.Errorf("%w: registry bundle has no verified catalog entry", ErrInventoryIdentity)
		}
		if req.Catalog.PluginID != string(bundle.ID) || req.Catalog.Version != bundle.Version ||
			req.Catalog.SDKFloor != bundle.SDKFloor || req.Catalog.SDKCeiling != bundle.SDKCeiling ||
			req.Catalog.ArtifactSHA256 != digest {
			return fmt.Errorf("%w: catalog, artifact, and bundle differ", ErrInventoryIdentity)
		}
	}
	if len(req.Envelope) > 0 {
		var envelope countersignedArtifact
		if decodeTrustEnvelope(req.Envelope, &envelope) == nil {
			manifest := envelope.AuthorSigned.Manifest
			if manifest.PluginID != string(bundle.ID) || manifest.Version != bundle.Version || manifest.ArtifactSHA256 != digest {
				return fmt.Errorf("%w: trust envelope, artifact, and bundle differ", ErrInventoryIdentity)
			}
		}
	}
	return nil
}

func requireStageConfirmation(bundle Bundle, digest string, assessment PluginTrustAssessment, req StageBundleRequest) error {
	if assessment.State != PluginUnsigned && req.Origin != BundleOriginSideloaded {
		return nil
	}
	confirmation := req.Confirmation
	if confirmation == nil || confirmation.Origin != req.Origin || confirmation.PluginID != bundle.ID ||
		confirmation.Version != bundle.Version || confirmation.ArtifactSHA256 != digest {
		return fmt.Errorf("%w: bind origin, plugin id, version, and artifact digest", ErrInventoryConfirmation)
	}
	return nil
}

func inventoryProvenance(req StageBundleRequest, assessment PluginTrustAssessment) PluginProvenance {
	path := loadPathSideloaded
	if req.Origin == BundleOriginRegistry {
		path = loadPathRegistryThirdParty
		var envelope countersignedArtifact
		if decodeTrustEnvelope(req.Envelope, &envelope) == nil {
			if _, firstParty := req.Roots.FirstPartyAuthorKeyIDs[envelope.AuthorSigned.Manifest.AuthorKeyID]; firstParty {
				path = loadPathRegistryFirstParty
			}
		}
	}
	return derivePluginProvenance(path, assessment.State)
}

func validInventoryProvenance(entry InventoryEntry) bool {
	if entry.Origin == BundleOriginSideloaded {
		return entry.Provenance == ProvenanceSideloaded
	}
	if !authenticatedPluginTrustState(entry.Trust.State) {
		return entry.Provenance == ProvenanceSideloaded
	}
	return entry.Provenance == ProvenanceFirstParty || entry.Provenance == ProvenanceThirdParty
}

func readInventoryState(path string) (inventoryState, bool, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is composed only by this package from its own inventory directory and fixed filenames (last-good.json / staged.json).
	if errors.Is(err, os.ErrNotExist) {
		return inventoryState{SchemaVersion: inventorySchemaVersion}, false, nil
	}
	if err != nil {
		return inventoryState{}, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state inventoryState
	if err := decoder.Decode(&state); err != nil {
		return inventoryState{}, true, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return inventoryState{}, true, errors.New("plugin inventory has trailing JSON")
	}
	return state, true, nil
}

func saveInventoryState(path string, state inventoryState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeStateFile(path, raw)
}

func upsertInventoryEntry(entries []InventoryEntry, id ID, replacement InventoryEntry) []InventoryEntry {
	out := append([]InventoryEntry(nil), entries...)
	for index, entry := range out {
		bundle, err := decodeInventoryBundle(entry.Artifact)
		if err == nil && bundle.ID == id {
			out[index] = replacement
			return out
		}
	}
	return append(out, replacement)
}

func cloneInventoryState(state inventoryState) inventoryState {
	return inventoryState{SchemaVersion: state.SchemaVersion, Entries: append([]InventoryEntry(nil), state.Entries...)}
}

func cloneCatalogEntry(entry *catalogindex.Entry) *catalogindex.Entry {
	if entry == nil {
		return nil
	}
	cloned := *entry
	return &cloned
}

func (i *Inventory) activePath() string { return filepath.Join(i.dir, "last-good.json") }
func (i *Inventory) stagedPath() string { return filepath.Join(i.dir, "staged.json") }

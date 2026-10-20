package plugin

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/beamfall/plugin-sdk"
	"github.com/beamfall/plugin-sdk/catalogindex"
)

func TestInventoryStagesAndAppliesOnRestart(t *testing.T) {
	dir := t.TempDir()
	base := inventoryBundle("compiled")
	dynamic := inventoryBundle("dynamic")
	artifact := inventoryArtifact(t, dynamic)
	inventory, err := OpenInventory(dir, []Bundle{base})
	if err != nil {
		t.Fatal(err)
	}
	request := sideloadRequest(t, dynamic, artifact)

	if _, err := inventory.Stage(StageBundleRequest{
		Artifact: artifact,
		Origin:   BundleOriginSideloaded,
	}); !errors.Is(err, ErrInventoryConfirmation) {
		t.Fatalf("unconfirmed Stage error = %v", err)
	}
	entry, err := inventory.Stage(request)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Trust.State != PluginUnsigned {
		t.Fatalf("Trust = %q, want Unsigned", entry.Trust.State)
	}

	restarted, err := OpenInventory(dir, []Bundle{base})
	if err != nil {
		t.Fatal(err)
	}
	bundles, applied, err := restarted.Apply()
	if err != nil || !applied {
		t.Fatalf("Apply = %v, %v", applied, err)
	}
	if !hasInventoryBundle(bundles, base.ID) || !hasInventoryBundle(bundles, dynamic.ID) {
		t.Fatalf("applied bundles = %v", bundleIDs(bundles))
	}
	if _, err := os.Stat(filepath.Join(dir, "staged.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged file remains after apply: %v", err)
	}
}

func TestInventoryRejectsIncompatibleAndConflictingBundles(t *testing.T) {
	base := inventoryBundle("compiled")
	tests := []struct {
		name   string
		mutate func(*Bundle)
	}{
		{"newer floor", func(bundle *Bundle) { bundle.SDKFloor = "99.0.0" }},
		{"older ceiling", func(bundle *Bundle) { bundle.SDKCeiling = "0.1.0" }},
		{"compiled id collision", func(bundle *Bundle) { bundle.ID = base.ID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			inventory, err := OpenInventory(dir, []Bundle{base})
			if err != nil {
				t.Fatal(err)
			}
			bundle := inventoryBundle("candidate")
			test.mutate(&bundle)
			artifact := inventoryArtifact(t, bundle)
			request := sideloadRequest(t, bundle, artifact)
			if _, err := inventory.Stage(request); err == nil {
				t.Fatal("Stage accepted invalid candidate")
			}
			if _, err := os.Stat(filepath.Join(dir, "staged.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid candidate wrote staged state: %v", err)
			}
		})
	}
}

// The entity-kind namespace is enforced against what the inventory ADDS. Two
// executable-owned bundles that already share a kind name are admitted as they
// always have been; a staged bundle that takes an executable kind is rejected.
func TestInventoryKindNamespaceScopedToStagedBundles(t *testing.T) {
	first := inventoryBundle("compiled-one")
	first.EntityKinds = []EntityKind{{Name: "maker", Archetype: "person"}}
	second := inventoryBundle("compiled-two")
	second.EntityKinds = []EntityKind{{Name: "maker", Archetype: "person"}}
	executable := []Bundle{first, second}

	if _, err := OpenInventory(t.TempDir(), executable); err != nil {
		t.Fatalf("executable set sharing a kind rejected: %v", err)
	}

	dir := t.TempDir()
	inventory, err := OpenInventory(dir, executable)
	if err != nil {
		t.Fatal(err)
	}
	candidate := inventoryBundle("candidate")
	candidate.EntityKinds = []EntityKind{{Name: "maker", Archetype: "person"}}
	artifact := inventoryArtifact(t, candidate)
	if _, err := inventory.Stage(sideloadRequest(t, candidate, artifact)); err == nil ||
		!strings.Contains(err.Error(), "global namespace") {
		t.Fatalf("Stage of kind-colliding candidate error = %v", err)
	}
}

func TestInventoryCorruptOrInterruptedCandidateRetainsLastGood(t *testing.T) {
	dir := t.TempDir()
	base := inventoryBundle("compiled")
	first := inventoryBundle("first")
	inventory, err := OpenInventory(dir, []Bundle{base})
	if err != nil {
		t.Fatal(err)
	}
	firstArtifact := inventoryArtifact(t, first)
	if _, err := inventory.Stage(sideloadRequest(t, first, firstArtifact)); err != nil {
		t.Fatal(err)
	}
	if _, applied, err := inventory.Apply(); err != nil || !applied {
		t.Fatalf("first Apply = %v, %v", applied, err)
	}
	lastGood, err := os.ReadFile(inventory.activePath())
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(inventory.stagedPath(), []byte(`{"schema_version":1,"entries":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	bundles, applied, err := inventory.Apply()
	if err == nil || applied || !hasInventoryBundle(bundles, first.ID) {
		t.Fatalf("corrupt Apply = bundles %v, applied %v, error %v", bundleIDs(bundles), applied, err)
	}
	after, err := os.ReadFile(inventory.activePath())
	if err != nil || !bytes.Equal(after, lastGood) {
		t.Fatalf("last-good changed after corrupt candidate: %v", err)
	}

	if err := os.Remove(inventory.stagedPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".plugin-state-interrupted.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenInventory(dir, []Bundle{base})
	if err != nil {
		t.Fatal(err)
	}
	bundles, applied, err = restarted.Apply()
	if err != nil || applied || !hasInventoryBundle(bundles, first.ID) {
		t.Fatalf("interrupted Apply = bundles %v, applied %v, error %v", bundleIDs(bundles), applied, err)
	}
}

func TestInventoryBindsCatalogAndConfirmationToArtifact(t *testing.T) {
	dir := t.TempDir()
	bundle := inventoryBundle("catalogued")
	artifact := inventoryArtifact(t, bundle)
	digest := artifactDigest(artifact)
	catalog := &catalogindex.Entry{
		PluginID:       string(bundle.ID),
		Version:        bundle.Version,
		SDKFloor:       bundle.SDKFloor,
		SDKCeiling:     bundle.SDKCeiling,
		ArtifactSHA256: digest,
		DeliveryRef:    "delivery-catalogued",
	}
	inventory, err := OpenInventory(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := StageBundleRequest{
		Artifact: artifact,
		Catalog:  catalog,
		Origin:   BundleOriginRegistry,
	}
	if _, err := inventory.Stage(request); !errors.Is(err, ErrInventoryConfirmation) {
		t.Fatalf("unsigned registry Stage error = %v", err)
	}
	request.Confirmation = &StageConfirmation{
		Origin:         request.Origin,
		PluginID:       bundle.ID,
		Version:        bundle.Version,
		ArtifactSHA256: strings.Repeat("0", 64),
	}
	if _, err := inventory.Stage(request); !errors.Is(err, ErrInventoryConfirmation) {
		t.Fatalf("misbound confirmation error = %v", err)
	}
	request.Confirmation.ArtifactSHA256 = digest
	request.Catalog.ArtifactSHA256 = strings.Repeat("f", 64)
	if _, err := inventory.Stage(request); !errors.Is(err, ErrInventoryIdentity) {
		t.Fatalf("catalog mismatch error = %v", err)
	}
	request.Catalog.ArtifactSHA256 = digest
	if _, err := inventory.Stage(request); err != nil {
		t.Fatalf("bound Stage: %v", err)
	}
}

func TestInventoryPreservesVerifiedRegistryStanding(t *testing.T) {
	tests := []struct {
		name       string
		firstParty bool
		want       PluginProvenance
	}{
		{name: "first party", firstParty: true, want: ProvenanceFirstParty},
		{name: "third party", want: ProvenanceThirdParty},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := inventoryBundle("registry-" + strings.ReplaceAll(test.name, " ", "-"))
			inventory, err := OpenInventory(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			entry, err := inventory.Stage(signedRegistryRequest(t, bundle, test.firstParty))
			if err != nil {
				t.Fatal(err)
			}
			wantStanding := PluginStanding{Provenance: test.want, TrustState: PluginTrusted}
			if got := (PluginStanding{Provenance: entry.Provenance, TrustState: entry.Trust.State}); got != wantStanding {
				t.Fatalf("staged standing = %#v, want %#v", got, wantStanding)
			}
			bundles, applied, err := inventory.Apply()
			if err != nil || !applied {
				t.Fatalf("Apply = %v, %v", applied, err)
			}
			standings, err := inventory.ActiveStandings()
			if err != nil {
				t.Fatal(err)
			}
			registry, err := NewInventoryRegistry(bundles, nil, standings)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := registry.Standing(bundle.ID)
			if !ok || got != wantStanding {
				t.Fatalf("active standing = %#v, %v; want %#v", got, ok, wantStanding)
			}
		})
	}
}

func signedRegistryRequest(t *testing.T, bundle Bundle, firstParty bool) StageBundleRequest {
	t.Helper()
	artifact := inventoryArtifact(t, bundle)
	digest := artifactDigest(artifact)
	authorPublic, authorPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	registryPublic, registryPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest := artifactManifest{
		PluginID:       string(bundle.ID),
		Version:        bundle.Version,
		ArtifactSHA256: digest,
		AuthorKeyID:    "author-test",
		IssuedAt:       time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
	}
	canonical, err := canonicalManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	authorSignature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(authorPrivate, canonical))
	countersigned, err := canonicalArtifactSigned(canonical, authorSignature)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(countersignedArtifact{
		AuthorSigned: authorSignedArtifact{
			Manifest:        manifest,
			AuthorSignature: authorSignature,
		},
		ChecksVersion:            "checks-test",
		RegistryKeyID:            "registry-test",
		RegistryCountersignature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(registryPrivate, countersigned)),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstPartyAuthors := map[string]struct{}{}
	if firstParty {
		firstPartyAuthors[manifest.AuthorKeyID] = struct{}{}
	}
	return StageBundleRequest{
		Artifact: artifact,
		Envelope: envelope,
		Roots: PluginTrustRoots{
			Authors:                map[string]ed25519.PublicKey{manifest.AuthorKeyID: authorPublic},
			Registries:             map[string]RegistryTrustRoot{"registry-test": {PublicKey: registryPublic, ChecksVersions: map[string]struct{}{"checks-test": {}}}},
			ChecksVersions:         map[string]struct{}{"checks-test": {}},
			FirstPartyAuthorKeyIDs: firstPartyAuthors,
		},
		Catalog: &catalogindex.Entry{
			PluginID:       string(bundle.ID),
			Version:        bundle.Version,
			SDKFloor:       bundle.SDKFloor,
			SDKCeiling:     bundle.SDKCeiling,
			ArtifactSHA256: digest,
			DeliveryRef:    "delivery-" + string(bundle.ID),
		},
		Origin: BundleOriginRegistry,
	}
}

func inventoryBundle(id string) Bundle {
	return Bundle{
		ID:           ID(id),
		Version:      "1.0.0",
		SDKFloor:     "0.1.0",
		SDKCeiling:   sdk.Version,
		DisplayName:  L("Inventory Fixture"),
		MediaLabel:   L("Entry"),
		MediaClasses: []string{"video"},
		Layouts:      []LayoutSpec{{ID: "home", Role: "browse"}},
	}
}

func inventoryArtifact(t *testing.T, bundle Bundle) []byte {
	t.Helper()
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func sideloadRequest(t *testing.T, bundle Bundle, artifact []byte) StageBundleRequest {
	t.Helper()
	digest := artifactDigest(artifact)
	return StageBundleRequest{
		Artifact: artifact,
		Origin:   BundleOriginSideloaded,
		Confirmation: &StageConfirmation{
			Origin:         BundleOriginSideloaded,
			PluginID:       bundle.ID,
			Version:        bundle.Version,
			ArtifactSHA256: digest,
		},
	}
}

func artifactDigest(artifact []byte) string {
	sum := sha256.Sum256(artifact)
	return hex.EncodeToString(sum[:])
}

func hasInventoryBundle(bundles []Bundle, id ID) bool {
	for _, bundle := range bundles {
		if bundle.ID == id {
			return true
		}
	}
	return false
}

func bundleIDs(bundles []Bundle) []ID {
	ids := make([]ID, 0, len(bundles))
	for _, bundle := range bundles {
		ids = append(ids, bundle.ID)
	}
	return ids
}

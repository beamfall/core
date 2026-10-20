package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/tools/wire-contract/contract"
)

const (
	wireContractPublishedBaselinePath      = "../../tools/wire-contract/testdata/wire-contract.json"
	wireContractRegistryPath               = "../../contracts/registry.json"
	wireContractFreshnessDriftScope        = "producer-publish-freshness"
	wireContractDeterminationCandidatePath = "wire-contract-candidate-v2.0.0.json"
)

type wireContractArtifact struct {
	Path    string
	Version []int
}

// wireContractPath is the committed versioned wire-contract snapshot path (ORG-10,
// ADR-0002): the consolidated, deterministic projection of the hand-rolled
// dispatcher's schema plus the stable /v1 REST + SSE surface. It lives under
// tools/ (NOT the conformance golden corpus): it is the exporter's own
// drift-pinned baseline, exercised by this gate.

// TestWireContract is the ORG-10 drift gate: it regenerates the UNVERSIONED wire
// contract from the live registries and byte-compares against the committed
// snapshot. Adding/reclassifying a top-level field or changing a result-struct
// shape shifts the live contract and fails this test until the snapshot is
// regenerated:
//
//	go run ./tools/wire-contract > tools/wire-contract/testdata/wire-contract.json
//
// The release pipeline stamps the same snapshot with the release tag
// (--version) and publishes it as a pinned artifact, so the committed baseline
// and every shipped contract derive from the identical, drift-gated registries.
func TestWireContract(t *testing.T) {
	live, err := json.MarshalIndent(contract.BuildWithKnownDrift("", wireContractKnownDrift(t)), "", "  ")
	if err != nil {
		t.Fatalf("marshal live wire contract: %v", err)
	}
	live = append(live, '\n')

	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		//nolint:gosec // committed public test fixture; mirrors the original file mode.
		if err := os.WriteFile(wireContractPath, live, 0o644); err != nil {
			t.Fatalf("write %s: %v", wireContractPath, err)
		}
		t.Logf("wire contract snapshot updated: %s", wireContractPath)
		return
	}

	committed, err := os.ReadFile(wireContractPath)
	if err != nil {
		t.Fatalf("read %s: %v — regenerate with `go run ./tools/wire-contract > tools/wire-contract/testdata/wire-contract.json`", wireContractPath, err)
	}
	if string(committed) != string(live) {
		t.Errorf("committed wire contract is stale — regenerate with `go run ./tools/wire-contract > tools/wire-contract/testdata/wire-contract.json`")
	}
}

// TestWireContractPublishedArtifactFreshness keeps development drift explicit:
// the baseline must match either the newest published artifact or the exact
// unpublished candidate under determination.
func TestWireContractPublishedArtifactFreshness(t *testing.T) {
	publishedErr := checkWireContractPublishedArtifactFreshness(
		wireContractPublishedBaselinePath,
		".",
		wireContractRegistryPath,
		time.Now(),
	)
	if publishedErr == nil {
		return
	}
	candidate, err := os.ReadFile(wireContractDeterminationCandidatePath)
	if err != nil {
		t.Fatal(publishedErr)
	}
	baseline, err := os.ReadFile(wireContractPublishedBaselinePath)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := sameWireContract(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !matches {
		t.Fatal(publishedErr)
	}
}

// TestWireContractPublishedArtifactFreshnessRedProof proves the freshness gate
// rejects a dev-only field until the producer publishes a matching artifact.
func TestWireContractPublishedArtifactFreshnessRedProof(t *testing.T) {
	dir := t.TempDir()
	baselinePath := filepath.Join(dir, "wire-contract.json")
	registryPath := filepath.Join(dir, "registry.json")
	writeWireContractTestFile(t, baselinePath, `{"contractVersion":"","v1":{"devOnlyField":true}}`)
	writeWireContractTestFile(t, filepath.Join(dir, "wire-contract-v1.0.0.json"), `{"contractVersion":"1.0.0","v1":{"devOnlyField":true}}`)
	writeWireContractTestFile(t, filepath.Join(dir, "wire-contract-v1.1.0.json"), `{"contractVersion":"1.1.0","v1":{}}`)
	writeWireContractTestFile(t, registryPath, `{"boundaries":[{"boundary":"client-core-wire","knownDrift":[]}]}`)

	today := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	if err := checkWireContractPublishedArtifactFreshness(baselinePath, dir, registryPath, today); err == nil {
		t.Fatal("development-only wire contract field passed publish-freshness gate")
	}

	writeWireContractTestFile(t, registryPath, `{"boundaries":[{"boundary":"client-core-wire","knownDrift":[{"scope":"producer-publish-freshness","deadline":"2026-07-13"}]}]}`)
	if err := checkWireContractPublishedArtifactFreshness(baselinePath, dir, registryPath, today); err != nil {
		t.Fatalf("unexpired producer freshness waiver did not cover drift: %v", err)
	}

	writeWireContractTestFile(t, registryPath, `{"boundaries":[{"boundary":"client-core-wire","knownDrift":[{"scope":"producer-publish-freshness","deadline":"2026-07-11"}]}]}`)
	if err := checkWireContractPublishedArtifactFreshness(baselinePath, dir, registryPath, today); err == nil {
		t.Fatal("expired producer freshness waiver covered drift")
	}
}

func TestWireContractPublishRefusesOverwrite(t *testing.T) {
	cmd := exec.Command("make", "publish-wire-contract", "VERSION=1.1.0")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("publish-wire-contract overwrote an existing version")
	}
	if !bytes.Contains(out, []byte("publish a new version instead of overwriting")) {
		t.Fatalf("publish-wire-contract refusal = %q", out)
	}
}

func TestWireContractPublicationIsConsolidated(t *testing.T) {
	published, err := newestWireContractArtifact(".")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(published.Path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot contract.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}

	managementRaw, err := os.ReadFile("mgmt-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var management contract.ManagementManifest
	if err := json.Unmarshal(managementRaw, &management); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Management, management) {
		t.Fatal("published wire contract does not contain the generated management manifest")
	}

	for name, shape := range map[string]contract.WireShape{
		"playbackInfo request mediaID":       snapshot.PlaybackInfo.Request,
		"playbackInfo request tier":          snapshot.PlaybackInfo.Request,
		"playbackInfo response streams":      snapshot.PlaybackInfo.Response,
		"Stream-Token grant restricted flag": snapshot.PlaybackInfo.StreamToken,
	} {
		field := map[string]string{
			"playbackInfo request mediaID":       "mediaID",
			"playbackInfo request tier":          "tier",
			"playbackInfo response streams":      "streams",
			"Stream-Token grant restricted flag": "restricted",
		}[name]
		if !wireShapeHasField(shape, field) {
			t.Fatalf("published %s shape is missing %q", name, field)
		}
	}
	if snapshot.Compatibility.Versioning != "semantic-versioning" || snapshot.Compatibility.IncompatibleChange == "" || snapshot.Compatibility.ConsumerPinRule == "" {
		t.Fatal("published wire contract is missing compatibility decisions")
	}

	registryRaw, err := os.ReadFile(wireContractRegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Boundaries []struct {
			Boundary   string                `json:"boundary"`
			KnownDrift []contract.KnownDrift `json:"knownDrift"`
		} `json:"boundaries"`
	}
	if err := json.Unmarshal(registryRaw, &registry); err != nil {
		t.Fatal(err)
	}
	for _, boundary := range registry.Boundaries {
		if boundary.Boundary == "client-core-wire" {
			if !reflect.DeepEqual(snapshot.KnownDrift, boundary.KnownDrift) {
				t.Fatal("published wire contract known drift differs from the registry")
			}
			return
		}
	}
	t.Fatal("client-core-wire registry boundary is missing")
}

func wireShapeHasField(shape contract.WireShape, name string) bool {
	for _, field := range shape.Fields {
		if field.Name == name || wireShapeHasField(field.Shape, name) {
			return true
		}
	}
	return shape.Element != nil && wireShapeHasField(*shape.Element, name)
}

func writeWireContractTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestWireContractConsumersPinPublishedVersion(t *testing.T) {
	published, err := newestWireContractArtifact(".")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(published.Path)
	if err != nil {
		t.Fatalf("read published wire contract %s: %v", published.Path, err)
	}
	var contract struct {
		Version string `json:"contractVersion"`
	}
	if err := json.Unmarshal(artifact, &contract); err != nil {
		t.Fatalf("decode published wire contract: %v", err)
	}
	registry, err := os.ReadFile(wireContractRegistryPath)
	if err != nil {
		t.Fatalf("read contract registry: %v", err)
	}
	var data struct {
		Boundaries []struct {
			Boundary  string          `json:"boundary"`
			Consumers json.RawMessage `json:"consumers"`
		} `json:"boundaries"`
	}
	if err := json.Unmarshal(registry, &data); err != nil {
		t.Fatalf("decode contract registry: %v", err)
	}
	for _, boundary := range data.Boundaries {
		if boundary.Boundary != "client-core-wire" {
			continue
		}
		consumers, err := decodeWireConsumerPins(boundary.Consumers)
		if err != nil {
			t.Fatalf("decode client-core-wire consumer pins: %v", err)
		}
		if err := validateWireConsumerPins(consumers, contract.Version); err != nil {
			t.Fatalf("client-core-wire consumer pins: %v", err)
		}
		return
	}
	t.Fatal("client-core-wire boundary missing from contract registry")
}

type wireConsumerPin struct {
	Repo          string `json:"repo"`
	PinnedVersion string `json:"pinnedVersion"`
}

func decodeWireConsumerPins(raw []byte) ([]wireConsumerPin, error) {
	var consumers []wireConsumerPin
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&consumers); err != nil {
		return nil, err
	}
	return consumers, nil
}

// parseWireSemVer parses a strict "MAJOR.MINOR.PATCH" version and rejects any
// trailing or pre-release garbage that fmt.Sscanf would otherwise tolerate.
func parseWireSemVer(v string) ([3]int, error) {
	var parsed [3]int
	if n, err := fmt.Sscanf(v, "%d.%d.%d", &parsed[0], &parsed[1], &parsed[2]); err != nil || n != 3 {
		return parsed, fmt.Errorf("invalid SemVer %q", v)
	}
	if parsed[0] < 0 || parsed[1] < 0 || parsed[2] < 0 {
		return parsed, fmt.Errorf("invalid SemVer %q", v)
	}
	if fmt.Sprintf("%d.%d.%d", parsed[0], parsed[1], parsed[2]) != v {
		return parsed, fmt.Errorf("invalid SemVer %q", v)
	}
	return parsed, nil
}

// validateWireConsumerPins enforces the client-core-wire producer-truth rules
// (ADR-0122 Amendment A §1): every pin is a well-formed object with a unique
// repo and a strict SemVer pinnedVersion sharing the producer's major. Minor
// lag is valid; malformed, duplicate, missing, or major-skewed pins fail.
func validateWireConsumerPins(consumers []wireConsumerPin, publishedVersion string) error {
	if len(consumers) == 0 {
		return fmt.Errorf("client-core-wire has no consumer pins")
	}
	published, err := parseWireSemVer(publishedVersion)
	if err != nil {
		return fmt.Errorf("published version: %w", err)
	}
	seen := make(map[string]bool, len(consumers))
	for _, c := range consumers {
		if c.Repo == "" {
			return fmt.Errorf("consumer pin missing repo: %#v", c)
		}
		if seen[c.Repo] {
			return fmt.Errorf("consumer repo %q duplicated", c.Repo)
		}
		seen[c.Repo] = true
		pinned, err := parseWireSemVer(c.PinnedVersion)
		if err != nil {
			return fmt.Errorf("consumer %q: %w", c.Repo, err)
		}
		if pinned[0] != published[0] {
			return fmt.Errorf("consumer %q pin %s skews major from published %s", c.Repo, c.PinnedVersion, publishedVersion)
		}
	}
	return nil
}

func TestWireContractConsumersPinValidation(t *testing.T) {
	const published = "1.1.0"
	valid := []wireConsumerPin{
		{Repo: "beamfall-web", PinnedVersion: "1.0.0"},
		{Repo: "beamfall-apple", PinnedVersion: "1.1.0"},
	}
	if err := validateWireConsumerPins(valid, published); err != nil {
		t.Fatalf("minor-lag pins must pass: %v", err)
	}
	rejected := map[string][]wireConsumerPin{
		"major skew":       {{Repo: "beamfall-web", PinnedVersion: "2.0.0"}},
		"duplicate repo":   {{Repo: "beamfall-web", PinnedVersion: "1.0.0"}, {Repo: "beamfall-web", PinnedVersion: "1.0.0"}},
		"missing repo":     {{Repo: "", PinnedVersion: "1.0.0"}},
		"missing pin":      {{Repo: "beamfall-web", PinnedVersion: ""}},
		"malformed semver": {{Repo: "beamfall-web", PinnedVersion: "1.0"}},
		"negative semver":  {{Repo: "beamfall-web", PinnedVersion: "-1.0.0"}},
		"empty":            {},
	}
	for name, consumers := range rejected {
		if err := validateWireConsumerPins(consumers, published); err == nil {
			t.Fatalf("%s must fail validation", name)
		}
	}
	// A bare-string consumer entry for client-core-wire cannot decode into the
	// structured pin shape — the producer-truth boundary rejects legacy strings.
	for name, raw := range map[string]string{
		"bare string": `["beamfall-web"]`,
		"extra field": `[{"repo":"beamfall-web","pinnedVersion":"1.0.0","extra":true}]`,
	} {
		if _, err := decodeWireConsumerPins([]byte(raw)); err == nil {
			t.Fatalf("%s consumer entry must fail strict decoding", name)
		}
	}
}

func newestWireContractArtifact(dir string) (wireContractArtifact, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "wire-contract-v*.json"))
	if err != nil {
		return wireContractArtifact{}, err
	}
	var artifacts []wireContractArtifact
	for _, path := range paths {
		base := filepath.Base(path)
		var major, minor, patch int
		if _, err := fmt.Sscanf(base, "wire-contract-v%d.%d.%d.json", &major, &minor, &patch); err != nil {
			continue
		}
		artifacts = append(artifacts, wireContractArtifact{Path: path, Version: []int{major, minor, patch}})
	}
	if len(artifacts) == 0 {
		return wireContractArtifact{}, fmt.Errorf("no versioned wire-contract artifact found")
	}
	sort.Slice(artifacts, func(i, j int) bool {
		for n := range artifacts[i].Version {
			if artifacts[i].Version[n] != artifacts[j].Version[n] {
				return artifacts[i].Version[n] > artifacts[j].Version[n]
			}
		}
		return false
	})
	return artifacts[0], nil
}

func sameWireContract(dev, published []byte) (bool, error) {
	var devJSON, publishedJSON map[string]any
	if err := json.Unmarshal(dev, &devJSON); err != nil {
		return false, fmt.Errorf("decode development wire contract: %w", err)
	}
	if err := json.Unmarshal(published, &publishedJSON); err != nil {
		return false, fmt.Errorf("decode published wire contract: %w", err)
	}
	delete(devJSON, "contractVersion")
	delete(publishedJSON, "contractVersion")
	return reflect.DeepEqual(devJSON, publishedJSON), nil
}

func checkWireContractPublishedArtifactFreshness(baselinePath, artifactDir, registryPath string, today time.Time) error {
	// Paths are repository constants in the gate and t.TempDir fixtures in the RED proof.
	//nolint:gosec // test-only dependency injection intentionally accepts controlled paths.
	baseline, err := os.ReadFile(baselinePath)
	if err != nil {
		return fmt.Errorf("read development wire contract: %w", err)
	}
	published, err := newestWireContractArtifact(artifactDir)
	if err != nil {
		return err
	}
	artifact, err := os.ReadFile(published.Path)
	if err != nil {
		return fmt.Errorf("read published wire contract %s: %w", published.Path, err)
	}
	equal, err := sameWireContract(baseline, artifact)
	if err != nil {
		return err
	}
	if equal {
		return nil
	}
	waived, err := wireContractFreshnessWaived(registryPath, today)
	if err != nil {
		return err
	}
	if waived {
		return nil
	}
	return fmt.Errorf("development wire contract differs from newest published artifact %s; publish a new versioned artifact before merging", published.Path)
}

func wireContractFreshnessWaived(registryPath string, today time.Time) (bool, error) {
	//nolint:gosec // test-only dependency injection intentionally accepts a controlled path.
	registry, err := os.ReadFile(registryPath)
	if err != nil {
		return false, fmt.Errorf("read contract registry: %w", err)
	}
	var data struct {
		Boundaries []struct {
			Boundary   string `json:"boundary"`
			KnownDrift []struct {
				Scope    string `json:"scope"`
				Deadline string `json:"deadline"`
			} `json:"knownDrift"`
		} `json:"boundaries"`
	}
	if err := json.Unmarshal(registry, &data); err != nil {
		return false, fmt.Errorf("decode contract registry: %w", err)
	}
	today = today.UTC().Truncate(24 * time.Hour)
	for _, boundary := range data.Boundaries {
		if boundary.Boundary != "client-core-wire" {
			continue
		}
		for _, drift := range boundary.KnownDrift {
			if drift.Scope != wireContractFreshnessDriftScope {
				continue
			}
			deadline, err := time.Parse("2006-01-02", drift.Deadline)
			if err != nil {
				return false, fmt.Errorf("decode %s knownDrift deadline %q: %w", wireContractFreshnessDriftScope, drift.Deadline, err)
			}
			if !deadline.Before(today) {
				return true, nil
			}
		}
	}
	return false, nil
}

// TestWireContractDeterministic asserts the exporter is byte-stable on re-run
// for a fixed version (the artifact's publishability requirement, ORG-10): two
// independent builds of the same version must encode identically.
func TestWireContractDeterministic(t *testing.T) {
	drift := wireContractKnownDrift(t)
	a, err := json.MarshalIndent(contract.BuildWithKnownDrift("v9.9.9", drift), "", "  ")
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	b, err := json.MarshalIndent(contract.BuildWithKnownDrift("v9.9.9", drift), "", "  ")
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("wire contract export is non-deterministic for a fixed version")
	}
}

func wireContractKnownDrift(t *testing.T) []contract.KnownDrift {
	t.Helper()
	raw, err := os.ReadFile(wireContractRegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Boundaries []struct {
			Boundary   string                `json:"boundary"`
			KnownDrift []contract.KnownDrift `json:"knownDrift"`
		} `json:"boundaries"`
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	for _, boundary := range registry.Boundaries {
		if boundary.Boundary == "client-core-wire" {
			return boundary.KnownDrift
		}
	}
	t.Fatal("client-core-wire registry boundary missing")
	return nil
}

func TestWireContractListsAreBounded(t *testing.T) {
	snapshot := contract.Build("")
	if err := contract.ValidateListBounds(snapshot); err != nil {
		t.Fatal(err)
	}

	// The lint must fail red when a future contract change adds an array without
	// declaring either cursor pagination or a finite maximum.
	snapshot.V1.Routes[0].Response.Shape.Fields = append(snapshot.V1.Routes[0].Response.Shape.Fields,
		server.V1FieldContract{Name: "unbounded", Shape: server.V1Shape{Type: "array", Element: &server.V1Shape{Type: "string"}}},
	)
	if err := contract.ValidateListBounds(snapshot); err == nil {
		t.Fatal("unbounded response list passed wire-contract lint")
	}
}

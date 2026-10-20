//go:build !beamfall_appstore

package conformance

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/beamfall/core/tools/wire-contract/contract"
)

func TestWireContractCandidateRequiresMajorBumpAndIsUnpublished(t *testing.T) {
	previous, err := os.ReadFile(wireContractPreviousPath)
	if err != nil {
		t.Fatal(err)
	}
	candidateSnapshot := contract.BuildWithKnownDrift("2.0.0", wireContractKnownDrift(t))
	candidateBuild, err := json.MarshalIndent(candidateSnapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	candidateBuild = append(candidateBuild, '\n')
	if os.Getenv("BEAMFALL_UPDATE_WIRE_CONTRACT_CANDIDATE") == "1" {
		writeCandidateArtifact(t, wireContractCandidatePath, candidateBuild)
		writeCandidateArtifact(t, wireContractCandidateDiffPath, candidateDiff(t, previous, candidateBuild))
	}
	candidateJSON, err := os.ReadFile(wireContractCandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	var candidate contract.Snapshot
	if err := json.Unmarshal(candidateJSON, &candidate); err != nil {
		t.Fatal(err)
	}
	if candidate.ContractVersion != "2.0.0" {
		t.Fatalf("candidate version = %q, want 2.0.0", candidate.ContractVersion)
	}
	if !bytes.Equal(candidateJSON, candidateBuild) {
		t.Fatal("committed candidate is stale")
	}
	canonicalJSON, err := os.ReadFile(wireContractPath)
	if err != nil {
		t.Fatal(err)
	}
	var canonical contract.Snapshot
	if err := json.Unmarshal(canonicalJSON, &canonical); err != nil {
		t.Fatal(err)
	}
	canonical.ContractVersion = candidate.ContractVersion
	canonicalBuild, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	canonicalBuild = append(canonicalBuild, '\n')
	if !bytes.Equal(candidateJSON, canonicalBuild) {
		t.Fatal("canonical fixture does not contain the candidate metadata")
	}
	assertCandidateSchemaDiff(t)
	minor := candidate
	minor.ContractVersion = "1.14.0"
	if err := contract.ValidateVersionTransition(previous, minor); err == nil {
		t.Fatal("metadata corrections rode a minor bump")
	} else if !strings.Contains(err.Error(), "bump the major version") {
		t.Fatalf("minor-bump refusal = %q, want major-version requirement", err)
	}
	if err := contract.ValidateVersionTransition(previous, candidate); err != nil {
		t.Fatalf("major candidate refused: %v", err)
	}
	if _, err := os.Stat("wire-contract-v2.0.0.json"); !os.IsNotExist(err) {
		t.Fatalf("candidate was published as wire-contract-v2.0.0.json: %v", err)
	}
}

func assertCandidateSchemaDiff(t *testing.T) {
	t.Helper()
	previous, err := os.ReadFile(wireContractPreviousPath)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := os.ReadFile(wireContractCandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	actual := candidateDiff(t, previous, candidate)
	expected, err := os.ReadFile(wireContractCandidateDiffPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("candidate differs from " + wireContractPreviousPath + " outside the documented schema diff")
	}
}

func candidateDiff(t *testing.T, previous, candidate []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	previousPath := dir + "/previous.json"
	candidatePath := dir + "/candidate.json"
	writeCandidateArtifact(t, previousPath, previous)
	writeCandidateArtifact(t, candidatePath, candidate)
	cmd := exec.Command( // #nosec G204 -- fixed "diff" binary; arguments are t.TempDir paths and constant labels
		"diff", "-u",
		"--label", wireContractPreviousPath,
		"--label", wireContractCandidatePath,
		previousPath, candidatePath,
	)
	actual, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("candidate schema diff: %v", err)
	}
	return actual
}

func writeCandidateArtifact(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil { // #nosec G703 -- path is built from t.TempDir()
		t.Fatalf("write %s: %v", path, err)
	}
}

//go:build !beamfall_appstore

package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
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
	golden, err := os.ReadFile(wireContractCandidateDiffPath)
	if err != nil {
		t.Fatal(err)
	}
	// The golden is a unified diff whose hunk boundaries depend on the diff
	// implementation that wrote it, so apply it instead of byte-comparing a
	// fresh `diff -u`: the documented diff must turn the previous contract into
	// exactly the candidate.
	patched, err := applyUnifiedDiff(previous, golden)
	if err != nil {
		t.Fatalf("documented schema diff does not apply to %s: %v", wireContractPreviousPath, err)
	}
	if !bytes.Equal(patched, candidate) {
		t.Fatal("candidate differs from " + wireContractPreviousPath + " outside the documented schema diff")
	}
}

// applyUnifiedDiff applies a single-file unified diff to original. Every context
// and removed line must match the original exactly, so a diff that does not
// describe original is refused rather than approximated.
func applyUnifiedDiff(original, diff []byte) ([]byte, error) {
	src := strings.SplitAfter(string(original), "\n")
	if src[len(src)-1] == "" {
		src = src[:len(src)-1]
	}
	var out []string
	pos := 0 // next unconsumed line of src
	hunkHeader := regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+\d+(?:,\d+)? @@`)
	lines := strings.SplitAfter(string(diff), "\n")
	inHunk := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			start, _ := strconv.Atoi(m[1])
			if m[2] == "0" {
				start++ // an empty old range names the line before the insertion
			}
			if start-1 < pos || start-1 > len(src) {
				return nil, fmt.Errorf("hunk %q starts outside the unconsumed input", strings.TrimSpace(line))
			}
			out = append(out, src[pos:start-1]...)
			pos = start - 1
			inHunk = true
			continue
		}
		if !inHunk { // file header lines
			continue
		}
		body := line[1:]
		if strings.HasPrefix(line, "\\") {
			// "\ No newline at end of file" applies to the preceding line.
			if len(out) > 0 && lines[i-1][0] != '-' {
				out[len(out)-1] = strings.TrimSuffix(out[len(out)-1], "\n")
			}
			continue
		}
		switch line[0] {
		case ' ', '-':
			if pos >= len(src) || src[pos] != body && strings.TrimSuffix(src[pos], "\n") != strings.TrimSuffix(body, "\n") {
				return nil, fmt.Errorf("line %d of the diff does not match input line %d", i+1, pos+1)
			}
			if line[0] == ' ' {
				out = append(out, src[pos])
			}
			pos++
		case '+':
			out = append(out, body)
		default:
			return nil, fmt.Errorf("line %d of the diff has unexpected prefix %q", i+1, line[0])
		}
	}
	out = append(out, src[pos:]...)
	return []byte(strings.Join(out, "")), nil
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

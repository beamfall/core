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
	minor.ContractVersion = "1.16.0"
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
// and removed line must match the original exactly, and every hunk header must
// agree with the hunk: the old start with where the hunk applies, the new start
// with where its output lands, and both counts with the lines the body
// consumes and produces. A diff that does not describe original is refused
// rather than approximated.
func applyUnifiedDiff(original, diff []byte) ([]byte, error) {
	src := strings.SplitAfter(string(original), "\n")
	if src[len(src)-1] == "" {
		src = src[:len(src)-1]
	}
	var out []string
	pos := 0 // next unconsumed line of src
	hunkHeader := regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	count := func(field string) int {
		if field == "" {
			return 1
		}
		n, _ := strconv.Atoi(field)
		return n
	}
	var (
		inHunk                             bool
		header                             string
		wantOld, wantNew, seenOld, seenNew int
	)
	closeHunk := func() error {
		if inHunk && (seenOld != wantOld || seenNew != wantNew) {
			return fmt.Errorf("hunk %q declares -%d +%d lines but its body has -%d +%d", header, wantOld, wantNew, seenOld, seenNew)
		}
		return nil
	}
	lines := strings.SplitAfter(string(diff), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			if err := closeHunk(); err != nil {
				return nil, err
			}
			header = strings.TrimSpace(line)
			oldStart, _ := strconv.Atoi(m[1])
			newStart, _ := strconv.Atoi(m[3])
			wantOld, wantNew, seenOld, seenNew = count(m[2]), count(m[4]), 0, 0
			if wantOld == 0 {
				oldStart++ // an empty range names the line before the change
			}
			if wantNew == 0 {
				newStart++
			}
			if oldStart-1 < pos || oldStart-1 > len(src) {
				return nil, fmt.Errorf("hunk %q starts outside the unconsumed input", header)
			}
			out = append(out, src[pos:oldStart-1]...)
			pos = oldStart - 1
			if newStart-1 != len(out) {
				return nil, fmt.Errorf("hunk %q new start %d does not match output line %d", header, newStart, len(out)+1)
			}
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
			seenOld++
			if line[0] == ' ' {
				out = append(out, src[pos])
				seenNew++
			}
			pos++
		case '+':
			out = append(out, body)
			seenNew++
		default:
			return nil, fmt.Errorf("line %d of the diff has unexpected prefix %q", i+1, line[0])
		}
	}
	if err := closeHunk(); err != nil {
		return nil, err
	}
	out = append(out, src[pos:]...)
	return []byte(strings.Join(out, "")), nil
}

func TestApplyUnifiedDiffValidatesHunkHeaders(t *testing.T) {
	const original = "a\nb\nc\nd\ne\n"
	const hunk = " b\n-c\n+C\n+C2\n d\n"
	const want = "a\nb\nC\nC2\nd\ne\n"
	tests := []struct {
		name, header string
		ok           bool
	}{
		{"correct", "@@ -2,3 +2,4 @@", true},
		{"old count too large", "@@ -2,4 +2,4 @@", false},
		{"old count too small", "@@ -2,2 +2,4 @@", false},
		{"new count too large", "@@ -2,3 +2,5 @@", false},
		{"new count too small", "@@ -2,3 +2,3 @@", false},
		{"old start too early", "@@ -1,3 +1,4 @@", false},
		{"old start too late", "@@ -3,3 +3,4 @@", false},
		{"new start wrong", "@@ -2,3 +999,4 @@", false},
		{"both starts and counts wrong", "@@ -1,600 +999,600 @@", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyUnifiedDiff([]byte(original), []byte("--- a\n+++ b\n"+tc.header+"\n"+hunk))
			if tc.ok {
				if err != nil || string(got) != want {
					t.Fatalf("got %q, %v; want %q", got, err, want)
				}
			} else if err == nil {
				t.Fatalf("header %q accepted, result %q", tc.header, got)
			}
		})
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

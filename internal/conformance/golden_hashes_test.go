package conformance

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// goldensSHA256Path is the committed expected-hash file for the standalone
// client-contract goldens, in `shasum -a 256` format (one "<hex>  <name>" line
// per golden). It is the core side of the client drift gate: the apple/android
// clients vendor byte-identical copies of these goldens and verify their copy's
// sha256 against these values; regenerate with `go run ./tools/golden-hashes`
// (= the tool that prints exactly this format).
const goldensSHA256Path = "testdata/goldens.sha256"

// goldenHashFiles is the canonical set the gate covers. Adding or removing a
// standalone golden requires updating this list, tools/golden-hashes/main.go,
// and the committed goldens.sha256 together — the test below enforces that they
// stay in lockstep.
var goldenHashFiles = []string{
	"graph-response.golden.json",
	"manifest.golden.json",
	"playback-info.golden.json",
	"token-scope.golden.json",
}

// TestGoldenHashes asserts the standalone goldens exist and that each
// one's live sha256 matches the committed goldens.sha256 — and that the
// manifest names exactly those goldens, no more, no less. This is what
// keeps the clients' vendored expected-hash file regenerable from a known-good
// core source: if a golden is edited (e.g. via BEAMFALL_UPDATE_GOLDEN) the hash
// shifts and this test fails until `go run ./tools/golden-hashes >
// internal/conformance/testdata/goldens.sha256` is re-run and the clients
// re-vendor.
func TestGoldenHashes(t *testing.T) {
	expected, err := parseSHA256Manifest(goldensSHA256Path)
	if err != nil {
		t.Fatalf("read %s: %v — regenerate with `go run ./tools/golden-hashes > %s`", goldensSHA256Path, err, goldensSHA256Path)
	}

	if len(expected) != len(goldenHashFiles) {
		t.Fatalf("%s lists %d files, want the %d standalone goldens — regenerate with `go run ./tools/golden-hashes`",
			goldensSHA256Path, len(expected), len(goldenHashFiles))
	}

	for _, name := range goldenHashFiles {
		want, ok := expected[name]
		if !ok {
			t.Errorf("%s missing an entry for %s — regenerate with `go run ./tools/golden-hashes`", goldensSHA256Path, name)
			continue
		}
		content, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Errorf("golden %s missing (%v) — generate with BEAMFALL_UPDATE_GOLDEN=1", name, err)
			continue
		}
		sum := sha256.Sum256(content)
		got := hex.EncodeToString(sum[:])
		if got != want {
			t.Errorf("golden %s sha256 drifted: live %s, committed %s.\n"+
				"If the golden change is intentional, re-run `go run ./tools/golden-hashes > %s` "+
				"and re-vendor the clients' contract fixtures.", name, got, want, goldensSHA256Path)
		}
	}
}

var sha256ManifestLine = regexp.MustCompile(`^([0-9a-f]{64})  ([^\r\n]+)$`)

// parseSHA256Manifest reads the repository's strict SHA-256 manifest format.
func parseSHA256Manifest(manifestPath string) (map[string]string, error) {
	// #nosec G304 -- callers supply repository-owned conformance manifest paths.
	f, err := os.Open(manifestPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	lineNumber := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		match := sha256ManifestLine.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("line %d: expected 64 lowercase hex digits, two spaces, and a path", lineNumber)
		}
		hash, name := match[1], match[2]
		if name != strings.TrimSpace(name) || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("line %d: unsafe manifest path %q", lineNumber, name)
		}
		for strings.HasPrefix(name, "./") {
			name = strings.TrimPrefix(name, "./")
		}
		for _, component := range strings.Split(name, "/") {
			if component == ".." {
				return nil, fmt.Errorf("line %d: path traversal is not allowed", lineNumber)
			}
		}
		name = path.Clean(name)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("line %d: path escapes manifest root", lineNumber)
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("line %d: duplicate normalized path %q", lineNumber, name)
		}
		out[name] = hash
	}
	return out, scanner.Err()
}

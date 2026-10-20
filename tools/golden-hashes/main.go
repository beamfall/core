// Command golden-hashes emits the canonical sha256 of the standalone
// client-contract goldens, in the `shasum -a 256` format:
//
//	<lowercase-hex-sha256>  <name>
//
// one line per golden, sorted by name, newline-terminated. These files —
//
//	internal/conformance/testdata/manifest.golden.json
//	internal/conformance/testdata/graph-response.golden.json
//	internal/conformance/testdata/playback-info.golden.json
//	internal/conformance/testdata/token-scope.golden.json
//
// are the source of truth for the client contract fixtures: the apple and
// android clients vendor byte-identical copies (Tests/BeamfallKitTests/Fixtures
// and kit/src/test/resources/fixtures) and each client gates its copy's sha256
// against the value printed here. This is the core side of that drift gate — run
// it to regenerate the committed expected-hash file the core test pins against,
// and the clients regenerate their vendored copy from the same output:
//
//	go run ./tools/golden-hashes > internal/conformance/testdata/goldens.sha256
//
// The `<hash>  <name>` shape is stable and `shasum -c`-compatible, so a client
// in any language verifies its copy with a plain per-file sha256 — no JSON,
// ordering, or concatenation subtleties. (The multi-context corpus has its own
// JSON manifest, internal/conformance/testdata/corpus/corpus.manifest.json;
// these standalone goldens are pinned separately, here.)
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// goldenDir is relative to the repo root, which is the working directory when
// the tool is invoked as `go run ./tools/golden-hashes` from the module root.
const goldenDir = "internal/conformance/testdata"

// goldenNames are the standalone client-contract goldens, by base name.
var goldenNames = []string{
	"graph-response.golden.json",
	"manifest.golden.json",
	"playback-info.golden.json",
	"token-scope.golden.json",
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "golden-hashes:", err)
		os.Exit(1)
	}
}

func run(out *os.File) error {
	names := append([]string(nil), goldenNames...)
	sort.Strings(names)
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(goldenDir, name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		fmt.Fprintf(out, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return nil
}

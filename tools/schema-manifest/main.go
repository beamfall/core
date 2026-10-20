// Command schema-manifest emits the committed GraphQL schema manifest (H23-A):
// the source-of-truth JSON listing every dispatchable top-level field with its
// effect class (read/write) and required authorization tier (public/session/
// admin), derived directly from the dispatcher's own registries via
// graph.SchemaManifest(). It is the cheap, regenerable contract artifact that
// clients and reviewers can diff against, and the core side of a drift gate:
//
//	go run ./tools/schema-manifest > internal/conformance/testdata/schema-manifest.json
//
// The conformance test TestSchemaManifest regenerates this in-memory and
// byte-compares against the committed file, so the registry and the manifest
// can never silently diverge (a new field, or a tier reclassification, fails
// the gate until the manifest is regenerated).
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/beamfall/core/internal/graph"
)

func main() {
	pretty, err := json.MarshalIndent(graph.SchemaManifest(), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "schema-manifest:", err)
		os.Exit(1)
	}
	// Newline-terminated, matching the other committed JSON goldens.
	if _, err := os.Stdout.Write(append(pretty, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "schema-manifest:", err)
		os.Exit(1)
	}
}

package conformance

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/beamfall/core/internal/graph"
)

// schemaManifestPath is the committed GraphQL schema source-of-truth path (H23-A):
// every dispatchable top-level field with its effect class and required tier,
// generated from graph.SchemaManifest() (which reads the dispatcher's own
// queryFields/mutationFields/adminFields registries).

// TestSchemaManifest is the H23-A drift gate: it regenerates the manifest from
// the live registry and byte-compares against the committed JSON. Adding a
// top-level field, or reclassifying one's effect/tier (e.g. moving a field into
// or out of adminFields), shifts the live manifest and fails this test until
// the artifact is regenerated:
//
//	go run ./tools/schema-manifest > internal/conformance/testdata/schema-manifest.json
//
// This makes the committed manifest a cheap, always-current contract artifact
// the clients and reviewers can diff against — it can never silently lag what
// execute() actually dispatches and gates.
func TestSchemaManifest(t *testing.T) {
	live, err := json.MarshalIndent(graph.SchemaManifest(), "", "  ")
	if err != nil {
		t.Fatalf("marshal live manifest: %v", err)
	}
	live = append(live, '\n')

	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(schemaManifestPath, live, 0o644); err != nil {
			t.Fatalf("write %s: %v", schemaManifestPath, err)
		}
		t.Logf("schema manifest updated: %s", schemaManifestPath)
		return
	}

	committed, err := os.ReadFile(schemaManifestPath)
	if err != nil {
		t.Fatalf("read %s: %v — regenerate with `go run ./tools/schema-manifest > %s`", schemaManifestPath, err, schemaManifestPath)
	}
	if string(committed) != string(live) {
		t.Errorf("committed schema manifest is stale — regenerate with `go run ./tools/schema-manifest > %s`", schemaManifestPath)
	}
}

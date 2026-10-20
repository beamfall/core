package conformance

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/beamfall/core/internal/graph"
)

// nestedSchemaPath is the committed GraphQL NESTED schema source-of-truth path
// (H23-B): the result-struct shape of every dispatchable top-level field,
// reflected recursively into its nested fields via graph.NestedSchema(). It
// complements schema-manifest.json (the flat H23-A effect/tier list) with the
// nested-field shape the depth-1 dispatcher cannot see at runtime.

// TestNestedSchema is the H23-B drift gate: it regenerates the nested schema
// from the live result structs and byte-compares against the committed JSON.
// Adding, removing, or renaming a nested field on any GraphQL result struct
// (e.g. adding sourceItems.mediaClass, or removing a field a client still
// selects) shifts the reflected schema and fails this test until the artifact
// is regenerated:
//
//	go run ./tools/nested-schema > internal/conformance/testdata/nested-schema.json
//
// This makes the nested wire shape a cheap, always-current contract artifact
// clients and reviewers can diff their selection sets against — it can never
// silently lag the Go structs execute() actually serializes. It is a CI-time
// check only: it does not change runtime dispatch (runtime nested-field
// rejection is gated on first conforming the shipped clients — see the H23-B
// notes).
func TestNestedSchema(t *testing.T) {
	live, err := json.MarshalIndent(graph.NestedSchema(), "", "  ")
	if err != nil {
		t.Fatalf("marshal live nested schema: %v", err)
	}
	live = append(live, '\n')

	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(nestedSchemaPath, live, 0o644); err != nil {
			t.Fatalf("write %s: %v", nestedSchemaPath, err)
		}
		t.Logf("nested schema updated: %s", nestedSchemaPath)
		return
	}

	committed, err := os.ReadFile(nestedSchemaPath)
	if err != nil {
		t.Fatalf("read %s: %v — regenerate with `go run ./tools/nested-schema > %s`", nestedSchemaPath, err, nestedSchemaPath)
	}
	if string(committed) != string(live) {
		t.Errorf("committed nested schema is stale — regenerate with `go run ./tools/nested-schema > %s`", nestedSchemaPath)
	}
}

// Command nested-schema emits the committed GraphQL NESTED schema (H23-B): the
// source-of-truth JSON describing the result shape of every dispatchable
// top-level field — each field's result type reflected recursively into its
// nested fields (json tags, through slices/pointers/embedded structs) via
// graph.NestedSchema(). It complements the flat H23-A manifest (effect/tier per
// top-level field) with the nested-field shape the depth-1 dispatcher's runtime
// router cannot see:
//
//	go run ./tools/nested-schema > internal/conformance/testdata/nested-schema.json
//
// The conformance test TestNestedSchema regenerates this in-memory and
// byte-compares against the committed file, so a result-struct shape change
// (added/removed/renamed nested field) fails the gate until the artifact is
// regenerated — and clients can diff their nested selection sets against the
// real wire shape.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/beamfall/core/internal/graph"
)

func main() {
	pretty, err := json.MarshalIndent(graph.NestedSchema(), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "nested-schema:", err)
		os.Exit(1)
	}
	// Newline-terminated, matching the other committed JSON goldens.
	if _, err := os.Stdout.Write(append(pretty, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "nested-schema:", err)
		os.Exit(1)
	}
}

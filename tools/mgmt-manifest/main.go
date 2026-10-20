// Command mgmt-manifest emits the management-surface manifest (ADR-0122 Tier 2):
// the cross-repo parity artifact listing every Console-tier (admin) GraphQL
// field and the HTTP management routes declared by the server, as required by
// ADR-0060/0061.
//
// Output is written to stdout (or to -out if given).
// The emitted file is the stable artifact that beamfall-web's parity checker
// reads to verify web has a corresponding implementation for every Core
// management capability (or a knownDrift entry in contracts/registry.json).
//
// Regenerate and commit with:
//
//	go run ./tools/mgmt-manifest > internal/conformance/mgmt-manifest.json
//
// or via the shell wrapper:
//
//	script/emit-mgmt-manifest.sh
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/server"
)

// MgmtManifest is the management-surface manifest artifact.
// It lists the admin-tier GraphQL operations and the management HTTP routes
// exposed by the Core server (ADR-0060/0061 surface).
type MgmtManifest struct {
	// SchemaVersion is the format version of this manifest document. Bump only
	// when the top-level shape changes.
	SchemaVersion string `json:"schemaVersion"`
	// GraphQLOperations lists every admin-tier (Console) GraphQL field sorted
	// by name. These are the fields gated by AdminSession in ADR-0007 §4.
	GraphQLOperations []GraphQLOperation `json:"graphqlOperations"`
	// ManagementRoutes lists the non-media, non-web-asset HTTP routes that
	// constitute the REST management surface alongside GraphQL.
	ManagementRoutes []server.ManagementRouteContract `json:"managementRoutes"`
}

// GraphQLOperation is one admin-tier top-level GraphQL field.
type GraphQLOperation struct {
	// Field is the lowercased top-level field name.
	Field string `json:"field"`
	// Effect is "read" or "write".
	Effect string `json:"effect"`
}

const schemaVersion = "1"

func main() {
	out := flag.String("out", "", "write to file instead of stdout")
	flag.Parse()

	fields := graph.SchemaManifest()

	// Filter to admin-tier only — these are the Console management operations
	// (ADR-0060/0061 management surface).
	var ops []GraphQLOperation
	for _, f := range fields {
		if f.Tier == "admin" {
			ops = append(ops, GraphQLOperation{
				Field:  f.Field,
				Effect: f.Effect,
			})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Field < ops[j].Field })

	manifest := MgmtManifest{
		SchemaVersion:     schemaVersion,
		GraphQLOperations: ops,
		ManagementRoutes:  server.ManagementRouteContracts(),
	}

	pretty, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mgmt-manifest:", err)
		os.Exit(1)
	}
	data := append(pretty, '\n')

	if *out != "" {
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "mgmt-manifest:", err)
			os.Exit(1)
		}
		return
	}
	if _, err := os.Stdout.Write(data); err != nil {
		fmt.Fprintln(os.Stderr, "mgmt-manifest:", err)
		os.Exit(1)
	}
}

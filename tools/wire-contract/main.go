// Command wire-contract emits the versioned wire-contract snapshot (ORG-10,
// ADR-0002): a single deterministic JSON document consolidating the hand-rolled
// dispatcher's schema — the flat top-level field manifest (effect/tier, H23-A),
// the reflected nested result shapes (H23-B), and the stable /v1 REST + SSE
// surface — stamped with a release version. It is the pinned wire contract a
// release ships so a client built against release vX can diff the exact field
// set and result shapes it was compiled against.
//
// Unversioned (drift-gate baseline; the conformance test pins this form):
//
//	go run ./tools/wire-contract > tools/wire-contract/testdata/wire-contract.json
//
// Versioned (release artifact, stamped with the release tag):
//
//	go run ./tools/wire-contract --version v1.2.3 > wire-contract.json
//
// Field inventory (XCONTRACT-10 drift-gate baseline; TestFieldInventory pins it) —
// the flat producer-field manifest (field/effect/tier/children) every client
// diffs its consumed+ignored field sets against for its completeness check:
//
//	go run ./tools/wire-contract --inventory > tools/wire-contract/testdata/field-inventory.json
//
// The conformance test TestWireContract regenerates the unversioned snapshot
// in-memory and byte-compares against the committed file, so the contract and
// the registries can never silently diverge.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/beamfall/core/tools/wire-contract/contract"
)

func main() {
	version := flag.String("version", "", "release version to stamp into the contract (e.g. v1.2.3); empty for the unversioned drift-gate baseline")
	inventory := flag.Bool("inventory", false, "emit the flat producer field-inventory (field/effect/tier/children) instead of the full contract snapshot; the artifact clients pin their completeness check against")
	registry := flag.String("registry", "", "contract registry used to source known drift and validate a version transition when publishing")
	refreshCurrent := flag.Bool("refresh-current", false, "repair the registry's current artifact only when the generated contract is a same-version additive superset")
	flag.Parse()
	if *refreshCurrent && (*registry == "" || *version == "") {
		fmt.Fprintln(os.Stderr, "wire-contract: --refresh-current requires --registry and --version")
		os.Exit(1)
	}
	// --refresh-current promises the emitted bytes ARE the validated same-version
	// contract; --inventory swaps the payload after validation, which would let a
	// refresh exit 0 while emitting something else entirely. Reject the combination.
	if *refreshCurrent && *inventory {
		fmt.Fprintln(os.Stderr, "wire-contract: --refresh-current is incompatible with --inventory (refresh must emit the validated contract bytes)")
		os.Exit(1)
	}

	snapshot := contract.Build(*version)
	if *registry != "" {
		previous, drift, err := publicationInputs(*registry)
		if err != nil {
			fmt.Fprintln(os.Stderr, "wire-contract:", err)
			os.Exit(1)
		}
		snapshot = contract.BuildWithKnownDrift(*version, drift)
		if *version != "" {
			validate := contract.ValidateVersionTransition
			if *refreshCurrent {
				validate = contract.ValidateAdditiveRefresh
			}
			if err := validate(previous, snapshot); err != nil {
				fmt.Fprintln(os.Stderr, "wire-contract:", err)
				os.Exit(1)
			}
		}
	}

	var payload any = snapshot
	if *inventory {
		payload = contract.BuildFieldInventory()
	}

	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "wire-contract:", err)
		os.Exit(1)
	}
	// Newline-terminated, matching the other committed JSON goldens.
	if _, err := os.Stdout.Write(append(pretty, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "wire-contract:", err)
		os.Exit(1)
	}
}

func publicationInputs(path string) ([]byte, []contract.KnownDrift, error) {
	// The path is an explicit operator-owned CLI input used only by the publication tool.
	//nolint:gosec
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var registry struct {
		Boundaries []struct {
			Boundary   string                `json:"boundary"`
			Artifact   string                `json:"artifact"`
			KnownDrift []contract.KnownDrift `json:"knownDrift"`
		} `json:"boundaries"`
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		return nil, nil, err
	}
	for _, boundary := range registry.Boundaries {
		if boundary.Boundary == "client-core-wire" {
			previous, err := os.ReadFile(boundary.Artifact)
			return previous, boundary.KnownDrift, err
		}
	}
	return nil, nil, fmt.Errorf("client-core-wire boundary missing from %s", path)
}

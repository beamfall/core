//go:build !beamfall_appstore

package conformance

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/beamfall/core/tools/wire-contract/contract"
)

// fieldInventoryPath is the committed producer field-inventory (XCONTRACT-10,
// ADR-0122 Amendment A §6): the flat projection of the wire contract each client
// pins its producer-field completeness check against. It lives beside the
// wire-contract snapshot under tools/ (not the conformance golden corpus): like
// the snapshot, it is the exporter's own drift-pinned baseline, exercised here.
const fieldInventoryPath = "../../tools/wire-contract/testdata/field-inventory.json"

// TestFieldInventory is the XCONTRACT-10 drift gate for the published
// field-inventory: it regenerates the inventory from the live registries and
// byte-compares against the committed artifact. Adding/reclassifying a top-level
// field or reshaping a result struct shifts the derived inventory and fails this
// test until it is regenerated:
//
//	go run ./tools/wire-contract --inventory > tools/wire-contract/testdata/field-inventory.json
//
// This keeps the Core-published producer-field truth (that clients diff their
// consumed+ignored sets against) from ever silently diverging from the wire
// contract.
func TestFieldInventory(t *testing.T) {
	live, err := json.MarshalIndent(contract.BuildFieldInventory(), "", "  ")
	if err != nil {
		t.Fatalf("marshal live field inventory: %v", err)
	}
	live = append(live, '\n')

	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(fieldInventoryPath, live, 0o644); err != nil {
			t.Fatalf("write %s: %v", fieldInventoryPath, err)
		}
		t.Logf("field inventory updated: %s", fieldInventoryPath)
		return
	}

	committed, err := os.ReadFile(fieldInventoryPath)
	if err != nil {
		t.Fatalf("read %s: %v — regenerate with `go run ./tools/wire-contract --inventory > tools/wire-contract/testdata/field-inventory.json`", fieldInventoryPath, err)
	}
	if string(committed) != string(live) {
		t.Errorf("committed field inventory is stale — regenerate with `go run ./tools/wire-contract --inventory > tools/wire-contract/testdata/field-inventory.json`")
	}
}

// TestFieldInventoryCoversEveryContractField asserts the inventory field set is
// exactly the wire contract's top-level field set — the inventory can never be a
// filtered subset (which would let a producer field escape every client's
// completeness check).
func TestFieldInventoryCoversEveryContractField(t *testing.T) {
	inventory := contract.BuildFieldInventory()
	snapshot := contract.Build("")

	inv := make(map[string]bool, len(inventory.Fields))
	for _, f := range inventory.Fields {
		inv[f.Field] = true
	}
	if len(inv) != len(inventory.Fields) {
		t.Fatalf("field inventory has duplicate field names: %d entries, %d unique", len(inventory.Fields), len(inv))
	}
	for _, f := range snapshot.Fields {
		if !inv[f.Field] {
			t.Errorf("contract field %q missing from field inventory", f.Field)
		}
	}
	if len(inventory.Fields) != len(snapshot.Fields) {
		t.Errorf("inventory has %d fields but contract has %d", len(inventory.Fields), len(snapshot.Fields))
	}
}

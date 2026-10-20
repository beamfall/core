package contract

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/graph"
)

func TestValidateVersionTransition(t *testing.T) {
	previous := []byte(`{"contractVersion":"1.1.0","fields":[{"field":"media","effect":"read"}]}`)
	additive := Snapshot{ContractVersion: "1.2.0"}
	if err := unmarshalSnapshotForTest(previous, &additive); err != nil {
		t.Fatal(err)
	}
	additive.ContractVersion = "1.2.0"
	additive.Fields = append(additive.Fields, additive.Fields[0])
	additive.Fields[1].Field = "sessions"
	if err := ValidateVersionTransition(previous, additive); err != nil {
		t.Fatalf("additive minor change rejected: %v", err)
	}

	breaking := additive
	breaking.Fields = breaking.Fields[1:]
	if err := ValidateVersionTransition(previous, breaking); err == nil {
		t.Fatal("breaking minor change passed without a major bump")
	}
	breaking.ContractVersion = "2.0.0"
	if err := ValidateVersionTransition(previous, breaking); err != nil {
		t.Fatalf("breaking major change rejected: %v", err)
	}
}

func TestValidateAdditiveRefresh(t *testing.T) {
	current := Snapshot{
		ContractVersion: "1.8.0",
		Fields:          []graph.SchemaField{graphFieldForTest("media", "read")},
	}
	previous, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	previous = append(previous, '\n')
	if err := ValidateAdditiveRefresh(previous, current); err != nil {
		t.Fatalf("byte-identical same-version refresh rejected: %v", err)
	}

	additive := current
	additive.Fields = append(additive.Fields, graphFieldForTest("sessions", "read"))
	if err := ValidateAdditiveRefresh(previous, additive); err == nil || !strings.Contains(err.Error(), "version bump") {
		t.Fatalf("additive same-version content drift error = %v, want version-bump rejection", err)
	}

	breaking := additive
	breaking.Fields = breaking.Fields[1:]
	if err := ValidateAdditiveRefresh(previous, breaking); err == nil {
		t.Fatal("breaking same-version refresh passed")
	}
	additive.ContractVersion = "1.8.1"
	if err := ValidateAdditiveRefresh(previous, additive); err == nil {
		t.Fatal("version-changing refresh passed")
	}
}

func graphFieldForTest(field, effect string) graph.SchemaField {
	return graph.SchemaField{Field: field, Effect: effect}
}

func unmarshalSnapshotForTest(raw []byte, snapshot *Snapshot) error {
	return json.Unmarshal(raw, snapshot)
}

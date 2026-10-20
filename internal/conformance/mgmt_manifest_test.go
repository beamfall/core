package conformance

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/beamfall/core/internal/graph"
)

type mgmtManifest struct {
	GraphQLOperations []struct {
		Field  string `json:"field"`
		Effect string `json:"effect"`
	} `json:"graphqlOperations"`
}

func TestManagementManifestMatchesAdminSchema(t *testing.T) {
	data, err := os.ReadFile(mgmtManifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", mgmtManifestPath, err)
	}
	var manifest mgmtManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode %s: %v", mgmtManifestPath, err)
	}

	admin := map[string]string{}
	for _, f := range graph.SchemaManifest() {
		if f.Tier == "admin" {
			admin[f.Field] = f.Effect
		}
	}
	seen := map[string]bool{}
	for _, op := range manifest.GraphQLOperations {
		if admin[op.Field] != op.Effect {
			t.Fatalf("management manifest operation %q=%q not in live admin schema %v", op.Field, op.Effect, admin[op.Field])
		}
		seen[op.Field] = true
	}
	for field, effect := range admin {
		if !seen[field] {
			t.Fatalf("management manifest missing live admin operation %q=%q", field, effect)
		}
	}
}

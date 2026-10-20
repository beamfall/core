package conformance

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/tools/wire-contract/contract"
)

// TestPublishedSystemSurfaceDisclosureContract pins the disclosure decision on
// the REST and GraphQL client integration surfaces. Reading the newest
// published artifact (not the live builder) prevents a code-only field from
// satisfying conformance.
func TestPublishedSystemSurfaceDisclosureContract(t *testing.T) {
	if graph.SystemSurfaceDisclosureSnapshotVersion != server.SystemSurfaceDisclosureSnapshotVersion {
		t.Fatalf(
			"GraphQL disclosure snapshot version = %q, REST = %q",
			graph.SystemSurfaceDisclosureSnapshotVersion,
			server.SystemSurfaceDisclosureSnapshotVersion,
		)
	}

	published, err := newestWireContractArtifact(".")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(published.Path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot contract.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}

	for surface, shape := range map[string]contract.WireShape{
		"manifest":     snapshot.Manifest.Response,
		"playbackInfo": snapshot.PlaybackInfo.Response,
	} {
		disclosure, ok := wireShapeField(shape, "systemSurfaceDisclosure")
		if !ok {
			t.Fatalf("published %s response has no top-level systemSurfaceDisclosure", surface)
		}
		for _, field := range []string{"snapshotVersion", "metadataAllowed"} {
			if !wireShapeHasDirectField(disclosure, field) {
				t.Fatalf("published %s systemSurfaceDisclosure has no %q", surface, field)
			}
		}
	}

	navigationManifest, ok := nestedSchemaField(snapshot.Nested, "navigationmanifest")
	if !ok {
		t.Fatal("published contract has no GraphQL navigationmanifest field")
	}
	disclosure, ok := nestedSchemaChild(navigationManifest.Children, "systemSurfaceDisclosure")
	if !ok {
		t.Fatal("published GraphQL navigationmanifest has no systemSurfaceDisclosure")
	}
	for _, field := range []string{"snapshotVersion", "metadataAllowed"} {
		if _, ok := nestedSchemaChild(disclosure.Children, field); !ok {
			t.Fatalf("published GraphQL navigationmanifest.systemSurfaceDisclosure has no %q", field)
		}
	}
}

func nestedSchemaField(fields []graph.NestedSchemaField, name string) (graph.NestedSchemaField, bool) {
	for _, field := range fields {
		if field.Field == name {
			return field, true
		}
	}
	return graph.NestedSchemaField{}, false
}

func nestedSchemaChild(fields []graph.NestedField, name string) (graph.NestedField, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return graph.NestedField{}, false
}

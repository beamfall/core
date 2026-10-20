package conformance

import (
	"os"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func TestFrozenSchemaSurface(t *testing.T) {
	data, err := os.ReadFile("testdata/schema-freeze.json")
	if err != nil {
		t.Fatalf("read frozen schema: %v", err)
	}
	if err := store.VerifyFrozenSchema(os.DirFS(repoRoot(t)), data); err != nil {
		t.Fatal(err)
	}
}

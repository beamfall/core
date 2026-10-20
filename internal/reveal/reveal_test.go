package reveal

import "testing"

func TestRegistryIsInstanceScoped(t *testing.T) {
	first := NewRegistry()
	second := NewRegistry()

	first.Register(Surface{ID: "navigationManifest", Class: ContentJSON})

	if len(first.All()) != 1 {
		t.Fatalf("first registry len = %d, want 1", len(first.All()))
	}
	if len(second.All()) != 0 {
		t.Fatalf("second registry len = %d, want 0", len(second.All()))
	}
}

func TestRegistryIgnoresDuplicateSurface(t *testing.T) {
	registry := NewRegistry()

	registry.Register(Surface{ID: "navigationManifest", Class: ContentJSON})
	registry.Register(Surface{ID: "navigationManifest", Class: ContentJSON})

	if len(registry.All()) != 1 {
		t.Fatalf("registry len = %d, want 1", len(registry.All()))
	}
}

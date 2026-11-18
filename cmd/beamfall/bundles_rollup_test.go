package main

import (
	"slices"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

// TestFirstPartyContainerRollUpRelations pins the genre-shelf roll-up to the
// first-party declarations: only filmtv's season→show chain qualifies, so a TV
// library's genre shelf lists series while music, photos and the rest keep
// listing items (CORE-2424).
func TestFirstPartyContainerRollUpRelations(t *testing.T) {
	registry, err := plugin.NewRegistry(firstPartyBundles())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	members, parents := registry.ContainerRollUpRelations()
	if !slices.Equal(members, []string{"season-episode"}) {
		t.Fatalf("members = %v, want [season-episode]", members)
	}
	if !slices.Equal(parents, []string{"season-of"}) {
		t.Fatalf("parents = %v, want [season-of]", parents)
	}

	var nilRegistry *plugin.Registry
	if m, p := nilRegistry.ContainerRollUpRelations(); m != nil || p != nil {
		t.Fatalf("nil registry = %v, %v, want nil, nil", m, p)
	}
}

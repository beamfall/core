package pathmap_test

import (
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/migrate/pathmap"
)

// TestDetectCollisionsReportsNtoOne proves an N-source→1-Beamfall-path collision
// is detected as one record naming every distinct contributing source, while a
// target reached by a single source is not a collision.
func TestDetectCollisionsReportsNtoOne(t *testing.T) {
	got := pathmap.DetectCollisions([]pathmap.Mapping{
		{Source: "/mnt/a/A.mkv", Target: "/data/A.mkv"},
		{Source: "/mnt/b/A.mkv", Target: "/data/A.mkv"},
		{Source: "/mnt/a/Solo.mkv", Target: "/data/Solo.mkv"},
	})
	want := []pathmap.Collision{
		{Target: "/data/A.mkv", Sources: []string{"/mnt/a/A.mkv", "/mnt/b/A.mkv"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectCollisions = %+v; want %+v", got, want)
	}
}

// TestDetectCollisionsDedupesSameSource proves the same (source,target) pair
// repeated is one contributor, not a collision — only DISTINCT source paths
// onto one target count.
func TestDetectCollisionsDedupesSameSource(t *testing.T) {
	got := pathmap.DetectCollisions([]pathmap.Mapping{
		{Source: "/mnt/a/A.mkv", Target: "/data/A.mkv"},
		{Source: "/mnt/a/A.mkv", Target: "/data/A.mkv"},
	})
	if len(got) != 0 {
		t.Fatalf("DetectCollisions = %+v; want no collision for a repeated same-source pair", got)
	}
}

// TestDetectCollisionsSortedByTarget proves multiple collisions arrive sorted by
// target so the emitted report lines are deterministic.
func TestDetectCollisionsSortedByTarget(t *testing.T) {
	got := pathmap.DetectCollisions([]pathmap.Mapping{
		{Source: "/s2/Z.mkv", Target: "/data/Z.mkv"},
		{Source: "/s1/Z.mkv", Target: "/data/Z.mkv"},
		{Source: "/s2/A.mkv", Target: "/data/A.mkv"},
		{Source: "/s1/A.mkv", Target: "/data/A.mkv"},
	})
	if len(got) != 2 || got[0].Target != "/data/A.mkv" || got[1].Target != "/data/Z.mkv" {
		t.Fatalf("DetectCollisions = %+v; want two collisions sorted A before Z", got)
	}
}

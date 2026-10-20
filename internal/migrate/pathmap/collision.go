package pathmap

import "sort"

// Mapping is one source path that resolved to a Beamfall path (after any prefix
// rewrite). The caller records one per path-matched import item.
type Mapping struct {
	Source string
	Target string
}

// Collision is N distinct source paths that resolved onto ONE Beamfall path.
// Their behavioral state merges additively under the ADR-0037
// §2.4 gate; this record exists so that union is reported explicitly and never
// silent. Sources are the distinct contributing source paths, sorted.
type Collision struct {
	Target  string
	Sources []string
}

// DetectCollisions groups mappings by Beamfall Target and returns the targets
// that more than one DISTINCT source path resolved onto, sorted by Target. A
// target reached by a single source (or the same source repeated) is not a
// collision. Duplicate (source,target) pairs collapse — the same source landing
// on the same target twice is one contributor, not a collision.
func DetectCollisions(mappings []Mapping) []Collision {
	sourcesByTarget := groupDistinctSources(mappings)
	collisions := make([]Collision, 0)
	for target, sources := range sourcesByTarget {
		if len(sources) < 2 {
			continue
		}
		sort.Strings(sources)
		collisions = append(collisions, Collision{Target: target, Sources: sources})
	}
	sort.Slice(collisions, func(i, j int) bool { return collisions[i].Target < collisions[j].Target })
	return collisions
}

// groupDistinctSources collects the set of distinct source paths per target,
// deduping so a repeated (source,target) pair counts once.
func groupDistinctSources(mappings []Mapping) map[string][]string {
	seen := map[string]map[string]bool{}
	for _, m := range mappings {
		if seen[m.Target] == nil {
			seen[m.Target] = map[string]bool{}
		}
		seen[m.Target][m.Source] = true
	}
	out := make(map[string][]string, len(seen))
	for target, sources := range seen {
		list := make([]string, 0, len(sources))
		for source := range sources {
			list = append(list, source)
		}
		out[target] = list
	}
	return out
}

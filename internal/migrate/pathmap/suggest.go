package pathmap

import (
	"sort"
	"strings"
)

// maxSuggestionSamples bounds the example source paths carried on a Suggestion,
// so a cluster of thousands of hits still previews cheaply.
const maxSuggestionSamples = 3

// Suggestion is a candidate prefix rewrite rule the wizard proposes, derived by
// clustering unmatched source paths that share the same head-difference against
// their located Beamfall counterparts. Hits is how many unmatched source paths
// the rule would rewrite; Samples are a bounded set of example sources for the
// operator to preview before apply (ADR-0178 §3: suggested rules are the primary
// flow, previewable before apply).
type Suggestion struct {
	Rule    Rule
	Hits    int
	Samples []string
}

// Suggest is the suggestion-clustering entry point. For each
// unmatched source path, resolve finds a Beamfall path that names the same file
// under a different mount (typically by shared basename/suffix); the differing
// heads become a candidate (SourcePrefix → TargetPrefix) rule. Identical
// candidates cluster into one Suggestion with a per-rule hit count. The result
// is sorted by hit count (desc), then SourcePrefix, then TargetPrefix, so the
// highest-impact rule leads and the ordering is deterministic. A source path
// resolve cannot place, or one whose heads do not actually differ, contributes
// no suggestion.
func Suggest(unmatched []string, resolve func(source string) (string, bool), platform Platform) []Suggestion {
	clusters := clusterSuggestions(unmatched, resolve, platform)
	suggestions := make([]Suggestion, 0, len(clusters))
	for _, s := range clusters {
		suggestions = append(suggestions, *s)
	}
	sortSuggestions(suggestions)
	return suggestions
}

// clusterSuggestions accumulates one Suggestion per distinct candidate rule,
// keyed by the rule's source+target prefix pair, counting hits and collecting a
// bounded sample of contributing source paths.
func clusterSuggestions(unmatched []string, resolve func(source string) (string, bool), platform Platform) map[Rule]*Suggestion {
	clusters := map[Rule]*Suggestion{}
	for _, source := range unmatched {
		beamfall, ok := resolve(source)
		if !ok {
			continue
		}
		rule, ok := commonSuffixSplit(source, beamfall, platform)
		if !ok {
			continue
		}
		accumulate(clusters, rule, source)
	}
	return clusters
}

// accumulate records one source path against its candidate rule, incrementing
// the hit count and appending to the bounded sample set.
func accumulate(clusters map[Rule]*Suggestion, rule Rule, source string) {
	cluster := clusters[rule]
	if cluster == nil {
		cluster = &Suggestion{Rule: rule}
		clusters[rule] = cluster
	}
	cluster.Hits++
	if len(cluster.Samples) < maxSuggestionSamples {
		cluster.Samples = append(cluster.Samples, source)
	}
}

// commonSuffixSplit derives a candidate rewrite rule from a source path and the
// Beamfall path that names the same file: it strips the longest shared trailing
// path segments and returns the differing heads as (SourcePrefix, TargetPrefix).
// Comparison is platform-aware (Windows folds case and treats `\` as `/`). It
// returns ok=false when no basename is shared, when the source head is empty, or
// when the heads do not differ (nothing to rewrite).
func commonSuffixSplit(source, beamfall string, platform Platform) (Rule, bool) {
	rs := &Ruleset{platform: platform}
	srcSegs := strings.Split(rs.normalize(source), "/")
	dstSegs := strings.Split(rs.normalize(beamfall), "/")
	shared := sharedTrailingSegments(srcSegs, dstSegs, rs)
	if shared == 0 {
		return Rule{}, false
	}
	srcPrefix := strings.Join(srcSegs[:len(srcSegs)-shared], "/")
	dstPrefix := strings.Join(dstSegs[:len(dstSegs)-shared], "/")
	if srcPrefix == "" || rs.fold(srcPrefix) == rs.fold(dstPrefix) {
		return Rule{}, false
	}
	return Rule{SourcePrefix: srcPrefix, TargetPrefix: dstPrefix}, true
}

// sharedTrailingSegments counts how many trailing path segments two split paths
// have in common, compared under the platform's case folding.
func sharedTrailingSegments(a, b []string, rs *Ruleset) int {
	shared := 0
	for shared < len(a) && shared < len(b) {
		as := a[len(a)-1-shared]
		bs := b[len(b)-1-shared]
		if as == "" || rs.fold(as) != rs.fold(bs) {
			break
		}
		shared++
	}
	return shared
}

// sortSuggestions orders suggestions by hit count (desc) then SourcePrefix then
// TargetPrefix, so the highest-impact rule leads and ties are deterministic.
func sortSuggestions(suggestions []Suggestion) {
	sort.Slice(suggestions, func(i, j int) bool {
		if suggestions[i].Hits != suggestions[j].Hits {
			return suggestions[i].Hits > suggestions[j].Hits
		}
		if suggestions[i].Rule.SourcePrefix != suggestions[j].Rule.SourcePrefix {
			return suggestions[i].Rule.SourcePrefix < suggestions[j].Rule.SourcePrefix
		}
		return suggestions[i].Rule.TargetPrefix < suggestions[j].Rule.TargetPrefix
	})
}

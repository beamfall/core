package pathmap_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/migrate/pathmap"
)

// suffixResolver is a test stand-in for the store's "find a Beamfall file that
// names this source file" lookup: it maps a source path to a Beamfall path by
// basename when one exists. It models the moved-mount case the suggestion flow
// exists for.
func suffixResolver(byBase map[string]string) func(string) (string, bool) {
	return func(source string) (string, bool) {
		base := source[strings.LastIndex(source, "/")+1:]
		beamfall, ok := byBase[base]
		return beamfall, ok
	}
}

// TestSuggestClustersByHitCount proves the primary suggestion flow (ADR-0178
// §3): unmatched source paths under one moved mount cluster into a single
// candidate rule at the mount boundary (the maximal shared suffix is stripped,
// so the rule generalizes to the whole subtree), carrying a per-rule hit count,
// sorted highest-impact first. Two different moved mounts yield two rules.
func TestSuggestClustersByHitCount(t *testing.T) {
	unmatched := []string{
		"/mnt/oldnas/Movies/A.mkv",
		"/mnt/oldnas/Movies/B.mkv",
		"/mnt/oldnas/Shows/C.mkv",
		"/mnt/olddisk/Music/D.mp3",
	}
	beamfall := suffixResolver(map[string]string{
		"A.mkv": "/data/Movies/A.mkv",
		"B.mkv": "/data/Movies/B.mkv",
		"C.mkv": "/data/Shows/C.mkv",
		"D.mp3": "/backup/Music/D.mp3",
	})
	got := pathmap.Suggest(unmatched, beamfall, pathmap.POSIX)
	want := []pathmap.Suggestion{
		{Rule: pathmap.Rule{SourcePrefix: "/mnt/oldnas", TargetPrefix: "/data"}, Hits: 3, Samples: []string{"/mnt/oldnas/Movies/A.mkv", "/mnt/oldnas/Movies/B.mkv", "/mnt/oldnas/Shows/C.mkv"}},
		{Rule: pathmap.Rule{SourcePrefix: "/mnt/olddisk", TargetPrefix: "/backup"}, Hits: 1, Samples: []string{"/mnt/olddisk/Music/D.mp3"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Suggest = %+v; want %+v", got, want)
	}
}

// TestSuggestSkipsUnresolvableAndNonDivergent proves a source path Beamfall can
// not place yields no suggestion, and a path whose head already matches Beamfall
// (nothing to rewrite) is not suggested either.
func TestSuggestSkipsUnresolvableAndNonDivergent(t *testing.T) {
	unmatched := []string{
		"/mnt/oldnas/Movies/A.mkv", // resolvable, divergent head → suggested
		"/mnt/oldnas/Movies/X.mkv", // Beamfall has no such file → skipped
		"/data/Movies/B.mkv",       // head already equals Beamfall → skipped
	}
	beamfall := suffixResolver(map[string]string{
		"A.mkv": "/data/Movies/A.mkv",
		"B.mkv": "/data/Movies/B.mkv",
	})
	got := pathmap.Suggest(unmatched, beamfall, pathmap.POSIX)
	want := []pathmap.Suggestion{
		{Rule: pathmap.Rule{SourcePrefix: "/mnt/oldnas", TargetPrefix: "/data"}, Hits: 1, Samples: []string{"/mnt/oldnas/Movies/A.mkv"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Suggest = %+v; want %+v", got, want)
	}
}

// TestSuggestAppliesRaisesResolution proves the loop closes: a suggested rule,
// built into a Ruleset, rewrites the previously-unmatched source paths onto the
// Beamfall paths that name the same files.
func TestSuggestAppliesRaisesResolution(t *testing.T) {
	unmatched := []string{"/mnt/oldnas/Movies/A.mkv", "/mnt/oldnas/Movies/B.mkv"}
	beamfall := suffixResolver(map[string]string{
		"A.mkv": "/data/Movies/A.mkv",
		"B.mkv": "/data/Movies/B.mkv",
	})
	suggestions := pathmap.Suggest(unmatched, beamfall, pathmap.POSIX)
	if len(suggestions) != 1 {
		t.Fatalf("Suggest returned %d rules; want 1", len(suggestions))
	}
	rs := pathmap.NewRuleset(pathmap.POSIX, suggestions[0].Rule)
	for _, u := range unmatched {
		got, ok := rs.Rewrite(u)
		wantBeamfall, _ := beamfall(u)
		if !ok || got != wantBeamfall {
			t.Fatalf("applied suggested rule to %q = %q,%v; want %q", u, got, ok, wantBeamfall)
		}
	}
}

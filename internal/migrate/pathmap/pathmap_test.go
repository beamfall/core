package pathmap_test

import (
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/migrate/pathmap"
)

// TestRewriteLongestMatchWins proves the ADR-0178 §3 precedence: among several
// matching rules the one with the longest source prefix rewrites the path, even
// when a shorter prefix is declared first.
func TestRewriteLongestMatchWins(t *testing.T) {
	rs := pathmap.NewRuleset(pathmap.POSIX,
		pathmap.Rule{SourcePrefix: "/mnt", TargetPrefix: "/wrong"},
		pathmap.Rule{SourcePrefix: "/mnt/oldnas/Movies", TargetPrefix: "/data/Movies"},
		pathmap.Rule{SourcePrefix: "/mnt/oldnas", TargetPrefix: "/also-wrong"},
	)
	got, ok := rs.Rewrite("/mnt/oldnas/Movies/A.mkv")
	if !ok || got != "/data/Movies/A.mkv" {
		t.Fatalf("Rewrite = %q,%v; want /data/Movies/A.mkv,true (longest prefix must win)", got, ok)
	}
}

// TestRewriteTieBreaksByDeclaredOrder proves that when two rules match with
// equally-long source prefixes, the earlier-declared rule wins.
func TestRewriteTieBreaksByDeclaredOrder(t *testing.T) {
	first := pathmap.NewRuleset(pathmap.POSIX,
		pathmap.Rule{SourcePrefix: "/mnt/media", TargetPrefix: "/first"},
		pathmap.Rule{SourcePrefix: "/mnt/media", TargetPrefix: "/second"},
	)
	if got, _ := first.Rewrite("/mnt/media/A.mkv"); got != "/first/A.mkv" {
		t.Fatalf("Rewrite = %q; want /first/A.mkv (first declared wins the tie)", got)
	}
	// Reversing declaration order flips the winner — confirming order, not some
	// incidental lexical property, is the tie-break.
	second := pathmap.NewRuleset(pathmap.POSIX,
		pathmap.Rule{SourcePrefix: "/mnt/media", TargetPrefix: "/second"},
		pathmap.Rule{SourcePrefix: "/mnt/media", TargetPrefix: "/first"},
	)
	if got, _ := second.Rewrite("/mnt/media/A.mkv"); got != "/second/A.mkv" {
		t.Fatalf("Rewrite = %q; want /second/A.mkv (declared order drives the tie-break)", got)
	}
}

// TestRewriteCaseFoldByPlatform proves case folding follows the source platform:
// a Windows-origin rule matches a differently-cased source path, while the same
// mismatch under POSIX does not match (POSIX paths are case-sensitive).
func TestRewriteCaseFoldByPlatform(t *testing.T) {
	win := pathmap.NewRuleset(pathmap.Windows,
		pathmap.Rule{SourcePrefix: `E:\Media`, TargetPrefix: "/data"},
	)
	got, ok := win.Rewrite(`e:\media\Show\Ep.mkv`)
	if !ok || got != "/data/Show/Ep.mkv" {
		t.Fatalf("Windows Rewrite = %q,%v; want /data/Show/Ep.mkv,true (case-insensitive + `\\`→`/`)", got, ok)
	}
	posix := pathmap.NewRuleset(pathmap.POSIX,
		pathmap.Rule{SourcePrefix: "/Media", TargetPrefix: "/data"},
	)
	if got, ok := posix.Rewrite("/media/A.mkv"); ok {
		t.Fatalf("POSIX Rewrite = %q,%v; want no match (POSIX is case-sensitive)", got, ok)
	}
}

// TestRewriteSeparatorNormalization proves Windows `\` separators normalize to
// `/` for both matching and output, and that a rule written with a trailing
// separator behaves like one without.
func TestRewriteSeparatorNormalization(t *testing.T) {
	rs := pathmap.NewRuleset(pathmap.Windows,
		pathmap.Rule{SourcePrefix: `D:\Shares\Media\`, TargetPrefix: "/srv/media/"},
	)
	got, ok := rs.Rewrite(`D:\Shares\Media\Films\A.mkv`)
	if !ok || got != "/srv/media/Films/A.mkv" {
		t.Fatalf("Rewrite = %q,%v; want /srv/media/Films/A.mkv,true", got, ok)
	}
}

// TestRewriteBoundaryAndNoMatch proves a prefix only matches on a segment
// boundary (never a partial segment) and that a non-matching path passes through
// unchanged, so an empty/nil ruleset is a no-op.
func TestRewriteBoundaryAndNoMatch(t *testing.T) {
	rs := pathmap.NewRuleset(pathmap.POSIX,
		pathmap.Rule{SourcePrefix: "/mnt/media", TargetPrefix: "/data"},
	)
	// /mnt/mediabank must NOT match /mnt/media (partial segment).
	if got, ok := rs.Rewrite("/mnt/mediabank/A.mkv"); ok {
		t.Fatalf("Rewrite = %q,%v; want no match on a partial segment", got, ok)
	}
	// Exact-prefix path (no remainder) rewrites to the bare target.
	if got, ok := rs.Rewrite("/mnt/media"); !ok || got != "/data" {
		t.Fatalf("Rewrite = %q,%v; want /data,true for an exact-prefix path", got, ok)
	}
	// An unrelated path is untouched.
	if got, ok := rs.Rewrite("/somewhere/else.mkv"); ok || got != "/somewhere/else.mkv" {
		t.Fatalf("Rewrite = %q,%v; want passthrough for a non-matching path", got, ok)
	}
	// A nil ruleset is a safe no-op.
	var none *pathmap.Ruleset
	if got, ok := none.Rewrite("/x/y.mkv"); ok || got != "/x/y.mkv" {
		t.Fatalf("nil Rewrite = %q,%v; want passthrough", got, ok)
	}
}

// TestPreviewReportsRuleIndex proves Preview reports the before→after mapping and
// which rule fired per sample (-1 when none), matching what Rewrite would apply.
func TestPreviewReportsRuleIndex(t *testing.T) {
	rs := pathmap.NewRuleset(pathmap.POSIX,
		pathmap.Rule{SourcePrefix: "/mnt/a", TargetPrefix: "/data/a"},
		pathmap.Rule{SourcePrefix: "/mnt/b", TargetPrefix: "/data/b"},
	)
	got := rs.Preview([]string{"/mnt/a/1.mkv", "/mnt/b/2.mkv", "/other/3.mkv"})
	want := []pathmap.PreviewRow{
		{Source: "/mnt/a/1.mkv", Rewritten: "/data/a/1.mkv", RuleIndex: 0},
		{Source: "/mnt/b/2.mkv", Rewritten: "/data/b/2.mkv", RuleIndex: 1},
		{Source: "/other/3.mkv", Rewritten: "/other/3.mkv", RuleIndex: -1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Preview = %+v; want %+v", got, want)
	}
}

// Package pathmap is the Migration Wizard's path-mapping seam: a
// prefix-rewrite normalization applied to source paths BEFORE the ADR-0037 §2.3
// path-match step, so paths that differ only by a moved mount, a Docker volume
// prefix, or a Windows drive letter still resolve to the same Beamfall Media.
//
// It is a normalization in front of the match chain, never a new match tier:
// the caller rewrites a source path, then hands the result to the unchanged
// §2.3 chain. The package is domain-agnostic (invariant #1) — it speaks only
// generic filesystem paths, never a source-system or domain name.
//
// Precedence is explicit: among a source path's matching rules the
// longest source prefix wins, and ties break by user-declared rule order. Case
// folding follows the SOURCE platform — Windows-origin paths compare
// case-insensitively (and treat `\` as a separator), POSIX-origin paths compare
// case-sensitively.
package pathmap

import "strings"

// Platform selects source-platform-appropriate comparison. It is
// the platform the SOURCE recorded its paths on, supplied by the wizard — never
// derived from the host the importer runs on.
type Platform int

const (
	// POSIX compares case-sensitively and treats only `/` as a separator.
	POSIX Platform = iota
	// Windows compares case-insensitively and treats `\` as a separator
	// (normalized to `/` before comparison).
	Windows
)

// Rule is one user-declared prefix rewrite: a source path at or under
// SourcePrefix is rewritten to sit under TargetPrefix instead, before the
// §2.3 path match. TargetPrefix is Beamfall's view of the same storage.
type Rule struct {
	SourcePrefix string
	TargetPrefix string
}

// Ruleset is an ordered set of prefix rewrite rules with a source platform. The
// slice order is the user-declared tie-break order.
type Ruleset struct {
	rules    []Rule
	platform Platform
}

// NewRuleset builds a Ruleset for a source platform. The rules are kept in
// declared order; that order is the documented tie-break when two rules match a
// path with equally-long source prefixes.
func NewRuleset(platform Platform, rules ...Rule) *Ruleset {
	return &Ruleset{rules: rules, platform: platform}
}

// Rewrite applies the longest-matching rule to path and returns the rewritten
// path and whether any rule fired. When several rules match, the one with the
// longest source prefix wins; ties break by declared rule order (earliest wins).
// Separators are normalized to `/` and, on Windows, comparison is
// case-insensitive; the untouched remainder keeps its original casing and is
// joined to the target prefix with `/`.
func (rs *Ruleset) Rewrite(path string) (string, bool) {
	if rs == nil || path == "" {
		return path, false
	}
	best := rs.winningRule(path)
	if best < 0 {
		return path, false
	}
	normPath := rs.normalize(path)
	prefixLen := len(trimTrailingSep(rs.normalize(rs.rules[best].SourcePrefix)))
	target := trimTrailingSep(rs.normalize(rs.rules[best].TargetPrefix))
	return target + normPath[prefixLen:], true
}

// winningRule returns the index of the rule Rewrite applies to path — the
// longest matching source prefix, ties broken by declared order (earliest wins)
// — or -1 when no rule matches. It is the single selection point Rewrite and
// Preview share so the two never disagree.
func (rs *Ruleset) winningRule(path string) int {
	foldPath := rs.fold(rs.normalize(path))
	best := -1
	bestLen := -1
	for i, rule := range rs.rules {
		prefix := trimTrailingSep(rs.normalize(rule.SourcePrefix))
		if prefix == "" || !hasPathPrefix(foldPath, rs.fold(prefix)) {
			continue
		}
		if len(prefix) > bestLen {
			best = i
			bestLen = len(prefix)
		}
	}
	return best
}

// normalize renders a path with `/` separators for comparison and output. On
// Windows both `\` and `/` are separators, so backslashes fold to `/`; on POSIX
// the path is returned unchanged (a literal `\` is an ordinary character).
func (rs *Ruleset) normalize(path string) string {
	if rs.platform == Windows {
		return strings.ReplaceAll(path, `\`, "/")
	}
	return path
}

// fold applies source-platform case folding for comparison only: Windows folds
// to lower case, POSIX is case-sensitive (returned unchanged).
func (rs *Ruleset) fold(path string) string {
	if rs.platform == Windows {
		return strings.ToLower(path)
	}
	return path
}

// hasPathPrefix reports whether path sits at or under prefix on a separator
// boundary — so `/mnt/media` matches `/mnt/media/A.mkv` and `/mnt/media` itself,
// but never `/mnt/mediabank/A.mkv`. Both arguments are already normalized/folded.
func hasPathPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

// trimTrailingSep drops a single trailing `/` so a rule written with or without
// it behaves identically. A bare "/" root is left intact.
func trimTrailingSep(prefix string) string {
	if len(prefix) > 1 && strings.HasSuffix(prefix, "/") {
		return strings.TrimRight(prefix, "/")
	}
	return prefix
}

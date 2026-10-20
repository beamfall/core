package updatecheck

import (
	"strconv"
	"strings"
)

// isNewer reports whether latest is a strictly newer release than current.
//
// Versions are compared as dot-separated numeric components (major.minor.patch
// …), tolerant of a leading "v" and of a trailing pre-release/build suffix
// (e.g. "1.6.0-beta.2") which is ignored for ordering — a finer pre-release
// ordering is not needed for an "update available" banner.
//
// If EITHER version is not parseable as a numeric version (notably the running
// "development" build, which has no release number), isNewer returns false: a
// non-release build is never nagged to "update", since there is nothing to
// compare it against.
func isNewer(latest, current string) bool {
	lv, ok := parseVersion(latest)
	if !ok {
		return false
	}
	cv, ok := parseVersion(current)
	if !ok {
		return false
	}
	return compare(lv, cv) > 0
}

// parseVersion splits a version string into its numeric components, dropping a
// leading "v" and any "-"/"+" pre-release/build suffix. It returns ok=false
// when there is no numeric component at all (e.g. "development").
func parseVersion(v string) ([]int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	// Drop a pre-release ("-beta.1") or build ("+abc") suffix.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return nil, false
	}
	return nums, true
}

// compare returns >0 if a is a higher version than b, <0 if lower, 0 if equal.
// Missing trailing components compare as zero ("1.6" == "1.6.0").
func compare(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

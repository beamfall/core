package ffmpeg

import (
	"strings"
	"testing"
)

// TestSandboxProfileDenyDefault asserts the generated SBPL profile is deny-all
// by default and grants exactly the allow-list paths: read-only
// for inputs, read+write for the staging/trash root, and no network.
func TestSandboxProfileDenyDefault(t *testing.T) {
	profile := sandboxProfile([]string{"/lib/in"}, []string{"/lib/stage"})

	mustContain := []string{
		"(deny default)",
		`(import "system.sb")`,
		"(deny network*)",
		`(allow file-read* (subpath "/lib/in"))`,
		`(allow file-read* (subpath "/lib/stage"))`,
		`(allow file-write* (subpath "/lib/stage"))`,
	}
	for _, want := range mustContain {
		if !strings.Contains(profile, want) {
			t.Errorf("profile missing %q\n---\n%s", want, profile)
		}
	}
	// A read-only input must never gain a write grant.
	if strings.Contains(profile, `(allow file-write* (subpath "/lib/in"))`) {
		t.Errorf("read-only input was granted write:\n%s", profile)
	}
}

// TestSandboxProfileQuotesMetacharacters asserts a path containing SBPL string
// metacharacters cannot break out of the quoted literal.
func TestSandboxProfileQuotesMetacharacters(t *testing.T) {
	profile := sandboxProfile([]string{`/lib/a"b\c`}, nil)
	if !strings.Contains(profile, `(subpath "/lib/a\"b\\c")`) {
		t.Errorf("metacharacters not escaped:\n%s", profile)
	}
}

// TestSandboxRuleSkipsEmpty asserts an empty allow-list path emits no rule.
func TestSandboxRuleSkipsEmpty(t *testing.T) {
	if got := sandboxRule("file-read*", ""); got != "" {
		t.Errorf("empty path produced a rule: %q", got)
	}
}

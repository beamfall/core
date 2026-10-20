// Locale-map display strings: every display-string field in
// the plugin/manifest contract is an optional locale map. The shorthand
// plain-string form is equivalent to {"en": …}; clients resolve against the
// device locale (BCP-47 best-match, e.g. fr-CA → fr) with English as the
// universal fallback. On the wire a single-English map serializes back to
// the plain-string shorthand, so English-only content keeps its old shape.
package plugin

import (
	"fmt"
	"regexp"
	"strings"
)

// localeTagPattern accepts lowercase BCP-47-shaped tags: a 2–3 letter
// primary subtag plus optional 2–8 char subtags ("en", "fr-ca", "pt-br").
// Lowercase-only keys keep resolution and serialization deterministic.
var localeTagPattern = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)

// validateLocaleString checks one declared locale map: a non-empty map must
// carry a non-blank "en" entry (the universal fallback anchor, ADR-0034 §1),
// lowercase BCP-47-shaped tags, and no blank values. An absent (nil/empty)
// map is legal — required-ness stays the caller's call.
func validateLocaleString(s LocaleString) error {
	if len(s) == 0 {
		return nil
	}
	if strings.TrimSpace(s["en"]) == "" {
		return fmt.Errorf("locale map must carry an %q entry (the English fallback anchor)", "en")
	}
	for tag, value := range s {
		if !localeTagPattern.MatchString(tag) {
			return fmt.Errorf("locale map tag %q is not a lowercase BCP-47 tag", tag)
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("locale map value for %q is blank", tag)
		}
	}
	return nil
}

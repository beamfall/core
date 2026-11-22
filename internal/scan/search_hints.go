package scan

import (
	"maps"

	sdk "github.com/beamfall/plugin-sdk"
)

// PluginTagHintResolver is the optional seam that reports a Plugin's
// Bundle.EmbeddedTagHints: exact embedded tag names mapped to the named search
// hints they fill (CORE-2333). The Identifier may implement it; without it a
// parse's hints come from the parser alone.
type PluginTagHintResolver interface {
	PluginEmbeddedTagHints(pluginID string) map[string]string
}

// SearchHints is the parse result's named search hints with the embedded tags
// filled in: each mapped tag with a non-empty value fills its hint when the
// parser left that hint absent or empty, so the parser's value wins. Tag
// values over the SDK's hint size limit are skipped, since a search cannot use
// a truncated value. The merged map must then pass the SDK's parse-hint
// validation; a parser whose hints fail it carries none, and identification
// searches without them. The result is nil when there are no hints.
func SearchHints(parsed ParsedMedia, tags, tagHints map[string]string) map[string]string {
	hints := maps.Clone(parsed.Hints)
	for tag, key := range tagHints {
		value := tags[tag]
		if value == "" || hints[key] != "" || !hintValueFits(key, value) {
			continue
		}
		if hints == nil {
			hints = map[string]string{}
		}
		hints[key] = value
	}
	if len(hints) == 0 || sdk.ValidateParseHints(sdk.ParsedMedia{Hints: hints}) != nil {
		return nil
	}
	return hints
}

// hintValueFits reports whether value is within the SDK's per-hint size
// limit, asking the SDK validator rather than restating the bound.
func hintValueFits(key, value string) bool {
	return sdk.ValidateParseHints(sdk.ParsedMedia{Hints: map[string]string{key: value}}) == nil
}

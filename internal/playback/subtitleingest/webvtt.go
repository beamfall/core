package subtitleingest

import "strings"

// WebVTT cue payloads are exposed to the browser DOM by the web player, so a cue
// carrying markup outside the WebVTT tag vocabulary is a documented XSS / cue-
// injection vector. SanitizeWebVTT neutralises it in two passes:
//
//  1. STYLE, REGION, and NOTE blocks are stripped entirely (see
//     stripNonCueBlocks). STYLE bodies are CSS and modern text-track renderers
//     do fetch `url(...)` from a `::cue` rule, so a STYLE block is a live
//     network-beacon vector, not just a markup one; validating a CSS subset
//     safely would mean writing a CSS parser as new attack surface, which is
//     worse than simply not carrying STYLE at all. REGION and NOTE carry no
//     rendering value our own player needs, so the same fail-closed default
//     applies to them instead of hand-rolling a second validator per block
//     type.
//  2. Every angle-bracket construct that is not a recognised WebVTT cue tag is
//     HTML-escaped so the browser renders it as literal text instead of
//     markup (SanitizeWebVTT's per-character scan). A recognised tag is
//     reduced to its allowlisted shape — name, an optional dot-suffixed class
//     list, and (for voice/language tags only) a restricted-charset
//     annotation — so any other attribute is silently dropped rather than
//     passed through, regardless of its name or how it is spelled.
//
// The WebVTT-allowed cue tags are a small closed set — class (<c>), italic
// (<i>), bold (<b>), underline (<u>), ruby (<ruby>/<rt>), voice (<v ...>),
// language (<lang ...>), and timestamp (<00:00:00.000>) tags, each with an
// optional class suffix (<c.loud>). Anything else — <script>, <img onerror=…>,
// <a href=…>, arbitrary HTML — is escaped. Allowed tags with no disallowed
// content pass through byte-for-byte, so a legitimate styled cue is unchanged.

// allowedVTTTags is the closed set of WebVTT cue tag names (lowercased, without
// the leading slash of a closing tag). Timestamp tags are matched separately by
// shape, not by name.
var allowedVTTTags = map[string]bool{
	"c":    true,
	"i":    true,
	"b":    true,
	"u":    true,
	"ruby": true,
	"rt":   true,
	"v":    true,
	"lang": true,
}

// annotatedVTTTags is the subset of allowedVTTTags that carries a free-text
// WebVTT annotation after the name/class (voice name, language code). No
// other tag has a legitimate use for anything past its name and classes.
var annotatedVTTTags = map[string]bool{
	"v":    true,
	"lang": true,
}

// SanitizeWebVTT returns vtt with STYLE/REGION/NOTE blocks removed and every
// non-WebVTT angle-bracket construct HTML-escaped. Recognised cue tags (and
// timestamp tags) are preserved, minus any attribute/annotation content that
// is not on the per-tag allowlist; a dangling "<" with no closing ">" is
// escaped. The transform is idempotent on already-safe input and never
// removes cue text — it only defangs markup and strips non-cue blocks.
func SanitizeWebVTT(vtt string) string {
	vtt = stripNonCueBlocks(vtt)
	var b strings.Builder
	b.Grow(len(vtt))
	for i := 0; i < len(vtt); {
		c := vtt[i]
		if c != '<' {
			b.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(vtt[i:], '>')
		if end < 0 {
			// Unterminated "<": no valid tag can follow, so escape and stop
			// treating the remainder as markup.
			b.WriteString(escapeLT(vtt[i:]))
			break
		}
		inner := vtt[i+1 : i+end] // tag body between the brackets
		if safe, ok := sanitizeVTTTag(inner); ok {
			b.WriteByte('<')
			b.WriteString(safe)
			b.WriteByte('>')
		} else {
			b.WriteString("&lt;")
			b.WriteString(escapeLT(inner))
			b.WriteString("&gt;")
		}
		i += end + 1
	}
	return b.String()
}

// stripNonCueBlocks removes every STYLE, REGION, and NOTE block from vtt.
// WebVTT blocks are separated by blank lines; the first block (the "WEBVTT"
// header) is always kept regardless of its content. A block is identified
// purely by its first line ("STYLE", "REGION", "NOTE", or "NOTE <comment>"),
// so a block missing its trailing blank line (truncated/malformed input) is
// still stripped up to EOF rather than passed through.
func stripNonCueBlocks(vtt string) string {
	lines := strings.Split(vtt, "\n")
	out := make([]string, 0, len(lines))
	sawHeader := false
	for i := 0; i < len(lines); {
		if strings.TrimRight(lines[i], "\r") == "" {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimRight(lines[j], "\r") != "" {
			j++
		}
		first := strings.TrimRight(lines[i], "\r")
		strip := sawHeader && isNonCueBlockHeader(first)
		if !strip {
			out = append(out, lines[i:j]...)
		}
		sawHeader = true
		i = j
	}
	return strings.Join(out, "\n")
}

// isNonCueBlockHeader reports whether a block's first line marks it as a
// STYLE, REGION, or NOTE block (single-line "NOTE ..." included).
func isNonCueBlockHeader(first string) bool {
	return first == "STYLE" || first == "REGION" || first == "NOTE" || strings.HasPrefix(first, "NOTE ") || strings.HasPrefix(first, "NOTE\t")
}

// sanitizeVTTTag inspects the raw text between "<" and ">" and returns the
// safe form to emit plus whether the tag is recognised at all. An
// unrecognised tag returns ("", false) so the caller escapes the original
// text. A recognised tag is reduced to name + optional dot-suffixed classes
// + (voice/lang only) a restricted-charset annotation — any other content
// (event-handler attributes, style=, href=, anything else) is dropped, not
// merely escaped, regardless of its name, case, or internal whitespace.
func sanitizeVTTTag(inner string) (string, bool) {
	if inner == "" {
		return "", false
	}
	if isTimestampTag(inner) {
		return inner, true
	}
	body := inner
	prefix := ""
	if strings.HasPrefix(body, "/") {
		prefix = "/"
		body = body[1:]
	}
	name := body
	rest := ""
	if idx := strings.IndexAny(body, ". \t"); idx >= 0 {
		name = body[:idx]
		rest = body[idx:]
	}
	if name == "" {
		return "", false
	}
	lower := strings.ToLower(name)
	if !allowedVTTTags[lower] {
		return "", false
	}
	classes, annotation := splitClassesAndAnnotation(rest)
	safe := prefix + name + classes
	if annotatedVTTTags[lower] && annotation != "" && isSafeAnnotation(annotation) {
		safe += " " + annotation
	}
	return safe, true
}

// splitClassesAndAnnotation consumes a leading run of ".class" segments
// (each restricted to [A-Za-z0-9_-], matching real WebVTT class syntax) and
// returns them verbatim as classes; whatever remains, with leading
// whitespace trimmed, is the candidate annotation. Because classes are built
// only from safe characters by construction, no separate class-charset check
// is needed: anything unsafe simply falls out of the class run and into the
// (separately validated) annotation.
func splitClassesAndAnnotation(rest string) (classes, annotation string) {
	i := 0
	for i < len(rest) && rest[i] == '.' {
		j := i + 1
		for j < len(rest) && isVTTClassChar(rest[j]) {
			j++
		}
		if j == i+1 {
			break // "." with no valid class chars following: not a class
		}
		i = j
	}
	classes = rest[:i]
	annotation = strings.TrimLeft(rest[i:], " \t")
	return classes, annotation
}

func isVTTClassChar(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// isSafeAnnotation reports whether s is safe to carry verbatim as a voice or
// language annotation: letters, digits, and a small set of punctuation found
// in real names/codes. Notably absent: '=', quotes, angle brackets, parens,
// colons, and '&' — the characters every confirmed attribute-injection and
// entity-escape bypass depends on. Any other character fails the whole
// annotation closed (dropped by the caller), not just the offending
// character, so no partial-strip logic can be evaded piecemeal.
func isSafeAnnotation(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == ' ', c == '.', c == ',', c == '\'', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// isTimestampTag reports whether inner is a WebVTT timestamp cue tag — digits,
// colons, and dots only, with at least one digit ("00:00:01.000"). These are
// mid-cue karaoke timestamps and are safe.
func isTimestampTag(inner string) bool {
	hasDigit := false
	for i := 0; i < len(inner); i++ {
		switch c := inner[i]; {
		case c >= '0' && c <= '9':
			hasDigit = true
		case c == ':' || c == '.':
		default:
			return false
		}
	}
	return hasDigit
}

// escapeLT escapes the two markup-significant bracket characters so escaped text
// cannot re-open a tag. "&" is left as-is: WebVTT entities are not an injection
// vector on their own, and re-escaping them would corrupt legitimate &amp; runs.
func escapeLT(s string) string {
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

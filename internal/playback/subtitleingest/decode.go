package subtitleingest

import (
	"strings"
	"unicode/utf8"
)

// Legacy sidecar files are routinely CP1252 (Windows-1252) or Latin-1 rather
// than UTF-8, and serving their bytes as UTF-8 renders mojibake for accented and
// smart-punctuation characters (ADR-0194 §5 charset sniffing). DecodeText
// normalises a text subtitle to UTF-8 before the WebVTT transform, keeping the
// ffmpeg-free sidecar path self-contained.
//
// Sniffing is byte-structural, not statistical: a file that already decodes as
// valid UTF-8 is returned unchanged (UTF-8 and ASCII are a strict superset of
// what a legitimate sidecar carries), so only genuinely non-UTF-8 bytes take the
// CP1252 fallback. CP1252 is the correct fallback because it maps every one of
// the 256 byte values to a defined character, so decoding never fails and a
// Latin-1 file (a CP1252 subset) round-trips identically.
func DecodeText(raw []byte) string {
	raw = stripBOM(raw)
	if utf8.Valid(raw) {
		return string(raw)
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, c := range raw {
		b.WriteRune(cp1252Rune(c))
	}
	return b.String()
}

// stripBOM removes a leading UTF-8 byte-order mark so the decoded text begins at
// the first real character (a WEBVTT header must be the first bytes).
func stripBOM(raw []byte) []byte {
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return raw[3:]
	}
	return raw
}

// cp1252Rune maps one Windows-1252 byte to its Unicode rune. Bytes 0x00–0x7F and
// 0xA0–0xFF follow ISO-8859-1 (identity into the Latin-1 code points); the
// 0x80–0x9F window is the CP1252-specific set (smart quotes, dashes, the Euro
// sign). The five undefined CP1252 slots map to U+FFFD, matching the WHATWG
// decoder so an undefined byte never silently becomes a control character.
func cp1252Rune(c byte) rune {
	if c < 0x80 || c >= 0xA0 {
		return rune(c)
	}
	return cp1252High[c-0x80]
}

// cp1252High maps the 0x80–0x9F byte window to its CP1252 runes. U+FFFD marks the
// five undefined positions (0x81, 0x8D, 0x8F, 0x90, 0x9D).
var cp1252High = [32]rune{
	0x20AC, 0xFFFD, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0xFFFD, 0x017D, 0xFFFD,
	0xFFFD, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0xFFFD, 0x017E, 0x0178,
}

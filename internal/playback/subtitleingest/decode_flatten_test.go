package subtitleingest

import (
	"strings"
	"testing"
)

func TestDecodeTextUTF8Passthrough(t *testing.T) {
	in := []byte("café — déjà vu\n")
	if got := DecodeText(in); got != "café — déjà vu\n" {
		t.Fatalf("DecodeText(utf8) = %q, want passthrough", got)
	}
}

func TestDecodeTextCP1252WithoutMojibake(t *testing.T) {
	// CP1252 bytes: 0xE9='é', 0x97='—' (em dash), 0x93/0x94 smart quotes.
	raw := []byte{'c', 'a', 'f', 0xE9, ' ', 0x97, ' ', 0x93, 'x', 0x94}
	got := DecodeText(raw)
	if !strings.Contains(got, "café") {
		t.Fatalf("DecodeText(cp1252) lost é: %q", got)
	}
	if !strings.Contains(got, "—") {
		t.Fatalf("DecodeText(cp1252) lost em dash: %q", got)
	}
	if strings.Contains(got, "�") {
		t.Fatalf("DecodeText(cp1252) produced replacement chars (mojibake): %q", got)
	}
}

func TestDecodeTextStripsBOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("WEBVTT")...)
	if got := DecodeText(raw); got != "WEBVTT" {
		t.Fatalf("DecodeText did not strip BOM: %q", got)
	}
}

func TestFlattenASSProducesTimedCues(t *testing.T) {
	ass := strings.Join([]string{
		"[Script Info]",
		"Title: x",
		"[V4+ Styles]",
		"Format: Name, Fontname",
		"[Events]",
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text",
		"Dialogue: 0,0:00:01.00,0:00:03.50,Default,,0,0,0,,{\\pos(1,2)}Hello, world",
		"Dialogue: 0,0:00:04.00,0:00:05.00,Default,,0,0,0,,Line one\\NLine two",
	}, "\n")
	out := FlattenASS(ass)
	if !strings.HasPrefix(out, "WEBVTT") {
		t.Fatalf("FlattenASS missing WEBVTT header: %q", out)
	}
	if !strings.Contains(out, "00:00:01.000 --> 00:00:03.500") {
		t.Fatalf("FlattenASS wrong timing: %q", out)
	}
	if !strings.Contains(out, "Hello, world") {
		t.Fatalf("FlattenASS dropped comma-bearing text: %q", out)
	}
	if strings.Contains(out, "\\pos") || strings.Contains(out, "{") {
		t.Fatalf("FlattenASS left override tags: %q", out)
	}
	if !strings.Contains(out, "Line one\nLine two") {
		t.Fatalf("FlattenASS did not convert \\N to newline: %q", out)
	}
}

func TestFlattenASSEmptyOnNoEvents(t *testing.T) {
	if got := FlattenASS("[Script Info]\nTitle: x\n"); strings.TrimSpace(got) != "WEBVTT" {
		t.Fatalf("FlattenASS(no events) = %q, want header only", got)
	}
}

func TestFlattenASSSortsCuesByStartTime(t *testing.T) {
	ass := strings.Join([]string{
		"[Events]",
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text",
		"Dialogue: 0,0:00:04.00,0:00:05.00,Default,,0,0,0,,Later",
		"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Earlier",
	}, "\n")

	out := FlattenASS(ass)
	earlier := strings.Index(out, "00:00:01.000 -->")
	later := strings.Index(out, "00:00:04.000 -->")
	if earlier < 0 || later < 0 || earlier > later {
		t.Fatalf("FlattenASS cue order = %q, want non-decreasing start times", out)
	}
}

func TestFlattenASSLineBreaksCannotForgeCueBoundary(t *testing.T) {
	ass := strings.Join([]string{
		"[Events]",
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text",
		"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,safe\\N\\N00:00:10.000 --> 00:00:11.000\\Nforged",
	}, "\n")

	out := FlattenASS(ass)
	blocks := strings.Split(strings.TrimSpace(out), "\n\n")
	if len(blocks) != 2 {
		t.Fatalf("FlattenASS emitted %d WebVTT blocks, want header plus one cue: %q", len(blocks), out)
	}
	if !strings.Contains(blocks[1], "safe\n00:00:10.000 --> 00:00:11.000\nforged") {
		t.Fatalf("FlattenASS did not preserve non-boundary line breaks: %q", out)
	}
}

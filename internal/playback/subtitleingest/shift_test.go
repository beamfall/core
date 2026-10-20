package subtitleingest

import (
	"strings"
	"testing"
)

func TestShiftWebVTTMovesCueTimings(t *testing.T) {
	in := "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello\n"
	out := ShiftWebVTT(in, 500)
	if !strings.Contains(out, "00:00:01.500 --> 00:00:03.000") {
		t.Fatalf("shift +500ms = %q", out)
	}
	if !strings.Contains(out, "Hello") {
		t.Fatalf("shift dropped cue text: %q", out)
	}
}

func TestShiftWebVTTNegativeClampsAtZero(t *testing.T) {
	in := "WEBVTT\n\n00:00:00.200 --> 00:00:02.000\nEarly\n"
	out := ShiftWebVTT(in, -1000)
	if !strings.Contains(out, "00:00:00.000 --> 00:00:01.000") {
		t.Fatalf("negative shift = %q, want start clamped to zero", out)
	}
}

func TestShiftWebVTTZeroIsIdentity(t *testing.T) {
	in := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHi\n"
	if got := ShiftWebVTT(in, 0); got != in {
		t.Fatalf("zero shift altered the document: %q", got)
	}
}

func TestShiftWebVTTPreservesCueSettingsAndText(t *testing.T) {
	in := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000 align:start line:90%\nA --> B arrow in text\n"
	out := ShiftWebVTT(in, 1000)
	if !strings.Contains(out, "align:start line:90%") {
		t.Fatalf("shift dropped cue settings: %q", out)
	}
	if !strings.Contains(out, "00:00:02.000 --> 00:00:03.000") {
		t.Fatalf("shift wrong timing: %q", out)
	}
}

func TestShiftWebVTTPreservesTimestampShapedCuePayloadWithIdentifier(t *testing.T) {
	in := "WEBVTT\n\ncue-id\n00:00:10.000 --> 00:00:11.000 align:start\n00:00:01.000 --> 00:00:02.000\nA --> B arrow in text\n"
	want := "WEBVTT\n\ncue-id\n00:00:10.500 --> 00:00:11.500 align:start\n00:00:01.000 --> 00:00:02.000\nA --> B arrow in text\n"
	if got := ShiftWebVTT(in, 500); got != want {
		t.Fatalf("shift altered cue payload:\n got=%q\nwant=%q", got, want)
	}
}

func TestShiftWebVTTPreservesTimestampShapedFlattenedPayload(t *testing.T) {
	ass := strings.Join([]string{
		"[Events]",
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text",
		"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,safe\\N00:00:10.000 --> 00:00:11.000\\NA --> B arrow in text\\Nforged",
	}, "\n")
	want := "WEBVTT\n\n00:00:01.500 --> 00:00:02.500\nsafe\n00:00:10.000 --> 00:00:11.000\nA --> B arrow in text\nforged\n\n"
	if got := ShiftWebVTT(FlattenASS(ass), 500); got != want {
		t.Fatalf("shift altered flattened cue payload:\n got=%q\nwant=%q", got, want)
	}
}

func TestShiftWebVTTSkipsNonCueBlocks(t *testing.T) {
	for _, header := range []string{"NOTE", "NOTE comment", "STYLE", "REGION"} {
		t.Run(header, func(t *testing.T) {
			in := "WEBVTT\n\n" + header + "\n00:00:01.000 --> 00:00:02.000\n\n00:00:03.000 --> 00:00:04.000\nText\n"
			want := "WEBVTT\n\n" + header + "\n00:00:01.000 --> 00:00:02.000\n\n00:00:03.500 --> 00:00:04.500\nText\n"
			if got := ShiftWebVTT(in, 500); got != want {
				t.Fatalf("shift altered %s block:\n got=%q\nwant=%q", header, got, want)
			}
		})
	}
}

func TestShiftWebVTTLeavesMalformedTimingUntouched(t *testing.T) {
	in := "WEBVTT\n\nnot:a:time --> also-bad\nText\n"
	if got := ShiftWebVTT(in, 500); got != in {
		t.Fatalf("malformed cue line was rewritten: %q", got)
	}
}

func TestShiftWebVTTAcceptsTwoPartTimestamps(t *testing.T) {
	in := "WEBVTT\n\n00:01.000 --> 00:02.000\nShort form\n"
	out := ShiftWebVTT(in, 1000)
	if !strings.Contains(out, "00:00:02.000 --> 00:00:03.000") {
		t.Fatalf("two-part timestamp shift = %q", out)
	}
}

func TestShiftWebVTTLeavesOverflowingTimestampUnchanged(t *testing.T) {
	in := "WEBVTT\n\n2562047788015215:00:00.000 --> 2562047788015215:00:01.000\nExtreme\n"
	if got := ShiftWebVTT(in, 1); got != in {
		t.Fatalf("overflowing timestamp wrapped or changed:\n got=%q\nwant=%q", got, in)
	}
}

func TestShiftWebVTTLeavesOverflowingAdditionUnchanged(t *testing.T) {
	in := "WEBVTT\n\n2562047788015:12:55.807 --> 2562047788015:12:55.807\nBoundary\n"
	if got := ShiftWebVTT(in, 1); got != in {
		t.Fatalf("overflowing shift addition wrapped or changed:\n got=%q\nwant=%q", got, in)
	}
}

func TestShiftWebVTTPreservesCRLF(t *testing.T) {
	in := "WEBVTT\r\n\r\n00:00:01.000 --> 00:00:02.000\r\nText\r\n"
	want := "WEBVTT\r\n\r\n00:00:01.001 --> 00:00:02.001\r\nText\r\n"
	if got := ShiftWebVTT(in, 1); got != want {
		t.Fatalf("CRLF shift changed line endings:\n got=%q\nwant=%q", got, want)
	}
}

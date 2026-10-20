package ffmpeg

import (
	"context"
	"strconv"
	"testing"
)

func TestSSEOFUnavailable(t *testing.T) {
	for _, detail := range []string{
		"Unrecognized option 'sseof'",
		"Option sseof not found",
		"option not found: sseof",
	} {
		if !sseofUnavailable(detail) {
			t.Fatalf("did not recognize unavailable -sseof: %q", detail)
		}
	}
	if sseofUnavailable("Invalid data found when processing input") {
		t.Fatal("ordinary input failure must not trigger absolute-seek retry")
	}
}

func TestParseOpeningTextCues(t *testing.T) {
	vtt := "WEBVTT\r\n\r\ncue-1\r\n00:00:02.000 --> 00:00:04.500 align:start\r\nPreviously on…\r\n\r\n00:03:01.000 --> 00:03:03.000\r\nToo late\r\n"
	cues := parseOpeningTextCues(vtt, 180)
	if len(cues) != 1 {
		t.Fatalf("cues = %+v, want one opening cue", cues)
	}
	if cues[0].StartSeconds != 2 || cues[0].EndSeconds != 4.5 || cues[0].Text != "Previously on…" {
		t.Fatalf("cue = %+v", cues[0])
	}
}

func TestParseOpeningTextCuesIgnoresMalformedTiming(t *testing.T) {
	vtt := "WEBVTT\n\nnot:a:time --> 00:00:02.000\nPreviously on\n"
	if cues := parseOpeningTextCues(vtt, 180); len(cues) != 0 {
		t.Fatalf("malformed timing produced cues: %+v", cues)
	}
}

func TestParseCueTimestampRejectsHourOverflow(t *testing.T) {
	hours := strconv.Itoa(int(^uint(0)>>1)/3600 + 1)
	if seconds, ok := parseCueTimestamp(hours + ":00:00.000"); ok {
		t.Fatalf("overflowing cue timestamp parsed as %v seconds", seconds)
	}
}

// io:ffmpeg-opening-text-cues
func TestOpeningTextCuesExtractsEmbeddedTextLocally(t *testing.T) {
	src := makeSRTMKV(t)
	cues, err := New().OpeningTextCues(context.Background(), src, 30)
	if err != nil {
		t.Fatalf("OpeningTextCues: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("cues = %+v, want two embedded cues", cues)
	}
	if cues[0].Text != "Hello sidecar" || cues[1].Text != "Second cue" {
		t.Fatalf("cue text = %+v", cues)
	}
}

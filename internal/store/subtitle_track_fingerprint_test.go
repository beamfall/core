package store

import (
	"bytes"
	"testing"
)

func TestSubtitleTrackFingerprintStableUnderRenameAndRemux(t *testing.T) {
	original := []byte("WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nHello.\n")

	fingerprint := func(t *testing.T, trackBytes []byte) string {
		t.Helper()
		got, err := SubtitleTrackFingerprint(bytes.NewReader(trackBytes))
		if err != nil {
			t.Fatalf("fingerprint subtitle track: %v", err)
		}
		return got
	}

	want := fingerprint(t, original)
	for _, unchanged := range []struct {
		name       string
		trackBytes []byte
	}{
		{name: "sidecar rename", trackBytes: bytes.Clone(original)},
		{name: "container remux", trackBytes: bytes.Clone(original)},
	} {
		t.Run(unchanged.name, func(t *testing.T) {
			if got := fingerprint(t, unchanged.trackBytes); got != want {
				t.Fatalf("fingerprint = %q, want stable %q", got, want)
			}
		})
	}

	for _, edited := range []struct {
		name       string
		trackBytes []byte
	}{
		{name: "cue content", trackBytes: []byte("WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nGoodbye.\n")},
		{name: "cue timing", trackBytes: []byte("WEBVTT\n\n00:00:01.500 --> 00:00:03.500\nHello.\n")},
	} {
		t.Run(edited.name, func(t *testing.T) {
			if got := fingerprint(t, edited.trackBytes); got == want {
				t.Fatalf("fingerprint remained %q after %s edit", got, edited.name)
			}
		})
	}
}

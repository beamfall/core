package scan

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func TestLocalMetadataClassFollowsFileAndProbe(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		probe probeOutcome
		want  store.MediaClass
	}{
		{"still by extension", "/r/a.jpg", probeOutcome{}, store.MediaClassImage},
		{"clip with video stream", "/r/clip.mov", probeOutcome{succeeded: true, hasVideo: true, hasAudio: true}, store.MediaClassVideo},
		{"audio only", "/r/a.m4a", probeOutcome{succeeded: true, hasAudio: true}, ""},
		{"unprobed non-image", "/r/clip.mp4", probeOutcome{}, ""},
	}
	for _, tc := range cases {
		if got := localMetadataClass(tc.path, tc.probe); got != tc.want {
			t.Errorf("%s: class = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestExtractLocalMetadataPassesClass(t *testing.T) {
	extractor := &fakeLocalExtractor{}
	got := extractLocalMetadata(context.Background(), store.MediaClassVideo, "/r", "/r/clip.mov", []LocalExtractor{extractor})
	if len(got) != 1 || len(extractor.calls) != 1 || extractor.calls[0] != "video|clip.mov" {
		t.Fatalf("metadata = %d, calls = %v, want one video call", len(got), extractor.calls)
	}
}

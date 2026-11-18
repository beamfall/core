package playback

import (
	"testing"

	"github.com/beamfall/core/internal/store"
)

func TestResolutionLabel(t *testing.T) {
	cases := []struct {
		width, height int
		want          string
	}{
		{0, 0, ""},
		{-1, 0, ""},
		{7680, 4320, Resolution8K},
		{3840, 2160, Resolution4K},
		{3840, 1600, Resolution4K}, // scope 4K keeps its class
		{4096, 1716, Resolution4K},
		{1920, 1080, Resolution1080p},
		{1920, 800, Resolution1080p},  // scope 1080p
		{1440, 1080, Resolution1080p}, // 4:3 HDV
		{0, 1080, Resolution1080p},    // height only
		{1280, 720, Resolution720p},
		{1280, 536, Resolution720p},
		{1728, 720, Resolution720p}, // 720 rung of scope 4K
		{960, 720, Resolution720p},
		{720, 576, ResolutionSD},
		{720, 480, ResolutionSD},
		{640, 360, ResolutionSD},
	}
	for _, c := range cases {
		if got := ResolutionLabel(c.width, c.height); got != c.want {
			t.Errorf("ResolutionLabel(%d, %d) = %q, want %q", c.width, c.height, got, c.want)
		}
	}
}

func TestSourceVideoFor(t *testing.T) {
	media := store.Media{VideoWidth: 3840, VideoHeight: 2160}
	hdr := []store.MediaFileStream{{Type: "audio"}, {Type: "video", Codec: "hevc", ColorTransfer: "smpte2084"}}
	got := sourceVideoFor(media, hdr)
	if got == nil || *got != (SourceVideo{Resolution: Resolution4K, Width: 3840, Height: 2160, HDR: "hdr10"}) {
		t.Fatalf("sourceVideoFor(4K HDR10) = %#v", got)
	}
	// An alternate Version's frame size is unknown: no label, HDR still reported.
	got = sourceVideoFor(store.Media{}, hdr)
	if got == nil || *got != (SourceVideo{HDR: "hdr10"}) {
		t.Fatalf("sourceVideoFor(unknown size) = %#v", got)
	}
	cover := store.Media{Class: store.MediaClassAudio, VideoWidth: 1920, VideoHeight: 1080}
	if got := sourceVideoFor(cover, hdr); got != nil {
		t.Fatalf("sourceVideoFor(audio with cover art) = %#v, want nil", got)
	}
	if got := sourceVideoFor(media, []store.MediaFileStream{{Type: "audio"}}); got != nil {
		t.Fatalf("sourceVideoFor(audio only) = %#v, want nil", got)
	}
}

func TestLabelRenditions(t *testing.T) {
	streams := []StreamChoice{{Renditions: []RenditionInfo{{ID: "src"}, {ID: "r1080", Height: 1080}, {ID: "r720", Height: 720}, {ID: "r480", Height: 480}}}}
	labelRenditions(streams, store.Media{VideoWidth: 3840, VideoHeight: 1600})
	want := []string{Resolution4K, Resolution1080p, Resolution720p, ResolutionSD}
	for i, rendition := range streams[0].Renditions {
		if rendition.Resolution != want[i] {
			t.Errorf("rendition %s = %q, want %q", rendition.ID, rendition.Resolution, want[i])
		}
	}
	// Unknown source size: a source rung stays unlabelled, a sized rung is
	// labelled by its height.
	streams = []StreamChoice{{Renditions: []RenditionInfo{{ID: "src"}, {ID: "r720", Height: 720}}}}
	labelRenditions(streams, store.Media{})
	if streams[0].Renditions[0].Resolution != "" || streams[0].Renditions[1].Resolution != Resolution720p {
		t.Fatalf("unknown-source renditions = %#v", streams[0].Renditions)
	}
}

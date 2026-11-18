package ffmpeg

import "testing"

// CORE-2523: a subtitle stream's title tag is kept, and SDH/CC, forced and
// commentary words in it set the flags its dispositions leave unset.
func TestParseProbeSubtitleTitleFlags(t *testing.T) {
	probe, err := parseProbe([]byte(`{
		"streams": [
			{"index": 0, "codec_type": "video", "codec_name": "h264"},
			{"index": 1, "codec_type": "audio", "codec_name": "eac3", "tags": {"title": "Commentary"}},
			{"index": 2, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "English (SDH)"}},
			{"index": 3, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "TITLE": " Forced "}},
			{"index": 4, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "English [CC]"}},
			{"index": 5, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "Hearing Impaired"}},
			{"index": 6, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "Director's Commentary"}},
			{"index": 7, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "spa", "title": "Spanish (Latin America)"}},
			{"index": 8, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "hin", "title": "Hindi"}, "disposition": {"forced": 1}},
			{"index": 9, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "English (non-SDH)"}},
			{"index": 10, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "Not Forced"}},
			{"index": 11, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "Non Hearing Impaired"}},
			{"index": 12, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "English (No SDH/CC)"}},
			{"index": 13, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "Forced, not SDH"}},
			{"index": 14, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "English (non-SDH) [Forced]"}},
			{"index": 15, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "No SDH or CC"}},
			{"index": 16, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "eng", "title": "English - non-SDH - Forced"}}
		],
		"format": {"format_name": "matroska,webm", "duration": "10"}
	}`), "/media/episode.mkv")
	if err != nil {
		t.Fatal(err)
	}
	type flags struct {
		title                   string
		forced, sdh, commentary bool
	}
	want := []flags{
		{},
		{title: "Commentary"}, // audio titles set no flags
		{title: "English (SDH)", sdh: true},
		{title: "Forced", forced: true},
		{title: "English [CC]", sdh: true},
		{title: "Hearing Impaired", sdh: true},
		{title: "Director's Commentary", commentary: true},
		{title: "Spanish (Latin America)"},
		{title: "Hindi", forced: true},
		{title: "English (non-SDH)"},
		{title: "Not Forced"},
		{title: "Non Hearing Impaired"},
		{title: "English (No SDH/CC)"},
		{title: "Forced, not SDH", forced: true},
		{title: "English (non-SDH) [Forced]", forced: true},
		{title: "No SDH or CC"},
		{title: "English - non-SDH - Forced", forced: true},
	}
	for i, w := range want {
		s := probe.Streams[i]
		got := flags{s.Title, s.Forced, s.HearingImpaired, s.Commentary}
		if got != w {
			t.Errorf("stream %d = %+v, want %+v", i, got, w)
		}
	}
}

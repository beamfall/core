package ffmpeg

import "testing"

func FuzzParseProbe(f *testing.F) {
	f.Add([]byte(sampleProbeJSON), "/media/Test Pattern.mp4")
	f.Add([]byte(`{"format":{"format_name":"tty"},"streams":[]}`), "/media/notes.txt")
	f.Add([]byte(`{"streams":[{"codec_type":"audio","codec_name":"aac"}],"format":{"format_name":"","duration":"0.5","tags":{"TITLE":"Signal"}}}`), "signal.m4a")

	f.Fuzz(func(t *testing.T, data []byte, path string) {
		_, _ = parseProbe(data, path)
	})
}

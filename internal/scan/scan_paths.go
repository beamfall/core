package scan

import (
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

func titleFromPath(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func containerFromPath(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == "" {
		return "file"
	}
	return ext
}

// streamsFromProbe maps the ffmpeg per-stream inventory onto the store's
// domain-agnostic stream rows. The slice index is the stream
// index (ffprobe order). A non-empty probe always returns a non-nil slice so
// the store full-replaces; an empty probe returns an empty (non-nil) slice so
// a file that genuinely has no streams clears any stale inventory.
func streamsFromProbe(streams []ffmpeg.ProbeStream) []store.MediaFileStream {
	out := make([]store.MediaFileStream, len(streams))
	for i, s := range streams {
		out[i] = store.MediaFileStream{
			Index:           i,
			Type:            s.Type,
			Codec:           s.Codec,
			Profile:         s.Profile,
			Level:           s.Level,
			Language:        s.Language,
			Default:         s.Default,
			Forced:          s.Forced,
			HearingImpaired: s.HearingImpaired,
			VisualImpaired:  s.VisualImpaired,
			Commentary:      s.Commentary,
			Channels:        s.Channels,
			ChannelLayout:   s.ChannelLayout,
			BitDepth:        s.BitDepth,
			ColorTransfer:   s.ColorTransfer,
			ColorPrimaries:  s.ColorPrimaries,
			HDR10Plus:       s.HDR10Plus,
			DoViProfile:     s.DoViProfile,
			DoViBLCompatID:  s.DoViBLCompatID,
		}
	}
	return out
}

// fontsFromProbe maps the probe's font attachments onto store rows. Returns a
// non-nil (possibly empty) slice for the same full-replace reason as
// streamsFromProbe.
func fontsFromProbe(fonts []ffmpeg.FontAttachment) []store.MediaFileFontAttachment {
	out := make([]store.MediaFileFontAttachment, len(fonts))
	for i, f := range fonts {
		out[i] = store.MediaFileFontAttachment{Filename: f.Filename, MimeType: f.MimeType}
	}
	return out
}

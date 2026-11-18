package playback

import "github.com/beamfall/core/internal/store"

// Resolution labels are the closed vocabulary clients badge video with
// (CORE-2485). Core derives them once so every client shows the same word.
const (
	Resolution8K    = "8K"
	Resolution4K    = "4K"
	Resolution1080p = "1080p"
	Resolution720p  = "720p"
	ResolutionSD    = "SD"
)

// ResolutionLabel names a frame size the way media players badge it. Either
// dimension reaching a class's threshold is enough, so a scope film keeps its
// class: 3840x1600 is 4K and 1920x800 is 1080p, while 1440x1080 HDV is 1080p.
// "" means the frame size is unknown (never probed, or not stored for this
// file); a known size below 720p is SD.
func ResolutionLabel(width, height int) string {
	switch {
	case width <= 0 && height <= 0:
		return ""
	case width >= 6000 || height >= 3400:
		return Resolution8K
	case width >= 3200 || height >= 1800:
		return Resolution4K
	case width >= 1800 || height >= 900:
		return Resolution1080p
	case width >= 1200 || height >= 650:
		return Resolution720p
	default:
		return ResolutionSD
	}
}

// SourceVideo describes the video the negotiated Version holds before any
// transcode: its frame size, resolution label and HDR format token ("" for
// SDR). Width and Height are zero, and Resolution empty, when the file's frame
// size is not stored.
type SourceVideo struct {
	Resolution string `json:"resolution,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	HDR        string `json:"hdr,omitempty"`
}

// sourceVideoFor reports the negotiated file's video, or nil for a file with
// no video stream or an audio-class item (whose "video" is cover art). media carries the negotiated Version's facts (versionFacts
// zeroes a frame size the store does not hold for that file).
func sourceVideoFor(media store.Media, streams []store.MediaFileStream) *SourceVideo {
	video, ok := primaryVideoStream(streams)
	if !ok || media.Class == store.MediaClassAudio {
		return nil
	}
	return &SourceVideo{
		Resolution: ResolutionLabel(media.VideoWidth, media.VideoHeight),
		Width:      max(media.VideoWidth, 0),
		Height:     max(media.VideoHeight, 0),
		HDR:        hdrFormat(video),
	}
}

// labelRenditions stamps each advertised rendition with its resolution label.
// A rung keeps the source aspect, so its width follows from its height;
// Height 0 is the source size. An audio-class item's renditions are audio
// only and stay unlabelled.
func labelRenditions(streams []StreamChoice, media store.Media) {
	if media.Class == store.MediaClassAudio {
		return
	}
	for _, stream := range streams {
		renditions := stream.Renditions // shares the backing array: writes land in place
		for j := range renditions {
			rendition := &renditions[j]
			if rendition.Height <= 0 {
				rendition.Resolution = ResolutionLabel(media.VideoWidth, media.VideoHeight)
				continue
			}
			width := rungWidth(media.VideoWidth, media.VideoHeight, rendition.Height)
			rendition.Resolution = ResolutionLabel(width, rendition.Height)
		}
	}
}

package playback

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// VersionInfo is one Version of a Media with two or more Versions (ADR-0009),
// listed in playbackInfo so a client can offer a version picker. ID is opaque
// and stable for the Media and label, so a client can remember a choice and
// send it back as InfoRequest.SelectedVersion. Label is the stored Version
// label; "" is the default Version, which clients name themselves.
type VersionInfo struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	VideoCodec string `json:"videoCodec,omitempty"`
	HDR        string `json:"hdr,omitempty"`
	// Resolution is the Version's ResolutionLabel ("4K", "1080p", ...). It is
	// set only for the Version whose frame size the store holds (the Media
	// row's), and empty for any other, whose frame size is unknown.
	Resolution string `json:"resolution,omitempty"`
	Selected   bool   `json:"selected"`
}

// versionID derives a Version's opaque wire ID from its Media and label. It
// never carries a path or content fingerprint.
func versionID(mediaID store.MediaID, label string) string {
	sum := sha256.Sum256([]byte("beamfall-version\x00" + string(mediaID) + "\x00" + label))
	return "v" + hex.EncodeToString(sum[:8])
}

// versionInfos lists versions for the picker, or nil for a single Version so
// that response stays unchanged. streams holds each Version's primary-Part
// inventory, index-aligned with versions; chosen indexes the negotiated one.
// media is the Media row, whose frame size belongs to the file with
// factsFingerprint.
func versionInfos(media store.Media, versions [][]store.MediaFile, streams [][]store.MediaFileStream, chosen int, factsFingerprint string) []VersionInfo {
	if len(versions) < 2 {
		return nil
	}
	infos := make([]VersionInfo, len(versions))
	for i, version := range versions {
		label := version[0].VersionLabel
		info := VersionInfo{ID: versionID(media.ID, label), Label: label, Selected: i == chosen}
		if version[0].ContentFingerprint == factsFingerprint {
			info.Resolution = ResolutionLabel(media.VideoWidth, media.VideoHeight)
		}
		for _, stream := range streams[i] {
			if stream.Type == "video" {
				info.VideoCodec, info.HDR = stream.Codec, hdrFormat(stream)
				break
			}
		}
		infos[i] = info
	}
	return infos
}

// versionFacts returns the Media facts a Version is judged by. The Media row
// holds the first Version's probe facts (the store keeps it so), so with
// several Versions each one is judged by what is known per file instead: its
// container from its own path, the way a scan names it, and its video codec
// from its own stream inventory. Frame size is not stored per file, so only
// the file the row describes (first is true; see store.MediaFactsFingerprint)
// keeps the row's; any other file's frame size is unknown
// (zero), which keeps the ladder from advertising rungs the file may not reach
// and lets unknownHeightBarred hold it off passthrough. Duration stays the row's.
func versionFacts(media store.Media, file store.MediaFile, streams []store.MediaFileStream, first bool) store.Media {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(file.Path)), ".")
	if ext == "" {
		ext = "file"
	}
	media.Container = ext
	if video, ok := primaryVideoStream(streams); ok && video.Codec != "" {
		media.Codec = video.Codec
	}
	if !first {
		media.VideoWidth, media.VideoHeight = 0, 0
	}
	return media
}

// unknownHeightBarred reports whether a file must not pass its video through
// (direct play or direct stream) because its frame height is unknown while the
// client caps height for its codec. guarded is true for a file the Media row
// does not describe, whose height is never stored, and for any Version of a
// multi-Version Media, whose first Version may have joined without a probe;
// a 4K Version would otherwise slip past a 1080p ceiling, including while the
// first Version's file is missing. A legacy single-file Media with no probed
// height still passes, as before. With no ceiling for the codec the height
// cannot refuse, so nothing is barred.
func unknownHeightBarred(media store.Media, profile CapabilityProfile, guarded bool) bool {
	if !guarded || media.VideoHeight > 0 || media.Class == store.MediaClassAudio || len(profile.VideoCodecs) == 0 {
		return false
	}
	codecCap, ok := videoCodecCap(profile.VideoCodecs, media.Codec)
	return ok && codecCap.MaxHeight > 0
}

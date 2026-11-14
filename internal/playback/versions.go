package playback

import (
	"crypto/sha256"
	"encoding/hex"

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
func versionInfos(mediaID store.MediaID, versions [][]store.MediaFile, streams [][]store.MediaFileStream, chosen int) []VersionInfo {
	if len(versions) < 2 {
		return nil
	}
	infos := make([]VersionInfo, len(versions))
	for i, version := range versions {
		label := version[0].VersionLabel
		info := VersionInfo{ID: versionID(mediaID, label), Label: label, Selected: i == chosen}
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

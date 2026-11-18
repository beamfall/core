package playback

import (
	"context"
	"fmt"

	"github.com/beamfall/core/internal/store"
)

// mainSuffix names the decision for the item being played, as opposed to the
// declared next item; caption track ids carry it.
const mainSuffix = "main"

// muxedCaptionID is the caption track id of a subtitle stream muxed into the
// file, for the decision named by suffix.
func muxedCaptionID(suffix string, streamIndex int) string {
	return fmt.Sprintf("muxed-%s-%d", suffix, streamIndex)
}

// sidecarCaptionID is the caption track id of a sidecar caption file, by its
// position in the store's full sidecar list for the file.
func sidecarCaptionID(suffix string, position int) string {
	return fmt.Sprintf("sidecar-%s-%d", suffix, position)
}

// TrackInventory is the stored stream and sidecar inventory ListTracks reads.
type TrackInventory interface {
	MediaFileStreams(ctx context.Context, fingerprint string) ([]store.MediaFileStream, error)
	MediaFileSidecarCaptions(ctx context.Context, fingerprint string) ([]store.MediaFileSidecarCaption, error)
}

// VersionTracks is one Version of a Media with the audio and caption tracks
// of its primary Part, as listed before playback (CORE-2442). ID is the id
// playbackInfo reports for the Version; caption ids are the ids playbackInfo
// gives the same tracks for the item being played, so a pick made from the
// listing names the same track there. Audio tracks never carry Selected and
// caption tracks never carry a URL: nothing is negotiated or tokenised.
type VersionTracks struct {
	ID            string         `json:"id"`
	Label         string         `json:"label"`
	VideoCodec    string         `json:"videoCodec,omitempty"`
	HDR           string         `json:"hdr,omitempty"`
	AudioTracks   []AudioTrack   `json:"audioTracks"`
	CaptionTracks []CaptionTrack `json:"captionTracks"`
}

// ListTracks lists a Media's Versions in playback order, files being the
// Media's (VersionLabel, PartOrdinal)-ordered files as the store returns
// them. It reads stored inventory only: no capability negotiation, no stream
// token and no playback session. Every sidecar caption is listed whatever its
// format; the player drops a pick its client cannot draw.
func ListTracks(ctx context.Context, inv TrackInventory, mediaID store.MediaID, files []store.MediaFile) ([]VersionTracks, error) {
	versions := groupVersions(files)
	out := make([]VersionTracks, 0, len(versions))
	for _, version := range versions {
		primary := version[0]
		streams, err := inv.MediaFileStreams(ctx, primary.ContentFingerprint)
		if err != nil {
			return nil, err
		}
		listed := VersionTracks{
			ID:            versionID(mediaID, primary.VersionLabel),
			Label:         primary.VersionLabel,
			AudioTracks:   resolveAudioTrack(streams, nil, nil).tracks,
			CaptionTracks: []CaptionTrack{},
		}
		// The first video stream, as versionInfos reads it for playbackInfo.
		for _, stream := range streams {
			if stream.Type == "video" {
				listed.VideoCodec, listed.HDR = stream.Codec, hdrFormat(stream)
				break
			}
		}
		for i := range listed.AudioTracks {
			listed.AudioTracks[i].Selected = false
		}
		if listed.AudioTracks == nil {
			listed.AudioTracks = []AudioTrack{}
		}
		for _, stream := range streams {
			if stream.Type != "subtitle" {
				continue
			}
			index := stream.Index
			listed.CaptionTracks = append(listed.CaptionTracks, CaptionTrack{
				ID:             muxedCaptionID(mainSuffix, stream.Index),
				Format:         stream.Codec,
				StreamIndex:    &index,
				Language:       stream.Language,
				Forced:         stream.Forced,
				SDH:            stream.HearingImpaired,
				VisualImpaired: stream.VisualImpaired,
				Commentary:     stream.Commentary,
			})
		}
		if primary.ContentFingerprint != "" {
			sidecars, err := inv.MediaFileSidecarCaptions(ctx, primary.ContentFingerprint)
			if err != nil {
				return nil, err
			}
			for i, sc := range sidecars {
				if sc.Kind == store.SidecarKindInteractive {
					continue
				}
				listed.CaptionTracks = append(listed.CaptionTracks, CaptionTrack{
					ID:       sidecarCaptionID(mainSuffix, i),
					Format:   sc.Format,
					Language: sc.Language,
					Forced:   sc.Forced,
					SDH:      sc.SDH,
				})
			}
		}
		out = append(out, listed)
	}
	return out, nil
}

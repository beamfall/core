// Package playback negotiates client playback capability into a playbackInfo response.
package playback

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// InfoRequest is the client request for playbackInfo.
type InfoRequest struct {
	MediaID           string            `json:"mediaID"`
	CapabilityProfile CapabilityProfile `json:"capabilityProfile"`
	// NextMediaID optionally declares the queue item the client intends to
	// play after MediaID (ADR-0029 §6: the queue is client-owned,
	// session-declared). When the next item is visible to the session scope
	// and servable, the response carries its Stream Token ahead of time so
	// gapless transitions need no extra round trip.
	NextMediaID string `json:"nextMediaID,omitempty"`
	// Foreground declares this playbackInfo as the client's foreground/
	// interactive playhead, the signal that ranks the session PriorityForeground
	// for admission control (ADR-0010 §6: higher-priority preempts lower). It is
	// additive and optional — the zero value (absent) is PriorityNormal, so a
	// client that never sets it behaves exactly as before (every session equal-
	// priority, idle-only preemption). The transcode-refusal verdict below is
	// computed at this rank, so playbackInfo never refuses a foreground start the
	// server would actually admit by preempting a normal stream.
	Foreground bool `json:"foreground,omitempty"`
	// SelectedSubtitleIndex is the absolute ffprobe stream index of the subtitle
	// track the client selected for this playbackInfo request. Omitted means no
	// concrete subtitle track has been selected server-side.
	SelectedSubtitleIndex *int `json:"selectedSubtitleIndex,omitempty"`
	// SelectedAudioIndex is the absolute ffprobe stream index of the audio
	// track the viewer picked (an AudioTrack.StreamIndex). It applies to
	// MediaID only. A track other than the file's default moves playback off
	// direct play onto a stream that maps the picked track; an index that names
	// no audio stream of the negotiated file is ignored and the server selects
	// as if it were absent.
	SelectedAudioIndex *int `json:"selectedAudioIndex,omitempty"`
	// Adjustment optionally asks for a pitch shift and tempo change on an
	// audio Media's playback. Omitted, or all defaults, changes nothing. It is
	// validated (an out-of-range value is an invalid request) and applies to
	// MediaID only, never to NextMediaID.
	Adjustment *PlaybackAdjustment `json:"adjustment,omitempty"`
	// SelectedVersion is the opaque ID of the Version (VersionInfo.ID) the
	// viewer picked. It applies to MediaID only; an ID that names none of its
	// Versions is ignored and the server negotiates as if it were absent.
	SelectedVersion string `json:"selectedVersion,omitempty"`
	// RemoteClient marks a request that did not come from the LAN. The
	// server sets it from the connection, never from the body: a remote
	// request is capped at the lower of the client's MaxStreamingBitrate and
	// the admin's remote bitrate policy (CORE-1717).
	RemoteClient bool `json:"-"`
}

// PreviewInfoRequest asks for the card-preview Generated Asset only. It is
// deliberately narrower than playbackInfo: no playable stream URLs are minted.
type PreviewInfoRequest struct {
	MediaID string `json:"mediaID"`
}

// CapabilityProfile is the client's declared playback capability.
// The legacy {Containers, DirectCodecs} pair is the floor: a profile that
// declares only those is judged exactly as before. The richer fields, when
// present, let canDirectPlay reason over the persisted stream inventory —
// codec profiles/levels with resolution & bitrate ceilings, audio channel
// limits, HDR formats, and subtitle formats — so a file that nominally matches
// a supported codec but exceeds a ceiling (4K above a 1080p cap, or a known
// bitrate above the profile limit), carries an unsupported HDR format (Dolby
// Vision), carries an unsupported audio/subtitle stream, or exceeds the
// downstream audio path correctly falls to transcode instead of a false
// direct-play.
type CapabilityProfile struct {
	Client string `json:"client"`
	// Tier is the client's model-family tier (ADR-0010 §1, the ADR-0156 §4
	// amendment): one client identity, N tiers. Free-form, client-authored,
	// matched case-insensitively by quirk selectors. Never an authorization
	// input.
	Tier string `json:"tier,omitempty"`
	// Firmware is the device's firmware/OS version as the platform reports it.
	// Quirk firmware ranges compare it as dotted numeric segments; a value that
	// does not parse that way never satisfies a bounded range. Never an
	// authorization input.
	Firmware     string   `json:"firmware,omitempty"`
	Containers   []string `json:"containers"`
	DirectCodecs []string `json:"directCodecs"`
	// VideoCodecs is the per-codec direct-play envelope. When non-empty it
	// supersedes DirectCodecs for video: a codec absent here is not
	// direct-playable even if it appears in DirectCodecs, and a present codec
	// must also satisfy its declared profile/resolution/bitrate ceilings.
	VideoCodecs []VideoCodecCap `json:"videoCodecs,omitempty"`
	// AudioCodecs is the per-codec audio envelope (codec + channel, sample rate
	// and bit depth ceilings). When non-empty it gates the file's audio
	// streams: a TrueHD track against a profile that omits TrueHD, or a 24-bit
	// 192 kHz FLAC track against a FLAC envelope capped at 96 kHz, forces
	// transcode.
	AudioCodecs []AudioCodecCap `json:"audioCodecs,omitempty"`
	// HDRFormats lists the HDR transfer formats the engine renders correctly
	// ("hdr10", "hdr10plus", "dolby-vision-p5", "dolby-vision-p8", "hlg" — the
	// closed per-profile Dolby Vision vocabulary of ADR-0017 §4). When non-empty,
	// an HDR video stream whose format is absent forces transcode (tone-map). SDR
	// content is unaffected. The server also emits two DV tokens that no profile
	// ever advertises: "dolby-vision-p7" (its enhancement layer is undecodable on
	// every client, so it plays base-layer-only via the honesty path — see
	// dolbyVisionP7BaseLayerReason) and the generic "dolby-vision" sentinel for DV
	// shapes without an HDR10-compatible base (they take the transcode path).
	HDRFormats []string `json:"hdrFormats,omitempty"`
	// SubtitleFormats lists the subtitle formats the engine renders without a
	// burn-in transcode ("srt", "webvtt", "ass", "pgs"). When non-empty it
	// gates muxed subtitle streams: an unsupported subtitle format forces a
	// transcode path rather than a false direct-play promise.
	SubtitleFormats []string `json:"subtitleFormats,omitempty"`
	// DownstreamAudio hints the speaker path ("stereo", "passthrough"). A
	// stereo sink caps direct audio at two channels; passthrough leaves the
	// codec envelope in control.
	DownstreamAudio string `json:"downstreamAudio,omitempty"`
	// NormalizationModes is the closed vocabulary of loudness-normalization the
	// client declares it can honor on a DIRECT-PLAY stream (ADR-0098 area 3):
	// "replaygain" (apply a precomputed ReplayGain offset itself) and "r128"
	// (apply an EBU R128 integrated-target offset itself). Empty means the client
	// declares no direct-play normalization capability — the server then reports
	// an honest passthrough-unsupported state rather than forcing a worse rung to
	// normalize. This is an EXPLICIT declared capability: the loudness state is
	// derived from it, never from the client name.
	NormalizationModes []string `json:"normalizationModes,omitempty"`
	// LoudnessReference optionally overrides the server's default normalization
	// target (LUFS). 0 (absent) uses the server default. It is informational for
	// the reported state; the served GainDB targets the analysis's recorded
	// reference, so a client that prefers a different target re-derives its own
	// offset from the reported IntegratedLUFS.
	LoudnessReference float64 `json:"loudnessReference,omitempty"`
	// MaxStreamingBitrate is the viewer's own streaming ceiling in bits per
	// second for the whole stream (CORE-1674): the quality limit a user picks
	// for the network they are on, such as a lower one on cellular. 0 (absent)
	// means no limit. It is a preference, not a device fact, so the server
	// folds it into the overlay's bitrate cap: a source above it does not
	// direct play, a cached rendition above it is not served, and the
	// transcode ladder stops at the best rung under it. A value below the
	// ladder floor plays the lowest rung. When the ceiling alone moves an item
	// off direct play, the decision reason is "bitrate-ceiling".
	MaxStreamingBitrate int `json:"maxStreamingBitrate,omitempty"`
}

// bitrateCeilingReason is the playbackInfo reason when the viewer's
// CapabilityProfile.MaxStreamingBitrate, and nothing else, kept an item off
// direct play or the video-copy rung.
const bitrateCeilingReason = "bitrate-ceiling"

// policyBitrateCeilingReason is the playbackInfo reason when the admin's
// remote bitrate cap (server-wide or per Profile, CORE-1717), tighter than
// any ceiling the client declared, kept an item off direct play or the
// video-copy rung.
const policyBitrateCeilingReason = "policy-bitrate-ceiling"

// Loudness-normalization modes a client may declare in
// CapabilityProfile.NormalizationModes (closed vocabulary). They name the kind of
// precomputed loudness offset the client can apply itself on a direct-play stream,
// matched against the stored measurement's Source provenance.
const (
	NormalizationModeReplayGain = "replaygain"
	NormalizationModeR128       = "r128"
)

// Loudness reporting states (ADR-0098 area 3) — the four honest, capability-
// derived outcomes playbackInfo distinguishes, never inferred from the client
// name:
//   - LoudnessStateDirectSupported: a measurement exists, the stream is direct-play,
//     and the client declared a mode covering the measurement's source, so the
//     client applies the offset itself on the unchanged direct-play rung.
//   - LoudnessStatePassthroughUnsupported: a measurement exists and the stream is
//     direct-play, but the client declared no covering mode — bytes pass through
//     un-normalized. The server does NOT force a transcode or a worse rung solely
//     to normalize (the ADR's explicit prohibition).
//   - LoudnessStateServerNormalized: the stream was already going to be transcoded
//     for other reasons, so the server applies the offset in that transcode.
//   - LoudnessStateUnavailable: no loudness measurement exists for the source yet.
const (
	LoudnessStateDirectSupported        = "direct-supported"
	LoudnessStatePassthroughUnsupported = "passthrough-unsupported"
	LoudnessStateServerNormalized       = "server-normalized"
	LoudnessStateUnavailable            = "unavailable"
)

// Loudness measurement Source provenance values (must match genassets'
// LoudnessAnalysis.Source vocabulary): "tags" is a measurement recovered from
// trusted ReplayGain/R128 container tags, "r128" one computed by an ebur128 pass.
// The Source selects which declared NormalizationMode covers the measurement.
const (
	LoudnessDataSourceTags = "tags"
	LoudnessDataSourceR128 = "r128"
)

// LoudnessData is the parsed loudness measurement playback reads through the
// AssetsLocator content seam (the four measured numbers plus provenance). It is a
// playback-owned value type so the playback package never imports genassets' wire
// JSON struct — genassets maps its LoudnessAnalysis into this at the boundary.
type LoudnessData struct {
	IntegratedLUFS  float64
	LoudnessRangeLU float64
	TruePeakDBTP    float64
	ReferenceLUFS   float64
	GainDB          float64
	// Source is the measurement provenance ("tags" | "r128"); it selects which
	// declared NormalizationMode covers it.
	Source string
}

// LoudnessInfo is the rung-specific loudness/normalization state playbackInfo
// reports for the current item (ADR-0098 area 3). State is the capability-derived
// outcome; the measured fields are present unless State is unavailable; AssetURL
// is the token-bearing loudness.json route when the measurement exists.
type LoudnessInfo struct {
	State           string  `json:"state"`
	IntegratedLUFS  float64 `json:"integratedLUFS,omitempty"`
	LoudnessRangeLU float64 `json:"loudnessRangeLU,omitempty"`
	TruePeakDBTP    float64 `json:"truePeakDBTP,omitempty"`
	ReferenceLUFS   float64 `json:"referenceLUFS,omitempty"`
	GainDB          float64 `json:"gainDB,omitempty"`
	Source          string  `json:"source,omitempty"`
	AssetURL        string  `json:"assetURL,omitempty"`
}

// VideoCodecCap is the direct-play envelope for one video codec. MaxHeight 0
// means "no resolution ceiling", MaxBitrate 0 means "no bitrate ceiling", and
// MaxLevel 0 means "no level ceiling". MaxBitrate is bits per second on the
// wire and is checked only when the file size and duration are known. MaxLevel
// uses ffprobe's integer level scale (H.264 4.1 => 41, HEVC 5.1 => 153) and is
// checked only when the source stream reports a level. Profile matching is
// case-insensitive on the ffprobe profile string ("Main 10", "High").
type VideoCodecCap struct {
	Codec      string   `json:"codec"`
	Profiles   []string `json:"profiles,omitempty"`
	MaxHeight  int      `json:"maxHeight,omitempty"`
	MaxBitrate int      `json:"maxBitrate,omitempty"`
	MaxLevel   int      `json:"maxLevel,omitempty"`
}

// AudioCodecCap is the direct-play envelope for one audio codec. MaxChannels 0
// means "no channel ceiling". MaxSampleRate (Hz) and MaxBitDepth (bits per
// sample) are the hi-res ceilings the device renders without resampling or
// truncating (CORE-1627); 0 means no ceiling. Each is checked only when the
// probe recorded the source stream's value, so a legacy row with no recorded
// rate or depth is never refused on that axis.
type AudioCodecCap struct {
	Codec         string `json:"codec"`
	MaxChannels   int    `json:"maxChannels,omitempty"`
	MaxSampleRate int    `json:"maxSampleRate,omitempty"`
	MaxBitDepth   int    `json:"maxBitDepth,omitempty"`
}

// InfoResponse is the playbackInfo payload consumed by clients.
type InfoResponse struct {
	MediaID string `json:"mediaID"`
	// MediaClass is the item's audio/video playback class ("video" | "audio",
	// store.MediaClass). Native clients route audio-vs-video transport off it
	// (background audio, lock-screen, CarPlay/Android Auto). Omitted when the
	// item is unavailable (no class to report) so the field's presence always
	// means a real class.
	MediaClass string `json:"mediaClass,omitempty"`
	// RestrictedOrigin reports whether the playable Media originates in a
	// restricted Library. It is populated only after the session has passed the
	// Profile/Library/Reveal filter, so an unavailable restricted item remains
	// indistinguishable from an unknown item. Receivers use this bit to discard
	// stale Cast state immediately when Reveal is lost.
	RestrictedOrigin    bool           `json:"restrictedOrigin,omitempty"`
	Decision            string         `json:"decision"`
	Reason              string         `json:"reason,omitempty"`
	StreamTokenRequired bool           `json:"streamTokenRequired"`
	Streams             []StreamChoice `json:"streams"`
	Timeline            Timeline       `json:"timeline"`
	// Parts is the negotiated Version's ordered Parts, which play
	// back-to-back as one timeline. A single-file item carries one Part; clients
	// that ignore it play the first (primary) Part as today. Omitted when the
	// item is unavailable.
	Parts []PartInfo `json:"parts,omitempty"`
	// Versions lists the Media's Versions when it has two or more, marking the
	// negotiated one selected, so a client can offer a version picker. Omitted
	// for a single-Version or unavailable item.
	Versions []VersionInfo `json:"versions,omitempty"`
	// Next is the ahead-of-time decision for the declared next queue item.
	// Omitted whenever the next item cannot be served — an item the scope
	// cannot see is indistinguishable from an unknown ID (Reveal contract).
	Next *NextInfo `json:"next,omitempty"`
	// TrackPreferences is the session Profile's deterministic track
	// pre-selection policy, applied by the player against the tracks it
	// discovers in the chosen stream.
	TrackPreferences TrackPreferences `json:"trackPreferences"`
	// Assets advertises the item's Generated Assets. Omitted when
	// no assets service is wired or the item has no servable file — clients
	// fall back to artwork, no layout difference (the defensive rule).
	Assets *AssetsInfo `json:"assets,omitempty"`
	// CaptionTracks lists muxed subtitle tracks and sidecar caption tracks for the
	// chosen Version's primary file. Sidecars carry token-bearing WebVTT URLs;
	// muxed tracks carry streamIndex. Both carry language / forced / SDH role
	// hints for the client track menu. Omitted when the item has no captions.
	CaptionTracks []CaptionTrack `json:"captionTracks,omitempty"`
	// AudioTracks lists every audio stream of the chosen Version's primary
	// file in stream order, with exactly one marked Selected: the track the
	// delivered streams play. It is chosen from the request's
	// SelectedAudioIndex, else the file's default track. A client picks another
	// track by asking playbackInfo again with its StreamIndex. Omitted when the
	// file has no audio stream.
	AudioTracks []AudioTrack `json:"audioTracks,omitempty"`
	// AudioQuality states the selected audio track's source codec, sample
	// rate and bit depth, and whether Streams[0] delivers that audio
	// bit-for-bit or transcodes it (CORE-1627). Omitted when the item is
	// unavailable or has no audio stream.
	AudioQuality *AudioQuality `json:"audioQuality,omitempty"`
	// Loudness is the rung-specific loudness/normalization state for the item
	// (ADR-0098 area 3): the honest direct-supported / passthrough-unsupported /
	// server-normalized / unavailable outcome derived from the declared
	// CapabilityProfile.NormalizationModes and the stored loudness measurement.
	// Omitted when the item is non-audio or unavailable, so the wire shape is
	// unchanged for clients and items the loudness contract does not touch.
	Loudness *LoudnessInfo `json:"loudness,omitempty"`
	// AppliedQuirks lists the IDs of the quirk entries whose OVERRIDE was in
	// force for this decision — matched entries carrying a
	// non-empty override — sorted and deduplicated. An entry that matched but
	// carries only behavior flags is NOT listed here; the two are reported
	// separately so neither can be mistaken for the other. It reports which
	// device overrides applied, NOT which of them changed the verdict (a
	// counterfactual the server does not compute). Omitted when no override
	// matched, so an unquirked device's response is byte-identical to before.
	AppliedQuirks []string `json:"appliedQuirks,omitempty"`
	// BehaviorFlags are the generic, server-decided client-behavior flags the
	// matching quirk entries raise, from the closed vocabulary
	// BehaviorFlagVocabulary reports — sorted, deduplicated. Clients apply them
	// data-driven and MUST silently ignore an entry they do not recognise; they
	// never hardcode a device exception. Flags describe the DEVICE, not the
	// item, so they are returned once at the top level and never repeated on
	// the next block. Omitted when no quirk matched.
	BehaviorFlags []string `json:"behaviorFlags,omitempty"`
	// SelectedSubtitleTrack is the server's authoritative-at-start subtitle
	// pre-selection, carried as the format-independent
	// language+kind+role ref plus a human-readable reason. Additive: an old
	// client ignores it, and an old server that omits it leaves the client to
	// local-resolve from the start, exactly as today. Omitted whenever the
	// server has no pre-selection to state.
	SelectedSubtitleTrack *SelectedSubtitleTrack `json:"selectedSubtitleTrack,omitempty"`
	// SubtitlesUnavailableReason states that SUBTITLES SPECIFICALLY cannot be
	// presented for this session — e.g. "burn-in-capacity-exhausted" on a cast
	// route. It follows the existing free-text Reason convention
	// and is deliberately INDEPENDENT of the whole-media Decision verdict:
	// reusing that enum would wrongly fail the entire playback decision when
	// only subtitles are unavailable, so subtitle unavailability never degrades
	// Decision. Empty/absent means subtitles are not known to be unavailable.
	SubtitlesUnavailableReason string `json:"subtitlesUnavailableReason,omitempty"`
	// Adjustment echoes the requested pitch shift and tempo change when the
	// streams apply it: an audio Media served through a transcode that encodes
	// it. Omitted when no adjustment was requested or it could not be applied
	// (the streams then play unadjusted). Stream positions are then song
	// positions divided by the tempo; Timeline stays in song positions.
	Adjustment *PlaybackAdjustment `json:"adjustment,omitempty"`
}

// CaptionTrack is one caption track surfaced in playbackInfo. Sidecar tracks use
// URL; muxed subtitle tracks use StreamIndex. Forced/SDH/VisualImpaired/
// Commentary are generic player roles, never domain vocabulary (invariant #1).
type CaptionTrack struct {
	ID             string `json:"id"`
	URL            string `json:"url,omitempty"`
	Format         string `json:"format"`
	StreamIndex    *int   `json:"streamIndex,omitempty"`
	Language       string `json:"language,omitempty"`
	Forced         bool   `json:"forced,omitempty"`
	SDH            bool   `json:"sdh,omitempty"`
	VisualImpaired bool   `json:"visualImpaired,omitempty"`
	Commentary     bool   `json:"commentary,omitempty"`
}

// AudioTrack is one audio stream of the negotiated file as surfaced in
// playbackInfo. StreamIndex is the absolute ffprobe stream index a client
// sends back as InfoRequest.SelectedAudioIndex. Default, Commentary and
// VisualImpaired are generic player roles, never domain vocabulary.
type AudioTrack struct {
	StreamIndex    int    `json:"streamIndex"`
	Codec          string `json:"codec"`
	Language       string `json:"language,omitempty"`
	Channels       int    `json:"channels,omitempty"`
	Default        bool   `json:"default,omitempty"`
	Commentary     bool   `json:"commentary,omitempty"`
	VisualImpaired bool   `json:"visualImpaired,omitempty"`
	Selected       bool   `json:"selected,omitempty"`
}

// Subtitle track kinds: the closed, format-independent provenance vocabulary of
// SubtitleTrackRef.Kind. It is exactly the distinction CaptionTrack already
// carries — a URL is a sidecar caption file, a StreamIndex is a subtitle stream
// muxed into the media file — and deliberately says nothing about the codec or
// container, so the ref stays stable across renditions.
const (
	SubtitleTrackKindMuxed   = "muxed"
	SubtitleTrackKindSidecar = "sidecar"
)

// SubtitleTrackKinds reports the closed SubtitleTrackRef.Kind vocabulary, sorted.
// Reflection publishes Kind as a plain string, so this is the machine-readable
// half of the closed set (the published contract note is the other half) and
// growing it is a deliberate wire-contract change.
func SubtitleTrackKinds() []string {
	return []string{SubtitleTrackKindMuxed, SubtitleTrackKindSidecar}
}

// SubtitleTrackRoles carries the same four role booleans CaptionTrack publishes.
// They are INDEPENDENT and CO-OCCURRING — a track can be forced AND SDH at once —
// so this is a role SET, not a single role, and it is lossless when roles
// co-occur. There is deliberately no precedence order among them: they are not
// ranked, they coexist. Forced/SDH/VisualImpaired/Commentary are generic player
// roles, never domain vocabulary (invariant #1).
type SubtitleTrackRoles struct {
	Forced         bool `json:"forced,omitempty"`
	SDH            bool `json:"sdh,omitempty"`
	VisualImpaired bool `json:"visualImpaired,omitempty"`
	Commentary     bool `json:"commentary,omitempty"`
}

// SubtitleTrackRef is the format-independent language+kind+role identity of one
// subtitle track — the same identity shape the ADR-0177 per-item
// choice record uses. It is deliberately NOT the request-side
// InfoRequest.SelectedSubtitleIndex ffprobe stream index, which is unstable
// across renditions and must not leak into this field; clients map the tuple
// onto their own manifest track list locally.
type SubtitleTrackRef struct {
	Language string             `json:"language,omitempty"`
	Kind     string             `json:"kind"`
	Roles    SubtitleTrackRoles `json:"roles"`
}

// SelectedSubtitleTrack is the server's subtitle pre-selection for this item:
// the track ref plus a human-readable Reason for the choice. It
// is authoritative at playback START only — the client-local resolver is the
// sole authority for mid-session track switches and for offline playback, so
// once the viewer switches tracks this value is superseded and is not
// re-applied. The precedence rule ships in the wire spec itself (the published
// playbackInfo contract notes), not only here.
type SelectedSubtitleTrack struct {
	Track  SubtitleTrackRef `json:"track"`
	Reason string           `json:"reason"`
}

// AssetsInfo carries token-bearing Generated Asset URLs for the current item.
// Trickplay names the sprite manifest, PreviewClip the sampled MP4 — each
// present only when the file exists. MarkerThumbnailBase is the URL prefix
// Marker thumbnail names (marker-<startMillis>.jpg) append to.
type AssetsInfo struct {
	Trickplay           string `json:"trickplay,omitempty"`
	PreviewClip         string `json:"previewClip,omitempty"`
	MarkerThumbnailBase string `json:"markerThumbnailBase"`
	ExpiresAt           string `json:"expiresAt"`
	// Waveform, BeatGrid, SyncLanes, and SilenceMap are the first-class optional audio
	// Generated Asset URLs (ADR-0098 area 2). Each is present only when the asset
	// exists; a missing asset omits the field rather than erroring. The
	// generators land with FND-B / MUSIC-3, so these are absent until then.
	Waveform   string `json:"waveform,omitempty"`
	BeatGrid   string `json:"beatGrid,omitempty"`
	SyncLanes  string `json:"syncLanes,omitempty"`
	SilenceMap string `json:"silenceMap,omitempty"`
}

// PreviewInfoResponse is the asset-only card-preview payload.
type PreviewInfoResponse struct {
	MediaID string      `json:"mediaID,omitempty"`
	Assets  *AssetsInfo `json:"assets,omitempty"`
}

// TrackPreferences carries the Profile's audio/subtitle pre-selection policy.
// The server applies it at negotiation (the audio stream it delivers and the
// SelectedSubtitleTrack it stamps) and forwards it verbatim so the player can
// resolve it against tracks only the client sees.
type TrackPreferences struct {
	AudioLanguages    []string `json:"audioLanguages"`
	SubtitleLanguages []string `json:"subtitleLanguages"`
	SubtitleMode      string   `json:"subtitleMode"`
	PreferSDH         bool     `json:"preferSDH"`
}

// NextInfo carries a ready-to-use stream choice for the declared next queue
// item. Its tokens follow the current item's target and lifetime rules.
type NextInfo struct {
	MediaID string `json:"mediaID"`
	// MediaClass mirrors InfoResponse.MediaClass for the declared next item, so
	// a gapless audio queue keeps routing audio transport across the handoff.
	MediaClass string         `json:"mediaClass,omitempty"`
	Decision   string         `json:"decision"`
	Streams    []StreamChoice `json:"streams"`
	Timeline   Timeline       `json:"timeline"`
	// Parts mirrors InfoResponse.Parts for the declared next item.
	Parts  []PartInfo  `json:"parts,omitempty"`
	Assets *AssetsInfo `json:"assets,omitempty"`
	// CaptionTracks mirrors InfoResponse.CaptionTracks for the declared next item,
	// so a prefetched next item already carries its
	// sidecar caption track menu.
	CaptionTracks []CaptionTrack `json:"captionTracks,omitempty"`
	// Loudness mirrors InfoResponse.Loudness for the declared next item (ADR-0098
	// area 3), so a gapless audio queue carries the next item's normalization
	// state across the handoff.
	Loudness *LoudnessInfo `json:"loudness,omitempty"`
	// AudioQuality mirrors InfoResponse.AudioQuality for the declared next
	// item, so a queue knows ahead of the handoff whether it stays lossless.
	AudioQuality *AudioQuality `json:"audioQuality,omitempty"`
}

// StreamChoice is one playable stream option.
type StreamChoice struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	URL       string `json:"url,omitempty"`
	Token     string `json:"token,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	// Renditions is the honest rung inventory of an HLS choice:
	// exactly the renditions the master playlist advertises, never above the
	// source resolution. Empty for direct-play choices.
	Renditions []RenditionInfo `json:"renditions,omitempty"`
	// Gapless is the stream's encoder delay and end padding (CORE-1626):
	// what a client trims from the decoded audio so consecutive items join
	// without a gap. Omitted when the stream declares neither or the server
	// cannot vouch for the arithmetic.
	Gapless *GaplessInfo `json:"gapless,omitempty"`
}

// GaplessInfo is the gapless trim of one stream's first audio track, in
// samples at SampleRate (CORE-1626): EncoderDelaySamples priming samples at
// the start of the decoded audio and EncoderPaddingSamples padding samples at
// its end, as the stream declares them in its container. A client whose
// decoder already applies the container's own gapless signalling (an MP4 edit
// list, an Opus pre-skip, a LAME header) must not trim them a second time.
type GaplessInfo struct {
	SampleRate            int `json:"sampleRate"`
	EncoderDelaySamples   int `json:"encoderDelaySamples"`
	EncoderPaddingSamples int `json:"encoderPaddingSamples"`
}

// RenditionInfo describes one advertised HLS rendition (ladder rung).
// Bandwidth is the enforced peak rate in bits/s; Height 0 means the source
// size (dimensions unknown, encoded without scaling).
type RenditionInfo struct {
	ID        string `json:"id"`
	Height    int    `json:"height,omitempty"`
	Bandwidth int    `json:"bandwidth"`
}

// Timeline describes the canonical playback timeline.
type Timeline struct {
	DurationSeconds int `json:"durationSeconds"`
	// PositionSeconds is the Profile's resume point; zero when unwatched,
	// watched, or unknown.
	PositionSeconds int `json:"positionSeconds,omitempty"`
}

// Service owns playbackInfo decisions.
type Service struct {
	store           *store.Store
	profileSessions profileSessionStore
	tokens          *streamtoken.Service
	now             func() time.Time
	sessions        *Sessions
	assets          AssetsLocator
	roster          PartyRoster
	selector        *SelectorService
	sealed          SealedPredicate
	quirks          QuirkDB
	composer        PictureComposer
	composedCache   string
	observed        observations
}

type profileSessionStore interface {
	MyProfileSessions(context.Context, store.ProfileID) ([]store.MyProfileSession, error)
}

// SealedPredicate reports whether a Library is currently Sealed (ADR-0098 area
// 2). Sealed Libraries get no selector-bearing Parts and no Generated Asset
// advertisement, and previously minted byte tokens fail closed once sealed.
// Composition wires it off libencrypt.Manager.Sealed; nil (the default) means no
// Library is sealed, so deployments without library encryption pay nothing.
type SealedPredicate func(libraryID store.LibraryID) bool

// PartyRoster reports whether a session is a current Watch-Party member for a
// given Media. playbackInfo consults it so a transcode blocked by the
// concurrent-transcode cap DEGRADES to the lowest rung (ADR-0010 §6 "drop to
// this quality"; PANEL-16) for a real party member instead of being refused —
// keeping the party whole. Interface at the consumer; the conductor Registry
// satisfies it. Membership is authoritative and server-side, so a client cannot
// spoof it to escape the cap. nil (the default) means no party context, so the
// capacity refusal stands unchanged.
type PartyRoster interface {
	SessionInWatchPartyForMedia(sessionID store.SessionID, mediaID store.MediaID) bool
}

// AssetsLocator reports Generated Asset availability by content fingerprint
// (interface at the consumer; *genassets.Service implements it). Each method
// returns the servable asset name and whether it exists.
type AssetsLocator interface {
	TrickplayAsset(fingerprint string) (string, bool)
	PreviewClipAsset(fingerprint string) (string, bool)
	// LoudnessAsset reports the loudness asset name when one exists for the
	// fingerprint (the asset-advertisement seam, same shape as the others).
	LoudnessAsset(fingerprint string) (string, bool)
	// LoudnessForFingerprint returns the PARSED loudness measurement (not just
	// existence) so playbackInfo can populate the rung-specific normalization
	// state from real content without touching on-disk paths. ok=false means no
	// measurement exists yet for the source.
	// libraryID scopes the read, since an Encrypted Library's measurement is
	// decrypted under its Library.
	LoudnessForFingerprint(libraryID store.LibraryID, fingerprint string) (LoudnessData, bool)
	// WaveformAsset, BeatGridAsset, SyncLanesAsset, and SilenceMapAsset report the first-class
	// optional audio Generated Asset name when it exists for the fingerprint
	// (ADR-0098 area 2). playbackInfo advertises the URL only when ok is true, so
	// a missing asset omits the field rather than erroring.
	WaveformAsset(fingerprint string) (string, bool)
	BeatGridAsset(fingerprint string) (string, bool)
	SyncLanesAsset(fingerprint string) (string, bool)
	SilenceMapAsset(fingerprint string) (string, bool)
}

// WaveformOnDemandLocator is an optional AssetsLocator extension for a locator
// whose asset route generates a missing waveform when it is fetched. Audio
// decisions then advertise the returned name before the envelope exists, so a
// player can draw the waveform without waiting for the sweep (CORE-2401).
type WaveformOnDemandLocator interface {
	WaveformOnDemandAsset() string
}

// SetSelector wires the opaque selected-file selector minter (ADR-0098 area 2).
// Optional; nil (the default) leaves Parts carrying an empty selector — used only
// by degenerate/test paths, never by composition, which always wires it.
func (s *Service) SetSelector(selector *SelectorService) {
	s.selector = selector
}

// SetSealed wires the Sealed Library predicate (ADR-0098 area 2). Optional; nil
// (the default) treats no Library as sealed.
func (s *Service) SetSealed(sealed SealedPredicate) {
	s.sealed = sealed
}

// SetAssets wires the Generated Assets locator so playbackInfo can advertise
// trickplay, preview clip, and Marker thumbnail URLs. Optional; nil (the
// default) omits the assets block.
func (s *Service) SetAssets(assets AssetsLocator) {
	s.assets = assets
}

// SetSessions wires the HLS transcode session manager so playbackInfo can
// enforce the Profile's stream cap against running transcodes. Optional; nil
// (the default) counts zero active streams.
func (s *Service) SetSessions(sessions *Sessions) {
	s.sessions = sessions
	if sessions != nil && s.store != nil {
		sessions.SetKeyframeStore(s.store) // probe each audio-copy source once (CORE-0021)
	}
}

// SetPartyRoster wires the Watch-Party membership oracle so a
// transcode blocked by the concurrent-transcode cap degrades to the lowest rung
// for a party member instead of being refused. Optional;
// nil (the default) leaves the capacity refusal unchanged.
func (s *Service) SetPartyRoster(roster PartyRoster) {
	s.roster = roster
}

// SetQuirks installs a device-quirks database, replacing the
// release-embedded baseline. It VALIDATES its argument with the same rules
// ParseQuirkDB applies and installs a deep clone, so no path can bypass the
// safety rules — a directly-constructed DB denying the floor transcode target,
// or declaring a staged override family, is rejected here exactly as it is at
// parse. The invariants are properties of the DATABASE, so they must hold
// however it arrives. Like the other setters, it is a composition-time call:
// Info is concurrently callable and the installed DB is treated as immutable,
// so callers must not swap it while requests are in flight.
func (s *Service) SetQuirks(db QuirkDB) error {
	validated, err := validateQuirkDB(db)
	if err != nil {
		return err
	}
	s.quirks = validated
	return nil
}

// QuirksVersion reports the installed quirks database's opaque version, so a
// diagnostic bundle can tell "quirk missing" from "quirk stale"
// without reaching into the database.
func (s *Service) QuirksVersion() string {
	return s.quirks.Version
}

// NewService creates a playback service. It parses the release-embedded quirks
// baseline and PANICS if it does not validate: an invalid baseline is a build
// defect, not a runtime condition (conformance makes it a gate failure), and a
// server that silently dropped its quirk DB would violate ADR-0161 §5's
// offline "full function" guarantee without saying so.
func NewService(st *store.Store, tokens *streamtoken.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	quirks, err := ParseQuirkDB(baselineQuirkDB)
	if err != nil {
		panic("playback: embedded quirks baseline is invalid: " + err.Error())
	}
	return &Service{store: st, profileSessions: st, tokens: tokens, now: now, quirks: quirks}
}

// decision is the resolved ladder outcome for one media ID.
type decision struct {
	media    store.Media
	verdict  string // direct-play | transcode | unavailable
	reason   string
	streams  []StreamChoice
	timeline Timeline
	assets   *AssetsInfo
	// parts is the chosen Version's ordered Parts. A single-file
	// Version yields one Part; multi-Part Versions present back-to-back as one
	// timeline. Empty for unavailable items.
	parts []PartInfo
	// versions is the Version list for a picker; nil for a single Version.
	versions []VersionInfo
	// captionTracks is the chosen Version's sidecar caption tracks (FEAT-4 /
	// ADR-0005 §4), each with a Stream-Token'd WebVTT URL. Empty for unavailable
	// items or files with no sidecars.
	captionTracks []CaptionTrack
	// unrenderableCaptions counts the caption sidecars withheld from
	// captionTracks because this client declares no renderer for their format.
	// It is the difference between "this item has no subtitles"
	// and "this item's subtitles are ones you cannot draw", which is what the
	// resolver needs to state the absence rather than leave it silent.
	unrenderableCaptions int
	// audioLanguage is the language tag of the chosen file's first audio stream —
	// the audio the viewer will hear absent a client-side switch. It is the
	// mandatory audioTrackLang input to the subtitle pre-selection resolver:
	// the wrong-language-forced and smart-mode cases cannot be
	// expressed without it. Empty when the probe recorded no audio language.
	audioLanguage string
	// audioTracks lists the chosen file's audio streams with the delivered one
	// marked Selected (see resolveAudioTrack).
	audioTracks []AudioTrack
	// audioQuality is the selected audio track's source quality and how the
	// primary stream delivers it (see audioQualityFor); nil when unavailable
	// or the file has no audio.
	audioQuality *AudioQuality
	// loudness is the parsed loudness measurement for the chosen file (ADR-0098
	// area 3), read through the AssetsLocator content seam inside decide where the
	// fingerprint is known; loudnessOK is false when none exists yet. The verdict
	// (above) plus this is what the assembly site needs to derive the rung-specific
	// normalization state, and assets.MarkerThumbnailBase supplies the asset URL
	// base — so the InfoResponse assembler holds both halves of LoudnessInfo.
	loudness   LoudnessData
	loudnessOK bool
	// loudnessFingerprint is the chosen file's content fingerprint, carried so the
	// assembler can resolve the loudness.json asset name through the locator
	// without re-deriving the file. Empty when loudnessOK is false.
	loudnessFingerprint string
	// overlayApplied records whether the quirk overlay actually governed this
	// decision. It is set on every return path BELOW the negotiation point and
	// left false on the pre-negotiation short-circuits (unavailable, sealed,
	// image-class), which never consult the overlay. Attribution keys on it
	// rather than on the verdict: "unavailable" is ALSO the outcome of a fully
	// quirk-negotiated decision that then hit the Profile transcode gate or the
	// capacity refusal, which is the single most valuable thing a playback
	// report can say.
	overlayApplied bool
	// adjustment is the pitch shift and tempo change the streams encode; zero
	// when none was requested or it does not apply to this decision.
	adjustment ffmpeg.AudioAdjust
}

// PartInfo is one ordered Part of the negotiated Version. Parts play
// sequentially as a single timeline. Selector is the OPAQUE
// selected-file selector (ADR-0098 area 2): it identifies the Part's Version +
// position without exposing any path or content fingerprint, and resolves to the
// file plane only server-side under current authorization. Ordinal is its
// 0-based timeline position. The raw content fingerprint is never client-visible.
type PartInfo struct {
	Ordinal  int    `json:"ordinal"`
	Selector string `json:"selector"`
	// versionLabel and partOrdinal are the internal coordinates the selector is
	// minted from; they are never marshaled (the selector is the only wire form).
	versionLabel string
	partOrdinal  int
}

// mintSelectors stamps each Part's opaque Selector from its internal coordinates,
// scoped to the decision's Media + Profile and bounded by the same expiry as the
// Stream Token grant (ADR-0098 area 2). A nil selector service (degenerate/test
// path) leaves the selector empty. Mutates parts in place and returns them.
func (s *Service) mintSelectors(parts []PartInfo, mediaID store.MediaID, profileID store.ProfileID, expiresAt time.Time) []PartInfo {
	if s.selector == nil {
		return parts
	}
	for i := range parts {
		parts[i].Selector = s.selector.Mint(mediaID, profileID, parts[i].versionLabel, parts[i].partOrdinal, expiresAt)
	}
	return parts
}

// Info validates the request and returns the current playback decision.
func (s *Service) Info(ctx context.Context, scope auth.Scope, req InfoRequest) (InfoResponse, error) {
	mediaID := strings.TrimSpace(req.MediaID)
	if mediaID == "" {
		return InfoResponse{}, ErrInvalidRequest
	}
	var adjust ffmpeg.AudioAdjust
	if req.Adjustment != nil {
		if err := req.Adjustment.validate(); err != nil {
			return InfoResponse{}, err
		}
		adjust = req.Adjustment.audioAdjust()
	}
	ctx = s.compatDecisionContext(ctx, scope)
	// The session Profile's playback policy (transcode gate + stream cap)
	// applies to every rung decision below.
	policy, err := s.store.Profile(ctx, scope.ProfileID)
	if err != nil {
		return InfoResponse{}, err
	}
	// A foreground/interactive playhead ranks PriorityForeground for admission
	// control; an unset flag is PriorityNormal, unchanged.
	priority := PriorityNormal
	if req.Foreground {
		priority = PriorityForeground
	}
	// The device-quirk overlay is resolved ONCE per request, so the
	// "main" and "next" decisions below are judged under an identical overlay:
	// the device does not change between queue items, and a divergence there
	// would hand the client a prefetched stream the current item's overlay
	// forbids. The declared CapabilityProfile is passed through unmodified — the
	// overlay is never written back onto it, which is what makes the
	// monotone-restrictive rule a property of the code shape.
	//
	// A remote request carries the admin's remote bitrate cap as its
	// streaming ceiling when that cap is tighter than the client's own, so
	// every MaxStreamingBitrate consumer (direct-play veto, cached renditions,
	// ladder rungs, the HLS session key) honours the lower of the two.
	//
	// The overlay keeps the two apart (CORE-1864): the viewer's ceiling is
	// raised to the ladder floor and exempts audio-only items, while the
	// admin's cap is enforced as configured.
	capability, policyCap, err := s.remoteCappedProfile(ctx, req, policy)
	if err != nil {
		return InfoResponse{}, err
	}
	ov := s.quirks.Resolve(capability).withClientCeiling(req.CapabilityProfile.MaxStreamingBitrate).withPolicyCeiling(policyCap)
	main, err := s.decide(ctx, scope, mediaID, mainSuffix, capability, policy, priority, req.SelectedSubtitleIndex, req.SelectedAudioIndex, strings.TrimSpace(req.SelectedVersion), ov, adjust)
	if err != nil {
		return InfoResponse{}, err
	}
	resp := InfoResponse{
		MediaID:             mediaID,
		Decision:            main.verdict,
		Reason:              main.reason,
		StreamTokenRequired: true,
		Streams:             main.streams,
		Timeline:            main.timeline,
		Parts:               main.parts,
		Versions:            main.versions,
		TrackPreferences:    trackPreferences(policy),
		Assets:              main.assets,
		CaptionTracks:       main.captionTracks,
		AudioTracks:         main.audioTracks,
		AudioQuality:        main.audioQuality,
		// Flags describe the device, not the decision, so they are never
		// suppressed: a client applies them to its player regardless of which
		// item it fetched.
		BehaviorFlags: ov.flags,
		Adjustment:    playbackAdjustment(main.adjustment),
	}
	if main.overlayApplied {
		resp.AppliedQuirks = ov.overrideIDs
	}
	if main.media.ID != "" {
		resp.MediaID = string(main.media.ID)
		resp.MediaClass = string(main.media.Class)
		restrictedOrigin, err := s.store.LibraryRestricted(ctx, main.media.LibraryID)
		if err != nil {
			return InfoResponse{}, err
		}
		resp.RestrictedOrigin = restrictedOrigin
	}
	// Loudness/normalization state (ADR-0098 area 3): assembled here, the one site
	// that holds both the rung verdict and the assets token base. Capability-
	// derived (declared NormalizationModes), never client-name inferred.
	resp.Loudness = s.loudnessInfo(main, capability)
	// Server subtitle pre-selection: authoritative at playback
	// START only. Resolved here, the one site holding the item's caption tracks,
	// the Profile policy, and the audio language together.
	subtitles := s.resolveSubtitles(ctx, scope, main, resp.TrackPreferences)
	resp.SelectedSubtitleTrack = subtitles.selection
	resp.SubtitlesUnavailableReason = subtitles.unavailableReason
	if nextID := strings.TrimSpace(req.NextMediaID); nextID != "" {
		// Best-effort: any next item that cannot be served — unknown,
		// outside the session scope, or with no playable file — is silently
		// omitted; current playback is never affected.
		next, nextErr := s.decide(ctx, scope, nextID, "next", capability, policy, priority, nil, nil, "", ov, ffmpeg.AudioAdjust{})
		if nextErr == nil && next.verdict != "unavailable" {
			// Attribution tracks what the response actually carries: a next
			// decision the payload drops would put override IDs on a body whose
			// only visible decision is an unrelated main.
			if next.overlayApplied {
				resp.AppliedQuirks = ov.overrideIDs
			}
			resp.Next = &NextInfo{
				MediaID:       string(next.media.ID),
				MediaClass:    string(next.media.Class),
				Decision:      next.verdict,
				Streams:       next.streams,
				Timeline:      next.timeline,
				Parts:         next.parts,
				Assets:        next.assets,
				CaptionTracks: next.captionTracks,
				Loudness:      s.loudnessInfo(next, capability),
				AudioQuality:  next.audioQuality,
			}
		}
	}
	return resp, nil
}

// remoteCappedProfile returns the CapabilityProfile the decision runs under.
// A LAN request gets the declared profile unchanged. A remote request gets
// the lowest non-zero of the server-wide remote cap, the Profile's remote
// cap and the client's MaxStreamingBitrate as its streaming ceiling; the int
// is the admin cap (0 when none applies). It is returned even when the
// client's own ceiling is tighter: that ceiling is raised to the ladder floor
// and exempts audio-only items, so the admin cap must still be enforced on its
// own terms (CORE-1864).
func (s *Service) remoteCappedProfile(ctx context.Context, req InfoRequest, policy store.Profile) (CapabilityProfile, int, error) {
	capability := req.CapabilityProfile
	if !req.RemoteClient {
		return capability, 0, nil
	}
	cfg, err := s.store.ConsoleConfig(ctx)
	if err != nil {
		return CapabilityProfile{}, 0, err
	}
	limit := minNonZero(cfg.RemoteMaxBitrate, policy.RemoteMaxBitrate)
	if limit <= 0 {
		return capability, 0, nil
	}
	if capability.MaxStreamingBitrate <= 0 || capability.MaxStreamingBitrate > limit {
		capability.MaxStreamingBitrate = limit
	}
	return capability, limit, nil
}

// PreviewInfo returns only the Generated Asset URL block needed for card
// preview clips. It never performs playback negotiation and never issues
// media-direct or media-hls tokens, so hover previews do not become active
// playback sessions.
func (s *Service) PreviewInfo(ctx context.Context, scope auth.Scope, req PreviewInfoRequest) (PreviewInfoResponse, error) {
	mediaID := strings.TrimSpace(req.MediaID)
	if mediaID == "" {
		return PreviewInfoResponse{}, ErrInvalidRequest
	}
	media, err := s.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		var validation store.ValidationError
		if errors.As(err, &validation) {
			return PreviewInfoResponse{}, nil
		}
		return PreviewInfoResponse{}, err
	}
	// A Sealed Library advertises no preview asset (ADR-0098 area 2).
	if s.sealed != nil && s.sealed(media.LibraryID) {
		return PreviewInfoResponse{}, nil
	}
	file, err := s.store.PrimaryMediaFile(ctx, media.ID, scope.LibraryFilter())
	if err != nil {
		return PreviewInfoResponse{MediaID: string(media.ID)}, nil
	}
	restricted, err := s.store.LibraryRestricted(ctx, media.LibraryID)
	if err != nil {
		return PreviewInfoResponse{}, err
	}
	expiresAt := s.now().Add(time.Hour).UTC()
	assets, err := s.assetsInfo(ctx, streamtoken.Grant{
		MediaID:    media.ID,
		DeviceID:   scope.DeviceID,
		SessionID:  scope.SessionID,
		ProfileID:  scope.ProfileID,
		Restricted: restricted,
		ExpiresAt:  expiresAt,
	}, file.ContentFingerprint, false, expiresAt)
	if err != nil {
		return PreviewInfoResponse{}, err
	}
	if assets != nil && assets.PreviewClip == "" {
		assets = nil
	}
	return PreviewInfoResponse{MediaID: string(media.ID), Assets: assets}, nil
}

// decide runs the full playback ladder (direct-play, then server HLS
// transcode) for one media ID under the session scope. An unavailable
// verdict carries a reason; a media the scope cannot see is indistinguishable
// from an unknown ID (Reveal contract). suffix distinguishes stream IDs for
// the current ("main") vs declared next ("next") item. priority is the
// admission rank the transcode-refusal check uses: a foreground
// playhead may be admitted by preempting a normal stream, so the verdict it
// receives matches what the byte layer would actually start at that rank.
// adjust is the requested pitch shift and tempo change; it applies only to an
// audio Media served through a transcode, which it then forces.
func (s *Service) decide(ctx context.Context, scope auth.Scope, mediaID, suffix string, profile CapabilityProfile, policy store.Profile, priority Priority, selectedSubtitleIndex, selectedAudioIndex *int, selectedVersion string, ov quirkOverlay, adjust ffmpeg.AudioAdjust) (dec decision, err error) {
	media, err := s.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		var validation store.ValidationError
		if errors.As(err, &validation) {
			return decision{verdict: "unavailable", reason: "media-unavailable", streams: []StreamChoice{}}, nil
		}
		return decision{}, err
	}
	// A Sealed Library gets no stream, no selector-bearing Parts, and no asset
	// advertisement (ADR-0098 area 2). Indistinguishable from unavailable on the
	// wire, exactly like a Library the scope cannot see (Reveal contract).
	if s.sealed != nil && s.sealed(media.LibraryID) {
		return decision{verdict: "unavailable", reason: "media-unavailable", streams: []StreamChoice{}}, nil
	}
	timeline := Timeline{DurationSeconds: media.DurationSeconds}
	if progress, ok, err := s.store.PlaybackProgressFor(ctx, scope.ProfileID, media.ID); err == nil && ok && !progress.Watched {
		timeline.PositionSeconds = progress.PositionSeconds
	}
	// 1:N files: fetch every available file, group into Versions
	// (alternate encodes) and Parts (timeline segments), then negotiate the best
	// Version for this client BEFORE choosing a stream. A single-file Media
	// yields one Version with one Part, so the negotiation is a no-op there and
	// existing behaviour is unchanged.
	files, err := s.store.MediaFilesForMedia(ctx, media.ID, scope.LibraryFilter())
	if err != nil {
		return decision{media: media, verdict: "unavailable", reason: "media-file-unavailable", streams: []StreamChoice{}, timeline: timeline}, nil
	}
	// Image-class posture: a still is served as direct authenticated
	// bytes, like a download. There is no codec negotiation, no transcode/HLS
	// ladder, no timeline, and none of the video/audio Generated Assets — so the
	// image path short-circuits before the capability ladder, which judges
	// containers no Capability Profile declares (jpg/png/…) and would otherwise
	// fall an image through to a transcode it can never start.
	if media.Class == store.MediaClassImage {
		return s.imageDecision(ctx, scope, media, files, suffix)
	}
	factsFingerprint, versioned, err := s.store.MediaFactsFingerprint(ctx, media.ID)
	if err != nil {
		return decision{}, err
	}
	file, streams, parts, versions, err := s.negotiateVersion(ctx, media, files, profile, selectedSubtitleIndex, selectedVersion, factsFingerprint, ov)
	if err != nil {
		return decision{}, err
	}
	// alternate marks a negotiated file other than the one the Media row's
	// frame size describes (the first Version, which may be missing). Under a
	// height ceiling, passthrough below is barred for it, or for any Version
	// of a multi-Version Media (counting missing Versions, so the guard holds
	// while only one is live), while its frame height is unknown.
	alternate := file.ContentFingerprint != factsFingerprint
	if len(versions) > 0 || alternate {
		// Every rung below judges the negotiated Version by its own file facts.
		media = versionFacts(media, file, streams, !alternate)
	}
	// A Media stored without a runtime (its container declared none) is
	// probed on demand, so the timeline and a live session's playlist cover
	// the whole file instead of ending where the encoder is (CORE-2314).
	if media.DurationSeconds <= 0 && len(parts) <= 1 {
		if seconds := s.probedDuration(ctx, media, file); seconds > 0 {
			media.DurationSeconds = seconds
			timeline.DurationSeconds = seconds
		}
	}
	barred := unknownHeightBarred(media, profile, alternate || versioned || len(versions) > 0)
	// One deferred stamp covers every decision return below, like
	// audioLanguage: the picker lists the same Versions whatever the verdict,
	// so a viewer whose pick cannot play can still pick another.
	defer func() {
		if err == nil {
			dec.versions = versions
		}
	}()
	// The operator's live-session dashboard reads the main item's decision.
	if suffix == mainSuffix {
		defer func() {
			if err == nil {
				s.observe(scope.SessionID, dec, file, streams, profile, selectedSubtitleIndex, ov)
			}
		}()
	}
	loudness, loudnessOK := s.loudnessMeasurement(media, file)

	// Loudness state (ADR-0098 area 3): once the chosen Version's file is known,
	// stamp the parsed loudness measurement (if any) onto whatever decision this
	// call returns, so the InfoResponse assembler can derive the rung-specific
	// normalization state from the verdict + measurement + declared capability.
	// The lookup happens before HLS grant minting so a transcode chosen for other
	// reasons can carry the same GainDB to FFmpeg; direct play still only reports
	// passthrough-supported/unsupported and never forces a worse rung.
	if media.Class == store.MediaClassAudio && file.ContentFingerprint != "" && s.assets != nil {
		fingerprint := file.ContentFingerprint
		defer func() {
			if err != nil || dec.verdict == "" || dec.verdict == "unavailable" {
				return
			}
			if loudnessOK {
				dec.loudness = loudness
				dec.loudnessOK = true
				dec.loudnessFingerprint = fingerprint
			}
		}()
	}
	restricted, err := s.store.LibraryRestricted(ctx, media.LibraryID)
	if err != nil {
		return decision{}, err
	}
	// The token must outlive the playback it authorizes: AVPlayer keeps making
	// range requests against the same URL for the whole runtime (plus pauses and
	// seeks). Revocation and relock are enforced live on every byte request via
	// the session scope, so a long expiry only bounds URL replay, not access.
	tokenLifetime := max(time.Hour, 2*time.Duration(media.DurationSeconds)*time.Second)
	expiresAt := s.now().Add(tokenLifetime).UTC()
	// Stamp each Part's opaque selector now that the grant expiry is known, so the
	// selector and Stream Token share one expiry boundary (ADR-0098 area 2).
	parts = s.mintSelectors(parts, media.ID, scope.ProfileID, expiresAt)
	receiverBound := isReceiverCapabilityProfile(profile)
	grant := streamtoken.Grant{
		MediaID:                 media.ID,
		DeviceID:                scope.DeviceID,
		SessionID:               scope.SessionID,
		ProfileID:               scope.ProfileID,
		Restricted:              restricted,
		ProfileCapability:       CapabilityFingerprint(profile),
		ReceiverBound:           receiverBound,
		ReceiverProfileResolved: receiverBound && ov.matched,
		Foreground:              priority == PriorityForeground,
		ExpiresAt:               expiresAt,
	}
	// A Media with several Versions binds the negotiated file into every token
	// minted from this grant, the primary's included, so the byte routes serve
	// exactly the bytes negotiated here. Unbound, a primary-file token would
	// follow PrimaryMediaFile to another Version once its file went missing.
	// A single-Version Media leaves it empty and keeps the primary-file path.
	if len(versions) > 0 {
		grant.FileFingerprint = file.ContentFingerprint
	}
	if burnSubtitleIndex := selectedBurnSubtitleIndex(streams, profile, selectedSubtitleIndex, ov); burnSubtitleIndex >= 0 {
		grant.BurnSubtitleIndex = burnSubtitleIndex + 1
	}
	// The audio track: the viewer's pick, else the Profile's preferred
	// language, else the file's default. A file with several audio streams
	// names it in the grant so a server stream maps exactly that track instead
	// of FFmpeg's own choice; one that is not the default rules out direct
	// play and the untouched precomputed rendition below, which carry the
	// default track only.
	audio := resolveAudioTrack(streams, selectedAudioIndex, policy.AudioLanguages)
	if len(audio.tracks) > 1 {
		grant.AudioStreamIndex = audio.chosen + 1
	}
	defer func() { dec.audioTracks = audio.tracks }()
	if s.sessions != nil {
		// Hand the resume position to the HLS session this decision addresses,
		// so a resumed transcode starts its encoder once, at that position.
		key := SessionKey{SessionID: scope.SessionID, MediaID: media.ID, ProfileCapability: grant.ProfileCapability}
		defer func() {
			if err != nil {
				return
			}
			for _, stream := range dec.streams {
				if stream.Kind == "hls" {
					s.sessions.NoteResumePosition(key, dec.timeline.PositionSeconds)
					return
				}
			}
		}()
	}
	// Passthrough and the encoder target are judged on the track the stream
	// maps (CORE-1874, CORE-1627), not on whichever of the file's tracks a
	// client could take as is.
	hlsAudio := hlsAudioProfile(profile, audio.chosenStreams(streams), audio.chosen, loudness, loudnessOK, ov)
	if ov.ceilingBindsAudio(media, file, streams) {
		hlsAudio = ceilingBoundAudio(hlsAudio)
	}
	// The audio-only rung advertises the peak of the audio it carries
	// (CORE-1849): the chosen track, or the first one as 0:a:0 maps it.
	audioSource := AudioSourceStream(audio.chosenStreams(streams), nil)
	audioOnlyPeak := audioOnlyBandwidth(hlsAudio, audioSource, false)
	// An audio-only stream whose peak exceeds the admin's cap drops to stereo
	// AAC, which fits any sane cap; policyTranscodeRefusal refuses one that
	// still does not (CORE-1864).
	if media.Class == store.MediaClassAudio && ov.policyMaxBitrate > 0 && audioOnlyPeak > ov.policyMaxBitrate {
		hlsAudio = ceilingBoundAudio(hlsAudio)
		audioOnlyPeak = audioOnlyBandwidth(hlsAudio, audioSource, false)
	}
	policyRefusal := ov.policyTranscodeRefusal(media, audioOnlyPeak)

	// Generated Asset advertisement: one assets-target token covers
	// the trickplay manifest + sheets, the preview clip, and Marker
	// thumbnails; the byte route re-verifies scope and reveal on every fetch.
	assets, err := s.assetsInfo(ctx, grant, file.ContentFingerprint, media.Class == store.MediaClassAudio, expiresAt)
	if err != nil {
		return decision{}, err
	}

	// Sidecar caption tracks: the .srt/.vtt files
	// discovered next to the chosen Version's primary file at scan, advertised as
	// token-bearing WebVTT URLs so the client lists them in the track menu
	// alongside the muxed subtitle tracks it reads from the stream.
	captionTracks, err := s.muxedCaptionTracksInfo(ctx, grant, file.ContentFingerprint, streams, suffix)
	if err != nil {
		return decision{}, err
	}
	subtitleFormats := ov.subtitleFormats(profile.SubtitleFormats)
	sidecarCaptionTracks, unrenderableCaptions, err := s.captionTracksInfo(ctx, grant, file.ContentFingerprint, suffix, expiresAt, subtitleFormats)
	if err != nil {
		return decision{}, err
	}
	captionTracks = append(captionTracks, sidecarCaptionTracks...)
	// One deferred stamp covers every decision return below, exactly like
	// audioLanguage: a withheld-caption count that some paths dropped would make
	// the "never silent" notice depend on which verdict the item happened to get.
	defer func() { dec.unrenderableCaptions = unrenderableCaptions }()
	// The audio language the viewer will hear is the mandatory audioTrackLang
	// input to subtitle pre-selection. decide has named returns, so
	// one deferred stamp covers every decision path below this point rather than
	// repeating the assignment at each return.
	audioLanguage := audio.language()
	defer func() { dec.audioLanguage = audioLanguage }()
	// Gapless trims (CORE-1626), one deferred stamp for every path. A
	// composed picture or a pitch/tempo adjustment re-times the audio, so
	// neither stream carries the source's arithmetic.
	defer func() {
		if err == nil && dec.reason != "composed-picture" && dec.adjustment.IsZero() {
			stampGapless(dec.streams, audio.chosenStreams(streams), hlsAudio, dec.timeline.PositionSeconds)
		}
	}()
	// Audio quality (CORE-1627), one deferred stamp for every path below:
	// the selected track's source facts and whether the primary stream
	// delivers them bit-for-bit. An unavailable verdict carries no stream to
	// describe, so audioQualityFor leaves it nil.
	defer func() {
		if err == nil {
			dec.audioQuality = audioQualityFor(dec.streams, streams, audio.chosen, hlsAudio, dec.adjustment, profile)
		}
	}()

	// The transcode pipeline is policy- and admission-gated before it is
	// offered anywhere. An honest verdict beats a stream that the server
	// would refuse to start (ADR-0010: never promise what cannot be served).
	transcodeRefusal := s.transcodeRefusal(ctx, scope, media.ID, profile, policy, priority)

	sourceVideo := VideoSignalFromStreams(streams)
	sourceHasAudio := hasAudioStream(streams)
	// A pitch shift or tempo change forces a transcode that encodes it: the
	// live composed picture when one applies, otherwise an audio-only stream.
	// It needs an audio Media with audio and a transcode the policy and
	// capacity admit; otherwise the item plays unadjusted.
	// A build without the adjustment's filters (or the audio encoder) declines
	// the adjustment rather than offer a stream that fails to start.
	if !adjust.IsZero() && media.Class == store.MediaClassAudio && sourceHasAudio && transcodeRefusal == "" && s.sessions != nil &&
		audioAdjustSupported(adjustedAudioRendition(hlsAudio, adjust)) {
		grant = withAudioAdjustGrant(grant, adjust)
	} else {
		adjust = ffmpeg.AudioAdjust{}
	}
	// A Dolby Vision P7 stream never satisfies canDirectPlay (no client advertises
	// "dolby-vision-p7"), but its HDR10-compatible base layer direct-plays while the
	// enhancement layer is dropped — the honest base-layer posture of ADR-0017 §4.
	// The reason makes that explicit in playbackInfo; the stream is a normal
	// direct-play of the file bytes.
	// Composed picture: an audio Media whose plugin describes a
	// picture plays as its composed video on a video-capable client — ahead
	// of direct play, which would lose the picture. A Ready precomputed
	// rendition is served as is; otherwise the picture is composed live when
	// the transcode policy and capacity admit it.
	if composed, live, ok, err := s.composedChoice(ctx, scope, grant, suffix, media, file, profile, hlsAudio, expiresAt, ov, transcodeRefusal, audio.overridesDefault()); err != nil {
		return decision{}, err
	} else if ok {
		verdict := "precomputed-rendition"
		if live {
			verdict = "transcode"
		}
		return decision{
			media:          media,
			verdict:        verdict,
			reason:         "composed-picture",
			streams:        []StreamChoice{composed},
			timeline:       timeline,
			parts:          parts,
			assets:         assets,
			captionTracks:  captionTracks,
			overlayApplied: true,
			adjustment:     adjust,
		}, nil
	}
	if !adjust.IsZero() {
		adjusted, err := s.adjustedAudioChoice(ctx, grant, suffix, hlsAudio, audioSource, expiresAt)
		if err != nil {
			return decision{}, err
		}
		return decision{
			media:          media,
			verdict:        "transcode",
			reason:         "playback-adjustment",
			streams:        []StreamChoice{adjusted},
			timeline:       timeline,
			parts:          parts,
			assets:         assets,
			captionTracks:  captionTracks,
			overlayApplied: true,
			adjustment:     adjust,
		}, nil
	}
	directPlay := !barred && !audio.overridesDefault() && canDirectPlay(media, file, streams, profile, selectedSubtitleIndex, ov)
	p7BaseLayer := !barred && !directPlay && !audio.overridesDefault() &&
		dolbyVisionP7BaseLayer(media, file, streams, profile, selectedSubtitleIndex, ov)
	if directPlay || p7BaseLayer {
		grant.Target = streamtoken.TargetMediaDirect
		token, err := s.tokens.Issue(ctx, grant)
		if err != nil {
			return decision{}, err
		}
		streams := []StreamChoice{
			{
				ID:        "direct-" + suffix,
				Kind:      "direct-play",
				URL:       "/api/media/direct/" + token,
				Token:     token,
				ExpiresAt: expiresAt.Format(time.RFC3339),
			},
		}
		// ADR-0010 §8: a manual rendition pin is offered on direct-playable
		// files too (the weak-WiFi case) — but only when the transcode could
		// actually start right now.
		if transcodeRefusal == "" && sourceVideo.FullTranscodeRefusal() == "" && policyRefusal == "" {
			hls, err := s.hlsChoice(ctx, grant, suffix, media, profile, hlsAudio, audioOnlyPeak, expiresAt, ov)
			if err != nil {
				return decision{}, err
			}
			streams = append(streams, hls)
		}
		reason := ""
		if p7BaseLayer {
			reason = dolbyVisionP7BaseLayerReason
		} else if transcodeRefusal == "transcode-disallowed" {
			// The Profile gate withheld the HLS stream (CORE-2181): a client
			// whose decoder then rejects the file can say converting is off
			// for this Profile rather than blame the format alone.
			reason = transcodeRefusal
		}
		return decision{
			media:          media,
			verdict:        "direct-play",
			reason:         reason,
			streams:        streams,
			timeline:       timeline,
			parts:          parts,
			assets:         assets,
			captionTracks:  captionTracks,
			overlayApplied: true,
		}, nil
	}

	if grant.BurnSubtitleIndex == 0 && !audio.overridesDefault() {
		if cached, ok, err := s.cachedRenditionChoice(ctx, scope, grant, suffix, media.ID, file.ContentFingerprint, profile, sourceVideo, sourceHasAudio, hlsAudio, expiresAt, ov); err != nil {
			return decision{}, err
		} else if ok {
			reason := "cached-rendition"
			// The cache is consulted before the video-copy rung, so only a
			// direct play the ceiling refused would have beaten it.
			// An unknown-height bar refuses that rung whatever the ceiling.
			if !barred && bitrateCeilingRefusedDirectPlay(media, file, streams, profile, selectedSubtitleIndex, ov) {
				reason = ov.ceilingReason(media)
			}
			return decision{
				media:          media,
				verdict:        "precomputed-rendition",
				reason:         reason,
				streams:        []StreamChoice{cached},
				timeline:       timeline,
				parts:          parts,
				assets:         assets,
				captionTracks:  captionTracks,
				overlayApplied: true,
			}, nil
		}
	}

	if transcodeRefusal != "" {
		// Capacity degrade (ADR-0010 §6 "drop to this quality"; PANEL-16): a real
		// Watch-Party member blocked only by the server's concurrent-transcode cap
		// is admitted at the cheapest honest rung rather than refused, so the party
		// stays whole. Only the server encoder-capacity refusal degrades
		// — a Profile policy gate (transcode disallowed, per-Profile stream cap)
		// still hard-refuses. Membership is server-authoritative (the conductor
		// roster), so this is not a client-spoofable cap bypass.
		if transcodeRefusal == "transcode-capacity-reached" && s.roster != nil &&
			s.roster.SessionInWatchPartyForMedia(scope.SessionID, media.ID) {
			var hls StreamChoice
			var err error
			if !barred && audioCopyEligible(media, file, streams, profile, selectedSubtitleIndex, ov) {
				// Video direct-plays; only audio needs transcode — copy the video and
				// transcode only the audio, lighter than a re-encode.
				hls, err = s.degradedAudioCopyChoice(ctx, grant, suffix, hlsAudio, expiresAt)
			} else {
				if reason := cmp.Or(sourceVideo.FullTranscodeRefusal(), policyRefusal); reason != "" {
					return decision{media: media, verdict: "unavailable", reason: reason, streams: []StreamChoice{}, timeline: timeline, parts: parts, assets: assets, captionTracks: captionTracks, overlayApplied: true}, nil
				}
				hls, err = s.degradedHLSChoice(ctx, grant, suffix, media, profile, hlsAudio, audioOnlyPeak, expiresAt, ov)
			}
			if err != nil {
				return decision{}, err
			}
			return decision{
				media:          media,
				verdict:        "transcode",
				reason:         "capacity-degraded",
				streams:        []StreamChoice{hls},
				timeline:       timeline,
				parts:          parts,
				assets:         assets,
				captionTracks:  captionTracks,
				overlayApplied: true,
			}, nil
		}
		return decision{media: media, verdict: "unavailable", reason: transcodeRefusal, streams: []StreamChoice{}, timeline: timeline, parts: parts, assets: assets, captionTracks: captionTracks, overlayApplied: true}, nil
	}

	// Audio-only transcode (ADR-0010 §5, "the single biggest transcode-cost
	// win"): the client can decode the video as-is but not the audio codec
	// (e.g. H.264/HEVC video with TrueHD or DTS audio). Copy the video stream
	// through and transcode only the audio — far cheaper than a full
	// re-encode. Resolved as an HLS choice carrying the single audio-copy
	// rendition.
	// A non-default audio track the client could otherwise play as-is takes
	// the same rung: the video is copied and only the picked track re-encoded.
	audioTrackCopy := audio.overridesDefault() && videoCopyEligible(media, file, streams, profile, selectedSubtitleIndex, ov)
	if !barred && (audioTrackCopy || audioCopyEligible(media, file, streams, profile, selectedSubtitleIndex, ov)) {
		hls, err := s.audioCopyChoice(ctx, grant, suffix, hlsAudio, expiresAt)
		if err != nil {
			return decision{}, err
		}
		reason := audioCopyReason(streams, profile, ov)
		if audioTrackCopy && reason == "container-remux" {
			reason = "audio-transcode-only"
		}
		return decision{
			media:          media,
			verdict:        "direct-stream",
			reason:         reason,
			streams:        []StreamChoice{hls},
			timeline:       timeline,
			parts:          parts,
			assets:         assets,
			captionTracks:  captionTracks,
			overlayApplied: true,
		}, nil
	}
	if reason := cmp.Or(sourceVideo.FullTranscodeRefusal(), policyRefusal); reason != "" {
		return decision{media: media, verdict: "unavailable", reason: reason, streams: []StreamChoice{}, timeline: timeline, parts: parts, assets: assets, captionTracks: captionTracks, overlayApplied: true}, nil
	}

	// Fall to the server HLS transcode rung: the session starts
	// lazily on the first segment request.
	hls, err := s.hlsChoice(ctx, grant, suffix, media, profile, hlsAudio, audioOnlyPeak, expiresAt, ov)
	if err != nil {
		return decision{}, err
	}
	reason := "requires-transcode"
	// Under an unknown-height bar no passthrough rung was open to the ceiling.
	if !barred && bitrateCeilingForced(media, file, streams, profile, selectedSubtitleIndex, ov) {
		reason = ov.ceilingReason(media)
	}
	return decision{
		media:          media,
		verdict:        "transcode",
		reason:         reason,
		streams:        []StreamChoice{hls},
		timeline:       timeline,
		parts:          parts,
		assets:         assets,
		captionTracks:  captionTracks,
		overlayApplied: true,
	}, nil
}

// imageDecision is the image-class playback posture: a still is
// served as direct authenticated bytes — a "direct-play" verdict carrying one
// TargetMediaDirect stream, no transcode/HLS rung, no timeline, no Generated
// Assets, and no caption tracks. The Stream Token still gates the bytes
// (Reveal-filtered, scope-bound) exactly like a video/audio direct stream; the
// /api/media/direct byte route serves the file with the right Content-Type and
// range support without any image-specific code. files is the Media's file set
// (ordered primary-first); an image Media with no servable file is unavailable.
func (s *Service) imageDecision(ctx context.Context, scope auth.Scope, media store.Media, files []store.MediaFile, suffix string) (decision, error) {
	if len(files) == 0 {
		return decision{media: media, verdict: "unavailable", reason: "media-file-unavailable", streams: []StreamChoice{}}, nil
	}
	restricted, err := s.store.LibraryRestricted(ctx, media.LibraryID)
	if err != nil {
		return decision{}, err
	}
	// A still has no runtime, so there is no playhead to outlive — a flat hour
	// bounds URL replay (revocation is enforced live per byte request anyway).
	expiresAt := s.now().Add(time.Hour).UTC()
	grant := streamtoken.Grant{
		MediaID:    media.ID,
		DeviceID:   scope.DeviceID,
		SessionID:  scope.SessionID,
		ProfileID:  scope.ProfileID,
		Restricted: restricted,
		ExpiresAt:  expiresAt,
		Target:     streamtoken.TargetMediaDirect,
	}
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return decision{}, err
	}
	return decision{
		media:   media,
		verdict: "direct-play",
		streams: []StreamChoice{
			{
				ID:        "direct-" + suffix,
				Kind:      "direct-play",
				URL:       "/api/media/direct/" + token,
				Token:     token,
				ExpiresAt: expiresAt.Format(time.RFC3339),
			},
		},
		timeline: Timeline{},
		parts: s.mintSelectors([]PartInfo{
			{Ordinal: 0, versionLabel: files[0].VersionLabel, partOrdinal: files[0].PartOrdinal},
		}, media.ID, scope.ProfileID, expiresAt),
	}, nil
}

// negotiateVersion picks the best Version for the client among a Media's
// available files, returning the chosen Version's primary file
// (its first Part), that file's stream inventory, the Version's ordered
// Parts, and the Version list for a picker. files is ordered by (VersionLabel,
// PartOrdinal), so consecutive files with the same label are one Version's
// Parts in timeline order.
//
// Selection: a selectedVersion naming one of the Media's Versions wins, even
// when it needs a transcode: the viewer asked for it. Otherwise prefer the
// first Version (in label order) whose primary Part direct-plays for this
// capability profile — direct-play beats transcode, and negotiating it BEFORE
// transcode is the whole point (a 1080p SDR Version direct-plays where the 4K
// HDR sibling would force a tone-map transcode). If no Version direct-plays,
// fall back to the first Version, which is the default ("" label) when
// present — identical to the pre-1:N primary-file choice. An unknown
// selectedVersion is ignored.
//
// A single-file Media has one Version with one Part, so this returns that file
// unchanged and no Version list: existing behaviour is preserved.
func (s *Service) negotiateVersion(ctx context.Context, media store.Media, files []store.MediaFile, profile CapabilityProfile, selectedSubtitleIndex *int, selectedVersion, factsFingerprint string, ov quirkOverlay) (store.MediaFile, []store.MediaFileStream, []PartInfo, []VersionInfo, error) {
	versions := groupVersions(files)
	streams := make([][]store.MediaFileStream, len(versions))
	for i, version := range versions {
		inventory, err := s.store.MediaFileStreams(ctx, version[0].ContentFingerprint)
		if err != nil {
			return store.MediaFile{}, nil, nil, nil, err
		}
		streams[i] = inventory
	}
	// Default to the first Version (label order: "" sorts first), with its
	// primary Part's streams — the honest transcode fallback.
	chosen := 0
	selected := false
	if selectedVersion != "" {
		for i, version := range versions {
			if versionID(media.ID, version[0].VersionLabel) == selectedVersion {
				chosen, selected = i, true
				break
			}
		}
	}
	eligible := func(i int) bool {
		trusted := versions[i][0].ContentFingerprint == factsFingerprint
		if len(versions) < 2 && trusted {
			return directPlayEligible(media, versions[i][0], streams[i], profile, selectedSubtitleIndex, ov)
		}
		facts := versionFacts(media, versions[i][0], streams[i], trusted)
		return !unknownHeightBarred(facts, profile, true) &&
			directPlayEligible(facts, versions[i][0], streams[i], profile, selectedSubtitleIndex, ov)
	}
	if !selected && !eligible(0) {
		// Look for a Version whose primary Part the client can play as-is (or whose
		// P7 HDR10 base layer plays), preferring it over a transcoding default.
		for i := range versions {
			if eligible(i) {
				chosen = i
				break
			}
		}
	}
	infos := versionInfos(media.ID, versions, streams, chosen)
	version := versions[chosen]
	// A single-Part Version is the pre-1:N shape (one file): emit no Parts array,
	// so its playbackInfo stays byte-identical to before — clients ignore the
	// absent field and play the primary stream as today (no wire-contract churn,
	// no regression). Only a genuinely multi-Part Version surfaces the timeline.
	if len(version) < 2 {
		return version[0], streams[chosen], nil, infos, nil
	}
	parts := make([]PartInfo, len(version))
	for i, part := range version {
		parts[i] = PartInfo{Ordinal: part.PartOrdinal, versionLabel: part.VersionLabel, partOrdinal: part.PartOrdinal}
	}
	return version[0], streams[chosen], parts, infos, nil
}

// groupVersions splits a (VersionLabel, PartOrdinal)-ordered file list into
// Versions, each a slice of its Parts in timeline order. Consecutive files
// sharing a VersionLabel are one Version.
func groupVersions(files []store.MediaFile) [][]store.MediaFile {
	var versions [][]store.MediaFile
	for i, file := range files {
		if i > 0 && file.VersionLabel == files[i-1].VersionLabel {
			versions[len(versions)-1] = append(versions[len(versions)-1], file)
			continue
		}
		versions = append(versions, []store.MediaFile{file})
	}
	return versions
}

// transcodeRefusal returns the honest reason no transcode can start for this
// (Profile, Media) right now, or "" when one could: the Profile's transcode
// gate and stream cap, then server admission control (ADR-0010 §6 — checked
// at start, so playbackInfo never advertises a transcode the server would
// refuse). priority is the admission rank the start would use: a foreground
// newcomer may preempt a strictly-lower-priority stream at
// capacity, so checking admission at that rank keeps the verdict honest — a
// foreground playhead is not refused for capacity it could actually claim.
func (s *Service) transcodeRefusal(ctx context.Context, scope auth.Scope, mediaID store.MediaID, profile CapabilityProfile, policy store.Profile, priority Priority) string {
	if !policy.TranscodeAllowed {
		return "transcode-disallowed"
	}
	if policy.MaxConcurrentStreams != nil &&
		s.activeProfileTranscodes(ctx, scope, mediaID) >= *policy.MaxConcurrentStreams {
		return "stream-limit-reached"
	}
	if s.sessions != nil && !s.sessions.CanAdmitPriority(SessionKey{
		SessionID:         scope.SessionID,
		MediaID:           mediaID,
		ProfileCapability: CapabilityFingerprint(profile),
	}, priority) {
		return "transcode-capacity-reached"
	}
	return ""
}

// hlsChoice builds the HLS stream option: a master-playlist URL plus the
// honest rendition inventory the master will advertise (ADR-0010 §8 — the
// rungs are derived from the source dimensions and the client's decode ceiling,
// and never exceed either). profile carries the client's declared capability,
// the one place the CapabilityProfile is in hand, so the advertised ladder is
// clipped to what the client can actually decode (rungsForClient); a profile
// that declares no per-codec ceiling falls back to source-only rungs exactly as
// before (clientLadderCeiling returns 0).
func (s *Service) hlsChoice(ctx context.Context, grant streamtoken.Grant, suffix string, media store.Media, profile CapabilityProfile, audio ffmpeg.AudioProfile, audioOnlyPeak int, expiresAt time.Time, ov quirkOverlay) (StreamChoice, error) {
	grant.Target = streamtoken.TargetMediaHLS
	grant = withHLSAudioGrant(grant, audio)
	caps := s.ladderCaps(profile, ov)
	grant = withHLSLadderGrant(grant, caps)
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return StreamChoice{}, err
	}
	rungs := advertisedRungs(media, caps, audioOnlyPeak)
	renditions := make([]RenditionInfo, 0, len(rungs))
	for _, rung := range rungs {
		renditions = append(renditions, RenditionInfo{ID: rung.ID, Height: advertisedRungHeight(media, rung), Bandwidth: rung.Bandwidth})
	}
	return StreamChoice{
		ID:         "hls-" + suffix,
		Kind:       "hls",
		URL:        "/api/media/hls/" + token + "/" + masterPlaylistName,
		Token:      token,
		ExpiresAt:  expiresAt.Format(time.RFC3339),
		Renditions: renditions,
	}, nil
}

func (s *Service) cachedRenditionChoice(ctx context.Context, scope auth.Scope, grant streamtoken.Grant, suffix string, mediaID store.MediaID, contentFingerprint string, profile CapabilityProfile, sourceVideo VideoSignal, sourceHasAudio bool, wantedAudio ffmpeg.AudioProfile, expiresAt time.Time, ov quirkOverlay) (StreamChoice, bool, error) {
	renditions, err := s.store.ReadyPlaybackRenditions(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return StreamChoice{}, false, err
	}
	for _, rendition := range renditions {
		if rendition.ContentFingerprint != contentFingerprint {
			continue
		}
		// Composed renditions carry a picture the source does not have; only
		// the dedicated composed branch (composedChoice) may hand one out.
		if rendition.RungID == ComposedRungID {
			continue
		}
		if !precomputedRenditionCompatible(rendition, profile, sourceVideo, sourceHasAudio, wantedAudio, ov) {
			continue
		}
		choice, err := s.precomputedStreamChoice(ctx, grant, suffix, rendition, expiresAt)
		return choice, err == nil, err
	}
	return StreamChoice{}, false, nil
}

// precomputedStreamChoice issues the HLS token and stream entry for one Ready
// precomputed rendition.
func (s *Service) precomputedStreamChoice(ctx context.Context, grant streamtoken.Grant, suffix string, rendition store.PlaybackRendition, expiresAt time.Time) (StreamChoice, error) {
	grant.Target = streamtoken.TargetMediaHLS
	grant.PlaybackRenditionID = rendition.ID
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return StreamChoice{}, err
	}
	return StreamChoice{
		ID:        "precomputed-" + suffix,
		Kind:      "hls",
		URL:       "/api/media/hls/" + token + "/" + masterPlaylistName,
		Token:     token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Renditions: []RenditionInfo{{
			ID:        rendition.RungID,
			Height:    rendition.Height,
			Bandwidth: int(rendition.Bandwidth),
		}},
	}, nil
}

func precomputedRenditionCompatible(r store.PlaybackRendition, profile CapabilityProfile, sourceVideo VideoSignal, sourceHasAudio bool, wantedAudio ffmpeg.AudioProfile, ov quirkOverlay) bool {
	if r.Kind != store.PlaybackRenditionKindHLS || r.Status != store.PlaybackRenditionReady {
		return false
	}
	if ov.vetoesRendition(r) {
		return false
	}
	if sourceVideo.FullTranscodeRefusal() != "" {
		return false
	}
	if sourceHasAudio && !precomputedAudioCompatible(wantedAudio) {
		return false
	}
	if sourceVideo.NeedsToneMap() {
		if _, err := os.Stat(filepath.Join(r.Path, toneMappedSDRMarker)); err != nil {
			return false
		}
	}
	// A rewrap purges an encrypted rendition's ciphertext while its Library is
	// Sealed, so its row can outlive the files: offer it only while they exist.
	if IsEncryptedPrecomputeRenditionID(r.ID) {
		if _, err := os.Stat(filepath.Join(r.Path, masterPlaylistName)); err != nil {
			return false
		}
	}
	if len(profile.VideoCodecs) > 0 {
		cap, ok := videoCodecCap(profile.VideoCodecs, r.Codec)
		if !ok {
			return false
		}
		return cap.MaxHeight <= 0 || r.Height <= cap.MaxHeight
	}
	if len(profile.DirectCodecs) > 0 && !containsFold(profile.DirectCodecs, r.Codec) {
		return false
	}
	ceiling := clientLadderCeiling(profile)
	return ceiling <= 0 || r.Height <= ceiling
}

func precomputedAudioCompatible(wanted ffmpeg.AudioProfile) bool {
	if wanted.Codec == "" {
		wanted.Codec = "aac"
	}
	if wanted.Channels <= 0 {
		wanted.Channels = 2
	}
	return normalizedAudioCodec(wanted.Codec) == "aac" && wanted.Channels <= 2
}

// clientLadderCeiling is the decode-height ceiling the advertised transcode
// ladder is clipped to (ADR-0010 §8: honest twice over — honest to the source
// and to the client). It is the smallest non-zero VideoCodecCap.MaxHeight the
// profile declares across its video codecs: the transcode may target any codec
// the client supports, so the ladder must stay within the tightest decode bound
// any of them imposes, never advertising a rung the client could not play on
// some path. 0 — no VideoCodecs envelope, or every declared codec leaves
// MaxHeight unset (no ceiling) — means "no client ceiling", so the ladder falls
// back to source-only rungs, byte-identical to the prior rungsFor behaviour.
func clientLadderCeiling(profile CapabilityProfile) int {
	ceiling := 0
	for _, c := range profile.VideoCodecs {
		if c.MaxHeight <= 0 {
			continue
		}
		if ceiling == 0 || c.MaxHeight < ceiling {
			ceiling = c.MaxHeight
		}
	}
	return ceiling
}

// ladderCaps resolves the transcode ladder's ceilings. The quirk overlay is
// applied HERE rather than laundered through the CapabilityProfile: a legacy
// profile with an empty VideoCodecs has nowhere to store a ceiling, ladderCaps
// carries only height, and rewriting the declared profile is what would invert
// the overlay (an emptied list reads as "not gated"). A denied video codec also
// has to leave the transcode TARGET, or the transcode re-emits exactly what the
// veto refused — HEVC has a grant channel (HLSHEVC) and is excluded here.
func (s *Service) ladderCaps(profile CapabilityProfile, ov quirkOverlay) ladderCaps {
	caps := ladderCaps{ClientMaxHeight: clientLadderCeiling(profile)}
	if s.sessions != nil && s.sessions.SupportsTranscodeCodec("hevc") && !ov.deniesVideoCodec("hevc") {
		if cap, ok := videoCodecCap(profile.VideoCodecs, "hevc"); ok {
			caps.HEVC = true
			caps.HEVCMaxHeight = cap.MaxHeight
		}
	}
	// A bitrate cap is exactly expressible as a height ceiling because the rung
	// vocabulary is closed and fixed, so no new streamtoken.Grant field is
	// needed: withHLSLadderGrant/LadderCapsFromGrant already round-trip both
	// heights and the byte layer reconstructs the identical rung set.
	// Each codec takes its own rung heights: an HEVC rung fits more picture
	// under the same cap than the H.264 rung of that height.
	caps.ClientMaxHeight = minNonZero(caps.ClientMaxHeight, ov.heightCeiling())
	caps.HEVCMaxHeight = minNonZero(caps.HEVCMaxHeight, ov.hevcHeightCeiling())
	return caps
}

// advertisedRungHeight reports the height the wire may claim for a rung. A
// ceiling-bounded rung on a source whose dimensions were never probed is an
// UPPER BOUND, not an observed output size, so advertising it would state a
// resolution the server never measured. RenditionInfo.Height is omitempty, so
// the field simply disappears — the honest "unknown" the dimensionless source
// rung already conveys. The rung keeps its ceiling for the encoder, which
// reconstructs the same rung list from LadderCapsFromGrant.
// advertisedRungs is the rung inventory a transcode of media offers. An
// audio-class Media offers the single audio-only rung (ADR-0010 §8, honest
// ladder): a picture stream it carries is embedded art, not video, so its
// dimensions must not seed a resolution ladder and the master must not
// advertise a video rate. The byte layer reaches the same rung through
// StreamShape.AudioOnly. audioOnlyPeak is that rung's BANDWIDTH
// (audioOnlyBandwidth).
func advertisedRungs(media store.Media, caps ladderCaps, audioOnlyPeak int) []Rung {
	if media.Class == store.MediaClassAudio {
		return []Rung{audioOnlyRung(audioOnlyPeak)}
	}
	return rungsForLadder(media.VideoWidth, media.VideoHeight, caps)
}

func advertisedRungHeight(media store.Media, rung Rung) int {
	if media.VideoHeight <= 0 {
		return 0
	}
	return rung.Height
}

// audioCopyChoice builds the HLS stream option for the audio-only-transcode
// case: the master advertises the single audio-copy rendition —
// video copied through, only audio transcoded — so there is no resolution
// ladder (a copied stream keeps the source size by construction).
func (s *Service) audioCopyChoice(ctx context.Context, grant streamtoken.Grant, suffix string, audio ffmpeg.AudioProfile, expiresAt time.Time) (StreamChoice, error) {
	grant.Target = streamtoken.TargetMediaHLS
	// Carry the audio-only-transcode verdict into the grant so the byte layer
	// seeds the audio-copy rung on the shared master.m3u8 fetch too, not only on
	// audio-copy/* artifact requests (PLAY-3b residual: the master URL is shared
	// with the standard transcode, so rung-name ordering can no longer decide it).
	grant.AudioCopy = true
	grant = withHLSAudioGrant(grant, audio)
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return StreamChoice{}, err
	}
	rung := audioCopyRung()
	return StreamChoice{
		ID:         "audio-copy-" + suffix,
		Kind:       "hls",
		URL:        "/api/media/hls/" + token + "/" + masterPlaylistName,
		Token:      token,
		ExpiresAt:  expiresAt.Format(time.RFC3339),
		Renditions: []RenditionInfo{{ID: rung.ID, Height: rung.Height, Bandwidth: rung.Bandwidth}},
	}, nil
}

// degradedHLSChoice builds the capacity-degraded HLS option (ADR-0010 §6 "drop
// to this quality"; PANEL-16): it mirrors hlsChoice but sets grant.Degraded so
// the byte layer creates a session capped to — and admitted over the cap for —
// the single lowest rung, and it advertises exactly that one rendition so the
// client parses an honest one-rung ladder (also honoring §8: one rendition per
// session). The lowest rung is honest twice over: at or below the source height
// and at or below the client decode ceiling.
func (s *Service) degradedHLSChoice(ctx context.Context, grant streamtoken.Grant, suffix string, media store.Media, profile CapabilityProfile, audio ffmpeg.AudioProfile, audioOnlyPeak int, expiresAt time.Time, ov quirkOverlay) (StreamChoice, error) {
	grant.Target = streamtoken.TargetMediaHLS
	grant.Degraded = true
	grant = withHLSAudioGrant(grant, audio)
	caps := s.ladderCaps(profile, ov)
	grant = withHLSLadderGrant(grant, caps)
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return StreamChoice{}, err
	}
	rung := lowestRung(advertisedRungs(media, caps, audioOnlyPeak))
	return StreamChoice{
		ID:         "hls-" + suffix,
		Kind:       "hls",
		URL:        "/api/media/hls/" + token + "/" + masterPlaylistName,
		Token:      token,
		ExpiresAt:  expiresAt.Format(time.RFC3339),
		Renditions: []RenditionInfo{{ID: rung.ID, Height: advertisedRungHeight(media, rung), Bandwidth: rung.Bandwidth}},
	}, nil
}

// degradedAudioCopyChoice is the capacity-degraded form of the audio-only
// transcode: the video is copied through and only
// the audio is transcoded — already the cheapest single-rung encode — admitted
// over the cap for a party member via grant.Degraded. It is audioCopyChoice with
// the degrade flag set.
func (s *Service) degradedAudioCopyChoice(ctx context.Context, grant streamtoken.Grant, suffix string, audio ffmpeg.AudioProfile, expiresAt time.Time) (StreamChoice, error) {
	grant.Target = streamtoken.TargetMediaHLS
	grant.AudioCopy = true
	grant.Degraded = true
	grant = withHLSAudioGrant(grant, audio)
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return StreamChoice{}, err
	}
	rung := audioCopyRung()
	return StreamChoice{
		ID:         "audio-copy-" + suffix,
		Kind:       "hls",
		URL:        "/api/media/hls/" + token + "/" + masterPlaylistName,
		Token:      token,
		ExpiresAt:  expiresAt.Format(time.RFC3339),
		Renditions: []RenditionInfo{{ID: rung.ID, Height: rung.Height, Bandwidth: rung.Bandwidth}},
	}, nil
}

// assetsInfo builds the Generated Asset advertisement for one decided item:
// nil when no locator is wired (the response field stays omitted, so the wire
// shape is unchanged for deployments without assets). audio marks an
// audio-class item, whose waveform is advertised even before it exists when the
// locator generates it on demand.
func (s *Service) assetsInfo(ctx context.Context, grant streamtoken.Grant, fingerprint string, audio bool, expiresAt time.Time) (*AssetsInfo, error) {
	if s.assets == nil || fingerprint == "" {
		return nil, nil
	}
	grant.Target = streamtoken.TargetMediaAssets
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return nil, err
	}
	base := "/api/media/assets/" + token + "/"
	info := &AssetsInfo{
		MarkerThumbnailBase: base,
		ExpiresAt:           expiresAt.Format(time.RFC3339),
	}
	if name, ok := s.assets.TrickplayAsset(fingerprint); ok {
		info.Trickplay = base + name
	}
	if name, ok := s.assets.PreviewClipAsset(fingerprint); ok {
		info.PreviewClip = base + name
	}
	// First-class optional audio Generated Assets (ADR-0098 area 2): advertised
	// only when the asset exists; a missing one omits its field.
	if name, ok := s.assets.WaveformAsset(fingerprint); ok {
		info.Waveform = base + name
	} else if onDemand, ok := s.assets.(WaveformOnDemandLocator); ok && audio {
		info.Waveform = base + onDemand.WaveformOnDemandAsset()
	}
	if name, ok := s.assets.BeatGridAsset(fingerprint); ok {
		info.BeatGrid = base + name
	}
	if name, ok := s.assets.SyncLanesAsset(fingerprint); ok {
		info.SyncLanes = base + name
	}
	if name, ok := s.assets.SilenceMapAsset(fingerprint); ok {
		info.SilenceMap = base + name
	}
	return info, nil
}

// loudnessInfo assembles the rung-specific loudness/normalization state for a
// decision (ADR-0098 area 3). It is the single merge point that holds both halves
// of the report: the rung verdict (dec.verdict) and the parsed measurement
// (dec.loudness), plus the assets token base from dec.assets for the loudness.json
// URL. State is derived from the DECLARED CapabilityProfile.NormalizationModes
// versus the measurement's Source — never from the client name (the ADR's
// explicit requirement). nil means no loudness block (non-audio, unavailable, or
// no measurement yet).
//
// The four honest states:
//   - direct-play verdict + a declared mode covering the measurement source →
//     direct-supported (the client applies GainDB itself; the rung is NOT
//     downgraded to normalize — ADR-0098's prohibition on forcing a worse rung).
//   - direct-play verdict + no covering declared mode → passthrough-unsupported
//     (honest: bytes pass through un-normalized; still no forced transcode).
//   - any transcode/precomputed verdict (chosen for OTHER reasons) → server-
//     normalized (the server applies the offset in the transcode it already runs).
//   - no measurement → unavailable (handled by the caller via dec.loudnessOK).
func (s *Service) loudnessInfo(dec decision, profile CapabilityProfile) *LoudnessInfo {
	if !dec.loudnessOK {
		return nil
	}
	info := &LoudnessInfo{
		IntegratedLUFS:  dec.loudness.IntegratedLUFS,
		LoudnessRangeLU: dec.loudness.LoudnessRangeLU,
		TruePeakDBTP:    dec.loudness.TruePeakDBTP,
		ReferenceLUFS:   dec.loudness.ReferenceLUFS,
		GainDB:          dec.loudness.GainDB,
		Source:          dec.loudness.Source,
	}
	// Asset URL: reuse the already-minted assets-target token base (the same base
	// the trickplay/preview/Marker routes use); the byte route re-verifies scope
	// and reveal on every fetch and the loudness.json name is in the closed asset
	// vocabulary. The locator resolves the wire name so playback never hardcodes
	// the genassets file vocabulary.
	if s.assets != nil && dec.assets != nil && dec.assets.MarkerThumbnailBase != "" {
		if name, ok := s.assets.LoudnessAsset(dec.loudnessFingerprint); ok {
			info.AssetURL = dec.assets.MarkerThumbnailBase + name
		}
	}
	info.State = loudnessState(dec.verdict, dec.loudness.Source, profile.NormalizationModes)
	return info
}

// loudnessState derives the rung-specific normalization state from the playback
// verdict, the measurement's Source provenance, and the client's DECLARED
// normalization modes — never the client name (ADR-0098 area 3). A direct-play
// verdict reports supported only when the client declared a mode covering the
// measurement source; otherwise it is an honest passthrough-unsupported (the
// server never forces a transcode/worse rung solely to normalize). Any other
// verdict is a transcode the server chose for other reasons, where it applies the
// offset itself → server-normalized.
func loudnessState(verdict, source string, declaredModes []string) string {
	if verdict == "direct-play" {
		if normalizationModeCovers(declaredModes, source) {
			return LoudnessStateDirectSupported
		}
		return LoudnessStatePassthroughUnsupported
	}
	return LoudnessStateServerNormalized
}

func (s *Service) loudnessMeasurement(media store.Media, file store.MediaFile) (LoudnessData, bool) {
	if media.Class != store.MediaClassAudio || file.ContentFingerprint == "" || s.assets == nil {
		return LoudnessData{}, false
	}
	return s.assets.LoudnessForFingerprint(media.LibraryID, file.ContentFingerprint)
}

// hlsAudioProfile picks the transcode's audio target. The overlay constrains
// BOTH the passthrough branch and the encoder target, or a denied audio codec
// is simply re-emitted by the transcode: bitstream passthrough would hand the
// device the very codec or channel layout the quirk denies, and the E-AC-3
// target would re-select a denied E-AC-3. AAC is the last-resort target with no
// fallback below it, which is why the parser rejects an entry denying it.
// chosen is the absolute index of the audio stream the transcode maps;
// passthrough is decided on that stream alone.
func hlsAudioProfile(profile CapabilityProfile, streams []store.MediaFileStream, chosen int, loudness LoudnessData, loudnessOK bool, ov quirkOverlay) ffmpeg.AudioProfile {
	if passthroughAudioDeclared(streams, chosen, profile) && !ov.vetoesAudioPassthrough(streams) && ov.MaxAudioChannels <= 0 {
		return ffmpeg.AudioProfile{Codec: "copy"}
	}
	audio := ffmpeg.AudioProfile{Codec: "aac", Channels: 2}
	if hlsAudioSupportsEAC3(profile) && !ov.deniesAudioCodec("eac3") {
		audio.Codec = "eac3"
		audio.Channels = 6
	}
	if ov.MaxAudioChannels > 0 && audio.Channels > ov.MaxAudioChannels {
		audio.Channels = ov.MaxAudioChannels
	}
	if loudnessOK {
		gain := loudness.GainDB
		audio.GainDB = &gain
	}
	return audio
}

// ceilingBindsAudio reports whether a video transcode runs on a ceiling-bound
// ladder rung, whose 256 kbit/s audio reserve holds only stereo AAC.
// Audio-only items keep their audio: the ceiling never applies to them.
func (ov quirkOverlay) ceilingBindsAudio(media store.Media, file store.MediaFile, streams []store.MediaFileStream) bool {
	return ov.streamCeiling() > 0 && !audioOnlyMedia(media) && ov.vetoesVideoPassthrough(media, file, streams)
}

// ceilingBoundAudio holds a re-encoded stream's audio to stereo AAC under the
// viewer's streaming ceiling. Each ladder rung reserves 256 kbit/s for audio
// (bandwidthFor); passthrough or 5.1 E-AC-3 can exceed that on its own, which
// would push the stream past the ceiling the viewer chose. Stereo AAC at the
// encoder's default rate fits the reserve.
func ceilingBoundAudio(audio ffmpeg.AudioProfile) ffmpeg.AudioProfile {
	audio.Codec = "aac"
	if audio.Channels <= 0 || audio.Channels > 2 {
		audio.Channels = 2
	}
	return audio
}

func hlsAudioSupportsEAC3(profile CapabilityProfile) bool {
	if strings.EqualFold(profile.DownstreamAudio, "stereo") {
		return false
	}
	for _, cap := range profile.AudioCodecs {
		codec := strings.ToLower(strings.ReplaceAll(cap.Codec, "-", ""))
		if codec == "eac3" || codec == "eac" || codec == "ec3" {
			return cap.MaxChannels == 0 || cap.MaxChannels >= 6
		}
	}
	return false
}

func hasAudioStream(streams []store.MediaFileStream) bool {
	for _, stream := range streams {
		if stream.Type == "audio" {
			return true
		}
	}
	return false
}

// passthroughAudioDeclared reports whether the client declared passthrough for
// the chosen audio stream (an absolute stream index) within its ceilings. Only
// the chosen track counts: the transcode maps that track alone, so another
// track the client could pass through must not authorise copying it.
func passthroughAudioDeclared(streams []store.MediaFileStream, chosen int, profile CapabilityProfile) bool {
	source, ok := audioStreamAt(streams, chosen)
	return ok && passthroughCovers(profile, source)
}

func withHLSAudioGrant(grant streamtoken.Grant, audio ffmpeg.AudioProfile) streamtoken.Grant {
	if audio.Codec != "" && audio.Codec != "aac" {
		grant.HLSAudioCodec = audio.Codec
	}
	if audio.Channels > 0 && audio.Channels != 2 {
		grant.HLSAudioChannels = audio.Channels
	}
	grant.HLSAudioGainDB = audio.GainDB
	return grant
}

func withHLSLadderGrant(grant streamtoken.Grant, caps ladderCaps) streamtoken.Grant {
	grant.HLSClientMaxHeight = caps.ClientMaxHeight
	grant.HLSHEVC = caps.HEVC
	grant.HLSHEVCMaxHeight = caps.HEVCMaxHeight
	return grant
}

func LadderCapsFromGrant(grant streamtoken.Grant) ladderCaps {
	return ladderCaps{
		ClientMaxHeight: grant.HLSClientMaxHeight,
		HEVC:            grant.HLSHEVC,
		HEVCMaxHeight:   grant.HLSHEVCMaxHeight,
	}
}

func HLSAudioProfileFromGrant(grant streamtoken.Grant) ffmpeg.AudioProfile {
	return ffmpeg.AudioProfile{
		Codec:    grant.HLSAudioCodec,
		Channels: grant.HLSAudioChannels,
		GainDB:   grant.HLSAudioGainDB,
	}
}

// normalizationModeCovers reports whether the client's declared modes include one
// that can apply a measurement of the given Source. A "tags"-sourced measurement
// (a precomputed ReplayGain offset) is covered by the "replaygain" mode; an
// "r128"-sourced (analysis) measurement by the "r128" mode. The match is on the
// declared CAPABILITY, never the client identity.
func normalizationModeCovers(declaredModes []string, source string) bool {
	want := NormalizationModeR128
	if source == LoudnessDataSourceTags {
		want = NormalizationModeReplayGain
	}
	return slices.Contains(declaredModes, want)
}

// isMuxedTextSubtitleCodec reports whether an ffprobe subtitle codec_name is a
// text format the /api/media/subtitle route can extract to WebVTT (ADR-0010
// §5). Bitmap codecs (PGS/VobSub/DVB) are deliberately absent: they keep the
// burn-in / withhold routing and never carry a sidecar URL.
// IsMuxedTextSubtitleCodec reports whether a muxed subtitle codec converts to a
// WebVTT sidecar (text, not bitmap). The subtitle byte route uses it to pick
// every text track of a file for one extraction pass (CORE-2315).
func IsMuxedTextSubtitleCodec(codec string) bool {
	return isMuxedTextSubtitleCodec(codec)
}

func isMuxedTextSubtitleCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "subrip", "srt", "ass", "ssa", "webvtt", "mov_text", "text":
		return true
	}
	return false
}

// muxedCaptionTracksInfo lists the subtitle streams muxed into the chosen file.
// Every text track additionally carries a Stream-Token'd WebVTT URL under the
// media-subtitle target (one token per track, sharing the playback grant's
// scope and expiry), mirroring the sidecar advertisement: a client on a
// transcoded rung cannot read the muxed track from the HLS output, so the URL
// is the only delivery path for it. The token is revoked with the session on
// every fetch exactly like the other byte targets.
func (s *Service) muxedCaptionTracksInfo(ctx context.Context, grant streamtoken.Grant, fileFingerprint string, streams []store.MediaFileStream, suffix string) ([]CaptionTrack, error) {
	var tracks []CaptionTrack
	for _, stream := range streams {
		if stream.Type != "subtitle" {
			continue
		}
		index := stream.Index
		tracks = append(tracks, CaptionTrack{
			ID:             muxedCaptionID(suffix, stream.Index),
			Format:         stream.Codec,
			StreamIndex:    &index,
			Language:       stream.Language,
			Forced:         stream.Forced,
			SDH:            stream.HearingImpaired,
			VisualImpaired: stream.VisualImpaired,
			Commentary:     stream.Commentary,
		})
	}
	for i := range tracks {
		if !isMuxedTextSubtitleCodec(tracks[i].Format) {
			continue
		}
		if fileFingerprint == "" {
			return nil, errors.New("muxed subtitle file fingerprint is required")
		}
		grant.Target = streamtoken.TargetMediaSubtitle
		grant.SubtitleStreamIndex = *tracks[i].StreamIndex + 1
		grant.SubtitleFileFingerprint = fileFingerprint
		token, err := s.tokens.Issue(ctx, grant)
		if err != nil {
			return nil, err
		}
		tracks[i].URL = fmt.Sprintf("/api/media/subtitle/%s/%d.vtt", token, *tracks[i].StreamIndex)
	}
	return tracks, nil
}

// captionTracksInfo builds the sidecar caption track advertisement for one
// decided item: one CaptionTrack per recorded CAPTION
// sidecar, each with a Stream-Token'd WebVTT URL under the media-sidecar target
// and the language / forced / SDH hints. The URL index is the sidecar's
// position in the store's relative-path-ordered FULL list (captions and any
// interactive sidecars alike), which the byte route resolves back to the same
// row — so an interactive sidecar in the list does not shift a caption's index.
// Interactive sidecars are intentionally NOT surfaced in this
// caption menu: they are served behind the same Stream Token but their rich UI
// is a sideload/Mac surface, not the generic caption track menu. Returns nil
// (the field stays omitted) when the file has no caption sidecars.
// It also reports how many caption sidecars were WITHHELD because this client
// cannot render their format. Advertising a track the client
// cannot draw is a phantom: the menu offers it, the pre-selection may stamp it,
// and the viewer sees "subtitles on" with nothing on screen. The count feeds the
// resolver's unavailable-reason notice so the absence is stated, not silent.
func (s *Service) captionTracksInfo(ctx context.Context, grant streamtoken.Grant, fingerprint, suffix string, expiresAt time.Time, subtitleFormats []string) ([]CaptionTrack, int, error) {
	if fingerprint == "" {
		return nil, 0, nil
	}
	sidecars, err := s.store.MediaFileSidecarCaptions(ctx, fingerprint)
	if err != nil {
		return nil, 0, err
	}
	if len(sidecars) == 0 {
		return nil, 0, nil
	}
	grant.Target = streamtoken.TargetMediaSidecar
	grant.SidecarRawASS = profileRendersASS(subtitleFormats)
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return nil, 0, err
	}
	base := "/api/media/sidecar/" + token + "/"
	var tracks []CaptionTrack
	unrenderable := 0
	for i, sc := range sidecars {
		if sc.Kind == store.SidecarKindInteractive {
			continue
		}
		if !sidecarRenderable(sc.Format, subtitleFormats) {
			unrenderable++
			continue
		}
		tracks = append(tracks, CaptionTrack{
			ID:       sidecarCaptionID(suffix, i),
			URL:      fmt.Sprintf("%s%d.%s", base, i, sidecarDeliveryExt(sc.Format, grant.SidecarRawASS)),
			Format:   sc.Format,
			Language: sc.Language,
			Forced:   sc.Forced,
			SDH:      sc.SDH,
		})
	}
	return tracks, unrenderable, nil
}

// sidecarRenderable reports whether a client declaring these (overlay-filtered)
// subtitle formats can actually draw a sidecar of this format. A TEXT sidecar is
// projected to WebVTT by the byte route, so every client renders it. A BITMAP
// sidecar (ADR-0194 §5 Tier 2) is served as raw bytes and needs a native
// renderer for its family — the same rule a muxed bitmap stream already obeys.
// The difference is that a muxed bitmap stream a client cannot render still has
// somewhere to go (burn-in re-encodes it into the video), while a bitmap SIDECAR
// has no such ladder: withheld or phantom are the only two options.
func sidecarRenderable(format string, subtitleFormats []string) bool {
	family, isBitmap := bitmapSidecarFamily(format)
	if !isBitmap {
		return true
	}
	return declaresSubtitleFormat(subtitleFormats, family)
}

// bitmapSidecarFamily maps a bitmap sidecar format onto the capability-profile
// vocabulary word for the renderer family it needs. A text format reports false.
func bitmapSidecarFamily(format string) (string, bool) {
	switch strings.ToLower(format) {
	case "sup":
		return "pgs", true
	case "idx", "sub":
		return "vobsub", true
	}
	return "", false
}

// declaresSubtitleFormat reports whether an overlay-filtered format list carries
// a format, case-insensitively.
func declaresSubtitleFormat(subtitleFormats []string, want string) bool {
	for _, f := range subtitleFormats {
		if strings.EqualFold(f, want) {
			return true
		}
	}
	return false
}

// sidecarDeliveryExt picks the byte-route extension a sidecar caption URL is
// minted with. An .ass/.ssa sidecar served to a
// declared-`ass` (libass-capable) profile takes the raw-ASS passthrough route
// (".ass", byte-identical authored styling); every other case takes the
// universal flattened-WebVTT route (".vtt").
func sidecarDeliveryExt(format string, libassCapable bool) string {
	if libassCapable && (format == "ass" || format == "ssa") {
		return "ass"
	}
	return "vtt"
}

// profileRendersASS reports whether the effective (quirk-overlay-filtered)
// subtitle formats a client declared include the ASS family — i.e. the client
// carries a libass engine and can render authored ASS directly rather than a
// flattened WebVTT projection.
func profileRendersASS(subtitleFormats []string) bool {
	return declaresSubtitleFormat(subtitleFormats, "ass")
}

// activeProfileTranscodes counts the Profile's running HLS transcode sessions
// — the v1 definition of an active stream (direct-play byte serving is
// stateless and untracked). The requesting session's own transcode of the
// same Media is excluded: it would be reused, not forked.
func (s *Service) activeProfileTranscodes(ctx context.Context, scope auth.Scope, mediaID store.MediaID) int {
	if s.sessions == nil {
		return 0
	}
	keys := s.sessions.ActiveKeys()
	if len(keys) == 0 {
		return 0
	}
	profileSessions, err := s.profileSessions.MyProfileSessions(ctx, scope.ProfileID)
	if err != nil {
		return 0 // an unresolvable snapshot never counts against the cap
	}
	profileSessionIDs := make(map[store.SessionID]struct{}, len(profileSessions))
	for _, session := range profileSessions {
		profileSessionIDs[session.ID] = struct{}{}
	}
	active := 0
	for _, key := range keys {
		if key.SessionID == scope.SessionID && key.MediaID == mediaID {
			continue
		}
		if _, ok := profileSessionIDs[key.SessionID]; ok {
			active++
		}
	}
	return active
}

// trackPreferences maps the stored Profile policy onto the wire shape; lists
// serialize as [] (never null) so clients can index them defensively.
func trackPreferences(policy store.Profile) TrackPreferences {
	audioLanguages := policy.AudioLanguages
	if audioLanguages == nil {
		audioLanguages = []string{}
	}
	subtitleLanguages := policy.SubtitleLanguages
	if subtitleLanguages == nil {
		subtitleLanguages = []string{}
	}
	return TrackPreferences{
		AudioLanguages:    audioLanguages,
		SubtitleLanguages: subtitleLanguages,
		SubtitleMode:      string(policy.SubtitleMode),
		PreferSDH:         policy.PreferSDH,
	}
}

// canDirectPlay decides whether the client can play the source file as-is. The
// container always gates. Video and audio are judged against the richer
// envelope when the profile declares one, falling back to the
// legacy DirectCodecs membership when it does not — so a client that sends only
// {Containers, DirectCodecs} is judged exactly as before.
func canDirectPlay(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) bool {
	if !slices.Contains(profile.Containers, media.Container) {
		return false
	}
	if ov.vetoesVideoPassthrough(media, file, streams) ||
		ov.vetoesAudioPassthrough(streams) ||
		ov.vetoesSourceContainer() {
		return false
	}
	videoOK := false
	if audioOnlyMedia(media) {
		videoOK = audioOnlyCodecDeclared(media, streams, profile)
	} else {
		videoOK = videoDirectPlayable(media, file, streams, profile)
	}
	return videoOK &&
		audioDirectPlayable(streams, profile) &&
		subtitleFormatsCompatible(streams, profile, selectedSubtitleIndex, ov)
}

// aacEncoderDelaySamples is the priming FFmpeg's native AAC encoder puts
// ahead of the first input sample, and aacFrameSamples its frame length.
const (
	aacEncoderDelaySamples = 1024
	aacFrameSamples        = 1024
)

// aacSampleRates are the sampling rates the AAC encoder takes as is. The
// segmenting encode does not resample, so its output runs at the source's
// rate, and the gapless arithmetic holds only for these.
var aacSampleRates = map[int]bool{
	96000: true, 88200: true, 64000: true, 48000: true, 44100: true, 32000: true,
	24000: true, 22050: true, 16000: true, 12000: true, 11025: true, 8000: true, 7350: true,
}

// stampGapless sets each stream choice's gapless trim from the source's
// first audio stream (CORE-1626). A direct-play choice serves the source's
// own bytes, so it carries what the source declares. A live HLS transcode of
// an audio-only source re-encodes to AAC from the source's first sample
// (ffmpeg.Rendition.KeepPriming): its decoded output is the encoder's own
// priming, the source's decoded content, and padding out to a whole frame,
// all at the source's rate. That holds only for a measured content length,
// an AAC target, a rate the encoder takes without resampling, and an encode
// from the start: a resume position starts the encoder mid-item, where its
// output is a different length. Any other transcode carries no trim rather
// than a wrong one.
func stampGapless(choices []StreamChoice, streams []store.MediaFileStream, hlsAudio ffmpeg.AudioProfile, resumeSeconds int) {
	source, ok := firstAudioStream(streams)
	if !ok || source.SampleRate <= 0 {
		return
	}
	_, hasVideo := primaryVideoStream(streams)
	for i := range choices {
		choice := &choices[i]
		switch {
		case choice.Kind == "direct-play":
			if source.EncoderDelaySamples > 0 || source.EncoderPaddingSamples > 0 {
				choice.Gapless = &GaplessInfo{SampleRate: source.SampleRate, EncoderDelaySamples: source.EncoderDelaySamples, EncoderPaddingSamples: source.EncoderPaddingSamples}
			}
		case choice.Kind == "hls" && strings.HasPrefix(choice.ID, "hls-"):
			codec := normalizedAudioCodec(hlsAudio.Codec)
			if hasVideo || resumeSeconds > 0 || source.DecodedSamples <= 0 || (codec != "aac" && codec != "") || !aacSampleRates[source.SampleRate] {
				continue
			}
			choice.Gapless = &GaplessInfo{SampleRate: source.SampleRate, EncoderDelaySamples: aacEncoderDelaySamples, EncoderPaddingSamples: aacPaddingSamples(source.DecodedSamples)}
		}
	}
}

// aacPaddingSamples is the end padding of an AAC encode of decoded content
// samples: the encoder emits whole frames covering its priming and the
// content, so the padding fills the last frame.
func aacPaddingSamples(decoded int64) int {
	covered := aacEncoderDelaySamples + decoded
	frames := (covered + aacFrameSamples - 1) / aacFrameSamples
	return int(frames*aacFrameSamples - covered)
}

// firstAudioStream is the source's first audio stream: the one a direct play
// decodes by default and a live transcode maps.
func firstAudioStream(streams []store.MediaFileStream) (store.MediaFileStream, bool) {
	for _, stream := range streams {
		if stream.Type == "audio" {
			return stream, true
		}
	}
	return store.MediaFileStream{}, false
}

// audioOnlyMedia reports an audio-class item. Its Media.Codec is the audio
// codec (scan sets it from the probe), so the video envelope has nothing to
// judge: without this exemption a client declaring videoCodecs could never
// direct-play a track (CORE-1143). A picture stream an audio item carries is
// embedded cover art, not video (CORE-1542, CORE-1848), so it changes nothing
// here, as advertisedRungs and StreamShape.AudioOnly already treat it.
func audioOnlyMedia(media store.Media) bool {
	return media.Class == store.MediaClassAudio
}

// audioOnlyCodecDeclared stands in for the video gate on audio-only items.
// Without a declared audio envelope audioDirectPlayable gates nothing, so the
// legacy DirectCodecs list judges Media.Codec, as the video gate did before.
// With one, recorded audio streams are judged by audioDirectPlayable and a
// probe-less file falls back to Media.Codec against the envelope.
func audioOnlyCodecDeclared(media store.Media, streams []store.MediaFileStream, profile CapabilityProfile) bool {
	if len(profile.AudioCodecs) == 0 {
		return slices.Contains(profile.DirectCodecs, media.Codec) ||
			(audioCodecFamily(media.Codec) == "pcm" && slices.Contains(profile.DirectCodecs, "pcm"))
	}
	for _, stream := range streams {
		if stream.Type == "audio" {
			return true
		}
	}
	_, ok := audioCodecCap(profile.AudioCodecs, media.Codec)
	return ok
}

// audioCopyEligible reports whether the audio-only-transcode mode applies:
// the container and video stream are direct-playable but at
// least one audio stream is not (an unsupported codec like TrueHD/DTS, or a
// channel count over the profile's ceiling). The video is copied through and
// only the audio is transcoded — the single biggest transcode-cost win. The
// audio envelope must be declared for this to fire; without it audio is never
// gated, so canDirectPlay already accepted the file (legacy behaviour).
// The quirk overlay reaches this predicate from two sides. A video-side veto
// kills the rung outright (the video cannot be copied); an audio-only deny, or a
// force-remux, SELECTS it — the cheap correct answer, since the device gets a
// new container with the video passed through untouched. vetoesSourceContainer
// is deliberately not consulted: this branch already leaves the source
// container.
//
// A source container the profile does not declare (an mkv for a browser that plays only
// mp4/webm) is the plain remux case of ADR-0010 §2: the streams are playable, only the
// container is not, so the video is copied into the HLS container instead of re-encoded. The
// HDR-preservation rule applies to that swap, so a source without a recorded video stream, or
// with HDR10+/Dolby Vision metadata, still falls through to the full transcode.
//
// An audio-class item never takes this rung: its only picture stream is
// embedded art, which is not video to copy through. Its transcode is the
// audio-only rendition (advertisedRungs).
func audioCopyEligible(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) bool {
	if media.Class == store.MediaClassAudio {
		return false
	}
	containerDeclared := slices.Contains(profile.Containers, media.Container)
	return videoCopyEligible(media, file, streams, profile, selectedSubtitleIndex, ov) &&
		(!containerDeclared || audioNeedsRework(streams, profile, ov))
}

// videoCopyEligible reports whether the audio-copy rung may copy this source's
// video: the container is declared or the remux preserves HDR, no overlay
// vetoes video passthrough, the video direct-plays, and the selected subtitle
// needs no burn-in. audioCopyEligible adds that the rung has audio work to do;
// a non-default audio track pick supplies that work on its own.
func videoCopyEligible(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) bool {
	containerDeclared := slices.Contains(profile.Containers, media.Container)
	if !containerDeclared && !remuxPreservesHDR(streams) {
		return false
	}
	if ov.vetoesVideoPassthrough(media, file, streams) {
		return false
	}
	return videoDirectPlayable(media, file, streams, profile) &&
		subtitleFormatsCompatible(streams, profile, selectedSubtitleIndex, ov)
}

// audioNeedsRework reports whether the audio-copy rung has work to do: the
// profile cannot take the source audio, the overlay vetoes audio passthrough,
// or a forced remux demands a new container.
//
// The forceRemux arm obeys ADR-0010 §2's HDR-preservation rule rather than
// bypassing it: a remux that would drop HDR10+ dynamic metadata or Dolby Vision
// RPU is not eligible, so on those sources the decision falls through to the
// full transcode instead. The quirk still gets what it asked for — the device
// never receives the source container — by the one route that preserves the
// picture. (A Dolby Vision profile 5 source is narrower still: ADR-0017 §4 has
// no software transcode path for it, so the refusal fires before the transcode
// rung and the verdict stays unavailable.)
func audioNeedsRework(streams []store.MediaFileStream, profile CapabilityProfile, ov quirkOverlay) bool {
	return !audioDirectPlayable(streams, profile) ||
		ov.vetoesAudioPassthrough(streams) ||
		(ov.ForceRemux && remuxPreservesHDR(streams))
}

// bitrateCeilingForced reports whether the viewer's streaming ceiling is the
// one thing that moved this item off a passthrough rung: under the overlay
// without the ceiling the source would direct play, play its Dolby Vision
// base layer, or take the video-copy rung. It is only asked once the ceilinged
// overlay has already refused those rungs.
func bitrateCeilingForced(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) bool {
	return bitrateCeilingRefusedDirectPlay(media, file, streams, profile, selectedSubtitleIndex, ov) ||
		(ov.streamCeiling() > 0 && audioCopyEligible(media, file, streams, profile, selectedSubtitleIndex, ov.withoutClientCeiling()))
}

// bitrateCeilingRefusedDirectPlay reports whether, without the viewer's
// streaming ceiling, the source would direct play or play its Dolby Vision
// base layer.
func bitrateCeilingRefusedDirectPlay(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) bool {
	if ov.streamCeiling() <= 0 {
		return false
	}
	base := ov.withoutClientCeiling()
	return canDirectPlay(media, file, streams, profile, selectedSubtitleIndex, base) ||
		dolbyVisionP7BaseLayer(media, file, streams, profile, selectedSubtitleIndex, base)
}

// audioCopyReason names the audio-copy rung honestly. When the rung was
// selected SOLELY by forceRemux the audio was fine — and may even be passed
// through as copy — so reporting "audio-transcode-only" would claim an audio
// transcode that is not happening. This is the chunk's one reason-vocabulary
// addition, and it is emitted only on a path that cannot exist without a quirk
// entry.
func audioCopyReason(streams []store.MediaFileStream, profile CapabilityProfile, ov quirkOverlay) string {
	if audioDirectPlayable(streams, profile) && !ov.vetoesAudioPassthrough(streams) {
		return "container-remux"
	}
	return "audio-transcode-only"
}

// videoDirectPlayable judges the source video codec against the profile. With a
// VideoCodecs envelope it enforces codec, profile, resolution ceiling, and HDR
// support against the primary video stream; without one it falls back to the
// flat DirectCodecs membership. MaxBitrate is enforced from file size and media
// duration when both are known; if either is absent, bitrate remains unknown and
// does not reject a legacy/probe-less file.
func videoDirectPlayable(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile) bool {
	return videoPlayable(media, file, streams, profile, false)
}

// videoPlayable is videoDirectPlayable with an explicit toggle for the Dolby
// Vision HDR sub-gate. asBaseLayer=true excuses only the DV-HDR gate — used by the
// P7 base-layer honesty path, where the HDR10-compatible base layer is already
// proven renderable (dolbyVisionP7BaseLayer checks hdr10BaseLayerRenderable first).
// Every other gate (codec, profile, resolution, bitrate, level, and non-DV HDR)
// still applies. The public videoDirectPlayable passes false, so canDirectPlay and
// audioCopyEligible are unchanged: DV never leaks into the audio-copy path.
func videoPlayable(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, asBaseLayer bool) bool {
	// An audio-class item has no picture to judge: against a per-codec envelope its audio
	// streams (audioDirectPlayable) are the whole codec question, and the media's audio codec
	// must not be looked up among the video codecs.
	if media.Class == store.MediaClassAudio && len(profile.VideoCodecs) > 0 {
		return true
	}
	if len(profile.VideoCodecs) == 0 {
		return slices.Contains(profile.DirectCodecs, media.Codec)
	}
	video, ok := primaryVideoStream(streams)
	codecCap, capOK := videoCodecCap(profile.VideoCodecs, media.Codec)
	if !capOK {
		return false
	}
	if codecCap.MaxHeight > 0 && media.VideoHeight > codecCap.MaxHeight {
		return false
	}
	if bitrate := estimatedSourceBitrate(media, file); codecCap.MaxBitrate > 0 && bitrate > codecCap.MaxBitrate {
		return false
	}
	// Profile/HDR gates read the stream inventory; a probe-less file (no video
	// stream recorded) cannot be checked against them, so it passes those
	// sub-gates on the codec+resolution evidence alone (the legacy behaviour for
	// files scanned before PLAY-1 captured streams).
	if ok {
		if len(codecCap.Profiles) > 0 && video.Profile != "" &&
			!containsFold(codecCap.Profiles, video.Profile) {
			return false
		}
		if codecCap.MaxLevel > 0 && video.Level > 0 && video.Level > codecCap.MaxLevel {
			return false
		}
		if format := hdrFormat(video); len(profile.HDRFormats) > 0 && format != "" &&
			!(asBaseLayer && isDolbyVisionFormat(format)) &&
			!slices.Contains(profile.HDRFormats, format) {
			return false
		}
	}
	return true
}

// isDolbyVisionFormat reports whether an hdrFormat token is a Dolby Vision token.
func isDolbyVisionFormat(format string) bool {
	return strings.HasPrefix(format, hdrDolbyVisionGeneric)
}

// directPlayEligible is the shared predicate Version negotiation and the final
// decide() verdict agree on: a Version direct-plays if the client can play it
// as-is (canDirectPlay) OR it is a P7 stream whose HDR10-compatible base layer is
// playable (dolbyVisionP7BaseLayer). Keeping both selection sites on this one
// predicate stops a P7-base-layer-playable Version being skipped in favour of a
// transcoding default.
func directPlayEligible(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) bool {
	return canDirectPlay(media, file, streams, profile, selectedSubtitleIndex, ov) ||
		dolbyVisionP7BaseLayer(media, file, streams, profile, selectedSubtitleIndex, ov)
}

// hdr10BaseLayerRenderable reports whether the profile can render a Dolby Vision
// P7 stream's HDR10 base layer: either it declares no HDR envelope (legacy: HDR is
// not gated) or it advertises "hdr10".
func hdr10BaseLayerRenderable(profile CapabilityProfile) bool {
	return len(profile.HDRFormats) == 0 || slices.Contains(profile.HDRFormats, "hdr10")
}

// dolbyVisionP7BaseLayer reports whether a Dolby Vision P7 file (HDR10-compatible
// base, BLCompatID 1 or 6) can direct-play its base layer while dropping the undecodable
// enhancement layer. No CapabilityProfile ever advertises
// "dolby-vision-p7", so this is the only path a P7 stream direct-plays. It fires
// only when the container gates, the profile can render the HDR10 base layer, the
// base-layer video is otherwise playable (codec/resolution/bitrate/level, DV-HDR
// gate excused), AND audio+subtitle direct-play — so a P7 file with unsupported
// audio is NOT eligible and falls through to the normal transcode/tone-map ladder,
// keeping the P7 exception out of audioCopyEligible/direct-stream.
func dolbyVisionP7BaseLayer(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) bool {
	if !slices.Contains(profile.Containers, media.Container) {
		return false
	}
	video, ok := primaryVideoStream(streams)
	if !ok || hdrFormat(video) != hdrDolbyVisionP7 {
		return false
	}
	if !hdr10BaseLayerRenderable(profile) {
		return false
	}
	// A quirked P7 device must not sneak in via the base-layer honesty path.
	if ov.vetoesVideoPassthrough(media, file, streams) ||
		ov.vetoesAudioPassthrough(streams) ||
		ov.vetoesSourceContainer() {
		return false
	}
	return videoPlayable(media, file, streams, profile, true) &&
		audioDirectPlayable(streams, profile) &&
		subtitleFormatsCompatible(streams, profile, selectedSubtitleIndex, ov)
}

// audioDirectPlayable gates every audio stream against the profile's audio
// envelope. With no AudioCodecs declared the audio path is not gated (legacy
// behaviour). An audio codec the profile omits — or one over its channel
// ceiling / downstream sink — forces transcode.
func audioDirectPlayable(streams []store.MediaFileStream, profile CapabilityProfile) bool {
	if len(profile.AudioCodecs) == 0 {
		return true
	}
	for _, stream := range streams {
		if stream.Type != "audio" {
			continue
		}
		codecCap, ok := audioCodecCapForStream(profile.AudioCodecs, stream)
		if !ok {
			return false
		}
		if maxChannels := effectiveAudioChannelLimit(codecCap, profile.DownstreamAudio); maxChannels > 0 && stream.Channels > maxChannels {
			return false
		}
		if exceedsAudioResolution(codecCap, stream) {
			return false
		}
	}
	return true
}

// exceedsAudioResolution reports whether a source audio stream's recorded
// sample rate or bit depth is over the codec envelope's hi-res ceiling
// (CORE-1627). An unrecorded value (0) or an unset ceiling never exceeds.
func exceedsAudioResolution(codecCap AudioCodecCap, stream store.MediaFileStream) bool {
	return (codecCap.MaxSampleRate > 0 && stream.SampleRate > codecCap.MaxSampleRate) ||
		(codecCap.MaxBitDepth > 0 && stream.BitDepth > codecCap.MaxBitDepth)
}

func audioCodecCapForStream(caps []AudioCodecCap, stream store.MediaFileStream) (AudioCodecCap, bool) {
	switch normalizedAudioCodec(stream.Codec) {
	case "truehd":
		return audioCodecCap(caps, "truehd")
	case "dts", "dca":
		if dtsHDMasterAudioProfile(stream.Profile) {
			return audioCodecCap(caps, "dts-hd-ma")
		}
	}
	return audioCodecCap(caps, stream.Codec)
}

func passthroughAudioCodecCap(caps []AudioCodecCap, stream store.MediaFileStream) (AudioCodecCap, bool) {
	switch normalizedAudioCodec(stream.Codec) {
	case "truehd":
		return audioCodecCap(caps, "truehd")
	case "dts", "dca":
		if dtsHDMasterAudioProfile(stream.Profile) {
			return audioCodecCap(caps, "dts-hd-ma")
		}
	}
	return AudioCodecCap{}, false
}

func effectiveAudioChannelLimit(codecCap AudioCodecCap, downstreamAudio string) int {
	maxChannels := codecCap.MaxChannels
	switch strings.ToLower(downstreamAudio) {
	case "stereo":
		if maxChannels == 0 || maxChannels > 2 {
			return 2
		}
	case "passthrough", "":
	}
	return maxChannels
}

// Dolby Vision HDR tokens (closed per-profile vocabulary, ADR-0017 §4). Match on
// probed DoViProfile + DoViBLCompatID. Two compatibility IDs carry an HDR10
// base layer: 1 (HDR10, the streaming profile 8.1 that WEB-DL sources ship) and
// 6 (the Ultra HD Blu-ray HDR10 variant profile 7 uses). 2 (SDR base, 8.2) and
// 4 (HLG base, 8.4) do not. hdrDolbyVisionP5/P8 are the advertisable tokens the Android capability
// profile / device-quirks table declare verbatim; hdrDolbyVisionP7 is emitted but
// never advertised (its FEL is undecodable on every client — the base-layer
// honesty path in dolbyVisionP7BaseLayer). hdrDolbyVisionGeneric is the sentinel
// for any DV shape without an HDR10-compatible base: no profile advertises it, so
// it never false-direct-plays as HDR10 and takes the transcode/tone-map path.
const (
	hdrDolbyVisionP5      = "dolby-vision-p5"
	hdrDolbyVisionP7      = "dolby-vision-p7"
	hdrDolbyVisionP8      = "dolby-vision-p8"
	hdrDolbyVisionGeneric = "dolby-vision"
	doViBLCompatHDR10     = 1 // HDR10 base layer (profile 8.1)
	doViBLCompatBluRay    = 6 // Ultra HD Blu-ray HDR10 base layer
)

// doViHDR10BaseLayer reports whether a Dolby Vision base-layer compatibility ID
// names an HDR10-compatible base layer (ADR-0017 §4: P8.1 direct-plays).
func doViHDR10BaseLayer(blCompatID int) bool {
	return blCompatID == doViBLCompatHDR10 || blCompatID == doViBLCompatBluRay
}

// dolbyVisionP7BaseLayerReason is the honest playbackInfo reason for a P7 stream
// that direct-plays its HDR10-compatible base layer while dropping the undecodable
// enhancement layer (ADR-0017 §4: "P7 plays base-layer-only and playbackInfo says so").
const dolbyVisionP7BaseLayerReason = "dolby-vision-p7-base-layer"

// hdrFormat classifies a video stream's HDR format from its color/DoVi fields,
// matching the HDRFormats vocabulary. "" means SDR (no HDR gate applies). A DV
// stream ALWAYS returns a DV-family token (never bare "hdr10"), so a profile that
// renders HDR10 cannot false-direct-play a DV base that is not clean HDR10.
func hdrFormat(video store.MediaFileStream) string {
	if video.DoViProfile != 0 {
		return dolbyVisionFormat(video)
	}
	if video.HDR10Plus {
		return "hdr10plus"
	}
	switch video.ColorTransfer {
	case "smpte2084":
		return "hdr10"
	case "arib-std-b67":
		return "hlg"
	}
	return ""
}

// dolbyVisionFormat maps a Dolby Vision stream to its closed-vocabulary token.
// Only P5, and P8 and P7 over an HDR10-compatible base layer (BLCompatID 1 or
// 6), get a per-profile token; every other DV shape, including P8.2 (SDR base)
// and P8.4 (HLG base), returns the un-advertised generic sentinel.
func dolbyVisionFormat(video store.MediaFileStream) string {
	switch {
	case video.DoViProfile == 5:
		return hdrDolbyVisionP5
	case video.DoViProfile == 8 && doViHDR10BaseLayer(video.DoViBLCompatID):
		return hdrDolbyVisionP8
	case video.DoViProfile == 7 && doViHDR10BaseLayer(video.DoViBLCompatID):
		return hdrDolbyVisionP7
	default:
		return hdrDolbyVisionGeneric
	}
}

const toneMappedSDRMarker = ".beamfall-tonemapped-sdr"

// VideoSignal is the source video color metadata needed after playbackInfo
// minting, when the byte/precompute layers build ffmpeg renditions.
type VideoSignal struct {
	ColorTransfer  string
	ColorPrimaries string
	DoViProfile    int
	DoViBLCompatID int
	// Codec and BitDepth are the primary video stream's, as probed.
	Codec    string
	BitDepth int
}

func VideoSignalFromStreams(streams []store.MediaFileStream) VideoSignal {
	video, ok := primaryVideoStream(streams)
	if !ok {
		return VideoSignal{}
	}
	return VideoSignal{
		ColorTransfer:  video.ColorTransfer,
		ColorPrimaries: video.ColorPrimaries,
		DoViProfile:    video.DoViProfile,
		DoViBLCompatID: video.DoViBLCompatID,
		Codec:          video.Codec,
		BitDepth:       video.BitDepth,
	}
}

func (v VideoSignal) NeedsToneMap() bool {
	switch {
	case v.DoViProfile == 7 || v.DoViProfile == 8:
		return true
	case v.ColorTransfer == "smpte2084" || v.ColorTransfer == "arib-std-b67":
		return true
	default:
		return false
	}
}

// toneMapSupported gates HDR renditions on a PROVEN tone-map capability. It is
// a variable so a test can state the build it means (conforming or not) instead
// of inheriting whatever FFmpeg happens to sit on the developer's PATH.
var toneMapSupported = ffmpeg.ToneMapSupported

// FullTranscodeRefusal names why this source cannot be re-encoded at all, or ""
// when it can. The reason reaches the wire as playbackInfo's `reason`, so a
// client is told up front instead of discovering it as an opaque mid-playback
// FFmpeg failure.
//
// Dolby Vision profile 5 has no base layer to tone-map. An HDR source on a
// build that has not proven it can run Beamfall's tone-map chain is refused for
// the same reason: the honest outcome of a missing capability is fewer
// renditions offered, never a rendition with the wrong colors.
func (v VideoSignal) FullTranscodeRefusal() string {
	if v.DoViProfile == 5 {
		return ffmpeg.ToneMapRefusalDoViP5
	}
	if v.NeedsToneMap() {
		if !ffmpeg.ToneMapEnabled() {
			return ffmpeg.ToneMapRefusalDisabled
		}
		if !toneMapSupported() {
			return ffmpeg.ToneMapRefusalUnverified
		}
	}
	return ""
}

func estimatedSourceBitrate(media store.Media, file store.MediaFile) int {
	if media.DurationSeconds <= 0 || file.SizeBytes <= 0 {
		return 0
	}
	maxInt := int64(int(^uint(0) >> 1))
	if file.SizeBytes > maxInt/8 {
		return int(maxInt)
	}
	bitsPerSecond := file.SizeBytes * 8 / int64(media.DurationSeconds)
	if bitsPerSecond > maxInt {
		return int(maxInt)
	}
	return int(bitsPerSecond)
}

// Both subtitle predicates read the overlay's DERIVED VIEW of the declared
// formats rather than the profile field itself, so the Version chosen, the
// direct-play verdict, and the issued grant all agree on one subtitle view
// without the declared profile ever being rewritten.
func subtitleFormatsCompatible(streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) bool {
	stream, ok := selectedSubtitleStream(streams, selectedSubtitleIndex)
	if !ok {
		return true
	}
	return ffmpeg.ResolveSubtitleDelivery(stream.Codec, ov.subtitleFormats(profile.SubtitleFormats)) != ffmpeg.SubtitleBurnIn
}

func selectedBurnSubtitleIndex(streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) int {
	stream, ok := selectedSubtitleStream(streams, selectedSubtitleIndex)
	if !ok {
		return -1
	}
	if ffmpeg.ResolveSubtitleDelivery(stream.Codec, ov.subtitleFormats(profile.SubtitleFormats)) != ffmpeg.SubtitleBurnIn {
		return -1
	}
	return stream.Index
}

func selectedSubtitleStream(streams []store.MediaFileStream, selectedSubtitleIndex *int) (store.MediaFileStream, bool) {
	if selectedSubtitleIndex == nil {
		return store.MediaFileStream{}, false
	}
	for _, stream := range streams {
		if stream.Type != "subtitle" {
			continue
		}
		if stream.Index == *selectedSubtitleIndex {
			return stream, true
		}
	}
	return store.MediaFileStream{}, false
}

// primaryVideoStream returns the first video-type stream (ffprobe order). The
// boolean is false when the inventory records no video stream.
func primaryVideoStream(streams []store.MediaFileStream) (store.MediaFileStream, bool) {
	for _, stream := range streams {
		if stream.Type == "video" {
			return stream, true
		}
	}
	return store.MediaFileStream{}, false
}

func videoCodecCap(caps []VideoCodecCap, codec string) (VideoCodecCap, bool) {
	for _, c := range caps {
		if strings.EqualFold(c.Codec, codec) {
			return c, true
		}
	}
	return VideoCodecCap{}, false
}

func audioCodecCap(caps []AudioCodecCap, codec string) (AudioCodecCap, bool) {
	want := audioCodecFamily(codec)
	for _, c := range caps {
		if audioCodecFamily(c.Codec) == want {
			return c, true
		}
	}
	return AudioCodecCap{}, false
}

func normalizedAudioCodec(codec string) string {
	codec = strings.ToLower(strings.TrimSpace(codec))
	codec = strings.ReplaceAll(codec, "_", "-")
	codec = strings.ReplaceAll(codec, " ", "-")
	switch codec {
	case "dtshdma", "dts-hdma", "dts-hd-master-audio", "dts-ma":
		return "dts-hd-ma"
	}
	return codec
}

// audioCodecFamily is the token capability matching compares. It folds
// ffprobe's concrete linear-PCM names (pcm_s16le, pcm_s24be, pcm_f32le,
// pcm_bluray, ...) into the wire token "pcm", so a profile declaring "pcm"
// matches a WAV or AIFF track (CORE-0409). A-law and mu-law are companded, not
// linear PCM, and keep their own names. normalizedAudioCodec is left unchanged
// because losslessAudioStream and the quirk canonicalizer read its output.
func audioCodecFamily(codec string) string {
	name := normalizedAudioCodec(codec)
	if strings.HasPrefix(name, "pcm-") && name != "pcm-alaw" && name != "pcm-mulaw" {
		return "pcm"
	}
	return name
}

func dtsHDMasterAudioProfile(profile string) bool {
	profile = strings.ToLower(profile)
	return strings.Contains(profile, "hd") && strings.Contains(profile, "ma")
}

func containsFold(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if strings.EqualFold(candidate, needle) {
			return true
		}
	}
	return false
}

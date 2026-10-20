package playback

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Rung is one advertised HLS rendition (ADR-0010 §8: honest ABR). The ladder
// is a small fixed vocabulary; what varies per Media is which rungs the
// source can honestly back — a rung is advertised only at or below the
// source height, never upscaled. Bandwidth is the peak rate the master
// playlist declares: the encoder's maxrate ceiling plus audio, so the
// advertisement is an upper bound the server actually enforces.
//
// CopyVideo marks the audio-only-transcode rung: the video
// stream is muxed through untouched and only the audio is transcoded. It
// carries no Height/VideoKbps ceiling — a copied stream keeps the source
// dimensions and bitrate by construction — so the rung is advertised at the
// source size.
type Rung struct {
	ID        string
	Codec     string // empty = h264
	Height    int    // 0 = encode at source size (dimensions unknown)
	Width     int    // 0 = unknown; derived from the source aspect when known
	VideoKbps int
	Bandwidth int  // peak bits/s for #EXT-X-STREAM-INF BANDWIDTH
	CopyVideo bool // true = -c:v copy + audio transcode (no video re-encode)
}

// h264LadderRungs is the software-safe rung vocabulary, best first.
var h264LadderRungs = []Rung{
	{ID: "1080p", Height: 1080, VideoKbps: 8000},
	{ID: "720p", Height: 720, VideoKbps: 4000},
	{ID: "480p", Height: 480, VideoKbps: 1500},
}

// hevcLadderRungs is offered only when the client declares HEVC and the server
// has a verified hardware encoder, so 4K transcodes never fall onto libx265.
var hevcLadderRungs = []Rung{
	{ID: "2160p-hevc", Codec: "hevc", Height: 2160, VideoKbps: 25000},
	{ID: "1440p-hevc", Codec: "hevc", Height: 1440, VideoKbps: 16000},
}

type ladderCaps struct {
	ClientMaxHeight int
	HEVC            bool
	HEVCMaxHeight   int
}

// sourceRungID names the single rung offered when the source dimensions are
// unknown: encode at source size under the top ladder bitrate ceiling — the
// one claim that cannot upscale by construction.
const sourceRungID = "source"

// audioCopyRungID names the single rung offered for the audio-only-transcode
// case: the video is copied through and only the audio is
// transcoded, so it is the lone rung the master advertises — there is no
// resolution ladder to climb when the video is never re-encoded.
const audioCopyRungID = "audio-copy"

// audioCopyRung is the single rung for the audio-only-transcode decision: it
// copies the source video (no Height/VideoKbps cap) and transcodes only the
// audio. Bandwidth advertises the source video rate ceiling plus the AAC
// allowance; with no per-stream source bitrate recorded, the top ladder rung's
// ceiling stands in as an honest upper bound.
func audioCopyRung() Rung {
	top := h264LadderRungs[0]
	return Rung{
		ID:        audioCopyRungID,
		CopyVideo: true,
		Bandwidth: bandwidthFor(top.VideoKbps),
	}
}

// bandwidthFor converts a video maxrate ceiling into the peak stream rate
// advertised to clients: video plus a stereo-AAC-and-mux allowance.
func bandwidthFor(videoKbps int) int {
	return videoKbps*1000 + 256_000
}

// rungsFor returns the rungs the source can honestly back, best first, with no
// client-capability ceiling. A source below the lowest ladder rung gets a
// single rung at its own height; unknown dimensions get the single source rung.
// It is rungsForClient with the ceiling disabled — the existing callers that
// have no client height in hand (the byte layer, which never sees the
// capability profile — see decide() in playback.go) keep their behaviour
// exactly.
func rungsFor(sourceWidth, sourceHeight int) []Rung {
	return rungsForClient(sourceWidth, sourceHeight, 0)
}

// rungsForClient returns the rungs that are *honest twice over*:
// at or below the source height (never upscaled) and at or below what the
// client can actually consume. clientMaxHeight is the decode ceiling the
// CapabilityProfile declares for the codec the transcode targets (the smallest
// VideoCodecCap.MaxHeight that applies); 0 means "no client ceiling" — an
// unknown or unconstrained client, judged on the source alone, identical to the
// prior source-only behaviour.
//
// The never-upscale rule still wins: a rung is dropped if it exceeds either
// bound. When the two bounds together rule out every ladder rung — a tiny
// source, or a client whose ceiling sits below the lowest ladder rung — the
// floor is a single rung at min(source, ceiling), so the client is always
// offered exactly one rendition it can both receive (honest to the source) and
// play (honest to its capability), never zero.
func rungsForClient(sourceWidth, sourceHeight, clientMaxHeight int) []Rung {
	return rungsForLadder(sourceWidth, sourceHeight, ladderCaps{ClientMaxHeight: clientMaxHeight})
}

func rungsForLadder(sourceWidth, sourceHeight int, caps ladderCaps) []Rung {
	if sourceHeight <= 0 {
		return []Rung{unknownDimensionRung(caps)}
	}
	var rungs []Rung
	for _, rung := range ladderVocabulary(caps.HEVC) {
		ceiling := sourceHeight
		switch rung.Codec {
		case "hevc":
			if caps.HEVCMaxHeight > 0 && caps.HEVCMaxHeight < ceiling {
				ceiling = caps.HEVCMaxHeight
			}
		default:
			if caps.ClientMaxHeight > 0 && caps.ClientMaxHeight < ceiling {
				ceiling = caps.ClientMaxHeight
			}
		}
		if rung.Height > ceiling {
			continue // never advertise above the source or the client ceiling
		}
		rung.Width = rungWidth(sourceWidth, sourceHeight, rung.Height)
		rung.Bandwidth = bandwidthFor(rung.VideoKbps)
		rungs = append(rungs, rung)
	}
	if len(rungs) == 0 {
		lowest := h264LadderRungs[len(h264LadderRungs)-1]
		ceiling := sourceHeight
		if caps.ClientMaxHeight > 0 && caps.ClientMaxHeight < ceiling {
			ceiling = caps.ClientMaxHeight
		}
		rungs = []Rung{{
			ID:        fmt.Sprintf("%dp", ceiling),
			Height:    ceiling,
			Width:     rungWidth(sourceWidth, sourceHeight, ceiling),
			VideoKbps: lowest.VideoKbps,
			Bandwidth: bandwidthFor(lowest.VideoKbps),
		}}
	}
	return rungs
}

// unknownDimensionRung is the single rung offered when the source dimensions
// were never probed. With no ceiling it is the unbounded source rung, exactly
// as before. With one, the rung is bounded at that ceiling instead — otherwise
// a probe-less file would ignore the ceiling entirely, which is the one place a
// height or bitrate cap could silently fail to reach the encoder. The rung is
// built by copying the WHOLE selected Rung value so its Codec survives; a
// field-by-field literal would turn a selected HEVC rung into an H.264 encode.
// Its Height is an upper bound for the encoder, never an advertised measured
// size — see advertisedRungHeight.
func unknownDimensionRung(caps ladderCaps) Rung {
	if caps.ClientMaxHeight <= 0 {
		top := h264LadderRungs[0]
		return Rung{ID: sourceRungID, VideoKbps: top.VideoKbps, Bandwidth: bandwidthFor(top.VideoKbps)}
	}
	rung, ceiling := bestRungWithinCaps(caps)
	rung.ID = fmt.Sprintf("%dp", ceiling)
	rung.Height = ceiling
	rung.Width = 0
	rung.Bandwidth = bandwidthFor(rung.VideoKbps)
	return rung
}

// bestRungWithinCaps returns the best ladder rung at or below the applicable
// ceiling, plus that ceiling. A ceiling below every fixed rung has no candidate
// — the lowest rung's bitrate stands in, mirroring the known-height synthesis
// in rungsForLadder exactly, so a sub-480 cap is bounded on a probe-less source
// just as it is on a measured one.
func bestRungWithinCaps(caps ladderCaps) (Rung, int) {
	for _, rung := range ladderVocabulary(caps.HEVC) {
		ceiling := caps.ClientMaxHeight
		if rung.Codec == "hevc" && caps.HEVCMaxHeight > 0 && caps.HEVCMaxHeight < ceiling {
			ceiling = caps.HEVCMaxHeight
		}
		if rung.Height <= ceiling {
			return rung, ceiling
		}
	}
	return h264LadderRungs[len(h264LadderRungs)-1], caps.ClientMaxHeight
}

func ladderVocabulary(hevc bool) []Rung {
	if !hevc {
		return h264LadderRungs
	}
	rungs := make([]Rung, 0, len(hevcLadderRungs)+len(h264LadderRungs))
	rungs = append(rungs, hevcLadderRungs...)
	rungs = append(rungs, h264LadderRungs...)
	return rungs
}

// lowestRung returns the lowest (cheapest) rung of a best-first rung list — the
// last element, since rungsFor/rungsForClient emit rungs highest-first and
// always return at least one. It is the rung a capacity-degraded Watch-Party
// session is capped to (ADR-0010 §6 "drop to this quality"; PANEL-16): the
// cheapest honest encode the source can back, so the member keeps a stream
// rather than being refused at the cap.
func lowestRung(rungs []Rung) Rung {
	return rungs[len(rungs)-1]
}

func validateKnownRungID(id string, height int) error {
	if !cleanRungSegment(id) {
		return fmt.Errorf("playback rung %q is not a clean relative segment", id)
	}
	for _, rung := range ladderVocabulary(true) {
		if id == rung.ID {
			return nil
		}
	}
	if id == sourceRungID || id == audioCopyRungID || id == ComposedRungID {
		return nil
	}
	if height > 0 && id == fmt.Sprintf("%dp", height) {
		return nil
	}
	return fmt.Errorf("playback rung %q is not known", id)
}

func cleanRungSegment(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.TrimSpace(id) != id {
		return false
	}
	if filepath.IsAbs(id) {
		return false
	}
	return !strings.ContainsAny(id, `/\`)
}

// rungWidth scales the source width to the rung height, rounded to the even
// pixel the encoder's scale=-2 produces; 0 when the source width is unknown.
func rungWidth(sourceWidth, sourceHeight, rungHeight int) int {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return 0
	}
	width := (sourceWidth*rungHeight + sourceHeight/2) / sourceHeight
	return width &^ 1
}

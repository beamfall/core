package playback

import (
	"slices"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

// Play methods reported on the operator's live-session dashboard. They are
// a coarser, operator-facing reading of the playbackInfo verdict and never
// travel on the playbackInfo wire.
const (
	PlayMethodDirect    = "direct"
	PlayMethodRemux     = "remux"
	PlayMethodTranscode = "transcode"
)

// Play reasons: the closed vocabulary saying why a session is not direct
// playing. The empty reason means nothing stood in the way.
const (
	PlayReasonContainer       = "container"
	PlayReasonVideoCodec      = "video-codec"
	PlayReasonResolution      = "resolution"
	PlayReasonBitrate         = "bitrate"
	PlayReasonVideoProfile    = "video-profile"
	PlayReasonHDR             = "hdr"
	PlayReasonAudio           = "audio"
	PlayReasonSubtitles       = "subtitles"
	PlayReasonDeviceQuirk     = "device-quirk"
	PlayReasonCapacity        = "capacity"
	PlayReasonCachedRendition = "cached-rendition"
	PlayReasonQualityChoice   = "quality-choice"
)

// PlayObservation is the last playback decision made for one session and
// item, as the dashboard reports it.
type PlayObservation struct {
	Method  string
	Reason  string
	Bitrate int // bits per second; 0 when unknown
}

type observationKey struct {
	session store.SessionID
	media   store.MediaID
}

type observedPlay struct {
	PlayObservation
	at time.Time
}

// observations holds the decisions in memory only: they describe live
// sessions, which do not outlive the process either.
type observations struct {
	mu sync.Mutex
	m  map[observationKey]observedPlay
}

const (
	observationSweepSize = 512
	observationMaxAge    = 48 * time.Hour
)

func (o *observations) record(key observationKey, obs PlayObservation, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = make(map[observationKey]observedPlay)
	}
	if len(o.m) >= observationSweepSize {
		for k, v := range o.m {
			if now.Sub(v.at) > observationMaxAge {
				delete(o.m, k)
			}
		}
	}
	o.m[key] = observedPlay{PlayObservation: obs, at: now}
}

func (o *observations) lookup(key observationKey) (PlayObservation, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.m[key]
	return v.PlayObservation, ok
}

// PlayObservation returns the last decision made for a session and item,
// corrected by what the session is being served: a session that was offered
// direct play but holds a transcode chose a lower quality itself.
func (s *Service) PlayObservation(sessionID store.SessionID, mediaID store.MediaID) (PlayObservation, bool) {
	obs, ok := s.observed.lookup(observationKey{session: sessionID, media: mediaID})
	if !ok {
		return PlayObservation{}, false
	}
	if s.sessions == nil {
		return obs, true
	}
	bandwidth, transcoded := s.sessions.deliveredBandwidth(sessionID, mediaID)
	if !transcoded {
		return obs, true
	}
	if obs.Method == PlayMethodDirect {
		obs.Method = PlayMethodTranscode
		obs.Reason = PlayReasonQualityChoice
	}
	if bandwidth > 0 && obs.Method == PlayMethodTranscode {
		obs.Bitrate = bandwidth
	}
	return obs, true
}

// deliveredBandwidth reports whether the session is being served a transcode
// of the item and, when its rendition is known, that rendition's bandwidth. It
// holds for as long as the session does, not only while the encoder runs: an
// encoder that has finished the item leaves the viewer on the same rendition.
// A route change (cast, AirPlay) keeps one transcode per capability profile
// for the same session and item; the one read most recently is the route the
// viewer is on.
func (s *Sessions) deliveredBandwidth(sessionID store.SessionID, mediaID store.MediaID) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var current *hlsSession
	for key, session := range s.sessions {
		if key.SessionID != sessionID || key.MediaID != mediaID || !session.started {
			continue
		}
		if current == nil || session.lastAccess.After(current.lastAccess) {
			current = session
		}
	}
	if current == nil {
		return 0, false
	}
	for _, rung := range current.rungs {
		if rung.ID == current.deliveredRung {
			return rung.Bandwidth, true
		}
	}
	return 0, true
}

// observe records the dashboard reading of one main-item decision.
func (s *Service) observe(sessionID store.SessionID, dec decision, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) {
	obs := PlayObservation{Bitrate: estimatedSourceBitrate(dec.media, file)}
	cause := directPlayRefusalCause(dec.media, file, streams, profile, selectedSubtitleIndex, ov)
	switch dec.verdict {
	case "direct-play":
		obs.Method = PlayMethodDirect
	case "direct-stream":
		obs.Method = PlayMethodRemux
		obs.Reason = cause
	case "transcode", "precomputed-rendition":
		obs.Method = PlayMethodTranscode
		obs.Reason = cause
		if dec.reason == "capacity-degraded" {
			obs.Reason = PlayReasonCapacity
		} else if obs.Reason == "" && dec.verdict == "precomputed-rendition" {
			obs.Reason = PlayReasonCachedRendition
		}
		obs.Bitrate = topRenditionBandwidth(dec.streams)
	default:
		return
	}
	s.observed.record(observationKey{session: sessionID, media: dec.media.ID}, obs, s.now())
}

func topRenditionBandwidth(streams []StreamChoice) int {
	top := 0
	for _, stream := range streams {
		for _, rendition := range stream.Renditions {
			top = max(top, rendition.Bandwidth)
		}
	}
	return top
}

// directPlayRefusalCause names the first gate of canDirectPlay the item
// fails, in the order canDirectPlay applies them. It returns "" exactly when
// canDirectPlay accepts the item.
func directPlayRefusalCause(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile, selectedSubtitleIndex *int, ov quirkOverlay) string {
	if !slices.Contains(profile.Containers, media.Container) {
		return PlayReasonContainer
	}
	if ov.vetoesVideoPassthrough(media, file, streams) ||
		ov.vetoesAudioPassthrough(streams) ||
		ov.vetoesSourceContainer() {
		return PlayReasonDeviceQuirk
	}
	if cause := videoRefusalCause(media, file, streams, profile); cause != "" {
		return cause
	}
	if !audioDirectPlayable(streams, profile) {
		return PlayReasonAudio
	}
	if !subtitleFormatsCompatible(streams, profile, selectedSubtitleIndex, ov) {
		return PlayReasonSubtitles
	}
	return ""
}

// videoRefusalCause mirrors videoPlayable gate for gate, naming the one that
// refuses.
func videoRefusalCause(media store.Media, file store.MediaFile, streams []store.MediaFileStream, profile CapabilityProfile) string {
	if videoDirectPlayable(media, file, streams, profile) {
		return ""
	}
	if len(profile.VideoCodecs) == 0 {
		return PlayReasonVideoCodec
	}
	codecCap, ok := videoCodecCap(profile.VideoCodecs, media.Codec)
	if !ok {
		return PlayReasonVideoCodec
	}
	if codecCap.MaxHeight > 0 && media.VideoHeight > codecCap.MaxHeight {
		return PlayReasonResolution
	}
	if codecCap.MaxBitrate > 0 && estimatedSourceBitrate(media, file) > codecCap.MaxBitrate {
		return PlayReasonBitrate
	}
	if video, ok := primaryVideoStream(streams); ok {
		if format := hdrFormat(video); len(profile.HDRFormats) > 0 && format != "" &&
			!slices.Contains(profile.HDRFormats, format) {
			return PlayReasonHDR
		}
	}
	// What remains of videoPlayable is the codec profile and level.
	return PlayReasonVideoProfile
}

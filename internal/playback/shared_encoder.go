package playback

import (
	"fmt"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// sharedEncoderKey identifies an unencrypted live transcode or composed
// karaoke pipeline independent of per-viewer auth SessionID so N viewers
// watching the same stream share one FFmpeg (+ one wazero presenter) while
// preserving per-session LiveBinding and cutoffCtx revocation (ADR-0012 §7).
type sharedEncoderKey struct {
	scopeKey          string
	libraryID         store.LibraryID
	mediaID           store.MediaID
	fingerprint       string
	rungID            string
	height            int
	videoCodec        string
	videoKbps         int
	copyVideo         bool
	audioOnly         bool
	audioCodec        string
	audioChannels     int
	audioStreamIndex  int
	burnSubtitleIndex int
	composedDigest    string
	pitchSemitones    int
	tempo             float64
	softwareOnly      bool
	h264Only          bool
	quality           string
}

type sharedGroup struct {
	key    sharedEncoderKey
	leader *hlsSession
	leases map[*hlsSession]struct{}
}

func (h *hlsSession) mediaDir() string {
	if h != nil && h.sharedDir != "" {
		return h.sharedDir
	}
	return h.dir
}

// SetPartyRoster wires the Watch Party / Karaoke Party roster onto Sessions
// so cross-Profile party members can share one unencrypted live encoder.
func (s *Sessions) SetPartyRoster(r PartyRoster) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.roster = r
	s.mu.Unlock()
}

func (s *Sessions) sharedKeyFor(h *hlsSession, rung Rung) (sharedEncoderKey, bool) {
	if h == nil || h.secure != nil || h.degraded || h.key.MediaID == "" {
		return sharedEncoderKey{}, false
	}
	inParty := s != nil && s.roster != nil && s.roster.SessionInWatchPartyForMedia(h.key.SessionID, h.key.MediaID)
	if !inParty && h.profile == "" {
		return sharedEncoderKey{}, false
	}
	scopeKey := string(h.profile)
	if inParty {
		scopeKey = "party"
	}
	fp := h.sourceGeneration
	if fp == "" {
		fp = h.src
	}
	audioIdx := -1
	if h.audioStreamIndex != nil {
		audioIdx = *h.audioStreamIndex
	}
	var composedDigest string
	if h.composed != nil {
		composedDigest, _ = h.composed.digest()
	}
	var swOnly, h264Only bool
	var quality string
	if h.encodePolicy != nil {
		swOnly = h.encodePolicy.SoftwareOnly
		h264Only = h.encodePolicy.H264Only
		quality = h.encodePolicy.Quality
	}
	return sharedEncoderKey{
		scopeKey:          scopeKey,
		libraryID:         h.library,
		mediaID:           h.key.MediaID,
		fingerprint:       fp,
		rungID:            rung.ID,
		height:            rung.Height,
		videoCodec:        rung.Codec,
		videoKbps:         rung.VideoKbps,
		copyVideo:         rung.CopyVideo,
		audioOnly:         rung.AudioOnly,
		audioCodec:        h.audioProfile.Codec,
		audioChannels:     h.audioProfile.Channels,
		audioStreamIndex:  audioIdx,
		burnSubtitleIndex: h.burnSubtitleIndex,
		composedDigest:    composedDigest,
		pitchSemitones:    h.adjust.PitchSemitones,
		tempo:             h.adjust.Tempo,
		softwareOnly:      swOnly,
		h264Only:          h264Only,
		quality:           quality,
	}, true
}

// HasSharedEncoder reports whether an active unencrypted shared encoder is
// already running for mediaID (used to avoid degrading Watch Party / Karaoke
// overflow members when they can fan out from the existing encoder).
func (s *Sessions) HasSharedEncoder(mediaID store.MediaID, fingerprint string) bool {
	if s == nil || mediaID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, g := range s.sharedGroups {
		if k.mediaID == mediaID && (fingerprint == "" || k.fingerprint == fingerprint) &&
			g.leader != nil && g.leader.state == sessionRunning && (g.leader.proc != nil || g.leader.start != nil) {
			return true
		}
	}
	return false
}

// attachSharedGroupLocked attaches follower session h to an existing active
// sharedGroup for (h, rung) when requested >= 0 is already produced or covered
// by the leader's current encode window. Returns the leader *hlsSession if
// attached (or already attached), else nil. Callers hold s.mu.
func (s *Sessions) attachSharedGroupLocked(h *hlsSession, rung Rung, requested int) *hlsSession {
	if g := h.sharedGroup; g != nil {
		if g.leader == h {
			return h
		}
		if g.leader != nil && g.leader.state == sessionRunning && g.leader.currentRung == rung.ID {
			if requested < 0 || (requested >= g.leader.baseSegment && (requested <= g.leader.frontier || g.leader.proc != nil || g.leader.start != nil)) {
				return g.leader
			}
		}
		// Incompatible seek or leader exited: detach from shared group so h can
		// start its own independent encode without disrupting other lessees.
		s.detachFromSharedGroupLocked(h)
	}
	key, ok := s.sharedKeyFor(h, rung)
	if !ok || h.proc != nil || h.start != nil {
		return nil
	}
	g := s.sharedGroups[key]
	if g == nil || g.leader == nil || g.leader == h || g.leader.state != sessionRunning {
		return nil
	}
	if g.leader.currentRung != rung.ID || (g.leader.proc == nil && g.leader.start == nil) {
		return nil
	}
	if requested >= 0 && requested < g.leader.baseSegment {
		return nil
	}
	g.leases[h] = struct{}{}
	h.sharedGroup = g
	h.sharedDir = g.leader.dir
	return g.leader
}

func (s *Sessions) registerSharedLeaderLocked(h *hlsSession, rung Rung) {
	if h.sharedGroup != nil {
		return
	}
	key, ok := s.sharedKeyFor(h, rung)
	if !ok {
		return
	}
	if s.sharedGroups == nil {
		s.sharedGroups = make(map[sharedEncoderKey]*sharedGroup)
	}
	if existing := s.sharedGroups[key]; existing != nil && existing.leader != h {
		return
	}
	g := &sharedGroup{
		key:    key,
		leader: h,
		leases: map[*hlsSession]struct{}{h: {}},
	}
	s.sharedGroups[key] = g
	h.sharedGroup = g
}

func (s *Sessions) detachFromSharedGroupLocked(h *hlsSession) {
	g := h.sharedGroup
	if g == nil {
		return
	}
	delete(g.leases, h)
	h.sharedGroup = nil
	h.sharedDir = ""
	if g.leader == h {
		// Try to promote an active follower so remaining viewers keep streaming.
		var nextLeader *hlsSession
		for candidate := range g.leases {
			if candidate != h && candidate.state == sessionRunning {
				nextLeader = candidate
				break
			}
		}
		if nextLeader != nil {
			s.promoteSharedLeaderLocked(g, h, nextLeader)
			return
		}
		delete(s.sharedGroups, g.key)
		g.leader = nil
	} else if len(g.leases) == 0 {
		delete(s.sharedGroups, g.key)
	}
}

// promoteSharedLeaderLocked transfers running FFmpeg ownership, scratch dir,
// and capacity charge from outgoing leader oldL to surviving follower newL so
// revoking or closing oldL does not interrupt surviving viewers. Callers hold s.mu.
func (s *Sessions) promoteSharedLeaderLocked(g *sharedGroup, oldL, newL *hlsSession) {
	oldDir, oldScratch := newL.dir, newL.scratch
	newL.dir = oldL.dir
	newL.scratch = oldL.scratch
	newL.sharedDir = ""
	newL.proc = oldL.proc
	newL.run = oldL.run
	newL.start = oldL.start
	newL.writing = oldL.writing
	newL.currentRung = oldL.currentRung
	newL.deliveredRung = oldL.deliveredRung
	newL.baseSegment = oldL.baseSegment
	newL.frontier = oldL.frontier
	newL.requestedFrontier = oldL.requestedFrontier
	newL.started = oldL.started
	newL.transcodeLease = oldL.transcodeLease
	if newL.run != nil {
		newL.run.session = newL
	}
	if newL.start != nil {
		newL.start.session = newL
	}
	if cost, ok := s.charges[oldL]; ok {
		delete(s.charges, oldL)
		s.charges[newL] = cost
		oldL.charged = false
		newL.charged = true
	}
	// Hand oldL's own scratch/dir to the promoted leader, and give oldL the
	// follower's unused scratch/dir so oldL's teardown cleans up only the unused dir.
	oldL.dir = oldDir
	oldL.scratch = oldScratch
	oldL.proc = nil
	oldL.run = nil
	oldL.start = nil
	oldL.writing = nil
	oldL.transcodeLease = nil
	g.leader = newL
	// Point remaining followers' sharedDir at newL.dir (same path string).
	for f := range g.leases {
		if f != newL {
			f.sharedDir = newL.dir
		}
	}
}

func (s *Sessions) watcherForSession(h *hlsSession) *sessionWatcher {
	s.mu.Lock()
	target := h
	if h.sharedGroup != nil && h.sharedGroup.leader != nil {
		target = h.sharedGroup.leader
	}
	s.mu.Unlock()
	return target.sharedWatcher()
}

func (h *hlsSession) String() string {
	return fmt.Sprintf("hlsSession(%s:%s)", h.key.SessionID, h.key.MediaID)
}

var _ = ffmpeg.AudioAdjust{}

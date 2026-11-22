package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// Composed-picture renditions (ADR-0057 precomputed renditions): an audio
// Media whose plugin can describe a picture for it — a graphics sidecar or
// timed text — is precomputed once into an ordinary H.264/AAC HLS rendition
// with that picture, so any video-capable client plays it as a video and a
// Watch Party's members all read the same cached bytes. Core names only the
// shape (graphics, timed text); what the picture means is plugin data.
const (
	// ComposedRungID is the rung every composed rendition is stored under.
	ComposedRungID = "composed"

	composedCodec  = "h264"
	composedHeight = 720
	// composedVideoKbps is the 720p composed encode's video maxrate, and
	// composedBandwidth the whole peak the rendition is advertised and
	// admitted at: that maxrate plus the ladder's audio and mux allowance
	// (bandwidthFor), the same total as the live 720p composed rung.
	composedVideoKbps = 1500
	composedBandwidth = composedVideoKbps*1000 + audioMuxReserve
	// composedTimedTextFile is written into the rung directory for the encode
	// and removed afterwards; the HLS serving path never serves it either way.
	composedTimedTextFile = "picture.ass"
	// composedDigestLen is how much of the picture digest the rendition ID
	// carries — enough that a changed picture never collides.
	composedDigestLen = 16

	// ComposedSweepJobKind is the seeded Schedule's job: it walks the
	// Libraries whose plugin composes pictures and enqueues each missing
	// composed rendition (EnsureComposedRendition).
	ComposedSweepJobKind store.JobKind = "playback-composed-sweep"
)

// ComposedPicture is a plugin's description of a picture for a Media that has
// none. Exactly one field is set: GraphicsPath, a graphics track beside the
// source FFmpeg decodes as video; or TimedText, an ASS document.
type ComposedPicture struct {
	GraphicsPath string
	TimedText    string
}

// PictureComposer resolves the composed picture for a Media and its source
// file. ok is false when the Media's plugin offers none (most Media). An error
// is a failure to decide, never a decline.
type PictureComposer func(ctx context.Context, media store.Media, sourcePath string) (ComposedPicture, bool, error)

// GenerationComposer is PictureComposer keyed by Media ID, for the precompute
// generator, which re-resolves the picture from the job's payload alone.
type GenerationComposer func(ctx context.Context, mediaID store.MediaID, sourcePath string) (ComposedPicture, bool, error)

// SetPictureComposer enables composed-picture renditions in playbackInfo. nil
// (the default) disables them.
func (s *Service) SetPictureComposer(composer PictureComposer) {
	s.composer = composer
}

// composedGraphicsRecipe names the graphics drawing recipe in the rendition
// digest. Change it with any visible change to the graphics branch of the
// composed encode (CORE-3149: the xBR upscale replaced nearest-neighbour).
const composedGraphicsRecipe = "xbr4"

func (p ComposedPicture) validate() error {
	if (p.GraphicsPath == "") == (p.TimedText == "") {
		return errors.New("composed picture sets exactly one of graphics or timed text")
	}
	return nil
}

// digest identifies the picture's content so an edited sidecar yields a new
// rendition: timed text hashes its document; a graphics track hashes its path,
// size, and modification time (reading a whole track per playbackInfo would
// cost more than the change detection is worth). The graphics hash also
// carries composedGraphicsRecipe, so a change to how graphics are drawn
// re-makes their renditions instead of serving the old picture.
func (p ComposedPicture) digest() (string, error) {
	return p.digestWithStat(os.Stat)
}

func (p ComposedPicture) digestWithStat(stat func(string) (os.FileInfo, error)) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	h := sha256.New()
	if p.TimedText != "" {
		fmt.Fprintf(h, "timed-text\x00%s", p.TimedText)
	} else {
		info, err := stat(p.GraphicsPath)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "graphics\x00%s\x00%s\x00%d\x00%d", composedGraphicsRecipe, p.GraphicsPath, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))[:composedDigestLen], nil
}

// composedRenditionID is the cache identity of one composed picture over one
// source file. The rung stays ComposedRungID; the picture digest rides in the
// ID so a changed picture never matches a stale rendition.
func composedRenditionID(mediaID store.MediaID, fingerprint, digest string) store.PlaybackRenditionID {
	return PrecomputeRenditionID(mediaID, fingerprint, ComposedRungID+"-"+digest)
}

// composesPicture reports whether a Media is a candidate: only audio Media are
// given a picture. Video Media (including any whose picture the plugin also
// describes) keep their own.
func composesPicture(media store.Media) bool {
	return media.Class == store.MediaClassAudio
}

// PrecomputeComposedRendition enqueues the composed rendition for one Media
// when its plugin describes a picture, through the same explicit trigger and
// job as every precomputed rendition. It returns no jobs, and no
// error, when the Media is not a candidate or the composer declines; the
// caller then falls back to the ordinary ladder.
//
// supported is the same FFmpeg capability check EnsureComposedRendition and
// the live composed path consult (nil: the cached check of the resolved
// build). A picture whose recipe the build cannot run is refused with
// ErrComposedRecipeUnsupported and nothing is queued, so the operator's
// explicit precompute never queues a render that can only fail.
func PrecomputeComposedRendition(ctx context.Context, st *store.Store, mediaID store.MediaID, composer PictureComposer, supported ComposedRecipeCheck, cacheRoot string, now time.Time) ([]store.Job, error) {
	req, picture, ok, err := composedRequest(ctx, st, mediaID, composer, cacheRoot)
	if err != nil || !ok {
		return nil, err
	}
	if !composedRecipeRunnable(ctx, supported, picture, req) {
		return nil, ErrComposedRecipeUnsupported
	}
	job, err := RequestPrecomputeRendition(ctx, st, req, normalizePrecomputeTime(now))
	if err != nil {
		return nil, err
	}
	return []store.Job{job}, nil
}

// EnsureComposedRendition is the Schedule-driven form of
// PrecomputeComposedRendition (ADR-0057 §2: a Schedule is an explicit
// trigger): it enqueues only when no rendition exists yet for the Media's
// current picture. A rendition in any state counts: generating, ready, failed,
// or evicted. So a sweep never re-renders a song it already rendered, never
// loops on one whose encode failed, and never fights budget eviction; the
// explicit Console precompute (PrecomputeComposedRendition) still re-renders
// any of them. An edited sidecar changes the digest and so the rendition ID,
// which the next sweep picks up; the superseded rendition ages out through
// the budget's LRU eviction.
//
// supported is the FFmpeg capability check the live composed path uses (nil:
// the cached check of the resolved build). A picture whose recipe the build
// cannot run (a timed-text picture without libass's `ass` filter, say) is not
// queued: EnsureComposedRendition returns ErrComposedRecipeUnsupported and
// records nothing, so no render fails on every sweep, and a later build that
// can run it picks the picture up. A rendition already stored for the picture
// is still left alone, so a Ready one keeps being served.
func EnsureComposedRendition(ctx context.Context, st *store.Store, mediaID store.MediaID, composer PictureComposer, supported ComposedRecipeCheck, cacheRoot string, now time.Time) (bool, error) {
	req, picture, ok, err := composedRequest(ctx, st, mediaID, composer, cacheRoot)
	if err != nil || !ok {
		return false, err
	}
	_, err = st.PlaybackRendition(ctx, req.RenditionID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, store.ErrPlaybackRenditionNotFound) {
		return false, err
	}
	if !composedRecipeRunnable(ctx, supported, picture, req) {
		return false, ErrComposedRecipeUnsupported
	}
	if _, err := RequestPrecomputeRendition(ctx, st, req, normalizePrecomputeTime(now)); err != nil {
		return false, err
	}
	return true, nil
}

// composedRequest resolves the precompute request for one Media's composed
// picture, and the picture. ok is false when the Media is not a candidate,
// its Library is encrypted, or the composer declines.
func composedRequest(ctx context.Context, st *store.Store, mediaID store.MediaID, composer PictureComposer, cacheRoot string) (PrecomputeRequest, ComposedPicture, bool, error) {
	if st == nil {
		return PrecomputeRequest{}, ComposedPicture{}, false, errors.New("store is required")
	}
	if composer == nil || mediaID == "" || strings.TrimSpace(cacheRoot) == "" {
		return PrecomputeRequest{}, ComposedPicture{}, false, nil
	}
	media, err := st.Media(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return PrecomputeRequest{}, ComposedPicture{}, false, err
	}
	if !composesPicture(media) {
		return PrecomputeRequest{}, ComposedPicture{}, false, nil
	}
	// Same fail-closed rule as every precomputed rendition.
	encrypted, err := st.LibraryEncrypted(ctx, media.LibraryID)
	if err != nil || encrypted {
		return PrecomputeRequest{}, ComposedPicture{}, false, err
	}
	files, err := st.MediaFilesForMedia(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return PrecomputeRequest{}, ComposedPicture{}, false, err
	}
	if len(files) == 0 {
		return PrecomputeRequest{}, ComposedPicture{}, false, errors.New("media has no source files")
	}
	source := files[0]
	picture, ok, err := composer(ctx, media, source.Path)
	if err != nil || !ok {
		return PrecomputeRequest{}, ComposedPicture{}, false, err
	}
	digest, err := picture.digest()
	if err != nil {
		return PrecomputeRequest{}, ComposedPicture{}, false, err
	}
	renditionID := composedRenditionID(mediaID, source.ContentFingerprint, digest)
	return PrecomputeRequest{
		MediaID:            mediaID,
		ContentFingerprint: source.ContentFingerprint,
		RenditionID:        renditionID,
		RungID:             ComposedRungID,
		Codec:              composedCodec,
		Height:             composedHeight,
		Bandwidth:          composedBandwidth,
		SourcePath:         source.Path,
		Path:               filepath.Join(cacheRoot, string(renditionID)),
	}, picture, true, nil
}

// composedRecipeRunnable consults the capability check (nil: the cached check
// of the resolved build) against the exact rendition the precomputed encode
// runs at, for both the explicit precompute and the Schedule sweep.
func composedRecipeRunnable(ctx context.Context, supported ComposedRecipeCheck, picture ComposedPicture, req PrecomputeRequest) bool {
	if supported == nil {
		supported = func(timedText bool, rendition ffmpeg.Rendition) bool {
			return composedRecipeSupported(ctx, timedText, rendition)
		}
	}
	return supported(picture.TimedText != "", composedPrecomputeRendition(req.Height))
}

// composedPrecomputeRendition is the shape the precomputed composed encode
// runs at, and so the shape its capability is checked against.
func composedPrecomputeRendition(height int) ffmpeg.Rendition {
	return ffmpeg.Rendition{
		Height:        height,
		VideoKbps:     composedVideoKbps,
		AudioCodec:    "aac",
		AudioChannels: 2,
	}
}

// precomputeComposedHLS is the generator's composed branch. It re-resolves the
// picture at generation time: a declined or changed picture (a sidecar edited
// or removed while the job was queued) is ErrPrecomputeInputChanged, so the
// stale rendition is never published.
func (g FFmpegPrecomputeGenerator) precomputeComposedHLS(ctx context.Context, runner *ffmpeg.Runner, spec PrecomputeSpec) error {
	if g.Composer == nil {
		return errors.New("composed rendition requested without a picture composer")
	}
	picture, ok, err := g.Composer(ctx, spec.MediaID, spec.SourcePath)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPrecomputeInputChanged
	}
	digest, err := picture.digest()
	if err != nil {
		return err
	}
	if composedRenditionID(spec.MediaID, spec.ContentFingerprint, digest) != spec.RenditionID {
		return ErrPrecomputeInputChanged
	}
	rungDir := filepath.Join(spec.OutputDir, spec.RungID)
	if err := os.RemoveAll(rungDir); err != nil {
		return err
	}
	if err := os.MkdirAll(rungDir, 0o700); err != nil {
		return err
	}
	input := ffmpeg.ComposedPicture{GraphicsPath: picture.GraphicsPath}
	if picture.TimedText != "" {
		input.TimedTextPath = filepath.Join(rungDir, composedTimedTextFile)
		defer func() { _ = os.Remove(input.TimedTextPath) }()
		if err := os.WriteFile(input.TimedTextPath, []byte(picture.TimedText), 0o600); err != nil {
			return err
		}
	}
	proc, err := runner.StartBackgroundComposedHLS(ctx, spec.SourcePath, rungDir, input, composedPrecomputeRendition(spec.Height))
	if err != nil {
		return err
	}
	if err := proc.Wait(); err != nil {
		return err
	}
	return writePrecomputeMasterPlaylist(filepath.Join(spec.OutputDir, "master.m3u8"), spec)
}

// composedChoice is playbackInfo's composed-picture branch: for an audio Media
// whose plugin describes a picture, a video-capable client gets the Ready
// composed rendition matching the current picture instead of the bare audio.
// With none Ready, transcodeRefusal empty (policy and capacity admit a live
// stream), a composed shape within the client and quirk limits, and an FFmpeg
// build that can run the picture's recipe, it answers a live composed stream
// instead (live=true) and queues the precomputed rendition for the next play.
// It answers ok=false —
// leaving the ladder untouched — whenever any condition fails, including a
// composer error: playback never breaks because a picture could not be
// resolved. A grant carrying a pitch shift or tempo change skips the
// precomputed renditions: only the live stream can encode it.
func (s *Service) composedChoice(ctx context.Context, scope auth.Scope, grant streamtoken.Grant, suffix string, media store.Media, file store.MediaFile, profile CapabilityProfile, wantedAudio ffmpeg.AudioProfile, expiresAt time.Time, ov quirkOverlay, transcodeRefusal string, audioOverride bool) (choice StreamChoice, live, ok bool, err error) {
	if s.composer == nil || !composesPicture(media) || grant.BurnSubtitleIndex != 0 || !profileTakesComposedVideo(profile) {
		return StreamChoice{}, false, false, nil
	}
	renditions, err := s.store.ReadyPlaybackRenditions(ctx, media.ID, scope.LibraryFilter())
	if err != nil {
		return StreamChoice{}, false, false, err
	}
	liveRung, shapeFits := liveComposedRung(profile, ov)
	canLive := transcodeRefusal == "" && s.sessions != nil && shapeFits
	// Only resolve the picture (which reads sidecars) when it could be used:
	// a composed rendition might match, or a live stream could be offered.
	if !hasComposedRendition(renditions, file.ContentFingerprint) && !canLive {
		return StreamChoice{}, false, false, nil
	}
	picture, resolved, err := s.composer(ctx, media, file.Path)
	if err != nil || !resolved {
		return StreamChoice{}, false, false, nil
	}
	stat := os.Stat
	if s.sessions != nil && s.sessions.statSource != nil {
		stat = s.sessions.statSource
	}
	digest, err := picture.digestWithStat(stat)
	if err != nil {
		return StreamChoice{}, false, false, nil
	}
	want := composedRenditionID(media.ID, file.ContentFingerprint, digest)
	// A precomputed rendition is unadjusted and carries the default audio
	// track, so a pitch shift, a tempo change or another selected track
	// (CORE-1873) is always composed live.
	adjusted := !grantAudioAdjust(grant).IsZero()
	for _, rendition := range renditions {
		rendition = withComposedPeak(rendition)
		if adjusted || audioOverride || rendition.ID != want || !composedRenditionCompatible(rendition, profile, wantedAudio, ov) {
			continue
		}
		choice, err := s.precomputedStreamChoice(ctx, grant, suffix, rendition, expiresAt)
		return choice, false, err == nil, err
	}
	// A build that cannot run the recipe (no libass for timed text, no AAC
	// encoder) would turn a song that plays as audio into a failing stream, so
	// it keeps the audio.
	if !canLive || !composedRecipeSupported(ctx, picture.TimedText != "", composedLiveRendition(liveRung, wantedAudio, grantAudioAdjust(grant))) {
		return StreamChoice{}, false, false, nil
	}
	choice, err = s.liveComposedChoice(ctx, grant, suffix, wantedAudio, liveRung, expiresAt)
	if err != nil {
		return StreamChoice{}, false, false, err
	}
	if err := s.queueComposedRendition(ctx, media.ID); err != nil {
		slog.WarnContext(ctx, "queueing composed rendition after a live composed play failed", "media", media.ID, "error", err)
	}
	return choice, true, true, nil
}

// liveComposedChoice mints the live composed-picture HLS stream: the grant
// carries ComposedPicture, and the chosen shape as its client height ceiling,
// so the byte layer opens a session with that single composed rung under the
// ordinary transcode session limits.
func (s *Service) liveComposedChoice(ctx context.Context, grant streamtoken.Grant, suffix string, audio ffmpeg.AudioProfile, rung Rung, expiresAt time.Time) (StreamChoice, error) {
	grant.Target = streamtoken.TargetMediaHLS
	grant = withHLSAudioGrant(grant, audio)
	grant = withHLSLadderGrant(grant, ladderCaps{ClientMaxHeight: rung.Height})
	grant.ComposedPicture = true
	token, err := s.issueDecisionGrant(ctx, grant)
	if err != nil {
		return StreamChoice{}, err
	}
	return StreamChoice{
		ID:         "composed-" + suffix,
		Kind:       "hls",
		URL:        "/api/media/hls/" + token + "/" + masterPlaylistName,
		Token:      token,
		ExpiresAt:  expiresAt.Format(time.RFC3339),
		Renditions: []RenditionInfo{{ID: rung.ID, Height: rung.Height, Bandwidth: rung.Bandwidth}},
	}, nil
}

// ComposedRecipeCheck reports whether the resolved FFmpeg build can run the
// composed encode of a timed-text (true) or graphics (false) picture with a
// rendition.
type ComposedRecipeCheck func(timedText bool, rendition ffmpeg.Rendition) bool

// ErrComposedRecipeUnsupported is PrecomputeComposedRendition or
// EnsureComposedRendition declining a picture
// the FFmpeg build cannot compose.
var ErrComposedRecipeUnsupported = errors.New("ffmpeg build cannot run the composed picture recipe")

// composedRecipeSupported is the cached ComposedRecipeCheck of the resolved
// build. It is a variable so a test can state the build it means instead of
// inheriting the FFmpeg on the developer's PATH, like toneMapSupported.
var composedRecipeSupported = ffmpeg.ComposedRecipeSupportedContext

// composedLiveRendition is the rendition a live composed session encodes for
// rung, audio and adjust: the fields hlsSession.rendition fills that decide
// which filters and encoders the command names.
func composedLiveRendition(rung Rung, audio ffmpeg.AudioProfile, adjust ffmpeg.AudioAdjust) ffmpeg.Rendition {
	return ffmpeg.Rendition{
		Height:        rung.Height,
		VideoKbps:     rung.VideoKbps,
		AudioCodec:    audio.Codec,
		AudioChannels: audio.Channels,
		AudioGainDB:   audio.GainDB,
	}.WithAudioAdjust(adjust)
}

// liveComposedRung is the live composed shape a client may be sent: H.264 that
// no matched quirk denies, under the same limits the cached composed path
// applies (composedRenditionCompatible) — the client's declared H.264 maximum
// height and bitrate, and the quirk height and bitrate caps, each bitrate held
// against the rung's whole stream rate (video plus the audio and mux
// allowance). ok is false when no composed shape fits.
func liveComposedRung(profile CapabilityProfile, ov quirkOverlay) (Rung, bool) {
	if ov.deniesVideoCodec(composedCodec) {
		return Rung{}, false
	}
	maxHeight, maxBitrate := 0, ov.MaxBitrate
	if len(profile.VideoCodecs) > 0 {
		cap, ok := videoCodecCap(profile.VideoCodecs, composedCodec)
		if !ok {
			return Rung{}, false
		}
		maxHeight = cap.MaxHeight
		maxBitrate = minNonZero(maxBitrate, cap.MaxBitrate)
	} else {
		maxHeight = clientLadderCeiling(profile)
	}
	return composedLiveRungWithin(minNonZero(maxHeight, ov.MaxHeight), maxBitrate)
}

// composedDemandPriority is the job priority of a composed render queued by a
// live play: above the Schedule sweep's default, so the song just played is
// rendered ahead of the backlog and its next play is precomputed.
const composedDemandPriority = 10

// SetComposedCacheRoot names the precompute cache directory a live composed
// play queues its precomputed rendition under. Empty (the default) queues
// nothing.
func (s *Service) SetComposedCacheRoot(root string) {
	s.composedCache = root
}

// queueComposedRendition queues the Media's composed rendition at demand
// priority after a live composed play, unless a rendition row for its current
// picture already exists. A render the sweep
// already queued is raised to demand priority instead of duplicated.
func (s *Service) queueComposedRendition(ctx context.Context, mediaID store.MediaID) error {
	req, _, ok, err := composedRequest(ctx, s.store, mediaID, s.composer, s.composedCache)
	if err != nil || !ok {
		return err
	}
	// Same rule as EnsureComposedRendition: any rendition row for the current
	// picture means generation already ran or is running.
	if _, err := s.store.PlaybackRendition(ctx, req.RenditionID, store.LibraryFilter{AllLibraries: true, Revealed: true}); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrPlaybackRenditionNotFound) {
		return err
	}
	req.Priority = composedDemandPriority
	job, err := RequestPrecomputeRendition(ctx, s.store, req, s.now())
	if err != nil {
		return err
	}
	return s.store.RaiseQueuedJobPriority(ctx, job.ID, composedDemandPriority)
}

func hasComposedRendition(renditions []store.PlaybackRendition, fingerprint string) bool {
	for _, r := range renditions {
		if r.RungID == ComposedRungID && r.ContentFingerprint == fingerprint {
			return true
		}
	}
	return false
}

// profileTakesComposedVideo reports whether a client can play the composed
// rendition's H.264 video. An audio-only client (no video codecs declared)
// keeps direct play of the bare audio.
func profileTakesComposedVideo(profile CapabilityProfile) bool {
	if len(profile.VideoCodecs) > 0 {
		_, ok := videoCodecCap(profile.VideoCodecs, composedCodec)
		return ok
	}
	return containsFold(profile.DirectCodecs, composedCodec)
}

// composedRenditionCompatible is precomputedRenditionCompatible without the
// source-video checks: a composed picture never comes from the source, so the
// source's (absent) video signal says nothing about it.
func composedRenditionCompatible(r store.PlaybackRendition, profile CapabilityProfile, wantedAudio ffmpeg.AudioProfile, ov quirkOverlay) bool {
	if r.Kind != store.PlaybackRenditionKindHLS || r.Status != store.PlaybackRenditionReady || r.RungID != ComposedRungID {
		return false
	}
	if ov.vetoesRendition(r) || !precomputedAudioCompatible(wantedAudio) {
		return false
	}
	if len(profile.VideoCodecs) > 0 {
		// The same envelope the live composed shapes are chosen within
		// (liveComposedRung): height and the whole peak bandwidth.
		cap, ok := videoCodecCap(profile.VideoCodecs, r.Codec)
		return ok && (cap.MaxHeight <= 0 || r.Height <= cap.MaxHeight) &&
			(cap.MaxBitrate <= 0 || r.Bandwidth <= cap.MaxBitrate)
	}
	ceiling := clientLadderCeiling(profile)
	return ceiling <= 0 || r.Height <= ceiling
}

// withComposedPeak holds a Ready composed rendition to its whole peak. A row
// stored before CORE-1804 recorded the bare video maxrate as its bandwidth;
// the encode was the same, so it is admitted and advertised at the same total
// as one stored now.
func withComposedPeak(r store.PlaybackRendition) store.PlaybackRendition {
	if r.RungID == ComposedRungID && r.Bandwidth < composedBandwidth {
		r.Bandwidth = composedBandwidth
	}
	return r
}

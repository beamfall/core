package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

	composedCodec     = "h264"
	composedHeight    = 720
	composedBandwidth = 1_500_000
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

func (p ComposedPicture) validate() error {
	if (p.GraphicsPath == "") == (p.TimedText == "") {
		return errors.New("composed picture sets exactly one of graphics or timed text")
	}
	return nil
}

// digest identifies the picture's content so an edited sidecar yields a new
// rendition: timed text hashes its document; a graphics track hashes its path,
// size, and modification time (reading a whole track per playbackInfo would
// cost more than the change detection is worth).
func (p ComposedPicture) digest() (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	h := sha256.New()
	if p.TimedText != "" {
		fmt.Fprintf(h, "timed-text\x00%s", p.TimedText)
	} else {
		info, err := os.Stat(p.GraphicsPath)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "graphics\x00%s\x00%d\x00%d", p.GraphicsPath, info.Size(), info.ModTime().UnixNano())
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
func PrecomputeComposedRendition(ctx context.Context, st *store.Store, mediaID store.MediaID, composer PictureComposer, cacheRoot string, now time.Time) ([]store.Job, error) {
	req, ok, err := composedRequest(ctx, st, mediaID, composer, cacheRoot)
	if err != nil || !ok {
		return nil, err
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
func EnsureComposedRendition(ctx context.Context, st *store.Store, mediaID store.MediaID, composer PictureComposer, cacheRoot string, now time.Time) (bool, error) {
	req, ok, err := composedRequest(ctx, st, mediaID, composer, cacheRoot)
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
	if _, err := RequestPrecomputeRendition(ctx, st, req, normalizePrecomputeTime(now)); err != nil {
		return false, err
	}
	return true, nil
}

// composedRequest resolves the precompute request for one Media's composed
// picture. ok is false when the Media is not a candidate, its Library is
// encrypted, or the composer declines.
func composedRequest(ctx context.Context, st *store.Store, mediaID store.MediaID, composer PictureComposer, cacheRoot string) (PrecomputeRequest, bool, error) {
	if st == nil {
		return PrecomputeRequest{}, false, errors.New("store is required")
	}
	if composer == nil || mediaID == "" || strings.TrimSpace(cacheRoot) == "" {
		return PrecomputeRequest{}, false, nil
	}
	media, err := st.Media(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return PrecomputeRequest{}, false, err
	}
	if !composesPicture(media) {
		return PrecomputeRequest{}, false, nil
	}
	// Same fail-closed rule as every precomputed rendition.
	encrypted, err := st.LibraryEncrypted(ctx, media.LibraryID)
	if err != nil || encrypted {
		return PrecomputeRequest{}, false, err
	}
	files, err := st.MediaFilesForMedia(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return PrecomputeRequest{}, false, err
	}
	if len(files) == 0 {
		return PrecomputeRequest{}, false, errors.New("media has no source files")
	}
	source := files[0]
	picture, ok, err := composer(ctx, media, source.Path)
	if err != nil || !ok {
		return PrecomputeRequest{}, false, err
	}
	digest, err := picture.digest()
	if err != nil {
		return PrecomputeRequest{}, false, err
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
	}, true, nil
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
	proc, err := runner.StartBackgroundComposedHLS(ctx, spec.SourcePath, rungDir, input, ffmpeg.Rendition{
		Height:        spec.Height,
		VideoKbps:     spec.Bandwidth / 1000,
		AudioCodec:    "aac",
		AudioChannels: 2,
	})
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
// It answers ok=false — leaving the ladder untouched — whenever any condition
// fails, including a composer error: playback never breaks because a picture
// could not be resolved.
func (s *Service) composedChoice(ctx context.Context, scope auth.Scope, grant streamtoken.Grant, suffix string, media store.Media, file store.MediaFile, profile CapabilityProfile, wantedAudio ffmpeg.AudioProfile, expiresAt time.Time, ov quirkOverlay) (StreamChoice, bool, error) {
	if s.composer == nil || !composesPicture(media) || grant.BurnSubtitleIndex != 0 || !profileTakesComposedVideo(profile) {
		return StreamChoice{}, false, nil
	}
	renditions, err := s.store.ReadyPlaybackRenditions(ctx, media.ID, scope.LibraryFilter())
	if err != nil {
		return StreamChoice{}, false, err
	}
	// Only resolve the picture (which reads sidecars) when a composed
	// rendition could possibly match.
	if !hasComposedRendition(renditions, file.ContentFingerprint) {
		return StreamChoice{}, false, nil
	}
	picture, ok, err := s.composer(ctx, media, file.Path)
	if err != nil || !ok {
		return StreamChoice{}, false, nil
	}
	digest, err := picture.digest()
	if err != nil {
		return StreamChoice{}, false, nil
	}
	want := composedRenditionID(media.ID, file.ContentFingerprint, digest)
	for _, rendition := range renditions {
		if rendition.ID != want || !composedRenditionCompatible(rendition, profile, wantedAudio, ov) {
			continue
		}
		choice, err := s.precomputedStreamChoice(ctx, grant, suffix, rendition, expiresAt)
		return choice, err == nil, err
	}
	return StreamChoice{}, false, nil
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
		cap, ok := videoCodecCap(profile.VideoCodecs, r.Codec)
		return ok && (cap.MaxHeight <= 0 || r.Height <= cap.MaxHeight)
	}
	ceiling := clientLadderCeiling(profile)
	return ceiling <= 0 || r.Height <= ceiling
}

// Package skipdetect is Core's byte-level skip-segment detector (ENR-7 /
// ADR-0048 Decision 3/4/6): the transcode-preempted, post-scan-debounced job
// that emits `detected` skip-segments (intro / recap / credits) over the
// GENERIC relation graph. It NEVER names a domain container (the vocab
// firewall, invariant #1): "related Media" is the ordered children of a common
// container, walked via store.OrderedSiblingMedia.
//
// Three local methods, per Decision 4 and its 2026-08-12 amendment:
//
//   - chapter-import (deterministic, high-confidence, helps standalone files):
//     a titled embedded chapter ("Intro", "End Credits") maps to a segment by a
//     title heuristic.
//   - cross-Media audio fingerprinting (the series-shaped engine): a sibling's
//     long shared leading audio run becomes the `intro`; the shared trailing run
//     becomes `credits`. Confidence comes from the match strength and is gated —
//     below the auto-apply threshold the segment is dropped (Decision 5), never
//     surfaced.
//   - prior-Media reverse fingerprinting: short matches from the current
//     opening, scattered across multiple earlier ordered siblings, become a
//     `recap`. An opening text cue can corroborate this evidence but never emits
//     a segment by itself.
//
// All writes go through store.ReplaceDetectedSegment, which enforces manual-
// always-wins, suppression, and idempotency, so the detector itself never has
// to special-case those — it only decides WHAT to detect and at what confidence.
package skipdetect

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strings"
	"time"
	"unicode"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// Method names recorded on detected segments (the Decision-4 method vocabulary,
// also referenced by the 0063 provenance migration comment). Stored verbatim in
// the marker's method column.
const (
	MethodChapterImport      = "chapter-import"
	MethodFingerprint        = "fingerprint"
	MethodReverseFingerprint = "reverse-fingerprint"
)

// Confidence policy. Chapter-import is deterministic, so a
// titled-chapter match is high-confidence and always auto-applies. Fingerprint
// confidence is derived from the match strength and gated: a run below the
// threshold is dropped.
const (
	// chapterImportConfidence is the fixed high score for a titled-chapter
	// match — the source is the operator's own container metadata, not a guess.
	chapterImportConfidence = 0.95
	// fingerprintAutoApplyThreshold is the floor a fingerprint match's confidence
	// must clear to be written; below it the segment is dropped (Decision 5:
	// "auto-apply above a confidence threshold; below it, drop").
	fingerprintAutoApplyThreshold = 0.80
)

// Fingerprint analysis bounds. The detector compares only the candidate intro/
// credits regions — the head and tail of each file — not the whole runtime: an
// intro lives near the start, credits near the end. These bound the analysis
// length and the minimum shared run that counts as a segment.
const (
	// fingerprintHeadWindows is how many leading analysis windows (seconds) the
	// intro search compares across siblings.
	fingerprintHeadWindows = 180
	// fingerprintTailWindows is how many trailing windows the credits search
	// compares (credits + theme typically run longer than an intro).
	fingerprintTailWindows = 240
	// minSharedWindows is the shortest shared run (seconds) that counts as a
	// real segment — a couple of incidentally-similar seconds is not an intro.
	minSharedWindows = 15
	// maxIntroWindows caps an intro (seconds). A longer shared opening is not an
	// intro but near-identical audio (a duplicate encode, a constant audio bed),
	// and auto-skipping it would jump past real content.
	maxIntroWindows = 120
	// maxSegmentRuntimeFraction caps an intro or credits run as a share of the
	// Media's runtime, so a short file can never be skipped whole. With intro
	// anchored at 0 and credits at EOF, a cap of one half also keeps the two
	// from overlapping.
	maxSegmentRuntimeFraction = 0.5
	// windowMatchTolerance is the max per-window loudness difference (on the
	// [0,1] magnitude scale) for two windows to count as "the same" audio.
	windowMatchTolerance = 0.06
	// reverseFingerprintHeadWindows bounds the opening region that can be a
	// recap. Prior sibling signatures are not bounded to their heads.
	reverseFingerprintHeadWindows = 180
	// Reverse matching is stricter than aligned method (b): searching an entire
	// file creates far more collision opportunities than comparing two heads.
	reverseWindowMatchTolerance = 0.015
	// reverseMatchMinWindows rejects score/sting collisions;
	// reverseMatchMaxWindows keeps the evidence shaped as short reused clips.
	reverseMatchMinWindows    = 4
	reverseCueMatchMinWindows = 3
	reverseMatchMaxWindows    = 8
	// reverseOriginScatterWindows requires evidence to come from materially
	// different points in the prior material, not one recurring aligned cue.
	reverseOriginScatterWindows  = 15
	reverseMinCoverageWindows    = 12
	reverseCueMinCoverageWindows = 6
	reverseMinDensity            = 0.65
	reverseCueMinDensity         = 0.50
	reverseMaxStartWindows       = 30
	reverseCoverageSaturation    = 12
	recapCueMaxStartSeconds      = 45
	recapCueConfidenceBonus      = 0.25
)

// Spectral sibling matching (CORE-3111). The spectral signature carries one
// 32-bit sub-fingerprint per ffmpeg.SpectralFrameSeconds; matching searches
// every relative offset at frame resolution and scores whole windows, so a
// shared intro is found when a cold open moves it, and boundaries stay
// whole-second like the loudness path.
const (
	// spectralFramesPerWindow groups sub-fingerprints into one analysis window.
	spectralFramesPerWindow = int(ffmpeg.FingerprintWindowSeconds / ffmpeg.SpectralFrameSeconds)
	// spectralFrameMatchBits is the max Hamming distance for two detailed
	// sub-fingerprints to agree. Re-encodes and small shifts of one sound stay
	// under 2 bits; unrelated frames average spectralChanceBits, and fewer than
	// 3% of them fall this close.
	spectralFrameMatchBits = 10
	// spectralChanceBits is the mean distance between unrelated sub-fingerprints
	// (half of 32 bits). A spectral run's confidence measures its distance below
	// chance, so a run near the match limit scores below the auto-apply bar.
	spectralChanceBits = 16
	// spectralDetailBits is the fewest set (and unset) bits a sub-fingerprint
	// needs to describe a sound. A pure tone sets about one bit and a smooth
	// broadband slope sets nearly all of them, so two different tones, or two
	// different noises, sit a few bits apart. Such frames match only when
	// identical; any difference counts as chance distance.
	spectralDetailBits = 4
	// spectralMinWindowFrames is the fewest agreeing frame pairs a window needs
	// to match, and the fewest disagreeing pairs it needs to diverge.
	spectralMinWindowFrames = 2
)

// Detector is skipdetect's slice of the FFmpeg seam (interface at the consumer,
// per the modular-monolith seam rule; *ffmpeg.Runner implements it). Both calls
// are read-only and routed through the hardened spawn seam inside the Runner.
type Detector interface {
	// Chapters reads the container's embedded titled chapters.
	Chapters(ctx context.Context, path string) ([]ffmpeg.Chapter, error)
	// AudioFingerprint returns a coarse per-window loudness signature over the
	// first windowCount windows of src's primary audio stream.
	AudioFingerprint(ctx context.Context, src string, windowCount int) ([]float64, error)
}

// tailFingerprinter is an optional detector capability kept separate from
// Detector so existing chapter-only adapters do not need to implement it.
type tailFingerprinter interface {
	AudioFingerprintTail(ctx context.Context, src string, windowCount, durationSeconds int) ([]float64, error)
}

// spectralFingerprinter is an optional detector capability. When present, the
// sibling intro and credits search compares spectral sub-fingerprints at frame
// resolution instead of loudness windows, because siblings often share their
// loudness curve without sharing their sound (CORE-3111). The reverse recap
// search still reads the loudness signature.
type spectralFingerprinter interface {
	SpectralFingerprint(ctx context.Context, src string, seconds int) ([]uint32, error)
	SpectralFingerprintTail(ctx context.Context, src string, seconds, durationSeconds int) ([]uint32, error)
}

// openingTextCueDetector is optional so chapter/fingerprint-only adapters keep
// working. The FFmpeg Runner implements it for embedded text subtitles; a
// transcript-backed adapter can provide the same bounded cue shape.
type openingTextCueDetector interface {
	OpeningTextCues(ctx context.Context, src string, maxSeconds int) ([]ffmpeg.TextCue, error)
}

// Service runs skip-segment detection for one Media at a time. It owns no
// storage of its own — detected segments are Markers, written through the
// store's manual-respecting ReplaceDetectedSegment.
type Service struct {
	store    *store.Store
	detector Detector
	now      func() time.Time
}

// NewService builds the detector. now defaults to time.Now (UTC applied at
// write); a test injects a fixed clock.
func NewService(st *store.Store, detector Detector) *Service {
	return &Service{store: st, detector: detector, now: func() time.Time { return time.Now().UTC() }}
}

// serverFilter is the detector's server-internal read scope: it sees every
// Library (reveal filtering is a serving-boundary concern), mirroring genassets.
var serverFilter = store.LibraryFilter{Revealed: true, AllLibraries: true}

// DetectForMedia runs both detection methods for one Media, honoring the
// gates that make detection safe to enqueue blindly:
//
//   - video-only: a non-video Media is a no-op;
//   - per-Library toggle default-off (Decision 6): a Library that has not opted
//     in is a no-op — NO detection runs and NO segment is written;
//   - a Media with no known duration or no primary file is a no-op.
//
// It is safe to call repeatedly: every write is idempotent and manual/
// suppression-respecting in the store. A per-method failure is collected and
// returned (so the sweep can count it) but never aborts the other method.
func (s *Service) DetectForMedia(ctx context.Context, id store.MediaID) error {
	if s.detector == nil {
		return errors.New("no detector configured")
	}
	media, err := s.store.Media(ctx, id, serverFilter)
	if err != nil {
		return fmt.Errorf("loading media %s: %w", id, err)
	}
	// Video-only (Decision 6): audio (podcast intro/ad skipping) is deferred,
	// music never applies, images have no timeline.
	if media.Class != store.MediaClassVideo || media.DurationSeconds <= 0 {
		return nil
	}
	// Per-Library toggle, default OFF (Decision 6): the operator's "this content
	// is series-like" assertion is the only signal that turns detection on.
	enabled, err := s.store.SkipDetectionEnabled(ctx, media.LibraryID)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	file, err := s.store.PrimaryMediaFile(ctx, id, serverFilter)
	if err != nil || file.Path == "" {
		return nil
	}

	var errs []error
	if err := s.importChapters(ctx, id, file.Path); err != nil {
		errs = append(errs, fmt.Errorf("chapter import: %w", err))
	}
	if err := s.fingerprintSiblings(ctx, media, file.Path); err != nil {
		errs = append(errs, fmt.Errorf("fingerprint: %w", err))
	}
	return errors.Join(errs...)
}

// importChapters maps the container's titled chapters to detected segments
// (Decision 4 method (a)). A chapter whose title matches a kind heuristic
// becomes a high-confidence `detected` segment of that kind; the first chapter
// matching each kind wins (a file rarely has two intros). Untitled or
// unrecognised chapters are ignored. Deterministic and cheap — the only method
// that helps standalone films.
func (s *Service) importChapters(ctx context.Context, id store.MediaID, path string) error {
	chapters, err := s.detector.Chapters(ctx, path)
	if err != nil {
		return err
	}
	written := map[string]bool{}
	var errs []error
	for _, chapter := range chapters {
		kind := kindFromChapterTitle(chapter.Title)
		if kind == "" || written[kind] {
			continue
		}
		if chapter.EndSeconds <= chapter.StartSeconds {
			continue
		}
		written[kind] = true
		if _, err := s.store.ReplaceDetectedSegment(ctx, id, kind,
			chapter.StartSeconds, chapter.EndSeconds,
			MethodChapterImport, chapterImportConfidence, store.FlavorNone, s.now()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// fingerprintSiblings compares this Media's candidate intro/credits regions to
// its ordered siblings' (Decision 4 method (b)). A long shared LEADING audio run
// across a sibling is the intro; a long shared TRAILING run is the credits. The
// match strength (run length and tightness) sets the confidence; a match below
// the auto-apply threshold is dropped. A Media with no siblings (a standalone
// film) yields nothing here — that is correct, not an error.
//
// Analysis is staged before any write: a detector error, store read error or
// context cancellation returns with the prior detector-owned markers intact.
// Only a completed analysis reconciles, replacing both fingerprint methods'
// rows with the new (possibly empty) result.
func (s *Service) fingerprintSiblings(ctx context.Context, media store.Media, path string) error {
	result, err := s.analyzeFingerprints(ctx, media, path)
	if err != nil {
		return err
	}
	return s.reconcileFingerprints(ctx, media.ID, result)
}

// fingerprintResult is one completed fingerprint analysis, staged before any
// marker is written.
type fingerprintResult struct {
	intro, credits, recap          segment
	hasIntro, hasCredits, hasRecap bool
}

func (s *Service) analyzeFingerprints(ctx context.Context, media store.Media, path string) (fingerprintResult, error) {
	var result fingerprintResult
	siblings, err := s.store.OrderedSiblingMedia(ctx, media.ID)
	if err != nil {
		return result, err
	}
	if len(siblings) == 0 {
		return result, ctx.Err()
	}
	headSelf, err := s.detector.AudioFingerprint(ctx, path, fingerprintHeadWindows)
	if err != nil {
		return result, err
	}
	// Best shared run across any sibling, per kind. Comparing against the
	// closest sibling (not every pair) is enough: a shared intro recurs across
	// all siblings, so one strong match is the signal.
	limits := siblingRunLimits{
		intro:   runLimits{maxWindows: maxIntroWindows, durationSeconds: media.DurationSeconds},
		credits: runLimits{durationSeconds: media.DurationSeconds, offsetSeconds: creditsOffset(media.DurationSeconds), closing: true},
	}
	var bestIntro, bestCredits sharedRun
	if spectral, ok := s.detector.(spectralFingerprinter); ok {
		bestIntro, bestCredits, err = bestSiblingRuns(ctx, s.store, media, path, siblings, limits, siblingSignatures[uint32]{
			head: func(src string) ([]uint32, error) {
				return spectral.SpectralFingerprint(ctx, src, fingerprintHeadWindows)
			},
			tail: func(src string, durationSeconds int) ([]uint32, error) {
				return spectral.SpectralFingerprintTail(ctx, src, fingerprintTailWindows, durationSeconds)
			},
			leading:  spectralSharedRun,
			trailing: spectralSharedRun,
		})
	} else if tailDetector, ok := s.detector.(tailFingerprinter); ok {
		bestIntro, bestCredits, err = bestSiblingRuns(ctx, s.store, media, path, siblings, limits, siblingSignatures[float64]{
			head: func(src string) ([]float64, error) {
				// This Media's head is already read for the recap search.
				if src == path {
					return headSelf, nil
				}
				return s.detector.AudioFingerprint(ctx, src, fingerprintHeadWindows)
			},
			tail: func(src string, durationSeconds int) ([]float64, error) {
				return tailDetector.AudioFingerprintTail(ctx, src, fingerprintTailWindows, durationSeconds)
			},
			leading:  anyLimits(sharedLeadingRun),
			trailing: anyLimits(sharedTrailingRun),
		})
	} else {
		err = errors.New("skipdetect: detector does not support EOF-aligned audio fingerprints")
	}
	if err != nil {
		return result, err
	}
	// A per-sibling failure is skipped, but a cancellation that caused it would
	// make the result look empty: never reconcile a canceled analysis.
	if err := ctx.Err(); err != nil {
		return result, err
	}

	result.intro, result.hasIntro = bestIntro.segment(0)
	result.credits, result.hasCredits = bestCredits.segment(creditsOffset(media.DurationSeconds))
	prior, err := s.store.PriorOrderedSiblingMedia(ctx, media.ID)
	if err != nil {
		return result, err
	}
	if len(prior) < 2 {
		return result, nil
	}
	cue, err := s.recapCue(ctx, path)
	if err != nil {
		return result, err
	}
	recap, ok := s.reverseFingerprint(ctx, headSelf, prior, cue)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !ok {
		return result, nil
	}
	if result.hasIntro && overlaps(recap, result.intro) {
		return result, nil
	}
	if result.hasCredits && overlaps(recap, result.credits) {
		return result, nil
	}
	existing, err := s.store.MarkersForMedia(ctx, media.ID, serverFilter)
	if err != nil {
		return result, err
	}
	if hasNonReverseRecap(existing) {
		return result, nil
	}
	result.recap, result.hasRecap = recap, true
	return result, nil
}

// siblingSignatures reads one signature type for the sibling intro and credits
// search: the loudness windows or the spectral sub-fingerprints.
type siblingSignatures[T any] struct {
	head              func(src string) ([]T, error)
	tail              func(src string, durationSeconds int) ([]T, error)
	leading, trailing func(self, sibling []T, limits runLimits) sharedRun
}

// anyLimits adapts an anchored loudness matcher, which finds one run per
// sibling pair and leaves the limits to the caller.
func anyLimits(match func(self, sibling []float64) sharedRun) func(self, sibling []float64, limits runLimits) sharedRun {
	return func(self, sibling []float64, _ runLimits) sharedRun { return match(self, sibling) }
}

// siblingRunLimits holds the plausibility bounds for each search.
type siblingRunLimits struct {
	intro, credits runLimits
}

// bestSiblingRuns returns the longest plausible intro and credits runs shared
// with any sibling. A sibling whose file or signature cannot be read is
// skipped; this Media's own signature failing fails the analysis.
func bestSiblingRuns[T any](ctx context.Context, st *store.Store, media store.Media, path string, siblings []store.MediaID, limits siblingRunLimits, sigs siblingSignatures[T]) (intro, credits sharedRun, err error) {
	headSelf, err := sigs.head(path)
	if err != nil {
		return intro, credits, err
	}
	tailSelf, err := sigs.tail(path, media.DurationSeconds)
	if err != nil {
		return intro, credits, err
	}
	for _, siblingID := range siblings {
		siblingFile, err := st.PrimaryMediaFile(ctx, siblingID, serverFilter)
		if err != nil || siblingFile.Path == "" {
			continue
		}
		headSib, err := sigs.head(siblingFile.Path)
		if err != nil {
			continue
		}
		if run := sigs.leading(headSelf, headSib, limits.intro); run.plausible(limits.intro) && run.beats(intro) {
			intro = run
		}
		sibling, err := st.Media(ctx, siblingID, serverFilter)
		if err != nil || sibling.DurationSeconds <= 0 {
			continue
		}
		tailSib, err := sigs.tail(siblingFile.Path, sibling.DurationSeconds)
		if err != nil {
			continue
		}
		if run := sigs.trailing(tailSelf, tailSib, limits.credits); run.plausible(limits.credits) && run.beats(credits) {
			credits = run
		}
	}
	return intro, credits, nil
}

// reconcileFingerprints replaces the detector-owned fingerprint rows with a
// completed analysis. Clearing both methods first also invalidates
// pre-COREREV-12 MethodFingerprint credits computed from file heads. Manual
// rows and suppression locks are preserved by the store reconciliation seams.
func (s *Service) reconcileFingerprints(ctx context.Context, id store.MediaID, result fingerprintResult) error {
	if err := s.store.ReplaceDetectedMarkers(ctx, id, MethodFingerprint, nil, serverFilter, s.now()); err != nil {
		return err
	}
	if err := s.store.ReplaceDetectedMarkers(ctx, id, MethodReverseFingerprint, nil, serverFilter, s.now()); err != nil {
		return err
	}
	var errs []error
	write := func(kind string, seg segment, method string) {
		if _, err := s.store.ReplaceDetectedSegment(ctx, id, kind,
			seg.start, seg.end, method, seg.confidence, store.FlavorNone, s.now()); err != nil {
			errs = append(errs, err)
		}
	}
	if result.hasIntro {
		write("intro", result.intro, MethodFingerprint)
	}
	if result.hasCredits {
		write("credits", result.credits, MethodFingerprint)
	}
	if result.hasRecap {
		write("recap", result.recap, MethodReverseFingerprint)
	}
	return errors.Join(errs...)
}

type reverseMatch struct {
	selfStart   int
	selfEnd     int
	originStart int
	siblingID   store.MediaID
}

type recapCue struct {
	found      bool
	start, end float64
}

func (s *Service) reverseFingerprint(ctx context.Context, opening []float64, prior []store.MediaID, cue recapCue) (segment, bool) {
	var matches []reverseMatch
	minMatchWindows := reverseMatchMinWindows
	if cue.found {
		minMatchWindows = reverseCueMatchMinWindows
	}
	for _, siblingID := range prior {
		sibling, err := s.store.Media(ctx, siblingID, serverFilter)
		if err != nil || sibling.DurationSeconds <= 0 {
			continue
		}
		file, err := s.store.PrimaryMediaFile(ctx, siblingID, serverFilter)
		if err != nil || file.Path == "" {
			continue
		}
		windowCount := int(math.Ceil(float64(sibling.DurationSeconds) / ffmpeg.FingerprintWindowSeconds))
		full, err := s.detector.AudioFingerprint(ctx, file.Path, windowCount)
		if err != nil {
			continue
		}
		matches = append(matches, backwardMatches(opening, full, siblingID, minMatchWindows)...)
	}
	return recapFromMatches(matches, len(opening), cue)
}

func backwardMatches(opening, origin []float64, siblingID store.MediaID, minMatchWindows int) []reverseMatch {
	var matches []reverseMatch
	for selfStart := 0; selfStart < len(opening); {
		bestOrigin, bestWindows := -1, 0
		for originStart := 0; originStart < len(origin); originStart++ {
			windows := matchingWindows(opening[selfStart:], origin[originStart:], reverseMatchMaxWindows, reverseWindowMatchTolerance)
			if windows > bestWindows {
				bestOrigin, bestWindows = originStart, windows
			}
		}
		if bestWindows < minMatchWindows {
			selfStart++
			continue
		}
		matches = append(matches, reverseMatch{
			selfStart: selfStart, selfEnd: selfStart + bestWindows,
			originStart: bestOrigin, siblingID: siblingID,
		})
		selfStart += bestWindows
	}
	return matches
}

func matchingWindows(a, b []float64, limit int, tolerance float64) int {
	n := min(len(a), len(b), limit)
	for i := 0; i < n; i++ {
		if math.Abs(a[i]-b[i]) > tolerance {
			return i
		}
	}
	return n
}

func recapFromMatches(matches []reverseMatch, openingWindows int, cue recapCue) (segment, bool) {
	if len(matches) == 0 || openingWindows == 0 {
		return segment{}, false
	}
	covered := make([]bool, openingWindows)
	sources := make(map[store.MediaID]bool)
	first, last := openingWindows, 0
	minOrigin, maxOrigin := math.MaxInt, 0
	for _, match := range matches {
		sources[match.siblingID] = true
		minOrigin = min(minOrigin, match.originStart)
		maxOrigin = max(maxOrigin, match.originStart)
		first = min(first, match.selfStart)
		last = max(last, match.selfEnd)
		for i := match.selfStart; i < match.selfEnd && i < len(covered); i++ {
			covered[i] = true
		}
	}
	if len(sources) < 2 || maxOrigin-minOrigin < reverseOriginScatterWindows {
		return segment{}, false
	}
	if first > reverseMaxStartWindows && !cue.found {
		return segment{}, false
	}
	coverage := 0
	for _, window := range covered[first:last] {
		if window {
			coverage++
		}
	}
	density := float64(coverage) / float64(last-first)
	minCoverage, minDensity := reverseMinCoverageWindows, reverseMinDensity
	if cue.found {
		minCoverage, minDensity = reverseCueMinCoverageWindows, reverseCueMinDensity
	}
	if coverage < minCoverage || density < minDensity {
		return segment{}, false
	}
	coverageFactor := min(1, float64(coverage)/reverseCoverageSaturation)
	confidence := 0.65*density + 0.35*coverageFactor
	if cue.found {
		confidence = min(1, confidence+recapCueConfidenceBonus)
	}
	if confidence < fingerprintAutoApplyThreshold {
		return segment{}, false
	}
	start := float64(first) * ffmpeg.FingerprintWindowSeconds
	end := float64(last) * ffmpeg.FingerprintWindowSeconds
	if cue.found {
		start = min(start, cue.start)
		end = max(end, cue.end)
	}
	return segment{start: start, end: end, confidence: confidence}, true
}

func (s *Service) recapCue(ctx context.Context, path string) (recapCue, error) {
	detector, ok := s.detector.(openingTextCueDetector)
	if !ok {
		return recapCue{}, nil
	}
	cues, err := detector.OpeningTextCues(ctx, path, reverseFingerprintHeadWindows)
	if err != nil {
		return recapCue{}, err
	}
	for _, cue := range cues {
		if cue.StartSeconds < 0 {
			continue
		}
		if cue.EndSeconds <= cue.StartSeconds {
			continue
		}
		if cue.StartSeconds > recapCueMaxStartSeconds {
			continue
		}
		if recapCuePhrase(cue.Text) {
			return recapCue{found: true, start: cue.StartSeconds, end: cue.EndSeconds}, nil
		}
	}
	return recapCue{}, nil
}

func recapCuePhrase(text string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, text)
	normalized = " " + strings.Join(strings.Fields(normalized), " ") + " "
	for _, phrase := range []string{" previously on ", " last time on ", " earlier on "} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func overlaps(a, b segment) bool {
	return a.start < b.end && b.start < a.end
}

func hasNonReverseRecap(markers []store.Marker) bool {
	for _, marker := range markers {
		if marker.SegmentKind == "recap" && marker.Method != MethodReverseFingerprint {
			return true
		}
	}
	return false
}

// creditsOffset is where the analysed tail window 0 sits on the real timeline:
// the credits search compares the last fingerprintTailWindows seconds, so a run
// found at tail-window i maps to (duration - tailWindows + i) seconds. When the
// file is shorter than the tail window, the tail starts at 0.
func creditsOffset(durationSeconds int) float64 {
	offset := float64(durationSeconds) - fingerprintTailWindows*ffmpeg.FingerprintWindowSeconds
	if offset < 0 {
		return 0
	}
	return offset
}

// sharedRun is a matched run of windows starting at startWindow in this Media's
// analysed region, spanning windows windows long, with an average per-window
// tightness (0 = identical) that sets the confidence. compared is how many
// windows both signatures had to compare.
type sharedRun struct {
	startWindow int
	windows     int
	compared    int
	avgDiff     float64
	// diffScale is the avgDiff at which tightness reaches zero; 0 means
	// windowMatchTolerance (the loudness scale).
	diffScale float64
}

// runLimits bounds what a shared run may claim before it becomes a segment.
type runLimits struct {
	// maxWindows caps the run length; 0 leaves only the analysed region.
	maxWindows int
	// durationSeconds is this Media's runtime; 0 when unknown.
	durationSeconds int
	// offsetSeconds maps analysed window 0 to this Media's timeline.
	offsetSeconds float64
	// closing marks the credits search: its run must start in the second half
	// of the runtime, and an intro run must end in the first. Head and tail
	// regions overlap on a short file, and a spectral run is not anchored to
	// window 0 or EOF, so without this one shared segment could be claimed by
	// both searches, or an intro and credits from different siblings overlap.
	closing bool
}

// plausible reports whether run can be a real intro or credits segment. A run
// is rejected when it is longer than limits.maxWindows, when it covers more
// than maxSegmentRuntimeFraction of the runtime, or when it matched every
// window the two signatures had in common: then no boundary was ever seen, and
// the run is the whole short file or a near-duplicate sibling, not a segment
// whose end is known. Comparing best-of-siblings only among plausible runs
// keeps such a sibling from hiding a real segment found against another.
func (run sharedRun) plausible(limits runLimits) bool {
	if limits.maxWindows > 0 && run.windows > limits.maxWindows {
		return false
	}
	if run.windows >= run.compared {
		return false
	}
	if limits.durationSeconds <= 0 {
		return true
	}
	runtime := float64(limits.durationSeconds)
	seconds := float64(run.windows) * ffmpeg.FingerprintWindowSeconds
	if seconds > maxSegmentRuntimeFraction*runtime {
		return false
	}
	start := limits.offsetSeconds + float64(run.startWindow)*ffmpeg.FingerprintWindowSeconds
	if limits.closing {
		return start >= runtime/2
	}
	return start+seconds <= runtime/2
}

// beats reports whether run should replace best as the chosen candidate. A run
// that clears the auto-apply bar beats one that does not, so a longer loose run
// never hides a shorter trusted one; then the longer run wins, then the
// tighter.
func (run sharedRun) beats(best sharedRun) bool {
	if run.windows == 0 {
		return false
	}
	if trusted := run.trusted(); trusted != best.trusted() {
		return trusted
	}
	if run.windows != best.windows {
		return run.windows > best.windows
	}
	return run.avgDiff < best.avgDiff
}

// trusted reports whether run would become a segment (see segment).
func (run sharedRun) trusted() bool {
	return run.windows >= minSharedWindows && run.confidence() >= fingerprintAutoApplyThreshold
}

type segment struct {
	start, end, confidence float64
}

// segment converts a shared run into a timeline segment, applying the min-run
// and auto-apply-threshold gates. offsetSeconds maps analysed-window 0 to the
// real timeline (0 for the head/intro search; creditsOffset for the tail). ok is
// false when the run is too short or its confidence is below threshold — the
// caller drops it (Decision 5).
func (run sharedRun) segment(offsetSeconds float64) (segment, bool) {
	if run.windows < minSharedWindows {
		return segment{}, false
	}
	confidence := run.confidence()
	if confidence < fingerprintAutoApplyThreshold {
		return segment{}, false
	}
	start := offsetSeconds + float64(run.startWindow)*ffmpeg.FingerprintWindowSeconds
	end := start + float64(run.windows)*ffmpeg.FingerprintWindowSeconds
	return segment{start: start, end: end, confidence: confidence}, true
}

// confidenceLengthSaturation is the run length (windows) at which the length
// bonus is fully earned — a match this long or longer gets the full length
// credit. Set at 2× the minimum run so a clean match a little past the floor is
// already trusted, while tightness remains the dominant signal.
const confidenceLengthSaturation = minSharedWindows * 2

// confidence scores a shared run in [0,1]. Tightness (how closely the windows
// matched, scaled by the tolerance) is the dominant signal: a near-identical
// run is a real shared cue. Length adds a smaller bonus that saturates at
// confidenceLengthSaturation so a clean run just past the minimum is already
// trusted rather than penalised for not spanning the whole head window. A loose
// run scores low and is dropped by the auto-apply threshold.
func (run sharedRun) confidence() float64 {
	if run.windows == 0 {
		return 0
	}
	scale := run.diffScale
	if scale == 0 {
		scale = windowMatchTolerance
	}
	tightness := 1 - run.avgDiff/scale
	if tightness < 0 {
		tightness = 0
	}
	lengthFactor := float64(run.windows) / float64(confidenceLengthSaturation)
	if lengthFactor > 1 {
		lengthFactor = 1
	}
	score := 0.85*tightness + 0.15*lengthFactor
	if score > 1 {
		score = 1
	}
	return score
}

// sharedLeadingRun finds the longest run of consecutive matching windows
// starting at window 0 of both signatures — the shared cue both files open (or,
// for the tail signature, close) with. Two windows match when their loudness
// magnitudes differ by no more than windowMatchTolerance. The run ends at the
// first non-matching window; the average difference over the run sets its
// tightness. Comparing from window 0 is exactly right for an intro (both files
// open with the theme) and for credits when the tail signatures are aligned to
// the same trailing length.
func sharedLeadingRun(self, sibling []float64) sharedRun {
	n := len(self)
	if len(sibling) < n {
		n = len(sibling)
	}
	var matched int
	var sumDiff float64
	for i := 0; i < n; i++ {
		diff := math.Abs(self[i] - sibling[i])
		if diff > windowMatchTolerance {
			break
		}
		sumDiff += diff
		matched++
	}
	if matched == 0 {
		return sharedRun{}
	}
	return sharedRun{startWindow: 0, windows: matched, compared: n, avgDiff: sumDiff / float64(matched)}
}

// sharedTrailingRun finds the longest run ending at EOF in both signatures.
// Tail fingerprints are chronological, so credits occupy the suffix rather
// than window zero of the analysed region.
func sharedTrailingRun(self, sibling []float64) sharedRun {
	n := len(self)
	if len(sibling) < n {
		n = len(sibling)
	}
	var matched int
	var sumDiff float64
	for matched < n {
		diff := math.Abs(self[len(self)-1-matched] - sibling[len(sibling)-1-matched])
		if diff > windowMatchTolerance {
			break
		}
		sumDiff += diff
		matched++
	}
	if matched == 0 {
		return sharedRun{}
	}
	return sharedRun{startWindow: len(self) - matched, windows: matched, compared: n, avgDiff: sumDiff / float64(matched)}
}

// spectralSharedRun finds the best plausible run of matching windows that self
// and sibling share at any relative offset. Windows are spectralFramesPerWindow
// frames aligned to self's frame 0, so startWindow is a window of self's
// analysed region; the sibling side may sit at any frame. Each window matches,
// diverges or cannot be compared (spectralWindowDistance); a run is a maximal
// stretch of matching windows, and it needs minSharedWindows windows that match
// on fresh frames: a held chord may carry a run, but cannot make one. Only
// offsets with at least one diverging window yield candidates: there the two
// signatures provably differ, so what they share is a segment. An offset that
// never diverges shows only that the two agree where they can be compared.
// When such an offset shares more than the chosen run explains, the pair is one
// recording (a duplicate, perhaps padded with silence or repeating a passage)
// and yields nothing. The run explains the matches against a reprise of its
// audio on one file's side; every other match at that offset, the run meeting
// itself (a looped passage) included, is what a duplicate adds.
// compared is the number of windows the two signatures overlap at the run's
// offset. Only maximal runs are judged against limits, so a run too long to be
// a segment is never cut down to one; among those, beats picks.
func spectralSharedRun(self, sibling []uint32, limits runLimits) sharedRun {
	const fpw = spectralFramesPerWindow
	windows := len(self) / fpw
	var best sharedRun
	bestShift := 0
	var runs []sharedRun
	// matched holds, for each offset that never diverges, its fresh matching
	// windows: held frames one step off a slow theme's alignment still match,
	// but carry no shared change, so they are no evidence of a duplicate.
	type offsetMatches struct {
		shift   int
		windows []int
	}
	var unproven []offsetMatches
	// Self frame i pairs with sibling frame i-shift.
	for shift := -(len(sibling) - fpw); shift <= len(self)-fpw; shift++ {
		first := max(0, ceilDiv(shift, fpw))
		last := min(windows, floorDiv(len(sibling)+shift, fpw))
		if last-first < minSharedWindows {
			continue
		}
		runs = runs[:0]
		var matched []int
		diverged := false
		runStart, diffBits, fresh := first, 0, 0
		for w := first; w <= last; w++ {
			d, verdict := 0, windowIncomparable
			if w < last {
				d, verdict = spectralWindowDistance(self, sibling, w*fpw, w*fpw-shift)
				diverged = diverged || verdict == windowDiverges
			}
			if verdict == windowMatches || verdict == windowSustains {
				diffBits += d
				if verdict == windowMatches {
					fresh++
					matched = append(matched, w)
				}
				continue
			}
			if run := w - runStart; run >= minSharedWindows && fresh >= minSharedWindows {
				runs = append(runs, sharedRun{
					startWindow: runStart,
					windows:     run,
					compared:    last - first,
					avgDiff:     float64(diffBits) / float64(run*fpw),
					diffScale:   spectralChanceBits,
				})
			}
			runStart, diffBits, fresh = w+1, 0, 0
		}
		if !diverged {
			if len(matched) >= minSharedWindows {
				unproven = append(unproven, offsetMatches{shift, matched})
			}
			continue
		}
		for _, cand := range runs {
			if cand.plausible(limits) && cand.beats(best) {
				best, bestShift = cand, shift
			}
		}
	}
	if best.windows == 0 {
		return sharedRun{}
	}
	// The run's frames on each timeline; a window touching either is part of
	// the shared segment, not evidence of a duplicate.
	selfStart, selfEnd := best.startWindow*fpw, (best.startWindow+best.windows)*fpw
	sibStart, sibEnd := selfStart-bestShift, selfEnd-bestShift
	for _, om := range unproven {
		selfReprise, sibReprise := 0, 0
		for _, w := range om.windows {
			self, sib := w*fpw, w*fpw-om.shift
			inSelf := self+fpw > selfStart && self < selfEnd
			inSib := sib+fpw > sibStart && sib < sibEnd
			switch {
			case inSelf && !inSib:
				selfReprise++ // the run's audio in self against a reprise in sibling
			case inSib && !inSelf:
				sibReprise++
			}
		}
		if len(om.windows)-max(selfReprise, sibReprise) >= minSharedWindows {
			return sharedRun{}
		}
	}
	return best
}

// windowVerdict is how one window of two signatures compares.
type windowVerdict int

const (
	windowIncomparable windowVerdict = iota
	windowMatches
	// windowSustains matches only through held frames: no agreeing pair
	// changes on both sides.
	windowSustains
	windowDiverges
)

// spectralWindowDistance compares the spectralFramesPerWindow frames of self
// from frame i with those of sibling from frame j, returning the window's
// distance (scaled to the full window over its informative pairs) and its
// verdict. Each frame pair agrees, disagrees or is neutral:
//
//   - silent on both sides: neutral, as shared silence is not shared audio;
//   - silent on one side: disagrees;
//   - both detailed: agrees within spectralFrameMatchBits, else disagrees;
//   - otherwise (a low-detail tone or hum): agrees only when identical, as two
//     tones a few bits apart are different sounds;
//   - an agreeing low-detail pair whose frames both repeat their predecessors
//     is neutral instead: a held tone or a steady hum carries no timing, so
//     shared hum can neither make a window match nor hide its differences. Any
//     other agreeing pair in which either frame repeats its predecessor (a held
//     chord, or a steady sound against a moving one) still agrees, but is not
//     fresh: only a change both files make together shows shared timing.
//
// The window matches when at least spectralMinWindowFrames pairs agree and at
// most a quarter of its informative pairs disagree (a re-encode can clip a note
// edge to silence); it only sustains when none of those agreeing pairs is
// fresh. It diverges when at least
// spectralMinWindowFrames pairs disagree and they outnumber the agreeing ones.
// Otherwise it cannot be compared. Agreeing pairs add their Hamming distance (detailed) or nothing
// (identical); disagreeing pairs add spectralChanceBits.
func spectralWindowDistance(self, sibling []uint32, i, j int) (int, windowVerdict) {
	var total, agree, held, disagree int
	for k := range spectralFramesPerWindow {
		a, b := self[i+k], sibling[j+k]
		silentA, silentB := a == ffmpeg.SpectralSilentFrame, b == ffmpeg.SpectralSilentFrame
		var dist int
		switch {
		case silentA && silentB:
			continue
		case silentA || silentB:
			dist = spectralChanceBits
		case spectralFrameDetailed(a) && spectralFrameDetailed(b):
			dist = bits.OnesCount32(a ^ b)
			if dist > spectralFrameMatchBits {
				dist = spectralChanceBits
			}
		case a != b:
			dist = spectralChanceBits
		}
		if dist == spectralChanceBits {
			disagree++
			total += dist
			continue
		}
		// A frame with no predecessor shows no change, so it counts as held.
		repeatA := i+k == 0 || self[i+k-1] == a
		repeatB := j+k == 0 || sibling[j+k-1] == b
		if repeatA && repeatB && !spectralFrameDetailed(a) {
			continue
		}
		if repeatA || repeatB {
			held++
		}
		agree++
		total += dist
	}
	informative := agree + disagree
	switch {
	case agree >= spectralMinWindowFrames && 4*disagree <= informative && agree > held:
		return total * spectralFramesPerWindow / informative, windowMatches
	case agree >= spectralMinWindowFrames && 4*disagree <= informative:
		return total * spectralFramesPerWindow / informative, windowSustains
	case disagree >= spectralMinWindowFrames && disagree > agree:
		return total * spectralFramesPerWindow / informative, windowDiverges
	default:
		return 0, windowIncomparable
	}
}

// spectralFrameDetailed reports whether a sub-fingerprint describes a sound
// well enough to match: not silent, and with at least spectralDetailBits bits
// both set and unset. The silent hash has no bits set, so it fails too.
func spectralFrameDetailed(hash uint32) bool {
	set := bits.OnesCount32(hash)
	return set >= spectralDetailBits && 32-set >= spectralDetailBits
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func ceilDiv(a, b int) int {
	return -floorDiv(-a, b)
}

// kindFromChapterTitle maps a chapter title to a skip-segment kind by a
// case-insensitive substring heuristic, or "" when the title names no skip
// segment. The vocabulary is the closed three-kind set;
// the matched phrases are generic English cues an authoring tool stamps on a
// chapter, NOT domain vocabulary. "recap" and "previously on" both map to
// recap; "intro"/"opening"/"theme" to intro; "credits"/"ending" to credits.
func kindFromChapterTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" {
		return ""
	}
	switch {
	case strings.Contains(t, "recap"), strings.Contains(t, "previously on"):
		return "recap"
	case strings.Contains(t, "intro"), strings.Contains(t, "opening"), strings.Contains(t, "theme"):
		return "intro"
	case strings.Contains(t, "credits"), strings.Contains(t, "ending"):
		return "credits"
	default:
		return ""
	}
}

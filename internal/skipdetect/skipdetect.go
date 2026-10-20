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
func (s *Service) fingerprintSiblings(ctx context.Context, media store.Media, path string) error {
	// Remediation for pre-COREREV-12 output: old MethodFingerprint credits were
	// computed from file heads. Invalidate all detector-owned fingerprint rows
	// at the next sweep, then rebuild valid intro/credits below. Manual rows and
	// suppression locks are preserved by the store reconciliation seams.
	if err := s.store.ReplaceDetectedMarkers(ctx, media.ID, MethodFingerprint, nil, serverFilter, s.now()); err != nil {
		return err
	}
	if err := s.store.ReplaceDetectedMarkers(ctx, media.ID, MethodReverseFingerprint, nil, serverFilter, s.now()); err != nil {
		return err
	}
	siblings, err := s.store.OrderedSiblingMedia(ctx, media.ID)
	if err != nil {
		return err
	}
	if len(siblings) == 0 {
		return nil
	}
	headSelf, err := s.detector.AudioFingerprint(ctx, path, fingerprintHeadWindows)
	if err != nil {
		return err
	}
	tailDetector, ok := s.detector.(tailFingerprinter)
	if !ok {
		return errors.New("skipdetect: detector does not support EOF-aligned audio fingerprints")
	}
	tailSelf, err := tailDetector.AudioFingerprintTail(ctx, path, fingerprintTailWindows, media.DurationSeconds)
	if err != nil {
		return err
	}

	// Best shared run across any sibling, per kind. Comparing against the
	// closest sibling (not every pair) is enough: a shared intro recurs across
	// all siblings, so one strong match is the signal.
	var bestIntro, bestCredits sharedRun
	for _, siblingID := range siblings {
		siblingFile, err := s.store.PrimaryMediaFile(ctx, siblingID, serverFilter)
		if err != nil || siblingFile.Path == "" {
			continue
		}
		headSib, err := s.detector.AudioFingerprint(ctx, siblingFile.Path, fingerprintHeadWindows)
		if err != nil {
			continue
		}
		if run := sharedLeadingRun(headSelf, headSib); run.windows > bestIntro.windows {
			bestIntro = run
		}
		sibling, err := s.store.Media(ctx, siblingID, serverFilter)
		if err != nil || sibling.DurationSeconds <= 0 {
			continue
		}
		tailSib, err := tailDetector.AudioFingerprintTail(ctx, siblingFile.Path, fingerprintTailWindows, sibling.DurationSeconds)
		if err != nil {
			continue
		}
		if run := sharedTrailingRun(tailSelf, tailSib); run.windows > bestCredits.windows {
			bestCredits = run
		}
	}

	var errs []error
	intro, hasIntro := bestIntro.segment(0)
	if hasIntro {
		if _, err := s.store.ReplaceDetectedSegment(ctx, media.ID, "intro",
			intro.start, intro.end, MethodFingerprint, intro.confidence, store.FlavorNone, s.now()); err != nil {
			errs = append(errs, err)
		}
	}
	credits, hasCredits := bestCredits.segment(creditsOffset(media.DurationSeconds))
	if hasCredits {
		if _, err := s.store.ReplaceDetectedSegment(ctx, media.ID, "credits",
			credits.start, credits.end, MethodFingerprint, credits.confidence, store.FlavorNone, s.now()); err != nil {
			errs = append(errs, err)
		}
	}
	prior, err := s.store.PriorOrderedSiblingMedia(ctx, media.ID)
	if err != nil {
		errs = append(errs, err)
		return errors.Join(errs...)
	}
	if len(prior) < 2 {
		return errors.Join(errs...)
	}
	cue, cueErr := s.recapCue(ctx, path)
	if cueErr != nil {
		errs = append(errs, cueErr)
	}
	recap, ok := s.reverseFingerprint(ctx, headSelf, prior, cue)
	if !ok {
		return errors.Join(errs...)
	}
	if hasIntro && overlaps(recap, intro) {
		return errors.Join(errs...)
	}
	if hasCredits && overlaps(recap, credits) {
		return errors.Join(errs...)
	}
	existing, markerErr := s.store.MarkersForMedia(ctx, media.ID, serverFilter)
	if markerErr != nil {
		errs = append(errs, markerErr)
		return errors.Join(errs...)
	}
	if hasNonReverseRecap(existing) {
		return errors.Join(errs...)
	}
	if _, err := s.store.ReplaceDetectedSegment(ctx, media.ID, "recap",
		recap.start, recap.end, MethodReverseFingerprint, recap.confidence, store.FlavorNone, s.now()); err != nil {
		errs = append(errs, err)
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
// tightness (0 = identical) that sets the confidence.
type sharedRun struct {
	startWindow int
	windows     int
	avgDiff     float64
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
	tightness := 1 - run.avgDiff/windowMatchTolerance
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
	return sharedRun{startWindow: 0, windows: matched, avgDiff: sumDiff / float64(matched)}
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
	return sharedRun{startWindow: len(self) - matched, windows: matched, avgDiff: sumDiff / float64(matched)}
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

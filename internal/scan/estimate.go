package scan

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// First-scan estimate (OPS-4 / ADR-0013 §2: "the wizard estimates and says
// what's deferrable — play immediately; sprites arrive overnight"). The wizard
// shows it before the first scan, so it must be cheap: a single directory walk
// that counts and classifies by extension, with NO hashing, probing, or store
// access. It deliberately trades exactness for speed — it is a "what am I about
// to scan and how long roughly" wizard hint, never an authoritative figure.
//
// The estimate's whole point is the split it makes explicit: the inline scan
// work (discover → fingerprint → probe → import) is the only work that gates
// playability, and everything in DeferrableWork (the ADR-0033 Generated Asset
// family — trickplay sprites, preview clips, Marker thumbnails, waveform
// envelopes) is background work the asset-sweep job fills in afterward. Nothing
// in DeferrableWork blocks a Media item from playing.

// Per-file inline-cost model. The inline scan reads each file's bytes once for
// the content fingerprint and runs one FFmpeg probe — both roughly linear in
// file count at the wizard's resolution (file sizes are not walked; a size-
// weighted model is more than an estimate needs). The constant is a deliberately
// round per-file wall-clock figure on modest hardware; it scales linearly with
// the counted files so the wizard's "about N minutes" reads sensibly. It is a
// hint, not a contract — the resource governor's worker pool and the real disk
// throughput move the true number.
const inlineCostPerFile = 250 * time.Millisecond

// Deferrable-work kind vocabulary (invariant #1 — named by shape, never by
// domain). These mirror the ADR-0033 Generated Asset family the asset-sweep job
// produces. They are the generic asset-shape names the wizard renders, not
// media-domain words.
const (
	// DeferrableTrickplay is the trickplay sprite-sheet backlog (video only).
	DeferrableTrickplay = "trickplay-sprites"
	// DeferrablePreviewClip is the hover/scrub preview-clip backlog (video only).
	DeferrablePreviewClip = "preview-clips"
	// DeferrableMarkerThumbs is the Marker-thumbnail backlog (video only). It is
	// counted per video file as a presence estimate — the real per-Marker count
	// is unknowable before identification, so the wizard shows "video items that
	// may grow Marker thumbnails," not a thumbnail total.
	DeferrableMarkerThumbs = "marker-thumbnails"
	// DeferrableWaveform is the audio waveform-envelope backlog (audio only).
	DeferrableWaveform = "waveform-envelopes"
	// DeferrableSilenceMap is the audio silence-map backlog (audio only) —
	// FFmpeg silencedetect spans for trim-silence seeking (ADR-0052 §2.4(a)).
	DeferrableSilenceMap = "silence-maps"
)

// DeferrableWork is one kind of background asset-generation backlog the first
// scan will leave for the asset-sweep job. PlayabilityGating is
// always false for every Generated Asset: none of them block playback, which is
// the "play now; sprites overnight" promise made machine-readable.
type DeferrableWork struct {
	// Kind is the generic asset-shape name (the Deferrable* vocabulary above).
	Kind string `json:"kind"`
	// EstimatedCount is the number of files expected to need this asset kind,
	// derived from the cheap extension-based class split. It is an upper-bound
	// estimate: the asset-sweep skips items whose asset already exists or whose
	// Library has the kind toggled off, so the real backlog is never larger.
	EstimatedCount int `json:"estimatedCount"`
	// PlayabilityGating reports whether this work must finish before the media is
	// playable. Always false here — Generated Assets are derived, deferrable
	// enrichments, never a playback prerequisite.
	PlayabilityGating bool `json:"playabilityGating"`
}

// Estimate is the first-scan wizard's structured pre-scan projection.
// EstimatedFiles and EstimatedInlineDuration describe the must-do-now scan that
// gates playability; DeferrableWork enumerates the background asset backlog that
// does not. InlineWorkGatesPlayability is the explicit statement of the split:
// the inline scan is the only playability-gating work, so it is true, while
// every DeferrableWork entry is false.
type Estimate struct {
	// EstimatedFiles is the count of media-extension files the walk found —
	// the inline scan's unit of work. It applies the same allowlist, dotfile,
	// and Extra filters the real scan applies, so it counts what would import.
	EstimatedFiles int `json:"estimatedFiles"`
	// EstimatedVideoFiles / EstimatedAudioFiles split EstimatedFiles by the cheap
	// extension-based class guess (the real class comes from the probe; this is
	// the wizard estimate). They drive the per-kind deferrable counts.
	EstimatedVideoFiles int `json:"estimatedVideoFiles"`
	EstimatedAudioFiles int `json:"estimatedAudioFiles"`
	// EstimatedInlineSeconds is the projected wall-clock of the inline,
	// playability-gating scan (discover + fingerprint + probe + import). Seconds,
	// rounded, so the wire shape carries no sub-second noise the wizard would
	// drop anyway.
	EstimatedInlineSeconds int `json:"estimatedInlineSeconds"`
	// InlineWorkGatesPlayability is always true: the inline scan IS the work that
	// makes a Media item playable. It pairs with every DeferrableWork entry's
	// false to make the "play now; sprites overnight" split unambiguous.
	InlineWorkGatesPlayability bool `json:"inlineWorkGatesPlayability"`
	// DeferrableWork is the background asset-generation backlog the first scan
	// leaves behind — the work the wizard tells the operator arrives later. Never
	// nil (an empty library yields zero-count entries, not a null).
	DeferrableWork []DeferrableWork `json:"deferrableWork"`
}

// videoExtensions is the video subset of the scan's media allowlist — the
// estimate classifies a file as video vs audio by extension alone (the real
// class is the probe's job; this is a cheap wizard guess). Any allowlisted
// extension not here is audio. Keep in sync with mediaExtensions in scan.go.
var videoExtensions = map[string]bool{
	"mp4": true, "m4v": true, "mov": true, "mkv": true, "webm": true,
	"avi": true, "wmv": true, "asf": true, "ts": true, "m2ts": true,
	"mts": true, "vob": true, "ogv": true, "3gp": true, "3g2": true,
	"flv": true, "f4v": true, "rmvb": true, "divx": true,
}

// EstimateRoots walks the given Library roots once and projects the first-scan
// cost and deferrable backlog. It is cheap by construction: it stats
// directory entries and classifies by extension, never opening, hashing, or
// probing a file, so the wizard can call it interactively on a 40TB tree. An
// unreadable directory or file is skipped (best-effort, like the real scan's
// resilience), so a permission-denied subtree yields a smaller estimate rather
// than an error.
//
// It does NOT apply the file-stability window (a settling file still counts as
// future scan work) and does NOT consult the store — the wizard runs before any
// scan or even any Library row, so the estimate is filesystem-only.
func EstimateRoots(ctx context.Context, roots []string) (Estimate, error) {
	est := Estimate{InlineWorkGatesPlayability: true}
	for _, root := range roots {
		if err := estimateRoot(ctx, root, &est); err != nil {
			return Estimate{}, err
		}
	}
	est.EstimatedFiles = est.EstimatedVideoFiles + est.EstimatedAudioFiles
	est.EstimatedInlineSeconds = int((time.Duration(est.EstimatedFiles) * inlineCostPerFile).Round(time.Second).Seconds())
	// The deferrable breakdown mirrors the ADR-0033 Generated Asset family and
	// the asset-sweep's per-class generators (genassets.SweepHandler): video
	// items grow trickplay sprites, preview clips, and Marker thumbnails; audio
	// items grow waveform envelopes and silence maps. Every entry is non-gating —
	// the split the wizard surfaces. Always emitted (zero counts included) so the
	// wizard shows a stable shape rather than a list that appears and disappears.
	est.DeferrableWork = []DeferrableWork{
		{Kind: DeferrableTrickplay, EstimatedCount: est.EstimatedVideoFiles, PlayabilityGating: false},
		{Kind: DeferrablePreviewClip, EstimatedCount: est.EstimatedVideoFiles, PlayabilityGating: false},
		{Kind: DeferrableMarkerThumbs, EstimatedCount: est.EstimatedVideoFiles, PlayabilityGating: false},
		{Kind: DeferrableWaveform, EstimatedCount: est.EstimatedAudioFiles, PlayabilityGating: false},
		{Kind: DeferrableSilenceMap, EstimatedCount: est.EstimatedAudioFiles, PlayabilityGating: false},
	}
	return est, nil
}

// estimateRoot walks one root, applying the same media-extension, dotfile, and
// Extra filters the real scan applies (so the count reflects what would import,
// not raw file count), and classifies each kept file as video or audio by
// extension.
func estimateRoot(ctx context.Context, root string, est *Estimate) error {
	if root == "" {
		return nil
	}
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// Resilience: an unreadable directory must not abort the estimate.
			// Returning SkipDir on a directory error skips just that subtree; a
			// file-level error is swallowed by counting nothing for it.
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if skipErr := skipExcludedScanDirectory(entry.Name(), entry.IsDir()); skipErr != nil {
			return skipErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		path = filepath.Clean(path)
		// Same gates as the real scan, in the same order, minus the cost-bearing
		// ones (stability window, hashing, probing): an empty derived title is
		// junk, a non-allowlisted extension is non-media, and an Extra would
		// import but is not a discovery-surface Media — the estimate counts what
		// the wizard's "your library" figure should reflect.
		if titleFromPath(path) == "" {
			return nil
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if !mediaExtensions[ext] {
			return nil
		}
		if isExtraPath(path) {
			return nil
		}
		if videoExtensions[ext] {
			est.EstimatedVideoFiles++
		} else {
			est.EstimatedAudioFiles++
		}
		return nil
	})
	// A root that does not exist yet (the wizard may probe a path the operator
	// is still typing) is not an error — it estimates zero. Any other walk error
	// propagates.
	if walkErr != nil {
		if ctx.Err() != nil {
			return walkErr
		}
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		return walkErr
	}
	return nil
}

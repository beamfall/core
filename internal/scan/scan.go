// Package scan walks Library roots and imports generic Media files.
package scan

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// Prober is scan's slice of the FFmpeg seam (ADR-0016: interface at the
// consumer). A nil Prober, or a probe failure, falls back to path-derived
// metadata so scanning works on machines without FFmpeg.
type Prober interface {
	Probe(ctx context.Context, path string) (ffmpeg.Probe, error)
}

// Identifier runs the auto-apply identification pipeline
// for one freshly imported/changed Media item: above the plugin's per-plugin
// threshold it applies the top candidate autonomously (allowed because Trash is
// ON, invariant #6); below it the item lands in the Unmatched queue. Defined at
// the consumer so scan need not import internal/identify — the arch
// whitelist forbids that edge, and identify cannot import scan, so the
// composition root supplies the adapter that bridges the two. A nil Identifier
// (the default) leaves scan importing-only, the pre-PIPE-1 behaviour.
//
// fingerprint is the file's content fingerprint (the pin's key); class/title/year
// and the serialized-item ordinals are the parser's guess carried into the
// scorer. A pipeline error is best-effort: scan logs it and keeps walking rather
// than abort the Library scan on one identification failure.
type Identifier interface {
	AutoIdentify(ctx context.Context, mediaID store.MediaID, pluginID, fingerprint string, parsed ParsedMedia) error
}

// OutcomeIdentifier is an optional Identifier capability that also says what
// the pass did with the item. scan aggregates the outcomes that applied no
// match and reports them once per scan, so a Library whose items can never
// match (for example a plugin with no scraper for the parsed class) is not
// silent.
type OutcomeIdentifier interface {
	AutoIdentifyOutcome(ctx context.Context, mediaID store.MediaID, pluginID, fingerprint string, parsed ParsedMedia) (IdentifyOutcome, error)
}

// IdentifyOutcome is scan's view of one identification pass. Reason is ""
// when a match was applied and one of the Identify* reasons otherwise;
// ScraperID names the scraper searched, when one was.
type IdentifyOutcome struct {
	Reason    string
	ScraperID string
}

// Why an identification pass applied no match.
const (
	IdentifyQueuedBelowThreshold = "queued-below-threshold"
	IdentifyQueuedNoCandidates   = "queued-no-candidates"
	IdentifyQueuedManual         = "queued-manual"
	IdentifySkippedNoScraper     = "skipped-no-scraper"
	IdentifySkippedNoBundle      = "skipped-no-bundle"
	IdentifySkippedNoScorer      = "skipped-no-scorer"
	IdentifySkippedNotWired      = "skipped-not-wired"
	IdentifySkippedPinned        = "skipped-pinned"
)

// IdentifyGroup counts the items one scan left unidentified for the same
// plugin, scraper, parsed class and reason, with one example path.
type IdentifyGroup struct {
	Plugin  string `json:"plugin"`
	Scraper string `json:"scraper,omitempty"`
	Class   string `json:"class,omitempty"`
	Reason  string `json:"reason"`
	Count   int    `json:"count"`
	Example string `json:"example"`
}

// LocalExtractor is scan's consumer-side seam for local-file scraper kinds
// (ADR-0043 EXIF today). The composition root adapts plugin ScraperSpecs and
// scrape.Engine into this interface so scan stays free of a scrape import.
type LocalExtractor interface {
	ExtractLocal(ctx context.Context, class, root, path string) (LocalMetadata, error)
}

// LocalMetadata is the generic field payload a local extractor returns.
// Fields uses scraper-framework names (capture_date, camera_make, ...);
// ImpliedMediaFields uses already-declared Media field names stripped of their
// plugin "media." prefix. Entities carries plugin-declared Entity/Media links.
type LocalMetadata struct {
	Fields             map[string]any
	ImpliedMediaFields map[string]string
	Entities           []LocalEntity
}

// LocalEntity is one plugin-declared Entity/Media link a local extractor
// contributes, carried in LocalMetadata.Entities.
type LocalEntity struct {
	Kind        string
	Title       string
	Relation    string
	Cardinality string
	SortOrder   *int
}

// MetadataHydrationCapability declares the fixed Media shapes a plugin's local
// metadata hook accepts and the plugin-declared flag field used for idempotent
// backfill. Both values come from the active bundle; scan names no domain.
type MetadataHydrationCapability struct {
	MediaClasses []store.MediaClass
	MarkerField  string
	// MediaFieldsBounded is true when MediaFields names every text, number
	// or date Media field the hydrator writes (at the scan tier); otherwise
	// it may write any. The scan uses it to tell which older scan-tier values
	// the filename parser alone wrote (CORE-2554).
	MediaFieldsBounded bool
	MediaFields        []string
}

// Supports reports whether this capability accepts class.
func (c MetadataHydrationCapability) Supports(class store.MediaClass) bool {
	for _, supported := range c.MediaClasses {
		if supported == class {
			return true
		}
	}
	return false
}

// MetadataHydrationInput is the generic local Media shape passed immediately
// after ImportMediaFile. Metadata contains FFmpeg probe tags when available;
// the plugin adapter owns all interpretation into declared fields and Entities.
// ContentFingerprint is the file's content hash (empty when unknown), so the
// adapter can tell re-tagged bytes from an unchanged file on a rescan.
type MetadataHydrationInput struct {
	MediaID            store.MediaID
	LibraryID          store.LibraryID
	Class              store.MediaClass
	Path               string
	Metadata           map[string]string
	ContentFingerprint string
}

// MetadataHydrator is scan's consumer-side local metadata hook. A dedicated
// hook is required because a contribution may write both Media fields and the
// declared Entity/relation graph. The composition root validates and adapts
// plugin contributions so scan remains independent of plugin implementations.
type MetadataHydrator interface {
	Capability() MetadataHydrationCapability
	HydrateMetadata(ctx context.Context, input MetadataHydrationInput) error
}

const JobKind store.JobKind = "scan"

// A scan job yields after scanJobFileBatchSize checkpointed files or after
// scanJobRunBudget of work, whichever comes first, so other background jobs
// (a scheduled backup, another Library's scan) get a worker within about
// that long once the run reaches new work. The time bound matters when files
// are slow to import: at the tens of seconds a file that online
// identification can cost, a 100-file batch held a worker for most of an hour
// (CORE-2306). The budget starts at the run's first checkpoint, after the walk
// back to the resume point (CORE-2353).
const scanJobFileBatchSize = 100

var scanJobRunBudget = 2 * time.Minute

// scanJobClock times the run budget. Tests replace it.
var scanJobClock = time.Now

var errImageDerivation = errors.New("required image derivatives incomplete")

// mediaExtensions is the allowlist of file extensions the scanner accepts
// as potential media (master #43). Any file whose lower-cased extension is
// not in this set is silently skipped — it will never import .txt, .pdf,
// .zip, or other non-media junk from a folder like Downloads.
//
// The list covers the video, audio, and image containers the
// platform speaks. Extend when a new container family is added; removing an
// extension here hides already-imported files from future re-scans (they
// will be marked missing, not deleted). Image extensions live in
// imageExtensions below and are merged in by init so the class lookup and
// the allowlist never drift.
var mediaExtensions = map[string]bool{
	// Video containers
	"mp4":  true,
	"m4v":  true,
	"mov":  true,
	"mkv":  true,
	"webm": true,
	"avi":  true,
	"wmv":  true,
	"asf":  true,
	"ts":   true,
	"m2ts": true,
	"mts":  true,
	"vob":  true,
	"ogv":  true,
	"3gp":  true,
	"3g2":  true,
	"flv":  true,
	"f4v":  true,
	"rmvb": true,
	"divx": true,
	// Audio containers
	"mp3":  true,
	"flac": true,
	"aac":  true,
	"m4a":  true,
	"m4b":  true,
	"ogg":  true,
	"oga":  true,
	"opus": true,
	"wav":  true,
	"aiff": true,
	"aif":  true,
	"alac": true,
	"wma":  true,
	"ape":  true,
	"mka":  true,
}

// imageExtensions is the still-image container allowlist. A file
// with one of these extensions classifies as store.MediaClassImage — served
// as direct bytes, never probed for streams, never transcoded, and never
// given the video/audio Generated Assets. Classification is by extension, not
// by probe: a still has no audio/video stream shape for the probe's
// audio-vs-video branch to read, and an extension check costs nothing.
var imageExtensions = map[string]bool{
	"jpg":  true,
	"jpeg": true,
	"png":  true,
	"gif":  true,
	"webp": true,
	"heic": true,
	"heif": true,
	"cr2":  true,
	"nef":  true,
	"arw":  true,
	"dng":  true,
	"avif": true,
	"bmp":  true,
	"tif":  true,
	"tiff": true,
}

func init() {
	// Every image extension is also a scannable media extension; merging here
	// keeps the two maps from drifting.
	for ext := range imageExtensions {
		mediaExtensions[ext] = true
	}
}

// imageClassForPath reports whether path is an image-container file by its
// lower-cased extension. The scanner sets store.MediaClassImage
// for these before the probe runs, so the probe's audio-vs-video branch never
// reclassifies a still.
func imageClassForPath(path string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	return imageExtensions[ext]
}

var sampleScanFile = store.SampleMediaFileContentChecked

// stabilityWindow is the file-stability quiet period: a file
// whose mtime is newer than now-stabilityWindow is still being written (a rip
// or torrent arriving as a write burst, a copy in flight) and is skipped this
// pass so its still-streaming bytes are never fingerprinted or probed. The
// next scan picks it up once it has settled — safe because the scan Schedule
// is the correctness backstop, so a skipped-too-eagerly file is
// never lost, only deferred. Five seconds comfortably outlasts the Watcher's
// debounce quiet period (the cheap accelerator) while staying short enough
// that a freshly finished copy is imported on the next pass.
const stabilityWindow = 5 * time.Second

// probeTimeout is the per-file ffprobe budget. A single hung probe
// must not stall the scan job indefinitely; 30 s covers pathological containers
// while still bounding worst-case scan latency per file.
// Package-level var so tests can tighten the budget without a real ffprobe.
var probeTimeout = 30 * time.Second

// now lets tests pin the wall clock used for the file-stability check.
var now = time.Now

// Request describes a Library root scan.
type Request struct {
	LibraryID store.LibraryID
	Root      string
	// Backfill re-runs local metadata hydration for already-imported Media that
	// lacks the plugin-declared marker. A normal scan leaves it false.
	Backfill bool
}

// JobRequest describes a Library root scan to enqueue as a durable job.
type JobRequest struct {
	ID        store.JobID
	LibraryID store.LibraryID
	Root      string
	CreatedAt time.Time
	// Backfill — see Request.Backfill; carried into the durable job payload.
	Backfill bool
}

// jobPayload version history: v1 carried {library_id, root}; v2 adds the
// metadata-backfill flag. A v1 payload still loads — the absent field decodes
// as false, so an in-flight pre-upgrade scan job resumes as a normal scan.
type jobPayload struct {
	Version   int             `json:"version"`
	LibraryID store.LibraryID `json:"library_id"`
	Root      string          `json:"root"`
	Backfill  bool            `json:"backfill"`
}

type jobCheckpoint struct {
	Version       int    `json:"version"`
	Stage         string `json:"stage"`
	LastPath      string `json:"last_path,omitempty"`
	FilesSeen     int    `json:"files_seen"`
	TotalFiles    int    `json:"total_files,omitempty"`
	MediaImported int    `json:"media_imported"`
	// MissingCredentials carries Result.MissingCredentials across job
	// yields, so a resumed scan does not report them again (CORE-1101).
	MissingCredentials []string `json:"missing_credentials,omitempty"`
	// The scan summary counts likewise survive yields (CORE-2040).
	FilesAdded     int `json:"files_added,omitempty"`
	FilesChanged   int `json:"files_changed,omitempty"`
	MediaUnmatched int `json:"media_unmatched,omitempty"`
	// UnmatchedRecords lists the folder-level Unmatched records already
	// counted in MediaUnmatched, so a resumed scan counts each folder once.
	UnmatchedRecords []string `json:"unmatched_records,omitempty"`
	// VersionMedia lists the Version Media the scan walked so far; the
	// finished scan counts each one still Unmatched once, however its
	// Versions are spread across directories and passes (CORE-2194).
	VersionMedia     []string `json:"version_media,omitempty"`
	SplitVersionDirs []string `json:"split_version_dirs,omitempty"`
	// CountedVersionMedia lists the Version Media a finished walk already
	// counted, kept by the complete checkpoint: a job reclaimed after a crash
	// before it completed resumes from there and may walk a new Version of
	// one of them, which must not count it again (CORE-2196).
	CountedVersionMedia []string `json:"counted_version_media,omitempty"`
	// GroupMedia lists the Media of several files (Versions, parts) already
	// counted in MediaImported, so a resumed scan does not count them again.
	GroupMedia []string `json:"group_media,omitempty"`
	// Unidentified carries the identification outcomes that applied no
	// match across job yields, so the finished scan reports them once.
	Unidentified []IdentifyGroup `json:"unidentified,omitempty"`
}

// Result summarizes a completed scan.
type Result struct {
	FilesSeen     int
	MediaImported int
	LastPath      string
	Files         []ImportedFile
	// MissingCredentials lists, sorted, the provider credential refs
	// (scraper AuthRefs) that auto-identify needed but that are not
	// configured. Each is reported once per scan instead of failing every
	// file (CORE-1101).
	MissingCredentials []string
	// Unidentified groups the items identification left without a match,
	// sorted by plugin, scraper, class and reason.
	Unidentified []IdentifyGroup
	// FilesAdded and FilesChanged count the files this scan catalogued for the
	// first time and the ones it re-read because their size or mtime moved.
	// FilesMissing counts the files the post-walk pass marked missing, and
	// MediaUnmatched the items and refused folders this scan left in the
	// Unmatched queue (CORE-2040).
	FilesAdded     int
	FilesChanged   int
	FilesMissing   int
	MediaUnmatched int
	seenMedia      map[store.MediaID]bool
	totalFiles     int
	// unmatchedRecords holds the folder-level Unmatched records this scan
	// noted, true for the pending ones counted in MediaUnmatched (see
	// noteUnmatchedRecord).
	unmatchedRecords map[string]bool
	// versionMedia, countedVersionMedia and groupMedia back
	// jobCheckpoint.VersionMedia, CountedVersionMedia and GroupMedia.
	versionMedia        map[store.MediaID]bool
	countedVersionMedia map[store.MediaID]bool
	groupMedia          map[store.MediaID]bool
	// interrupted is the resume point of a scan stopped by cancellation.
	interrupted *jobCheckpoint
}

// ImportedFile records one file observed by a scan.
type ImportedFile struct {
	Path               string
	MediaID            store.MediaID
	ContentFingerprint string
}

type parsedPartImport struct {
	key           string
	imported      store.ImportMediaFile
	parsed        ParsedMedia
	refresh       store.ParserFieldRefresh
	markerTitle   string
	probeChapters []ffmpeg.ProbeChapter
	probed        bool
	// hydrate is set when the part is new or changed on disk, as a single
	// file must be to hydrate; tags are then its embedded metadata. The group
	// hydrates from its first part's.
	hydrate bool
	tags    map[string]string
}

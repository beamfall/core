package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/beamfall/core/internal/store"
)

// IncomingFolderName is the per-Library Incoming Folder convention (ADR-0020
// §4): a folder inside the Library root where ingest places payloads so the
// standard scan covers them. The leading dot keeps it visually distinct from
// curated content; the standard scanner walks it like any other directory.
const IncomingFolderName = ".incoming"

// SidecarSuffix is appended to a linked payload's path to name its Parse Hints
// sidecar. The standard identification pipeline can consume it to
// prefer the release name over payload filenames.
const SidecarSuffix = ".beamfall-ingest.json"

// IngestProvenanceSource is the provenance value stamped into every sidecar so
// the pipeline (and the operator) can tell where a file entered from.
const IngestProvenanceSource = "qbittorrent"

// Catalog is the slice of the store the ingest orchestrator reads/writes
// (ADR-0016: interface defined at the consumer). *store.Store satisfies it.
type Catalog interface {
	// AllLibraryRoots returns the configured root folders of every Library.
	AllLibraryRoots(ctx context.Context) ([]string, error)
	// LibraryRoots returns a Library's configured root folders.
	LibraryRoots(ctx context.Context, libraryID store.LibraryID) ([]string, error)
	// MediaFileExistsByFingerprint reports an existing live file by content
	// fingerprint (the exact-duplicate skip, §5).
	MediaFileExistsByFingerprint(ctx context.Context, fingerprint string) (store.MediaID, bool, error)
}

// ScanEnqueuer hands a Library root to the standard scan pipeline (§4). The
// orchestrator never scans itself — it only triggers the one scan pipeline.
type ScanEnqueuer interface {
	EnqueueScan(ctx context.Context, libraryID store.LibraryID, root string) error
}

// Fingerprinter computes a file's content fingerprint. Defaults to
// store.HashMediaFileContent; injectable for tests.
type Fingerprinter func(path string) (string, error)

// Ingestor routes completed downloads from a Source into Libraries: explicit
// category->Library map (never inference, §3), hardlink-first relocation into
// the Incoming Folder (§2), exact-duplicate skip (§5), Parse Hints sidecar, and
// scan handoff (§4). It deletes nothing.
type Ingestor struct {
	// mu guards the live config (source + routing), which the Console's
	// setQBittorrentIngest mutation swaps at runtime (ADR-0020 §6: applied
	// without restart) while the poll job concurrently reads it in Run.
	mu sync.RWMutex
	// transitionMu preserves caller order across validation and ownership
	// transfer. publishMu makes a successful transfer wait for the current
	// publication section, then prevents that retired generation from writing.
	transitionMu     sync.Mutex
	publishMu        sync.Mutex
	generation       uint64
	generationCtx    context.Context
	cancelGeneration context.CancelCauseFunc
	source           Source
	catalog          Catalog
	scan             ScanEnqueuer
	ledger           *Ledger
	routing          map[string]store.LibraryID // category -> Library
	// approvedRoots are the canonical operator-approved download roots (the
	// endpoint's category/default save paths captured at boot or at the last
	// successful Reconfigure). Every endpoint-nominated payload path is
	// untrusted and must resolve beneath one of these roots before any
	// fingerprint/link/sidecar work; an empty set refuses every import
	// (fail-closed) until ingest is (re)configured.
	approvedRoots []string
	fingerprint   Fingerprinter
	logger        *slog.Logger
}

// Config configures an Ingestor.
type Config struct {
	Source  Source
	Catalog Catalog
	Scan    ScanEnqueuer
	Ledger  *Ledger
	// Routing maps qBittorrent category -> Library ID. A category absent here
	// is unrouted: ignored and logged, never guessed (§3).
	Routing map[string]store.LibraryID
	// ApprovedRoots are the operator-approved download roots captured by the
	// composition root at boot (the endpoint's category/default save paths at
	// the moment the operator's configuration is applied). Empty means no
	// import can pass containment until Reconfigure supplies roots.
	ApprovedRoots []string
	// Fingerprint overrides the default content-fingerprint function (tests).
	Fingerprint Fingerprinter
	Logger      *slog.Logger
}

// NewIngestor validates config and builds an Ingestor. Source may be nil: the
// ingestor is then inert (Run is a no-op, Configured reports false) until the
// Console's setQBittorrentIngest mutation swaps in a real Source via Reconfigure.
// The catalog, scan enqueuer, and ledger are always
// required — they are process-lifetime wiring, not per-config state.
func NewIngestor(cfg Config) (*Ingestor, error) {
	if cfg.Catalog == nil {
		return nil, errors.New("qbittorrent: catalog is required")
	}
	if cfg.Scan == nil {
		return nil, errors.New("qbittorrent: scan enqueuer is required")
	}
	if cfg.Ledger == nil {
		return nil, errors.New("qbittorrent: ledger is required")
	}
	fingerprint := cfg.Fingerprint
	if fingerprint == nil {
		fingerprint = store.HashMediaFileContent
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	routing := map[string]store.LibraryID{}
	for category, library := range cfg.Routing {
		routing[category] = library
	}
	generationCtx, cancelGeneration := context.WithCancelCause(context.Background())
	return &Ingestor{
		generation:       1,
		generationCtx:    generationCtx,
		cancelGeneration: cancelGeneration,
		source:           cfg.Source,
		catalog:          cfg.Catalog,
		scan:             cfg.Scan,
		ledger:           cfg.Ledger,
		routing:          routing,
		approvedRoots:    cleanRoots(cfg.ApprovedRoots),
		fingerprint:      fingerprint,
		logger:           logger,
	}, nil
}

// cleanRoots copies and cleans a root list, dropping empty entries.
func cleanRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		cleaned := filepath.Clean(strings.TrimSpace(root))
		if cleaned == "" || cleaned == "." {
			continue
		}
		out = append(out, cleaned)
	}
	return out
}

// ApprovedRoots returns a copy of the live approved download roots.
func (i *Ingestor) ApprovedRoots() []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make([]string, len(i.approvedRoots))
	copy(out, i.approvedRoots)
	return out
}

// Routes returns a copy of the explicit category→Library routing map (ADR-0020
// §3). Read-only operator-Console view; the caller never mutates the live map.
// Configured reports whether a live Source is set — i.e. the operator has
// configured a qBittorrent endpoint (via boot config or the Console mutation).
// An inert ingestor (nil source) reports false, so the Console read can report
// "ingest not configured" honestly rather than enabled-with-no-endpoint.
func (i *Ingestor) Configured() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.source != nil
}

func (i *Ingestor) Routes() map[string]store.LibraryID {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make(map[string]store.LibraryID, len(i.routing))
	for category, library := range i.routing {
		out[category] = library
	}
	return out
}

// StatusFeed returns the recent ingest status feed from the ledger, newest
// first. It shares the live ledger handle the ingest job writes to, so the
// Console sees in-flight results without re-reading the file.
func (i *Ingestor) StatusFeed() []StatusEntry {
	return i.ledger.Status()
}

// RunResult summarizes one ingest pass.
type RunResult struct {
	Imported   int
	Skipped    int // exact-duplicate skips
	Unrouted   int
	Errors     int
	ScansArmed map[store.LibraryID]bool
}

// Run pulls completed items, routes and relocates the routable, and triggers a
// scan per Library that received new files. It is idempotent by torrent hash:
// already-processed hashes are skipped, so restarts never re-import (§5).
func (i *Ingestor) Run(ctx context.Context) (RunResult, error) {
	// Snapshot the live config once per pass so a concurrent Reconfigure swaps
	// in cleanly between passes rather than mutating routing mid-run.
	i.mu.RLock()
	generation := i.generation
	generationCtx := i.generationCtx
	source := i.source
	routing := i.routing
	approved := i.approvedRoots
	i.mu.RUnlock()
	runCtx, cancelRun := context.WithCancelCause(ctx)
	stopGenerationCancel := context.AfterFunc(generationCtx, func() {
		cancelRun(context.Cause(generationCtx))
	})
	defer stopGenerationCancel()
	defer cancelRun(nil)

	result := RunResult{ScansArmed: map[store.LibraryID]bool{}}
	if source == nil {
		// Inert: no endpoint configured yet. Nothing to poll (a no-op pass, not
		// an error) until the Console configures ingest.
		return result, nil
	}
	items, err := source.CompletedItems(runCtx)
	if err != nil {
		if errors.Is(context.Cause(runCtx), errGenerationRetired) {
			return result, nil
		}
		return result, err
	}
	i.publishMu.Lock()
	defer i.publishMu.Unlock()
	if !i.ownsGeneration(generation) {
		return result, nil
	}
	// Resolve the containment context once per pass: approved download roots
	// plus every Library root. A guard-build failure aborts the pass —
	// without a provable Library-root exclusion no import may proceed.
	guard, err := i.buildGuard(runCtx, approved)
	if err != nil {
		return result, err
	}
	for _, item := range items {
		if runCtx.Err() != nil {
			if errors.Is(context.Cause(runCtx), errGenerationRetired) {
				return result, nil
			}
			return result, runCtx.Err()
		}
		if item.ID == "" || i.ledger.Processed(item.ID) {
			continue
		}
		libraryID, routed := routing[item.Category]
		if !routed {
			i.recordUnrouted(item)
			result.Unrouted++
			continue
		}
		libraryID, armed, outcome := i.importItem(runCtx, item, libraryID, guard)
		switch outcome {
		case OutcomeImported:
			result.Imported++
			if armed {
				result.ScansArmed[libraryID] = true
			}
		case OutcomeAlreadyInCatalog:
			result.Skipped++
		case OutcomeError:
			result.Errors++
			if armed {
				result.ScansArmed[libraryID] = true
			}
		}
	}
	// One scan per Library that received files (deduped by the scan substrate).
	for libraryID := range result.ScansArmed {
		root, err := i.incomingFolder(runCtx, libraryID)
		if err != nil {
			i.logger.WarnContext(runCtx, "qbittorrent: incoming folder unavailable for scan", "library", string(libraryID))
			continue
		}
		if err := i.scan.EnqueueScan(runCtx, libraryID, root); err != nil {
			i.logger.WarnContext(runCtx, "qbittorrent: scan enqueue failed", "library", string(libraryID))
		}
	}
	return result, nil
}

func (i *Ingestor) ownsGeneration(generation uint64) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.generation == generation
}

// importItem relocates an item's media-bearing files into the target Library's
// Incoming Folder, skipping exact-fingerprint duplicates, and records the
// outcome. It returns the Library, whether at least one file landed (so a scan
// should be armed), and the recorded outcome.
//
// Containment: the endpoint-nominated ContentPath must confine
// to an approved download root before ANY payload work — no stat/fingerprint
// of an unconfined path — and each resolved file re-confines (with the
// no-follow leaf check) immediately before its fingerprint/link/sidecar step.
func (i *Ingestor) importItem(ctx context.Context, item Item, libraryID store.LibraryID, guard rootGuard) (store.LibraryID, bool, Outcome) {
	if !guard.confinesPath(item.ContentPath) {
		detail := "content path outside approved download roots"
		if len(guard.approved) == 0 {
			detail = "no approved download roots configured"
		}
		i.recordError(item, libraryID, detail)
		return libraryID, false, OutcomeError
	}
	incoming, err := i.incomingFolder(ctx, libraryID)
	if err != nil {
		i.recordError(item, libraryID, "library root unavailable")
		return libraryID, false, OutcomeError
	}
	mediaFiles, err := i.mediaFilesFor(ctx, item)
	if err != nil {
		i.recordError(item, libraryID, "payload unreadable")
		return libraryID, false, OutcomeError
	}
	if len(mediaFiles) == 0 {
		// No media-bearing files (samples/junk stay with the seed, §5). Mark
		// processed so it is not re-examined every pass; report as unrouted-ish
		// via an error-free skip recorded as already-in-catalog would mislead,
		// so record an informational error-free entry.
		i.ledger.Record(StatusEntry{
			TorrentHash: item.ID,
			ReleaseName: item.ReleaseName,
			Category:    item.Category,
			Outcome:     OutcomeImported,
			LibraryID:   string(libraryID),
			Detail:      "no media-bearing files",
		})
		return libraryID, false, OutcomeImported
	}

	linkedAny := false
	refusedAny := false
	var lastMode RelocateMode
	for _, mediaFile := range mediaFiles {
		// Final containment gate, immediately before the first byte of payload
		// work: the resolved file must still confine to an approved download
		// root, sit outside every Library root, and be a regular file (never a
		// symlink leaf) — so the inode fingerprinted/linked below is exactly
		// the one admitted here.
		if !guard.confinesFile(mediaFile.absolutePath) {
			i.logger.Warn("qbittorrent: refusing file outside approved download roots", "torrent", item.ID, "path", mediaFile.relPath)
			refusedAny = true
			continue
		}
		fingerprint, err := i.fingerprint(mediaFile.absolutePath)
		if err != nil {
			i.recordError(item, libraryID, "fingerprint failed")
			return libraryID, linkedAny, OutcomeError
		}
		existing, exists, err := i.catalog.MediaFileExistsByFingerprint(ctx, fingerprint)
		if err != nil {
			i.recordError(item, libraryID, "catalog lookup failed")
			return libraryID, linkedAny, OutcomeError
		}
		if exists {
			// Exact-fingerprint duplicate: nothing linked, seed untouched (§5).
			i.ledger.Record(StatusEntry{
				TorrentHash:  item.ID,
				ReleaseName:  item.ReleaseName,
				Category:     item.Category,
				Outcome:      OutcomeAlreadyInCatalog,
				LibraryID:    string(libraryID),
				MediaPointer: string(existing),
				Detail:       relName(mediaFile.relPath),
			})
			// A duplicate among a multi-file payload still lets the rest import;
			// continue rather than abort. The whole-item outcome is decided
			// after the loop.
			continue
		}
		dst := filepath.Join(incoming, mediaFile.relPath)
		// Final sink guard (defense in depth): relPath is already validated in
		// mediaFilesFor/walkDirMedia, but re-assert containment before any write
		// so no future producer can reintroduce a path-traversal escape into the
		// Library root.
		if !isSubpath(dst, incoming) {
			i.logger.Warn("qbittorrent: skipping file resolving outside incoming folder", "library", string(libraryID), "rel", mediaFile.relPath)
			continue
		}
		mode, err := relocateFile(mediaFile.absolutePath, dst)
		if err != nil {
			i.recordError(item, libraryID, "relocate failed")
			return libraryID, linkedAny, OutcomeError
		}
		lastMode = mode
		linkedAny = true
		if err := writeSidecar(dst, item, mediaFile.relPath); err != nil {
			// A missing sidecar degrades identification, not the import; the
			// file is already linked. Log and continue.
			i.logger.WarnContext(ctx, "qbittorrent: parse-hints sidecar write failed", "library", string(libraryID))
		}
	}

	if !linkedAny {
		if refusedAny {
			// At least one member was refused by containment and nothing
			// landed: report an error (not processed) so a later legitimate
			// configuration can retry, and the refusal is operator-visible.
			i.recordError(item, libraryID, "payload files outside approved download roots")
			return libraryID, false, OutcomeError
		}
		// Every member was an exact duplicate.
		return libraryID, false, OutcomeAlreadyInCatalog
	}
	i.ledger.Record(StatusEntry{
		TorrentHash: item.ID,
		ReleaseName: item.ReleaseName,
		Category:    item.Category,
		Outcome:     OutcomeImported,
		LibraryID:   string(libraryID),
		Mode:        lastMode,
	})
	return libraryID, true, OutcomeImported
}

// resolvedFile is a media-bearing file to relocate: its absolute source path
// and its path relative to the Incoming Folder target.
type resolvedFile struct {
	absolutePath string
	relPath      string
}

// mediaFilesFor returns the media-bearing files of an item. Single-file items
// resolve to the content path; multi-file items resolve to each member file,
// skipping non-media and junk (§5). The download dir must lie outside Library
// roots (§2) — that overlap is validated at config time, not here.
func (i *Ingestor) mediaFilesFor(ctx context.Context, item Item) ([]resolvedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(item.Files) == 0 {
		// Single-file payload: ContentPath is the file itself.
		info, err := os.Stat(item.ContentPath)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			// qBittorrent reported no member files but the payload is a
			// directory; walk it as a fallback.
			return i.walkDirMedia(ctx, item.ContentPath)
		}
		if !isMediaFile(item.ContentPath) {
			return nil, nil
		}
		// Containment of the single-file ContentPath (approved download roots,
		// Library-root exclusion, no-follow leaf) is enforced by importItem's
		// rootGuard — item-level before any payload work, and per-file
		// immediately before fingerprint/link. A self-relative check here
		// (ContentPath against its own parent) proves nothing: every readable
		// path trivially lies under its own parent.
		return []resolvedFile{{absolutePath: item.ContentPath, relPath: filepath.Base(item.ContentPath)}}, nil
	}
	var files []resolvedFile
	for _, member := range item.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel := filepath.Clean(member.RelPath)
		// Member paths come from qBittorrent verbatim (source.go), and
		// filepath.Clean does not strip a leading "..". Reject anything that is
		// absolute or escapes the content root before it can steer a write
		// outside the Incoming Folder (ADR-0020 §2; path-traversal hardening).
		if escapesRoot(rel) {
			i.logger.Warn("qbittorrent: skipping member with escaping path", "torrent", item.ID, "path", member.RelPath)
			continue
		}
		if !isMediaFile(rel) || isJunkPath(rel) {
			continue
		}
		abs := filepath.Join(item.ContentPath, rel)
		// escapesRoot only inspected the literal rel string; the member (or an
		// intermediate component) may still be a symlink whose target escapes
		// the content root. Resolve symlinks and require the real path stay
		// inside ContentPath before it can be fingerprinted/hardlinked
		// (ADR-0020 §2; symlink hardening).
		within, err := resolvesWithinRoot(abs, item.ContentPath)
		if err != nil || !within {
			i.logger.Warn("qbittorrent: skipping member resolving outside content root", "torrent", item.ID, "path", member.RelPath)
			continue
		}
		files = append(files, resolvedFile{
			absolutePath: abs,
			relPath:      rel,
		})
	}
	return files, nil
}

// walkDirMedia is the fallback for a directory payload with no member-file list.
func (i *Ingestor) walkDirMedia(ctx context.Context, root string) ([]resolvedFile, error) {
	var files []resolvedFile
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		// Defense in depth: WalkDir only descends into root, so rel should
		// never escape — but guard it the same way as a member-file path before
		// trusting it as a relocation target.
		if escapesRoot(rel) {
			i.logger.Warn("qbittorrent: skipping walked file with escaping path", "root", root, "path", path)
			return nil
		}
		if !isMediaFile(rel) || isJunkPath(rel) {
			return nil
		}
		// WalkDir does not follow symlinks (it lists them but does not descend),
		// so a walked media path can still be a symlink whose target escapes
		// root. Resolve and require containment before trusting it as a hardlink
		// source (ADR-0020 §2; symlink hardening).
		within, resolveErr := resolvesWithinRoot(path, root)
		if resolveErr != nil || !within {
			i.logger.Warn("qbittorrent: skipping walked file resolving outside root", "root", root, "path", path)
			return nil
		}
		files = append(files, resolvedFile{absolutePath: path, relPath: rel})
		return nil
	})
	return files, err
}

// incomingFolder returns the absolute Incoming Folder path for a Library: a
// subfolder inside the Library's first root (§4).
func (i *Ingestor) incomingFolder(ctx context.Context, libraryID store.LibraryID) (string, error) {
	roots, err := i.catalog.LibraryRoots(ctx, libraryID)
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("qbittorrent: library %s has no roots", libraryID)
	}
	return filepath.Join(roots[0], IncomingFolderName), nil
}

func (i *Ingestor) recordUnrouted(item Item) {
	detail := "uncategorized"
	if item.Category != "" {
		detail = "category not mapped"
	}
	i.ledger.Record(StatusEntry{
		TorrentHash: item.ID,
		ReleaseName: item.ReleaseName,
		Category:    item.Category,
		Outcome:     OutcomeUnrouted,
		Detail:      detail,
	})
	i.logger.Info("qbittorrent: torrent unrouted", "category", item.Category)
}

func (i *Ingestor) recordError(item Item, libraryID store.LibraryID, detail string) {
	i.ledger.Record(StatusEntry{
		TorrentHash: item.ID,
		ReleaseName: item.ReleaseName,
		Category:    item.Category,
		Outcome:     OutcomeError,
		LibraryID:   string(libraryID),
		Detail:      detail,
	})
	i.logger.Warn("qbittorrent: ingest item failed",
		"torrent", item.ID,
		"release", item.ReleaseName,
		"category", item.Category,
		"library", string(libraryID),
		"detail", detail,
	)
}

func relName(rel string) string {
	return filepath.Base(rel)
}

// mediaExtensions is the conservative media-bearing allowlist (§5: only
// Media-bearing files are linked). Containers only — sidecars, samples, NFOs
// stay with the seed.
var mediaExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".wmv": true, ".flv": true, ".webm": true, ".mpg": true, ".mpeg": true,
	".ts": true, ".m2ts": true, ".vob": true,
	".mp3": true, ".m4a": true, ".flac": true, ".aac": true, ".ogg": true,
	".opus": true, ".wav": true, ".wma": true,
}

func isMediaFile(path string) bool {
	return mediaExtensions[strings.ToLower(filepath.Ext(path))]
}

// isJunkPath rejects sample/extra clips that should stay with the seed (§5,
// ADR-0009 junk conventions). Conservative: only obvious "sample" markers.
func isJunkPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	if base == "sample.mkv" || base == "sample.mp4" || base == "sample.avi" {
		return true
	}
	if strings.Contains(lower, "/sample/") || strings.HasPrefix(lower, "sample/") {
		return true
	}
	if strings.HasPrefix(base, "sample-") || strings.HasSuffix(strings.TrimSuffix(base, filepath.Ext(base)), "-sample") {
		return true
	}
	return false
}

package scan

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/beamfall/core/internal/store"
)

var excludedScanDirectoryNames = map[string]struct{}{
	"@eaDir":       {},
	".AppleDouble": {},
	".actors":      {},
	".HomeTheater": {},
}

func skipExcludedScanDirectory(name string, isDir bool) error {
	if !isDir {
		return nil
	}
	if strings.HasPrefix(name, ".") {
		return fs.SkipDir
	}
	if _, excluded := excludedScanDirectoryNames[name]; excluded {
		return fs.SkipDir
	}
	return nil
}

func scanFromCheckpoint(ctx context.Context, st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor, metadataHydrators map[string]MetadataHydrator, gov WorkerGovernor, imageDeriver ImageDeriver, req Request, checkpoint jobCheckpoint, scanStarted time.Time, progress func(jobCheckpoint, store.JobProgress) error) (Result, error) {
	workers := scanWorkers(gov)
	state := scanWalkState{
		ctx:                 ctx,
		st:                  st,
		prober:              prober,
		grabber:             grabber,
		identifier:          identifier,
		imageDeriver:        imageDeriver,
		req:                 req,
		checkpoint:          checkpoint,
		progress:            progress,
		workers:             workers,
		workBudget:          make(chan struct{}, workers),
		result:              Result{FilesSeen: checkpoint.FilesSeen, MediaImported: checkpoint.MediaImported, LastPath: checkpoint.LastPath, MissingCredentials: append([]string(nil), checkpoint.MissingCredentials...), FilesAdded: checkpoint.FilesAdded, FilesChanged: checkpoint.FilesChanged, MediaUnmatched: checkpoint.MediaUnmatched, seenMedia: map[store.MediaID]bool{}, totalFiles: checkpoint.TotalFiles, unmatchedRecords: setOf(checkpoint.UnmatchedRecords)},
		directoryContexts:   map[string]ParseContext{},
		directoryPreflights: map[string]*contextualDirectoryPreflight{},
		versionAnchors:      map[string]versionAnchor{},
		versionDirsSeeded:   map[string]bool{},
		sidecars:            newSidecarDiscovery(interactiveSidecarExts),
		pendingCompletions:  make([]*completedFile, 0, workers),
		containerArt:        map[store.EntityID]*containerArtChoice{},
		localReleases:       map[localArtSlot]bool{},
		localMatched:        map[localArtSlot]bool{},
		startedAt:           time.Now(),
	}
	// Local sidecar artwork (CORE-0359) rides on the artwork service handed in
	// as the ImageDeriver; the container seam rides on the Identifier.
	state.localArtwork, _ = imageDeriver.(LocalArtworkImporter)
	state.containerArtwork, _ = imageDeriver.(ContainerArtworkImporter)
	state.containerResolver, _ = identifier.(ContainerEntityResolver)

	if len(parsers) > 0 || identifier != nil || len(localExtractors) > 0 || len(metadataHydrators) > 0 {
		pluginID, err := st.LibraryPluginID(ctx, req.LibraryID)
		if err != nil {
			return Result{}, err
		}
		state.libraryPluginID = pluginID
		if resolver, ok := identifier.(PluginMediaClassResolver); ok && pluginID != "" {
			state.imagesAreContent = slices.Contains(resolver.PluginMediaClasses(pluginID), store.MediaClassImage)
		}
		state.localExtractors = localExtractors[pluginID]
		if pluginID != "" {
			state.parser = parsers[pluginID]
			state.metadataHydrator = metadataHydrators[pluginID]
			if state.metadataHydrator != nil {
				state.metadataCapability = state.metadataHydrator.Capability()
			}
		}
	}
	if req.Backfill && state.metadataHydrator != nil && prober != nil {
		stale, err := metadataBackfillStale(ctx, st, req.LibraryID, state.metadataCapability)
		if err != nil {
			return Result{}, err
		}
		state.backfillStale = stale
	}
	extrasRoot, err := extrasRootFor(ctx, st, req.LibraryID, req.Root)
	if err != nil {
		return Result{}, err
	}
	state.extrasRoot = extrasRoot
	if state.result.totalFiles == 0 {
		err := filepath.Walk(req.Root, func(path string, info fs.FileInfo, err error) error {
			if err == nil {
				if skipErr := skipExcludedScanDirectory(info.Name(), info.IsDir()); skipErr != nil {
					return skipErr
				}
			}
			if err == nil && state.isSupportedMediaFile(path, info) && now().Sub(info.ModTime()) >= stabilityWindow {
				state.result.totalFiles++
			}
			return nil
		})
		if err != nil {
			return Result{}, err
		}
	}

	state.deriveCtx, state.cancelDerivations = context.WithCancel(ctx)
	defer state.derivativeWG.Wait()
	defer state.cancelDerivations()

	if workers > 1 {
		err = runScanPipeline(ctx, req.Root, workers, state.admitEntry, state.fill, state.consumeTask, state.skipOrPropagate)
	} else {
		err = filepath.WalkDir(req.Root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr == nil {
				if skipErr := skipExcludedScanDirectory(entry.Name(), entry.IsDir()); skipErr != nil {
					return skipErr
				}
			}
			task, admitErr := state.admitEntry(path, entry, walkErr)
			if admitErr != nil {
				return state.skipOrPropagate(path, admitErr)
			}
			if task == nil {
				return nil
			}
			state.fill(ctx, task)
			return state.skipOrPropagate(path, state.consumeTask(task))
		})
	}
	if err != nil {
		return state.stopped(err)
	}
	if err := state.flushPendingParts(); err != nil {
		return state.stopped(err)
	}
	for len(state.pendingCompletions) > 0 {
		if err := state.completeFirst(); err != nil {
			return state.stopped(err)
		}
	}
	state.applyDeferredArtwork()
	if err := state.markMissingIfPlausible(scanStarted); err != nil {
		return Result{}, err
	}
	if err := linkExtraRelationsForLibrary(ctx, st, req.LibraryID, state.extrasRoot); err != nil {
		return Result{}, err
	}
	return state.result, nil
}

func metadataBackfillStale(ctx context.Context, st *store.Store, libraryID store.LibraryID, capability MetadataHydrationCapability) (map[store.MediaID]struct{}, error) {
	stale := map[store.MediaID]struct{}{}
	queried := map[store.MediaClass]bool{}
	for _, class := range capability.MediaClasses {
		if class == "" || queried[class] {
			continue
		}
		queried[class] = true
		matching, err := st.MediaLackingFieldInLibrary(ctx, libraryID, class, capability.MarkerField)
		if err != nil {
			return nil, err
		}
		for mediaID := range matching {
			stale[mediaID] = struct{}{}
		}
	}
	return stale, nil
}

// The missing-ratio guard: a completed scan that would flip more than
// missingRatioGuardThreshold of a root's present files to missing at once
// skips the missing pass instead. Losing most of a library in one scan is far
// likelier a partly mounted or unreadable share than a real mass deletion.
// It only applies from missingRatioGuardMinPresent present files, so deleting
// most of a small library still marks those files missing.
const (
	missingRatioGuardThreshold  = 0.5
	missingRatioGuardMinPresent = 20
)

// markMissingIfPlausible runs the post-walk missing pass only when the walk
// result is believable. An absent, non-directory or unreadable root fails the
// scan; a root that walked no media files while it still has present rows, or
// a pass tripping the missing-ratio guard, skips marking with a warning.
func (s *scanWalkState) markMissingIfPlausible(scanStarted time.Time) error {
	if err := checkScanRootReadable(s.req.Root); err != nil {
		return err
	}
	present, stale, err := s.presentFilesUnderRoot(scanStarted)
	if err != nil {
		return err
	}
	if reason := missingPassSkipReason(present, stale, s.walkMediaFiles); reason != "" {
		slog.Warn("scan: not marking files missing", "library", string(s.req.LibraryID), "root", s.req.Root, "reason", reason, "present", present, "unseen", stale)
		return nil
	}
	if err := s.st.MarkMissingMediaFilesNotSeenSince(s.ctx, s.req.LibraryID, s.req.Root, scanStarted); err != nil {
		return err
	}
	s.result.FilesMissing = stale
	return nil
}

func checkScanRootReadable(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("scan root %q is unavailable: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("scan root %q is not a directory", root)
	}
	if _, err := os.ReadDir(root); err != nil {
		return fmt.Errorf("scan root %q cannot be read: %w", root, err)
	}
	return nil
}

// presentFilesUnderRoot counts the library's present files under the scan
// root and how many of them this scan has not refreshed (the rows the missing
// pass would flip), using the same millisecond cut as the store. Unrefreshed
// rows that are artwork companions, catalogued before CORE-1708, are left
// out of both counts: the walk skips them on purpose, so they say nothing about
// the root's health, and counting them would let a library with more artwork
// than media trip the guard on every rescan and never drop them.
func (s *scanWalkState) presentFilesUnderRoot(scanStarted time.Time) (present, stale int, err error) {
	root := filepath.Clean(s.req.Root)
	prefix := root + string(filepath.Separator)
	cutoff := scanStarted.UnixMilli()
	err = s.st.ForEachMediaFileForLibrary(s.ctx, s.req.LibraryID, func(file store.MediaFile) error {
		if file.Path != root && !strings.HasPrefix(file.Path, prefix) {
			return nil
		}
		unseen := file.LastSeenAt.IsZero() || file.LastSeenAt.UnixMilli() < cutoff
		if unseen && s.isArtworkCompanion(file.Path) {
			return nil
		}
		present++
		if unseen {
			stale++
		}
		return nil
	})
	return present, stale, err
}

func missingPassSkipReason(present, stale, walkedMediaFiles int) string {
	if present == 0 || stale == 0 {
		return ""
	}
	if walkedMediaFiles == 0 {
		return "scan root walked no media files while the library still has present files"
	}
	if present >= missingRatioGuardMinPresent && float64(stale) > missingRatioGuardThreshold*float64(present) {
		return fmt.Sprintf("missing-ratio guard: %d of %d present files would be marked missing (threshold %.0f%%)", stale, present, missingRatioGuardThreshold*100)
	}
	return ""
}

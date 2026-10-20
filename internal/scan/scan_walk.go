package scan

import (
	"context"
	"io/fs"
	"path/filepath"
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
		result:              Result{FilesSeen: checkpoint.FilesSeen, MediaImported: checkpoint.MediaImported, LastPath: checkpoint.LastPath, seenMedia: map[store.MediaID]bool{}, totalFiles: checkpoint.TotalFiles},
		directoryContexts:   map[string]ParseContext{},
		directoryPreflights: map[string]*contextualDirectoryPreflight{},
		sidecars:            newSidecarDiscovery(interactiveSidecarExts),
		pendingCompletions:  make([]*completedFile, 0, workers),
	}

	if len(parsers) > 0 || identifier != nil || len(localExtractors) > 0 || len(metadataHydrators) > 0 {
		pluginID, err := st.LibraryPluginID(ctx, req.LibraryID)
		if err != nil {
			return Result{}, err
		}
		state.libraryPluginID = pluginID
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

	var err error
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
		return Result{}, err
	}
	if err := state.flushPendingParts(); err != nil {
		return Result{}, err
	}
	for len(state.pendingCompletions) > 0 {
		if err := state.completeFirst(); err != nil {
			return Result{}, err
		}
	}
	if err := st.MarkMissingMediaFilesNotSeenSince(ctx, req.LibraryID, req.Root, scanStarted); err != nil {
		return Result{}, err
	}
	if err := linkExtraRelationsForLibrary(ctx, st, req.LibraryID); err != nil {
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

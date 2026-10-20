package scan

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

type scanWalkState struct {
	ctx                 context.Context
	st                  *store.Store
	prober              Prober
	grabber             FrameGrabber
	parser              FilenameParser
	identifier          Identifier
	localExtractors     []LocalExtractor
	metadataHydrator    MetadataHydrator
	metadataCapability  MetadataHydrationCapability
	imageDeriver        ImageDeriver
	libraryPluginID     string
	backfillStale       map[store.MediaID]struct{}
	req                 Request
	checkpoint          jobCheckpoint
	progress            func(jobCheckpoint, store.JobProgress) error
	workers             int
	workBudget          chan struct{}
	result              Result
	pendingParts        []parsedPartImport
	directoryContexts   map[string]ParseContext
	directoryPreflights map[string]*contextualDirectoryPreflight
	sidecars            *sidecarDiscovery
	deriveCtx           context.Context
	cancelDerivations   context.CancelFunc
	derivativeWG        sync.WaitGroup
	pendingCompletions  []*completedFile
}

func (s *scanWalkState) acquireBudget(ctx context.Context) error {
	select {
	case s.workBudget <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *scanWalkState) releaseBudget() {
	<-s.workBudget
}

func (s *scanWalkState) isSupportedMediaFile(path string, info fs.FileInfo) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	return info.Mode().IsRegular() && titleFromPath(path) != "" && mediaExtensions[ext]
}

func (s *scanWalkState) directoryContext(path string) ParseContext {
	dir := filepath.Dir(path)
	if cached, ok := s.directoryContexts[dir]; ok {
		return cached
	}
	parseContext := ParseContext{}
	entries, err := os.ReadDir(dir)
	if err == nil {
		count := 0
		for _, entry := range entries {
			info, infoErr := entry.Info()
			if infoErr != nil {
				count = 0
				break
			}
			if s.isSupportedMediaFile(filepath.Join(dir, entry.Name()), info) {
				count++
				if count > 1 {
					break
				}
			}
		}
		switch count {
		case 1:
			parseContext.DirectoryMediaFileCount = DirectoryMediaFileCountSingle
		case 2:
			parseContext.DirectoryMediaFileCount = DirectoryMediaFileCountMultiple
		}
	}
	s.directoryContexts[dir] = parseContext
	return parseContext
}

func (s *scanWalkState) admitEntry(path string, entry fs.DirEntry, err error) (*workerTask, error) {
	if err != nil {
		return nil, err
	}
	if entry.IsDir() {
		return nil, nil
	}
	path = filepath.Clean(path)
	if s.checkpoint.LastPath != "" && path <= s.checkpoint.LastPath {
		return nil, nil
	}
	info, err := entry.Info()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || titleFromPath(path) == "" {
		return nil, nil
	}
	if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."); !mediaExtensions[ext] {
		return nil, nil
	}
	if now().Sub(info.ModTime()) < stabilityWindow {
		return nil, nil
	}
	return &workerTask{
		path: path,
		info: fileStat{size: info.Size(), modTime: info.ModTime()},
		done: make(chan struct{}),
	}, nil
}

func (s *scanWalkState) fill(ctx context.Context, task *workerTask) {
	if err := s.acquireBudget(ctx); err != nil {
		task.err = err
		return
	}
	defer s.releaseBudget()
	task.fill(ctx, s.st, s.prober, s.grabber)
	if task.err == nil && imageClassForPath(task.path) {
		task.local = extractLocalMetadata(ctx, s.req.Root, task.path, s.localExtractors)
	}
}

func (s *scanWalkState) skipOrPropagate(path string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, jobs.ErrYielded) || errors.Is(err, errImageDerivation) {
		return err
	}
	slog.Warn("scan: skipping unreadable or invalid file", "path", path, "error", err.Error())
	return nil
}

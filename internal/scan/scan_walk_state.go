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
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

type scanWalkState struct {
	ctx                context.Context
	st                 *store.Store
	prober             Prober
	grabber            FrameGrabber
	parser             FilenameParser
	identifier         Identifier
	localExtractors    []LocalExtractor
	metadataHydrator   MetadataHydrator
	metadataCapability MetadataHydrationCapability
	imageDeriver       ImageDeriver
	libraryPluginID    string
	// imageRule is what the walk does with still images, from the classes
	// the Library's Plugin declares (CORE-0043, CORE-2361).
	imageRule     ImageRule
	backfillStale map[store.MediaID]struct{}
	req           Request
	checkpoint    jobCheckpoint
	scanStarted   time.Time
	// completedFingerprints maps the bytes of each file this pass completed
	// to its Media (keepCountedMediaBeforeMove, CORE-2197).
	completedFingerprints map[string]store.MediaID
	// movedFingerprints holds the bytes this walk imported at a path whose
	// own row did not hold them, which takes the row of any other file with
	// the same bytes (refreshMovedRow, CORE-2210).
	movedFingerprints   map[string]bool
	progress            func(jobCheckpoint, store.JobProgress) error
	workers             int
	workBudget          chan struct{}
	result              Result
	pendingParts        []parsedPartImport
	directoryContexts   map[string]ParseContext
	directoryPreflights map[string]*contextualDirectoryPreflight
	versionAnchors      map[string]versionAnchor
	versionDirsSeeded   map[string]bool
	sidecars            *sidecarDiscovery
	deriveCtx           context.Context
	cancelDerivations   context.CancelFunc
	derivativeWG        sync.WaitGroup
	pendingCompletions  []*completedFile
	// tallies holds what this scan did to each file until the file completes
	// and its counts reach the result (see noteFile).
	tallies map[string]fileTally
	// unreportedParts are the files of the part group being reported that
	// are already in the catalog but not yet counted (see resumeCheckpoint).
	unreportedParts []string
	// committed is the file whose import is in the catalog but not yet queued
	// for completion (see resumeCheckpoint).
	committed string
	// held are the files completed while a part group was pending, which the
	// resume point has not passed yet (see completeFirst).
	held              []heldCompletion
	localArtwork      LocalArtworkImporter
	containerArtwork  ContainerArtworkImporter
	containerResolver ContainerEntityResolver
	containerArt      map[store.EntityID]*containerArtChoice
	localReleases     map[localArtSlot]bool
	localMatched      map[localArtSlot]bool
	// extrasRoot is the configured Library root holding req.Root; it bounds
	// the Extras folder conventions even when req.Root is a subfolder.
	extrasRoot string
	// walkMediaFiles counts the media-file candidates this run's walk saw,
	// including ones a resumed checkpoint skips as already done. Zero means
	// the root walked empty, so the post-walk missing pass must not run.
	walkMediaFiles int
	// startedAt is the wall-clock time this walk began, compared with lock
	// marker file times, so it uses the real clock rather than now.
	startedAt time.Time
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
	return info.Mode().IsRegular() && titleFromPath(path) != "" && mediaExtensions[ext] && !s.imageRule.skips(path)
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
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if !entry.Type().IsRegular() || titleFromPath(path) == "" || !mediaExtensions[ext] {
		return nil, nil
	}
	s.walkMediaFiles++
	if s.checkpoint.LastPath != "" && compareWalkOrder(s.req.Root, path, s.checkpoint.LastPath) <= 0 {
		return nil, nil
	}
	info, err := entry.Info()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	if s.imageRule.skips(path) {
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

// compareWalkOrder orders two paths under root the way filepath.WalkDir visits
// them: component by component relative to root, each directory's entries in
// lexical order and a directory before everything inside it. A plain byte
// compare of full paths disagrees whenever a sibling name sorts below the
// separator: "a-c/x" < "a/b/y" bytewise ('-' < '/'), yet WalkDir finishes a/
// before it reaches a-c/. Paths outside root fall back to the byte compare.
func compareWalkOrder(root, a, b string) int {
	relA, errA := filepath.Rel(root, a)
	relB, errB := filepath.Rel(root, b)
	if errA != nil || errB != nil || !filepath.IsLocal(relA) || !filepath.IsLocal(relB) {
		return strings.Compare(a, b)
	}
	partsA := strings.Split(relA, string(filepath.Separator))
	partsB := strings.Split(relB, string(filepath.Separator))
	for i := 0; i < len(partsA) && i < len(partsB); i++ {
		if c := strings.Compare(partsA[i], partsB[i]); c != 0 {
			return c
		}
	}
	return len(partsA) - len(partsB)
}

package scan

import (
	"fmt"

	"github.com/beamfall/core/internal/store"
)

type completedFile struct {
	path        string
	mediaID     store.MediaID
	fingerprint string
	done        chan struct{}
	err         error
}

func (s *scanWalkState) completeFirst() error {
	completed := s.pendingCompletions[0]
	s.pendingCompletions = s.pendingCompletions[1:]
	select {
	case <-completed.done:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	if completed.err != nil {
		return fmt.Errorf("%w: %w", errImageDerivation, completed.err)
	}
	s.result.FilesSeen++
	s.applyTally(completed.path)
	s.result.Files = append(s.result.Files, ImportedFile{
		Path:               completed.path,
		MediaID:            completed.mediaID,
		ContentFingerprint: completed.fingerprint,
	})
	// A file completed while parts are still pending (an image between
	// numbered parts) must not move the resume checkpoint onto or past the
	// first pending part: a resume from there would re-walk only the later
	// parts and catalogue them as a group of their own (CORE-1837).
	held := len(s.pendingParts) != 0 && compareWalkOrder(s.req.Root, completed.path, s.pendingParts[0].imported.Path) >= 0
	if !held {
		s.result.LastPath = completed.path
	}
	if !s.result.seenMedia[completed.mediaID] {
		s.result.seenMedia[completed.mediaID] = true
		s.result.MediaImported++
	}
	// A held completion reports no progress: each report counts toward the
	// job's batch, and a batch that yields with the checkpoint held would
	// resume onto the same files and yield again, forever, once a pending
	// group is followed by a batch's worth of images.
	if s.progress != nil && !held {
		return s.progress(s.result.checkpoint("scanning"), store.JobProgress{
			Current: int64(s.result.FilesSeen),
			Total:   int64(s.result.totalFiles),
			Unit:    "files",
			Message: "Scanning",
		})
	}
	return nil
}

// fileTally is what a scan did to one file, for the scan summary (CORE-2040).
type fileTally struct {
	added, changed, unmatched bool
}

// noteFile records what the scan did to path. The counts reach the result
// only when the file completes (applyTally), so a yielded scan's checkpoint
// counts only the files it will not walk again.
func (s *scanWalkState) noteFile(path string, note func(*fileTally)) {
	if s.tallies == nil {
		s.tallies = map[string]fileTally{}
	}
	tally := s.tallies[path]
	note(&tally)
	s.tallies[path] = tally
}

func (s *scanWalkState) applyTally(path string) {
	tally, ok := s.tallies[path]
	if !ok {
		return
	}
	delete(s.tallies, path)
	if tally.added {
		s.result.FilesAdded++
	}
	if tally.changed {
		s.result.FilesChanged++
	}
	if tally.unmatched {
		s.result.MediaUnmatched++
	}
}

func (s *scanWalkState) queueCompletion(path string, mediaID store.MediaID, fingerprint string, derive bool) error {
	s.applyLocalArtwork(path, mediaID)
	completed := &completedFile{path: path, mediaID: mediaID, fingerprint: fingerprint, done: make(chan struct{})}
	s.pendingCompletions = append(s.pendingCompletions, completed)
	if derive && s.imageDeriver != nil {
		s.derivativeWG.Add(1)
		go func() {
			defer s.derivativeWG.Done()
			defer close(completed.done)
			if err := s.acquireBudget(s.deriveCtx); err != nil {
				completed.err = err
				return
			}
			defer s.releaseBudget()
			completed.err = s.imageDeriver.DeriveImage(s.deriveCtx, mediaID, path, fingerprint)
		}()
	} else {
		close(completed.done)
	}
	if len(s.pendingCompletions) >= s.workers {
		return s.completeFirst()
	}
	return nil
}

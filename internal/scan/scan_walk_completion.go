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
	s.result.Files = append(s.result.Files, ImportedFile{
		Path:               completed.path,
		MediaID:            completed.mediaID,
		ContentFingerprint: completed.fingerprint,
	})
	s.result.LastPath = completed.path
	if !s.result.seenMedia[completed.mediaID] {
		s.result.seenMedia[completed.mediaID] = true
		s.result.MediaImported++
	}
	if s.progress != nil {
		return s.progress(s.result.checkpoint("scanning"), store.JobProgress{
			Current: int64(s.result.FilesSeen),
			Total:   int64(s.result.totalFiles),
			Unit:    "files",
			Message: "Scanning",
		})
	}
	return nil
}

func (s *scanWalkState) queueCompletion(path string, mediaID store.MediaID, fingerprint string, derive bool) error {
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

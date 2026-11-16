package scan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

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
	// The file stays pending until its derivation succeeds, so a scan stopped
	// while waiting still carries it in the resume checkpoint.
	completed := s.pendingCompletions[0]
	select {
	case <-completed.done:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	if completed.err != nil {
		return fmt.Errorf("%w: %w", errImageDerivation, completed.err)
	}
	s.pendingCompletions = s.pendingCompletions[1:]
	s.result.FilesSeen++
	tally := s.applyTally(completed.path)
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
	newMedia := !s.result.seenMedia[completed.mediaID]
	if newMedia {
		s.result.seenMedia[completed.mediaID] = true
		s.result.MediaImported++
		if s.result.versionMedia[completed.mediaID] || slices.ContainsFunc(s.pendingCompletions, func(pending *completedFile) bool { return pending.mediaID == completed.mediaID }) {
			s.noteGroupMedia(completed.mediaID)
		}
	}
	if held {
		s.held = append(s.held, heldCompletion{path: completed.path, mediaID: completed.mediaID, tally: tally, newMedia: newMedia})
	}
	// A held completion reports no progress: each report counts toward the
	// job's batch, and a batch that yields with the checkpoint held would
	// resume onto the same files and yield again, forever, once a pending
	// group is followed by a batch's worth of images.
	if s.progress != nil && !held {
		return s.progress(s.resumeCheckpoint(), store.JobProgress{
			Current: int64(s.result.FilesSeen),
			Total:   int64(s.result.totalFiles),
			Unit:    "files",
			Message: "Scanning",
		})
	}
	return nil
}

// countedInEarlierPass returns the Media an earlier pass of this job already
// counted for the bytes a resumed walk is about to import at path, or "".
// The catalog keeps one file row per content fingerprint, so importing a
// second file with the same bytes moves the row to it. Before that move, a row
// at another path under the root, at or before the resume point and seen
// since the scan started, is a file an earlier pass completed and counted
// (CORE-2197).
func (s *scanWalkState) countedInEarlierPass(path, fingerprint string) (store.MediaID, error) {
	resumeAt := s.checkpoint.LastPath
	if resumeAt == "" {
		return "", nil
	}
	mediaID, ok, err := s.st.MediaFileExistsByFingerprint(s.ctx, fingerprint)
	if err != nil || !ok || s.result.seenMedia[mediaID] {
		return "", err
	}
	files, err := s.st.MediaFilesForMedia(s.ctx, mediaID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		return "", err
	}
	root := filepath.Clean(s.req.Root)
	prefix := root + string(filepath.Separator)
	cutoff := s.scanStarted.UnixMilli()
	for _, file := range files {
		if file.ContentFingerprint != fingerprint || file.Path == path || !strings.HasPrefix(file.Path, prefix) {
			continue
		}
		if file.LastSeenAt.UnixMilli() >= cutoff && compareWalkOrder(root, file.Path, resumeAt) <= 0 {
			return mediaID, nil
		}
	}
	return "", nil
}

// noteGroupMedia records a Media of several files counted in MediaImported,
// so a resumed scan that walks its other files does not count it again.
func (s *scanWalkState) noteGroupMedia(mediaID store.MediaID) {
	if s.result.groupMedia == nil {
		s.result.groupMedia = map[store.MediaID]bool{}
	}
	s.result.groupMedia[mediaID] = true
}

// completePending completes every queued file, in order.
func (s *scanWalkState) completePending() error {
	for len(s.pendingCompletions) > 0 {
		if err := s.completeFirst(); err != nil {
			return err
		}
	}
	return nil
}

// fileTally is what a scan did to one file, for the scan summary (CORE-2040).
type fileTally struct {
	added, changed, unmatched bool
}

// heldCompletion is a file completed while the resume point was held below a
// pending part group: a resume from there walks it again.
type heldCompletion struct {
	path     string
	mediaID  store.MediaID
	tally    fileTally
	newMedia bool
}

// releaseHeld moves the resume point past the held completions once no part
// group holds it back any more.
func (s *scanWalkState) releaseHeld() {
	for _, held := range s.held {
		if compareWalkOrder(s.req.Root, held.path, s.result.LastPath) > 0 {
			s.result.LastPath = held.path
		}
	}
	s.held = nil
}

// dropTallies forgets the files a scan skipped without cataloguing them.
func (s *scanWalkState) dropTallies(paths ...string) {
	for _, path := range paths {
		delete(s.tallies, path)
	}
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

// resumeCheckpoint is the resume point for the scan so far. A file imported
// but not yet queued (committed), a completion still pending, and each part of
// a group already imported but not yet reported (unreportedParts) are already
// in the catalog, so a resume takes them through the fast path and never
// tallies them again: their added or changed counts ride in the checkpoint
// now, while the result counts them only once they complete. Their unmatched
// counts do not, because the fast path counts a queued item again
// (retryAutoIdentify).
func (s *scanWalkState) resumeCheckpoint() jobCheckpoint {
	checkpoint := s.result.checkpoint("scanning")
	carry := func(path string) {
		tally := s.tallies[path]
		if tally.added {
			checkpoint.FilesAdded++
		}
		if tally.changed {
			checkpoint.FilesChanged++
		}
	}
	if s.committed != "" {
		carry(s.committed)
	}
	for _, pending := range s.pendingCompletions {
		carry(pending.path)
	}
	// A held completion past the resume point is walked again on resume, so
	// the checkpoint keeps only its added or changed count, as for a pending
	// completion.
	for _, held := range s.held {
		if compareWalkOrder(s.req.Root, held.path, checkpoint.LastPath) <= 0 {
			continue
		}
		checkpoint.FilesSeen--
		if held.newMedia {
			checkpoint.MediaImported--
		}
		if held.tally.unmatched {
			checkpoint.MediaUnmatched--
		}
		if held.newMedia {
			checkpoint.GroupMedia = slices.DeleteFunc(checkpoint.GroupMedia, func(id string) bool { return id == string(held.mediaID) })
		}
	}
	for _, path := range s.unreportedParts {
		carry(path)
		// A part ordered before the resume point is never walked again, so
		// the checkpoint counts it as seen too.
		if compareWalkOrder(s.req.Root, path, checkpoint.LastPath) < 0 {
			checkpoint.FilesSeen++
		}
	}
	return checkpoint
}

// stopped ends a scan that failed or yielded. A cancelled scan returns its
// resume point, so the next run still counts the files this one already put
// in the catalog (CORE-2040).
func (s *scanWalkState) stopped(err error) (Result, error) {
	err = s.flushDeferredArtworkOnYield(err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		checkpoint := s.resumeCheckpoint()
		return Result{interrupted: &checkpoint}, err
	}
	return Result{}, err
}

func (s *scanWalkState) applyTally(path string) fileTally {
	tally, ok := s.tallies[path]
	if !ok {
		return fileTally{}
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
	return tally
}

func (s *scanWalkState) queueCompletion(path string, mediaID store.MediaID, fingerprint string, derive bool) error {
	s.applyLocalArtwork(path, mediaID)
	s.committed = ""
	// Content deduplication can resolve a second file to a Media an earlier
	// file already counted. The checkpoint keeps such a Media, so a resume
	// that walks the second file again does not count it twice (CORE-2197).
	if s.result.seenMedia[mediaID] {
		s.noteGroupMedia(mediaID)
	}
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

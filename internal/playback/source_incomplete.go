package playback

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

type sourceEndObserver interface {
	ObserveSourceEnd(context.Context, store.MediaID, string, string, string, func() (float64, bool)) error
}

// Remember observed EOF beyond a player session's lifetime. The observation is
// about one file generation, not the catalogue's duration or the Media's other
// Versions: a growing/replaced download becomes eligible again without a rescan.
// This bounded memory fallback supplements the durable source observations in Store.
type incompleteSourceCache struct {
	mu    sync.Mutex
	files map[string]os.FileInfo
}

func sameSourceFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (c *incompleteSourceCache) rememberVerified(path string, observed os.FileInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil || len(c.files) >= durationProbeCacheLimit && c.files[path] == nil {
		c.files = make(map[string]os.FileInfo)
	}
	c.files[path] = observed
}

func (c *incompleteSourceCache) contains(path string) bool {
	c.mu.Lock()
	observed := c.files[path]
	c.mu.Unlock()
	if observed == nil {
		return false
	}
	current, err := os.Stat(path)
	if err == nil && sameSourceFile(observed, current) {
		return true
	}
	c.mu.Lock()
	// A concurrent encoder may already have observed the replacement file.
	if c.files[path] == observed {
		delete(c.files, path)
	}
	c.mu.Unlock()
	return false
}

// A path's latest observed generation remains known while any instance
// can still publish an observation. Cache eviction cannot revive an older one.
type sourceGenerationOwner struct {
	generation         string
	users              int
	claims, published  uint64
	durationGeneration string
	durationPublished  uint64
}

// Take the ordinal before stat so a completion delayed after its last stat
// cannot supersede a newer publication, even after the newer owner retires.
func (s *Sessions) claimSource(path string, secure *EncryptedSource) sourceCapture {
	s.mu.Lock()
	owner := s.acquireSourceLocked(path)
	owner.claims++
	order := owner.claims
	s.mu.Unlock()
	source := captureSource(path, secure, s.statSource)
	source.owner, source.order = owner, order
	return source
}

func (s *Sessions) releaseSource(source sourceCapture) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseSourceLocked(source.path, source.owner)
}

func (c sourceCapture) supersededLocked() bool {
	return c.order < c.owner.published && c.generation != c.owner.generation
}

func (c sourceCapture) publishLocked() {
	if c.generation != "" {
		c.owner.published = max(c.owner.published, c.order)
		c.owner.generation = c.generation
	}
}

func (h *hlsSession) currentSourceLocked() bool {
	return h.sourceOwner == nil || h.sourceOwner.generation == h.sourceGeneration
}

func (s *Sessions) acquireSourceLocked(path string) *sourceGenerationOwner {
	if s.sourceOwners == nil {
		s.sourceOwners = make(map[string]*sourceGenerationOwner)
	}
	owner := s.sourceOwners[path]
	if owner == nil {
		owner = &sourceGenerationOwner{}
		s.sourceOwners[path] = owner
	}
	owner.users++
	return owner
}

func (s *Sessions) releaseSourceLocked(path string, owner *sourceGenerationOwner) {
	if owner != nil {
		owner.users--
		if owner.users == 0 {
			delete(s.sourceOwners, path)
		}
	}
}

func (s *Sessions) holdInspectionLocked(session *hlsSession) {
	if session.inspections == 0 {
		session.inspectionDone = make(chan struct{})
	}
	session.inspections++
}

func (s *Sessions) releaseInspectionLocked(session *hlsSession) {
	session.inspections--
	if session.inspections == 0 {
		close(session.inspectionDone)
	}
}

// ErrArtifactChanged asks the caller to retry an artifact invalidated by a restart.
var ErrArtifactChanged = errors.New("playback artifact run changed")

// Observe a finalized segment or a reaped run's output. Store callbacks use
// nonblocking lock attempts so their caller can retry outside Store.
func (s *Sessions) observeIncompleteSource(ctx context.Context, key SessionKey, session *hlsSession, artifact artifactObservation, reaping bool, observe func() (time.Duration, bool)) (time.Duration, bool, error) {
	run := artifact.run
	valid := func() error {
		if session.state != sessionRunning {
			return ErrSessionEvicted
		}
		if !session.currentSourceLocked() {
			return errSetupUnavailable
		}
		if session.run != run || session.writing != nil && session.writing != run ||
			!reaping && session.artifactVersion != artifact.version {
			return ErrArtifactChanged
		}
		return nil
	}
	for {
		var end time.Duration
		var observed, busy, held bool
		var settling <-chan struct{}
		var claimErr error
		inspect := func() (float64, bool) {
			if !s.mu.TryLock() {
				busy = true
				return 0, false
			}
			claimErr = valid()
			if claimErr == nil {
				claimErr = ctx.Err()
			}
			if claimErr == nil {
				if !reaping && run != nil && session.writing == run {
					select {
					case <-run.reaped:
						settling = run.exited
					default:
					}
				}
				if settling == nil {
					s.holdInspectionLocked(session)
					held = true
				}
			}
			s.mu.Unlock()
			if !held {
				return 0, false
			}
			end, observed = observe()
			if observed && session.sourceGeneration != "" &&
				!session.sourceCapture().matches(captureSource(session.src, session.secure, s.statSource)) {
				observed = false
			}
			if !s.mu.TryLock() {
				busy = true
				return 0, false
			}
			claimErr = valid()
			if claimErr == nil {
				claimErr = ctx.Err()
			}
			s.mu.Unlock()
			return end.Seconds(), claimErr == nil && observed
		}

		var err error
		if s.sourceStore == nil || session.sourceGeneration == "" {
			_, _ = inspect()
		} else {
			fingerprint := ""
			if session.secure != nil {
				fingerprint = session.secure.Fingerprint
			}
			err = s.sourceStore.ObserveSourceEnd(ctx, key.MediaID, session.src, fingerprint, session.sourceGeneration, inspect)
		}
		s.mu.Lock()
		if held {
			s.releaseInspectionLocked(session)
		}
		currentErr := valid()
		if currentErr == nil && claimErr == nil && !busy && observed && session.sourceInfo != nil {
			s.incompleteSources.rememberVerified(session.src, session.sourceInfo)
		}
		s.mu.Unlock()
		if currentErr != nil {
			return 0, false, currentErr
		}
		if claimErr != nil {
			return 0, false, claimErr
		}
		if err != nil {
			slog.Error("observing source end failed", "media", key.MediaID, "error", err)
			return end, observed && !busy, err
		}
		if settling != nil {
			if err := waitEncoderOwner(ctx, session, settling); err != nil {
				return 0, false, err
			}
			continue
		}
		if !busy {
			return end, observed, nil
		}
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
	}
}

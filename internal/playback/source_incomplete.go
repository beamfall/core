package playback

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"
)

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
	generation string
	users      int
}

func (s *Sessions) retainSourceLocked(session *hlsSession) {
	if session.sourceGeneration == "" {
		return
	}
	if s.sourceOwners == nil {
		s.sourceOwners = make(map[string]*sourceGenerationOwner)
	}
	owner := s.sourceOwners[session.src]
	if owner == nil {
		owner = &sourceGenerationOwner{}
		s.sourceOwners[session.src] = owner
	}
	owner.generation = session.sourceGeneration
	owner.users++
	session.sourceOwner = owner
}

func (s *Sessions) releaseSourceLocked(session *hlsSession) {
	if owner := session.sourceOwner; owner != nil {
		owner.users--
		if owner.users == 0 {
			delete(s.sourceOwners, session.src)
		}
	}
}

// ErrArtifactChanged asks the caller to retry an artifact invalidated by a restart.
var ErrArtifactChanged = errors.New("playback artifact run changed")

// Observe a finalized segment or a reaped run's output. Store callbacks use
// nonblocking lock attempts so their caller can retry outside Store.
func (s *Sessions) observeIncompleteSource(ctx context.Context, key SessionKey, session *hlsSession, run *encoderRun, observe func() (time.Duration, bool)) (time.Duration, bool, error) {
	valid := func() error {
		if session.state != sessionRunning {
			return ErrSessionEvicted
		}
		if owner := session.sourceOwner; owner != nil && owner.generation != session.sourceGeneration {
			return errSetupUnavailable
		}
		if session.run != run || session.writing != nil && session.writing != run {
			return ErrArtifactChanged
		}
		return nil
	}
	for {
		var end time.Duration
		var observed, busy, held bool
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
				if session.inspections == 0 {
					session.inspectionDone = make(chan struct{})
				}
				session.inspections++
				held = true
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
			session.inspections--
			if session.inspections == 0 {
				close(session.inspectionDone)
			}
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
		if !busy {
			return end, observed, nil
		}
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
	}
}

package playback

import (
	"context"
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

func (c *incompleteSourceCache) remember(path string, observed os.FileInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil || len(c.files) >= durationProbeCacheLimit {
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

// Detect EOF under the store's seal lock, then publish the session/cache bound.
// The store retains failed writes independently of this session and retries them
// before a read or seal can expose/discard the catalog.
func (s *Sessions) observeIncompleteSource(key SessionKey, session *hlsSession, observe func() (time.Duration, bool)) (time.Duration, bool, error) {
	var end time.Duration
	var observed bool
	var err error
	// Store -> Sessions matches unseal's derivative reconciliation lock order.
	// Keep the probe inside ObserveSourceEnd so sealing still serializes with
	// detection and persistence. If teardown removed the session during the
	// probe, the files may have disappeared and the result must be discarded.
	inspect := func() (float64, bool) {
		s.mu.Lock()
		current := s.liveLocked(key, session)
		s.mu.Unlock()
		if !current {
			return 0, false
		}
		probedEnd, found := observe()
		s.mu.Lock()
		if s.liveLocked(key, session) {
			end, observed = probedEnd, found
		}
		s.mu.Unlock()
		return end.Seconds(), observed
	}
	// Sources without a stable generation use only the existing memory fallback.
	// They cannot be durably identified, so do not require a catalog lookup.
	if s.sourceStore == nil || session.sourceGeneration == "" {
		_, _ = inspect()
	} else {
		fingerprint := ""
		if session.secure != nil {
			fingerprint = session.secure.Fingerprint
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = s.sourceStore.ObserveSourceEnd(ctx, key.MediaID, session.src, fingerprint, session.sourceGeneration, inspect)
		if err != nil {
			slog.Error("observing source end failed", "media", key.MediaID, "error", err)
		}
	}
	var sameGeneration bool
	if observed && session.secure == nil && session.sourceInfo != nil {
		stat := os.Stat
		if s.statSource != nil {
			stat = s.statSource
		}
		current, err := stat(session.src)
		sameGeneration = err == nil && sameSourceFile(session.sourceInfo, current)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.liveLocked(key, session) {
		return 0, false, ErrSessionEvicted
	}
	if sameGeneration {
		// Publish only while this instance is live. A successor may already
		// have cached EOF for a newer generation of this path.
		s.incompleteSources.remember(session.src, session.sourceInfo)
	}
	return end, observed, err
}

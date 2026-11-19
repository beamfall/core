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
	if observed == nil {
		return
	}
	current, err := os.Stat(path)
	if err != nil || !sameSourceFile(observed, current) {
		return
	}
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

func (s *Sessions) rememberIncompleteSource(key SessionKey, session *hlsSession, end time.Duration) {
	if session.secure == nil {
		s.incompleteSources.remember(session.src, session.sourceInfo)
	}
	if s.sourceStore != nil {
		fingerprint := ""
		if session.secure != nil {
			fingerprint = session.secure.Fingerprint
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.sourceStore.RecordSourceEnd(ctx, key.MediaID, session.src, fingerprint, session.sourceGeneration, end.Seconds()); err != nil {
			slog.Error("persisting observed source end failed", "media", key.MediaID, "error", err)
		}
	}
}

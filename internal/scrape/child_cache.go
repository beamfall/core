package scrape

import (
	"crypto/sha256"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	childResponseTTL  = 5 * time.Minute
	childCacheEntries = 64
	childCacheBytes   = 10 << 20
)

type childResponse struct {
	data    []byte
	expires time.Time
}

// Small, process-local cache of immutable responses. The key uses the
// authorized request so a credential change cannot reuse another identity's
// response. Query parameters and credentials are kept out of cache keys.
type childResponseCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]childResponse
	size    int
}

func childResponseKey(req *http.Request) [sha256.Size]byte {
	digest := sha256.New()
	_, _ = io.WriteString(digest, req.Method+" "+req.URL.String()+"\n")
	_ = req.Header.Write(digest)
	return [sha256.Size]byte(digest.Sum(nil))
}

func (c *childResponseCache) get(key [sha256.Size]byte, now time.Time) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; now.Before(entry.expires) {
		return entry.data
	}
	return nil
}

func (c *childResponseCache) put(key [sha256.Size]byte, data []byte, now time.Time) {
	if len(data) > childCacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[[sha256.Size]byte]childResponse)
	}
	c.size -= len(c.entries[key].data)
	delete(c.entries, key)
	for len(c.entries) >= childCacheEntries || c.size+len(data) > childCacheBytes {
		var oldestKey [sha256.Size]byte
		var oldest childResponse
		for key, entry := range c.entries {
			if oldest.expires.IsZero() || entry.expires.Before(oldest.expires) {
				oldestKey, oldest = key, entry
			}
		}
		delete(c.entries, oldestKey)
		c.size -= len(oldest.data)
	}
	c.entries[key] = childResponse{data: data, expires: now.Add(childResponseTTL)}
	c.size += len(data)
}

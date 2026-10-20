package mediahttp

import (
	"sync"
	"time"
)

type hotSegmentCache struct {
	mu      sync.Mutex
	max     int64
	used    int64
	seq     int64
	active  map[string]int
	entries map[string]hotSegmentEntry
}

type hotSegmentEntry struct {
	data    []byte
	modTime time.Time
	last    int64
}

type hotSegmentLease struct {
	cache *hotSegmentCache
	key   string
}

func newHotSegmentCache(maxBytes int64) *hotSegmentCache {
	if maxBytes <= 0 {
		return nil
	}
	return &hotSegmentCache{
		max:     maxBytes,
		active:  map[string]int{},
		entries: map[string]hotSegmentEntry{},
	}
}

func (c *hotSegmentCache) begin(key string) ([]byte, time.Time, bool, bool, hotSegmentLease) {
	if c == nil || key == "" {
		return nil, time.Time{}, false, false, hotSegmentLease{}
	}
	c.mu.Lock()
	c.seq++
	fanout := c.active[key] > 0
	c.active[key]++
	entry, hit := c.entries[key]
	if hit {
		entry.last = c.seq
		c.entries[key] = entry
	}
	c.mu.Unlock()
	return cloneSegmentBytes(entry.data), entry.modTime, hit, fanout, hotSegmentLease{cache: c, key: key}
}

func (l hotSegmentLease) done() {
	if l.cache == nil || l.key == "" {
		return
	}
	l.cache.finish(l.key)
}

func (c *hotSegmentCache) finish(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := c.active[key]
	if count <= 1 {
		delete(c.active, key)
		c.deleteEntry(key)
		return
	}
	c.active[key] = count - 1
}

func (c *hotSegmentCache) store(key string, data []byte, modTime time.Time) bool {
	if c == nil || key == "" || len(data) == 0 || int64(len(data)) > c.max {
		return false
	}
	data = cloneSegmentBytes(data)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[key] < 2 {
		return false
	}

	c.seq++
	if existing, ok := c.entries[key]; ok {
		c.used -= int64(len(existing.data))
	}
	c.entries[key] = hotSegmentEntry{data: data, modTime: modTime, last: c.seq}
	c.used += int64(len(data))
	c.evictOverBudget()
	_, ok := c.entries[key]
	return ok
}

func (c *hotSegmentCache) stats() (int, int64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.used
}

func (c *hotSegmentCache) evictOverBudget() {
	for c.used > c.max && len(c.entries) > 0 {
		var victim string
		var oldest int64
		for key, entry := range c.entries {
			if victim == "" || entry.last < oldest {
				victim = key
				oldest = entry.last
			}
		}
		c.deleteEntry(victim)
	}
}

func (c *hotSegmentCache) deleteEntry(key string) {
	if entry, ok := c.entries[key]; ok {
		c.used -= int64(len(entry.data))
		delete(c.entries, key)
	}
}

func cloneSegmentBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

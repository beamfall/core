package artwork

import (
	"container/list"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// versionCacheCap bounds the memoised version tokens. Each entry is a path plus
// a short token (a few hundred bytes), so the cap holds a large library's hot
// working set — every shelf, search and queue item renders several slots — in
// a couple of megabytes.
const versionCacheCap = 8192

// racyWindow is how recently a file may have been modified and still be too
// fresh to memoise. Filesystems with coarse timestamps (a kernel tick, or
// FAT/SMB's two seconds) can rewrite a file in place with the same size inside
// one timestamp grain, which the (size, mtime) check alone cannot see; such a
// file is re-hashed until its mtime is safely in the past (git's racy-clean rule).
const racyWindow = 2 * time.Second

// versionCache memoises content-hash version tokens by path. An entry is valid
// only while the file's (size, mtime-ns) from a single Stat still match the
// values seen when it was hashed, so a rewritten file is always re-hashed; the
// package's own writers also forget entries eagerly. The token itself stays a
// pure content hash — the cache only decides when the hash must be recomputed.
type versionCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     list.List
	// hash computes the token for a path; nil means contentVersion. It is the
	// test seam that counts real file reads.
	hash func(path string) (string, error)
}

type versionEntry struct {
	path  string
	size  int64
	mtime int64
	token string
}

func (c *versionCache) version(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		c.forget(path)
		return "", false
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	c.mu.Lock()
	if el, ok := c.entries[path]; ok {
		if e := el.Value.(*versionEntry); e.size == size && e.mtime == mtime {
			c.lru.MoveToFront(el)
			token := e.token
			c.mu.Unlock()
			return token, true
		}
	}
	hash := c.hash
	c.mu.Unlock()
	if hash == nil {
		hash = contentVersion
	}
	// Hashing runs unlocked. A writer racing it leaves a (stale stat, newer
	// token) or (newer stat, stale token) pair whose stat no longer matches the
	// file, so the next call re-hashes; it never pins a wrong token.
	token, err := hash(path)
	if err != nil {
		return "", false
	}
	if time.Since(info.ModTime()) >= racyWindow {
		c.store(&versionEntry{path: path, size: size, mtime: mtime, token: token})
	} else {
		c.forget(path)
	}
	return token, true
}

func (c *versionCache) store(e *versionEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[e.path]; ok {
		el.Value = e
		c.lru.MoveToFront(el)
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
	}
	c.entries[e.path] = c.lru.PushFront(e)
	if c.lru.Len() > versionCacheCap {
		oldest := c.lru.Back()
		c.lru.Remove(oldest)
		delete(c.entries, oldest.Value.(*versionEntry).path)
	}
}

// forget drops the entry for one artwork file.
func (c *versionCache) forget(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[path]; ok {
		c.lru.Remove(el)
		delete(c.entries, path)
	}
}

// forgetDir drops every entry under dir, for writers that remove a whole
// artwork directory.
func (c *versionCache) forgetDir(dir string) {
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, el := range c.entries {
		if strings.HasPrefix(path, prefix) {
			c.lru.Remove(el)
			delete(c.entries, path)
		}
	}
}

// scaleFailureCap bounds the negative derivative cache; on overflow it is
// simply cleared, which costs at most one more attempt per failing original.
const scaleFailureCap = 1024

// scaleFailures remembers originals the scaler could not decode, keyed by the
// exact file version (path, size, mtime-ns), so a corrupt original is attempted
// once rather than respawning the scaler on every request. Replacing the
// original changes its key, which retries it naturally.
type scaleFailures struct {
	mu   sync.Mutex
	seen map[scaleFailureKey]struct{}
}

type scaleFailureKey struct {
	path  string
	size  int64
	mtime int64
}

func newScaleFailureKey(path string, info os.FileInfo) scaleFailureKey {
	return scaleFailureKey{path: path, size: info.Size(), mtime: info.ModTime().UnixNano()}
}

func (f *scaleFailures) failed(key scaleFailureKey) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.seen[key]
	return ok
}

// record notes a failure and reports whether it is new (so the caller logs it
// once).
func (f *scaleFailures) record(key scaleFailureKey) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.seen[key]; ok {
		return false
	}
	if f.seen == nil || len(f.seen) >= scaleFailureCap {
		f.seen = make(map[scaleFailureKey]struct{})
	}
	f.seen[key] = struct{}{}
	return true
}

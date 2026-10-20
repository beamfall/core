package mediahttp

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHotSegmentCacheCopiesOutsideLock(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	source, err := os.ReadFile(filepath.Join(filepath.Dir(file), "hot_segment_cache.go"))
	if err != nil {
		t.Fatal(err)
	}

	begin := string(source[strings.Index(string(source), "func (c *hotSegmentCache) begin"):strings.Index(string(source), "func (l hotSegmentLease) done")])
	if strings.Index(begin, "c.mu.Unlock()") > strings.LastIndex(begin, "cloneSegmentBytes") {
		t.Fatal("begin copies cached bytes while holding the mutex")
	}
	store := string(source[strings.Index(string(source), "func (c *hotSegmentCache) store"):strings.Index(string(source), "func (c *hotSegmentCache) stats")])
	if strings.Index(store, "cloneSegmentBytes") > strings.Index(store, "c.mu.Lock()") {
		t.Fatal("store copies caller bytes while holding the mutex")
	}
}

func TestHotSegmentCacheConcurrentMutationIsolation(t *testing.T) {
	cache := newHotSegmentCache(1024)
	_, _, _, _, first := cache.begin("segment")
	_, _, _, _, second := cache.begin("segment")
	defer first.done()
	defer second.done()

	want := []byte("segment")
	if !cache.store("segment", want, time.Time{}) {
		t.Fatal("initial store failed")
	}

	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				got, _, hit, _, lease := cache.begin("segment")
				if !hit || !bytes.Equal(got, want) {
					errs <- string(got)
					lease.done()
					return
				}
				got[0] = 'x'
				lease.done()
			}
		}()
	}
	for range 100 {
		input := []byte("segment")
		if !cache.store("segment", input, time.Time{}) {
			t.Fatal("concurrent store failed")
		}
		input[0] = 'x'
	}
	wg.Wait()
	close(errs)
	for got := range errs {
		t.Fatalf("cached bytes mutated: %q", got)
	}
}

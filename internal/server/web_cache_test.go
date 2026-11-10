package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"testing/fstest"
)

const immutableCache = "public, max-age=31536000, immutable"

func webCacheFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                 {Data: []byte("<html></html>")},
		"sw.js":                      {Data: []byte("self.x=1;")},
		"favicon.png":                {Data: []byte("png-bytes")},
		"assets/index-Ab3dEf9_.js":   {Data: bytes.Repeat([]byte("console.log(1);\n"), 200)},
		"assets/font-Ab3dEf9_.woff2": {Data: []byte("woff2-bytes")},
		"assets/placeholder.js":      {Data: []byte("plain")},
	}
}

func getWeb(h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://server.test"+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWebAssetImmutableCacheControl(t *testing.T) {
	h := webAppHandlerFS(webCacheFS(), nil)
	for path, want := range map[string]string{
		"/web/assets/index-Ab3dEf9_.js":   immutableCache,
		"/web/assets/font-Ab3dEf9_.woff2": immutableCache,
		"/web/assets/placeholder.js":      "",
		"/web/favicon.png":                "",
		"/web/sw.js":                      "no-cache",
		"/web/":                           "no-store",
	} {
		if got := getWeb(h, path, nil).Header().Get("Cache-Control"); got != want {
			t.Errorf("%s Cache-Control = %q, want %q", path, got, want)
		}
	}
	if rec := getWeb(h, "/web/assets/nope-Ab3dEf9_.js", nil); rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "" {
		t.Errorf("missing asset: status %d cache %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestWebAssetRewrittenBodyIsNotImmutable(t *testing.T) {
	base, _ := url.Parse("https://host.test/prefix/")
	h := webAppHandlerFS(webCacheFS(), base)
	if got := getWeb(h, "/web/assets/index-Ab3dEf9_.js", nil).Header().Get("Cache-Control"); got != "" {
		t.Fatalf("rewritten asset Cache-Control = %q, want none", got)
	}
	if got := getWeb(h, "/web/assets/font-Ab3dEf9_.woff2", nil).Header().Get("Cache-Control"); got != immutableCache {
		t.Fatalf("non-text asset Cache-Control = %q, want immutable", got)
	}
}

func TestWebAssetGzip(t *testing.T) {
	want := webCacheFS()["assets/index-Ab3dEf9_.js"].Data
	for _, base := range []*url.URL{nil, {Path: "/prefix/"}} {
		h := webAppHandlerFS(webCacheFS(), base)
		const p = "/web/assets/index-Ab3dEf9_.js"

		rec := getWeb(h, p, map[string]string{"Accept-Encoding": "br, gzip;q=0.8"})
		if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("base %v: headers %v", base, rec.Header())
		}
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(zr)
		if base == nil && !bytes.Equal(got, want) {
			t.Fatal("decoded body differs from original")
		}
		if rec.Header().Get("Content-Length") == "" || rec.Body.Len() >= len(want) {
			t.Fatalf("content-length %q, encoded %d", rec.Header().Get("Content-Length"), rec.Body.Len())
		}

		for _, hdr := range []map[string]string{nil, {"Accept-Encoding": "gzip;q=0"}, {"Accept-Encoding": "identity"}} {
			rec = getWeb(h, p, hdr)
			if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatalf("identity headers: %v", rec.Header())
			}
			if base == nil && !bytes.Equal(rec.Body.Bytes(), want) {
				t.Fatal("identity body differs")
			}
		}

		rec = getWeb(h, "/web/assets/font-Ab3dEf9_.woff2", map[string]string{"Accept-Encoding": "gzip"})
		if rec.Header().Get("Content-Encoding") != "" {
			t.Fatal("woff2 must not be compressed")
		}
		if rec = getWeb(h, "/web/assets/missing-Ab3dEf9_.js", map[string]string{"Accept-Encoding": "gzip"}); rec.Code != http.StatusNotFound {
			t.Fatalf("missing asset status %d", rec.Code)
		}
	}
}

func TestWebAssetRangeStaysIdentity(t *testing.T) {
	h := webAppHandlerFS(webCacheFS(), nil)
	rec := getWeb(h, "/web/assets/index-Ab3dEf9_.js", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"})
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Encoding") != "" || rec.Body.Len() != 10 {
		t.Fatalf("range: status %d headers %v len %d", rec.Code, rec.Header(), rec.Body.Len())
	}
}

// countingFS counts opens of each name so a test can prove a gzip cache hit
// does not re-read the original asset.
type countingFS struct {
	fs.FS
	mu    sync.Mutex
	opens map[string]int
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.mu.Lock()
	if c.opens == nil {
		c.opens = map[string]int{}
	}
	c.opens[name]++
	c.mu.Unlock()
	return c.FS.Open(name)
}

func (c *countingFS) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens[name]
}

func TestWebAssetGzipHitDoesNotReadOriginal(t *testing.T) {
	const name = "assets/index-Ab3dEf9_.js"
	cfs := &countingFS{FS: webCacheFS()}
	h := webAppHandlerFS(cfs, nil)
	hdr := map[string]string{"Accept-Encoding": "gzip"}

	getWeb(h, "/web/"+name, hdr)
	afterMiss := cfs.count(name)
	if afterMiss == 0 {
		t.Fatal("cache miss never read the original asset")
	}
	rec := getWeb(h, "/web/"+name, hdr)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("second request not gzip: %v", rec.Header())
	}
	if got := cfs.count(name); got != afterMiss+1 {
		// +1 is the fs.Stat existence check; the body must not be read again.
		t.Fatalf("opens after hit = %d, want %d", got, afterMiss+1)
	}
}

package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
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
	if got := getWeb(h, "/web/assets/index-Ab3dEf9_.js", nil).Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("rewritten asset Cache-Control = %q, want no-cache", got)
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

// Un-hashed compressible files revalidate (sw.js is no-cache), so each
// representation carries a strong ETag and a matching If-None-Match gets 304.
func TestWebAssetETagRevalidation(t *testing.T) {
	h := webAppHandlerFS(webCacheFS(), nil)
	const p = "/web/sw.js"
	identity := getWeb(h, p, nil)
	gzipped := getWeb(h, p, map[string]string{"Accept-Encoding": "gzip"})
	idTag, gzTag := identity.Header().Get("ETag"), gzipped.Header().Get("ETag")
	if idTag == "" || gzTag == "" || idTag == gzTag || strings.HasPrefix(idTag, "W/") {
		t.Fatalf("ETags identity %q gzip %q, want distinct strong validators", idTag, gzTag)
	}
	for _, tc := range []struct {
		tag string
		hdr map[string]string
	}{
		{idTag, map[string]string{"If-None-Match": `"other", ` + idTag}},
		{gzTag, map[string]string{"If-None-Match": "W/" + gzTag, "Accept-Encoding": "gzip"}},
	} {
		rec := getWeb(h, p, tc.hdr)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Fatalf("%v: status %d body %d, want 304", tc.hdr, rec.Code, rec.Body.Len())
		}
		if rec.Header().Get("ETag") != tc.tag || rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("304 headers %v", rec.Header())
		}
	}
	if rec := getWeb(h, p, map[string]string{"If-None-Match": gzTag}); rec.Code != http.StatusOK || rec.Body.String() != "self.x=1;" {
		t.Fatalf("gzip validator matched the identity representation: %d", rec.Code)
	}
	if tag := getWeb(h, "/web/assets/index-Ab3dEf9_.js", map[string]string{"Accept-Encoding": "gzip"}).Header().Get("ETag"); tag != "" {
		t.Fatalf("immutable asset ETag = %q, want none", tag)
	}
}

// blockingReadFS stalls reads of one file until released, so a test can hold
// that asset's first compression in flight.
type blockingReadFS struct {
	fs.FS
	name    string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingReadFS) Open(name string) (fs.File, error) {
	f, err := b.FS.Open(name)
	if err != nil || name != b.name {
		return f, err
	}
	return &blockingFile{File: f, fs: b}, nil
}

type blockingFile struct {
	fs.File
	fs *blockingReadFS
}

func (f *blockingFile) Read(p []byte) (int, error) {
	f.fs.once.Do(func() {
		close(f.fs.entered)
		<-f.fs.release
	})
	return f.File.Read(p)
}

// A browser's parallel first requests after a restart must not queue behind
// one another: while one asset's compression is in flight, another completes.
func TestWebAssetGzipCompressesAssetsConcurrently(t *testing.T) {
	bfs := &blockingReadFS{FS: webCacheFS(), name: "assets/index-Ab3dEf9_.js", entered: make(chan struct{}), release: make(chan struct{})}
	h := webAppHandlerFS(bfs, nil)
	hdr := map[string]string{"Accept-Encoding": "gzip"}

	slow := make(chan *httptest.ResponseRecorder)
	go func() { slow <- getWeb(h, "/web/assets/index-Ab3dEf9_.js", hdr) }()
	<-bfs.entered

	fast := make(chan *httptest.ResponseRecorder)
	go func() { fast <- getWeb(h, "/web/sw.js", hdr) }()
	select {
	case rec := <-fast:
		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("sw.js headers %v", rec.Header())
		}
	case <-time.After(5 * time.Second):
		close(bfs.release)
		t.Fatal("sw.js waited behind another asset's compression")
	}
	close(bfs.release)
	if rec := <-slow; rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("slow asset headers %v", rec.Header())
	}
}

// TestWebAssetRewrittenRevalidates pins CORE-1161: a file rewritten for a base
// path is rewritten once, carries a strong validator per representation, and a
// repeat load is a 304 rather than the whole file again.
func TestWebAssetRewrittenRevalidates(t *testing.T) {
	const name = "assets/app-Ab3dEf9_.js"
	assets := webCacheFS()
	assets[name] = &fstest.MapFile{Data: bytes.Repeat([]byte(`fetch("/api/graphql");`+"\n"), 100)}
	want := bytes.Repeat([]byte(`fetch("/prefix/api/graphql");`+"\n"), 100)
	cfs := &countingFS{FS: assets}
	base, _ := url.Parse("https://host.test/prefix")
	h := webAppHandlerFS(cfs, base)
	afterStart := cfs.count(name)

	gzipHdr := map[string]string{"Accept-Encoding": "gzip"}
	identity := getWeb(h, "/web/"+name, nil)
	gzipped := getWeb(h, "/web/"+name, gzipHdr)
	if identity.Code != http.StatusOK || !bytes.Equal(identity.Body.Bytes(), want) {
		t.Fatalf("identity: status %d, body is not the rewritten file", identity.Code)
	}
	zr, err := gzip.NewReader(gzipped.Body)
	if err != nil {
		t.Fatalf("gzip body: %v", err)
	}
	if got, _ := io.ReadAll(zr); !bytes.Equal(got, want) {
		t.Fatal("gzip body does not decode to the rewritten file")
	}
	idTag, gzTag := identity.Header().Get("ETag"), gzipped.Header().Get("ETag")
	if idTag == "" || gzTag == "" || idTag == gzTag || strings.HasPrefix(idTag, "W/") || strings.HasPrefix(gzTag, "W/") {
		t.Fatalf("ETags identity %q gzip %q, want distinct strong validators", idTag, gzTag)
	}
	for _, rec := range []*httptest.ResponseRecorder{identity, gzipped} {
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("Cache-Control = %q, want no-cache", got)
		}
	}

	for _, tc := range []struct {
		tag string
		hdr map[string]string
	}{
		{idTag, map[string]string{"If-None-Match": idTag}},
		{gzTag, map[string]string{"If-None-Match": gzTag, "Accept-Encoding": "gzip"}},
	} {
		rec := getWeb(h, "/web/"+name, tc.hdr)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Fatalf("%v: status %d body %d, want 304 and no body", tc.hdr, rec.Code, rec.Body.Len())
		}
		if rec.Header().Get("ETag") != tc.tag || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("304 headers %v", rec.Header())
		}
	}
	if rec := getWeb(h, "/web/"+name, map[string]string{"If-None-Match": gzTag}); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("gzip validator matched the identity representation: %d", rec.Code)
	}

	// Five requests, five existence checks: the original is never read again,
	// so nothing is rewritten or compressed per request.
	if got := cfs.count(name); got != afterStart+5 {
		t.Fatalf("opens after serving = %d, want %d", got, afterStart+5)
	}

	// A different base path yields different bytes and so different validators.
	other, _ := url.Parse("https://host.test/elsewhere")
	if tag := getWeb(webAppHandlerFS(assets, other), "/web/"+name, nil).Header().Get("ETag"); tag == "" || tag == idTag {
		t.Fatalf("ETag under another base path = %q, want a different validator from %q", tag, idTag)
	}
}

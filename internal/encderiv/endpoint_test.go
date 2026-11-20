package encderiv

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memSource struct{ *bytes.Reader }

func (memSource) Close() error { return nil }

// countingOpener serves data and counts the sources it opened.
type countingOpener struct {
	data  []byte
	opens atomic.Int32
	wrap  func(*bytes.Reader) io.ReadSeekCloser
}

func (o *countingOpener) open() (io.ReadSeekCloser, error) {
	o.opens.Add(1)
	r := bytes.NewReader(o.data)
	if o.wrap != nil {
		return o.wrap(r), nil
	}
	return memSource{r}, nil
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_800_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type endpointFixture struct {
	ep     *endpoint
	clock  *fakeClock
	source *countingOpener
}

func newEndpointFixture(t *testing.T, data []byte, size int64, peer peerChecker) *endpointFixture {
	t.Helper()
	clock := newFakeClock()
	source := &countingOpener{data: data}
	ep, err := startEndpoint(endpointConfig{
		open:     source.open,
		size:     size,
		deadline: clock.Now().Add(time.Hour),
		idle:     time.Minute,
		now:      clock.Now,
		peer:     peer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Revoke)
	return &endpointFixture{ep: ep, clock: clock, source: source}
}

func testClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type response struct {
	status int
	header http.Header
	body   []byte
	err    error
}

func (f *endpointFixture) do(t *testing.T, method, path string, edit func(*http.Request)) response {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+f.ep.host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(req)
	}
	resp, err := testClient().Do(req)
	if err != nil {
		t.Fatalf("%s: %s", method, strings.ReplaceAll(err.Error(), f.ep.token, "TOKEN"))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: body, err: err}
}

func (f *endpointFixture) sourcePath() string { return "/" + f.ep.token + "/" + sourcePath }

func withRange(spec string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Range", spec) }
}

func TestEndpointServesFullAndRangedReads(t *testing.T) {
	data := testPayload(3*copyChunkBytes + 17)
	size := int64(len(data))
	f := newEndpointFixture(t, data, size, nil)

	full := f.do(t, http.MethodGet, f.sourcePath(), nil)
	if full.err != nil || full.status != http.StatusOK || !bytes.Equal(full.body, data) {
		t.Fatalf("full read: status %d, %d bytes, err %v", full.status, len(full.body), full.err)
	}
	if full.header.Get("Accept-Ranges") != "bytes" || full.header.Get("Cache-Control") != "no-store" ||
		full.header.Get("Content-Length") != strconv.FormatInt(size, 10) {
		t.Fatalf("full read headers: %v", full.header)
	}

	for _, tc := range []struct {
		spec       string
		start, end int64
	}{
		{"bytes=70000-140000", 70000, 140000},
		{"bytes=0-0", 0, 0},
		{"bytes=" + strconv.FormatInt(size-10, 10) + "-", size - 10, size - 1},
		{"bytes=5-99999999999", 5, size - 1},
		{"bytes=" + strconv.FormatInt(copyChunkBytes, 10) + "-", copyChunkBytes, size - 1},
	} {
		got := f.do(t, http.MethodGet, f.sourcePath(), withRange(tc.spec))
		want := data[tc.start : tc.end+1]
		if got.err != nil || got.status != http.StatusPartialContent || !bytes.Equal(got.body, want) {
			t.Fatalf("%s: status %d, %d bytes (want %d), err %v", tc.spec, got.status, len(got.body), len(want), got.err)
		}
		wantRange := "bytes " + strconv.FormatInt(tc.start, 10) + "-" + strconv.FormatInt(tc.end, 10) + "/" + strconv.FormatInt(size, 10)
		if got.header.Get("Content-Range") != wantRange {
			t.Fatalf("%s: Content-Range %q, want %q", tc.spec, got.header.Get("Content-Range"), wantRange)
		}
	}

	head := f.do(t, http.MethodHead, f.sourcePath(), nil)
	if head.status != http.StatusOK || len(head.body) != 0 || head.header.Get("Content-Length") != strconv.FormatInt(size, 10) {
		t.Fatalf("HEAD: status %d, %d bytes, headers %v", head.status, len(head.body), head.header)
	}
	if err := f.ep.Err(); err != nil {
		t.Fatalf("Err() = %v after valid reads", err)
	}
	if got := f.source.opens.Load(); got != 7 {
		t.Fatalf("opened the source %d times, want one per request (7)", got)
	}
}

func TestEndpointRefusesUnsatisfiableRanges(t *testing.T) {
	data := testPayload(1000)
	f := newEndpointFixture(t, data, int64(len(data)), nil)
	for _, spec := range []string{
		"bytes=-10",       // suffix
		"bytes=0-1,4-5",   // multiple ranges
		"bytes=1000-",     // starts at the end
		"bytes=5000-6000", // beyond the end
		"bytes=10-5",      // reversed
		"items=0-1",       // other unit
		"bytes=a-b",
		"bytes=+1-2",
		"bytes=",
		"bytes=1",
	} {
		got := f.do(t, http.MethodGet, f.sourcePath(), withRange(spec))
		if got.status != http.StatusRequestedRangeNotSatisfiable || got.header.Get("Content-Range") != "bytes */1000" {
			t.Fatalf("%q: status %d, Content-Range %q", spec, got.status, got.header.Get("Content-Range"))
		}
	}
	if got := f.source.opens.Load(); got != 0 {
		t.Fatalf("an unsatisfiable range opened the source %d times", got)
	}
	if err := f.ep.Err(); err != nil {
		t.Fatalf("Err() = %v; an unsatisfiable range is not a token misuse", err)
	}
}

func TestParseRange(t *testing.T) {
	const size = 100
	for _, tc := range []struct {
		header           string
		start, end       int64
		partial, wantsOK bool
	}{
		{"", 0, 99, false, true},
		{"bytes=0-", 0, 99, true, true},
		{"bytes=10-20", 10, 20, true, true},
		{"bytes=99-", 99, 99, true, true},
		{"bytes=50-500", 50, 99, true, true},
		{"bytes=100-", 0, 0, false, false},
		{"bytes=-5", 0, 0, false, false},
		{"bytes=0-1,2-3", 0, 0, false, false},
		{"bytes=5-4", 0, 0, false, false},
		{"bytes=1234567890123456789-", 0, 0, false, false},
		{"bytes= 1-2", 0, 0, false, false},
		{"Bytes=1-2", 0, 0, false, false},
	} {
		start, end, partial, ok := parseRange(tc.header, size)
		if ok != tc.wantsOK || (ok && (start != tc.start || end != tc.end || partial != tc.partial)) {
			t.Fatalf("parseRange(%q) = %d,%d,%v,%v; want %d,%d,%v,%v",
				tc.header, start, end, partial, ok, tc.start, tc.end, tc.partial, tc.wantsOK)
		}
	}
}

func TestEndpointRefusesWrongToken(t *testing.T) {
	data := testPayload(100)
	f := newEndpointFixture(t, data, int64(len(data)), nil)
	other := strings.Repeat("0", len(f.ep.token))
	for _, path := range []string{
		"/" + other + "/" + sourcePath,
		"/" + f.ep.token[:len(f.ep.token)-1] + "/" + sourcePath,
		"/" + strings.ToUpper(f.ep.token) + "/" + sourcePath,
		"/" + f.ep.token + "/other",
		"/" + f.ep.token + "/" + sourcePath + "/more",
		"/" + f.ep.token,
		"/" + sourcePath,
		"/",
	} {
		got := f.do(t, http.MethodGet, path, nil)
		if got.status != http.StatusNotFound {
			t.Fatalf("status %d for a wrong token", got.status)
		}
		if bytes.Contains(got.body, []byte(f.ep.token)) {
			t.Fatal("refusal echoed the token")
		}
	}
	if f.source.opens.Load() != 0 || f.ep.Err() != nil {
		t.Fatal("a wrong token reached the source or failed the job")
	}
}

func TestEndpointRefusesBadMethodAndFailsJob(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			f := newEndpointFixture(t, testPayload(100), 100, nil)
			got := f.do(t, method, f.sourcePath(), nil)
			if got.status != http.StatusMethodNotAllowed || got.header.Get("Allow") != "GET, HEAD" {
				t.Fatalf("status %d, Allow %q", got.status, got.header.Get("Allow"))
			}
			if !errors.Is(f.ep.Err(), errForbidden) || f.source.opens.Load() != 0 {
				t.Fatalf("Err() = %v, opens %d; a misused token must fail the job unread", f.ep.Err(), f.source.opens.Load())
			}
		})
	}
}

func TestEndpointRefusesForeignHost(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "evil.example", "[::1]"} {
		t.Run(host, func(t *testing.T) {
			f := newEndpointFixture(t, testPayload(100), 100, nil)
			got := f.do(t, http.MethodGet, f.sourcePath(), func(r *http.Request) {
				if strings.Contains(host, ":") || host == "localhost" || host == "evil.example" {
					r.Host = host + ":" + strconv.Itoa(f.ep.Port())
				} else {
					r.Host = host
				}
			})
			if got.status != http.StatusForbidden || !errors.Is(f.ep.Err(), errForbidden) || f.source.opens.Load() != 0 {
				t.Fatalf("status %d, Err() %v, opens %d", got.status, f.ep.Err(), f.source.opens.Load())
			}
		})
	}
}

func TestEndpointRefusesNonLoopbackPeer(t *testing.T) {
	f := newEndpointFixture(t, testPayload(100), 100, nil)
	for _, remote := range []string{"192.0.2.10:4000", "[2001:db8::1]:4000", "10.0.0.1:80", "not-an-address"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+f.ep.host+f.sourcePath(), nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		f.ep.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403", remote, rec.Code)
		}
	}
	if f.source.opens.Load() != 0 || f.ep.Err() != nil {
		t.Fatal("a non-loopback peer reached the source or failed the job")
	}
}

func TestEndpointPeerCheck(t *testing.T) {
	var gotPeer, gotServer netip.AddrPort
	refuse := newEndpointFixture(t, testPayload(100), 100, func(peer, server netip.AddrPort) error {
		gotPeer, gotServer = peer, server
		return errors.New("foreign uid")
	})
	got := refuse.do(t, http.MethodGet, refuse.sourcePath(), nil)
	if got.status != http.StatusForbidden || !errors.Is(refuse.ep.Err(), errForbidden) || refuse.source.opens.Load() != 0 {
		t.Fatalf("status %d, Err() %v, opens %d", got.status, refuse.ep.Err(), refuse.source.opens.Load())
	}
	if gotServer != refuse.ep.server || !gotPeer.Addr().IsLoopback() || gotPeer.Port() == gotServer.Port() {
		t.Fatalf("peer check saw peer %v server %v, want the client socket and %v", gotPeer, gotServer, refuse.ep.server)
	}

	accept := newEndpointFixture(t, testPayload(100), 100, func(netip.AddrPort, netip.AddrPort) error { return nil })
	if got := accept.do(t, http.MethodGet, accept.sourcePath(), nil); got.status != http.StatusOK {
		t.Fatalf("status %d with an accepting peer check", got.status)
	}
}

func TestEndpointRefusesAfterExpiry(t *testing.T) {
	for name, advance := range map[string]time.Duration{
		"ttl":  time.Hour,
		"idle": time.Minute + time.Second,
	} {
		t.Run(name, func(t *testing.T) {
			f := newEndpointFixture(t, testPayload(100), 100, nil)
			if got := f.do(t, http.MethodGet, f.sourcePath(), nil); got.status != http.StatusOK {
				t.Fatalf("status %d before expiry", got.status)
			}
			f.clock.Advance(advance)
			if got := f.do(t, http.MethodGet, f.sourcePath(), nil); got.status != http.StatusGone {
				t.Fatalf("status %d after expiry, want 410", got.status)
			}
			if !errors.Is(f.ep.Err(), ErrExpired) {
				t.Fatalf("Err() = %v, want ErrExpired", f.ep.Err())
			}
			// Expiry is final: winding the clock back does not revive the token.
			f.clock.Advance(-advance)
			if got := f.do(t, http.MethodGet, f.sourcePath(), nil); got.status != http.StatusGone {
				t.Fatalf("status %d after the clock moved back, want 410", got.status)
			}
			if got := f.source.opens.Load(); got != 1 {
				t.Fatalf("opened the source %d times, want once", got)
			}
		})
	}
}

func TestEndpointRefusesAfterRevoke(t *testing.T) {
	f := newEndpointFixture(t, testPayload(100), 100, nil)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(f.ep.Revoke)
	}
	wg.Wait()
	f.ep.Revoke()

	req, err := http.NewRequest(http.MethodGet, f.ep.URL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := testClient().Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("revoked endpoint still accepts connections (status %d)", resp.StatusCode)
	}

	direct := httptest.NewRequest(http.MethodGet, "http://"+f.ep.host+f.sourcePath(), nil)
	direct.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	f.ep.ServeHTTP(rec, direct)
	if rec.Code != http.StatusGone || !errors.Is(f.ep.Err(), ErrRevoked) || f.source.opens.Load() != 0 {
		t.Fatalf("status %d, Err() %v, opens %d", rec.Code, f.ep.Err(), f.source.opens.Load())
	}
}

// gatedSource blocks reads at or past gateAt until release closes.
type gatedSource struct {
	*bytes.Reader
	gateAt  int64
	release <-chan struct{}
}

func (g *gatedSource) Read(p []byte) (int, error) {
	if pos, _ := g.Seek(0, io.SeekCurrent); pos >= g.gateAt {
		<-g.release
	}
	return g.Reader.Read(p)
}

func (*gatedSource) Close() error { return nil }

func TestEndpointRevokeAbortsBodyMidStream(t *testing.T) {
	data := testPayload(4 * copyChunkBytes)
	f := newEndpointFixture(t, data, int64(len(data)), nil)
	release := make(chan struct{})
	f.source.wrap = func(r *bytes.Reader) io.ReadSeekCloser {
		return &gatedSource{Reader: r, gateAt: copyChunkBytes, release: release}
	}
	resp, err := testClient().Get(f.ep.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	first := make([]byte, copyChunkBytes)
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}

	revoked := make(chan struct{})
	go func() {
		f.ep.Revoke()
		close(revoked)
	}()
	// Revoke closes the connection while the handler waits on its source.
	rest, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatal("body completed after revocation")
	}
	close(release)
	<-revoked
	if got := len(first) + len(rest); got >= len(data) {
		t.Fatalf("read %d of %d bytes after revocation", got, len(data))
	}
	if !errors.Is(f.ep.Err(), ErrRevoked) {
		t.Fatalf("Err() = %v, want ErrRevoked", f.ep.Err())
	}
}

type interruptedSource struct {
	memSource
	wait  func()
	stage string
	reads int
}

func (s *interruptedSource) Seek(offset int64, whence int) (int64, error) {
	if s.stage == "seek" {
		s.wait()
	}
	return s.memSource.Seek(offset, whence)
}

func (s *interruptedSource) Read(p []byte) (int, error) {
	s.reads++
	if s.stage == "partial read" && s.reads == 1 {
		s.wait()
		return s.memSource.Read(p[:1])
	}
	return s.memSource.Read(p)
}

func TestEndpointCutoffPreventsFurtherSourceReads(t *testing.T) {
	for _, stage := range []string{"seek", "partial read"} {
		t.Run(stage, func(t *testing.T) {
			data := testPayload(32)
			f := newEndpointFixture(t, data, int64(len(data)), nil)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			src := &interruptedSource{memSource: memSource{bytes.NewReader(data)}, stage: stage, wait: func() {
				close(entered)
				<-release
			}}
			w := httptest.NewRecorder()
			done := make(chan any, 1)
			go func() {
				defer func() { done <- recover() }()
				f.ep.serveRange(w, httptest.NewRequest(http.MethodGet, "/", nil), func() (io.ReadSeekCloser, error) {
					return src, nil
				})
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("source did not reach the cutoff window")
			}
			f.ep.Abort()
			unblock()
			select {
			case stopped := <-done:
				if stage == "seek" && (stopped != nil || src.reads != 0 || w.Code != http.StatusGone) {
					t.Fatalf("seek cutoff: panic %v, reads %d, status %d", stopped, src.reads, w.Code)
				}
				if stage == "partial read" && (stopped != http.ErrAbortHandler || src.reads != 1 || w.Body.Len() != 0) {
					t.Fatalf("partial read cutoff: panic %v, reads %d, bytes %d", stopped, src.reads, w.Body.Len())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cutoff did not end the source response")
			}
		})
	}
}

// hookSource runs onRead once, before the first read at or past at.
type hookSource struct {
	*bytes.Reader
	at     int64
	once   sync.Once
	onRead func()
}

func (h *hookSource) Read(p []byte) (int, error) {
	if pos, _ := h.Seek(0, io.SeekCurrent); pos >= h.at {
		h.once.Do(h.onRead)
	}
	return h.Reader.Read(p)
}

func (*hookSource) Close() error { return nil }

func TestEndpointExpiryAbortsBodyMidStream(t *testing.T) {
	data := testPayload(4 * copyChunkBytes)
	f := newEndpointFixture(t, data, int64(len(data)), nil)
	f.source.wrap = func(r *bytes.Reader) io.ReadSeekCloser {
		return &hookSource{Reader: r, at: copyChunkBytes, onRead: func() { f.clock.Advance(2 * time.Hour) }}
	}
	got := f.do(t, http.MethodGet, f.sourcePath(), nil)
	if got.err == nil || len(got.body) != copyChunkBytes {
		t.Fatalf("read %d of %d bytes (err %v) past the token's expiry", len(got.body), len(data), got.err)
	}
	if !errors.Is(f.ep.Err(), ErrExpired) {
		t.Fatalf("Err() = %v, want ErrExpired", f.ep.Err())
	}
}

// failingSource returns err once its reader is exhausted.
type failingSource struct {
	*bytes.Reader
	err error
}

func (s *failingSource) Read(p []byte) (int, error) {
	n, err := s.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		return n, s.err
	}
	return n, err
}

func (*failingSource) Close() error { return nil }

func TestEndpointSourceFailureFailsJob(t *testing.T) {
	data := testPayload(2 * copyChunkBytes)
	t.Run("open fails", func(t *testing.T) {
		clock := newFakeClock()
		ep, err := startEndpoint(endpointConfig{
			open:     func() (io.ReadSeekCloser, error) { return nil, errors.New("sealed") },
			size:     int64(len(data)),
			deadline: clock.Now().Add(time.Hour),
			idle:     time.Minute,
			now:      clock.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer ep.Revoke()
		f := &endpointFixture{ep: ep, clock: clock}
		if got := f.do(t, http.MethodGet, f.sourcePath(), nil); got.status != http.StatusInternalServerError {
			t.Fatalf("status %d, want 500", got.status)
		}
		if !errors.Is(ep.Err(), errSourceFailure) {
			t.Fatalf("Err() = %v, want errSourceFailure", ep.Err())
		}
	})
	t.Run("source shorter than declared", func(t *testing.T) {
		f := newEndpointFixture(t, data[:copyChunkBytes+10], int64(len(data)), nil)
		got := f.do(t, http.MethodGet, f.sourcePath(), nil)
		if got.err == nil || !errors.Is(f.ep.Err(), errSourceFailure) {
			t.Fatalf("read err %v, Err() %v; a short source must fail the job", got.err, f.ep.Err())
		}
	})
	t.Run("source fails authentication", func(t *testing.T) {
		f := newEndpointFixture(t, data[:copyChunkBytes+10], int64(len(data)), nil)
		f.source.wrap = func(r *bytes.Reader) io.ReadSeekCloser {
			return &failingSource{Reader: r, err: errors.New("chunk failed authentication")}
		}
		got := f.do(t, http.MethodGet, f.sourcePath(), nil)
		if got.err == nil || !errors.Is(f.ep.Err(), errSourceFailure) {
			t.Fatalf("read err %v, Err() %v; a source error must fail the job", got.err, f.ep.Err())
		}
	})
}

func TestEndpointTokenIsFreshPerJobAndLoopbackOnly(t *testing.T) {
	a := newEndpointFixture(t, nil, 0, nil)
	b := newEndpointFixture(t, nil, 0, nil)
	if len(a.ep.token) != 2*tokenBytes || a.ep.token == b.ep.token {
		t.Fatalf("tokens %d hex chars, equal %v; want 64 and fresh per job", len(a.ep.token), a.ep.token == b.ep.token)
	}
	if !a.ep.server.Addr().IsLoopback() || !a.ep.server.Addr().Is4() {
		t.Fatalf("endpoint listens on %v, want IPv4 loopback", a.ep.server)
	}
	want := "http://127.0.0.1:" + strconv.Itoa(a.ep.Port()) + "/" + a.ep.token + "/" + sourcePath
	if a.ep.URL() != want {
		t.Fatal("URL is not the loopback source path under the token")
	}
}

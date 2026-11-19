package encderiv

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

const testLiveRung = "r"

func acceptPeer(netip.AddrPort, netip.AddrPort) error { return nil }

// newTestLiveSession returns a session over a fresh directory holding the
// test rung's directory, as the session engine lays it out.
func newTestLiveSession(t *testing.T) *LiveSession {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, testLiveRung), 0o700); err != nil {
		t.Fatal(err)
	}
	session, err := NewLiveSession(dir, "lib-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session
}

type liveRunFixture struct {
	run     *LiveRun
	session *LiveSession
}

func newLiveRunFixture(t *testing.T, idle time.Duration) *liveRunFixture {
	t.Helper()
	session := newTestLiveSession(t)
	source := &countingOpener{data: testPayload(1000)}
	run, err := StartLiveRun(LiveRunConfig{
		Session:     session,
		Rung:        testLiveRung,
		Open:        source.open,
		Size:        1000,
		IdleTimeout: idle,
		peer:        acceptPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Revoke)
	return &liveRunFixture{run: run, session: session}
}

func (f *liveRunFixture) put(t *testing.T, name string, body []byte, edit func(*http.Request)) response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, f.run.OutputURL()+"/"+name, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(req)
	}
	return f.send(t, req)
}

func (f *liveRunFixture) send(t *testing.T, req *http.Request) response {
	t.Helper()
	got, err := f.do(req)
	if err != nil {
		t.Fatalf("%s: %s", req.Method, strings.ReplaceAll(err.Error(), f.run.Secret(), "TOKEN"))
	}
	return got
}

// do sends req and returns the transport error, if any, instead of failing.
func (f *liveRunFixture) do(req *http.Request) (response, error) {
	resp, err := testClient().Do(req)
	if err != nil {
		return response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: got, err: err}, nil
}

// sessionFiles lists every file under the session directory, slash-separated.
func sessionFiles(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		names = append(names, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestLiveSessionStoresCiphertextBoundToItsName(t *testing.T) {
	session := newTestLiveSession(t)
	plain := []byte("#EXTM3U\n#EXT-X-VERSION:7\n" + strings.Repeat("plain playlist line\n", 20))
	if err := session.WriteFile(testLiveRung+"/index.m3u8", plain); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(session.Dir(), testLiveRung, "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("plain playlist line")) || bytes.Contains(raw, []byte("#EXTM3U")) {
		t.Fatal("the stored file holds plaintext")
	}
	f, err := session.Open(testLiveRung + "/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || !bytes.Equal(got, plain) || f.Size() != int64(len(plain)) {
		t.Fatalf("decrypted %d bytes (size %d), err %v", len(got), f.Size(), err)
	}
	// A file moved to another name fails authentication.
	if err := os.Rename(filepath.Join(session.Dir(), testLiveRung, "index.m3u8"), filepath.Join(session.Dir(), testLiveRung, "other.m3u8")); err != nil {
		t.Fatal(err)
	}
	if f, err := session.Open(testLiveRung + "/other.m3u8"); err == nil {
		_ = f.Close()
		t.Fatal("a renamed file opened")
	}
	if names := sessionFiles(t, session.Dir()); len(names) != 1 {
		t.Fatalf("files %v: WriteFile left a temporary file", names)
	}
}

func TestLiveSessionRefusesUnsafeNames(t *testing.T) {
	session := newTestLiveSession(t)
	for _, rel := range []string{"", "/abs", "../x", "r/../../x", "r/./x", "r//x", `r\x`, "r/x.part", "x.part/y"} {
		if err := session.WriteFile(rel, []byte("x")); err == nil {
			t.Fatalf("WriteFile(%q) succeeded", rel)
		}
		if _, err := session.Open(rel); err == nil {
			t.Fatalf("Open(%q) succeeded", rel)
		}
	}
	if _, ok := session.Rel(filepath.Dir(session.Dir())); ok {
		t.Fatal("Rel accepted the parent directory")
	}
	if _, ok := session.Rel(session.Dir()); ok {
		t.Fatal("Rel accepted the session directory itself")
	}
	if rel, ok := session.Rel(filepath.Join(session.Dir(), testLiveRung, "seg00001.m4s")); !ok || rel != testLiveRung+"/seg00001.m4s" {
		t.Fatalf("Rel = %q, %v", rel, ok)
	}
	if _, err := NewLiveSession("relative", "lib"); err == nil {
		t.Fatal("a relative session directory was accepted")
	}
	if _, err := NewLiveSession(t.TempDir(), ""); err == nil {
		t.Fatal("a session without a Library was accepted")
	}
}

func TestLiveSessionCloseZeroesTheKey(t *testing.T) {
	session := newTestLiveSession(t)
	if err := session.WriteFile(testLiveRung+"/a", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	open, err := session.Open(testLiveRung + "/a")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = open.Close() }()
	buf := make([]byte, 3)
	if _, err := io.ReadFull(open, buf); err != nil || string(buf) != "pay" {
		t.Fatalf("read before Close: %q, %v", buf, err)
	}
	key := session.key
	session.Close()
	session.Close()
	// A reader still streaming the file stops: no plaintext after Close.
	if n, err := open.Read(buf); n != 0 || !errors.Is(err, ErrLiveSessionClosed) {
		t.Fatalf("Read after Close: %d, %v", n, err)
	}
	if n, err := open.ReadAt(buf, 0); n != 0 || !errors.Is(err, ErrLiveSessionClosed) {
		t.Fatalf("ReadAt after Close: %d, %v", n, err)
	}
	if _, err := open.Seek(0, io.SeekStart); !errors.Is(err, ErrLiveSessionClosed) {
		t.Fatalf("Seek after Close: %v", err)
	}
	if err := open.Close(); err != nil {
		t.Fatalf("Close of an ended file: %v", err)
	}
	if !bytes.Equal(key, make([]byte, len(key))) || session.key != nil {
		t.Fatal("Close left key material behind")
	}
	if _, err := session.Open(testLiveRung + "/a"); !errors.Is(err, ErrLiveSessionClosed) {
		t.Fatalf("Open after Close: %v", err)
	}
	if err := session.WriteFile(testLiveRung+"/b", []byte("x")); !errors.Is(err, ErrLiveSessionClosed) {
		t.Fatalf("WriteFile after Close: %v", err)
	}
	if names := sessionFiles(t, session.Dir()); len(names) != 1 {
		t.Fatalf("files %v after a refused write", names)
	}
}

func TestLiveRunStoresUploadsEncrypted(t *testing.T) {
	f := newLiveRunFixture(t, time.Minute)
	init := append([]byte("\x00\x00\x00\x18ftypiso6"), testPayload(2000)...)
	segment := append([]byte("\x00\x00\x00\x18moof"), testPayload(50000)...)
	for name, body := range map[string][]byte{liveInitName: init, "seg00000.m4s": segment, "seg123456.m4s": segment} {
		if got := f.put(t, name, body, nil); got.status != http.StatusCreated || got.header.Get("Cache-Control") != "no-store" {
			t.Fatalf("PUT %s: status %d", name, got.status)
		}
		raw, err := os.ReadFile(filepath.Join(f.session.Dir(), testLiveRung, name)) // #nosec G304 -- path is beneath a test-owned temporary directory.
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, body[4:8]) || bytes.Contains(raw, body[100:132]) {
			t.Fatalf("%s is stored as plaintext", name)
		}
		lf, err := f.session.Open(testLiveRung + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(lf)
		_ = lf.Close()
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("%s decrypted to %d bytes, err %v", name, len(got), err)
		}
	}
	// The muxer's own playlist is read and discarded.
	if got := f.put(t, livePlaylistName, []byte("#EXTM3U\n"), nil); got.status != http.StatusCreated {
		t.Fatalf("PUT playlist: status %d", got.status)
	}
	if _, err := os.Stat(filepath.Join(f.session.Dir(), testLiveRung, livePlaylistName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the muxer's playlist was stored: %v", err)
	}
	// A restarted encoder re-sends init.mp4: the first copy stays and the run
	// goes on.
	if got := f.put(t, liveInitName, []byte("second"), nil); got.status != http.StatusConflict {
		t.Fatalf("second PUT init: status %d", got.status)
	}
	lf, err := f.session.Open(testLiveRung + "/" + liveInitName)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(lf)
	_ = lf.Close()
	if !bytes.Equal(got, init) {
		t.Fatal("a second upload replaced the first")
	}
	if err := f.run.Err(); err != nil {
		t.Fatalf("Err() = %v after well-formed uploads", err)
	}
	if names := sessionFiles(t, f.session.Dir()); len(names) != 3 {
		t.Fatalf("files %v", names)
	}
	// The source stays readable on the same token.
	req, err := http.NewRequest(http.MethodGet, f.run.SourceURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.send(t, req); got.status != http.StatusOK || len(got.body) != 1000 {
		t.Fatalf("GET source: status %d, %d bytes", got.status, len(got.body))
	}
}

func TestLiveRunRefusesUploadsOffItsList(t *testing.T) {
	cases := map[string]string{
		"other name":    "out/" + testLiveRung + "/other.mp4",
		"short segment": "out/" + testLiveRung + "/seg1.m4s",
		"ts segment":    "out/" + testLiveRung + "/seg00000.ts",
		"part name":     "out/" + testLiveRung + "/seg00000.m4s.part",
		"other rung":    "out/other/" + liveInitName,
		"nested":        "out/" + testLiveRung + "/sub/" + liveInitName,
		"escape":        "out/" + testLiveRung + "/../" + liveInitName,
		"encoded dots":  "out/" + testLiveRung + "/%2e%2e",
		"bare out":      "out/" + testLiveRung + "/",
	}
	for name, resource := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLiveRunFixture(t, time.Minute)
			req, err := http.NewRequest(http.MethodPut, "http://"+f.run.ep.host+"/"+f.run.Secret()+"/x", bytes.NewReader([]byte("data")))
			if err != nil {
				t.Fatal(err)
			}
			req.URL.Opaque = "/" + f.run.Secret() + "/" + resource
			got := f.send(t, req)
			if got.status != http.StatusNotFound || !errors.Is(f.run.Err(), errForbidden) {
				t.Fatalf("status %d, Err() %v", got.status, f.run.Err())
			}
			if bytes.Contains(got.body, []byte(f.run.Secret())) {
				t.Fatal("refusal echoed the token")
			}
			if names := sessionFiles(t, f.session.Dir()); len(names) != 0 {
				t.Fatalf("a refused upload left %v", names)
			}
		})
	}
}

func TestLiveRunRefusesWrongMethods(t *testing.T) {
	for _, tc := range []struct {
		method, resource, allow string
	}{
		{http.MethodGet, "out/" + testLiveRung + "/" + liveInitName, http.MethodPut},
		{http.MethodPost, "out/" + testLiveRung + "/" + liveInitName, http.MethodPut},
		{http.MethodDelete, "out/" + testLiveRung + "/seg00000.m4s", http.MethodPut},
		{http.MethodPut, sourcePath, "GET, HEAD"},
	} {
		t.Run(tc.method+" "+tc.resource, func(t *testing.T) {
			f := newLiveRunFixture(t, time.Minute)
			req, err := http.NewRequest(tc.method, "http://"+f.run.ep.host+"/"+f.run.Secret()+"/"+tc.resource, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := f.send(t, req)
			if got.status != http.StatusMethodNotAllowed || got.header.Get("Allow") != tc.allow || !errors.Is(f.run.Err(), errForbidden) {
				t.Fatalf("status %d, Allow %q, Err() %v", got.status, got.header.Get("Allow"), f.run.Err())
			}
		})
	}
}

func TestLiveRunRefusesWrongTokenWithoutFailing(t *testing.T) {
	f := newLiveRunFixture(t, time.Minute)
	other := strings.Repeat("0", len(f.run.Secret()))
	req, err := http.NewRequest(http.MethodPut, strings.Replace(f.run.OutputURL(), f.run.Secret(), other, 1)+"/"+liveInitName, bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.send(t, req); got.status != http.StatusNotFound {
		t.Fatalf("status %d", got.status)
	}
	if f.run.Err() != nil || len(sessionFiles(t, f.session.Dir())) != 0 {
		t.Fatal("a wrong token failed the run or stored a file")
	}
}

func TestLiveRunRefusesForeignHostAndPeer(t *testing.T) {
	f := newLiveRunFixture(t, time.Minute)
	got := f.put(t, liveInitName, []byte("x"), func(r *http.Request) { r.Host = "localhost:" + fmt.Sprint(f.run.Port()) })
	if got.status != http.StatusForbidden || !errors.Is(f.run.Err(), errForbidden) {
		t.Fatalf("foreign Host: status %d, Err() %v", got.status, f.run.Err())
	}

	f = newLiveRunFixture(t, time.Minute)
	req := httptest.NewRequest(http.MethodPut, f.run.OutputURL()+"/"+liveInitName, bytes.NewReader([]byte("x")))
	req.RemoteAddr = "192.0.2.10:4000"
	rec := httptest.NewRecorder()
	f.run.ep.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || f.run.Err() != nil {
		t.Fatalf("non-loopback peer: status %d, Err() %v", rec.Code, f.run.Err())
	}

	f = newLiveRunFixture(t, time.Minute)
	f.run.ep.peer = func(netip.AddrPort, netip.AddrPort) error { return errors.New("foreign process") }
	if got := f.put(t, liveInitName, []byte("x"), nil); got.status != http.StatusForbidden || !errors.Is(f.run.Err(), errForbidden) {
		t.Fatalf("foreign peer process: status %d, Err() %v", got.status, f.run.Err())
	}
	if len(sessionFiles(t, f.session.Dir())) != 0 {
		t.Fatal("a refused upload stored a file")
	}
}

func TestLiveRunRefusesOversizedUploads(t *testing.T) {
	body := make([]byte, liveInitLimit+1)
	for name, chunked := range map[string]bool{"declared": false, "chunked": true} {
		t.Run(name, func(t *testing.T) {
			f := newLiveRunFixture(t, time.Minute)
			req, err := http.NewRequest(http.MethodPut, f.run.OutputURL()+"/"+liveInitName, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if chunked {
				req.ContentLength = -1
				req.Body = io.NopCloser(bytes.NewReader(body))
			}
			// The endpoint closes the connection on a refusal, so the client
			// may see the close while still writing instead of the 413.
			got, sendErr := f.do(req)
			if sendErr == nil && got.status != http.StatusRequestEntityTooLarge {
				t.Fatalf("status %d, want 413", got.status)
			}
			if !errors.Is(f.run.Err(), errUploadTooLarge) {
				t.Fatalf("Err() %v, want errUploadTooLarge", f.run.Err())
			}
			if names := sessionFiles(t, f.session.Dir()); len(names) != 0 {
				t.Fatalf("an oversized upload left %v", names)
			}
		})
	}
}

// signalSink passes uploads to a live sink and closes reading once the sink
// has consumed the first bytes of a body, by which time its .part file exists.
type signalSink struct {
	uploadSink
	once    sync.Once
	reading chan struct{}
}

func (s *signalSink) upload(ctx context.Context, name string, body io.Reader, check func() error) error {
	return s.uploadSink.upload(ctx, name, readerFunc(func(p []byte) (int, error) {
		n, err := body.Read(p)
		if n > 0 {
			s.once.Do(func() { close(s.reading) })
		}
		return n, err
	}), check)
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestLiveRunRevokeMidUploadLeavesNoFile(t *testing.T) {
	session := newTestLiveSession(t)
	sink := &signalSink{uploadSink: liveSink{session: session, rung: testLiveRung}, reading: make(chan struct{})}
	source := &countingOpener{data: testPayload(10)}
	ep, err := startEndpoint(endpointConfig{
		open:     source.open,
		size:     10,
		deadline: time.Now().Add(time.Hour),
		idle:     time.Minute,
		now:      time.Now,
		sink:     sink,
		rung:     testLiveRung,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Revoke)
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPut, ep.OutputURL()+"/seg00000.m4s", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 1 << 20
	done := make(chan error, 1)
	go func() {
		resp, err := testClient().Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		done <- err
	}()
	go func() { _, _ = pw.Write(testPayload(64 << 10)) }()
	<-sink.reading
	if parts := sessionFiles(t, session.Dir()); len(parts) != 1 || !strings.HasSuffix(parts[0], livePartSuffix) {
		t.Fatalf("mid-upload files %v, want one .part", parts)
	}
	ep.Revoke()
	_ = pw.CloseWithError(errors.New("test done"))
	<-done
	if names := sessionFiles(t, session.Dir()); len(names) != 0 {
		t.Fatalf("a revoked upload left %v", names)
	}
}

// TestLiveRunFinishStoresTheUploadInFlight covers an encoder that exits while
// its last upload is still arriving: FFmpeg does not wait for the answer, so
// Finish must let the upload complete, while refusing any new connection.
func TestLiveRunFinishStoresTheUploadInFlight(t *testing.T) {
	session := newTestLiveSession(t)
	sink := &signalSink{uploadSink: liveSink{session: session, rung: testLiveRung}, reading: make(chan struct{})}
	source := &countingOpener{data: testPayload(10)}
	ep, err := startEndpoint(endpointConfig{
		open:     source.open,
		size:     10,
		deadline: time.Now().Add(time.Hour),
		idle:     time.Minute,
		now:      time.Now,
		sink:     sink,
		rung:     testLiveRung,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Revoke)
	body := testPayload(128 << 10)
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPut, ep.OutputURL()+"/seg00000.m4s", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(body))
	go func() {
		if resp, err := testClient().Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	go func() { _, _ = pw.Write(body[:64<<10]) }()
	<-sink.reading

	drained := make(chan struct{})
	go func() { ep.Drain(); close(drained) }()
	// Drain has begun once the listener refuses connections.
	for deadline := time.Now().Add(10 * time.Second); ; {
		conn, err := net.Dial("tcp", ep.host)
		if err != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("Drain never closed the listener")
		}
	}
	if _, err := pw.Write(body[64<<10:]); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	<-drained

	if err := ep.Err(); err != nil {
		t.Fatalf("Err() = %v after a completed upload", err)
	}
	f, err := session.Open(testLiveRung + "/seg00000.m4s")
	if err != nil {
		t.Fatalf("the upload in flight was not stored: %v (files %v)", err, sessionFiles(t, session.Dir()))
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("stored %d bytes (err %v), want the %d uploaded", len(got), err, len(body))
	}
}

func TestLiveRunPublishesCompletedUploadAfterClientDisconnect(t *testing.T) {
	session := newTestLiveSession(t)
	source := &countingOpener{data: testPayload(10)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	staged := make(chan struct{})
	release := make(chan struct{})
	published := make(chan error, 1)
	body := []byte("complete fragment")
	run, err := StartLiveRun(LiveRunConfig{
		Session: session, Rung: testLiveRung, Open: source.open, Size: 10, peer: acceptPeer,
		PublishSegment: func(uploadCtx context.Context, _ string, open func() (*LiveFile, error), commit func() error) error {
			file, err := open()
			if err != nil {
				published <- err
				return err
			}
			got, err := io.ReadAll(file)
			_ = file.Close()
			if err == nil && !bytes.Equal(got, body) {
				err = fmt.Errorf("could not read staged fragment: got %d bytes", len(got))
			}
			if err != nil {
				published <- err
				return err
			}
			close(staged)
			select {
			case <-release:
			case <-uploadCtx.Done():
				return uploadCtx.Err()
			case <-ctx.Done():
				return ctx.Err()
			}
			err = commit()
			published <- err
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Revoke)
	clientCtx, disconnect := context.WithCancel(ctx)
	defer disconnect()
	const name = "seg00002.m4s"
	req, err := http.NewRequestWithContext(clientCtx, http.MethodPut, run.OutputURL()+"/"+name, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		if resp, err := testClient().Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-staged:
	case err := <-published:
		t.Fatalf("could not inspect the staged upload: %v", err)
	case <-ctx.Done():
		t.Fatal("upload did not reach the publication hook")
	}
	disconnect()
	<-clientDone
	close(release)
	select {
	case err := <-published:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("the complete upload was lost after disconnect")
	}
	f, err := session.Open(testLiveRung + "/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got, err := io.ReadAll(f); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("published fragment = %q, %v", got, err)
	}
}

func TestLiveRunPauseSuspendsIdleAndKeepsAbsoluteExpiry(t *testing.T) {
	session := newTestLiveSession(t)
	source := &countingOpener{data: testPayload(1000)}
	clock := newFakeClock()
	run, err := StartLiveRun(LiveRunConfig{
		Session: session, Rung: testLiveRung, Open: source.open, Size: 1000,
		IdleTimeout: time.Minute, TTL: 10 * time.Minute, now: clock.Now, peer: acceptPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Revoke)
	f := &liveRunFixture{run: run, session: session}
	run.SetIdleSuspended(true)
	clock.Advance(2 * time.Minute)
	run.SetIdleSuspended(false)
	if got := f.put(t, "seg00002.m4s", []byte("segment 2"), nil); got.status != http.StatusCreated {
		t.Fatalf("resumed upload status = %d, want 201", got.status)
	}
	run.SetIdleSuspended(true)
	clock.Advance(9 * time.Minute)
	if got := f.put(t, "seg00003.m4s", []byte("segment 3"), nil); got.status != http.StatusGone {
		t.Fatalf("after absolute expiry status = %d, want 410", got.status)
	}
	run.SetIdleSuspended(false)
	if got := f.put(t, "seg00004.m4s", []byte("segment 4"), nil); got.status != http.StatusGone {
		t.Fatalf("resuming revived an expired token: %d", got.status)
	}
}

func TestLiveRunPauseSuspendsInFlightUploadRead(t *testing.T) {
	session := newTestLiveSession(t)
	sink := &signalSink{uploadSink: liveSink{session: session, rung: testLiveRung}, reading: make(chan struct{})}
	source := &countingOpener{data: testPayload(10)}
	const idle = 250 * time.Millisecond
	ep, err := startEndpoint(endpointConfig{
		open: source.open, size: 10, sink: sink, rung: testLiveRung, idle: idle,
		deadline: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Revoke)
	f := &liveRunFixture{session: session, run: &LiveRun{ep: ep}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pr, pw := io.Pipe()
	defer pw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, ep.OutputURL()+"/seg00000.m4s", pr)
	if err != nil {
		t.Fatal(err)
	}
	body := testPayload(128 << 10)
	req.ContentLength = int64(len(body))
	done := make(chan int, 1)
	go func() {
		result, err := f.do(req)
		if err != nil {
			done <- 0
			return
		}
		done <- result.status
	}()
	if _, err := pw.Write(body[:64<<10]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.reading:
	case <-ctx.Done():
		t.Fatal("the upload did not reach the sink")
	}
	f.run.SetIdleSuspended(true)
	select {
	case status := <-done:
		t.Fatalf("paused upload returned prematurely: %d", status)
	case <-time.After(2 * idle):
	}
	f.run.SetIdleSuspended(false)
	if _, err := pw.Write(body[64<<10:]); err != nil {
		t.Fatal(err)
	}
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	if status := <-done; status != http.StatusCreated {
		t.Fatalf("resumed upload status = %d, want 201", status)
	}
}

func TestLiveRunPausedPartialUploadExpiresAtAbsoluteDeadline(t *testing.T) {
	session := newTestLiveSession(t)
	sink := &signalSink{uploadSink: liveSink{session: session, rung: testLiveRung}, reading: make(chan struct{})}
	source := &countingOpener{data: testPayload(10)}
	ep, err := startEndpoint(endpointConfig{
		open: source.open, size: 10, sink: sink, rung: testLiveRung,
		idle: 10 * time.Second, deadline: time.Now().Add(400 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Revoke)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", ep.host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "PUT /%s/out/%s/seg00000.m4s HTTP/1.1\r\nHost: %s\r\nContent-Length: 131072\r\n\r\n", ep.token, testLiveRung, ep.host); err != nil {
		t.Fatal("could not write upload headers")
	}
	if _, err := conn.Write(testPayload(64 << 10)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.reading:
	case <-ctx.Done():
		t.Fatal("upload did not reach the sink")
	}
	if parts := sessionFiles(t, session.Dir()); len(parts) != 1 || !strings.HasSuffix(parts[0], livePartSuffix) {
		t.Fatalf("mid-upload files %v, want one .part", parts)
	}
	ep.setIdleSuspended(true)
	select {
	case <-ep.failed:
	case <-ctx.Done():
		t.Fatal("paused upload remained in flight past absolute expiry")
	}
	if err := ep.Err(); !errors.Is(err, ErrExpired) {
		t.Fatalf("endpoint error = %v, want expired", err)
	}
	if files := sessionFiles(t, session.Dir()); len(files) != 0 {
		t.Fatalf("expired upload left %v", files)
	}
}

func TestLiveRunIdleCountsOnlyAStalledUpload(t *testing.T) {
	if testing.Short() {
		t.Skip("waits on real time")
	}
	const idle = 300 * time.Millisecond

	// A body that keeps arriving, slower in total than the idle timeout, is
	// encoder activity and completes.
	f := newLiveRunFixture(t, idle)
	pr, pw := io.Pipe()
	go func() {
		for range 8 {
			if _, err := pw.Write(testPayload(1000)); err != nil {
				return
			}
			time.Sleep(idle / 4)
		}
		_ = pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPut, f.run.OutputURL()+"/seg00000.m4s", pr)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.send(t, req); got.status != http.StatusCreated || f.run.Err() != nil {
		t.Fatalf("slow upload: status %d, Err() %v", got.status, f.run.Err())
	}

	// A body that stalls for the idle timeout ends the token.
	f = newLiveRunFixture(t, idle)
	conn, err := net.Dial("tcp", f.run.ep.host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	head := "PUT /" + f.run.Secret() + "/out/" + testLiveRung + "/seg00000.m4s HTTP/1.1\r\nHost: " + f.run.ep.host + "\r\nContent-Length: 1000\r\n\r\n"
	if _, err := io.WriteString(conn, head+"0123456789"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusGone || !errors.Is(f.run.Err(), ErrExpired) {
		t.Fatalf("stalled upload: status %d, Err() %v", resp.StatusCode, f.run.Err())
	}
	select {
	case <-f.run.Failed():
	default:
		t.Fatal("Failed() still open after expiry")
	}
	if names := sessionFiles(t, f.session.Dir()); len(names) != 0 {
		t.Fatalf("a stalled upload left %v", names)
	}
}

// TestLiveRunRefusesATruncatedUpload: a body that ends before its declared
// length is not published as a short file.
func TestLiveRunRefusesATruncatedUpload(t *testing.T) {
	f := newLiveRunFixture(t, time.Minute)
	conn, err := net.Dial("tcp", f.run.ep.host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	head := "PUT /" + f.run.Secret() + "/out/" + testLiveRung + "/seg00000.m4s HTTP/1.1\r\nHost: " + f.run.ep.host + "\r\nContent-Length: 1000\r\n\r\n"
	if _, err := io.WriteString(conn, head+"0123456789"); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	// The server answers once the upload is finished with; the half-closed
	// connection may also just end.
	if resp, err := http.ReadResponse(bufio.NewReader(conn), nil); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusCreated {
			t.Fatal("truncated upload accepted")
		}
	} else {
		_, _ = io.Copy(io.Discard, conn)
	}
	if names := sessionFiles(t, f.session.Dir()); len(names) != 0 {
		t.Fatalf("a truncated upload left %v", names)
	}
}

func TestStartLiveRunValidatesItsConfig(t *testing.T) {
	session := newTestLiveSession(t)
	open := (&countingOpener{}).open
	for name, cfg := range map[string]LiveRunConfig{
		"no session":     {Rung: "r", Open: open},
		"no source":      {Session: session, Rung: "r"},
		"negative size":  {Session: session, Rung: "r", Open: open, Size: -1},
		"empty rung":     {Session: session, Open: open},
		"rung with dots": {Session: session, Rung: "..", Open: open},
		"rung with path": {Session: session, Rung: "a/b", Open: open},
	} {
		if run, err := StartLiveRun(cfg); err == nil {
			run.Revoke()
			t.Fatalf("%s: accepted", name)
		}
	}
}

// TestLiveRunKeepsNoPlaintextWithRealFFmpeg runs the production encoder
// against a live run and proves that no fragment it produced ever reached the
// disk as plaintext (ADR-0236 D2).
func TestLiveRunKeepsNoPlaintextWithRealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video")
	}
	fixture := ffmpegFixture(t, true)
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	session := newTestLiveSession(t)
	run, err := StartLiveRun(LiveRunConfig{
		Session: session,
		Rung:    testLiveRung,
		Open:    encryptedSource(t, fixture),
		Size:    int64(len(fixture)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Revoke()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rendition := ffmpeg.Rendition{Height: 120, VideoCodec: "h264", VideoKbps: 300, AudioCodec: "aac", AudioChannels: 2}
	ep := ffmpeg.EncryptedLiveEndpoint{Source: run.SourceURL(), Output: run.OutputURL(), Secret: run.Secret()}
	// io:ffmpeg-start-encrypted-hls-segments
	proc, err := ffmpeg.New().StartEncryptedHLSSegments(ctx, ep, 0, 4, rendition)
	if errors.Is(err, ffmpeg.ErrPipeConfinementUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	waitErr := proc.Wait()
	run.Finish()
	if waitErr != nil || run.Err() != nil {
		t.Fatalf("ffmpeg: %v; endpoint: %v; stderr: %s", waitErr, run.Err(), proc.StderrTail())
	}
	if strings.Contains(proc.StderrTail(), run.Secret()) {
		t.Fatal("stderr carries the token")
	}

	names := sessionFiles(t, session.Dir())
	want := map[string]bool{testLiveRung + "/" + liveInitName: false}
	for i := range fixtureSeconds / 4 {
		want[fmt.Sprintf("%s/seg%05d.m4s", testLiveRung, i)] = false
	}
	for _, name := range names {
		rung, base, _ := strings.Cut(name, "/")
		if _, ok := want[name]; !ok && (rung != testLiveRung || !liveSegmentName.MatchString(base)) {
			t.Fatalf("unexpected file %s in %v", name, names)
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing %s in %v", name, names)
		}
	}

	sourceWindows := windows(fixture, 16)
	for _, dir := range []string{session.Dir(), tmpDir} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path) //nolint:gosec // test temp file
			if err != nil {
				return err
			}
			for _, box := range []string{"ftyp", "moof", "mdat", "moov", "styp"} {
				if bytes.Contains(raw, []byte(box)) {
					return fmt.Errorf("%s holds a plaintext %s box", path, box)
				}
			}
			for _, w := range sourceWindows {
				if bytes.Contains(raw, w) {
					return fmt.Errorf("%s holds source plaintext", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// The stored fragments decrypt to the init segment and media fragments.
	for rel, box := range map[string]string{testLiveRung + "/" + liveInitName: "ftyp", testLiveRung + "/seg00001.m4s": "moof"} {
		f, err := session.Open(rel)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, b := range topLevelBoxes(t, plain) {
			found = found || b.typ == box
		}
		if !found {
			t.Fatalf("%s decrypted without a %s box", rel, box)
		}
	}
}

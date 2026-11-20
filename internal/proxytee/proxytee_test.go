package proxytee

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

type concurrentOrigin struct {
	mu       sync.Mutex
	calls    int
	entered  chan struct{}
	release  chan struct{}
	peerBody []byte
}

func (o *concurrentOrigin) OpenOriginRange(context.Context, string, string) (scrape.OriginResponse, error) {
	o.mu.Lock()
	o.calls++
	call := o.calls
	o.mu.Unlock()
	body := enclosureBody
	if call == 1 {
		close(o.entered)
		<-o.release
	} else {
		body = o.peerBody
		if len(body) == 0 {
			body = []byte("CONCURRENT-PEER-MUST-NOT-TOUCH-ARCHIVE")
		}
	}
	return scrape.OriginResponse{
		Body: io.NopCloser(strings.NewReader(string(body))), StatusCode: http.StatusOK, TotalSize: int64(len(body)),
	}, nil
}

// enclosureBody is the fixture origin enclosure the tests tee through.
var enclosureBody = []byte("BEAMFALL-PODCAST-ENCLOSURE-BYTES-0123456789-the-quick-brown-fox")

// originServer is a local httptest origin enclosure with HTTP Range support —
// the fixture stand-in for a podcast feed's enclosure URL.
type originServer struct {
	server *httptest.Server
}

func newOriginServer(t *testing.T) *originServer {
	t.Helper()
	o := &originServer{}
	o.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		// http.ServeContent gives real Range support (206 + Content-Range) for
		// free, exactly like a CDN enclosure.
		http.ServeContent(w, r, "enclosure.mp3", time.Unix(0, 0), strings.NewReader(string(enclosureBody)))
	}))
	t.Cleanup(o.server.Close)
	return o
}

// harness wires a real store (dev-seeded), auth, token service, and a proxy-tee
// pointed at a loopback-permitted scrape engine (the real ADR-0012 SSRF gate,
// with the test-only loopback exception so httptest is reachable). It returns
// the service, a valid Stream Token for the provisional Media, the Media ID,
// and the archive path the completed copy is promoted into.
type harness struct {
	svc         *Service
	store       *store.Store
	tokens      *streamtoken.Service
	mediaID     store.MediaID
	originURL   string
	archiveRoot string
	archivePath string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st := storetest.Open(t)
	authService := auth.NewService(st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	// The real scrape engine IS the SSRF guard; WithInsecureLoopback
	// is the documented test-only exception so an httptest origin is reachable.
	engine := scrape.New(scrape.WithInsecureLoopback())
	archiveRoot := t.TempDir()
	svc, err := New(st, authService, tokens, engine, archiveRoot)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	origin := newOriginServer(t)
	archivePath := filepath.Join(archiveRoot, "archive", "episode.mp3")
	mediaID, err := svc.Register(context.Background(), Registration{
		LibraryID:   "library-local",
		GUID:        "guid-episode-42",
		Title:       "Episode 42",
		OriginURL:   origin.server.URL + "/enclosure.mp3",
		ArchivePath: archivePath,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return &harness{svc: svc, store: st, tokens: tokens, mediaID: mediaID, originURL: origin.server.URL, archiveRoot: archiveRoot, archivePath: archivePath}
}

func TestRegisterRejectsArchiveTraversalOutsideRoot(t *testing.T) {
	h := newHarness(t)
	outside := filepath.Join(h.archiveRoot, "..", "proxytee-escape.mp3")
	_, err := h.svc.Register(context.Background(), Registration{
		LibraryID: "library-local", GUID: "guid-traversal", Title: "Traversal",
		OriginURL: h.originURL + "/enclosure.mp3", ArchivePath: outside,
	})
	if err == nil || !strings.Contains(err.Error(), "configured archive root") {
		t.Fatalf("Register outside root error = %v", err)
	}
}

func TestProxyTeeReplacesFinalPartialSymlinkWithoutFollowing(t *testing.T) {
	h := newHarness(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, h.archivePath+".partial"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got, err := os.ReadFile(victim) // #nosec G304 -- victim is created in this test's private t.TempDir.
	if err != nil || string(got) != "sentinel" {
		t.Fatalf("symlink victim changed to %q, %v", got, err)
	}
	archived, err := os.ReadFile(h.archivePath) // #nosec G304 -- archive path is beneath this test's private root.
	if err != nil || !bytes.Equal(archived, enclosureBody) {
		t.Fatalf("archive = %q, %v", archived, err)
	}
}

func TestRegisterRejectsAncestorSymlinkEscape(t *testing.T) {
	h := newHarness(t)
	outside := t.TempDir()
	link := filepath.Join(h.archiveRoot, "linked-outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Register(context.Background(), Registration{
		LibraryID: "library-local", GUID: "guid-ancestor-symlink", Title: "Symlink",
		OriginURL: h.originURL + "/enclosure.mp3", ArchivePath: filepath.Join(link, "escape.mp3"),
	})
	if err == nil {
		t.Fatal("Register accepted archive path through an ancestor symlink outside the root")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.mp3")); !os.IsNotExist(statErr) {
		t.Fatalf("outside archive created: %v", statErr)
	}
}

func TestNewRejectsSymlinkArchiveRoot(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "archive-link")
	if err := os.Symlink("target", root); err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, nil, nil, nil, root); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("New symlink archive root error = %v", err)
	}
}

func TestRegisterFailsClosedAfterArchiveRootReplacement(t *testing.T) {
	h := newHarness(t)
	displaced := h.archiveRoot + ".displaced"
	if err := os.Rename(h.archiveRoot, displaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(h.archiveRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("playback status after root replacement = %d, want 200", rec.Code)
	}
	for _, path := range []string{
		h.archivePath,
		h.archivePath + ".partial",
		filepath.Join(displaced, "archive", "episode.mp3"),
		filepath.Join(displaced, "archive", "episode.mp3.partial"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("archive root replacement wrote %q: %v", path, statErr)
		}
	}

	_, err := h.svc.Register(context.Background(), Registration{
		LibraryID: "library-local", GUID: "guid-root-replaced", Title: "Replacement",
		OriginURL: h.originURL + "/enclosure.mp3", ArchivePath: filepath.Join(h.archiveRoot, "escape.mp3"),
	})
	if !errors.Is(err, errArchiveRootChanged) {
		t.Fatalf("Register after root replacement error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(h.archiveRoot, "escape.mp3")); !os.IsNotExist(statErr) {
		t.Fatalf("replacement tree was written: %v", statErr)
	}
}

func TestPromotionRejectsPostHashSymlinkSubstitution(t *testing.T) {
	h := newHarness(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	parked := filepath.Join(h.archiveRoot, "parked-hashed-file")
	h.svc.beforeCAS = func() {
		if err := os.Rename(h.archivePath, parked); err != nil {
			t.Errorf("park hashed archive: %v", err)
			return
		}
		if err := os.Symlink(victim, h.archivePath); err != nil {
			t.Errorf("substitute final symlink: %v", err)
		}
	}

	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got, err := os.ReadFile(victim) // #nosec G304 -- victim is created in this test's private t.TempDir.
	if err != nil || string(got) != "sentinel" {
		t.Fatalf("symlink victim changed to %q, %v", got, err)
	}
	if _, err := os.Lstat(h.archivePath); !os.IsNotExist(err) {
		t.Fatalf("substituted final path survived: %v", err)
	}
	if provisional, err := h.store.MediaFileProvisional(context.Background(), h.mediaID); err != nil || !provisional {
		t.Fatalf("provisional = %v (err %v), want unchanged", provisional, err)
	}
}

func TestPromotionCompensatesPostCASSymlinkSubstitution(t *testing.T) {
	h := newHarness(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	parked := filepath.Join(h.archiveRoot, "parked-post-cas-file")
	h.svc.afterCAS = func() {
		if err := os.Rename(h.archivePath, parked); err != nil {
			t.Errorf("park promoted archive: %v", err)
			return
		}
		if err := os.Symlink(victim, h.archivePath); err != nil {
			t.Errorf("substitute post-CAS final symlink: %v", err)
		}
	}

	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got, err := os.ReadFile(victim) // #nosec G304 -- victim is created in this test's private t.TempDir.
	if err != nil || string(got) != "sentinel" {
		t.Fatalf("symlink victim changed to %q, %v", got, err)
	}
	if _, err := os.Lstat(h.archivePath); !os.IsNotExist(err) {
		t.Fatalf("post-CAS substituted final path survived: %v", err)
	}
	if provisional, err := h.store.MediaFileProvisional(context.Background(), h.mediaID); err != nil || !provisional {
		t.Fatalf("provisional = %v (err %v), want compensated", provisional, err)
	}
}

func TestPromotionCompensatesBeforeCASArchiveRootReplacement(t *testing.T) {
	testPromotionCompensatesArchiveRootReplacement(t, func(service *Service, replace func()) {
		service.beforeCAS = replace
	})
}

func TestPromotionCompensatesAfterCASArchiveRootReplacement(t *testing.T) {
	testPromotionCompensatesArchiveRootReplacement(t, func(service *Service, replace func()) {
		service.afterCAS = replace
	})
}

func TestPromotionDiscardsHeldRootBytesWhenCASIsCanceledAfterRootReplacement(t *testing.T) {
	h := newHarness(t)
	displaced := h.archiveRoot + ".displaced"
	ctx, cancel := context.WithCancel(context.Background())
	h.svc.beforeCAS = func() {
		if err := os.Rename(h.archiveRoot, displaced); err != nil {
			t.Fatalf("displace archive root: %v", err)
		}
		if err := os.Mkdir(h.archiveRoot, 0o700); err != nil {
			t.Fatalf("replace archive root: %v", err)
		}
		cancel()
	}

	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if provisional, err := h.store.MediaFileProvisional(context.Background(), h.mediaID); err != nil || !provisional {
		t.Fatalf("provisional = %v (err %v), want unchanged", provisional, err)
	}
	assertTreeHasNoFiles(t, h.archiveRoot)
	assertTreeHasNoFiles(t, displaced)
}

func testPromotionCompensatesArchiveRootReplacement(t *testing.T, install func(*Service, func())) {
	t.Helper()
	h := newHarness(t)
	displaced := h.archiveRoot + ".displaced"
	install(h.svc, func() {
		if err := os.Rename(h.archiveRoot, displaced); err != nil {
			t.Fatalf("displace archive root: %v", err)
		}
		if err := os.Mkdir(h.archiveRoot, 0o700); err != nil {
			t.Fatalf("replace archive root: %v", err)
		}
	})

	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if provisional, err := h.store.MediaFileProvisional(context.Background(), h.mediaID); err != nil || !provisional {
		t.Fatalf("provisional = %v (err %v), want compensated", provisional, err)
	}
	file, err := h.store.PrimaryMediaFile(context.Background(), h.mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != h.archivePath+".partial" {
		t.Fatalf("compensated DB path = %q, want lexical partial %q", file.Path, h.archivePath+".partial")
	}
	assertTreeHasNoFiles(t, h.archiveRoot)
	assertTreeHasNoFiles(t, displaced)
}

func assertTreeHasNoFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && !entry.IsDir() {
			t.Errorf("unexpected archive residue %q", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk archive tree %q: %v", root, err)
	}
}

func (h *harness) proxyToken(t *testing.T) string {
	return h.proxyTokenForSession(t, "session-default")
}

func (h *harness) proxyTokenForSession(t *testing.T, sessionID store.SessionID) string {
	t.Helper()
	authService := auth.NewService(h.store)
	authService.SetCertificationScales(map[string][]string{"filmtv": {"low", "high"}})
	scope, err := authService.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	token, err := h.tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaProxy,
		MediaID:   h.mediaID,
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: time.Unix(100, 0).UTC().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

func TestProxyBytesUsePlaybackCertificationGate(t *testing.T) {
	h := newHarness(t)
	h.svc.auth.SetCertificationScales(map[string][]string{"filmtv": {"low", "high"}})
	library, err := h.store.CreateLibrary(context.Background(), "Certified", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	h.mediaID, err = h.svc.Register(context.Background(), Registration{
		LibraryID: library.ID, GUID: "guid-certified", Title: "Unrated",
		OriginURL: h.originURL + "/enclosure.mp3", ArchivePath: filepath.Join(h.archiveRoot, "unrated.mp3"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.SetProfileCertificationCeiling(context.Background(), "profile-default", store.ProfileCertificationCeiling{
		PluginID: "filmtv", Ceiling: "low", AllowUnrated: false,
	}); err != nil {
		t.Fatal(err)
	}
	token := h.proxyToken(t)
	rec := httptest.NewRecorder()

	h.svc.ServeProxy(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unrated Media above playback ceiling", rec.Code)
	}
}

// TestProxyTeeStreamsToClientAndArchivesInPlace is the POD-2 acceptance test:
// (a) bytes reach the client AND land on disk; (b) on completion the Media is
// fingerprinted + promoted in place (same MediaID, provisional cleared);
// (c) the Stream Token fronts the proxy, never the origin URL.
func TestProxyTeeStreamsToClientAndArchivesInPlace(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Pre-state: the Media is provisional (a streamed-on-play copy, not yet kept).
	if prov, err := h.store.MediaFileProvisional(ctx, h.mediaID); err != nil || !prov {
		t.Fatalf("pre-state provisional = %v (err %v), want true", prov, err)
	}

	token := h.proxyToken(t)
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, req)

	// (a) bytes reach the client.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(enclosureBody) {
		t.Fatalf("client body = %q, want the full enclosure", rec.Body.String())
	}
	// (c) the Stream Token fronts the proxy — the origin URL never appears in the
	// request path the client used, nor anywhere in the response the client got.
	if strings.Contains(req.URL.Path, "http") || strings.Contains(rec.Body.String(), h.originURL) {
		t.Fatalf("origin URL leaked to the client surface")
	}

	// (b) on completion the Media is promoted in place: same MediaID, provisional
	// cleared, and the file moved to the archive path and fingerprinted.
	if prov, err := h.store.MediaFileProvisional(ctx, h.mediaID); err != nil || prov {
		t.Fatalf("post-state provisional = %v (err %v), want false (promoted)", prov, err)
	}
	file, err := h.store.PrimaryMediaFile(ctx, h.mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile after promotion: %v", err)
	}
	if file.Path != h.archivePath {
		t.Fatalf("promoted file path = %q, want archive path %q", file.Path, h.archivePath)
	}
	wantFP, err := store.HashMediaFileContent(h.archivePath)
	if err != nil {
		t.Fatalf("hash archive file: %v", err)
	}
	if file.ContentFingerprint != wantFP {
		t.Fatalf("fingerprint = %q, want the teed-file content hash %q", file.ContentFingerprint, wantFP)
	}
	if !strings.HasPrefix(file.ContentFingerprint, "sha256:") {
		t.Fatalf("fingerprint = %q, want a real sha256 content hash, not a placeholder", file.ContentFingerprint)
	}

	// The bytes that landed on disk equal the bytes the client got.
	onDisk, err := os.ReadFile(h.archivePath)
	if err != nil {
		t.Fatalf("read archived file: %v", err)
	}
	if string(onDisk) != string(enclosureBody) {
		t.Fatalf("on-disk bytes != enclosure")
	}
	// The in-progress partial is gone (renamed into place).
	if _, err := os.Stat(h.archivePath + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("partial still present after promotion: %v", err)
	}
}

// TestAbandonedPartialIsNotPromoted is the POD-2 acceptance point (d): an
// abandoned mid-stream partial is NOT auto-promoted. A client range request
// (a seek) is a sub-resource fetch, never the full body, so it must leave the
// Media provisional and never fingerprint/promote a partial.
func TestAbandonedPartialIsNotPromoted(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	token := h.proxyToken(t)
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	req.Header.Set("Range", "bytes=0-9") // a seek/partial fetch, not the whole body
	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, req)

	// The client still gets its ranged bytes (playback/seek works)...
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 for a range request: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(enclosureBody[0:10]) {
		t.Fatalf("ranged client body = %q, want first 10 bytes", rec.Body.String())
	}

	// ...but the Media is NOT promoted: a partial never becomes a permanent
	// archive, and the file is never fingerprinted.
	if prov, err := h.store.MediaFileProvisional(ctx, h.mediaID); err != nil || !prov {
		t.Fatalf("provisional = %v (err %v), want true — a partial must NOT promote", prov, err)
	}
	file, err := h.store.PrimaryMediaFile(ctx, h.mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile: %v", err)
	}
	if strings.HasPrefix(file.ContentFingerprint, "sha256:") {
		t.Fatalf("partial was fingerprinted (%q) — must stay the provisional placeholder", file.ContentFingerprint)
	}
	if _, err := os.Stat(h.archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive file exists after a partial: a partial must not be moved into place")
	}
}

func TestRangedRequestsNeverWritePartial(t *testing.T) {
	h := newHarness(t)
	token := h.proxyToken(t)

	for _, byteRange := range []string{"bytes=10-19", "bytes=0-9"} {
		req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
		req.Header.Set("Range", byteRange)
		rec := httptest.NewRecorder()
		h.svc.ServeProxy(rec, req)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("range %q status = %d, want 206", byteRange, rec.Code)
		}
	}

	if _, err := os.Stat(h.archivePath + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("range request created archive partial: %v", err)
	}
}

func TestConcurrentFullRequestsHaveOneArchiveOwner(t *testing.T) {
	h := newHarness(t)
	peerBody := []byte(strings.Repeat("peer-multichunk-", 8192))
	origin := &concurrentOrigin{entered: make(chan struct{}), release: make(chan struct{}), peerBody: peerBody}
	h.svc.origin = origin
	token := h.proxyToken(t)
	ownerDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.svc.ServeProxy(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil))
		ownerDone <- rec
	}()
	<-origin.entered

	peer := httptest.NewRecorder()
	h.svc.ServeProxy(peer, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil))
	close(origin.release)
	owner := <-ownerDone
	if owner.Code != http.StatusOK || peer.Code != http.StatusOK {
		t.Fatalf("owner/peer status = %d/%d", owner.Code, peer.Code)
	}
	if !bytes.Equal(peer.Body.Bytes(), peerBody) {
		t.Fatalf("peer body length = %d, want complete %d-byte multi-chunk response", peer.Body.Len(), len(peerBody))
	}
	archived, err := os.ReadFile(h.archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(archived) != string(enclosureBody) {
		t.Fatalf("archive was corrupted by peer: %q", archived)
	}
}

func TestRegisterWaiterReturnsAfterSuccessfulPromotion(t *testing.T) {
	h := newHarness(t)
	origin := &concurrentOrigin{entered: make(chan struct{}), release: make(chan struct{})}
	h.svc.origin = origin
	token := h.proxyToken(t)
	ownerDone := make(chan struct{})
	go func() {
		defer close(ownerDone)
		h.svc.ServeProxy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil))
	}()
	<-origin.entered

	registerDone := make(chan error, 1)
	go func() {
		_, err := h.svc.Register(context.Background(), Registration{
			LibraryID: "library-local", GUID: "guid-episode-42", Title: "Episode 42",
			OriginURL: "https://example.invalid/replacement", ArchivePath: filepath.Join(h.archiveRoot, "replacement.mp3"),
		})
		registerDone <- err
	}()
	select {
	case err := <-registerDone:
		t.Fatalf("Register returned before promotion owner released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(origin.release)
	<-ownerDone
	select {
	case err := <-registerDone:
		if !errors.Is(err, store.ErrProvisionalRegistrationConflict) {
			t.Fatalf("Register error = %v, want promotion conflict", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Register waiter spun after successful promotion closed its slot")
	}
}

func TestRegisterWaitForOwnerHonorsContext(t *testing.T) {
	h := newHarness(t)
	st, acquired := h.svc.acquirePromotion(h.mediaID)
	if !acquired {
		t.Fatal("could not acquire promotion owner")
	}
	defer h.svc.releasePromotion(h.mediaID, st.generation, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.svc.Register(ctx, Registration{
		LibraryID: "library-local", GUID: "guid-episode-42", Title: "Episode 42",
		OriginURL: "https://example.invalid/replacement", ArchivePath: filepath.Join(h.archiveRoot, "replacement.mp3"),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Register error = %v, want context.Canceled", err)
	}
}

func TestRegisterDuringStreamReturnsBusyBeforeOwnerCompletes(t *testing.T) {
	h := newHarness(t)
	origin := &concurrentOrigin{entered: make(chan struct{}), release: make(chan struct{})}
	h.svc.origin = origin
	token := h.proxyToken(t)
	ownerDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.svc.ServeProxy(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil))
		ownerDone <- rec
	}()
	<-origin.entered
	released := false
	defer func() {
		if !released {
			close(origin.release)
		}
	}()

	registerDone := make(chan error, 1)
	go func() {
		_, err := h.svc.Register(context.Background(), Registration{
			LibraryID: "library-local", GUID: "guid-episode-42", Title: "Episode 42",
			OriginURL: "https://example.invalid/replacement", ArchivePath: filepath.Join(h.archiveRoot, "replacement.mp3"),
		})
		registerDone <- err
	}()
	select {
	case err := <-registerDone:
		var busy *RegistrationBusyError
		if !errors.As(err, &busy) {
			t.Fatalf("Register error = %v, want RegistrationBusyError", err)
		}
		if busy.MediaID != h.mediaID {
			t.Fatalf("busy MediaID = %q, want %q", busy.MediaID, h.mediaID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Register waited for the entire stream")
	}
	select {
	case <-ownerDone:
		t.Fatal("promotion owner completed before its origin was released")
	default:
	}

	close(origin.release)
	released = true
	owner := <-ownerDone
	if owner.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want 200", owner.Code)
	}
	archived, err := os.ReadFile(h.archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archived, enclosureBody) {
		t.Fatalf("archive = %q, want original stream bytes", archived)
	}
}

func TestRegisterStoreWriteDoesNotHoldServiceMutex(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	registerProvisional := h.svc.registerProvisional
	h.svc.registerProvisional = func(ctx context.Context, provisional store.ProvisionalMedia) (store.MediaID, string, error) {
		close(entered)
		<-release
		return registerProvisional(ctx, provisional)
	}
	registerDone := make(chan error, 1)
	go func() {
		_, err := h.svc.Register(context.Background(), Registration{
			LibraryID: "library-local", GUID: "guid-unlocked-store-write", Title: "Unlocked",
			OriginURL: h.originURL + "/enclosure.mp3", ArchivePath: filepath.Join(h.archiveRoot, "unlocked.mp3"),
		})
		registerDone <- err
	}()
	<-entered

	lookupDone := make(chan bool, 1)
	go func() {
		_, ok := h.svc.lookup(h.mediaID)
		lookupDone <- ok
	}()
	select {
	case ok := <-lookupDone:
		if !ok {
			t.Fatal("existing registration disappeared during store write")
		}
	case <-time.After(time.Second):
		t.Fatal("lookup blocked behind registration store write")
	}
	close(release)
	released = true
	if err := <-registerDone; err != nil {
		t.Fatal(err)
	}
}

func TestRegisterCompensatesCanceledPublication(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	registerProvisional := h.svc.registerProvisional
	h.svc.registerProvisional = func(ctx context.Context, provisional store.ProvisionalMedia) (store.MediaID, string, error) {
		mediaID, previousPath, err := registerProvisional(ctx, provisional)
		cancel()
		return mediaID, previousPath, err
	}
	reg := Registration{
		LibraryID: "library-local", GUID: "guid-canceled-publication", Title: "Canceled",
		OriginURL: h.originURL + "/enclosure.mp3", ArchivePath: filepath.Join(h.archiveRoot, "canceled.mp3"),
	}
	if _, err := h.svc.Register(ctx, reg); !errors.Is(err, context.Canceled) {
		t.Fatalf("Register error = %v, want context.Canceled", err)
	}
	mediaID, previousPath, err := h.store.RegisterProvisionalMedia(context.Background(), store.ProvisionalMedia{
		LibraryID: reg.LibraryID, GUID: reg.GUID, Title: reg.Title, TeePath: reg.ArchivePath + ".partial",
	})
	if err != nil || previousPath != "" {
		t.Fatalf("registration after compensation mediaID=%q previousPath=%q err=%v", mediaID, previousPath, err)
	}
}

func TestRegisterRereadsReplacementSlotAfterPromotionWake(t *testing.T) {
	h := newHarness(t)
	origin := &concurrentOrigin{entered: make(chan struct{}), release: make(chan struct{})}
	h.svc.origin = origin
	replacement := &streamSlot{
		current: &stream{generation: 2}, owner: true, changed: make(chan struct{}),
	}
	h.svc.afterCAS = func() {
		h.svc.mu.Lock()
		old := h.svc.slots[h.mediaID]
		old.owner = false
		close(old.changed)
		h.svc.slots[h.mediaID] = replacement
		h.svc.mu.Unlock()
	}
	defer h.svc.releasePromotion(h.mediaID, replacement.current.generation, true)
	token := h.proxyToken(t)
	released := false
	defer func() {
		if !released {
			close(origin.release)
		}
	}()
	ownerDone := make(chan struct{})
	go func() {
		defer close(ownerDone)
		h.svc.ServeProxy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil))
	}()
	<-origin.entered

	registerDone := make(chan error, 1)
	go func() {
		_, err := h.svc.Register(context.Background(), Registration{
			LibraryID: "library-local", GUID: "guid-episode-42", Title: "Episode 42",
			OriginURL: "https://example.invalid/replacement", ArchivePath: filepath.Join(h.archiveRoot, "replacement.mp3"),
		})
		registerDone <- err
	}()
	select {
	case err := <-registerDone:
		t.Fatalf("Register returned before promotion wake: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(origin.release)
	released = true
	<-ownerDone
	select {
	case err := <-registerDone:
		var busy *RegistrationBusyError
		if !errors.As(err, &busy) {
			t.Fatalf("Register error = %v, want replacement-slot busy error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Register did not observe the replacement slot")
	}
}

func TestPromoteRestoresPartialWhenStoreUpdateFails(t *testing.T) {
	h := newHarness(t)
	st, ok := h.svc.lookup(h.mediaID)
	if !ok {
		t.Fatal("registered stream missing")
	}
	if err := os.MkdirAll(filepath.Dir(st.teePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.teePath, enclosureBody, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	partialInfo, err := os.Stat(st.teePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.promote(ctx, h.mediaID, st, partialInfo); err == nil {
		t.Fatal("promote succeeded with canceled store context")
	}
	if _, err := os.Stat(st.teePath); err != nil {
		t.Fatalf("partial not restored after store failure: %v", err)
	}
	if _, err := os.Stat(st.archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive exists after store failure: %v", err)
	}
	if provisional, err := h.store.MediaFileProvisional(context.Background(), h.mediaID); err != nil || !provisional {
		t.Fatalf("provisional = %v (err %v), want unchanged", provisional, err)
	}
}

func TestCloseErrorDoesNotPromote(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	realClose := closeTeeFile
	closeTeeFile = func(f *os.File) error {
		_ = realClose(f)
		return errors.New("forced close error")
	}
	t.Cleanup(func() { closeTeeFile = realClose })

	token := h.proxyToken(t)
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if prov, err := h.store.MediaFileProvisional(ctx, h.mediaID); err != nil || !prov {
		t.Fatalf("provisional = %v (err %v), want true after tee close error", prov, err)
	}
	if _, err := os.Stat(h.archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive file exists after tee close error: %v", err)
	}
	if _, err := os.Stat(h.archivePath + ".partial"); err != nil {
		t.Fatalf("partial missing after tee close error: %v", err)
	}
}

// TestProxyRejectsTokenForWrongTarget confirms the proxy refuses a token minted
// for a different byte target (defence-in-depth: a direct-play token cannot be
// replayed against the proxy endpoint).
func TestProxyRejectsTokenForWrongTarget(t *testing.T) {
	h := newHarness(t)
	authService := auth.NewService(h.store)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	token, err := h.tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect, // wrong target
		MediaID:   h.mediaID,
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: time.Unix(100, 0).UTC().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	rec := httptest.NewRecorder()
	h.svc.ServeProxy(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a wrong-target token", rec.Code)
	}
}

// TestRegisterRefusesPrivateOrigin confirms the SSRF guard is live: an origin
// that resolves to a non-public address is refused at fetch time (the proxy
// fetches server-side through the ADR-0012 §9 hardened engine). A malicious
// feed cannot make the server hit internal addresses.
func TestRegisterRefusesPrivateOrigin(t *testing.T) {
	st := storetest.Open(t)
	authService := auth.NewService(st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	// NO WithInsecureLoopback: the production SSRF posture refuses literal IPs /
	// private ranges outright.
	engine := scrape.New()
	archiveRoot := t.TempDir()
	svc, err := New(st, authService, tokens, engine, archiveRoot)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	mediaID, err := svc.Register(context.Background(), Registration{
		LibraryID:   "library-local",
		GUID:        "guid-ssrf",
		Title:       "Malicious",
		OriginURL:   "http://169.254.169.254/latest/meta-data/", // cloud metadata
		ArchivePath: filepath.Join(archiveRoot, "x.mp3"),
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(context.Background(), streamtoken.Grant{
		Target: streamtoken.TargetMediaProxy, MediaID: mediaID,
		DeviceID: scope.DeviceID, SessionID: scope.SessionID, ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	rec := httptest.NewRecorder()
	svc.ServeProxy(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 — the SSRF gate must refuse a private origin", rec.Code)
	}
}

// ensure the scrape engine satisfies OriginOpener (compile-time contract).
var _ OriginOpener = (*scrape.Engine)(nil)

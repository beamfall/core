package proxytee

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// A catalogued feed entry already owns a file-less Media; registering its
// origin stream must attach to that Media rather than fork a second one, keep
// the catalogue's title, and serve ranged reads through the proxy.
func TestEnsureRegisteredReusesFilelessCataloguedMedia(t *testing.T) {
	ctx := context.Background()
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	const library store.LibraryID = "library-local"
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: library,
		FeedURL:   "https://feeds.example.test/feed.xml",
		Root:      t.TempDir(),
	}, time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	key := store.SubscriptionEntryStreamKey(sub.ID, "entry-guid-1")
	cataloguedID := storetest.SeedFilelessGUIDMedia(t, dataDir, library, key, "Catalogued Title")
	if want := store.SubscriptionEntryMediaID(library, sub.ID, "entry-guid-1"); cataloguedID != want {
		t.Fatalf("seeded ID = %q, want %q", cataloguedID, want)
	}

	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	archiveRoot := t.TempDir()
	svc, err := New(st, auth.NewService(st), tokens, scrape.New(scrape.WithInsecureLoopback()), archiveRoot)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	origin := newOriginServer(t)
	reg := Registration{
		LibraryID: library, GUID: key, Title: "Registration Title",
		Container: "mp3", Codec: "mp3", Class: store.MediaClassAudio,
		OriginURL: origin.server.URL + "/enclosure.mp3",
	}

	mediaID, err := svc.EnsureRegistered(ctx, reg)
	if err != nil || mediaID != cataloguedID {
		t.Fatalf("EnsureRegistered = %q, %v; want existing %q", mediaID, err, cataloguedID)
	}
	all := store.LibraryFilter{AllLibraries: true, Revealed: true}
	media, err := st.Media(ctx, mediaID, all)
	if err != nil || media.Title != "Catalogued Title" {
		t.Fatalf("Media = %+v, %v; want catalogue title kept", media, err)
	}
	current, ok := svc.lookup(mediaID)
	if !ok || filepath.Dir(current.archivePath) != svc.archiveDir || !strings.HasSuffix(current.archivePath, string(mediaID)+".mp3") {
		t.Fatalf("registered stream = %+v, %v; want archive under root named by Media ID", current, ok)
	}

	again, err := svc.EnsureRegistered(ctx, reg)
	if err != nil || again != mediaID {
		t.Fatalf("second EnsureRegistered = %q, %v; want idempotent %q", again, err, mediaID)
	}

	h := &harness{svc: svc, store: st, tokens: tokens, mediaID: mediaID}
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	svc.ServeProxy(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 10 {
		t.Fatalf("ranged proxy = %d with %d bytes, want 206 with 10", rec.Code, rec.Body.Len())
	}
}

type recordingOriginAuthOpener struct {
	lastURL   string
	lastRange string
	lastAuth  string
}

func (o *recordingOriginAuthOpener) OpenOriginRange(_ context.Context, rawURL, rangeHeader string) (scrape.OriginResponse, error) {
	return o.OpenOriginRangeAuth(context.Background(), rawURL, rangeHeader, "")
}

func (o *recordingOriginAuthOpener) OpenOriginRangeAuth(_ context.Context, rawURL, rangeHeader, authHeader string) (scrape.OriginResponse, error) {
	o.lastURL = rawURL
	o.lastRange = rangeHeader
	o.lastAuth = authHeader
	return scrape.OriginResponse{
		Body:        io.NopCloser(strings.NewReader("0123456789")),
		StatusCode:  http.StatusPartialContent,
		ContentType: "audio/mpeg",
		TotalSize:   100,
	}, nil
}

func TestEnsureRegisteredScopesPrivateFeedAuthAndRotates(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	opener := &recordingOriginAuthOpener{}
	svc, err := New(st, auth.NewService(st), tokens, opener, t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	cases := []struct {
		name      string
		guid      string
		feedURL   string
		originURL string
		wantAuth  string
	}{
		{
			name:      "same host",
			guid:      "guid-same-host",
			feedURL:   "https://feeds.example.test/private.xml",
			originURL: "https://feeds.example.test/enclosure.mp3",
			wantAuth:  "Bearer first-secret",
		},
		{
			name:      "same-site subdomain",
			guid:      "guid-same-site",
			feedURL:   "https://feeds.example.test/private.xml",
			originURL: "https://cdn.example.test/enclosure.mp3",
			wantAuth:  "Bearer first-secret",
		},
		{
			name:      "cross-host different site",
			guid:      "guid-cross-site",
			feedURL:   "https://feeds.example.test/private.xml",
			originURL: "https://cdn.other.test/enclosure.mp3",
			wantAuth:  "",
		},
		{
			name:      "public suffix sibling",
			guid:      "guid-psl-sibling",
			feedURL:   "https://a.github.io/private.xml",
			originURL: "https://b.github.io/enclosure.mp3",
			wantAuth:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mediaID, err := svc.EnsureRegistered(ctx, Registration{
				LibraryID:  "library-local",
				GUID:       tc.guid,
				Title:      tc.name,
				Container:  "mp3",
				Codec:      "mp3",
				Class:      store.MediaClassAudio,
				OriginURL:  tc.originURL,
				FeedURL:    tc.feedURL,
				AuthHeader: "Bearer first-secret",
			})
			if err != nil {
				t.Fatalf("EnsureRegistered: %v", err)
			}
			h := &harness{svc: svc, store: st, tokens: tokens, mediaID: mediaID}
			req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil)
			req.Header.Set("Range", "bytes=0-9")
			rec := httptest.NewRecorder()
			svc.ServeProxy(rec, req)
			if rec.Code != http.StatusPartialContent {
				t.Fatalf("ServeProxy status = %d: %s", rec.Code, rec.Body.String())
			}
			if opener.lastAuth != tc.wantAuth {
				t.Fatalf("upstream auth = %q, want %q", opener.lastAuth, tc.wantAuth)
			}
		})
	}

	// Rotating the Subscription secret replaces the in-memory header on EnsureRegistered.
	rotatedID, err := svc.EnsureRegistered(ctx, Registration{
		LibraryID:  "library-local",
		GUID:       "guid-same-site",
		Title:      "same-site subdomain",
		Container:  "mp3",
		Codec:      "mp3",
		Class:      store.MediaClassAudio,
		OriginURL:  "https://cdn.example.test/enclosure.mp3",
		FeedURL:    "https://feeds.example.test/private.xml",
		AuthHeader: "Bearer rotated-secret",
	})
	if err != nil {
		t.Fatalf("EnsureRegistered(rotated): %v", err)
	}
	h := &harness{svc: svc, store: st, tokens: tokens, mediaID: rotatedID}
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+h.proxyToken(t), nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	svc.ServeProxy(rec, req)
	if rec.Code != http.StatusPartialContent || opener.lastAuth != "Bearer rotated-secret" {
		t.Fatalf("after rotation: status=%d auth=%q, want 206 Bearer rotated-secret", rec.Code, opener.lastAuth)
	}
}

func TestRevokeSubscriptionStopsCachedAuthRangeRequests(t *testing.T) {
	ctx := context.Background()
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	opener := &recordingOriginAuthOpener{}
	svc, err := New(st, auth.NewService(st), tokens, opener, t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.test/private.xml",
		AuthRef:   "subscription-auth-revoke-test",
		Root:      t.TempDir(),
	}, now)
	if err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	subID := sub.ID
	key := store.SubscriptionEntryStreamKey(subID, "entry-1")
	seededID := storetest.SeedFilelessGUIDMedia(t, dataDir, "library-local", key, "Private Entry")
	reg := Registration{
		LibraryID:  "library-local",
		GUID:       key,
		Title:      "Private Entry",
		Container:  "mp3",
		Codec:      "mp3",
		Class:      store.MediaClassAudio,
		OriginURL:  "https://feeds.example.test/enclosure.mp3",
		FeedURL:    "https://feeds.example.test/private.xml",
		AuthHeader: "Bearer cached-subscription-secret",
	}
	mediaID, err := svc.EnsureRegistered(ctx, reg)
	if err != nil || mediaID != seededID {
		t.Fatalf("EnsureRegistered = %q, %v; want %q", mediaID, err, seededID)
	}

	h := &harness{svc: svc, store: st, tokens: tokens, mediaID: mediaID}
	token := h.proxyToken(t)

	// Before removal, range requests send the cached Authorization header.
	req := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	svc.ServeProxy(rec, req)
	if rec.Code != http.StatusPartialContent || opener.lastAuth != "Bearer cached-subscription-secret" {
		t.Fatalf("before revoke: status=%d auth=%q, want 206 with secret", rec.Code, opener.lastAuth)
	}

	// Removing and revoking the subscription drops the registration and cached
	// credential so the same unexpired proxy token cannot keep sending cached auth headers.
	opener.lastAuth = ""
	opener.lastURL = ""
	if _, err := st.RemoveSubscription(ctx, subID); err != nil {
		t.Fatalf("RemoveSubscription: %v", err)
	}
	svc.RevokeSubscription(subID)

	afterReq := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	afterReq.Header.Set("Range", "bytes=10-19")
	afterRec := httptest.NewRecorder()
	svc.ServeProxy(afterRec, afterReq)
	if afterRec.Code != http.StatusNotFound {
		t.Fatalf("after revoke: status=%d, want 404", afterRec.Code)
	}
	if opener.lastAuth != "" || opener.lastURL != "" {
		t.Fatalf("after revoke: origin was fetched with url=%q auth=%q, want no fetch", opener.lastURL, opener.lastAuth)
	}

	// A concurrent playback request that read the secret before removal and
	// finishes registering after RevokeSubscription must not restore cached auth.
	if _, err := svc.EnsureRegistered(ctx, reg); !errors.Is(err, store.ErrSubscriptionNotFound) {
		t.Fatalf("EnsureRegistered after removal = %v, want %v", err, store.ErrSubscriptionNotFound)
	}
	lateReq := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+token, nil)
	lateReq.Header.Set("Range", "bytes=20-29")
	lateRec := httptest.NewRecorder()
	svc.ServeProxy(lateRec, lateReq)
	if lateRec.Code != http.StatusNotFound {
		t.Fatalf("after late registration attempt: status=%d, want 404", lateRec.Code)
	}
	if opener.lastAuth != "" || opener.lastURL != "" {
		t.Fatalf("after late registration attempt: origin fetched url=%q auth=%q, want no fetch", opener.lastURL, opener.lastAuth)
	}

	// Also cover removal occurring during registerProvisional, before
	// RevokeSubscription scans slots.
	subInFlight, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.test/in-flight.xml",
		AuthRef:   "subscription-auth-in-flight",
		Root:      t.TempDir(),
	}, now)
	if err != nil {
		t.Fatalf("AddSubscription(in-flight): %v", err)
	}
	inFlightKey := store.SubscriptionEntryStreamKey(subInFlight.ID, "entry-1")
	inFlightMediaID := storetest.SeedFilelessGUIDMedia(t, dataDir, "library-local", inFlightKey, "In-Flight Entry")
	inFlightToken := (&harness{svc: svc, store: st, tokens: tokens, mediaID: inFlightMediaID}).proxyToken(t)
	origRegisterProvisional := svc.registerProvisional
	svc.registerProvisional = func(ctx context.Context, p store.ProvisionalMedia) (store.MediaID, string, error) {
		id, prev, regErr := origRegisterProvisional(ctx, p)
		if _, rmErr := st.RemoveSubscription(ctx, subInFlight.ID); rmErr != nil {
			t.Errorf("RemoveSubscription during registerProvisional: %v", rmErr)
		}
		return id, prev, regErr
	}
	t.Cleanup(func() { svc.registerProvisional = origRegisterProvisional })

	if _, err := svc.EnsureRegistered(ctx, Registration{
		LibraryID:  "library-local",
		GUID:       inFlightKey,
		Title:      "In-Flight Entry",
		Container:  "mp3",
		Codec:      "mp3",
		Class:      store.MediaClassAudio,
		OriginURL:  "https://feeds.example.test/in-flight.mp3",
		FeedURL:    "https://feeds.example.test/in-flight.xml",
		AuthHeader: "Bearer in-flight-secret",
	}); !errors.Is(err, store.ErrSubscriptionNotFound) {
		t.Fatalf("EnsureRegistered with mid-flight removal = %v, want %v", err, store.ErrSubscriptionNotFound)
	}
	inFlightReq := httptest.NewRequest(http.MethodGet, "/api/media/proxy/"+inFlightToken, nil)
	inFlightReq.Header.Set("Range", "bytes=0-9")
	inFlightRec := httptest.NewRecorder()
	svc.ServeProxy(inFlightRec, inFlightReq)
	if inFlightRec.Code != http.StatusNotFound || opener.lastAuth != "" {
		t.Fatalf("after mid-flight removal: status=%d auth=%q, want 404 with empty auth", inFlightRec.Code, opener.lastAuth)
	}
}

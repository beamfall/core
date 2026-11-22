package proxytee

import (
	"context"
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
	key := store.SubscriptionEntryStreamKey("subscription-1", "entry-guid-1")
	cataloguedID := storetest.SeedFilelessGUIDMedia(t, dataDir, library, key, "Catalogued Title")
	if want := store.SubscriptionEntryMediaID(library, "subscription-1", "entry-guid-1"); cataloguedID != want {
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

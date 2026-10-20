package mediahttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/artwork"
)

// boundArtworkRequest builds a versioned artwork request carrying the
// compatibility binding (generation, subject) the middleware would attach.
func boundArtworkRequest(mediaID, slot, version, generation, subject string) *http.Request {
	req := artworkRequest(mediaID, slot+"?v="+version)
	return req.WithContext(WithCompatBinding(req.Context(), generation, subject))
}

func artworkCacheHeaders(t *testing.T, handler *Handler, req *http.Request) (int, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.Artwork(rec, req)
	return rec.Code, rec.Header().Get("Cache-Control"), rec.Header().Get("ETag")
}

// A decision-bound artwork response must not be reusable across the decision
// that authorised it: neither after the decision generation moves, nor for a
// different subject.
func TestArtworkCacheEntryIsBoundToDecisionAndSubject(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestPoster(t, svc, "media-alpha")
	version, ok := svc.ArtworkVersion("media-alpha", artwork.SlotPoster)
	if !ok {
		t.Fatal("ArtworkVersion = absent, want poster version")
	}

	code, cacheControl, firstETag := artworkCacheHeaders(t, handler,
		boundArtworkRequest("media-alpha", "poster", version, "7", "session:session-default"))
	if code != http.StatusOK {
		t.Fatalf("bound status = %d, want 200", code)
	}
	if cacheControl != "private, no-cache" {
		t.Fatalf("bound Cache-Control = %q, want a revalidating private directive", cacheControl)
	}
	if strings.Contains(cacheControl, "immutable") {
		t.Fatalf("bound Cache-Control = %q, must not bless a decision-bound response as immutable", cacheControl)
	}
	if firstETag == `"`+version+`"` {
		t.Fatalf("bound ETag = %q, want a validator that covers more than the content version", firstETag)
	}
	if strings.Contains(firstETag, "session-default") {
		t.Fatalf("bound ETag = %q, must not echo the subject", firstETag)
	}

	// The same bytes under a later decision generation get a different
	// validator, so the stored entry cannot satisfy the new request.
	_, _, laterGenerationETag := artworkCacheHeaders(t, handler,
		boundArtworkRequest("media-alpha", "poster", version, "8", "session:session-default"))
	if laterGenerationETag == firstETag {
		t.Fatalf("validator after generation change = %q, want a value distinct from %q", laterGenerationETag, firstETag)
	}

	// The same bytes for a different subject likewise get a different validator.
	_, _, otherSubjectETag := artworkCacheHeaders(t, handler,
		boundArtworkRequest("media-alpha", "poster", version, "7", "session:session-other"))
	if otherSubjectETag == firstETag {
		t.Fatalf("validator for another subject = %q, want a value distinct from %q", otherSubjectETag, firstETag)
	}

	// A stored entry is only revalidated back into a 304 by its own validator.
	stale := boundArtworkRequest("media-alpha", "poster", version, "8", "session:session-default")
	stale.Header.Set("If-None-Match", firstETag)
	staleRec := httptest.NewRecorder()
	handler.Artwork(staleRec, stale)
	if staleRec.Code != http.StatusOK {
		t.Fatalf("cross-decision conditional request = %d, want 200 with fresh bytes", staleRec.Code)
	}
	if staleRec.Body.String() != "jpeg bytes" {
		t.Fatalf("cross-decision body = %q, want freshly served bytes", staleRec.Body.String())
	}

	crossSubject := boundArtworkRequest("media-alpha", "poster", version, "7", "session:session-other")
	crossSubject.Header.Set("If-None-Match", firstETag)
	crossRec := httptest.NewRecorder()
	handler.Artwork(crossRec, crossSubject)
	if crossRec.Code != http.StatusOK {
		t.Fatalf("cross-subject conditional request = %d, want 200 with fresh bytes", crossRec.Code)
	}

	matching := boundArtworkRequest("media-alpha", "poster", version, "7", "session:session-default")
	matching.Header.Set("If-None-Match", firstETag)
	matchRec := httptest.NewRecorder()
	handler.Artwork(matchRec, matching)
	if matchRec.Code != http.StatusNotModified {
		t.Fatalf("same-decision conditional request = %d, want 304", matchRec.Code)
	}
}

// A bound request whose decision cannot be named in full is uncacheable rather
// than falling back to the immutable default.
func TestArtworkWithUnestablishedDecisionIsUncacheable(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestPoster(t, svc, "media-alpha")
	version, ok := svc.ArtworkVersion("media-alpha", artwork.SlotPoster)
	if !ok {
		t.Fatal("ArtworkVersion = absent, want poster version")
	}

	partial := map[string][2]string{
		"no generation": {"", "session:session-default"},
		"no subject":    {"7", ""},
		"neither":       {"", ""},
	}
	for name, binding := range partial {
		t.Run(name, func(t *testing.T) {
			code, cacheControl, etag := artworkCacheHeaders(t, handler,
				boundArtworkRequest("media-alpha", "poster", version, binding[0], binding[1]))
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if cacheControl != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", cacheControl)
			}
			if etag != "" {
				t.Fatalf("ETag = %q, want no validator for an unestablished decision", etag)
			}
		})
	}
}

// An unbound request is untouched by the binding: it keeps the content-addressed
// long cache.
func TestArtworkWithoutCompatBindingKeepsLongCache(t *testing.T) {
	handler, svc := newArtworkTestHandler(t)
	writeTestPoster(t, svc, "media-alpha")
	version, ok := svc.ArtworkVersion("media-alpha", artwork.SlotPoster)
	if !ok {
		t.Fatal("ArtworkVersion = absent, want poster version")
	}

	code, cacheControl, etag := artworkCacheHeaders(t, handler, artworkRequest("media-alpha", "poster?v="+version))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if cacheControl != "private, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q, want the unbound long cache", cacheControl)
	}
	if etag != `"`+version+`"` {
		t.Fatalf("ETag = %q, want the bare content version", etag)
	}
}

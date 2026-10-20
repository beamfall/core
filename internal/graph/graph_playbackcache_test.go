package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakePlaybackCache is a test double for the PlaybackCacheController seam: it
// records the Media it was asked to precompute and the eviction calls, so the
// graph test can assert the operator mutations reach the controller with the
// right (visible) Media and that gating happens before the controller is
// touched.
type fakePlaybackCache struct {
	budget        int64
	precomputed   []store.MediaID
	evictCalls    int
	precomputeRet int
	evictRet      int
}

func (f *fakePlaybackCache) PrecomputeMedia(_ context.Context, mediaID store.MediaID) (int, error) {
	f.precomputed = append(f.precomputed, mediaID)
	return f.precomputeRet, nil
}

func (f *fakePlaybackCache) EvictOverBudget(context.Context) (int, error) {
	f.evictCalls++
	return f.evictRet, nil
}

func (f *fakePlaybackCache) BudgetBytes() int64 { return f.budget }

// TestPlaybackRenditionCacheConsoleAdminGated is the Console vertical slice for
// the precomputed playback rendition cache controls: the health
// snapshot, the explicit precompute generation trigger (§2 — never automatic),
// and the budget eviction (§4) are all Admin-Session gated, and the generation
// trigger routes a visible Media to the controller while an unknown/hidden
// Media is rejected as an existence-oracle-safe "unknown media".
//
// feature:precomputed-renditions
func TestPlaybackRenditionCacheConsoleAdminGated(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/m/precompute.mkv",
		Title:               "precompute",
		Container:           "mkv",
		Codec:               "hevc",
		ContentFingerprint:  "sha256:precompute",
		DurationSecondsHint: 100,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	cache := &fakePlaybackCache{budget: 4096, precomputeRet: 3, evictRet: 2}
	handler := newTestHandlerWithStore(st)
	handler.SetPlaybackCache(cache)

	// Admin gate: before re-auth, every cache field is denied.
	_, errs := post(t, handler, `{"query":"query { playbackRenditionCache { ready budgetBytes } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin playbackRenditionCache errors = %#v, want admin gate", errs)
	}
	_, errs = post(t, handler,
		`{"query":"mutation($mediaID:ID!){ precomputePlaybackRenditions(mediaID:$mediaID){ affected } }","variables":{"mediaID":"`+string(media.ID)+`"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("pre-admin precompute errors = %#v, want admin gate", errs)
	}
	if len(cache.precomputed) != 0 {
		t.Fatalf("controller reached before admin gate: %v", cache.precomputed)
	}

	// Open the Admin Session window.
	if _, errs := post(t, handler,
		`{"query":"mutation V($password:String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("admin re-auth errors = %#v", errs)
	}

	// Cache health snapshot: configured (controller wired) with the budget.
	data, errs := post(t, handler, `{"query":"query { playbackRenditionCache { configured ready budgetBytes } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("playbackRenditionCache errors = %#v", errs)
	}
	var info PlaybackCacheInfo
	mustUnmarshal(t, data["playbackRenditionCache"], &info)
	if !info.Configured || info.BudgetBytes != 4096 {
		t.Fatalf("cache info = %+v, want configured budget 4096", info)
	}

	// Explicit generation trigger on the visible Media reaches the controller.
	data, errs = post(t, handler,
		`{"query":"mutation($mediaID:ID!){ precomputePlaybackRenditions(mediaID:$mediaID){ affected } }","variables":{"mediaID":"`+string(media.ID)+`"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("precompute errors = %#v", errs)
	}
	var gen PlaybackCacheOpResult
	mustUnmarshal(t, data["precomputePlaybackRenditions"], &gen)
	if gen.Affected != 3 {
		t.Fatalf("precompute affected = %d, want 3", gen.Affected)
	}
	if len(cache.precomputed) != 1 || cache.precomputed[0] != media.ID {
		t.Fatalf("controller precompute calls = %v, want [%s]", cache.precomputed, media.ID)
	}

	// An unknown Media is rejected without an existence oracle and never reaches
	// the controller.
	_, errs = post(t, handler,
		`{"query":"mutation($mediaID:ID!){ precomputePlaybackRenditions(mediaID:$mediaID){ affected } }","variables":{"mediaID":"media-nope"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown media") {
		t.Fatalf("unknown-media errors = %#v, want unknown media", errs)
	}
	if len(cache.precomputed) != 1 {
		t.Fatalf("controller reached for unknown media: %v", cache.precomputed)
	}

	// Budget eviction reaches the controller.
	data, errs = post(t, handler, `{"query":"mutation{ evictPlaybackRenditionCache{ affected } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("evict errors = %#v", errs)
	}
	var evicted PlaybackCacheOpResult
	mustUnmarshal(t, data["evictPlaybackRenditionCache"], &evicted)
	if evicted.Affected != 2 || cache.evictCalls != 1 {
		t.Fatalf("evict affected = %d (calls %d), want 2 (1 call)", evicted.Affected, cache.evictCalls)
	}
}

// TestPlaybackRenditionCacheUnconfiguredRefusesWrites proves the cache controls
// honestly report "unconfigured" and refuse the generation/eviction mutations
// when no controller is wired (no disk budget set), rather than silently
// dropping the write — the read still works and reports Configured=false.
//
// feature:precomputed-renditions
func TestPlaybackRenditionCacheUnconfiguredRefusesWrites(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st) // no SetPlaybackCache

	if _, errs := post(t, handler,
		`{"query":"mutation V($password:String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("admin re-auth errors = %#v", errs)
	}

	data, errs := post(t, handler, `{"query":"query { playbackRenditionCache { configured budgetBytes } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("playbackRenditionCache errors = %#v", errs)
	}
	var info PlaybackCacheInfo
	mustUnmarshal(t, data["playbackRenditionCache"], &info)
	if info.Configured || info.BudgetBytes != 0 {
		t.Fatalf("unconfigured cache info = %+v, want not configured, budget 0", info)
	}

	_, errs = post(t, handler,
		`{"query":"mutation($mediaID:ID!){ precomputePlaybackRenditions(mediaID:$mediaID){ affected } }","variables":{"mediaID":"media-any"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not configured") {
		t.Fatalf("unconfigured precompute errors = %#v, want not configured", errs)
	}
	_, errs = post(t, handler, `{"query":"mutation{ evictPlaybackRenditionCache{ affected } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not configured") {
		t.Fatalf("unconfigured evict errors = %#v, want not configured", errs)
	}
}

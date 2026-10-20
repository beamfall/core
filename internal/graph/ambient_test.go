package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// AV-1 / ADR-0035: the Ambient Display posture — Ambient Queue assembled live
// from a Source (Reveal-scoped, never persisted) and engagement-silent
// playback that emits no Engagement Event.

// TestAmbientQueueAssemblesFromSourceRevealScoped pins ADR-0035 §3: the queue
// is assembled from a Source through the same reveal-filtered funnel every
// other rail uses, so an unrevealed ambient session never sees restricted
// Media, and a revealed one does.
func TestAmbientQueueAssemblesFromSourceRevealScoped(t *testing.T) {
	handler := newTestHandler(t)
	query := []byte(`{"query":"query AmbientQueue($kind: String!) { ambientQueue(source: { kind: $kind }) { id kind title } }","variables":{"kind":"recently-added"}}`)

	// Unrevealed: restricted media-archive is excluded.
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(query))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var hidden struct {
		Data struct {
			AmbientQueue []SourceItem `json:"ambientQueue"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hidden); err != nil {
		t.Fatal(err)
	}
	if len(hidden.Errors) != 0 {
		t.Fatalf("errors = %#v", hidden.Errors)
	}
	if got := itemIDs(hidden.Data.AmbientQueue); !equalStringSlices(got, []string{"media-alpha"}) {
		t.Fatalf("unrevealed ambient queue = %v, want [media-alpha] (restricted excluded)", got)
	}

	// Revealed: the restricted item joins the queue.
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(query))
	authorizeRevealed(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var revealed struct {
		Data struct {
			AmbientQueue []SourceItem `json:"ambientQueue"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &revealed); err != nil {
		t.Fatal(err)
	}
	// recently-added is newest-first: media-archive was seeded after media-alpha.
	if got := itemIDs(revealed.Data.AmbientQueue); !equalStringSlices(got, []string{"media-archive", "media-alpha"}) {
		t.Fatalf("revealed ambient queue = %v, want [media-archive media-alpha]", got)
	}
}

// TestAmbientQueueIsNotPersisted pins ADR-0035 §3's never-persisted rule: the
// queue is reassembled from the Source on every call (session start +
// reconnect), so a change to the Source's backing Media between two calls is
// reflected in the second assembly. A persisted queue object would replay its
// stored membership and miss the new item; a live re-evaluation cannot.
func TestAmbientQueueIsNotPersisted(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	collect := func() []string {
		query := []byte(`{"query":"query AmbientQueue($kind: String!) { ambientQueue(source: { kind: $kind }) { id } }","variables":{"kind":"recently-added"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(query))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Data struct {
				AmbientQueue []SourceItem `json:"ambientQueue"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return itemIDs(resp.Data.AmbientQueue)
	}

	first := collect()
	// The unrevealed viewer sees only the one visible seed Media.
	if !equalStringSlices(first, []string{"media-alpha"}) {
		t.Fatalf("first ambient queue = %v, want [media-alpha]", first)
	}

	// Mutate the Source's backing data: import a new visible Media.
	added := seedAmbientMedia(t, st, 1)

	second := collect()
	// A fresh assembly reflects the new item; a persisted queue would not.
	if len(second) != 2 || !equalStringSets(second, append([]string{"media-alpha"}, added...)) {
		t.Fatalf("second ambient queue = %v, want fresh assembly including the newly added Media %v", second, added)
	}
}

// TestAmbientPlaybackIsEngagementSilent pins ADR-0035 §4: an ambient play
// writes NO Engagement Event. After reportPlayback(ambient: true) the events
// DB is untouched — no progress row, so continue-watching stays empty — while
// the same report without the flag DOES record (the control half proves the
// flag is what suppresses the write, not a broken write path).
func TestAmbientPlaybackIsEngagementSilent(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	report := []byte(`{"query":"mutation Report($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int, $ambient: Boolean) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, ambient: $ambient) { mediaID positionSeconds watched } }","variables":{"mediaID":"media-alpha","positionSeconds":45,"durationSeconds":120,"ambient":true}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(report))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			ReportPlayback struct {
				MediaID         string
				PositionSeconds int
				Watched         bool
			} `json:"reportPlayback"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	// The echo reflects the (unchanged) stored state: no row, so position 0.
	if resp.Data.ReportPlayback.PositionSeconds != 0 || resp.Data.ReportPlayback.Watched {
		t.Fatalf("ambient reportPlayback echo = %#v, want zero/unwatched", resp.Data.ReportPlayback)
	}

	// Direct events-DB assertion: NO progress row was written.
	_, ok, err := st.PlaybackProgressFor(t.Context(), "profile-default", "media-alpha")
	if err != nil {
		t.Fatalf("PlaybackProgressFor: %v", err)
	}
	if ok {
		t.Fatal("ambient play wrote a progress row; engagement-silence violated (ADR-0035 §4)")
	}

	// And continue-watching stays empty for the profile.
	cw := []byte(`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"continue-watching"}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(cw))
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var list struct {
		Data struct {
			SourceItems []SourceItem `json:"sourceItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data.SourceItems) != 0 {
		t.Fatalf("continue-watching = %#v, want empty after ambient play", list.Data.SourceItems)
	}

	// Control: the SAME report without the ambient flag DOES record — so the
	// silence above is the flag's effect, not a broken write path.
	live := []byte(`{"query":"mutation Report($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds) { positionSeconds } }","variables":{"mediaID":"media-alpha","positionSeconds":45,"durationSeconds":120}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(live))
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if _, ok, err := st.PlaybackProgressFor(t.Context(), "profile-default", "media-alpha"); err != nil {
		t.Fatalf("PlaybackProgressFor (control): %v", err)
	} else if !ok {
		t.Fatal("non-ambient reportPlayback wrote no row; control invalid (the write path itself is broken)")
	}
}

// seedAmbientMedia imports n visible Media into the seeded open Library and
// returns their store-derived IDs, so a Source's backing data can be mutated
// mid-test (the never-persisted re-assembly proof).
func seedAmbientMedia(t *testing.T, st *store.Store, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		suffix := string(rune('a' + i))
		media, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
			LibraryID:          "library-local",
			Path:               "/library/local/ambient-" + suffix + ".mp4",
			Title:              "Ambient " + string(rune('A'+i)),
			Class:              store.MediaClassVideo,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: "fp-ambient-" + suffix,
		})
		if err != nil {
			t.Fatalf("seed ambient media %d: %v", i, err)
		}
		ids = append(ids, string(media.ID))
	}
	return ids
}

// equalStringSets reports whether a and b contain the same elements, ignoring
// order (membership equality for the never-persisted reshuffle assertion).
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

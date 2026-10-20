package graph

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeWatchedObserver records the watched transitions it is notified of (the
// scrobble seam, ADR-0049 §2.7).
type fakeWatchedObserver struct {
	mu  sync.Mutex
	got []WatchedTransition
}

func (o *fakeWatchedObserver) Watched(t WatchedTransition) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.got = append(o.got, t)
}

func (o *fakeWatchedObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.got)
}

// TestReportPlaybackFiresWatchedObserverOnceOnTransition: finishing a Media
// fires the watched observer exactly once on the unwatched→watched transition,
// and a repeated finished-report does NOT re-fire it (so a service is never
// double-scrobbled by repeated position reports past the threshold).
func TestReportPlaybackFiresWatchedObserverOnceOnTransition(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	obs := &fakeWatchedObserver{}
	h.SetWatchedObserver(obs)

	finish := `{"query":"mutation { reportPlayback { watched } }","variables":{"mediaID":"media-alpha","positionSeconds":120,"durationSeconds":120,"finished":true}}`
	if _, errs := post(t, h, finish, authorizeViewer); len(errs) != 0 {
		t.Fatalf("reportPlayback errors = %#v", errs)
	}
	if obs.count() != 1 {
		t.Fatalf("watched observer should fire once on the transition, fired %d", obs.count())
	}
	tr := obs.got[0]
	if tr.MediaID != "media-alpha" || tr.LibraryID != "library-local" || string(tr.ProfileID) != "profile-default" {
		t.Fatalf("transition payload mismatch: %+v", tr)
	}

	// A second finished-report on the already-watched item must NOT re-fire.
	if _, errs := post(t, h, finish, authorizeViewer); len(errs) != 0 {
		t.Fatalf("second reportPlayback errors = %#v", errs)
	}
	if obs.count() != 1 {
		t.Fatalf("observer must fire only on the TRANSITION, not on repeated reports; fired %d", obs.count())
	}
}

// fakeSecretStore is an in-memory SubscriptionSecretStore: it records the
// secrets written by the scrobble link mutation so a test can assert the
// credential went to the secure store (not the DB) — and that nothing echoes it
// back. SetSecret("") clears, mirroring the real 0600 store.
type fakeSecretStore struct {
	mu      sync.Mutex
	secrets map[string]string
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{secrets: map[string]string{}}
}

func (f *fakeSecretStore) SetSecret(ref, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.TrimSpace(value) == "" {
		delete(f.secrets, ref)
		return nil
	}
	f.secrets[ref] = value
	return nil
}

func (f *fakeSecretStore) get(ref string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.secrets[ref]
	return v, ok
}

// scrobbleToken is the secret the link mutation must route to the secure store
// and NEVER into the engagement DB or the My-Data export.
const scrobbleToken = "SECRET-OAUTH-TOKEN-do-not-leak-7f3a"

// TestScrobbleLinkSecretGoesToSecureStoreNotDB: linking writes the credential to
// the secure store under the deterministic ref and records a NON-SECRET link
// row; the mutation echoes the link back with no token (invariant #5).
func TestScrobbleLinkSecretGoesToSecureStoreNotDB(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	secrets := newFakeSecretStore()
	h.SetSubscriptionSecrets(secrets)

	body := `{"query":"mutation { linkScrobble { service accountLabel linked } }","variables":{"service":"trakt","credential":"` + scrobbleToken + `","accountLabel":"alice"}}`
	data, errs := post(t, h, body, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("linkScrobble errors = %#v", errs)
	}
	var info ScrobbleLinkInfo
	mustUnmarshal(t, data["linkScrobble"], &info)
	if !info.Linked || info.Service != "trakt" || info.AccountLabel != "alice" {
		t.Fatalf("link echo mismatch: %+v", info)
	}

	// The credential went to the secure store under the deterministic ref.
	stored, ok := secrets.get("scrobble-profile-default-trakt")
	if !ok || stored != scrobbleToken {
		t.Fatalf("credential not written to secure store under expected ref")
	}

	// The raw mutation response must not echo the token anywhere.
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), scrobbleToken) {
		t.Fatalf("link response leaked the token: %s", raw)
	}
}

// TestScrobbleExportCarriesNoToken is the invariant-#5 negative test: a linked
// Profile's My-Data export lists the connection (service + label) but contains
// NO token anywhere in the marshaled archive.
func TestScrobbleExportCarriesNoToken(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	secrets := newFakeSecretStore()
	h.SetSubscriptionSecrets(secrets)

	// Link first.
	linkBody := `{"query":"mutation { linkScrobble { linked } }","variables":{"service":"lastfm","credential":"` + scrobbleToken + `","accountLabel":"alice-fm"}}`
	if _, errs := post(t, h, linkBody, authorizeViewer); len(errs) != 0 {
		t.Fatalf("linkScrobble errors = %#v", errs)
	}

	// Export the whole My-Data archive and assert: connection present, token absent.
	exportBody := `{"query":"query Q($pin: String){ myEngagement(pin: $pin){ schema connections { service accountLabel linkedAt } } }","variables":{"pin":"1234"}}`
	data, errs := post(t, h, exportBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("myEngagement errors = %#v", errs)
	}
	var export MyEngagementExportInfo
	mustUnmarshal(t, data["myEngagement"], &export)
	if len(export.Connections) != 1 {
		t.Fatalf("want 1 exported connection, got %d", len(export.Connections))
	}
	if export.Connections[0].Service != "lastfm" || export.Connections[0].AccountLabel != "alice-fm" {
		t.Fatalf("connection mismatch: %+v", export.Connections[0])
	}

	// The marshaled export bytes must NOT contain the token (invariant #5).
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), scrobbleToken) {
		t.Fatalf("My-Data export LEAKED the scrobble token: %s", raw)
	}
	// And it must not contain the secret_ref either (the export drops even the
	// handle — see store.MyScrobbleConnection).
	if strings.Contains(string(raw), "scrobble-profile-default-lastfm") {
		t.Fatalf("My-Data export leaked the secret ref: %s", raw)
	}
}

// TestUnlinkScrobbleClearsSecretAndRow: unlinking clears the secure-store
// credential and removes the link, so neither outlives the unlink.
func TestUnlinkScrobbleClearsSecretAndRow(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	secrets := newFakeSecretStore()
	h.SetSubscriptionSecrets(secrets)

	linkBody := `{"query":"mutation { linkScrobble { linked } }","variables":{"service":"trakt","credential":"` + scrobbleToken + `"}}`
	if _, errs := post(t, h, linkBody, authorizeViewer); len(errs) != 0 {
		t.Fatalf("link: %#v", errs)
	}
	if _, ok := secrets.get("scrobble-profile-default-trakt"); !ok {
		t.Fatal("precondition: credential should be stored after link")
	}

	unlinkBody := `{"query":"mutation { unlinkScrobble { linked } }","variables":{"service":"trakt"}}`
	data, errs := post(t, h, unlinkBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("unlink: %#v", errs)
	}
	var info ScrobbleLinkInfo
	mustUnmarshal(t, data["unlinkScrobble"], &info)
	if info.Linked {
		t.Fatal("unlink should report not linked")
	}
	if _, ok := secrets.get("scrobble-profile-default-trakt"); ok {
		t.Fatal("unlink must clear the secure-store credential")
	}

	// And the export now lists no connections.
	exportBody := `{"query":"query Q($pin: String){ myEngagement(pin: $pin){ connections { service } } }","variables":{"pin":"1234"}}`
	exData, _ := post(t, h, exportBody, authorizeViewer)
	var export MyEngagementExportInfo
	mustUnmarshal(t, exData["myEngagement"], &export)
	if len(export.Connections) != 0 {
		t.Fatalf("after unlink, want 0 connections, got %d", len(export.Connections))
	}
}

// --- MUSIC-7 status reads + richer outbound metadata (ADR-0098 area 7) -------

// fakeStatusSource is an in-memory ScrobbleStatusSource for the status read test.
type fakeStatusSource struct {
	dead     []ScrobbleDeadLetterInfo
	skips    []ScrobbleSkipInfo
	adapters []ScrobbleAdapterInfo
}

func (f fakeStatusSource) DeadLetters() []ScrobbleDeadLetterInfo { return f.dead }
func (f fakeStatusSource) Skips() []ScrobbleSkipInfo             { return f.skips }
func (f fakeStatusSource) Adapters() []ScrobbleAdapterInfo       { return f.adapters }

// TestScrobbleLinksReadReturnsOwnLinksNoToken: scrobbleLinks returns the session
// Profile's OWN links without any token.
func TestScrobbleLinksReadReturnsOwnLinksNoToken(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	h.SetSubscriptionSecrets(newFakeSecretStore())
	link := `{"query":"mutation { linkScrobble { linked } }","variables":{"service":"lastfm","credential":"` + scrobbleToken + `","accountLabel":"alice"}}`
	if _, errs := post(t, h, link, authorizeViewer); len(errs) != 0 {
		t.Fatalf("linkScrobble errors = %#v", errs)
	}
	data, errs := post(t, h, `{"query":"{ scrobbleLinks { service accountLabel linked linkedAt } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("scrobbleLinks errors = %#v", errs)
	}
	var links []ScrobbleLinkInfo
	mustUnmarshal(t, data["scrobbleLinks"], &links)
	if len(links) != 1 || links[0].Service != "lastfm" || !links[0].Linked || links[0].AccountLabel != "alice" {
		t.Fatalf("scrobbleLinks mismatch: %+v", links)
	}
	if raw, _ := json.Marshal(data); strings.Contains(string(raw), scrobbleToken) {
		t.Fatalf("scrobbleLinks leaked the token: %s", raw)
	}
}

// TestScrobbleEligibilityReadReflectsToggle: scrobbleEligibility lists per-
// visible-Library eligibility, reflecting an operator toggle.
func TestScrobbleEligibilityReadReflectsToggle(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	if err := st.SetScrobbleLibraryEligibility(t.Context(), "library-local", true); err != nil {
		t.Fatalf("seed eligibility: %v", err)
	}
	data, errs := post(t, h, `{"query":"{ scrobbleEligibility { libraryID eligible } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("scrobbleEligibility errors = %#v", errs)
	}
	var infos []ScrobbleEligibilityInfo
	mustUnmarshal(t, data["scrobbleEligibility"], &infos)
	var found bool
	for _, i := range infos {
		if i.LibraryID == "library-local" {
			found = true
			if !i.Eligible {
				t.Fatalf("library-local should be eligible: %+v", infos)
			}
		}
	}
	if !found {
		t.Fatalf("scrobbleEligibility did not include the visible library: %+v", infos)
	}
}

// TestScrobbleStatusRequiresAdminAndReturnsFeeds: scrobbleStatus is Admin-Session
// gated (cross-Profile diagnostics); with an Admin Session it returns the wired
// dead-letter/skip/adapter feeds.
func TestScrobbleStatusRequiresAdminAndReturnsFeeds(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	h.SetScrobbleStatusSource(fakeStatusSource{
		dead:     []ScrobbleDeadLetterInfo{{ProfileID: "p1", Service: "lastfm", MediaID: "m1", Attempts: 5, LastError: "boom", At: "2026-01-01T00:00:00Z"}},
		skips:    []ScrobbleSkipInfo{{ProfileID: "p2", MediaID: "m2", Reason: "no external id", At: "2026-01-02T00:00:00Z"}},
		adapters: []ScrobbleAdapterInfo{{Service: "lastfm", MediaClass: "audio", IdentitySchemes: []string{"musicbrainz-recording"}}},
	})

	q := `{"query":"{ scrobbleStatus { deadLetters { mediaID lastError } skips { reason } adapters { service mediaClass } } }"}`
	// Non-admin manager: refused.
	if _, errs := post(t, h, q, authorizeManager); len(errs) == 0 {
		t.Fatal("scrobbleStatus without an admin session must be refused")
	}
	// With an Admin Session: returns the feeds.
	openManagerAdminSession(t, st)
	data, errs := post(t, h, q, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("scrobbleStatus (admin) errors = %#v", errs)
	}
	var status ScrobbleStatusInfo
	mustUnmarshal(t, data["scrobbleStatus"], &status)
	if len(status.DeadLetters) != 1 || status.DeadLetters[0].MediaID != "m1" {
		t.Fatalf("dead-letter feed mismatch: %+v", status.DeadLetters)
	}
	if len(status.Skips) != 1 || status.Skips[0].Reason != "no external id" {
		t.Fatalf("skip feed mismatch: %+v", status.Skips)
	}
	if len(status.Adapters) != 1 || status.Adapters[0].Service != "lastfm" {
		t.Fatalf("adapter feed mismatch: %+v", status.Adapters)
	}
}

// TestScrobbleStatusEmptyWhenSourceNil: with no status source wired (plugin off),
// the admin read returns empty feeds, never an error.
func TestScrobbleStatusEmptyWhenSourceNil(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	data, errs := post(t, h, `{"query":"{ scrobbleStatus { deadLetters { mediaID } } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("scrobbleStatus with nil source must not error: %#v", errs)
	}
	var status ScrobbleStatusInfo
	mustUnmarshal(t, data["scrobbleStatus"], &status)
	if len(status.DeadLetters) != 0 {
		t.Fatalf("nil status source should yield empty dead-letter feed: %+v", status)
	}
}

// recordingMetadataResolver records whether it was asked to resolve and for
// which Media, returning canned metadata (MUSIC-7 threading test).
type recordingMetadataResolver struct {
	called bool
	got    store.MediaID
	md     OutboundAudioMetadata
}

func (r *recordingMetadataResolver) ResolveOutboundAudioMetadata(_ context.Context, id store.MediaID) OutboundAudioMetadata {
	r.called = true
	r.got = id
	return r.md
}

// TestOutboundMetadataResolverNotInvokedForVideo: a video watched transition
// (the dev seed's media-alpha is video) does NOT invoke the audio-metadata
// resolver, so a video scrobble carries no audio enrichment (MUSIC-7: resolution
// is audio-only).
func TestOutboundMetadataResolverNotInvokedForVideo(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	obs := &fakeWatchedObserver{}
	h.SetWatchedObserver(obs)
	resolver := &recordingMetadataResolver{md: OutboundAudioMetadata{ContributorName: "Should Not Appear"}}
	h.SetOutboundMetadataResolver(resolver)

	finish := `{"query":"mutation { reportPlayback { watched } }","variables":{"mediaID":"media-alpha","positionSeconds":120,"durationSeconds":120,"finished":true}}`
	if _, errs := post(t, h, finish, authorizeViewer); len(errs) != 0 {
		t.Fatalf("reportPlayback errors = %#v", errs)
	}
	if obs.count() != 1 {
		t.Fatalf("observer should fire once, fired %d", obs.count())
	}
	if resolver.called {
		t.Fatalf("metadata resolver must not run for a video transition")
	}
	if obs.got[0].Audio != (OutboundAudioMetadata{}) {
		t.Fatalf("video transition must carry no audio metadata: %+v", obs.got[0].Audio)
	}
}

// TestSetScrobbleEligibilityRequiresAdmin: the eligibility toggle is admin-gated
// (operator decision); a non-admin manager session is refused.
func TestSetScrobbleEligibilityRequiresAdmin(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	body := `{"query":"mutation { setScrobbleEligibility { libraryID eligible } }","variables":{"libraryID":"library-local","eligible":true}}`

	// Non-admin manager: refused.
	if _, errs := post(t, h, body, authorizeManager); len(errs) == 0 {
		t.Fatal("setScrobbleEligibility without an admin session must be refused")
	}

	// With an Admin Session: succeeds and flips eligibility on.
	openManagerAdminSession(t, st)
	data, errs := post(t, h, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setScrobbleEligibility (admin) errors = %#v", errs)
	}
	var info ScrobbleEligibilityInfo
	mustUnmarshal(t, data["setScrobbleEligibility"], &info)
	if !info.Eligible || info.LibraryID != "library-local" {
		t.Fatalf("eligibility echo mismatch: %+v", info)
	}
	eligible, err := st.ScrobbleLibraryEligible(t.Context(), "library-local")
	if err != nil || !eligible {
		t.Fatalf("library should be eligible after admin toggle (err=%v eligible=%v)", err, eligible)
	}
}

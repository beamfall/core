package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeCatalog is an in-memory Catalog.
type fakeCatalog struct {
	media      map[store.MediaID]store.Media
	sessions   []store.ActiveSession
	profiles   map[store.ProfileID]store.Profile
	devices    map[store.DeviceID]store.Device
	activity   []store.ActivityEntry
	latestAct  int64
	latestSeq  int64
	actionSeen []string
	mediaErr   error // returned once by the next Media read
}

func (c *fakeCatalog) Media(_ context.Context, id store.MediaID, _ store.LibraryFilter) (store.Media, error) {
	if c.mediaErr != nil {
		err := c.mediaErr
		c.mediaErr = nil // one-shot
		return store.Media{}, err
	}
	m, ok := c.media[id]
	if !ok {
		return store.Media{}, store.ValidationError("media not found")
	}
	return m, nil
}

func (c *fakeCatalog) ActiveSessions(context.Context, time.Time, store.LibraryFilter) ([]store.ActiveSession, error) {
	return append([]store.ActiveSession(nil), c.sessions...), nil
}

func (c *fakeCatalog) Profile(_ context.Context, id store.ProfileID) (store.Profile, error) {
	p, ok := c.profiles[id]
	if !ok {
		return store.Profile{}, errors.New("not found")
	}
	return p, nil
}

func (c *fakeCatalog) Device(_ context.Context, id store.DeviceID) (store.Device, error) {
	d, ok := c.devices[id]
	if !ok {
		return store.Device{}, errors.New("not found")
	}
	return d, nil
}

func (c *fakeCatalog) ActivityEntriesAfter(_ context.Context, after int64, actions []string, limit int) ([]store.ActivityEntry, error) {
	c.actionSeen = actions
	var out []store.ActivityEntry
	for _, e := range c.activity {
		if e.ID > after && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

func (c *fakeCatalog) LatestActivityID(context.Context) (int64, error) { return c.latestAct, nil }
func (c *fakeCatalog) LatestEventSeq(context.Context) (int64, error)   { return c.latestSeq, nil }

// recordingDoer answers 204 and keeps every request body.
type recordingDoer struct {
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	url     string
	header  http.Header
	body    []byte
	eventID string
}

func (r *recordingDoer) Do(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.reqs = append(r.reqs, recorded{url: req.URL.String(), header: req.Header.Clone(), body: body, eventID: req.Header.Get(EventIDHeader)})
	r.mu.Unlock()
	return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (r *recordingDoer) all() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recorded(nil), r.reqs...)
}

type derivedPayload struct {
	Kind    string `json:"kind"`
	Payload struct {
		MediaID    string `json:"mediaID"`
		Title      string `json:"title"`
		MediaClass string `json:"mediaClass"`
		ArtworkURL string `json:"artworkURL"`
		Profile    string `json:"profile"`
		Device     string `json:"device"`
	} `json:"payload"`
}

func decodeDerived(t *testing.T, body []byte) derivedPayload {
	t.Helper()
	var p derivedPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return p
}

type deriveFixture struct {
	em      *Emitter
	doer    *recordingDoer
	catalog *fakeCatalog
	state   *FileDeadLetter
	events  *fixedEvents
	sealed  map[store.LibraryID]bool
}

func newDeriveFixture(t *testing.T, kinds []string) *deriveFixture {
	t.Helper()
	state, err := OpenDeadLetter(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := &deriveFixture{
		doer:   &recordingDoer{},
		events: &fixedEvents{},
		state:  state,
		sealed: map[store.LibraryID]bool{},
		catalog: &fakeCatalog{
			media:    map[store.MediaID]store.Media{},
			profiles: map[store.ProfileID]store.Profile{},
			devices:  map[store.DeviceID]store.Device{},
		},
	}
	cfg := Config{Endpoints: []Endpoint{{ID: "ep", URL: "https://hooks.example/in", Secret: []byte("k"), EventKinds: kinds}}}
	f.em = newEmitter(t, cfg, f.events, f.doer, state)
	f.em.EnableDerivation(DeriveOptions{
		Catalog:       f.catalog,
		Sealed:        func(id store.LibraryID) bool { return f.sealed[id] },
		PublicBaseURL: func() string { return "https://media.example" },
	})
	return f
}

func (f *deriveFixture) tick(t *testing.T, cursor int64) int64 {
	t.Helper()
	next, err := f.em.tick(context.Background(), cursor)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return next
}

func TestMediaAddedCarriesTitleTypeAndArtwork(t *testing.T) {
	f := newDeriveFixture(t, []string{KindMediaAdded})
	f.catalog.latestSeq = 4
	f.catalog.media["media-1"] = store.Media{ID: "media-1", LibraryID: "lib", Title: "Sample Clip", Class: store.MediaClassVideo}
	f.events.events = []store.OutboxEvent{mediaEvent(3, store.MediaEventCreated), mediaEvent(5, store.MediaEventCreated)}

	if got := f.tick(t, 0); got != 5 {
		t.Fatalf("cursor = %d, want 5", got)
	}
	reqs := f.doer.all()
	if len(reqs) != 1 {
		t.Fatalf("want one delivery (seq 3 predates the seeded tail), got %d", len(reqs))
	}
	if reqs[0].eventID != "media.added-5" {
		t.Fatalf("event id = %q", reqs[0].eventID)
	}
	p := decodeDerived(t, reqs[0].body)
	if p.Kind != KindMediaAdded || p.Payload.Title != "Sample Clip" || p.Payload.MediaClass != "video" {
		t.Fatalf("payload = %+v", p)
	}
	if p.Payload.ArtworkURL != "https://media.example/api/media/artwork/media-1/poster" {
		t.Fatalf("artwork URL = %q", p.Payload.ArtworkURL)
	}
	if reqs[0].header.Get(SignatureHeader) == "" {
		t.Fatal("a generic derived delivery must be signed")
	}
}

func TestMediaAddedSkipsSealedLibrary(t *testing.T) {
	f := newDeriveFixture(t, []string{KindMediaAdded})
	f.sealed["vault"] = true
	f.catalog.media["media-1"] = store.Media{ID: "media-1", LibraryID: "vault", Title: "Hidden", Class: store.MediaClassImage}
	f.events.events = []store.OutboxEvent{mediaEvent(1, store.MediaEventCreated)}
	f.tick(t, 0)
	if n := len(f.doer.all()); n != 0 {
		t.Fatalf("a sealed Library's Media must not be announced, got %d deliveries", n)
	}
}

func TestPlaybackPrimesThenDiffs(t *testing.T) {
	f := newDeriveFixture(t, []string{KindPlaybackStarted, KindPlaybackStopped})
	a := store.ActiveSession{SessionID: "s1", MediaID: "m1", Title: "First", ProfileName: "Ada", DeviceName: "Den TV"}
	b := store.ActiveSession{SessionID: "s2", MediaID: "m2", Title: "Second"}
	f.catalog.media["m1"] = store.Media{ID: "m1", Title: "First"}
	f.catalog.media["m2"] = store.Media{ID: "m2", Title: "Second"}
	f.catalog.sessions = []store.ActiveSession{a}
	f.tick(t, 0)
	if n := len(f.doer.all()); n != 0 {
		t.Fatalf("the first snapshot must emit nothing, got %d", n)
	}
	f.catalog.sessions = []store.ActiveSession{a, b}
	f.tick(t, 0)
	reqs := f.doer.all()
	if len(reqs) != 1 {
		t.Fatalf("want one start, got %d", len(reqs))
	}
	if p := decodeDerived(t, reqs[0].body); p.Kind != KindPlaybackStarted || p.Payload.Title != "Second" {
		t.Fatalf("start payload = %+v", p)
	}
	f.catalog.sessions = []store.ActiveSession{b}
	f.tick(t, 0)
	reqs = f.doer.all()
	if len(reqs) != 2 {
		t.Fatalf("want a stop, got %d deliveries", len(reqs))
	}
	p := decodeDerived(t, reqs[1].body)
	if p.Kind != KindPlaybackStopped || p.Payload.Title != "First" || p.Payload.Profile != "Ada" || p.Payload.Device != "Den TV" {
		t.Fatalf("stop payload = %+v", p)
	}
}

func TestPlaybackSkipsSealedSessions(t *testing.T) {
	f := newDeriveFixture(t, []string{KindPlaybackStarted})
	f.sealed["vault"] = true
	f.tick(t, 0) // prime
	f.catalog.sessions = []store.ActiveSession{{SessionID: "s1", MediaID: "m1", LibraryID: "vault", Title: "Hidden"}}
	f.tick(t, 0)
	if n := len(f.doer.all()); n != 0 {
		t.Fatalf("a sealed session must not be reported, got %d", n)
	}
}

// A session that drops out of the live view because its Library was sealed
// or restricted after playback started is not reported as stopped: the stop
// would replay the cached title, Profile and device.
func TestPlaybackStopSkipsSessionsHiddenSinceStart(t *testing.T) {
	for name, hide := range map[string]func(f *deriveFixture){
		"sealed, row still live": func(f *deriveFixture) { f.sealed["lib"] = true },
		"sealed, row filtered": func(f *deriveFixture) {
			f.sealed["lib"] = true
			f.catalog.sessions = nil
		},
		"restricted": func(f *deriveFixture) {
			delete(f.catalog.media, "m1") // the filtered read no longer returns it
			f.catalog.sessions = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDeriveFixture(t, []string{KindPlaybackStopped})
			f.catalog.media["m1"] = store.Media{ID: "m1", LibraryID: "lib", Title: "Private"}
			f.catalog.sessions = []store.ActiveSession{{SessionID: "s1", MediaID: "m1", LibraryID: "lib", Title: "Private", ProfileName: "Ada"}}
			f.tick(t, 0) // prime with the visible session
			hide(f)
			f.tick(t, 0)
			if n := len(f.doer.all()); n != 0 {
				t.Fatalf("a session hidden since it started must not be reported, got %d deliveries", n)
			}
		})
	}
}

// cancelOnceDoer cancels the tick's context on its first request and fails
// it, as a shutdown during delivery would.
type cancelOnceDoer struct {
	next   Doer
	cancel context.CancelFunc
	done   bool
}

func (c *cancelOnceDoer) Do(req *http.Request) (*http.Response, error) {
	if !c.done {
		c.done = true
		c.cancel()
		return nil, context.Canceled
	}
	return c.next.Do(req)
}

// A shutdown during an activity delivery leaves the cursor before that row,
// so the next tick delivers it instead of skipping it.
func TestActivityCursorAdvancesOnlyAfterDelivery(t *testing.T) {
	f := newDeriveFixture(t, []string{KindDeviceRevoked})
	f.catalog.devices["d9"] = store.Device{ID: "d9", Name: "Old Phone"}
	f.tick(t, 0) // seed the tail at 0
	f.catalog.activity = []store.ActivityEntry{
		{ID: 1, Action: store.ActivityAdminLogin}, // not subscribed: passed over
		{ID: 2, Action: store.ActivityDeviceRevoke, TargetType: "device", TargetID: "d9"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.em.clients["ep"] = endpointClient{doer: &cancelOnceDoer{next: f.doer, cancel: cancel}, url: "https://hooks.example/in"}
	if _, err := f.em.tick(ctx, 0); err == nil {
		t.Fatal("an interrupted tick must report the interruption")
	}
	if got := f.state.ActivityCursor(); got != 1 {
		t.Fatalf("activity cursor = %d after an interrupted delivery, want 1", got)
	}
	f.tick(t, 0)
	reqs := f.doer.all()
	if len(reqs) != 1 || reqs[0].eventID != "activity-2" {
		t.Fatalf("the interrupted row must be delivered on the next tick, got %d requests", len(reqs))
	}
	if got := f.state.ActivityCursor(); got != 2 {
		t.Fatalf("activity cursor = %d, want 2", got)
	}
}

// A Media read that fails for an operational reason when a session ends does
// not lose the stop: the next tick delivers it, once.
func TestPlaybackStopRetriedAfterLookupError(t *testing.T) {
	f := newDeriveFixture(t, []string{KindPlaybackStopped})
	f.catalog.media["m1"] = store.Media{ID: "m1", LibraryID: "lib", Title: "First"}
	f.catalog.sessions = []store.ActiveSession{{SessionID: "s1", MediaID: "m1", LibraryID: "lib", Title: "First"}}
	f.tick(t, 0)
	f.catalog.sessions = nil
	f.catalog.mediaErr = errors.New("database is locked")
	if _, err := f.em.tick(context.Background(), 0); err == nil {
		t.Fatal("a failed lookup must surface as a tick error")
	}
	if n := len(f.doer.all()); n != 0 {
		t.Fatalf("no stop may be sent while visibility is unknown, got %d", n)
	}
	f.tick(t, 0)
	f.tick(t, 0)
	reqs := f.doer.all()
	if len(reqs) != 1 || decodeDerived(t, reqs[0].body).Kind != KindPlaybackStopped {
		t.Fatalf("want exactly one retried stop, got %d requests", len(reqs))
	}
}

func TestActivityEventsResolveNames(t *testing.T) {
	f := newDeriveFixture(t, []string{KindProfileSwitched, KindDeviceRevoked})
	f.catalog.latestAct = 2
	f.catalog.profiles["p2"] = store.Profile{ID: "p2", Name: "Grace"}
	f.catalog.devices["d9"] = store.Device{ID: "d9", Name: "Old Phone"}
	f.catalog.activity = []store.ActivityEntry{
		{ID: 1, Action: store.ActivityProfileSwitch, TargetType: "profile", TargetID: "p2"}, // before the seeded tail
		{ID: 3, Action: store.ActivityProfileSwitch, TargetType: "profile", TargetID: "p2"},
		{ID: 4, Action: store.ActivityProfileSwitch, TargetType: "profile", TargetID: "p2", LibraryID: "lib"},
		{ID: 5, Action: store.ActivityAdminLogin},
		{ID: 6, Action: store.ActivityDeviceRevoke, TargetType: "device", TargetID: "d9"},
	}
	f.tick(t, 0)
	reqs := f.doer.all()
	if len(reqs) != 2 {
		t.Fatalf("want profile switch and device revoke, got %d", len(reqs))
	}
	if p := decodeDerived(t, reqs[0].body); p.Kind != KindProfileSwitched || p.Payload.Profile != "Grace" {
		t.Fatalf("profile payload = %+v", p)
	}
	if reqs[0].eventID != "activity-3" {
		t.Fatalf("event id = %q", reqs[0].eventID)
	}
	if p := decodeDerived(t, reqs[1].body); p.Kind != KindDeviceRevoked || p.Payload.Device != "Old Phone" {
		t.Fatalf("device payload = %+v", p)
	}
	if f.state.ActivityCursor() != 6 {
		t.Fatalf("activity cursor = %d, want 6", f.state.ActivityCursor())
	}
	for _, action := range f.catalog.actionSeen {
		if _, ok := activityKinds[action]; !ok {
			t.Fatalf("activity read asked for a non-allowlisted action %q", action)
		}
	}
}

func TestSeedKeepsOutboxCursorOnUpgrade(t *testing.T) {
	f := newDeriveFixture(t, []string{KindMediaAdded})
	f.catalog.latestSeq = 50
	if err := f.state.SetCursor(10); err != nil {
		t.Fatal(err)
	}
	if got := f.tick(t, 10); got != 10 {
		t.Fatalf("an upgraded state must resume at its outbox cursor, got %d", got)
	}
	if !f.state.Seeded() {
		t.Fatal("seed must be recorded")
	}
}

func TestIdleClearsSeedSoReenableStartsAtTail(t *testing.T) {
	f := newDeriveFixture(t, []string{KindMediaAdded})
	f.tick(t, 0)
	if !f.state.Seeded() {
		t.Fatal("an enabled endpoint seeds")
	}
	disabled := f.em.endpoints()
	disabled[0].Disabled = true
	if err := f.em.SetEndpoints(disabled); err != nil {
		t.Fatal(err)
	}
	f.tick(t, 0)
	if f.state.Seeded() {
		t.Fatal("with nothing enabled the seed is cleared")
	}
}

func TestTestSendRecordsDelivery(t *testing.T) {
	f := newDeriveFixture(t, nil)
	disabled := f.em.endpoints()
	disabled[0].Disabled = true
	if err := f.em.SetEndpoints(disabled); err != nil {
		t.Fatal(err)
	}
	res, err := f.em.Test(context.Background(), "ep")
	if err != nil || !res.OK || res.StatusClass != "2xx" {
		t.Fatalf("test result = %+v, %v", res, err)
	}
	if p := decodeDerived(t, f.doer.all()[0].body); p.Kind != KindTest {
		t.Fatalf("test payload kind = %q", p.Kind)
	}
	log := f.state.Deliveries()
	if len(log) != 1 || !log[0].Test || log[0].Outcome != OutcomeDelivered || log[0].EventKind != KindTest {
		t.Fatalf("delivery log = %+v", log)
	}
	if _, err := f.em.Test(context.Background(), "missing"); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("unknown endpoint: %v", err)
	}
}

func TestSetEndpointsRejectsInvalidSetAtomically(t *testing.T) {
	f := newDeriveFixture(t, nil)
	before := f.em.endpoints()
	bad := append(before, Endpoint{ID: "chat", URL: "https://discord.example/x", Format: FormatDiscord, EventKinds: []string{"media.created"}})
	if err := f.em.SetEndpoints(bad); err == nil {
		t.Fatal("a chat endpoint subscribed to a spine kind must be rejected")
	}
	if after := f.em.endpoints(); len(after) != len(before) {
		t.Fatalf("a rejected set must leave the running set unchanged: %d -> %d", len(before), len(after))
	}
}

// A delivery that began before a reload keeps its old endpoint snapshot. Once
// that ID is reinstalled with another URL or local-network approval, the stale
// snapshot gets no client, so a retry cannot borrow the replacement's egress
// approval.
func TestStaleEndpointSnapshotCannotBorrowReplacementClient(t *testing.T) {
	doer := &recordingDoer{}
	old := Endpoint{ID: "hook", URL: "http://hooks.example/in", Secret: []byte("s")}
	em, err := New(Config{Endpoints: []Endpoint{old}}, &fixedEvents{}, doer, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := outbound{method: http.MethodPost, url: old.URL, header: jsonHeader()}
	if _, err := em.send(context.Background(), old, out); err != nil {
		t.Fatalf("current snapshot: %v", err)
	}

	for _, replacement := range []Endpoint{
		{ID: "hook", URL: "http://192.168.1.10/in", Secret: []byte("s")},
		{ID: "hook", URL: old.URL, Secret: []byte("s"), AllowPrivateNetwork: true},
	} {
		if err := em.SetEndpoints([]Endpoint{replacement}); err != nil {
			t.Fatal(err)
		}
		if _, err := em.send(context.Background(), old, out); !errors.Is(err, ErrRequestDiagnostic) {
			t.Fatalf("stale snapshot after reload to %+v: err = %v, want the request diagnostic", replacement, err)
		}
	}
	if got := len(doer.all()); got != 1 {
		t.Fatalf("requests sent = %d, want only the one before the reload", got)
	}
}

// TestPlaybackWebhookEmitsStopWhenPlaybackGoesIdle covers CORE-2346: the
// playback webhook deriver reads store.ActiveSessions and emits
// playback.stopped once a playback goes quiet past LiveSessionActivityWindow,
// matching Console liveSessions.
func TestPlaybackWebhookEmitsStopWhenPlaybackGoesIdle(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, now.Add(time.Hour)); err != nil {
		t.Fatalf("RecordStreamToken: %v", err)
	}

	state, err := OpenDeadLetter(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	doer := &recordingDoer{}
	cfg := Config{Endpoints: []Endpoint{{
		ID:         "ep",
		URL:        "https://hooks.example/in",
		Secret:     []byte("k"),
		EventKinds: []string{KindPlaybackStarted, KindPlaybackStopped},
	}}}
	em := newEmitter(t, cfg, &fixedEvents{}, doer, state)
	currentNow := now
	em.now = func() time.Time { return currentNow }
	em.EnableDerivation(DeriveOptions{
		Catalog:       st,
		PublicBaseURL: func() string { return "https://media.example" },
	})

	// First tick primes the live-session snapshot without emitting.
	if _, err := em.tick(ctx, 0); err != nil {
		t.Fatalf("prime tick: %v", err)
	}
	if reqs := doer.all(); len(reqs) != 0 {
		t.Fatalf("prime tick emitted %d requests, want 0", len(reqs))
	}

	// Advancing past LiveSessionActivityWindow with no progress drops the idle
	// playback and emits playback.stopped.
	currentNow = now.Add(store.LiveSessionActivityWindow + time.Second)
	if _, err := em.tick(ctx, 0); err != nil {
		t.Fatalf("idle tick: %v", err)
	}
	reqs := doer.all()
	if len(reqs) != 1 || decodeDerived(t, reqs[0].body).Kind != KindPlaybackStopped {
		t.Fatalf("idle tick requests = %+v, want one %s", reqs, KindPlaybackStopped)
	}
}

package integrationhost

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/store"
)

var errNoSuchTarget = errors.New("not found")

// fakeScope resolves media and Libraries through a LibraryFilter the way the
// store does: AllLibraries sees everything, otherwise only AllowedIDs.
type fakeScope struct {
	media       map[store.MediaID]store.LibraryID
	collections map[store.CollectionID]store.Collection
}

func filterSees(filter store.LibraryFilter, id store.LibraryID) bool {
	return filter.AllLibraries || slices.Contains(filter.AllowedIDs, id)
}

func (f *fakeScope) Media(_ context.Context, id store.MediaID, filter store.LibraryFilter) (store.Media, error) {
	library, ok := f.media[id]
	if !ok || !filterSees(filter, library) {
		return store.Media{}, errNoSuchTarget
	}
	return store.Media{ID: id, LibraryID: library}, nil
}

func (f *fakeScope) LibraryVisible(_ context.Context, id store.LibraryID, filter store.LibraryFilter) (bool, error) {
	for _, library := range f.media {
		if library == id {
			return filterSees(filter, id), nil
		}
	}
	return false, nil
}

func (f *fakeScope) CollectionByID(_ context.Context, id store.CollectionID, _ bool) (store.Collection, error) {
	c, ok := f.collections[id]
	if !ok {
		return store.Collection{}, errNoSuchTarget
	}
	return c, nil
}

// fakeWrites records every write that reached a seam.
type fakeWrites struct {
	scope    *fakeScope
	metadata [][]store.MediaID
	members  map[store.CollectionID][]store.MediaID
	artwork  map[store.MediaID]string
	watched  map[store.MediaID]bool
	progress map[store.MediaID]store.PlaybackProgress
	reads    int
	// stamped is the time each outbox-emitting seam was told to stamp its rows.
	stamped []time.Time
}

func newFakeWrites() *fakeWrites {
	return &fakeWrites{
		scope: &fakeScope{
			media: map[store.MediaID]store.LibraryID{
				"m-1": "lib-a", "m-2": "lib-a", "m-hidden": "lib-b",
			},
			collections: map[store.CollectionID]store.Collection{
				"c-hidden": {ID: "c-hidden", LibraryID: "lib-b"},
			},
		},
		members:  map[store.CollectionID][]store.MediaID{},
		artwork:  map[store.MediaID]string{},
		watched:  map[store.MediaID]bool{},
		progress: map[store.MediaID]store.PlaybackProgress{},
	}
}

func (f *fakeWrites) BulkEditApply(_ context.Context, ids []store.MediaID, _ []store.MediaField, _ store.LibraryFilter, now time.Time) (store.BulkOpBatch, error) {
	f.metadata = append(f.metadata, ids)
	f.stamped = append(f.stamped, now)
	return store.BulkOpBatch{ID: "batch-1", Kind: "edit", Affected: len(ids)}, nil
}

func (f *fakeWrites) CreateCollection(_ context.Context, c store.Collection, _ time.Time) (store.Collection, error) {
	c.ID = store.CollectionID("c-new")
	f.scope.collections[c.ID] = c
	return c, nil
}

func (f *fakeWrites) AddCollectionMedia(_ context.Context, id store.CollectionID, ids []store.MediaID, _ store.LibraryFilter, _ time.Time) (store.Collection, error) {
	f.members[id] = append(f.members[id], ids...)
	c := f.scope.collections[id]
	c.MemberCount = len(f.members[id])
	return c, nil
}

func (f *fakeWrites) ImportArtwork(_ context.Context, id store.MediaID, slot string, _ []byte) error {
	f.artwork[id] = slot
	return nil
}

func (f *fakeWrites) SetWatched(_ context.Context, _ store.ProfileID, id store.MediaID, watched bool, _ store.LibraryFilter, now time.Time) error {
	f.watched[id] = watched
	f.stamped = append(f.stamped, now)
	return nil
}

func (f *fakeWrites) PlaybackProgressFor(_ context.Context, _ store.ProfileID, id store.MediaID) (store.PlaybackProgress, bool, error) {
	f.reads++
	p, ok := f.progress[id]
	return p, ok, nil
}

func (f *fakeWrites) deps(audit Auditor, cap *causation.CausationCap) Deps {
	d := Deps{
		Scope: f.scope, Metadata: f, Collections: f, Artwork: f, WatchState: f, Playback: f,
		Profiles: fakeProfiles{libs: []store.LibraryID{"lib-a"}}, Causation: cap,
	}
	if audit != nil {
		d.Audit = audit
	}
	return d
}

func (f *fakeWrites) writeCount() int {
	return len(f.metadata) + len(f.members) + len(f.artwork) + len(f.watched)
}

func writeCap(t *testing.T) (*causation.CausationCap, context.Context) {
	t.Helper()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	cap := causation.NewCausationCap(causation.DefaultLimits(), func() time.Time { return now })
	token, err := cap.TokenFor("outbox:writes", now)
	if err != nil {
		t.Fatal(err)
	}
	return cap, causation.WithToken(context.Background(), token)
}

var pngBytes = []byte("\x89PNG\r\n\x1a\nfixture")

// scopedCalls runs one call of each new method. A nil error means the call
// reached its seam.
var scopedCalls = []struct {
	kind string
	run  func(context.Context, *Host) error
}{
	{PermWriteMetadata, func(ctx context.Context, h *Host) error {
		_, err := h.WriteMetadata(ctx, []string{"m-1"}, []MetadataField{{Name: "sortTitle", Type: "text", Text: "x"}})
		return err
	}},
	{PermWriteCollections, func(ctx context.Context, h *Host) error {
		_, err := h.CreateCollection(ctx, "lib-a", "Weekend picks")
		return err
	}},
	{PermWriteArtwork, func(ctx context.Context, h *Host) error {
		return h.SetArtwork(ctx, "m-1", "poster", pngBytes)
	}},
	{PermWriteWatchState, func(ctx context.Context, h *Host) error {
		return h.SetWatched(ctx, "m-1", true)
	}},
	{PermReadPlaybackState, func(ctx context.Context, h *Host) error {
		_, err := h.ReadPlaybackState(ctx, "m-1")
		return err
	}},
}

func approvedBound(pluginID, kind string) Grant {
	return Grant{PluginID: pluginID, Kind: kind, Approved: true, BoundProfileID: "prof-1"}
}

func TestScopedWritesDeniedWithoutApprovedGrant(t *testing.T) {
	for _, call := range scopedCalls {
		t.Run(call.kind, func(t *testing.T) {
			writes := newFakeWrites()
			audit := &recordingAudit{}
			cap, ctx := writeCap(t)
			for name, grants := range map[string][]Grant{
				"ungranted":  nil,
				"unapproved": {{PluginID: "p", Kind: call.kind, BoundProfileID: "prof-1"}},
			} {
				h := hostWith("p", writes.deps(audit, cap), grants...)
				if err := call.run(ctx, h); !errors.Is(err, ErrPermissionDenied) {
					t.Fatalf("%s: err = %v, want permission denied", name, err)
				}
			}
			if writes.writeCount() != 0 || writes.reads != 0 || len(audit.entries) != 0 {
				t.Fatalf("denied call reached a seam or the audit ledger: writes=%d reads=%d audit=%v",
					writes.writeCount(), writes.reads, audit.actions)
			}
		})
	}
}

func TestScopedWritesRequireAuditLedgerAndSeam(t *testing.T) {
	for _, call := range scopedCalls {
		t.Run(call.kind, func(t *testing.T) {
			writes := newFakeWrites()
			cap, ctx := writeCap(t)
			noAudit := hostWith("p", writes.deps(nil, cap), approvedBound("p", call.kind))
			if err := call.run(ctx, noAudit); !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("no audit ledger: err = %v, want permission denied", err)
			}
			if err := call.run(ctx, hostWith("p", Deps{Audit: &recordingAudit{}, Causation: cap}, approvedBound("p", call.kind))); !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("no seam: err = %v, want permission denied", err)
			}
			if writes.writeCount() != 0 || writes.reads != 0 {
				t.Fatal("call reached a seam without an audit ledger")
			}
		})
	}
}

type failingAudit struct{}

func (failingAudit) AppendActivity(context.Context, store.ActivityEntry) error {
	return errors.New("ledger full")
}

func TestScopedWritesFailWhenAuditFails(t *testing.T) {
	for _, call := range scopedCalls {
		t.Run(call.kind, func(t *testing.T) {
			writes := newFakeWrites()
			cap, ctx := writeCap(t)
			h := hostWith("p", writes.deps(failingAudit{}, cap), approvedBound("p", call.kind))
			if err := call.run(ctx, h); err == nil {
				t.Fatal("call succeeded although the audit write failed")
			}
			if writes.writeCount() != 0 || writes.reads != 0 {
				t.Fatal("call reached its seam although the audit write failed")
			}
		})
	}
}

func TestScopedWritesAuditedPerCall(t *testing.T) {
	want := map[string]string{
		PermWriteMetadata:     store.ActivityPluginMetadataWrite,
		PermWriteCollections:  store.ActivityPluginCollectionWrite,
		PermWriteArtwork:      store.ActivityPluginArtworkWrite,
		PermWriteWatchState:   store.ActivityPluginWatchStateWrite,
		PermReadPlaybackState: store.ActivityPluginPlaybackStateRead,
	}
	for _, call := range scopedCalls {
		t.Run(call.kind, func(t *testing.T) {
			writes := newFakeWrites()
			audit := &recordingAudit{}
			cap, ctx := writeCap(t)
			h := hostWith("p", writes.deps(audit, cap), approvedBound("p", call.kind))
			if err := call.run(ctx, h); err != nil {
				t.Fatal(err)
			}
			if len(audit.entries) != 1 {
				t.Fatalf("audit entries = %v, want one", audit.actions)
			}
			e := audit.entries[0]
			if e.Action != want[call.kind] || e.TargetType != "plugin" || e.TargetID != "p" ||
				e.LibraryID != "lib-a" || e.ProfileID != "prof-1" {
				t.Fatalf("audit entry = %+v", e)
			}
		})
	}
}

func TestScopedWritesRefuseTargetsOutsideTheGrant(t *testing.T) {
	writes := newFakeWrites()
	audit := &recordingAudit{}
	cap, ctx := writeCap(t)
	h := hostWith("p", writes.deps(audit, cap),
		approvedBound("p", PermWriteMetadata), approvedBound("p", PermWriteCollections),
		approvedBound("p", PermWriteArtwork), approvedBound("p", PermWriteWatchState),
		approvedBound("p", PermReadPlaybackState))

	checks := map[string]func() error{
		"metadata hidden media": func() error {
			_, err := h.WriteMetadata(ctx, []string{"m-1", "m-hidden"}, []MetadataField{{Name: "n", Type: "flag", Flag: true}})
			return err
		},
		"metadata unknown media": func() error {
			_, err := h.WriteMetadata(ctx, []string{"m-missing"}, []MetadataField{{Name: "n", Type: "flag"}})
			return err
		},
		"collection in hidden Library": func() error {
			_, err := h.CreateCollection(ctx, "lib-b", "Hidden")
			return err
		},
		"add to hidden collection": func() error {
			_, err := h.AddCollectionMedia(ctx, "c-hidden", []string{"m-1"})
			return err
		},
		"artwork on hidden media": func() error { return h.SetArtwork(ctx, "m-hidden", "poster", pngBytes) },
		"watch hidden media":      func() error { return h.SetWatched(ctx, "m-hidden", true) },
		"read hidden progress": func() error {
			_, err := h.ReadPlaybackState(ctx, "m-hidden")
			return err
		},
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, ErrTargetNotFound) {
			t.Errorf("%s: err = %v, want %v", name, err, ErrTargetNotFound)
		}
	}
	if writes.writeCount() != 0 || writes.reads != 0 || len(audit.entries) != 0 {
		t.Fatalf("out-of-scope call reached a seam or the ledger: writes=%d reads=%d audit=%v",
			writes.writeCount(), writes.reads, audit.actions)
	}
}

func TestProfileBoundKindsRefuseUnboundGrants(t *testing.T) {
	for _, kind := range []string{PermWriteWatchState, PermReadPlaybackState} {
		if !RequiresBoundProfile(kind) {
			t.Fatalf("%s must require a bound Profile", kind)
		}
		if err := validateGrant(Grant{PluginID: "p", Kind: kind, AllLibraries: true}); !errors.Is(err, ErrGrantScopeConflict) {
			t.Fatalf("validateGrant(%s, all Libraries) = %v, want %v", kind, err, ErrGrantScopeConflict)
		}
	}
	for _, kind := range []string{PermWriteMetadata, PermWriteCollections, PermWriteArtwork} {
		if RequiresBoundProfile(kind) {
			t.Fatalf("%s must not require a bound Profile", kind)
		}
	}

	writes := newFakeWrites()
	cap, ctx := writeCap(t)
	for name, grant := range map[string]Grant{
		"all Libraries": {PluginID: "p", Kind: PermWriteWatchState, Approved: true, AllLibraries: true},
		"unbound":       {PluginID: "p", Kind: PermWriteWatchState, Approved: true},
	} {
		h := hostWith("p", writes.deps(&recordingAudit{}, cap), grant)
		if err := h.SetWatched(ctx, "m-1", true); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("%s grant: err = %v, want permission denied", name, err)
		}
	}
	if len(writes.watched) != 0 {
		t.Fatal("watch state changed under an unbound grant")
	}
}

func TestScopedWritesRequireCausationToken(t *testing.T) {
	writes := newFakeWrites()
	cap, _ := writeCap(t)
	h := hostWith("p", writes.deps(&recordingAudit{}, cap), approvedBound("p", PermWriteArtwork))
	if err := h.SetArtwork(context.Background(), "m-1", "poster", pngBytes); !errors.Is(err, causation.ErrInvalidToken) {
		t.Fatalf("tokenless write: err = %v, want %v", err, causation.ErrInvalidToken)
	}
	if len(writes.artwork) != 0 {
		t.Fatal("artwork written without a causation token")
	}
}

func TestReadPlaybackStateReturnsBoundProfileProgress(t *testing.T) {
	writes := newFakeWrites()
	writes.progress["m-1"] = store.PlaybackProgress{PositionSeconds: 90, DurationSeconds: 600, PlayCount: 2}
	h := hostWith("p", writes.deps(&recordingAudit{}, nil), approvedBound("p", PermReadPlaybackState))
	state, err := h.ReadPlaybackState(context.Background(), "m-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Found || state.PositionSeconds != 90 || state.DurationSeconds != 600 || state.PlayCount != 2 {
		t.Fatalf("state = %+v", state)
	}
	state, err = h.ReadPlaybackState(context.Background(), "m-2")
	if err != nil || state.Found {
		t.Fatalf("no progress: state = %+v, err = %v", state, err)
	}
}

func TestWriteInputLimits(t *testing.T) {
	writes := newFakeWrites()
	cap, ctx := writeCap(t)
	h := hostWith("p", writes.deps(&recordingAudit{}, cap),
		approvedBound("p", PermWriteMetadata), approvedBound("p", PermWriteCollections), approvedBound("p", PermWriteArtwork))
	if _, err := h.WriteMetadata(ctx, []string{"m-1"}, []MetadataField{{Name: "d", Type: "date"}}); err == nil {
		t.Error("date field without a date was accepted")
	}
	if _, err := h.WriteMetadata(ctx, []string{"m-1"}, []MetadataField{{Name: "x", Type: "blob"}}); err == nil {
		t.Error("unknown field type was accepted")
	}
	if _, err := h.WriteMetadata(ctx, make([]string, maxWriteMediaIDs+1), []MetadataField{{Name: "x", Type: "text"}}); err == nil {
		t.Error("oversized media id list was accepted")
	}
	if _, err := h.CreateCollection(ctx, "lib-a", "   "); err == nil {
		t.Error("blank collection name was accepted")
	}
	if err := h.SetArtwork(ctx, "m-1", "poster", make([]byte, maxArtworkBytes+1)); err == nil {
		t.Error("oversized artwork was accepted")
	}
	if writes.writeCount() != 0 {
		t.Fatal("an invalid request reached a seam")
	}
}

// TestSampleWorkerCreatesCollectionAndSetsArtwork drives the wire protocol as a
// Kometa-style worker would: handshake, create a collection, fill it, set a
// poster. Each call is granted, scoped, admitted and audited.
func TestSampleWorkerCreatesCollectionAndSetsArtwork(t *testing.T) {
	const pluginID = "sample-collections"
	writes := newFakeWrites()
	audit := &recordingAudit{}
	cap, ctx := writeCap(t)
	token := causation.TokenFromContext(ctx)
	grants := fixedGrants{grants: map[string][]Grant{pluginID: {
		{PluginID: pluginID, Kind: PermWriteCollections, Approved: true, BoundProfileID: "prof-1"},
		{PluginID: pluginID, Kind: PermWriteArtwork, Approved: true, BoundProfileID: "prof-1"},
	}}}
	host := NewHost(pluginID, writes.deps(audit, cap), grants, nil)
	sup := &Supervisor{pluginID: pluginID, host: host, token: "sample-token"}

	worker, serverConn := net.Pipe()
	defer func() { _ = worker.Close() }()
	go sup.handleConn(context.Background(), serverConn)

	if err := writeFrame(worker, mustJSONBytes(handshakeFrame{Token: "sample-token"})); err != nil {
		t.Fatal(err)
	}
	ack := make([]byte, 1)
	if _, err := worker.Read(ack); err != nil || ack[0] != 0x01 {
		t.Fatalf("handshake ack = %v, %v", ack, err)
	}
	call := func(method string, w WriteRequest) hostResponse {
		t.Helper()
		if err := writeFrame(worker, mustJSONBytes(hostRequest{Method: method, Causation: token, Write: &w})); err != nil {
			t.Fatal(err)
		}
		raw, err := readFrame(worker)
		if err != nil {
			t.Fatal(err)
		}
		var resp hostResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	created := call(methodCreateCollection, WriteRequest{LibraryID: "lib-a", Name: "Weekend picks"})
	if created.Err != "" {
		t.Fatalf("create-collection: %s", created.Err)
	}
	var collection struct{ ID string }
	if err := json.Unmarshal(created.Data, &collection); err != nil || collection.ID == "" {
		t.Fatalf("create-collection data = %s, %v", created.Data, err)
	}
	if resp := call(methodAddCollectionMedia, WriteRequest{CollectionID: collection.ID, MediaIDs: []string{"m-1", "m-2"}}); resp.Err != "" {
		t.Fatalf("add-collection-media: %s", resp.Err)
	}
	if resp := call(methodSetArtwork, WriteRequest{MediaID: "m-1", Slot: "poster", Image: pngBytes}); resp.Err != "" {
		t.Fatalf("set-artwork: %s", resp.Err)
	}
	// Not granted: the same worker cannot change watch state.
	if resp := call(methodSetWatched, WriteRequest{MediaID: "m-1", Watched: true}); resp.Err == "" {
		t.Fatal("set-watched succeeded without a grant")
	}

	if got := writes.members[store.CollectionID(collection.ID)]; !slices.Equal(got, []store.MediaID{"m-1", "m-2"}) {
		t.Fatalf("collection members = %v", got)
	}
	if writes.artwork["m-1"] != "poster" || len(writes.watched) != 0 {
		t.Fatalf("artwork = %v, watched = %v", writes.artwork, writes.watched)
	}
	wantAudit := []string{store.ActivityPluginCollectionWrite, store.ActivityPluginCollectionWrite, store.ActivityPluginArtworkWrite}
	if !slices.Equal(audit.actions, wantAudit) {
		t.Fatalf("audit = %v, want %v", audit.actions, wantAudit)
	}
}

// outboxTail serves the rows a test appends after the writes that caused them.
type outboxTail struct {
	events []store.OutboxEvent
}

func (o *outboxTail) ReadEventOutboxFiltered(context.Context, int64, int, store.LibraryFilter) ([]store.OutboxEvent, error) {
	return o.events, nil
}

func (*outboxTail) CausationToken(store.OutboxEvent) causation.Token { return "" }

// ADR-0125: every outbox row a promoted seam appends retains the caller's
// token, so a Worker that reacts to its own write spends the same chain.
func TestScopedWritesBindDescendantEventsToTheCallersChain(t *testing.T) {
	writes := newFakeWrites()
	cap, ctx := writeCap(t)
	tail := &outboxTail{}
	deps := writes.deps(&recordingAudit{}, cap)
	deps.Events = tail
	h := hostWith("p", deps,
		approvedBound("p", PermWriteMetadata),
		approvedBound("p", PermWriteWatchState),
		approvedBound("p", PermEventSubscribe))
	if _, err := h.WriteMetadata(ctx, []string{"m-1"}, []MetadataField{{Name: "sortTitle", Type: "text", Text: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := h.SetWatched(ctx, "m-1", true); err != nil {
		t.Fatal(err)
	}
	if len(writes.stamped) != 2 {
		t.Fatalf("stamped = %v, want one time per outbox-emitting seam", writes.stamped)
	}
	tail.events = []store.OutboxEvent{
		{Seq: 1, Kind: store.MediaEventUpdated, AggregateType: store.OutboxAggregateMedia, AggregateID: "m-1", OccurredAt: writes.stamped[0].Truncate(time.Millisecond)},
		{Seq: 2, Kind: store.InvalidationKind, AggregateType: store.OutboxAggregateInvalidation, AggregateID: "prof-1", OccurredAt: writes.stamped[1].Truncate(time.Millisecond)},
		{Seq: 3, Kind: store.MediaEventUpdated, AggregateType: store.OutboxAggregateMedia, AggregateID: "m-2", OccurredAt: writes.stamped[0]},
	}
	events, err := h.SubscribeEvents(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := causation.TokenFromContext(ctx)
	if events[0].CausationToken != want || events[1].CausationToken != want {
		t.Fatal("a row the Worker's own write appended came back as a fresh chain root")
	}
	if events[2].CausationToken == want {
		t.Fatal("a row no Worker write appended inherited the Worker's chain")
	}

	// A Worker that writes again on every row it caused stops at the global
	// chain budget instead of looping.
	spent := 2
	for {
		events, err := h.SubscribeEvents(context.Background(), 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.WriteMetadata(causation.WithToken(context.Background(), events[0].CausationToken), []string{"m-1"}, []MetadataField{{Name: "sortTitle", Type: "text", Text: "x"}})
		if err != nil {
			if !errors.Is(err, causation.ErrGlobalBudget) {
				t.Fatalf("self-chain err = %v, want %v", err, causation.ErrGlobalBudget)
			}
			break
		}
		spent++
		if spent > causation.DefaultLimits().GlobalActions {
			t.Fatal("self-chain outran the global chain budget")
		}
	}
	if spent != causation.DefaultLimits().GlobalActions {
		t.Fatalf("self-chain spent %d actions, want %d", spent, causation.DefaultLimits().GlobalActions)
	}
}

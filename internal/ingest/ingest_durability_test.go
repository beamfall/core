package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func newTestEngine() *scrape.Engine {
	return scrape.New(scrape.WithInsecureLoopback())
}

// errSimulatedCrash aborts the ingest protocol at a chosen boundary with no
// cleanup, leaving exactly the on-disk and receipt state a process kill at
// that boundary would leave.
var errSimulatedCrash = errors.New("simulated crash")

// rootState classifies a Subscription root's contents into staging partials,
// staged payloads, and visible files.
type rootState struct {
	partials []string
	staged   []string
	visible  []string
}

func classifyRoot(t *testing.T, root string) rootState {
	t.Helper()
	var state rootState
	for _, name := range dirEntries(t, root) {
		switch {
		case strings.HasPrefix(name, stagePrefix) && strings.HasSuffix(name, partialSuffix):
			state.partials = append(state.partials, name)
		case strings.HasPrefix(name, stagePrefix) && strings.HasSuffix(name, stagedSuffix):
			state.staged = append(state.staged, name)
		default:
			state.visible = append(state.visible, name)
		}
	}
	return state
}

func queuedScanJobs(t *testing.T, st *store.Store) int {
	t.Helper()
	jobsList, err := st.ListJobs(context.Background(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, job := range jobsList {
		if job.Kind == scan.JobKind {
			count++
		}
	}
	return count
}

func entrySeen(t *testing.T, st *store.Store, id store.SubscriptionID, guid string) bool {
	t.Helper()
	seen, err := st.SubscriptionSeenGUIDs(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return seen[guid]
}

func TestPollPublishesWhenHardLinksAreUnsupported(t *testing.T) {
	fs := newFeedServer(t)
	st, sub, poller := healthFixture(t, fs.server.URL+"/feed.xml")
	ctx := t.Context()
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyEverything}); err != nil {
		t.Fatal(err)
	}
	fs.enclosures["one.mp3"] = []byte("first payload")
	fs.enclosures["two.mp3"] = []byte("second payload")
	first := fs.item("g1", "Track", "one.mp3")
	fs.setFeed(first)
	if err := os.WriteFile(filepath.Join(sub.Root, "Track.mp3"), []byte("existing file"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkCalls := 0
	poller.link = func(oldpath, newpath string) error {
		linkCalls++
		return &os.LinkError{Op: "link", Old: oldpath, New: newpath, Err: syscall.EOPNOTSUPP}
	}
	if count, err := poller.pollSubscription(ctx, sub); count != 1 || err != nil {
		t.Fatalf("first poll = %d, %v", count, err)
	}
	path := filepath.Join(sub.Root, "Track-2.mp3")
	entries, err := st.SubscriptionEntries(ctx, sub.ID)
	if err != nil || len(entries) != 1 || entries[0].DownloadPath != path {
		t.Fatalf("fallback publication did not record its diverted path: %v", err)
	}
	if data, err := readTestFile(path); err != nil || string(data) != "first payload" {
		t.Fatalf("fallback file = %q, %v", data, err)
	}
	fs.setFeed(first + fs.item("g2", "Second Track", "two.mp3"))
	advancePollClock(poller)
	if count, err := poller.pollSubscription(ctx, reloadSubscription(t, st, sub.ID)); count != 1 || err != nil {
		t.Fatalf("later poll = %d, %v", count, err)
	}
	if linkCalls != 2 || fs.hits("one.mp3") != 1 || fs.hits("two.mp3") != 1 || len(classifyRoot(t, sub.Root).staged) != 0 {
		t.Fatal("fallback did not publish each entry once and clear its stage")
	}
}

func TestScanBeforePublicationRecoveryKeepsEntryMedia(t *testing.T) {
	fs := newFeedServer(t)
	st := storetest.Open(t)
	ctx := t.Context()
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeRoot, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local", FeedURL: fs.server.URL + "/feed.xml", Root: relativeRoot,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	poller := NewPoller(st, newTestEngine())
	if err := st.SetSubscriptionDownloadPolicy(ctx, sub.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyEverything}); err != nil {
		t.Fatal(err)
	}
	fs.enclosures["one.mp3"] = []byte("complete payload")
	fs.setFeed(fs.item("g1", "First Track", "one.mp3"))
	before, err := st.MediaIDsForLibrary(ctx, sub.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	poller.fault = func(point string) error {
		if point == "payload-linked" {
			return errSimulatedCrash
		}
		return nil
	}
	if count, err := poller.pollSubscription(ctx, sub); count != 0 || !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("interrupted publication = %d, %v", count, err)
	}
	path := filepath.Join(root, "First Track.mp3")
	state := classifyRoot(t, root)
	if len(state.staged) != 1 || !slices.Equal(state.visible, []string{filepath.Base(path)}) {
		t.Fatalf("interrupted publication state = %+v", state)
	}
	entries, err := st.SubscriptionEntries(ctx, sub.ID)
	if err != nil || len(entries) != 1 || entries[0].DownloadPath != "" || entries[0].DownloadState != store.EntryDownloadQueued {
		t.Fatalf("publication transaction did not roll back: %v", err)
	}
	entryMediaID := entries[0].MediaID
	// Let the scan's file-stability check admit the published inode.
	settled := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, settled, settled); err != nil {
		t.Fatal(err)
	}
	scanned, err := scan.Scan(ctx, st, nil, nil, nil, nil, nil, scan.Request{LibraryID: sub.LibraryID, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if scanned.MediaImported != 1 {
		t.Fatalf("scan did not import the visible file: %+v", scanned)
	}
	poller.fault = nil
	if count, err := poller.pollSubscription(ctx, reloadSubscription(t, st, sub.ID)); count != 1 || err != nil {
		t.Fatalf("publication recovery = %d, %v", count, err)
	}
	entries, err = st.SubscriptionEntries(ctx, sub.ID)
	if err != nil || len(entries) != 1 || entries[0].MediaID != entryMediaID ||
		entries[0].DownloadPath != path || entries[0].DownloadState != store.EntryDownloadDownloaded || !entries[0].Downloaded {
		t.Fatalf("recovery did not preserve the entry's Media and mark it downloaded: %d entries, %v", len(entries), err)
	}
	after, err := st.MediaIDsForLibrary(ctx, sub.LibraryID)
	if err != nil || len(after) != len(before)+1 || !slices.Contains(after, entryMediaID) || fs.hits("one.mp3") != 1 {
		t.Fatalf("expected a single new Media and one enclosure fetch: Media %v, err %v", after, err)
	}
}

// TestIngestKillMatrixConvergesAfterRestart kills the ingest protocol at
// every boundary, asserts the exact durable state each crash leaves, then
// replays the poll (the restart) and asserts convergence: exactly one
// published payload, an exact-GUID receipt, no staging residue, and no
// redundant re-download once the payload was staged durably.
func TestIngestKillMatrixConvergesAfterRestart(t *testing.T) {
	cases := []struct {
		point string
		// state immediately after the crash
		wantPartials    int
		wantStaged      int
		wantVisible     int
		wantSeen        bool
		wantScanAtCrash int
		// enclosure fetch count after the replay: 2 when the crash landed
		// before the payload was staged durably (retry re-downloads), 1 once
		// the staged payload could be resumed instead.
		wantHits int
		// scan jobs queued after the replay; the payload-published crash
		// already queued its scan pre-crash, so the replay queues nothing.
		wantScanAfterReplay int
	}{
		{"enclosure-written", 1, 0, 0, false, 0, 2, 1},
		{"enclosure-synced", 1, 0, 0, false, 0, 2, 1},
		{"payload-staged", 0, 1, 0, false, 0, 1, 1},
		{"receipt-recorded", 0, 1, 0, true, 0, 1, 1},
		{"payload-published", 0, 0, 1, true, 1, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.point, func(t *testing.T) {
			st := storetest.Open(t)
			ctx := context.Background()
			fs := newFeedServer(t)
			fs.enclosures["one.mp3"] = []byte("durable-payload")
			fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))
			root := t.TempDir()
			sub, err := addDownloadAllSubscription(ctx, st, store.Subscription{
				LibraryID: "library-local",
				FeedURL:   fs.server.URL + "/feed.xml",
				Root:      root,
			}, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}

			poller := NewPoller(st, newTestEngine())
			poller.fault = func(point string) error {
				if point == tc.point {
					return errSimulatedCrash
				}
				return nil
			}
			runPoll(t, st, Handler(poller))

			state := classifyRoot(t, root)
			if len(state.partials) != tc.wantPartials || len(state.staged) != tc.wantStaged || len(state.visible) != tc.wantVisible {
				t.Fatalf("state after crash at %s = partials %v staged %v visible %v, want %d/%d/%d",
					tc.point, state.partials, state.staged, state.visible, tc.wantPartials, tc.wantStaged, tc.wantVisible)
			}
			if got := entrySeen(t, st, sub.ID, "guid-1"); got != tc.wantSeen {
				t.Fatalf("receipt after crash at %s = %v, want %v", tc.point, got, tc.wantSeen)
			}
			if got := queuedScanJobs(t, st); got != tc.wantScanAtCrash {
				t.Fatalf("scan jobs after crash at %s = %d, want %d", tc.point, got, tc.wantScanAtCrash)
			}
			// A durably staged payload already holds the complete body: the
			// receipt may only ever acknowledge bytes that survive a kill.
			if tc.wantStaged == 1 {
				data, err := readTestFile(filepath.Join(root, state.staged[0]))
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != "durable-payload" {
					t.Fatalf("staged payload = %q, want the complete body", data)
				}
			}

			// Restart: same feed, fault cleared, replay the poll.
			poller.fault = nil
			runPoll(t, st, Handler(poller))

			state = classifyRoot(t, root)
			if len(state.partials) != 0 || len(state.staged) != 0 {
				t.Fatalf("staging residue after replay: partials %v staged %v", state.partials, state.staged)
			}
			if len(state.visible) != 1 || state.visible[0] != "First Entry.mp3" {
				t.Fatalf("visible files after replay = %v, want exactly First Entry.mp3", state.visible)
			}
			data, err := readTestFile(filepath.Join(root, "First Entry.mp3"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "durable-payload" {
				t.Fatalf("published payload = %q", data)
			}
			if !entrySeen(t, st, sub.ID, "guid-1") {
				t.Fatal("receipt missing after replay")
			}
			if got := fs.hits("one.mp3"); got != tc.wantHits {
				t.Fatalf("enclosure fetches after replay = %d, want %d", got, tc.wantHits)
			}
			if got := queuedScanJobs(t, st); got != tc.wantScanAfterReplay {
				t.Fatalf("scan jobs after replay = %d, want %d", got, tc.wantScanAfterReplay)
			}
		})
	}
}

// TestReceiptsBindExactSourceGUIDs feeds entries whose GUIDs collide under
// any normalization — case folding, prefix truncation, unicode
// canonicalization — and asserts each keeps its own receipt and its own
// destination generation, with no entry fetched twice across polls.
func TestReceiptsBindExactSourceGUIDs(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	guids := []string{"guid-1", "GUID-1", "guid-10", "guid-\u00e9", "guid-e\u0301"}
	payloads := []string{"payload-a", "payload-b", "payload-c", "payload-d", "payload-e"}
	items := ""
	for i, guid := range guids {
		name := payloadName(i)
		fs.enclosures[name] = []byte(payloads[i])
		items += fs.item(guid, "Entry", name)
	}
	fs.setFeed(items)
	root := t.TempDir()
	sub, err := addDownloadAllSubscription(ctx, st, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	handler := Handler(NewPoller(st, newTestEngine()))
	runPoll(t, st, handler)

	// Every colliding-under-normalization GUID downloaded its own payload,
	// suffixed into its own destination generation in document order.
	wantFiles := []string{"Entry.mp3", "Entry-2.mp3", "Entry-3.mp3", "Entry-4.mp3", "Entry-5.mp3"}
	for i, name := range wantFiles {
		data, err := readTestFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("destination generation %d: %v (dir: %v)", i+1, err, dirEntries(t, root))
		}
		if string(data) != payloads[i] {
			t.Fatalf("%s = %q, want %q", name, data, payloads[i])
		}
	}
	seen, err := st.SubscriptionSeenGUIDs(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(guids) {
		t.Fatalf("receipts = %d, want %d distinct exact GUIDs (%v)", len(seen), len(guids), seen)
	}
	for _, guid := range guids {
		if !seen[guid] {
			t.Fatalf("receipt for exact GUID %q missing: %v", guid, seen)
		}
	}

	// A second poll of the identical feed re-fetches nothing: the exact
	// receipts, not any normalized form, gate re-downloads.
	runPoll(t, st, handler)
	for i := range guids {
		if got := fs.hits(payloadName(i)); got != 1 {
			t.Fatalf("enclosure %d fetched %d times, want 1", i, got)
		}
	}
	if got := len(dirEntries(t, root)); got != len(guids) {
		t.Fatalf("files after second poll = %d, want %d", got, len(guids))
	}
}

func payloadName(i int) string {
	return "payload-" + strconv.Itoa(i) + ".mp3"
}

// TestUnacknowledgedStagedPayloadLifecycle covers the one crash residue no
// receipt points at: a payload staged just before the kill whose entry the
// feed then drops. The next clean poll sweeps it, and the entry returning
// later is a fresh download with a fresh receipt.
func TestUnacknowledgedStagedPayloadLifecycle(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("first-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))
	root := t.TempDir()
	sub, err := addDownloadAllSubscription(ctx, st, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	poller := NewPoller(st, newTestEngine())
	poller.fault = func(point string) error {
		if point == "payload-staged" {
			return errSimulatedCrash
		}
		return nil
	}
	runPoll(t, st, Handler(poller))
	if state := classifyRoot(t, root); len(state.staged) != 1 {
		t.Fatalf("state after crash = %+v, want one staged payload", state)
	}

	// The feed drops the entry before the restart: the staged payload is
	// unacknowledged residue, and the clean poll sweeps it.
	poller.fault = nil
	fs.setFeed("")
	runPoll(t, st, Handler(poller))
	if state := classifyRoot(t, root); len(state.partials)+len(state.staged)+len(state.visible) != 0 {
		t.Fatalf("state after sweep = %+v, want an empty root", state)
	}
	if entrySeen(t, st, sub.ID, "guid-1") {
		t.Fatal("swept payload must not leave a receipt")
	}

	// The entry returns: a fresh download lands and is acknowledged.
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))
	advancePollClock(poller)
	runPoll(t, st, Handler(poller))
	data, err := readTestFile(filepath.Join(root, "First Entry.mp3"))
	if err != nil {
		t.Fatalf("returned entry: %v (dir: %v)", err, dirEntries(t, root))
	}
	if string(data) != "first-payload" {
		t.Fatalf("returned entry payload = %q", data)
	}
	if !entrySeen(t, st, sub.ID, "guid-1") {
		t.Fatal("receipt missing for returned entry")
	}
	if got := fs.hits("one.mp3"); got != 2 {
		t.Fatalf("enclosure fetches = %d, want 2 (staged residue was swept)", got)
	}
}

// TestSharedRootSweepPreservesOtherSubscriptionReceipt proves a clean poll
// only sweeps artifacts owned by that Subscription. An acknowledged staged
// payload remains available until its own Subscription resumes publication.
func TestSharedRootSweepPreservesOtherSubscriptionReceipt(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	root := t.TempDir()

	feedA := newFeedServer(t)
	feedA.setFeed("")
	feedB := newFeedServer(t)
	feedB.enclosures["owned.mp3"] = []byte("subscription-b-payload")
	feedB.setFeed(feedB.item("guid-b", "Owned by B", "owned.mp3"))

	subscriptionA, err := addDownloadAllSubscription(ctx, st, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   feedA.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	subscriptionB, err := addDownloadAllSubscription(ctx, st, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   feedB.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	poller := NewPoller(st, newTestEngine())
	poller.fault = func(point string) error {
		if point == "receipt-recorded" {
			return errSimulatedCrash
		}
		return nil
	}
	if _, err := poller.pollSubscription(ctx, subscriptionB); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("subscription B interrupted poll error = %v, want simulated crash", err)
	}
	if !entrySeen(t, st, subscriptionB.ID, "guid-b") {
		t.Fatal("subscription B receipt missing after interruption")
	}
	state := classifyRoot(t, root)
	if len(state.staged) != 1 {
		t.Fatalf("state after subscription B interruption = %+v, want one staged payload", state)
	}
	stagedName := state.staged[0]

	poller.fault = nil
	if _, err := poller.pollSubscription(ctx, subscriptionA); err != nil {
		t.Fatalf("subscription A clean poll: %v", err)
	}
	state = classifyRoot(t, root)
	if len(state.staged) != 1 || state.staged[0] != stagedName {
		t.Fatalf("state after subscription A sweep = %+v, want B artifact %q retained", state, stagedName)
	}

	if _, err := poller.pollSubscription(ctx, subscriptionB); err != nil {
		t.Fatalf("subscription B resume: %v", err)
	}
	state = classifyRoot(t, root)
	if len(state.staged) != 0 || len(state.visible) != 1 || state.visible[0] != "Owned by B.mp3" {
		t.Fatalf("state after subscription B resume = %+v", state)
	}
	published, err := readTestFile(filepath.Join(root, "Owned by B.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if string(published) != "subscription-b-payload" {
		t.Fatalf("published payload = %q", published)
	}
}

func TestPublishCollisionKeepsUnrelatedFile(t *testing.T) {
	for _, tc := range []struct{ name, crashAt string }{
		{"normal", ""},
		{"recovery after receipt", "receipt-recorded"},
		{"recovery after link", "payload-linked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testPublishCollision(t, tc.crashAt)
		})
	}
}

func testPublishCollision(t *testing.T, crashAt string) {
	t.Helper()
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("resumed-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))
	root := t.TempDir()
	keep := 1
	sub, err := addDownloadAllSubscription(ctx, st, store.Subscription{
		LibraryID:     "library-local",
		FeedURL:       fs.server.URL + "/feed.xml",
		Root:          root,
		RetentionKind: store.RetentionKeepLastN,
		RetentionN:    &keep,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	original := filepath.Join(root, "First Entry.mp3")
	diverted := filepath.Join(root, "First Entry-2.mp3")
	poller := NewPoller(st, newTestEngine())
	reachedReceipt := false
	poller.fault = func(point string) error {
		if point == "receipt-recorded" {
			if !entrySeen(t, st, sub.ID, "guid-1") || len(classifyRoot(t, root).staged) != 1 {
				t.Fatal("collision requires an acknowledged staged payload")
			}
			entries, err := st.SubscriptionEntries(ctx, sub.ID)
			if err != nil || len(entries) != 1 || entries[0].DownloadPath != "" {
				t.Error("catalogue exposed a path before publication")
			}
			reachedReceipt = true
			if err := os.WriteFile(original, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if point == crashAt {
			return errSimulatedCrash
		}
		return nil
	}
	count, err := poller.pollSubscription(ctx, sub)
	if !reachedReceipt {
		t.Fatalf("poll did not reach receipt: %v", err)
	}
	if crashAt != "" {
		if count != 0 || !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("interrupted poll = %d, %v", count, err)
		}
		poller = NewPoller(st, newTestEngine())
		count, err = poller.pollSubscription(ctx, sub)
	}
	if count != 1 || err != nil || fs.hits("one.mp3") != 1 {
		t.Fatalf("publication = %d, %v, enclosure requests = %d", count, err, fs.hits("one.mp3"))
	}
	if files := dirEntries(t, root); len(files) != 2 {
		t.Fatalf("publication left extra files: %v", files)
	}
	poller.fault = nil

	entries, err := st.SubscriptionEntries(ctx, sub.ID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("catalogue entries = %d, %v", len(entries), err)
	}
	entry := entries[0]
	if entry.DownloadPath != diverted {
		t.Errorf("download path = %q, want %q", entry.DownloadPath, diverted)
	}
	importFile := func(path, fingerprint string) store.MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: sub.LibraryID, Path: path, Title: filepath.Base(path),
			Container: "mp3", Codec: "mp3", ContentFingerprint: fingerprint, Class: store.MediaClassAudio,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	foreignID := importFile(original, "foreign-fingerprint")
	if foreignID == entry.MediaID {
		t.Error("unrelated file bound to the entry's Media")
	}
	downloadID := importFile(diverted, "download-fingerprint")
	if downloadID != entry.MediaID {
		t.Errorf("diverted file bound to %q, want %q", downloadID, entry.MediaID)
	}
	if data, err := readTestFile(diverted); err != nil || string(data) != "resumed-payload" {
		t.Fatalf("diverted payload = %q, %v", data, err)
	}

	fs.enclosures["two.mp3"] = []byte("newer-payload")
	fs.setFeed(fs.item("guid-2", "Newer Entry", "two.mp3"))
	advancePollClock(poller)
	if count, err := poller.pollSubscription(ctx, sub); count != 1 || err != nil {
		t.Fatalf("newer download = %d, %v", count, err)
	}
	importFile(filepath.Join(root, "Newer Entry.mp3"), "newer-fingerprint")
	if trashed, skipped, err := st.ApplySubscriptionRetentionPolicies(ctx); trashed != 1 || skipped || err != nil {
		t.Fatalf("retention = %d, %v, %v, want one item trashed", trashed, skipped, err)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{sub.LibraryID}, Revealed: true}
	if _, err := st.Media(ctx, foreignID, filter); err != nil {
		t.Errorf("unrelated Media unavailable after retention: %v", err)
	}
	if _, err := st.Media(ctx, downloadID, filter); !errors.Is(err, store.ValidationError("media not found")) {
		t.Errorf("diverted download still available after retention: %v", err)
	}
	foreign, err := readTestFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(foreign) != "foreign" {
		t.Errorf("unrelated file content after retention = %q", foreign)
	}
}

// TestConcurrentPollsAreRaceClean polls several Subscriptions through one
// Poller concurrently; run under -race it proves the staged protocol keeps
// the store, engine, and filesystem paths race-free across pollers.
func TestConcurrentPollsAreRaceClean(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("shared-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))

	const pollers = 4
	subscriptions := make([]store.Subscription, pollers)
	for i := range subscriptions {
		added, err := addDownloadAllSubscription(ctx, st, store.Subscription{
			LibraryID: "library-local",
			// Distinct query strings keep the (library, URL) pairs unique
			// while every fetch resolves to the same served feed.
			FeedURL: fs.server.URL + "/feed.xml?subscription=" + strconv.Itoa(i),
			Root:    t.TempDir(),
		}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		subscriptions[i] = added
	}

	poller := NewPoller(st, newTestEngine())
	var group sync.WaitGroup
	errs := make([]error, pollers)
	for i := range subscriptions {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, errs[i] = poller.pollSubscription(ctx, subscriptions[i])
		}(i)
	}
	group.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent poll %d: %v", i, err)
		}
	}
	for i, subscription := range subscriptions {
		data, err := readTestFile(filepath.Join(subscription.Root, "First Entry.mp3"))
		if err != nil {
			t.Fatalf("subscription %d download: %v", i, err)
		}
		if string(data) != "shared-payload" {
			t.Fatalf("subscription %d payload = %q", i, data)
		}
	}
}

// TestStageArtifactNames pins the round-trip and the malformed-name guard
// the sweep relies on: only names this package generated are ever removed.
func TestStageArtifactNames(t *testing.T) {
	subscriptionID := store.SubscriptionID("subscription-test")
	key := stageKey(subscriptionID, "guid-1")
	name := stageArtifactName(key, "First Entry-2.mp3", stagedSuffix)
	parsedKey, base, ok := parseStageArtifact(name, stagedSuffix)
	if !ok || parsedKey != key || base != "First Entry-2.mp3" {
		t.Fatalf("round-trip = %q/%q/%v", parsedKey, base, ok)
	}
	malformed := []string{
		"First Entry.mp3",
		".ingest-" + key + ".staged",        // no separator
		".ingest---payload.staged",          // empty key
		".ingest-" + key + "--" + ".staged", // empty base
		".ingest-" + key + "--x.partial",    // wrong suffix for this query
	}
	for _, name := range malformed {
		if _, _, ok := parseStageArtifact(name, stagedSuffix); ok {
			t.Fatalf("parseStageArtifact accepted %q", name)
		}
	}
	if stageKey(subscriptionID, "guid-1") == stageKey(subscriptionID, "GUID-1") {
		t.Fatal("stage keys must bind exact GUID bytes, never a normalized form")
	}
	owner, guidDigest, ok := strings.Cut(key, ".")
	if !ok {
		t.Fatalf("stage key %q has no owner separator", key)
	}
	if len(owner) != 32 {
		t.Fatalf("owner stage-key width = %d hex characters, want 32", len(owner))
	}
	if len(guidDigest) != sha256.Size*2 {
		t.Fatalf("GUID stage-key digest width = %d hex characters, want %d", len(guidDigest), sha256.Size*2)
	}
	if stageKey(subscriptionID, "guid-1") == stageKey("subscription-other", "guid-1") {
		t.Fatal("stage keys must bind the Subscription owner")
	}
	if !stageKeyOwnedBy(key, subscriptionID) {
		t.Fatal("generated stage key must match its Subscription owner")
	}
	for _, malformedKey := range []string{owner, owner + ".short", owner + "." + strings.Repeat("G", sha256.Size*2), key + ".extra"} {
		if stageKeyOwnedBy(malformedKey, subscriptionID) {
			t.Fatalf("stageKeyOwnedBy accepted malformed key %q", malformedKey)
		}
	}
}

func TestListStageArtifactsRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.mp3")
	if err := os.WriteFile(target, []byte("outside-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := stageKey("subscription-test", "guid-1")
	link := filepath.Join(root, stageArtifactName(key, "Visible.mp3", stagedSuffix))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	names, err := listStageArtifacts(root, stagedSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("staging artifacts = %v, want symlink refused", names)
	}
	staged, err := findStagedPayload(root, key)
	if err != nil {
		t.Fatal(err)
	}
	if staged != "" {
		t.Fatalf("staged payload = %q, want symlink refused", staged)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("refused symlink was mutated: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "Visible.mp3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("visible path exists after symlink refusal: %v", err)
	}
}

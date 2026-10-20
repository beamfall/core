package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
			sub, err := st.AddSubscription(ctx, store.Subscription{
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
	sub, err := st.AddSubscription(ctx, store.Subscription{
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
	sub, err := st.AddSubscription(ctx, store.Subscription{
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

	subscriptionA, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   feedA.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	subscriptionB, err := st.AddSubscription(ctx, store.Subscription{
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

// TestResumeDivertsInsteadOfClobbering crashes between acknowledgement and
// publication, lets a foreign file claim the acknowledged destination, and
// asserts the resumed publication diverts to the next generation instead of
// overwriting.
func TestResumeDivertsInsteadOfClobbering(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	fs := newFeedServer(t)
	fs.enclosures["one.mp3"] = []byte("resumed-payload")
	fs.setFeed(fs.item("guid-1", "First Entry", "one.mp3"))
	root := t.TempDir()
	if _, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   fs.server.URL + "/feed.xml",
		Root:      root,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	poller := NewPoller(st, newTestEngine())
	poller.fault = func(point string) error {
		if point == "receipt-recorded" {
			return errSimulatedCrash
		}
		return nil
	}
	runPoll(t, st, Handler(poller))

	if err := os.WriteFile(filepath.Join(root, "First Entry.mp3"), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	poller.fault = nil
	runPoll(t, st, Handler(poller))

	foreign, err := readTestFile(filepath.Join(root, "First Entry.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if string(foreign) != "foreign" {
		t.Fatalf("foreign file clobbered: %q", foreign)
	}
	diverted, err := readTestFile(filepath.Join(root, "First Entry-2.mp3"))
	if err != nil {
		t.Fatalf("diverted publication: %v (dir: %v)", err, dirEntries(t, root))
	}
	if string(diverted) != "resumed-payload" {
		t.Fatalf("diverted payload = %q", diverted)
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
		added, err := st.AddSubscription(ctx, store.Subscription{
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
	if len(owner) != stageOwnerBytes*2 {
		t.Fatalf("owner stage-key width = %d hex characters, want %d", len(owner), stageOwnerBytes*2)
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

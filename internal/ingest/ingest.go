// Package ingest polls Library-level feed Subscriptions and downloads new
// enclosures into the Library's folder. It is deliberately
// thin: a durable job fetches each Subscription's feed (conditional GET
// through the hardened scrape engine — ADR-0012 §9), detects new entries by
// GUID, streams enclosures to disk, then enqueues the standard Library scan
// — downloaded files enter through the one import pipeline, no
// parallel ingest machinery.
//
// Subscription feed URLs can embed private-feed credentials (invariant 5):
// nothing in this package logs a feed or enclosure URL, and errors from the
// scrape engine are already URL-sanitized.
//
// Downloads are crash-durable. Each enclosure lands through a staged
// protocol — write to a GUID-keyed partial, file sync, atomic rename to a
// staged name, directory sync — before the seen-entry receipt commits, and
// the receipt binds the exact source GUID bytes (never normalized) to the
// destination the payload publishes under. Every crash point is resumed or
// compensated idempotently on the next poll: incomplete partials are
// deleted and re-downloaded, acknowledged staged payloads are published,
// unacknowledged staged payloads are reused when the feed still lists their
// entry and swept after the next fully clean poll.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// PollJobKind is the durable job that polls every Subscription.
const PollJobKind store.JobKind = "subscription-poll"

// DefaultPollInterval is the cadence the composition root re-arms the poll
// job at (ADR-0030 §7: hourly default). The ADR-0032 Scheduler owns
// user-editable cadence later; this constant is the seed value.
const DefaultPollInterval = time.Hour

// downloadBudgetBytes caps one enclosure download. Feeds advertise lengths
// but lie; 2 GiB comfortably covers any real enclosure while bounding disk
// abuse from a hostile feed.
const downloadBudgetBytes int64 = 2 << 30

// Poller fetches Subscription feeds and downloads new enclosures.
type Poller struct {
	store  *store.Store
	engine *scrape.Engine
	logger *slog.Logger
	// fault, when non-nil, is a test-only crash seam: returning an error
	// from a named protocol boundary abandons the entry mid-protocol with no
	// cleanup — the same on-disk and receipt state a process kill at that
	// boundary leaves behind.
	fault func(point string) error
}

// NewPoller creates a Poller over the shared hardened scrape engine.
func NewPoller(st *store.Store, engine *scrape.Engine) *Poller {
	return &Poller{store: st, engine: engine, logger: slog.Default()}
}

// SetLogger overrides the default logger; nil restores slog.Default().
func (p *Poller) SetLogger(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	p.logger = logger
}

type pollPayload struct {
	Version int `json:"version"`
}

type pollCheckpoint struct {
	Version    int    `json:"version"`
	LastID     string `json:"last_id,omitempty"`
	Downloaded int    `json:"downloaded"`
	Failed     int    `json:"failed"`
}

// NewPollJob builds the deduped background job that polls every Subscription.
func NewPollJob(createdAt time.Time) (store.Job, error) {
	payload, err := json.Marshal(pollPayload{Version: 1})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      PollJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(PollJobKind),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// Handler returns the poll job handler: one Subscription per claim,
// checkpointed by Subscription ID so a preempted poll resumes where it
// stopped. Per-Subscription failures are counted, never fatal — one dead
// feed must not starve the rest.
func Handler(p *Poller) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var checkpoint pollCheckpoint
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
		}
		subscriptions, err := p.store.AllSubscriptions(ctx)
		if err != nil {
			return jobs.Result{}, err
		}
		remaining := false
		for _, subscription := range subscriptions {
			if checkpoint.LastID != "" && string(subscription.ID) <= checkpoint.LastID {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			downloaded, err := p.pollSubscription(ctx, subscription)
			checkpoint.Downloaded += downloaded
			if err != nil {
				checkpoint.Failed++
				// Never log the error text here: transport errors can embed
				// the feed URL (a secret). The Subscription ID is enough to
				// investigate from the Console.
				p.logger.WarnContext(ctx, "subscription poll failed", "subscription", string(subscription.ID))
				p.logger.DebugContext(ctx, "subscription poll failure detail", "subscription", string(subscription.ID), "error", err.Error())
			}
			checkpoint.LastID = string(subscription.ID)
			remaining = true
			break // one Subscription per claim keeps runs short and preemptible
		}
		checkpoint.Version = 1
		data, err := json.Marshal(checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		progress := store.JobProgress{
			Current: int64(checkpoint.Downloaded),
			Unit:    "downloads",
			Message: "Polling subscriptions",
		}
		if err := p.store.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{}, err
		}
		if remaining || ctx.Err() != nil {
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}

// pollSubscription fetches one feed, downloads unseen entries' enclosures
// into the Subscription's root, and enqueues the standard Library scan when
// anything new arrived. Returns how many enclosures were published,
// resumed crash residue included.
func (p *Poller) pollSubscription(ctx context.Context, subscription store.Subscription) (int, error) {
	now := time.Now().UTC()
	// Resolve the private-feed Authorization header from the secure store via
	// the stored ref (invariant #5): the literal credential never lives in the
	// catalog DB. An anonymous feed has an empty ref and resolves to "".
	authHeader, err := p.engine.Credential(subscription.AuthRef)
	if err != nil {
		return 0, err
	}
	seen, err := p.store.SubscriptionSeenGUIDs(ctx, subscription.ID)
	if err != nil {
		return 0, err
	}
	// Resume before the conditional fetch: a payload staged and acknowledged
	// by a crashed run must reach its destination even when the feed now
	// answers 304 and the entry loop never runs.
	published, err := p.resumeStagedPayloads(ctx, subscription, seen, now)
	if err != nil {
		return 0, err
	}
	result, err := p.engine.FetchConditionalAuth(ctx, subscription.FeedURL, subscription.ETag, subscription.LastModified, authHeader)
	if err != nil {
		return published, err
	}
	if result.NotModified {
		return published, p.store.MarkSubscriptionPolled(ctx, subscription.ID, result.ETag, result.LastModified, now)
	}
	entries, err := scrape.ParseFeedEntries(result.Data)
	if err != nil {
		return published, err
	}
	downloaded := 0
	var firstErr error
	for _, entry := range entries {
		if ctx.Err() != nil {
			return published + downloaded, ctx.Err()
		}
		if entry.EnclosureURL == "" || seen[entry.GUID] {
			continue
		}
		if err := p.ingestEntry(ctx, subscription, entry, authHeader, now); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// Mirror the persisted receipt in the in-memory set so entries
		// sharing an exact GUID do not re-download in this poll.
		seen[entry.GUID] = true
		downloaded++
	}
	// A partial poll still counts as polled: seen GUIDs gate re-downloads,
	// but only a fully clean poll may store the new validators — otherwise a
	// 304 on the next poll would skip the entries that failed this time.
	if firstErr != nil {
		if err := p.store.MarkSubscriptionPolled(ctx, subscription.ID, "", "", now); err != nil {
			return published + downloaded, err
		}
		return published + downloaded, firstErr
	}
	// Only a fully clean poll sweeps: any owned staging artifact still
	// present belongs to an entry the feed no longer lists or to a
	// superseded attempt, and would otherwise accumulate forever.
	if err := sweepStaleArtifacts(subscription.Root, subscription.ID); err != nil {
		return published + downloaded, err
	}
	return published + downloaded, p.store.MarkSubscriptionPolled(ctx, subscription.ID, result.ETag, result.LastModified, now)
}

// ingestEntry lands one feed entry crash-durably. The payload stages first
// (write → file sync → atomic rename → directory sync), then the receipt
// commits, binding the exact source GUID bytes — never normalized — to the
// chosen destination, and only then does the staged payload publish under
// its visible name. Every boundary in between either resumes from the
// staged payload or retries the download on the next poll; bytes that could
// still be lost are never acknowledged.
func (p *Poller) ingestEntry(ctx context.Context, subscription store.Subscription, entry scrape.FeedEntry, authHeader string, now time.Time) error {
	if err := os.MkdirAll(subscription.Root, 0o755); err != nil {
		return err
	}
	key := stageKey(subscription.ID, entry.GUID)
	staged, err := findStagedPayload(subscription.Root, key)
	if err != nil {
		return err
	}
	if staged == "" {
		staged, err = p.downloadToStage(ctx, subscription.Root, key, entry, authHeader)
		if err != nil {
			return err
		}
	}
	_, targetBase, ok := parseStageArtifact(filepath.Base(staged), stagedSuffix)
	if !ok {
		return errors.New("ingest: malformed staged payload name")
	}
	target := filepath.Join(subscription.Root, targetBase)
	// The receipt is the durable acknowledgement: it commits only after the
	// staged payload would survive a crash.
	if err := p.store.MarkSubscriptionEntrySeen(ctx, subscription.ID, entry.GUID, target, now); err != nil {
		return err
	}
	if err := p.faultAt("receipt-recorded"); err != nil {
		return err
	}
	// Enqueue the deduped scan before publication: if the process dies
	// between here and the visible rename, the queued scan plus the staged
	// resume still import the payload. The post-publication enqueue below
	// covers a scan worker that claimed this job before the visible name
	// existed; while the first job is still queued the two dedupe to one.
	if err := p.enqueueScan(ctx, subscription, now); err != nil {
		return err
	}
	if _, err := p.publishStagedPayload(subscription.Root, staged); err != nil {
		return err
	}
	return p.enqueueScan(ctx, subscription, now)
}

// downloadToStage streams the enclosure into a GUID-keyed partial file and
// promotes it to a staged payload: write → file sync → atomic rename →
// directory sync. Only the staged form is ever treated as complete; a
// leftover partial from a crashed or failed attempt is deleted here and
// re-downloaded.
func (p *Poller) downloadToStage(ctx context.Context, root, key string, entry scrape.FeedEntry, authHeader string) (string, error) {
	if err := removeStageArtifacts(root, key, partialSuffix); err != nil {
		return "", err
	}
	target := availablePath(root, entryFilename(entry))
	base := filepath.Base(target)
	partial := filepath.Join(root, stageArtifactName(key, base, partialSuffix))
	// The partial path is confined by construction: root is the
	// Subscription's configured folder, the key contains only hex and a dot,
	// and the base comes from sanitizeFilename/enclosureExtension's
	// conservative charset with no path separators.
	file, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // #nosec G304 G302 -- path components are a hex-and-dot key + sanitized base joined under the Subscription root; 0644 because the payload becomes an ordinary Library media file other processes read
	if err != nil {
		return "", err
	}
	// The three abort paths below discard the close error deliberately: the
	// partial is abandoned before the durable point, and the next attempt
	// deletes and re-downloads it, so durability never depends on this close.
	if _, err := p.engine.DownloadResourceAuth(ctx, entry.EnclosureURL, file, downloadBudgetBytes, authHeader); err != nil {
		_ = file.Close() // #nosec G104 -- abandoning a pre-durable partial; next attempt deletes it
		return "", err
	}
	if err := p.faultAt("enclosure-written"); err != nil {
		_ = file.Close() // #nosec G104 -- abandoning a pre-durable partial; next attempt deletes it
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close() // #nosec G104 -- abandoning a pre-durable partial; next attempt deletes it
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := p.faultAt("enclosure-synced"); err != nil {
		return "", err
	}
	staged := filepath.Join(root, stageArtifactName(key, base, stagedSuffix))
	if err := os.Rename(partial, staged); err != nil {
		return "", err
	}
	if err := syncDir(root); err != nil {
		return "", err
	}
	if err := p.faultAt("payload-staged"); err != nil {
		return "", err
	}
	return staged, nil
}

// publishStagedPayload atomically renames a staged payload to its visible
// destination and syncs the directory. The destination encoded at stage
// time is preferred; if something else claimed that name since, the payload
// diverts to the next free name rather than clobbering.
func (p *Poller) publishStagedPayload(root, staged string) (string, error) {
	_, targetBase, ok := parseStageArtifact(filepath.Base(staged), stagedSuffix)
	if !ok {
		return "", errors.New("ingest: malformed staged payload name")
	}
	target := availablePath(root, targetBase)
	if err := os.Rename(staged, target); err != nil {
		return "", err
	}
	if err := syncDir(root); err != nil {
		return "", err
	}
	if err := p.faultAt("payload-published"); err != nil {
		return "", err
	}
	return target, nil
}

// resumeStagedPayloads publishes staged payloads whose receipt is already
// durable — a crash landed between acknowledgement and publication. A
// staged payload with no receipt stays put: its entry is still unseen, so
// the entry loop reuses it while the feed lists it, and the post-poll sweep
// clears it once the feed has dropped it.
func (p *Poller) resumeStagedPayloads(ctx context.Context, subscription store.Subscription, seen map[string]bool, now time.Time) (int, error) {
	staged, err := listStageArtifacts(subscription.Root, stagedSuffix)
	if err != nil {
		return 0, err
	}
	if len(staged) == 0 {
		return 0, nil
	}
	seenKeys := make(map[string]bool, len(seen))
	for guid := range seen {
		seenKeys[stageKey(subscription.ID, guid)] = true
	}
	published := 0
	for _, name := range staged {
		key, _, ok := parseStageArtifact(name, stagedSuffix)
		if !ok || !seenKeys[key] {
			continue
		}
		if _, err := p.publishStagedPayload(subscription.Root, filepath.Join(subscription.Root, name)); err != nil {
			return published, err
		}
		published++
	}
	if published == 0 {
		return 0, nil
	}
	return published, p.enqueueScan(ctx, subscription, now)
}

// faultAt is the crash seam described on Poller.fault; production leaves
// fault nil and every boundary is a no-op.
func (p *Poller) faultAt(point string) error {
	if p.fault == nil {
		return nil
	}
	return p.fault(point)
}

// enqueueScan hands the new files to the standard pipeline: the deduped
// Library scan over the Subscription root imports them like any other file.
func (p *Poller) enqueueScan(ctx context.Context, subscription store.Subscription, now time.Time) error {
	job, err := scan.NewJob(scan.JobRequest{
		LibraryID: subscription.LibraryID,
		Root:      subscription.Root,
		CreatedAt: now,
	})
	if err != nil {
		return err
	}
	_, err = p.store.EnqueueJob(ctx, job)
	return err
}

// entryFilename derives a safe on-disk name from the entry title (sanitized)
// plus an extension from the declared MIME type or the enclosure URL path.
func entryFilename(entry scrape.FeedEntry) string {
	name := sanitizeFilename(entry.Title)
	if name == "" {
		sum := sha256.Sum256([]byte(entry.GUID))
		name = "entry-" + hex.EncodeToString(sum[:6])
	}
	return name + "." + enclosureExtension(entry)
}

// mimeExtensions maps the common enclosure MIME types to file extensions.
var mimeExtensions = map[string]string{
	"audio/mpeg":  "mp3",
	"audio/mp3":   "mp3",
	"audio/mp4":   "m4a",
	"audio/x-m4a": "m4a",
	"audio/aac":   "aac",
	"audio/ogg":   "ogg",
	"audio/opus":  "opus",
	"audio/flac":  "flac",
	"audio/wav":   "wav",
	"video/mp4":   "mp4",
	"video/x-m4v": "m4v",
	"video/webm":  "webm",
	"video/mpeg":  "mpg",
}

func enclosureExtension(entry scrape.FeedEntry) string {
	mimeType, _, _ := strings.Cut(entry.EnclosureType, ";")
	if ext, ok := mimeExtensions[strings.ToLower(strings.TrimSpace(mimeType))]; ok {
		return ext
	}
	ext := strings.TrimPrefix(filepath.Ext(urlPathBase(entry.EnclosureURL)), ".")
	if ext != "" && len(ext) <= 5 && isAlphanumeric(ext) {
		return strings.ToLower(ext)
	}
	return "bin"
}

// urlPathBase returns the last path segment of a URL, ignoring the query
// (where private-feed tokens live — never part of a filename).
func urlPathBase(rawURL string) string {
	withoutQuery, _, _ := strings.Cut(rawURL, "?")
	withoutQuery, _, _ = strings.Cut(withoutQuery, "#")
	return filepath.Base(withoutQuery)
}

func isAlphanumeric(text string) bool {
	for _, r := range text {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// sanitizeFilename keeps a conservative character set so a feed-supplied
// title can never traverse paths or smuggle control characters.
func sanitizeFilename(title string) string {
	const maxLen = 120
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ', r == '-', r == '_', r == '.':
			if !lastDash {
				b.WriteRune(r)
				lastDash = r != ' '
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= maxLen {
			break
		}
	}
	return strings.Trim(b.String(), "-. ")
}

// availablePath returns dir/name, suffixing "-2", "-3", … before the
// extension when the name is taken — distinct entries with the same title
// must not clobber each other.
func availablePath(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidate := filepath.Join(dir, name)
	for suffix := 2; ; suffix++ {
		_, targetErr := os.Lstat(candidate)
		_, partialErr := os.Lstat(candidate + ".partial")
		if errors.Is(targetErr, os.ErrNotExist) && errors.Is(partialErr, os.ErrNotExist) {
			return candidate
		}
		candidate = filepath.Join(dir, stem+"-"+strconv.Itoa(suffix)+ext)
		if suffix > 10000 {
			return candidate // pathological; let the open fail loudly
		}
	}
}

// Staging artifacts live beside their destination as dotfiles whose
// suffixes are never media extensions, so the scan pipeline ignores them.
// The name encodes the Subscription owner, GUID key, and destination base
// name — the filesystem itself is the durable owner + GUID → destination
// binding a resumed poll reads back after a crash.
const (
	stagePrefix     = ".ingest-"
	stageSeparator  = "--"
	partialSuffix   = ".partial"
	stagedSuffix    = ".staged"
	stageOwnerBytes = 16
)

// stageKey derives a Subscription-owned staging namespace. The owner tag is
// 128 bits and the GUID digest uses all 256 bits; the exact GUID bytes are
// hashed without normalization.
func stageKey(subscriptionID store.SubscriptionID, guid string) string {
	ownerSum := sha256.Sum256([]byte(subscriptionID))
	sum := sha256.Sum256([]byte(guid))
	return hex.EncodeToString(ownerSum[:stageOwnerBytes]) + "." + hex.EncodeToString(sum[:])
}

func stageKeyOwnedBy(key string, subscriptionID store.SubscriptionID) bool {
	owner, guidDigest, ok := strings.Cut(key, ".")
	if !ok {
		return false
	}
	ownerSum := sha256.Sum256([]byte(subscriptionID))
	if owner != hex.EncodeToString(ownerSum[:stageOwnerBytes]) {
		return false
	}
	decodedGUID, err := hex.DecodeString(guidDigest)
	return err == nil && len(decodedGUID) == sha256.Size && hex.EncodeToString(decodedGUID) == guidDigest
}

func stageArtifactName(key, targetBase, suffix string) string {
	return stagePrefix + key + stageSeparator + targetBase + suffix
}

// parseStageArtifact splits a staging artifact name into its GUID key and
// destination base name. sanitizeFilename collapses separator runs and
// enclosure extensions are alphanumeric, so the separator can never occur
// inside a destination base.
func parseStageArtifact(name, suffix string) (key, targetBase string, ok bool) {
	trimmed := strings.TrimPrefix(name, stagePrefix)
	if trimmed == name || !strings.HasSuffix(trimmed, suffix) {
		return "", "", false
	}
	trimmed = strings.TrimSuffix(trimmed, suffix)
	key, targetBase, found := strings.Cut(trimmed, stageSeparator)
	if !found || key == "" || targetBase == "" {
		return "", "", false
	}
	return key, targetBase, true
}

// listStageArtifacts returns the staging artifact names with the given
// suffix in root; a root that does not exist yet has none.
func listStageArtifacts(root, suffix string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if _, _, ok := parseStageArtifact(entry.Name(), suffix); ok {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// findStagedPayload returns the staged payload path for the GUID key, or ""
// when no completed download is staged for it.
func findStagedPayload(root, key string) (string, error) {
	names, err := listStageArtifacts(root, stagedSuffix)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		parsedKey, _, _ := parseStageArtifact(name, stagedSuffix)
		if parsedKey == key {
			return filepath.Join(root, name), nil
		}
	}
	return "", nil
}

// removeStageArtifacts deletes the GUID key's artifacts with the given
// suffix — compensation for a crashed attempt before a fresh one starts.
func removeStageArtifacts(root, key, suffix string) error {
	names, err := listStageArtifacts(root, suffix)
	if err != nil {
		return err
	}
	for _, name := range names {
		parsedKey, _, _ := parseStageArtifact(name, suffix)
		if parsedKey != key {
			continue
		}
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}

// sweepStaleArtifacts removes this Subscription's staging leftovers after a
// fully clean poll. Anything owned by another Subscription remains available
// for that owner's receipt-scoped resume. An owned leftover has no matching
// receipt or resume would already have published it, so its unseen entry can
// safely re-download if it returns to the feed.
func sweepStaleArtifacts(root string, subscriptionID store.SubscriptionID) error {
	for _, suffix := range []string{partialSuffix, stagedSuffix} {
		names, err := listStageArtifacts(root, suffix)
		if err != nil {
			return err
		}
		for _, name := range names {
			key, _, _ := parseStageArtifact(name, suffix)
			if !stageKeyOwnedBy(key, subscriptionID) {
				continue
			}
			if err := os.Remove(filepath.Join(root, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncDir flushes a directory so a completed rename survives power loss:
// the rename itself is atomic, but the updated directory entry needs its
// own sync to be durable.
func syncDir(dir string) error {
	handle, err := os.Open(dir) // #nosec G304 -- read-only open of the Subscription root this package created, solely to sync the directory
	if err != nil {
		return err
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

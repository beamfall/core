package webhook

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

// Catalog is the read surface the emitter derives human-meaningful events
// from. *store.Store satisfies it.
type Catalog interface {
	Media(ctx context.Context, id store.MediaID, filter store.LibraryFilter) (store.Media, error)
	ActiveSessions(ctx context.Context, now time.Time, filter store.LibraryFilter) ([]store.ActiveSession, error)
	Profile(ctx context.Context, id store.ProfileID) (store.Profile, error)
	Device(ctx context.Context, id store.DeviceID) (store.Device, error)
	ActivityEntriesAfter(ctx context.Context, afterID int64, actions []string, limit int) ([]store.ActivityEntry, error)
	LatestActivityID(ctx context.Context) (int64, error)
	LatestEventSeq(ctx context.Context) (int64, error)
}

// ArtworkSource loads a Media's poster for formats that attach the image to
// the message itself.
type ArtworkSource interface {
	Artwork(ctx context.Context, id store.MediaID) (*Artwork, error)
}

// DeriveOptions wires event derivation into an emitter.
type DeriveOptions struct {
	Catalog Catalog
	// Sealed reports whether a Library is an encrypted library that is
	// currently sealed. Nothing from a sealed Library is ever sent.
	Sealed func(store.LibraryID) bool
	// PublicBaseURL returns the owner-configured public origin used to build
	// artwork URLs; empty leaves ArtworkURL unset.
	PublicBaseURL func() string
	Artwork       ArtworkSource
}

// deriveState persists the activity cursor and the seeded flag.
// *FileDeadLetter satisfies it; memoryState is the fallback.
type deriveState interface {
	ActivityCursor() int64
	SetActivityCursor(int64) error
	Seeded() bool
	SetSeeded(bool) error
}

type memoryState struct {
	mu       sync.Mutex
	activity int64
	seeded   bool
}

func (m *memoryState) ActivityCursor() int64 { m.mu.Lock(); defer m.mu.Unlock(); return m.activity }
func (m *memoryState) SetActivityCursor(c int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c > m.activity {
		m.activity = c
	}
	return nil
}
func (m *memoryState) Seeded() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.seeded }
func (m *memoryState) SetSeeded(s bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seeded = s
	return nil
}

// activityKinds is the allowlist of audit actions that become derived
// events. Every other action (configuration changes, enrollment refusals,
// library operations) stays in the activity log only.
var activityKinds = map[string]string{
	store.ActivityProfileSwitch:    KindProfileSwitched,
	store.ActivityAdminLogin:       KindAdminSignedIn,
	store.ActivityAdminLoginDenied: KindAdminSignInFailed,
	store.ActivityEnroll:           KindDeviceEnrolled,
	store.ActivityDeviceRevoke:     KindDeviceRevoked,
	store.ActivitySessionTerminate: KindSessionTerminated,
}

var activityActions = func() []string {
	actions := make([]string, 0, len(activityKinds))
	for action := range activityKinds {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}()

// activityBatch bounds one tick's activity read.
const activityBatch = 100

// playbackEntry is one live (session, Media) pair in the playback snapshot.
type playbackEntry struct {
	row   store.ActiveSession
	media store.Media
}

// deriver computes the derived events from the catalog. It runs only on the
// emitter's poll goroutine, so its snapshot needs no lock.
type deriver struct {
	catalog       Catalog
	sealed        func(store.LibraryID) bool
	publicBaseURL func() string
	state         deriveState
	now           func() time.Time

	// sessions is the last playback snapshot; primed is false until the
	// first snapshot is taken, which emits nothing, so a restart never
	// reports sessions that were already playing as newly started.
	sessions map[string]playbackEntry
	primed   bool
}

// filter is the read scope for derived events: every Library, restricted
// ones unrevealed, so a restricted Library's titles never leave the server.
// Sealed libraries are checked separately after the read.
var deriveFilter = store.LibraryFilter{AllLibraries: true, Revealed: false}

func (d *deriver) isSealed(id store.LibraryID) bool {
	return id != "" && d.sealed != nil && d.sealed(id)
}

// idle resets derivation while no endpoint is enabled. It does no reads; the
// next seed starts from the tail.
func (d *deriver) idle() {
	d.sessions = nil
	d.primed = false
	if d.state.Seeded() {
		_ = d.state.SetSeeded(false)
	}
}

// seed moves the cursors to the current tail the first time an endpoint is
// enabled, so a new endpoint never receives history. It reports whether it
// seeded and the outbox cursor to resume from. A state written before
// derivation existed (an outbox cursor but no activity cursor and never
// seeded) keeps its outbox cursor, so an upgrade does not skip spine events
// queued across the restart.
func (d *deriver) seed(ctx context.Context, cursor int64) (bool, int64, error) {
	if d.state.Seeded() {
		return false, cursor, nil
	}
	activity, err := d.catalog.LatestActivityID(ctx)
	if err != nil {
		return false, cursor, err
	}
	next := cursor
	upgrade := cursor > 0 && d.state.ActivityCursor() == 0
	if !upgrade {
		seq, err := d.catalog.LatestEventSeq(ctx)
		if err != nil {
			return false, cursor, err
		}
		if seq > next {
			next = seq
		}
	}
	if err := d.state.SetActivityCursor(activity); err != nil {
		return false, cursor, ErrCursorDiagnostic
	}
	if err := d.state.SetSeeded(true); err != nil {
		return false, cursor, ErrCursorDiagnostic
	}
	return true, next, nil
}

// artworkURL is the poster URL under the owner's public origin. Fetching it
// needs a session; it identifies the image for receivers that hold one.
func (d *deriver) artworkURL(id store.MediaID) string {
	if d.publicBaseURL == nil {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(d.publicBaseURL()), "/")
	if base == "" {
		return ""
	}
	return base + "/api/media/artwork/" + string(id) + "/poster"
}

func (d *deriver) mediaNotification(kind string, m store.Media) Notification {
	return Notification{
		Kind:          kind,
		AggregateType: store.OutboxAggregateMedia,
		AggregateID:   string(m.ID),
		MediaID:       string(m.ID),
		Title:         m.Title,
		MediaClass:    string(m.Class),
		Year:          m.Year,
		ArtworkURL:    d.artworkURL(m.ID),
	}
}

// mediaAdded derives media.added from a media.created spine event. A Media
// that is no longer readable, sits in a restricted Library or in a sealed
// one yields nothing.
func (d *deriver) mediaAdded(ctx context.Context, event store.OutboxEvent) (Notification, bool, error) {
	m, err := d.catalog.Media(ctx, store.MediaID(event.AggregateID), deriveFilter)
	if err != nil {
		if ctx.Err() != nil {
			return Notification{}, false, ctx.Err()
		}
		return Notification{}, false, nil
	}
	if d.isSealed(m.LibraryID) || strings.TrimSpace(m.Title) == "" {
		return Notification{}, false, nil
	}
	n := d.mediaNotification(KindMediaAdded, m)
	// Namespaced so it never equals the raw outbox event's id, which an
	// unfiltered generic endpoint also receives for the same row.
	n.ID = KindMediaAdded + "-" + strconv.FormatInt(event.Seq, 10)
	n.OccurredAt = event.OccurredAt
	return n, true, nil
}

// playback diffs the live sessions against the last snapshot and emits a
// start for each new (session, Media) pair and a stop for each that ended.
// The snapshot is replaced before delivery, so a failed delivery is
// dead-lettered once rather than re-derived every tick.
func (d *deriver) playback(ctx context.Context, e *Emitter) error {
	if !e.subscribed(KindPlaybackStarted) && !e.subscribed(KindPlaybackStopped) {
		d.sessions = nil
		d.primed = false
		return nil
	}
	now := d.now()
	rows, err := d.catalog.ActiveSessions(ctx, now, deriveFilter)
	if err != nil {
		return err
	}
	current := make(map[string]playbackEntry, len(rows))
	hidden := make(map[string]bool)
	for _, row := range rows {
		key := string(row.SessionID) + "|" + string(row.MediaID)
		if strings.TrimSpace(row.Title) == "" || d.isSealed(row.LibraryID) {
			hidden[key] = true // a hidden or sealed Media is never reported
			continue
		}
		if prev, ok := d.sessions[key]; ok {
			current[key] = playbackEntry{row: row, media: prev.media}
			continue
		}
		entry := playbackEntry{row: row}
		if m, err := d.catalog.Media(ctx, row.MediaID, deriveFilter); err == nil && !d.isSealed(m.LibraryID) {
			entry.media = m
		} else {
			entry.media = store.Media{ID: row.MediaID, LibraryID: row.LibraryID, Title: row.Title}
		}
		current[key] = entry
	}
	previous := d.sessions
	d.sessions = current
	if !d.primed {
		d.primed = true
		return nil
	}
	for _, key := range sortedKeys(current) {
		if _, ok := previous[key]; !ok {
			if err := e.notify(ctx, d.playbackNotification(KindPlaybackStarted, current[key], now)); err != nil {
				return err
			}
		}
	}
	for _, key := range sortedKeys(previous) {
		if _, ok := current[key]; ok || hidden[key] {
			continue
		}
		if !d.stillVisible(ctx, previous[key]) {
			continue // sealed or restricted since it started: say nothing about it
		}
		if err := e.notify(ctx, d.playbackNotification(KindPlaybackStopped, previous[key], now)); err != nil {
			return err
		}
	}
	return nil
}

// stillVisible reports whether a Media whose session left the live view may
// still be named. A session in a Library sealed or restricted since playback
// started drops out of the filtered view just as a finished one does; its stop
// would replay the cached title, Profile and device, so it is not reported.
func (d *deriver) stillVisible(ctx context.Context, entry playbackEntry) bool {
	if d.isSealed(entry.row.LibraryID) {
		return false
	}
	m, err := d.catalog.Media(ctx, entry.row.MediaID, deriveFilter)
	return err == nil && !d.isSealed(m.LibraryID) && strings.TrimSpace(m.Title) != ""
}

func (d *deriver) playbackNotification(kind string, entry playbackEntry, at time.Time) Notification {
	n := d.mediaNotification(kind, entry.media)
	if n.Title == "" {
		n.Title = entry.row.Title
	}
	verb := "started"
	if kind == KindPlaybackStopped {
		verb = "stopped"
	}
	n.ID = fmt.Sprintf("playback-%s-%s-%s-%d", entry.row.SessionID, entry.row.MediaID, verb, at.Unix())
	n.OccurredAt = at
	n.ProfileName = entry.row.ProfileName
	n.DeviceName = entry.row.DeviceName
	return n
}

func sortedKeys(m map[string]playbackEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// activity derives user and session events from new allowlisted activity-log
// rows. The cursor advances past a row once it has been offered to every
// endpoint, as drain does for the outbox: at-least-once, with the stable
// activity-<id> event id letting a receiver drop a replay after a restart.
func (d *deriver) activity(ctx context.Context, e *Emitter) error {
	entries, err := d.catalog.ActivityEntriesAfter(ctx, d.state.ActivityCursor(), activityActions, activityBatch)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if kind, ok := activityKinds[entry.Action]; ok && entry.LibraryID == "" && e.subscribed(kind) {
			if err := e.notify(ctx, d.activityNotification(ctx, kind, entry)); err != nil {
				return err
			}
		}
		if err := d.state.SetActivityCursor(entry.ID); err != nil {
			return ErrCursorDiagnostic
		}
	}
	return nil
}

func (d *deriver) activityNotification(ctx context.Context, kind string, entry store.ActivityEntry) Notification {
	n := Notification{
		ID:            "activity-" + strconv.FormatInt(entry.ID, 10),
		Kind:          kind,
		AggregateType: entry.TargetType,
		AggregateID:   entry.TargetID,
		OccurredAt:    entry.OccurredAt,
	}
	profileID := entry.ProfileID
	if kind == KindProfileSwitched && entry.TargetType == "profile" && entry.TargetID != "" {
		profileID = store.ProfileID(entry.TargetID)
	}
	if profileID != "" {
		if p, err := d.catalog.Profile(ctx, profileID); err == nil {
			n.ProfileName = p.Name
		}
	}
	deviceID := entry.DeviceID
	if (kind == KindDeviceEnrolled || kind == KindDeviceRevoked) && entry.TargetType == "device" && entry.TargetID != "" {
		deviceID = store.DeviceID(entry.TargetID)
	}
	if deviceID != "" {
		if dev, err := d.catalog.Device(ctx, deviceID); err == nil {
			n.DeviceName = dev.Name
		}
	}
	return n
}

// EnableDerivation turns on the derived events. Call it before Run.
func (e *Emitter) EnableDerivation(opts DeriveOptions) {
	if opts.Catalog == nil {
		return
	}
	state, ok := e.deadLetter.(deriveState)
	if !ok {
		state = &memoryState{}
	}
	e.derive = &deriver{
		catalog:       opts.Catalog,
		sealed:        opts.Sealed,
		publicBaseURL: opts.PublicBaseURL,
		state:         state,
		now:           func() time.Time { return e.now() },
	}
	e.artwork = opts.Artwork
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// ErrQueueMediaNotFound covers missing targets and targets hidden from the queue.
const ErrQueueMediaNotFound = ValidationError("media not found")

// ErrQueueMediaLocked reports a visible target blocked by certification.
var ErrQueueMediaLocked = errors.New("media is locked")

// ErrQueueProfileOutsideSchedule reports a Profile whose schedule disallows
// the queue write at the current time.
var ErrQueueProfileOutsideSchedule = errors.New("profile access not allowed at this time")

// ErrQueueReboundProfileOutsideSchedule identifies a refusal for a Profile
// other than the one the request authenticated for.
var ErrQueueReboundProfileOutsideSchedule = fmt.Errorf("session changed profile: %w", ErrQueueProfileOutsideSchedule)

// QueueAddResult captures the placement, visible first page, and scope at commit.
type QueueAddResult struct {
	Items     []SourceItem
	Filter    LibraryFilter
	ProfileID ProfileID
	MediaID   MediaID
	Position  *int // Zero-based in the visible queue, nil for a muted target.
}

// QueueWriteAuthorization carries the request scope and policy evaluators.
// ResolveCertification must not call back into the store.
type QueueWriteAuthorization struct {
	ProfileID            ProfileID
	SessionID            SessionID
	ExpectedProfileID    ProfileID
	Filter               LibraryFilter
	Now                  func() time.Time
	ResolveCertification func([]ProfileCertificationCeiling) []CertificationConstraint
}

// QueueAddForProfile adds or moves Media using current store authorization.
func (s *Store) QueueAddForProfile(ctx context.Context, profileID ProfileID, sessionID SessionID, expectedProfileID ProfileID, mediaID MediaID, position *int, filter LibraryFilter, authNow func() time.Time, resolveCertification func([]ProfileCertificationCeiling) []CertificationConstraint) (QueueAddResult, error) {
	if profileID == "" || mediaID == "" {
		return QueueAddResult{}, ValidationError("profile ID and media ID are required")
	}
	auth := QueueWriteAuthorization{
		ProfileID: profileID, SessionID: sessionID, ExpectedProfileID: expectedProfileID,
		Filter: filter, Now: authNow, ResolveCertification: resolveCertification,
	}
	var result QueueAddResult
	_, err := s.writeAuthorizedQueue(ctx, auth, []MediaID{mediaID}, "", false, func(view queueWriteView) (PlayQueue, error) {
		view.queue.addLegacy(mediaID, position, view.visible, s.serverNow())
		var err error
		result, err = s.queueAddProjection(ctx, view, mediaID)
		return PlayQueue{}, err
	})
	if err != nil {
		return QueueAddResult{}, err
	}
	return result, nil
}

// QueueAddMany applies an ordered batch only if all targets are
// eligible at commit.
func (s *Store) QueueAddMany(ctx context.Context, auth QueueWriteAuthorization, mediaIDs []MediaID, placement QueuePlacement) (PlayQueue, error) {
	if !validQueueBatch(mediaIDs, placement) {
		return PlayQueue{}, ValidationError("invalid queue placement or empty media list")
	}
	return s.writeAuthorizedQueue(ctx, auth, mediaIDs, "", false, func(view queueWriteView) (PlayQueue, error) {
		view.queue.addMany(mediaIDs, placement, view.visible)
		return view.queue.playQueue(view.visible), nil
	})
}

// QueuePlayNext moves an existing entry next after checking the
// entry's Media against the current Profile policy.
func (s *Store) QueuePlayNext(ctx context.Context, auth QueueWriteAuthorization, entryID string) (PlayQueue, error) {
	if entryID == "" {
		return PlayQueue{}, ErrQueueMediaNotFound
	}
	return s.writeAuthorizedQueue(ctx, auth, nil, entryID, false, func(view queueWriteView) (PlayQueue, error) {
		if err := view.queue.moveEntry(queueTarget{entryID: entryID}, nil, view.visible); err != nil {
			return PlayQueue{}, err
		}
		return view.queue.playQueue(view.visible), nil
	})
}

// EditPlayQueue applies an edit and projects the resulting queue with the same
// visibility, while the catalog writer reservation holds the session stable.
func (s *Store) EditPlayQueue(ctx context.Context, auth QueueWriteAuthorization, edit QueueEdit) (PlayQueue, error) {
	if auth.ProfileID == "" {
		return PlayQueue{}, ValidationError("profile ID is required")
	}
	return s.writeAuthorizedQueue(ctx, auth, nil, "", true, func(view queueWriteView) (PlayQueue, error) {
		if err := edit(view.queue, view.visible); err != nil {
			return PlayQueue{}, err
		}
		return view.queue.playQueue(view.visible), nil
	})
}

type queueWriteView struct {
	queue     *queueSnapshot
	visible   map[MediaID]Media
	readers   mediaReaders
	filter    LibraryFilter
	profileID ProfileID
}

func (s *Store) writeAuthorizedQueue(ctx context.Context, auth QueueWriteAuthorization, targets []MediaID, entryID string, lockEntries bool, mutate func(queueWriteView) (PlayQueue, error)) (PlayQueue, error) {
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return PlayQueue{}, err
	}
	if auth.ProfileID == "" || auth.Now == nil || auth.ResolveCertification == nil {
		return PlayQueue{}, errors.New("queue write requires a profile, clock and certification resolver")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlayQueue{}, err
	}

	// Catalog and private planes hold BEGIN IMMEDIATE reservations through
	// the events commit. Only the events transaction changes any data.
	catalog, closeCatalog, err := beginQueuePolicyTx(ctx, s.catalogW)
	if err != nil {
		return PlayQueue{}, err
	}
	defer closeCatalog()
	policy, err := readQueueWritePolicy(ctx, catalog, auth)
	if err != nil {
		return PlayQueue{}, err
	}
	events, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return PlayQueue{}, err
	}
	defer events.Rollback()
	q, err := loadQueue(ctx, events, policy.profileID)
	if err != nil {
		return PlayQueue{}, err
	}
	if entryID != "" {
		i := slices.IndexFunc(q.entries, func(entry queueEntry) bool { return entry.id == entryID })
		if i < 0 {
			return PlayQueue{}, ErrQueueMediaNotFound
		}
		targets = []MediaID{q.entries[i].mediaID}
	}
	mediaIDs := queueMediaIDs(q.entries, targets)
	lockTargets := targets
	if lockEntries {
		// State edits act on, and return, the currently visible queue.
		lockTargets = mediaIDs
	}
	private := map[LibraryID]*sql.Tx{}
	readers, release, err := s.queueWriteReaders(ctx, catalog, policy.filter, lockTargets, private)
	if err != nil {
		return PlayQueue{}, err
	}
	defer release()
	visible, err := visibleMediaBatch(ctx, readers, mediaIDs, policy.filter)
	if err != nil {
		return PlayQueue{}, err
	}
	for _, id := range targets {
		lib := visible[id].LibraryID
		if s.encryptedMediaReaderLocked(lib) != nil && private[lib] == nil {
			return PlayQueue{}, ErrQueueMediaNotFound
		}
	}
	if err := authorizeQueueTargets(ctx, readers, policy.filter, visible, targets); err != nil {
		return PlayQueue{}, err
	}
	original := slices.Clone(q.entries)
	result, err := mutate(queueWriteView{&q, visible, readers, policy.filter, policy.profileID})
	if err != nil {
		return PlayQueue{}, err
	}
	if err := policy.checkAccess(ctx, catalog, auth.Now()); err != nil {
		return PlayQueue{}, err
	}
	if err := saveQueue(ctx, events, policy.profileID, q, original); err != nil {
		return PlayQueue{}, err
	}
	if err := policy.checkAccess(ctx, catalog, auth.Now()); err != nil {
		return PlayQueue{}, err
	}
	if err := events.Commit(); err != nil {
		return PlayQueue{}, err
	}
	return result, nil
}

// beginQueuePolicyTx lets cancellation abort connection acquisition and the
// queue write while keeping policy locks held until the write finishes.
func beginQueuePolicyTx(ctx context.Context, db *sql.DB) (*sql.Tx, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return tx, func() {
		_ = tx.Rollback()
		_ = conn.Close()
	}, nil
}

// queueWriteReaders routes targets to each eligible private plane, then
// re-reads visibility and certification under that plane's writer lock.
func (s *Store) queueWriteReaders(ctx context.Context, catalog *sql.Tx, filter LibraryFilter, targets []MediaID, locked map[LibraryID]*sql.Tx) (mediaReaders, func(), error) {
	readers := mediaReaders{catalog: catalog, encrypted: func(id LibraryID) rowsQueryer {
		if tx := locked[id]; tx != nil {
			return tx
		}
		return s.encryptedMediaReaderLocked(id)
	}}
	var releases []func()
	release := func() {
		for _, closeTx := range slices.Backward(releases) {
			closeTx()
		}
	}
	if len(s.encryptedDBs) == 0 {
		return readers, release, nil
	}

	routingFilter := filter
	routingFilter.Certification = nil
	candidates, err := visibleMediaBatch(ctx, readers, targets, routingFilter)
	if err != nil {
		return mediaReaders{}, nil, err
	}
	for _, id := range targets {
		lib := candidates[id].LibraryID
		if s.encryptedMediaReaderLocked(lib) == nil || locked[lib] != nil {
			continue
		}
		tx, closeTx, err := beginQueuePolicyTx(ctx, s.encryptedDBs[lib].writer)
		if err != nil {
			release()
			return mediaReaders{}, nil, err
		}
		locked[lib] = tx
		releases = append(releases, closeTx)
	}
	return readers, release, nil
}

func authorizeQueueTargets(ctx context.Context, readers mediaReaders, filter LibraryFilter, visible map[MediaID]Media, targets []MediaID) error {
	items := make([]SourceItem, 0, len(targets))
	for _, target := range targets {
		media, ok := visible[target]
		if !ok {
			return ErrQueueMediaNotFound
		}
		items = append(items, queueSourceItem(media))
	}
	if err := annotateLocked(ctx, readers, filter.Certification, items, filter.GrantedMediaIDs); err != nil {
		return err
	}
	for _, item := range items {
		if item.Locked {
			return ErrQueueMediaLocked
		}
	}
	return nil
}

func (s *Store) queueAddProjection(ctx context.Context, view queueWriteView, mediaID MediaID) (QueueAddResult, error) {
	items := make([]SourceItem, 0, len(view.visible))
	seen := make(map[MediaID]bool, len(view.visible))
	for _, entry := range *view.queue.activeOrder() {
		if media, ok := view.visible[entry.mediaID]; ok && !seen[entry.mediaID] {
			items = append(items, queueSourceItem(media))
			seen[entry.mediaID] = true
		}
	}
	items, err := s.applyMuteFilterLocked(ctx, s.catalogR, view.profileID, view.filter, items)
	if err != nil {
		return QueueAddResult{}, err
	}
	var position *int
	if i := slices.IndexFunc(items, func(item SourceItem) bool { return item.ID == mediaID }); i >= 0 {
		position = &i
	}
	items = pageOf(items, defaultSourceItemsLimit, 0)
	if view.filter.Revealed {
		restricted, err := restrictedLibraryIDs(ctx, view.readers.catalog)
		if err != nil {
			return QueueAddResult{}, err
		}
		for i := range items {
			items[i].Restricted = restricted[items[i].LibraryID]
		}
	}
	if err := annotateLocked(ctx, view.readers, view.filter.Certification, items, view.filter.GrantedMediaIDs); err != nil {
		return QueueAddResult{}, err
	}
	return QueueAddResult{
		Items: slices.Clone(items), Filter: view.filter, ProfileID: view.profileID,
		MediaID: mediaID, Position: position,
	}, nil
}

type queueWritePolicy struct {
	session   Session
	profileID ProfileID
	windows   []ProfileAccessWindow
	filter    LibraryFilter
	rebound   bool
}

func readQueueWritePolicy(ctx context.Context, tx *sql.Tx, auth QueueWriteAuthorization) (queueWritePolicy, error) {
	policy := queueWritePolicy{profileID: auth.ProfileID, filter: auth.Filter}
	if auth.SessionID != "" {
		session, err := readSession(ctx, tx, auth.SessionID)
		if err != nil {
			return queueWritePolicy{}, err
		}
		device, err := readDevice(ctx, tx, session.DeviceID)
		if err != nil {
			return queueWritePolicy{}, err
		}
		if device.Revoked {
			return queueWritePolicy{}, ErrDeviceRevoked
		}
		policy.session = session
	}
	policy.rebound = auth.SessionID != "" && policy.session.ProfileID != auth.ProfileID
	policy.filter.Revealed = policy.filter.Revealed && policy.session.Revealed
	if policy.rebound {
		policy.profileID = policy.session.ProfileID
		policy.filter.Revealed = policy.session.Revealed
	}
	if auth.ExpectedProfileID != "" && auth.ExpectedProfileID != policy.profileID {
		return queueWritePolicy{}, ErrSessionProfileMismatch
	}
	profile, err := readProfile(ctx, tx, policy.profileID)
	if err != nil {
		return queueWritePolicy{}, err
	}
	if profile.Disabled {
		return queueWritePolicy{}, ErrProfileDisabled
	}
	if profile.Kind == ProfileManaged {
		policy.windows, err = readProfileAccessWindows(ctx, tx, policy.profileID)
		if err != nil {
			return queueWritePolicy{}, err
		}
		policy.filter.GrantedMediaIDs, err = readGrantedMediaIDs(ctx, tx, policy.profileID)
		if err != nil {
			return queueWritePolicy{}, err
		}
	} else {
		policy.filter.GrantedMediaIDs = nil
	}
	if err := policy.checkAccess(ctx, tx, auth.Now()); err != nil {
		return queueWritePolicy{}, err
	}
	policy.filter = queueAddFilter(profile, policy.rebound, policy.filter)
	policy.filter.Certification = auth.ResolveCertification(profile.CertificationCeilings)
	return policy, nil
}

func queueAddFilter(profile Profile, rebound bool, filter LibraryFilter) LibraryFilter {
	if rebound {
		filter.AllLibraries = false
		filter.AllowedIDs = profile.Library
	} else if !filter.AllLibraries {
		allowed := make([]LibraryID, 0, len(filter.AllowedIDs))
		for _, id := range filter.AllowedIDs {
			if slices.Contains(profile.Library, id) {
				allowed = append(allowed, id)
			}
		}
		filter.AllowedIDs = allowed
	}
	filter.Revealed = filter.Revealed && profile.Kind == ProfileAdult
	return filter
}

func (p queueWritePolicy) checkAccess(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if !p.session.ExpiresAt.IsZero() && !now.Before(p.session.ExpiresAt) {
		return ErrSessionExpired
	}
	if AllowedAt(p.windows, now.Local()) {
		return nil
	}
	if p.session.ID == "" {
		return ErrQueueProfileOutsideSchedule
	}
	granted, err := activeExtensionGrant(ctx, tx, p.profileID, p.session.ID, now)
	if err != nil {
		return err
	}
	if !granted {
		if p.rebound {
			return ErrQueueReboundProfileOutsideSchedule
		}
		return ErrQueueProfileOutsideSchedule
	}
	return nil
}

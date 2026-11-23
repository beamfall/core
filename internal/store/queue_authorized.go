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

// QueueItemsResult contains the visible first page and its scope at commit.
type QueueItemsResult struct {
	Items     []SourceItem
	Filter    LibraryFilter
	ProfileID ProfileID
}

// QueueAddResult captures the placement, visible first page, and scope at commit.
type QueueAddResult struct {
	QueueItemsResult
	MediaID  MediaID
	Position *int // Zero-based in the visible queue, nil for a muted target.
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
	trustedFilter        bool // Store-internal edits already have a visibility filter.
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
	result := QueueAddResult{MediaID: mediaID}
	err := s.writeQueue(ctx, auth, queueWriteTargets{ids: []MediaID{mediaID}, requireEligible: true}, func(view queueWriteView) error {
		view.queue.addLegacy(mediaID, position, view.visible, s.serverNow())
		var err error
		result.QueueItemsResult, result.Position, err = s.queueItemsProjection(ctx, view, mediaID)
		return err
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
	return s.editPlayQueue(ctx, auth, queueWriteTargets{ids: mediaIDs, requireEligible: true}, func(q *queueSnapshot, visible map[MediaID]Media) error {
		q.addMany(mediaIDs, placement, visible)
		return nil
	})
}

// QueuePlayNext moves an existing entry next after checking the
// entry's Media against the current Profile policy.
func (s *Store) QueuePlayNext(ctx context.Context, auth QueueWriteAuthorization, entryID string) (PlayQueue, error) {
	if entryID == "" {
		return PlayQueue{}, ErrQueueMediaNotFound
	}
	return s.editPlayQueue(ctx, auth, queueWriteTargets{entryID: entryID, requireEligible: true}, moveQueueTarget(queueTarget{entryID: entryID}, nil))
}

// EditPlayQueue applies an edit and returns the resulting entry-based queue.
func (s *Store) EditPlayQueue(ctx context.Context, auth QueueWriteAuthorization, edit QueueEdit) (PlayQueue, error) {
	return s.editPlayQueue(ctx, auth, queueWriteTargets{}, edit)
}

func (s *Store) editPlayQueue(ctx context.Context, auth QueueWriteAuthorization, targets queueWriteTargets, edit QueueEdit) (PlayQueue, error) {
	var result PlayQueue
	err := s.writeQueue(ctx, auth, targets, func(view queueWriteView) error {
		if err := edit(view.queue, view.visible); err != nil {
			return err
		}
		result = view.queue.playQueue(view.visible)
		return nil
	})
	if err != nil {
		return PlayQueue{}, err
	}
	return result, nil
}

// EditQueueItems applies an edit and returns the legacy Media-addressed queue page.
func (s *Store) EditQueueItems(ctx context.Context, auth QueueWriteAuthorization, edit QueueEdit) (QueueItemsResult, error) {
	return s.editQueueItems(ctx, auth, nil, edit)
}

// StartQueueStation replaces the visible entries and returns the legacy queue page.
func (s *Store) StartQueueStation(ctx context.Context, auth QueueWriteAuthorization, mediaIDs []MediaID, confirmReplace bool) (QueueItemsResult, error) {
	return s.editQueueItems(ctx, auth, mediaIDs, replaceQueueWithStation(mediaIDs, confirmReplace))
}

func (s *Store) editQueueItems(ctx context.Context, auth QueueWriteAuthorization, extra []MediaID, edit QueueEdit) (QueueItemsResult, error) {
	var result QueueItemsResult
	err := s.writeQueue(ctx, auth, queueWriteTargets{ids: extra}, func(view queueWriteView) error {
		if err := edit(view.queue, view.visible); err != nil {
			return err
		}
		var err error
		result, _, err = s.queueItemsProjection(ctx, view, "")
		return err
	})
	if err != nil {
		return QueueItemsResult{}, err
	}
	return result, nil
}

type queueWriteTargets struct {
	ids             []MediaID
	entryID         string
	requireEligible bool
}

type queueWriteView struct {
	queue     *queueSnapshot
	visible   map[MediaID]Media
	readers   mediaReaders
	filter    LibraryFilter
	profileID ProfileID
	// events is the transaction the queue saves in, for writes that must
	// commit or roll back with it.
	events *sql.Tx
}

// writeQueue binds visibility and Profile policy to the events commit for every
// queue edit. The catalog and encrypted-plane transactions only reserve writers.
func (s *Store) writeQueue(ctx context.Context, auth QueueWriteAuthorization, targets queueWriteTargets, mutate func(queueWriteView) error) error {
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return err
	}
	if auth.ProfileID == "" {
		return ValidationError("profile ID is required")
	}
	if auth.Now == nil || (!auth.trustedFilter && auth.ResolveCertification == nil) {
		return errors.New("queue write requires a clock and certification resolver")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}

	catalog, closeCatalog, err := beginCatalogPolicyTx(ctx, s.catalogW)
	if err != nil {
		return err
	}
	defer closeCatalog()
	policy, err := readQueueWritePolicy(ctx, catalog, auth)
	if err != nil {
		return err
	}
	events, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer events.Rollback()
	q, err := loadQueue(ctx, events, policy.profileID)
	if err != nil {
		return err
	}
	if targets.entryID != "" {
		i := slices.IndexFunc(q.entries, func(entry queueEntry) bool { return entry.id == targets.entryID })
		if i < 0 {
			return ErrQueueMediaNotFound
		}
		targets.ids = []MediaID{q.entries[i].mediaID}
	}
	mediaIDs := queueMediaIDs(q.entries, targets.ids)
	readers := mediaReaders{catalog: catalog, encrypted: func(LibraryID) rowsQueryer { return nil }}
	if len(mediaIDs) != 0 && len(s.encryptedDBs) != 0 {
		encrypted, release, err := s.queueEncryptedReaders(ctx, catalog, policy.filter)
		if err != nil {
			return err
		}
		defer release()
		readers.encrypted = encrypted
	}
	visible, err := visibleMediaBatch(ctx, readers, mediaIDs, policy.filter)
	if err != nil {
		return err
	}
	if targets.requireEligible {
		if err := authorizeQueueTargets(ctx, readers, policy.filter, visible, targets.ids); err != nil {
			return err
		}
	}
	original := slices.Clone(q.entries)
	if err := mutate(queueWriteView{&q, visible, readers, policy.filter, policy.profileID, events}); err != nil {
		return err
	}
	if err := policy.checkAccess(ctx, catalog, auth.Now()); err != nil {
		return err
	}
	if err := saveQueue(ctx, events, policy.profileID, q, original); err != nil {
		return err
	}
	if err := policy.checkAccess(ctx, catalog, auth.Now()); err != nil {
		return err
	}
	return events.Commit()
}

// beginCatalogPolicyTx lets cancellation abort connection acquisition while
// keeping policy locks held until the dependent write finishes.
func beginCatalogPolicyTx(ctx context.Context, db *sql.DB) (*sql.Tx, func(), error) {
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

// queueEncryptedReaders reserves every eligible open private plane before
// evaluating visibility, including planes used only by existing queue entries.
func (s *Store) queueEncryptedReaders(ctx context.Context, catalog *sql.Tx, filter LibraryFilter) (func(LibraryID) rowsQueryer, func(), error) {
	libraries, err := loadLibraries(ctx, catalog)
	if err != nil {
		return nil, nil, err
	}
	locked := map[LibraryID]rowsQueryer{}
	var releases []func()
	release := func() {
		for _, closeTx := range slices.Backward(releases) {
			closeTx()
		}
	}
	for _, library := range libraries {
		db := s.encryptedDBs[library.ID]
		if db == nil || db.state != "unsealed-registered" || db.writer == nil || db.reader == nil ||
			!library.Encrypted || !libraryVisible(library, filter) {
			continue
		}
		tx, closeTx, err := beginCatalogPolicyTx(ctx, db.writer)
		if err != nil {
			release()
			return nil, nil, err
		}
		locked[library.ID] = tx
		releases = append(releases, closeTx)
	}
	return func(id LibraryID) rowsQueryer { return locked[id] }, release, nil
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

func (s *Store) queueItemsProjection(ctx context.Context, view queueWriteView, mediaID MediaID) (QueueItemsResult, *int, error) {
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
		return QueueItemsResult{}, nil, err
	}
	var position *int
	if i := slices.IndexFunc(items, func(item SourceItem) bool { return item.ID == mediaID }); i >= 0 {
		position = &i
	}
	items = pageOf(items, defaultSourceItemsLimit, 0)
	if view.filter.Revealed {
		restricted, err := restrictedLibraryIDs(ctx, view.readers.catalog)
		if err != nil {
			return QueueItemsResult{}, nil, err
		}
		for i := range items {
			items[i].Restricted = restricted[items[i].LibraryID]
		}
	}
	if err := annotateLocked(ctx, view.readers, view.filter.Certification, items, view.filter.GrantedMediaIDs); err != nil {
		return QueueItemsResult{}, nil, err
	}
	return QueueItemsResult{Items: slices.Clone(items), Filter: view.filter, ProfileID: view.profileID}, position, nil
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
	if !auth.trustedFilter {
		// Relocking revokes Reveal from an in-flight request. For the same
		// Profile, the request's filter also caps the visibility it can gain.
		policy.filter.Revealed = policy.filter.Revealed && policy.session.Revealed
	}
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
	}
	if err := policy.checkAccess(ctx, tx, auth.Now()); err != nil {
		return queueWritePolicy{}, err
	}
	if auth.trustedFilter {
		return policy, nil
	}
	policy.filter.GrantedMediaIDs = nil
	if profile.Kind == ProfileManaged {
		policy.filter.GrantedMediaIDs, err = readGrantedMediaIDs(ctx, tx, policy.profileID)
		if err != nil {
			return queueWritePolicy{}, err
		}
	}
	policy.filter = queueProfileFilter(profile, policy.rebound, policy.filter)
	policy.filter.Certification = auth.ResolveCertification(profile.CertificationCeilings)
	return policy, nil
}

func queueProfileFilter(profile Profile, rebound bool, filter LibraryFilter) LibraryFilter {
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

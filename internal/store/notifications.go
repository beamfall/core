package store

// In-app notification inbox: the polled, client-independent
// first slice of the Core Platform push primitive. CreateNotification is the
// creation seam — the future parental extension-request flow (ADR-0039 §2.1–2.4)
// calls it to drop an "approve more time?" notice into the supervising Profile's
// inbox. APNs/FCM dispatch is a later layer behind a feature flag (see
// PushRegistrar); the inbox works fully without it (clients poll on foreground).
//
// The schema lives in 0047_notification_inbox.sql. Every read here is BOTH
// recipient-gated (a Profile reads only notifications addressed to it) AND
// reveal-filtered (a notification referencing restricted content via library_id
// is invisible to an unrevealed session) — mirroring ActivityEntries
// (activity.go) exactly. A notification with no content reference (library_id
// '') is visible to its recipient regardless of reveal state.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// NotificationKind is a closed-vocabulary classifier for one notification. It is
// a plain string the client maps to a localized label; it is never a domain term
// (invariant 1) and Core never branches on an unknown value. The two launch
// kinds are the parental extension-request notice and a generic system notice.
type NotificationKind string

const (
	// NotificationAccessRequest is the parental "child wants to watch a Locked
	// item" notice. The requestAccess mutation creates it for
	// every adult Profile when a managed Profile taps "Ask a parent" on a Locked
	// item. The Ref carries the request id; the adult client fetches details over
	// the authenticated API (invariant #5 — no content in the notification row).
	NotificationAccessRequest NotificationKind = "access-request"
	// NotificationExtensionRequest is the parental "child asked for more time"
	// notice. The future extension-request flow creates it.
	NotificationExtensionRequest NotificationKind = "extension-request"
	// NotificationSystem is a generic server/system notice (import job done,
	// new content available — the non-urgent inbox traffic of ADR-0039 §2.5).
	NotificationSystem NotificationKind = "system"
	// NotificationWatchPartyInvite is a Watch Party invitation:
	// the host invited this Profile to a synchronized-playback party. The Ref
	// carries the ephemeral party id the invitee round-trips back on accept; Core
	// never interprets it. The notification's LibraryID stays empty so the invite
	// reaches the recipient regardless of reveal state — the Reveal intersection
	// (§2.3) is enforced on ACCEPT against the accepting session's own filter, not
	// by hiding the invite.
	NotificationWatchPartyInvite NotificationKind = "watch-party-invite"
)

// Notification is one inbox row addressed to a single recipient Profile.
// LibraryID is the reveal anchor (empty = no content reference). Ref is an
// opaque originating-object id (e.g. an ExtensionRequest id) the client
// round-trips; Core never interprets it. ReadAt is zero when the notification is
// unread.
type Notification struct {
	ID                 string
	RecipientProfileID ProfileID
	Kind               NotificationKind
	Title              string
	Body               string
	LibraryID          LibraryID
	Ref                string
	CreatedAt          time.Time
	ReadAt             time.Time
}

// CreateNotification inserts one inbox notification for a recipient Profile. It
// is the primitive the parental extension-request flow (ADR-0039 §2.1–2.4) will
// call to notify a supervising Profile — that approve/deny layer is not built
// here; this is just the durable drop point.
//
// id, recipient, kind, and title are required. CreatedAt defaults to now when
// zero. A new notification is always unread (read_at_ms NULL). Inserting a
// notification does NOT reveal-check the LibraryID — a flow may legitimately
// address content the recipient hasn't revealed yet; the reveal filter is
// enforced on READ (ListNotifications / UnreadNotificationCount), so a restricted
// notification simply stays hidden until the recipient reveals.
func (s *Store) CreateNotification(ctx context.Context, n Notification) error {
	if strings.TrimSpace(n.ID) == "" {
		return ValidationError("notification ID is required")
	}
	if n.RecipientProfileID == "" {
		return ValidationError("recipient profile ID is required")
	}
	if n.Kind == "" {
		return ValidationError("notification kind is required")
	}
	if strings.TrimSpace(n.Title) == "" {
		return ValidationError("notification title is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	createdMS := time.Now().UTC().UnixMilli()
	if !n.CreatedAt.IsZero() {
		createdMS = n.CreatedAt.UTC().UnixMilli()
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO notification
			(id, recipient_profile_id, kind, title, body, library_id, ref, created_at_ms, read_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, n.ID, string(n.RecipientProfileID), string(n.Kind), n.Title, n.Body,
		string(n.LibraryID), n.Ref, createdMS)
	return err
}

// ListNotifications returns the recipient Profile's inbox, newest first, both
// recipient-gated and reveal-filtered. unreadOnly restricts the page to unread
// (read_at_ms IS NULL) notifications.
//
// Reveal filtering mirrors ActivityEntries exactly: a notification whose
// library_id names a Library the caller cannot currently see (restricted +
// unrevealed, or outside the allowed set, or a Library that no longer exists) is
// omitted entirely — for that reader the inbox reads as if it never existed.
// Notifications with no content reference (library_id ”) are always visible to
// their recipient. Filtering happens in the WHERE clause before LIMIT, so page
// boundaries leak nothing.
func (s *Store) ListNotifications(ctx context.Context, recipient ProfileID, filter LibraryFilter, unreadOnly bool, limit int) ([]Notification, error) {
	if recipient == "" {
		return nil, ValidationError("recipient profile ID is required")
	}
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	where, args, err := s.notificationVisibilityClause(ctx, recipient, filter)
	if err != nil {
		return nil, err
	}
	if unreadOnly {
		where += " AND read_at_ms IS NULL"
	}
	args = append(args, limit)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, recipient_profile_id, kind, title, body, library_id, ref, created_at_ms, read_at_ms
		FROM notification
		WHERE `+where+`
		ORDER BY created_at_ms DESC, id DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NotificationByID returns one visible notification for recipient. Unknown,
// other-Profile, and reveal-hidden notifications all return ok=false so callers
// cannot use the id as an existence oracle.
func (s *Store) NotificationByID(ctx context.Context, id string, recipient ProfileID, filter LibraryFilter) (Notification, bool, error) {
	if strings.TrimSpace(id) == "" {
		return Notification{}, false, ValidationError("notification ID is required")
	}
	if recipient == "" {
		return Notification{}, false, ValidationError("recipient profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Notification{}, false, err
	}
	where, args, err := s.notificationVisibilityClause(ctx, recipient, filter)
	if err != nil {
		return Notification{}, false, err
	}
	args = append(args, id)
	n, err := scanNotification(s.catalogR.QueryRowContext(ctx, `
		SELECT id, recipient_profile_id, kind, title, body, library_id, ref, created_at_ms, read_at_ms
		FROM notification
		WHERE `+where+` AND id = ?`, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Notification{}, false, nil
		}
		return Notification{}, false, err
	}
	return n, true, nil
}

// UnreadNotificationCount returns the count of unread notifications the
// recipient Profile may currently see — recipient-gated AND reveal-filtered the
// same way ListNotifications is, so a hidden restricted notification never
// inflates the badge for an unrevealed session.
func (s *Store) UnreadNotificationCount(ctx context.Context, recipient ProfileID, filter LibraryFilter) (int, error) {
	if recipient == "" {
		return 0, ValidationError("recipient profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	where, args, err := s.notificationVisibilityClause(ctx, recipient, filter)
	if err != nil {
		return 0, err
	}
	where += " AND read_at_ms IS NULL"
	var count int
	if err := s.catalogR.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notification WHERE `+where, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// MarkNotificationRead marks one notification read, but only if it both belongs
// to the recipient Profile AND is currently visible to it under the reveal
// filter. A notification the caller cannot see is reported as not found, never
// mutated — the mutation is never an existence oracle for hidden content, and a
// Profile can never mark another Profile's notification read. Marking an
// already-read notification is idempotent (read_at_ms is left at its first
// value). The boolean reports whether a row was updated.
func (s *Store) MarkNotificationRead(ctx context.Context, id string, recipient ProfileID, filter LibraryFilter) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, ValidationError("notification ID is required")
	}
	if recipient == "" {
		return false, ValidationError("recipient profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	// Resolve the notification's recipient + library in one read, then apply the
	// recipient gate and reveal filter before any write. An invisible or
	// other-Profile notification is "not found" — no row is touched.
	var owner, libraryID string
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT recipient_profile_id, library_id FROM notification WHERE id = ?`, id,
	).Scan(&owner, &libraryID)
	if err != nil {
		return false, nil // unknown id — not found, idempotent no-op
	}
	if ProfileID(owner) != recipient {
		return false, nil // addressed to another Profile — not this caller's to read
	}
	visible, err := s.notificationLibraryVisible(ctx, LibraryID(libraryID), filter)
	if err != nil {
		return false, err
	}
	if !visible {
		return false, nil // references content the caller cannot see — stay blind
	}
	if _, err := s.catalogW.ExecContext(ctx,
		`UPDATE notification SET read_at_ms = ? WHERE id = ? AND read_at_ms IS NULL`,
		time.Now().UTC().UnixMilli(), id); err != nil {
		return false, err
	}
	// The notification is owned, visible, and now read (whether this call marked
	// it or it was already read) — report found-and-read, idempotently.
	return true, nil
}

// notificationVisibilityClause builds the shared WHERE fragment that scopes a
// notification read to one recipient AND the caller's visible Libraries. It
// mirrors ActivityEntries' clause: the recipient gate is an equality, the reveal
// gate is "library_id = ” OR library_id IN (visible…)". The empty-library
// branch keeps content-free system notices visible regardless of reveal state;
// a notification naming a Library the caller cannot see is excluded fail-closed.
func (s *Store) notificationVisibilityClause(ctx context.Context, recipient ProfileID, filter LibraryFilter) (string, []any, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return "", nil, err
	}
	clauses := []string{"library_id = ''"}
	args := []any{string(recipient)}
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			clauses = append(clauses, "library_id = ?")
			args = append(args, library.ID)
		}
	}
	where := "recipient_profile_id = ? AND (" + strings.Join(clauses, " OR ") + ")"
	return where, args, nil
}

// notificationLibraryVisible reports whether a notification's library_id is
// visible to the caller under filter. The empty library_id (no content
// reference) is always visible; a named Library that no longer exists is hidden
// fail-closed, same as the read path.
func (s *Store) notificationLibraryVisible(ctx context.Context, libraryID LibraryID, filter LibraryFilter) (bool, error) {
	if libraryID == "" {
		return true, nil
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return false, err
	}
	for _, library := range libraries {
		if library.ID == libraryID {
			return libraryVisible(library, filter), nil
		}
	}
	return false, nil // names a Library that no longer exists — fail closed
}

// rowScanner abstracts *sql.Row / *sql.Rows for scanNotification.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNotification(row rowScanner) (Notification, error) {
	var n Notification
	var recipient, kind, libraryID string
	var createdMS int64
	var readMS sql.NullInt64
	if err := row.Scan(&n.ID, &recipient, &kind, &n.Title, &n.Body, &libraryID, &n.Ref, &createdMS, &readMS); err != nil {
		return Notification{}, err
	}
	n.RecipientProfileID = ProfileID(recipient)
	n.Kind = NotificationKind(kind)
	n.LibraryID = LibraryID(libraryID)
	n.CreatedAt = time.UnixMilli(createdMS).UTC()
	if readMS.Valid {
		n.ReadAt = time.UnixMilli(readMS.Int64).UTC()
	}
	return n, nil
}

package graph

import (
	"context"
	"errors"

	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/store"
)

// Server-localized text in the graph API (CORE-2102). Everything else the
// graph says in words is a stable English message with an extensions.code
// that clients branch on; see docs/architecture/server-text-localization.md.

// libraryRootKeys maps each folder-check refusal to its catalog entry.
var libraryRootKeys = map[store.LibraryRootReason]l10n.Key{
	store.LibraryRootMissing:    l10n.LibraryRootMissing,
	store.LibraryRootCantOpen:   l10n.LibraryRootCantOpen,
	store.LibraryRootNotFolder:  l10n.LibraryRootNotFolder,
	store.LibraryRootUnreadable: l10n.LibraryRootUnreadable,
}

// localizeLibraryRootError lets graphFailMessage render a folder-check refusal
// in the request locale. The error still unwraps to the English
// ValidationError, so its code stays INVALID.
func localizeLibraryRootError(err error) error {
	var refusal *store.LibraryRootError
	if !errors.As(err, &refusal) {
		return err
	}
	key, ok := libraryRootKeys[refusal.Reason]
	if !ok {
		return err
	}
	return &l10n.Error{Key: key, Args: l10n.Args{"path": refusal.Path}, Err: err}
}

// notificationTitleKeys maps each inbox notification kind the server creates
// to its title. A kind without an entry keeps its stored title.
var notificationTitleKeys = map[store.NotificationKind]l10n.Key{
	store.NotificationAccessRequest:     l10n.NotificationAccessRequestTitle,
	store.NotificationExtensionRequest:  l10n.NotificationExtensionRequestTitle,
	store.NotificationWatchPartyInvite:  l10n.NotificationWatchPartyInviteTitle,
	store.NotificationWatchPartyRequest: l10n.NotificationWatchPartySeekTitle,
}

// storedNotificationTitle is the source-locale title saved with a new
// notification, for readers that have no request locale (exports, webhooks).
func storedNotificationTitle(kind store.NotificationKind) string {
	return l10n.Text(l10n.Source(), notificationTitleKeys[kind], nil)
}

// notificationTitle renders a notification's title in the request locale.
func notificationTitle(ctx context.Context, n store.Notification) string {
	key, ok := notificationTitleKeys[n.Kind]
	if !ok {
		return n.Title
	}
	return l10n.Text(l10n.FromContext(ctx), key, nil)
}

// legacyParticipantFallback is the fallback member name older servers stored in
// a seek request's body; it renders like an empty body.
const legacyParticipantFallback = "Participant"

// notificationBody renders a notification's body in the request locale. Only
// a seek request without a member name has server text in its body.
func notificationBody(ctx context.Context, n store.Notification) string {
	if n.Kind == store.NotificationWatchPartyRequest && (n.Body == "" || n.Body == legacyParticipantFallback) {
		return participantFallback(ctx)
	}
	return n.Body
}

// participantFallback is the non-identifying label for a Watch Party member
// whose Profile name is unknown (ADR-0111 §1).
func participantFallback(ctx context.Context) string {
	return l10n.Text(l10n.FromContext(ctx), l10n.WatchPartyParticipantFallback, nil)
}

package l10n

// Key names one catalog entry. Every Key declared here has an entry in
// catalog/en.json and in every shipped locale's catalog, and every catalog
// entry has a Key here; TestCatalogKeysMatchDeclaredKeys enforces both.
type Key string

// Notification inbox titles, rendered per request from the notification kind.
const (
	NotificationAccessRequestTitle    Key = "notification.access_request.title"
	NotificationExtensionRequestTitle Key = "notification.extension_request.title"
	NotificationWatchPartyInviteTitle Key = "notification.watch_party_invite.title"
	NotificationWatchPartySeekTitle   Key = "notification.watch_party_request.title"
	WatchPartyParticipantFallback     Key = "watch_party.participant_fallback"
)

// Library folder check refusals (CORE-1598), shown next to the folder field.
const (
	LibraryRootMissing    Key = "library_root.missing"
	LibraryRootCantOpen   Key = "library_root.cant_open"
	LibraryRootNotFolder  Key = "library_root.not_folder"
	LibraryRootUnreadable Key = "library_root.unreadable"
)

// Setup and sign-in hints sent with the lan_hostname_not_allowed code (CORE-1600).
const (
	SetupLANHintFinishSetup Key = "setup.lan_hint.finish_setup"
	SetupLANHintSignIn      Key = "setup.lan_hint.sign_in"
)

// Default navigation and Home shelf titles in the client manifest.
const (
	ManifestNavHome               Key = "manifest.nav.home"
	ManifestNavAmbient            Key = "manifest.nav.ambient"
	ManifestShelfContinueWatching Key = "manifest.shelf.continue_watching"
	ManifestShelfWatchlist        Key = "manifest.shelf.watchlist"
	ManifestShelfRecentlyAdded    Key = "manifest.shelf.recently_added"
	ManifestShelfMostPlayed       Key = "manifest.shelf.most_played"
)

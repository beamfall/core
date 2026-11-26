package l10n

// Key names one catalog entry. Every Key declared here has an entry in
// catalog/en.json and in every shipped locale's catalog, and every catalog
// entry has a Key here; TestCatalogKeysMatchDeclaredKeys enforces both.
type Key string

const WorkflowCapabilityDisabled Key = "workflow.capability_disabled"

const (
	FeedSearchInvalidURL Key = "feed_search.invalid_url"
	FeedSearchThrottled  Key = "feed_search.throttled"
)

const (
	InvalidMediaRelation         Key = "media_relation.invalid_input"
	OperatorRelationUndoConflict Key = "media_relation.undo_conflict"
)

const QueueMediaLocked Key = "queue.media_locked"

const (
	FixMatchEncryptedLibrary     Key = "fix_match.encrypted_library"
	FixMatchInvalidSequence      Key = "fix_match.invalid_sequence"
	FixMatchInvalidIndex         Key = "fix_match.invalid_index"
	FixMatchNoChildListing       Key = "fix_match.no_child_listing"
	FixMatchNoChildLevel         Key = "fix_match.no_child_level"
	FixMatchInvalidCandidate     Key = "fix_match.invalid_candidate"
	FixMatchExtraParent          Key = "fix_match.extra_parent"
	FixMatchMissingParent        Key = "fix_match.missing_parent"
	FixMatchInvalidParent        Key = "fix_match.invalid_parent"
	FixMatchUnknownParent        Key = "fix_match.unknown_parent"
	FixMatchAmbiguousParent      Key = "fix_match.ambiguous_parent"
	FixMatchInvalidParentOrdinal Key = "fix_match.invalid_parent_ordinal"
)

// Notification inbox titles, rendered per request from the notification kind.
const (
	NotificationAccessRequestTitle    Key = "notification.access_request.title"
	NotificationExtensionRequestTitle Key = "notification.extension_request.title"
	NotificationWatchPartyInviteTitle Key = "notification.watch_party_invite.title"
	NotificationWatchPartySeekTitle   Key = "notification.watch_party_request.title"
	WatchPartyParticipantFallback     Key = "watch_party.participant_fallback"
)

// Library folder check refusals (CORE-1598, CORE-1202), shown next to the folder field.
const (
	LibraryRootMissing    Key = "library_root.missing"
	LibraryRootCantOpen   Key = "library_root.cant_open"
	LibraryRootNotFolder  Key = "library_root.not_folder"
	LibraryRootUnreadable Key = "library_root.unreadable"
	LibraryRootOverlaps   Key = "library_root.overlaps"
)

// Setup and sign-in hints sent with the lan_hostname_not_allowed code (CORE-1600).
const (
	SetupLANHintFinishSetup Key = "setup.lan_hint.finish_setup"
	SetupLANHintSignIn      Key = "setup.lan_hint.sign_in"
)

// Home shelf titles added after CORE-2102, rendered into every shipped locale
// of the manifest's LocaleString (CORE-2822).
const ManifestShelfNextUp Key = "manifest.shelf.next_up"
const ManifestShelfContinueListening Key = "manifest.shelf.continue_listening"

// Station explanations (ADR-0098 area 5, CORE-2772): why an item joined a
// generated Station, and the Station-level scarcity note.
const (
	StationReasonSeed     Key = "station.reason.seed"
	StationReasonFrom     Key = "station.reason.from"
	StationReasonShared   Key = "station.reason.shared"
	StationReasonCategory Key = "station.reason.category"
	StationReasonEra      Key = "station.reason.era"
	StationReasonPlayed   Key = "station.reason.played"
	StationReasonPicked   Key = "station.reason.picked"
	StationReasonSonic    Key = "station.reason.sonic"
	StationEmpty          Key = "station.empty"
	StationShort          Key = "station.short"
)

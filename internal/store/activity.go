package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Activity actions (ADR-0012 §§3/11 and the ADR-0007 §3 promotion path):
// security- and curation-relevant mutations plus the failed-attempt audit
// events. A closed vocabulary — new actions join here, not at call sites.
const (
	// ActivityMutationAttempt/Finished form the request-level Graph mutation
	// lifecycle. Attempt is durable and fail-closed before dispatch. Finished is
	// best-effort after dispatch: a missing row means the outcome is explicitly
	// uncertain, while an audit outage can never falsify a committed response.
	// Finished rows are hidden from normal activity pages so the operation-
	// specific audit row remains the visible completion record.
	ActivityMutationAttempt  = "mutation.attempt"
	ActivityMutationFinished = "mutation.finished"
	// ActivityCryptoLifecyclePrepared is the durable, operation-specific intent
	// written before a secure-store key commit. It never claims the key change
	// completed; request-level mutation.finished and recovered key state carry
	// the eventual outcome.
	ActivityCryptoLifecyclePrepared = "crypto.lifecycle.prepared"
	ActivityReveal                  = "reveal"
	ActivityRevealDenied            = "reveal.denied"
	ActivityRelock                  = "relock"
	ActivityProfileSwitch           = "profile.switch"
	ActivityProfileLibrary          = "profile.library"
	ActivityLibraryDelete           = "library.delete"
	ActivityMediaTrash              = "media.trash"
	ActivityMediaRestore            = "media.restore"
	ActivityMarkerDelete            = "marker.delete"
	ActivityCollectionTrash         = "collection.trash"
	ActivityChannelDelete           = "channel.delete"
	ActivitySavedFilterDelete       = "saved_filter.delete"
	ActivitySmartCollectionDelete   = "smart_collection.delete"
	ActivitySavedStationDelete      = "saved_station.delete"
	ActivityRetentionRuleDelete     = "retention_rule.delete"
	ActivityScrobbleUnlink          = "scrobble.unlink"
	ActivityOfflinePackTokensDelete = "offline_pack_tokens.delete"
	// ActivityTrashPurge records an irreversible retained-trash purge. It names
	// no specific Media because the purged rows are gone and may span Libraries;
	// the mutation result carries only the affected count.
	ActivityTrashPurge = "trash.purge"
	// ActivityAITagAccept audits the operator approving a staged auto-tag into
	// canonical metadata (ADR-0041 §2.5 — the human gate). TargetID is the
	// pending-tag id; the promoted value is read back from the Media, not
	// memorialized here. Reject is a discard and is not audited (it changes no
	// canonical state).
	ActivityAITagAccept         = "media.ai_tag_accept"
	ActivityArtworkPin          = "artwork.pin"
	ActivityLibraryRoots        = "library.roots"
	ActivityPathPickerRootAdded = "path_picker.root_added"
	ActivityLibraryPreviewClips = "library.preview_clips"
	ActivityLibraryRestriction  = "library.restriction"
	// ActivityAccessSchedule audits a managed Profile's access-schedule write.
	// It carries the Profile id in TargetID only —
	// never the window contents.
	ActivityAccessSchedule               = "profile.access_schedule"
	ActivitySubscriptionRetention        = "subscription.retention"
	ActivitySubscriptionEntryAssociation = "subscription.entry_association"
	// ActivityRetentionTrash audits an autonomous retention rule routing a Media
	// item through recoverable Trash. TargetType is "retention_rule", TargetID
	// is the rule id, and LibraryID is the target Library for reveal-filtered
	// audit visibility. The Source expression and Profile keep identities are
	// deliberately not logged.
	ActivityRetentionTrash = "retention.trash"
	// ActivityConfigUpdate audits a Console config write. The entry carries the
	// config domain in TargetType ("console", "transcode", "quality-policy",
	// "library-backing", or an encrypted-library lifecycle label) and never any
	// value — it records that a change occurred, not the secret-free settings
	// themselves (those are read back from the config resolvers).
	ActivityConfigUpdate = "config.update"
	// ActivityPluginActivation audits a Console plugin activate/deactivate.
	// The plugin id is operator-facing config, not
	// content, so it rides TargetID with no LibraryID.
	ActivityPluginActivation = "plugin.activation"
	// ActivityPluginUninstall audits the explicit irreversible plugin data purge.
	// TargetID is the plugin id; TargetType carries the purged Entity count.
	ActivityPluginUninstall = "plugin.uninstall"
	// ActivityCapabilityToggle audits a Console/Manager Workflow-capability
	// enable/disable. The capability id rides TargetID with
	// TargetType "capability" and no LibraryID; the flip is an attributed
	// Admin-Session mutation, so a newly-flipped capability has a runtime trace.
	ActivityCapabilityToggle = "capability.toggle"
	// Integration Worker runtime audit actions. All carry the plugin
	// id in TargetID with TargetType "plugin" and never any secret/content.
	// ActivityPluginGrant audits a permission grant approval/revocation or a
	// re-approval forced by a permission-expanding update.
	ActivityPluginGrant = "plugin.grant"
	// ActivityPluginUpdate audits a worker update lifecycle step (stage/apply),
	// including the verified artifact hash transition recorded on the worker row.
	ActivityPluginUpdate = "plugin.update"
	// ActivityPluginCatalogRead audits a high-risk read-catalog Host API call.
	ActivityPluginCatalogRead = "plugin.catalog_read"
	// ActivityPluginIngestRequest audits a high-risk request-ingest handoff.
	ActivityPluginIngestRequest = "plugin.ingest_request"
	// ActivityPluginFileMutationRequest audits a high-risk file-mutation request
	// a worker submits through the audited mutation path (never a direct write).
	ActivityPluginFileMutationRequest = "plugin.file_mutation_request"
	// ActivityPluginProfileLink audits a high-risk per-Profile account-link
	// write made through the Integration Host API. TargetID is the plugin id;
	// the target Profile is recorded separately in ProfileID.
	ActivityPluginProfileLink = "plugin.profile_link"
	// ActivityPluginOutboundRequest audits an Integration Worker outbound HTTP
	// call carried through the enforced Host proxy. TargetID is the plugin id; the
	// URL/host/headers/body are never written to the row (they may carry secrets).
	ActivityPluginOutboundRequest = "plugin.outbound_request"
	// ActivityPluginDeadLetterRetry audits an operator-requested dead-letter
	// retry lifecycle action.
	ActivityPluginDeadLetterRetry = "plugin.dead_letter_retry"
	ActivityScheduleUpdate        = "schedule.update"
	ActivityTokenCreate           = "token.create"
	ActivityTokenRevoke           = "token.revoke"
	// ActivityKeyCreate / ActivityKeyRevoke audit scoped API key lifecycle,
	// distinct from the ADR-0013 admin token events. Neither
	// carries the key secret.
	ActivityKeyCreate     = "key.create"
	ActivityKeyRevoke     = "key.revoke"
	ActivityPairingCode   = "pairing.code"
	ActivityEnroll        = "device.enroll"
	ActivityEnrollDenied  = "enroll.denied"
	ActivityPromote       = "device.promote"
	ActivityPromoteDenied = "promote.denied"
	// ActivityAdminLogin / ActivityAdminLoginDenied audit Admin Session
	// re-auth — a failed admin login is a security
	// signal surfaced in the Console. Neither entry ever carries the password.
	ActivityAdminLogin       = "admin.login"
	ActivityAdminLoginDenied = "admin.login.denied"
	// ActivityMyDataDelete records a My Data engagement deletion.
	// The entry carries the SCOPE KIND only (in TargetType: "all" or "media")
	// and NEVER the deleted media id — the activity log must not memorialize
	// what the user deleted, or it would defeat the deletion. It carries no
	// LibraryID either, so it is visible to the user across reveal state (the
	// deletion is the user's own act on their own data).
	ActivityMyDataDelete = "mydata.delete"
	// ActivityOperatorDataDelete records a Console operator (or managed-Profile
	// guardian) deleting a TARGET Profile's engagement footprint (ADR-0036
	// §2.5/§2.6 — the Admin-Session-gated parallel to ActivityMyDataDelete). The
	// scope.ProfileID is the operator who acted; the deleted Profile's id rides
	// TargetID with TargetType "profile". Unlike the self-delete it DOES name the
	// target Profile id — this is an operator audit trail (who deleted whose
	// data, GDPR/offboarding evidence), not the subject's own privacy view.
	ActivityOperatorDataDelete = "mydata.operator_delete"
	// ActivityOwnerTransfer records an Admin-Session-gated transfer of the single
	// owner flag to another adult Profile. scope.ProfileID is the
	// acting (current) owner; the new owner's Profile id rides TargetID with
	// TargetType "profile". No LibraryID — ownership is not Library content.
	ActivityOwnerTransfer = "profile.owner_transfer"
	// ActivitySessionTerminate records an operator killing an active session
	// from the Console live-sessions dashboard. TargetID is the
	// terminated session id; no LibraryID (a session is not Library content).
	ActivitySessionTerminate = "session.terminate"
	// ActivityDeviceRevoke records an operator revoking a device key (ADR-0012
	// §7). TargetType is "device", TargetID the revoked device id; no LibraryID
	// (a device is not Library content). Revocation propagates to the device's
	// live transports — its open subscription/event streams self-terminate on
	// the next per-event scope re-evaluation, and its in-flight transcodes are
	// aborted at once through the playback termination seam.
	ActivityDeviceRevoke = "device.revoke"
	ActivityDeviceRole   = "device.role"
	// ActivitySessionControl records a cross-device transport command (ADR-0035
	// §7): a session-authenticated device, admin API token, or scoped API key
	// controlled an active session. TargetType is "session", TargetID the
	// controlled session id, and ProfileID anchors the TARGET session's Profile;
	// actor attribution rides SessionID/DeviceID, TokenID, or APIKeyID. The
	// specific verb is ephemeral transport and not memorialized. It carries no
	// LibraryID — a session is not Library content, so the entry is visible to
	// the target Profile across reveal state like the My-Data session reads.
	ActivitySessionControl = "session.control"
	// ActivityGroupControl records an Ambient Group Conductor membership or
	// transport action: a device formed/joined/left a group, or
	// drove a group transition (skip/track-end/pause-resume) or re-sync.
	// TargetType is "group", TargetID the ephemeral group id. Like
	// session.control it carries no LibraryID — group membership is in-memory,
	// LAN-only, same-Profile by construction — and the audit anchor is "device
	// acted on group G", the specific signal being ephemeral sync.
	ActivityGroupControl = "group.control"
	// Bulk dry-run / batch-undo envelope: one batch = one
	// activity entry = one undo unit. Each entry's TargetID is the op-batch id
	// (the undo handle), TargetType is "batch". The batch spans many Media and
	// possibly many Libraries, so no single LibraryID is recorded — the journal
	// holds the per-item detail, the log holds the audit anchor.
	ActivityMediaBulkEdit      = "media.bulk_edit"
	ActivityMediaOrganize      = "media.organize"
	ActivityMediaMerge         = "media.merge"
	ActivityManagedFileCreate  = "managed_file.create"
	ActivityManagedFileMove    = "managed_file.move"
	ActivityManagedFileDelete  = "managed_file.delete"
	ActivityManagedFileRestore = "managed_file.restore"
	// ActivityEntityMerge audits an Entity merge: the
	// loser folded into the winner and trashed, as one batch = one undo unit.
	// TargetID is the op-batch id (the undo handle), TargetType "batch" — same
	// audit-anchor shape as the Media batch ops; the journal holds the detail.
	ActivityEntityMerge = "entity.merge"
	ActivityBatchUndo   = "batch.undo"
	// ActivityMediaFieldLock audits a per-field source-precedence lock change
	// from the Console. TargetID is the Media id and
	// the entry carries the Media's LibraryID for reveal-filtered visibility;
	// it records that a lock toggled, never the field value behind it.
	ActivityMediaFieldLock = "media.field_lock"
	ActivityTagCreate      = "tag.create"
	ActivityTagUpdate      = "tag.update"
	ActivityTagDelete      = "tag.delete"
	ActivityTagRestore     = "tag.restore"
	ActivityTagAssign      = "tag.assign"
	ActivityTagBulkAssign  = "tag.bulk_assign"
	// Generic curation relation writes (ADR-0001/0061): relation names remain
	// plugin/core vocabulary and no relation payload is stored in the activity
	// row. TargetID anchors the source object; LibraryID preserves Reveal
	// filtering for content-level audit visibility.
	ActivityEntityRelation = "entity.relation"
	ActivityMediaRelation  = "media.relation"
	// ActivityAccessRequest records a managed Profile raising an access request
	// for a Locked item. TargetType is "access_request", TargetID
	// the request id. No LibraryID — the request names no content beyond the
	// opaque media id (invariant #5).
	ActivityAccessRequest = "access.request"
	// ActivityAccessResolve records an adult Profile resolving (granting, denying,
	// or suppressing) an access request. scope.ProfileID is the
	// responding adult; TargetType is "access_request", TargetID the request id.
	ActivityAccessResolve = "access.resolve"
	// ActivityExtensionRequest records a managed Profile raising a parental
	// extension request. TargetType is "extension",
	// TargetID the request id. No LibraryID — the request names no content (only
	// an opaque trigger source, invariant #5).
	ActivityExtensionRequest = "extension.request"
	// ActivityExtensionResponse records an adult Profile approving or denying an
	// extension request. The scope.ProfileID is the responding
	// adult; TargetType is "extension", TargetID the request id. The audit anchor
	// is "adult P resolved request R"; the approve/deny outcome rides the request
	// row, not memorialized here.
	ActivityExtensionResponse = "extension.response"
	// ActivityExtensionPreauth records an adult toggling a managed Profile's
	// §2.4 pre-authorization. TargetType is "profile", TargetID the managed
	// Profile id; the on/off value rides the preauth row, not the audit entry.
	ActivityExtensionPreauth = "extension.preauth"
)

// ActivityEntry is one append-only activity-log record. The
// shape is deliberately closed — fixed identifier columns, no free-form
// payload — so an entry can never carry a PIN, pairing code, token, or other
// secret. Entries whose target lives in a Library carry that LibraryID; the
// read path hides such entries from sessions that cannot see the Library
// (hidden == missing, never an existence oracle).
type ActivityEntry struct {
	ID int64
	// OperationID is an opaque server-issued idempotency key. Empty keeps the
	// ordinary append-only behavior; a non-empty value makes retries converge on
	// one durable row without updating or deleting prior audit history.
	OperationID string
	OccurredAt  time.Time
	Action      string
	SessionID   SessionID
	DeviceID    DeviceID
	ProfileID   ProfileID
	TargetType  string
	TargetID    string
	LibraryID   LibraryID
	// TokenID attributes mutations that arrived with an API token instead
	// of a session.
	TokenID string
	// APIKeyID attributes mutations that arrived with a scoped developer API
	// key instead of a session or admin API token. It stores
	// only the key id, never secret material.
	APIKeyID string
}

// ActivityQuery scopes an activity-log read. Filter applies the caller's
// reveal/Library visibility; a non-empty ProfileID additionally restricts the
// page to that Profile's own entries (the non-manager "self" scope).
type ActivityQuery struct {
	Filter                   LibraryFilter
	ProfileID                ProfileID
	Limit                    int
	Offset                   int
	IncludeMutationLifecycle bool
}

// AppendActivity appends one activity-log entry. There is no update or
// delete counterpart — the log is append-only by API shape, and the schema
// triggers abort any UPDATE/DELETE that reaches SQLite anyway.
func (s *Store) AppendActivity(ctx context.Context, entry ActivityEntry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return appendActivitySQL(ctx, s.eventsW, entry)
}

func validateActivityEntry(entry *ActivityEntry) error {
	if strings.TrimSpace(entry.Action) == "" {
		return ValidationError("activity action is required")
	}
	if entry.OccurredAt.IsZero() {
		return ValidationError("activity time is required")
	}
	entry.Action = strings.TrimSpace(entry.Action)
	entry.OccurredAt = normalizeTime(entry.OccurredAt)
	return nil
}

func appendActivitySQL(ctx context.Context, exec sqlExecer, entry ActivityEntry) error {
	if err := validateActivityEntry(&entry); err != nil {
		return err
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO activity_entry(operation_id, occurred_at_ms, action, session_id, device_id, profile_id, target_type, target_id, library_id, token_id, api_key_id)
		VALUES (NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(operation_id) WHERE operation_id IS NOT NULL DO NOTHING`,
		entry.OperationID, timeToMillis(entry.OccurredAt), entry.Action, entry.SessionID, entry.DeviceID,
		entry.ProfileID, entry.TargetType, entry.TargetID, entry.LibraryID, entry.TokenID, entry.APIKeyID)
	return err
}

// ActivityEntries returns activity-log entries newest first, reveal-filtered:
// an entry that references content in a Library the caller
// cannot see is omitted entirely — for that reader the page reads exactly as
// if the entry never existed. Filtering happens before paging, so page
// boundaries leak nothing either. Entries with no Library reference
// (reveal/relock, profile switches, pairing) are visible to every entitled
// reader of the page.
func (s *Store) ActivityEntries(ctx context.Context, query ActivityQuery) ([]ActivityEntry, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	// An entry naming a Library that no longer exists stays hidden too:
	// fail-closed, same as a Library outside the caller's allowed set.
	clauses := []string{"library_id = ''"}
	args := []any{}
	for _, library := range libraries {
		if libraryVisible(library, query.Filter) {
			clauses = append(clauses, "library_id = ?")
			args = append(args, library.ID)
		}
	}
	where := "(" + strings.Join(clauses, " OR ") + ")"
	if query.ProfileID != "" {
		where += " AND profile_id = ?"
		args = append(args, query.ProfileID)
	}
	if !query.IncludeMutationLifecycle {
		where += " AND action NOT IN (?, ?)"
		args = append(args, ActivityMutationAttempt, ActivityMutationFinished)
	}
	args = append(args, limit, offset)
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT id, operation_id, occurred_at_ms, action, session_id, device_id, profile_id, target_type, target_id, library_id, token_id, api_key_id
		FROM activity_entry
		WHERE `+where+`
		ORDER BY id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []ActivityEntry
	for rows.Next() {
		var entry ActivityEntry
		var operationID sql.NullString
		var occurredAt int64
		if err := rows.Scan(&entry.ID, &operationID, &occurredAt, &entry.Action, &entry.SessionID,
			&entry.DeviceID, &entry.ProfileID, &entry.TargetType, &entry.TargetID, &entry.LibraryID, &entry.TokenID, &entry.APIKeyID); err != nil {
			return nil, err
		}
		entry.OperationID = operationID.String
		entry.OccurredAt = millisToTime(occurredAt)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// ActivityByID returns the single activity entry with the given numeric id,
// applying the same reveal-filtered visibility as ActivityEntries. Returns
// sql.ErrNoRows if the entry does not exist or is hidden by the caller's
// reveal filter.
func (s *Store) ActivityByID(ctx context.Context, id int64, filter LibraryFilter) (ActivityEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ActivityEntry{}, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return ActivityEntry{}, err
	}
	clauses := []string{"library_id = ''"}
	args := []any{}
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			clauses = append(clauses, "library_id = ?")
			args = append(args, library.ID)
		}
	}
	where := "(" + strings.Join(clauses, " OR ") + ")"
	args = append(args, id)
	var entry ActivityEntry
	var operationID sql.NullString
	var occurredAt int64
	row := s.eventsR.QueryRowContext(ctx, `
		SELECT id, operation_id, occurred_at_ms, action, session_id, device_id, profile_id, target_type, target_id, library_id, token_id, api_key_id
		FROM activity_entry
		WHERE `+where+` AND id = ?`, args...)
	if err := row.Scan(&entry.ID, &operationID, &occurredAt, &entry.Action, &entry.SessionID,
		&entry.DeviceID, &entry.ProfileID, &entry.TargetType, &entry.TargetID, &entry.LibraryID, &entry.TokenID, &entry.APIKeyID); err != nil {
		return ActivityEntry{}, err
	}
	entry.OperationID = operationID.String
	entry.OccurredAt = millisToTime(occurredAt)
	return entry, nil
}

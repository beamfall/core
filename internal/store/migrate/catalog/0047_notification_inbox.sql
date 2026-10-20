-- In-app notification inbox (ADR-0039 §2.5): the polled, client-independent
-- first slice of the Core Platform push primitive. APNs/FCM dispatch is a later
-- layer behind a feature flag; this table is the durable inbox a client reads on
-- foreground, and the creation seam the future parental extension-request flow
-- (ADR-0039 §2.1–2.4) writes to.
--
-- A notification is addressed to exactly one recipient_profile_id; only that
-- Profile's session reads it (recipient-gated). kind is a closed-vocabulary
-- string ('extension-request', 'system', …) the client maps to a label —
-- never a domain term (invariant 1). created_at_ms / read_at_ms are epoch
-- milliseconds; read_at_ms IS NULL means unread.
--
-- library_id is the reveal anchor. It mirrors activity_entry.library_id
-- (0007_activity_log.sql): a notification that references restricted content
-- carries that content's Library, and the inbox read filters by the caller's
-- visible-Library set exactly like the activity log — a restricted notification
-- never reaches an unrevealed session. A notification with no content reference
-- (a plain system notice) carries '' and is visible to its recipient regardless
-- of reveal state. ref is an opaque originating-object id (e.g. the future
-- ExtensionRequest id) the client round-trips; Core never interprets it.
CREATE TABLE notification (
    id                   TEXT NOT NULL PRIMARY KEY,
    recipient_profile_id TEXT NOT NULL,
    kind                 TEXT NOT NULL,
    title                TEXT NOT NULL,
    body                 TEXT NOT NULL DEFAULT '',
    library_id           TEXT NOT NULL DEFAULT '',
    ref                  TEXT NOT NULL DEFAULT '',
    created_at_ms        INTEGER NOT NULL,
    read_at_ms           INTEGER
);

-- The inbox read is "this Profile's notifications, newest first" — index by
-- recipient + created_at so the paged read is a covered range scan, never a
-- full-table sort.
CREATE INDEX notification_recipient_created_idx
    ON notification(recipient_profile_id, created_at_ms);

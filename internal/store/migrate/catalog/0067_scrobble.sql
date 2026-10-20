-- Scrobble Integration Plugin durable consent state (ADR-0049 / SCROBBLE-1).
--
-- The scrobble plugin (internal/scrobble) is the Webhook Emitter's typed sibling
-- (ADR-0049 §2.1): a first-party Integration Plugin that pushes a Profile's
-- watched/played events to a third-party service the Profile personally linked.
-- Like every Integration Plugin it adds NO schema query strings of its own
-- (invariant #3); its durable, NON-SECRET consent state is core-owned here, the
-- same way PUSH-1's device_push_token is core-owned (ADR-0039). The volatile
-- delivery state (the per-event scrobble queue and the dead-letter) is plugin-
-- private JSON on disk, mirroring the Webhook Emitter's dead-letter — never a row
-- here.
--
-- CRUCIAL invariant #5 boundary: NO OAuth token, session key, or API secret ever
-- lands in either table. The link row carries only a non-secret secret_ref — a
-- pointer to the 0600 secure-store file that actually holds the credential
-- (internal/app secretFiles). A My-Data export (ADR-0036 §2.7) reads these rows
-- to show a Profile "where your data goes", and it must be safe to export every
-- column verbatim; the secret_ref is a meaningless handle without the 0600 file,
-- which the export never touches.
--
--   - scrobble_link: one row per (profile, service) the Profile has linked. The
--     consent locus is the Profile (ADR-0049 §2.2): the operator activates the
--     plugin, each Profile links its own account. service is the closed adapter
--     vocabulary value ("trakt"/"lastfm"/"listenbrainz"); secret_ref names the
--     0600 file holding that Profile+service credential. account_label is an
--     optional non-secret display name (e.g. the linked username) for the
--     "Where your data goes" panel; never a credential. linked_at is the link
--     instant. Deleting the Profile cascades the link away (the credential file
--     is cleared by the unlink mutation, never orphaned here).
--   - scrobble_library_eligibility: per-Library opt-in (ADR-0049 §2.3). A
--     Library scrobbles only when an explicit eligible=1 row exists; the ABSENCE
--     of a row is "not eligible", so adult/Restricted Libraries are never
--     eligible by default (ADR-0049 §2.3 / invariant: "adult never default") —
--     they require an explicit operator opt-in row exactly like any other
--     Library, and the plugin additionally refuses to honour an eligibility row
--     for a Restricted Library unless that opt-in was made deliberately. Keyed by
--     library alone (eligibility is service-agnostic: a Library either feeds
--     scrobbling or it does not); the per-service identity match happens later
--     via External IDs.
CREATE TABLE scrobble_link (
  profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  service TEXT NOT NULL,
  secret_ref TEXT NOT NULL,
  account_label TEXT NOT NULL DEFAULT '',
  linked_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, service)
);

CREATE TABLE scrobble_library_eligibility (
  library_id TEXT NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  eligible INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL,
  PRIMARY KEY (library_id)
);

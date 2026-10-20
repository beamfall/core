-- Generic Offline Pack substrate (ADR-0098 area 6). Offline music — and any
-- offline Media — uses the existing Offline Pack regime, not exportable
-- downloads. This is the DOMAIN-AGNOSTIC server spine: pack rows + the frozen
-- resolved Media list. The target is opaque (a kind + ref string) so the
-- generic substrate carries no domain vocabulary; Music target resolvers
-- (Album/Playlist/Track/Station) live in internal/musicpack and translate a
-- target into a visible Media list before any row is written.
--
-- Provider catalogs never enter here (invariant #1/#3): a pack item is always a
-- Beamfall Media row (FK to media), so no provider item can become pack bytes.
CREATE TABLE IF NOT EXISTS offline_pack (
  id             TEXT    NOT NULL PRIMARY KEY,
  profile_id     TEXT    NOT NULL,
  device_id      TEXT    NOT NULL DEFAULT '',
  -- Opaque target identity: the resolver-registry kind ("media", "music-album",
  -- …) and an opaque ref the resolver interprets. No domain meaning here.
  target_kind    TEXT    NOT NULL,
  target_ref     TEXT    NOT NULL DEFAULT '',
  -- pending → building → ready; revoked / expired are terminal denial states.
  state          TEXT    NOT NULL DEFAULT 'pending',
  -- Manifest metadata blob (visible items, artwork slots, generated-asset refs,
  -- Station explanations). Filtered at write time: never tombstone labels,
  -- hidden Library names, or excluded counts. Plaintext in the
  -- integrity-protected catalog this ticket; at-rest encryption is a residual
  -- gated on ENC-3 (the per-Library DEK path).
  manifest_json  TEXT    NOT NULL DEFAULT '',
  -- Reconnect progress sync (ADR-0098): the client resumes against these.
  bytes_total    INTEGER NOT NULL DEFAULT 0,
  bytes_ready    INTEGER NOT NULL DEFAULT 0,
  created_at_ms  INTEGER NOT NULL,
  updated_at_ms  INTEGER NOT NULL,
  expires_at_ms  INTEGER,
  revoked_at_ms  INTEGER
);

CREATE INDEX IF NOT EXISTS offline_pack_profile_idx
  ON offline_pack(profile_id, state, updated_at_ms);

CREATE INDEX IF NOT EXISTS offline_pack_expiry_idx
  ON offline_pack(expires_at_ms);

-- The frozen resolved Media list. Written once at build time (Station packs run
-- their generator ONCE and freeze the batch here; they do not regenerate while
-- offline). ON DELETE CASCADE keeps items bounded to their pack.
CREATE TABLE IF NOT EXISTS offline_pack_item (
  pack_id        TEXT    NOT NULL REFERENCES offline_pack(id) ON DELETE CASCADE,
  media_id       TEXT    NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  sort_order     INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (pack_id, media_id)
);

CREATE INDEX IF NOT EXISTS offline_pack_item_order_idx
  ON offline_pack_item(pack_id, sort_order, media_id);

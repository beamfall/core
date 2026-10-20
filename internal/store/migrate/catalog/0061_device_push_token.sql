-- Persisted device push token (ADR-0039 §2.5 / PUSH-1). The push seam used to
-- be a no-op (push.NoopRegistrar discarded the token); a persisting registrar
-- needs a home for "this enrolled device's current APNs/FCM/Web-Push token" so
-- the dispatcher can later fan a time-sensitive event out to a Profile's
-- devices.
--
-- Keyed on device_id (one current token per enrolled device — ADR-0039 §2.5:
-- "tokens are stored per enrolled-device record and refreshed on app launch").
-- A re-register from the same device REPLACEs the row (the registrar upserts),
-- so a refreshed token never accumulates stale duplicates. profile_id scopes
-- the registration so dispatch can fan out to a Profile's devices.
--
-- The token is NOT a secret in the invariant-#5 sense: it is an opaque,
-- per-device routing handle issued by APNs/FCM/the browser Push service, not an
-- operator credential, and it is required at dispatch time to address the
-- device. The operator credentials that SIGN the dispatch (the APNs .p8 key, the
-- FCM service-account JSON, the VAPID keys) live in the 0600 secure store
-- (secretFiles), never here and never in any catalog row.
--
-- platform is a closed three-value vocabulary (apns | fcm | webpush) matching
-- push.PushPlatform; the registrar validates it before insert. Both ON DELETE
-- CASCADE foreign keys keep the table clean: revoking a device or deleting a
-- Profile drops its token rather than leaving an undeliverable orphan.
--
-- Append-only past the FREEZE-4 marker (ADR-0011 §2): adding this migration is a
-- deliberate act — schemaFreezeCatalogMigrations bumps 58 → 59 in the same
-- commit and `make bench` re-confirms the P95 budgets.
CREATE TABLE IF NOT EXISTS device_push_token (
  device_id     TEXT PRIMARY KEY REFERENCES device(id) ON DELETE CASCADE,
  profile_id    TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  platform      TEXT NOT NULL,
  token         TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL
);

-- Dispatch fans out by Profile ("notify all adult Profiles' devices", ADR-0039
-- §2.3), so the lookup is keyed on profile_id; the index keeps that fan-out read
-- off a full-table scan as device counts grow.
CREATE INDEX IF NOT EXISTS idx_device_push_token_profile
  ON device_push_token(profile_id);

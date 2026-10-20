-- ADR-0157/0208 Offline Pack grant authority and encrypted-byte bindings.
-- Existing pre-protocol rows remain readable but have empty bindings and are
-- therefore ineligible for grant issuance.
ALTER TABLE offline_pack ADD COLUMN device_enrollment_id TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_pack ADD COLUMN device_key_thumbprint TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_pack ADD COLUMN device_public_key BLOB NOT NULL DEFAULT X'';
ALTER TABLE offline_pack ADD COLUMN device_attested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE offline_pack ADD COLUMN library_ids_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE offline_pack ADD COLUMN manifest_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE offline_pack ADD COLUMN manifest_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_pack ADD COLUMN grant_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE offline_pack ADD COLUMN rev_grant INTEGER NOT NULL DEFAULT 1;
ALTER TABLE offline_pack ADD COLUMN rev_enrollment INTEGER NOT NULL DEFAULT 1;
ALTER TABLE offline_pack ADD COLUMN rev_profile INTEGER NOT NULL DEFAULT 1;
ALTER TABLE offline_pack ADD COLUMN rev_library_policy INTEGER NOT NULL DEFAULT 1;
ALTER TABLE offline_pack ADD COLUMN rev_backup INTEGER NOT NULL DEFAULT 1;
ALTER TABLE offline_pack ADD COLUMN wrapped_cek BLOB NOT NULL DEFAULT X'';

ALTER TABLE offline_pack_item ADD COLUMN selected_version TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_pack_item ADD COLUMN plaintext_size INTEGER NOT NULL DEFAULT 0;
ALTER TABLE offline_pack_item ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_pack_item ADD COLUMN ciphertext_path TEXT NOT NULL DEFAULT '';

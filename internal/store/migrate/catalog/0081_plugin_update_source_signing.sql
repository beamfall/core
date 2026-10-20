-- PLUG-1-SIGNKEY / ADR-0058 signing-key pin install path. A signing-key-pinned
-- Update Source needs the detached Ed25519 signature over the artifact bytes and
-- the trusted public key it verifies against, so the launch path can verify a
-- signing-key-pinned worker (Artifact.Verify Pin{Kind: PinSigningKey}) before it
-- is ever launchable — the existing pin column still holds the operator-facing
-- signing-key id label (hash pins keep pin = the hex hash, unchanged).
--
-- Both columns hold PUBLIC trust material, not secrets: a public key is public by
-- definition and a detached signature is public. Integrity (not confidentiality)
-- is what matters, exactly like the hash pin already stored in this table, so they
-- live here in the integrity-protected catalog and NOT in the 0600 secret store
-- (invariant #5 governs secrets — this is not secret). Append-only; existing rows
-- (hash pins) default to empty and are unaffected.
ALTER TABLE plugin_update_source ADD COLUMN signing_key TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_update_source ADD COLUMN signature   TEXT NOT NULL DEFAULT '';

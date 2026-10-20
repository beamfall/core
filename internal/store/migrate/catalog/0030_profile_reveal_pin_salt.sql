-- Per-Profile random salt for the reveal PIN KDF (argon2id). NULL for
-- Profiles whose stored hash predates this migration (legacy bare-sha256);
-- those upgrade to a salted argon2id hash on the next successful verify.
ALTER TABLE profile ADD COLUMN reveal_pin_salt BLOB;

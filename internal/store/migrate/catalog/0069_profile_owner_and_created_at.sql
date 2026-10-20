-- ADR-0036 §2.7 (My Data): Profile creation timestamp, surfaced read-only in
-- the privacy panel. DEFAULT 0 for rows predating this migration (origin
-- unknown — seeded or pre-0069); CreateProfile records the real wall-clock ms
-- for every Profile minted from here on.
ALTER TABLE profile ADD COLUMN created_at_ms INTEGER NOT NULL DEFAULT 0;

-- ADR-0007 §2/§4: the single owner Profile. The owner-flagged adult Profile is
-- the only Profile permitted to open an Admin Session. The partial-unique index
-- enforces the single-owner invariant (at most one row may hold the flag); the
-- flag is movable (transfer = store.SetProfileOwner, atomic clear-then-set).
--
-- No row carries the flag on servers upgraded across this migration. The admin
-- gate falls back to any-adult-Profile + admin password (the pre-0069 behaviour)
-- whenever no owner is designated, so the migration can never lock the operator
-- out of Admin. First-run (pairing.Bootstrap) designates the bootstrap Profile.
ALTER TABLE profile ADD COLUMN is_owner INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX profile_single_owner ON profile(is_owner) WHERE is_owner = 1;

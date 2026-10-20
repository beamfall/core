-- Offline Pack invariants (CORE-AUDIT-7). SQLite cannot add CHECK/FK clauses
-- to an existing table in place, and rebuilding offline_pack would also touch
-- its referencing child table. Triggers enforce the same write-time guarantees
-- for upgraded catalogs without rewriting valid existing rows.

CREATE TRIGGER IF NOT EXISTS offline_pack_profile_fk_insert
BEFORE INSERT ON offline_pack
WHEN NOT EXISTS (SELECT 1 FROM profile WHERE id = NEW.profile_id)
BEGIN
  SELECT RAISE(ABORT, 'offline_pack profile not found');
END;

CREATE TRIGGER IF NOT EXISTS offline_pack_profile_fk_update
BEFORE UPDATE OF profile_id ON offline_pack
WHEN NOT EXISTS (SELECT 1 FROM profile WHERE id = NEW.profile_id)
BEGIN
  SELECT RAISE(ABORT, 'offline_pack profile not found');
END;

CREATE TRIGGER IF NOT EXISTS offline_pack_profile_delete_cascade
AFTER DELETE ON profile
BEGIN
  DELETE FROM offline_pack WHERE profile_id = OLD.id;
END;

CREATE TRIGGER IF NOT EXISTS offline_pack_state_check_insert
BEFORE INSERT ON offline_pack
WHEN NEW.state NOT IN ('pending', 'building', 'ready', 'revoked', 'expired')
BEGIN
  SELECT RAISE(ABORT, 'invalid offline_pack state');
END;

CREATE TRIGGER IF NOT EXISTS offline_pack_state_check_update
BEFORE UPDATE OF state ON offline_pack
WHEN NEW.state NOT IN ('pending', 'building', 'ready', 'revoked', 'expired')
BEGIN
  SELECT RAISE(ABORT, 'invalid offline_pack state');
END;

CREATE TRIGGER IF NOT EXISTS offline_pack_progress_check_insert
BEFORE INSERT ON offline_pack
WHEN NEW.bytes_total < 0 OR NEW.bytes_ready < 0 OR NEW.bytes_ready > NEW.bytes_total
BEGIN
  SELECT RAISE(ABORT, 'invalid offline_pack progress');
END;

CREATE TRIGGER IF NOT EXISTS offline_pack_progress_check_update
BEFORE UPDATE OF bytes_total, bytes_ready ON offline_pack
WHEN NEW.bytes_total < 0 OR NEW.bytes_ready < 0 OR NEW.bytes_ready > NEW.bytes_total
BEGIN
  SELECT RAISE(ABORT, 'invalid offline_pack progress');
END;

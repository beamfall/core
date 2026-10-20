ALTER TABLE auth_attempt RENAME COLUMN occurred_at TO occurred_at_ms;
UPDATE auth_attempt SET occurred_at_ms = occurred_at_ms * 1000;

ALTER TABLE activity_log RENAME COLUMN occurred_at TO occurred_at_ms;
UPDATE activity_log SET occurred_at_ms = occurred_at_ms * 1000;

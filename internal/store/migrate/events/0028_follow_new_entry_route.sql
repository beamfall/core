-- CORE-2958: where a followed Subscription's new entries go for this Profile.
-- 'new' leaves them in the subscription-new Source only; 'queue-end' and
-- 'queue-front' also add them to the Profile's Play Queue.
ALTER TABLE follow ADD COLUMN new_entry_route TEXT NOT NULL DEFAULT 'new'
  CHECK (new_entry_route IN ('new', 'queue-end', 'queue-front'));
-- The catalog first-seen time (ms) through which entries were judged for the
-- Play Queue. It advances in the events transaction that writes the queue, so
-- a crash replays the remaining entries on the next poll, never twice.
ALTER TABLE follow ADD COLUMN new_entry_routed_ms INTEGER NOT NULL DEFAULT 0;

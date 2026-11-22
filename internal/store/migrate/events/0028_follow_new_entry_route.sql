-- CORE-2958: where a followed Subscription's new entries go for this Profile.
-- 'new' leaves them in the subscription-new Source only; 'queue-end' and
-- 'queue-front' also add them to the Profile's Play Queue.
ALTER TABLE follow ADD COLUMN new_entry_route TEXT NOT NULL DEFAULT 'new'
  CHECK (new_entry_route IN ('new', 'queue-end', 'queue-front'));

-- Library-level feed Subscriptions (ADR-0030 §4): one downloaded copy per
-- household — a Subscription belongs to exactly one Library. feed_url can
-- embed private-feed credentials (token-in-URL feeds): it is a secret
-- (invariant 5) — stored at rest here, surfaced to manager sessions only,
-- NEVER logged and never sent to directories or shared caches. root is the
-- folder new enclosure downloads land in (the Library folder the standard
-- scan pipeline imports from). etag/last_modified carry the conditional-GET
-- validators from the previous poll (ADR-0030 §7).
CREATE TABLE subscription (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  feed_url TEXT NOT NULL,
  root TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  added_at_ms INTEGER NOT NULL,
  last_polled_at_ms INTEGER NOT NULL DEFAULT 0,
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  UNIQUE (library_id, feed_url)
);

-- Poll bookkeeping: feed entries already ingested, keyed by entry GUID
-- (ADR-0030 §3: the GUID is the dedupe/refresh key — never the enclosure
-- URL, which ad-insertion rewrites freely).
CREATE TABLE subscription_seen_entry (
  subscription_id TEXT NOT NULL REFERENCES subscription(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  seen_at_ms INTEGER NOT NULL,
  PRIMARY KEY (subscription_id, guid)
);

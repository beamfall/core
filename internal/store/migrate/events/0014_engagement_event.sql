-- ADR-0005 §1 (DEFER-1) — the timestamped, Profile-scoped engagement EVENT LOG.
--
-- ADR-0005 §1 specifies engagement as an append-only event log, not stored
-- counters: "Core stores timestamped, Profile-scoped engagement events; counts
-- are derived, never stored. Plugins declare custom engagement event kinds
-- riding the same log." The 0005 playback_progress table is the materialized
-- resume/recency PROJECTION (ADR-0011 §3 derived-truth projection) and stays
-- exactly as-is; this table is the additive raw event log alongside it. The
-- adult plugin's o-counter (ADR-0027) is the first consumer — but core never
-- names it: `kind` is a plugin-declared, shape-agnostic string core only ever
-- groups/compares, never parses (invariant #1).
--
-- Append-only is the contract: an event is INSERTed and never UPDATEd or
-- DELETEd as part of normal operation. A correction (e.g. a "remove last"
-- undo of an o-counter increment) is a NEW compensating event with a value
-- delta that cancels the prior one, so history is preserved and a running
-- aggregate is the SUM of value over the kind. (The only rows ever removed are
-- orphans whose target Media no longer exists — the same Media-existence prune
-- that governs playback_progress; pruning by Media existence is not a history
-- edit.)
--
-- Target shape mirrors how the existing engagement rows reference targets: the
-- primary target is media_id (TEXT, no hard FK — engagement rows reference
-- Media by id and are reconciled by the engagement-prune Media-existence sweep,
-- exactly like playback_progress / media_favorite). entity_id is an OPTIONAL
-- secondary target so an event can also (or instead) be attributed to an Entity
-- for the ADR-0027 "entity aggregates" read; at least one of the two is set
-- (enforced in the store write path, kept out of the schema so the table stays
-- a plain append target). occurred_at_ms is the true event instant (the
-- timestamped requirement); value is a small optional signed integer the kind
-- owns (e.g. an o-counter +1 increment or a -1 correction); payload is an
-- optional small opaque string for kinds that carry more than a count. None of
-- these are parsed by core.
CREATE TABLE engagement_event (
  id TEXT PRIMARY KEY,
  profile_id TEXT NOT NULL,
  media_id TEXT,
  entity_id TEXT,
  kind TEXT NOT NULL,
  value INTEGER NOT NULL DEFAULT 0,
  payload TEXT,
  occurred_at_ms INTEGER NOT NULL,
  created_at_ms INTEGER NOT NULL
);

-- Read path 1 — by Profile + target (Media): the o-counter's per-item history
-- and running aggregate for a Media item, and the Media-existence prune scan.
CREATE INDEX idx_engagement_event_profile_media_kind
  ON engagement_event (profile_id, media_id, kind, occurred_at_ms);

-- Read path 2 — by Profile + target (Entity): the ADR-0027 entity aggregate.
CREATE INDEX idx_engagement_event_profile_entity_kind
  ON engagement_event (profile_id, entity_id, kind, occurred_at_ms);

-- Read path 3 — by Profile + kind: a kind's full per-Profile history/aggregate
-- across targets (future analytics, ADR-0005 §6 "rich watch-analytics built on
-- the event log").
CREATE INDEX idx_engagement_event_profile_kind
  ON engagement_event (profile_id, kind, occurred_at_ms);

# Scoped write and playback-read permissions for Integration Workers

Ticket: CORE-1695 (`queue:core:main`, Jellyfin client-parity audit 2026-11-14).

## Why

The Integration Worker permission vocabulary (ADR-0058) lets a worker read the catalog, link
Profiles and hand off ingest or file mutations, but it cannot edit metadata, build collections,
set artwork or sync watch state. Collection managers and watch-state sync tools therefore cannot
run as Workers; they need an admin API token with full reach instead. A Worker grant is narrower,
audited and approved by the owner, so these tools should run through it.

## What Changes

- Five new permission kinds, all high-risk (owner approval at install, re-approval when an update
  adds one, one audit record per call):
  - `write-metadata`: undoable field edits through the bulk-edit path.
  - `write-collections`: create a collection and add media to it.
  - `write-artwork`: import artwork into a slot; locked slots are never replaced.
  - `write-watch-state`: mark media watched or unwatched for the bound Profile.
  - `read-playback-state`: read playback progress for the bound Profile.
- The kinds are added to the plugin SDK vocabulary first (`plugin-sdk`
  `openspec/changes/worker-write-permissions/`); Core's gated set must equal it.
- Every new Host method checks, in order: verified-manifest declaration and approved grant, a wired
  seam, an audit ledger, the causation cap (writes), the grant's Library filter, then that every
  target is visible through that filter. The audit row is written before the change and an audit
  failure aborts the call.
- Watch-state and playback-state grants must be bound to one Profile; an all-Libraries grant is
  refused for them. Writes for other kinds follow the grant's existing Library binding.
- Causation lineage (ADR-0125, ARCHITECTURE.md section 4): a write's admission binds the outbox rows
  its seam will append (one `media.updated` row per id for metadata, the bound Profile's
  `invalidation` row for watch state) in a bounded ledger on the shared causation cap, and event
  delivery returns those rows with the write's token. A Worker reacting to its own writes therefore
  spends one chain budget instead of starting fresh roots.
- New wire methods: `write-metadata`, `create-collection`, `add-collection-media`, `set-artwork`,
  `set-watched`, `read-playback-state`.
- Activity log gains five `plugin.*` actions with Console labels.

## Non-goals

- No new GraphQL surface: approval stays on the existing `approvePluginPermission` mutation.
- No change to the public client API (CORE-1694) or to Core's own Trakt-style scrobbling.
- Watch-state writes do not notify watched-state observers, so a sync Worker cannot cause a loop
  through its own write.

## Failure modes

- Missing seam, audit ledger, causation cap, grant or approval: permission denied, nothing written.
- A target outside the grant's Library filter: not found, nothing written or audited.
- Locked artwork slot: the slot-locked error is returned to the Worker.
- Descendant ledger full: the write is refused with a tracking-capacity dead letter, no budget spent.

## Acceptance

- The vocabulary adds the scoped grants with audit and owner consent.
- A sample Worker creates a collection and sets artwork through them (Host test over the wire).

## Rollback

Revert the Core change. A Worker manifest that declares the new kinds then fails bundle
validation and the Worker is not admitted; no stored data depends on the kinds.

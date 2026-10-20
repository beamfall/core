# Core Platform entities


**Media**:
The core generic entity for a single playable item in the library, backed by one or more
**Media Files**. The canonical wire/schema name (`type Media` in GraphQL). Domain display
labels ("Scene", "Episode", "Movie") are declared by the active **Plugin** via the
**Navigation Manifest** — clients render whichever label the manifest supplies.
_Avoid_: "Scene"/"Video"/"Clip" as schema or code identifiers.

**Media File**:
A physical file backing a Media item, joined by **content fingerprint** (size + partial
hash), never by path — a rename outside the server is a move, not a delete. Carries the
probed stream inventory and quality attributes. Multiple files of one Media are **Versions**
(same cut, different encodes — 4K DV + 1080p SDR; playback picks per client); ordered files
within a Version are **Parts** (CD1/CD2, one logical timeline). A vanished fingerprint goes
**missing** (health report), never silently deleted.
_Avoid_: path-keyed identity, treating Versions as duplicates.

**Edition**:
A different *cut* of the same work (Director's Cut vs Theatrical): separate Media linked by
an `edition-of` relation, grouped on detail screens. Editions have their own runtime,
markers, and resume; **Versions** share them.
_Avoid_: modelling an edition as a Version (their timelines differ).

**Extra**:
A typed child Media (trailer, behind-the-scenes, deleted scene, interview, sample) attached
to a parent Media, detected at scan by folder/suffix conventions. Excluded from browse
Sources, continue-watching, dedupe, and stats by default; surfaced on detail screens and in
the player.
_Avoid_: letting extras scan in as peer Media (browse pollution).

**Library**:
A named media scope **bound to exactly one Domain Plugin** (or to none — generic folder
browsing) and backed by root folders, Core custody, or one read-only **External Library
Provider**. The unit of domain scoping: a file's Library decides its Identification pipeline;
Entities are Library-scoped; Restrictions hide whole Libraries; the client's top-level
navigation is the Library list. (Redefined 2026-06-10 from the predecessor's "whole corpus"
meaning — Plex-style sections won.)
_Avoid_: "the library" meaning the whole corpus (say **server** or **catalog**), "section"
(Plex's word — collides with the rendering **Section**).

**Scanned Library / External Library / Custodial Library** *(backing-model axis; expanded 2026-08-23)*:
Three ways a Library obtains bytes. A **Scanned Library** indexes operator-owned files under root
folders. An **External Library** indexes operator-owned remote or virtual bytes read-only through
an **External Library Provider**; provider loss makes them unavailable, never deleted. A
**Custodial Library** holds the canonical copy itself through Core-controlled storage. Only the
Custodial form promises that Beamfall preserves the sole copy; External bytes require import before
they gain custody, offline durability, mutation, Trash, or restore semantics.
_Avoid_: "Backup Library" (collides with the Console's **backups** = operator server/DB
backups), "sync folder", remote streaming catalogs, treating an External copy as custodial, treating
a Custodial copy as a re-fetchable index.

**Encrypted Library** *(at-rest regime, 2026-06-13)*:
A Library whose contents are encrypted at rest under a **Library-scoped key**. A **Core,
domain-agnostic** regime (invariant #1), **orthogonal** to the bound Plugin, to the
**Scanned/Custodial** axis, and to the **Reveal** axis — Reveal governs who *sees* an item in
the UI; encryption governs whether it is *confidential at rest*. The deliberate scoped reversal
of ADR-0011 §9 ("at-rest encryption out of scope for v1"). **Server-readable**: the running
server holds/derives the key, so server-side scrape, derivatives, search, and Ambient Display
keep working — it protects a stolen disk, leaked backups, and off-box copies, **not** a
live-compromised server or root-while-running.
_Avoid_: "vault"/"secure folder" (brand carries no vault imagery), implying byte-blind E2E
(the server can read plaintext by design).

**Recovery Key** *(2026-06-13)*:
The **user-held** credential that independently unwraps an **Encrypted Library**'s data key, so
its contents stay retrievable with **no Beamfall server, account, or subscription alive** —
restore the encrypted data dir anywhere and unwrap. Minted **locally by the AGPL server** at
Encrypted-Library creation behind a hard "you must save this" acknowledgement gate (a
never-saved key is a silent data-hostage trap — the exact failure this feature exists to
prevent). **License-Key-shaped** (signed file/QR, ADR-0031) but distinct in purpose: a
**License Key** proves a lifetime Premium purchase; a Recovery Key recovers *your encrypted
data*. **Never escrowed to Beamfall cloud** (invariant #7).
_Avoid_: cloud-escrowed recovery, recovery gated by Premium or a Beamfall Account, conflating
with **License Key** (entitlement) or **Stream Token** (short-lived media-byte auth).

**Sealed / Unsealed (Encrypted Library)** *(2026-06-13)*:
The lock state of a **passphrase-unseal** Encrypted Library (ADR-0045 §2.4). **Sealed**: the
Library's key is not present, so its Media, derivatives, and metadata are absent from every
client, search, and **Source** — identically to **Reveal**-filtering — until the operator
**Unseals** it by entering the passphrase, which derives the key into memory only (re-derived
each restart, never written to disk). An **auto-unseal** Library is effectively always Unsealed
after boot. Orthogonal to **Reveal** (who *sees* an item) and to **Restrictions**: a Sealed
Library is *cryptographically* unavailable, not merely hidden.
_Avoid_: "locked" as a schema identifier (collides with field-locks), conflating **Sealed**
(no key present) with Reveal-hidden (key present, display-suppressed) or with **Restrictions**.

**Zero-Knowledge Backup** *(at-rest regime, off-box, 2026-06-21)*:
The **third at-rest regime** and the only **Beamfall-blind** one. A Library's bytes are
**client-side encrypted into ciphertext** before they leave the box, then replicated to an
**off-box destination** that only ever holds opaque ciphertext — neither the destination operator
nor Beamfall can read them. Distinct from the two **server-readable** regimes: a **Custodial
Library** holds the canonical copy *and can read it*; an **Encrypted Library** is confidential at
rest but the *running server* holds the key. Zero-Knowledge Backup is **off-site disaster
durability**, orthogonal to both — you can Zero-Knowledge-Backup a Scanned or Custodial,
plaintext or Encrypted, Library. Two **destinations**: a **BYO Bucket** (the operator's own
S3-compatible cold storage — the default) or a **Hosted Ciphertext Store** (a Beamfall-operated,
metered, byte-blind ciphertext tier — convenience only). Recovery is **user-held** and survives
Beamfall: the unwrap secret is a **Recovery Key**-shaped credential (never escrowed to Beamfall
cloud, invariant #7), so a dead cloud or dead destination never holds data hostage (Continuity
Pledge). What is sold is the **orchestration, encryption, and recovery** — never the bytes, and
never read access to them.
_Avoid_: "cloud backup"/"Beamfall backup" (implies Beamfall reads or owns the bytes — it cannot),
flat **custodial** off-site copy (server-readable off-box = hostage risk, the rejected design),
collision with the Console's **backups** (operator server/DB backups, ADR-0077) and with
**Custodial Library** (on-box, server-readable canonical copy), cloud-escrowed recovery, gating
*retrieval/export* of already-stored data behind Premium or a live Beamfall Account.

**Folder**:
A directory in the library tree. May contain direct Media and subfolders.
_Avoid_: Directory, Path, Node.

**Tag**:
A freeform label attachable to Media and other entities. Used both as a filter and as
user-assigned metadata. Tags may form an optional parent/child hierarchy (decided
2026-06-11); filters and counts can include descendants.
_Avoid_: per-domain tag systems (plugins emit core Tags).

**Marker**:
A labelled timestamp — or time range (optional end time) — inside a Media item. Carries
optional core **Tags**; when untitled, the first tag's name is the display fallback (no
special "primary tag" slot — simplified from the predecessor, 2026-06-11). Every Marker has
a **thumbnail** — a frame grab of its Media at the marker's timestamp (decided 2026-06-11;
the frame-grab capability is timestamp-generic, Markers are its first consumer). Surfaced on
detail screens, as player chapters, and cross-library via the `markers(filter)` **Source**
(the marker wall).
_Avoid_: a required primary tag, marker-only tag systems.

**Skip-segment**:
A **Marker** whose kind is one of a small closed core vocabulary — `intro`, `recap`,
`credits`, `ad` — denoting a span a viewer may want to bypass (`intro`/`recap`/`credits` added
2026-06-14; `ad` added 2026-07-08, ADR-0193). Always a time **range** (start + end); a `credits`
segment's end is the true end of the credits, never assumed to be end-of-file, so a post-credits
scene is content the segment ends before. An `ad` segment marks a podcast advertisement; its
**flavor** (`inserted` = dynamically-inserted, `host-read` = baked-in sponsor) is internal
**provenance**, never a public kind. Authored manually or proposed by **Skip-segment detection**.
_Avoid_: "chapter" (a navigation Marker, not a bypass span), assuming `credits` runs to
end-of-file, "outro" (use `credits`), `commercial`/`preview` kinds (deferred, not in the
closed set), `sponsor` as a kind (a non-binding *reserved* future split of `ad`, not live),
exposing ad flavor on the client wire.

**Auto-skip**:
A per-Profile playback **preference** — four independent booleans, one per **Skip-segment**
kind (`intro`/`recap`/`credits`/`ad`), Profile-global and default off — under which the client
seeks to a segment's end when playback enters it, rather than waiting for the viewer to invoke
the manual skip control. A pure client-side seek, orthogonal to binge **auto-advance**. The
boolean keys on kind **alone**; every served `ad` marker is auto-skippable (ADR-0193 drops
sub-precision host-read detections rather than surfacing them, so the wire needs no per-marker
eligibility flag).
_Avoid_: "auto-advance" (end-of-item next-episode binge — a separate, untouched feature),
"the server skips" (the server stores the preference and serves Markers; the client performs
the seek), a single combined toggle, per-Library/per-Show overrides (deferred), a per-marker
auto-eligibility flag or keying auto-skip on ad flavor (deliberately avoided).

**Skip-segment detection**:
A Core capability that proposes **Skip-segment** Markers from a Media item's own bytes, run as
a generation-class job (transcode-preempted, enqueued post-scan). Methods —
embedded-chapter import (titled container chapters), cross-episode fingerprinting (shared
audio spans across the ordered child Media of a common container), a local-ML classifier
(ADR-0179 method (c)), and — for podcast `ad` segments — a per-episode audio ad classifier
(ADR-0193, provenance method `podcast-ad-audio`, enqueued per-episode-immediate since a feed's
membership never stabilizes). Per-Library operator opt-in (default off); video Media only for
`intro`/`recap`/`credits` (v1), extended to podcast Media for `ad` (ADR-0193). Strictly
additive: a `detected` segment never clobbers a `manual` one, and a re-run replaces only its own
prior output; a detection below the confidence/boundary-precision bar is dropped, not surfaced.
_Avoid_: an online/community segment database (rejected — content egress conflicts with the
privacy posture), detection as plugin code (Core-only; the compiled-plugin seam stays at two
hooks), Core naming "Season"/"episode" (it groups by generic container shape), household-side
model training (ADR-0193: dev-side only, ships signed ADR-0170 weights).

**Collection / Sub-collection**:
A user-curated, nameable group of Media with a **defined member order** (manual,
user-rearrangeable); collections nest, and "Play" plays members in order. A **Smart
Collection** is a Collection whose membership is a persisted **Source** expression,
evaluated live (order comes from its sort, not manual arrangement). There is no separate
playlist concept — an ordered Collection *is* the playlist (decided 2026-06-11; Plex
playlist imports land here).
_Avoid_: "Playlist" as a schema or product concept.

**Saved Filter**:
A named, per-Profile, per-Library declarative filter (the Source filter vocabulary),
pinnable into Layouts. Restored from upstream's beloved saved-filters feature after the
expert review caught it being dropped silently.
_Avoid_: confusing with per-Profile *default/enforced* filters (policy, not bookmarks).

**Artwork Slot**:
One of a fixed core vocabulary of artwork positions on Media and Entities alike: **poster**
(2:3), **backdrop** (16:9 wide, text-safe), **thumb** (16:9 still), **logo** (transparent
wordmark), **portrait** (2:3 headshot). Scrapers and users fill slots; Layouts reference them.
Every slot is optional with defined fallbacks (poster → thumb → auto-generated frame grab), so
a plugin-free library still renders. Slots are added rarely — each is a client rendering
commitment.
_Avoid_: "Cover" (the predecessor's single-artwork term — superseded by the **thumb** slot),
plugin-invented artwork names.

**Generated Asset**:
A visual or data asset the server derives from a Media item's own bytes, as opposed to Artwork,
which is chosen (scraped, uploaded, or picked from candidates). Members: **scrub thumbnail
sprites** (timeline thumbnails), the **preview clip** (a short stitched video sampling
different parts of the runtime, played on hover/focus), **Marker thumbnails** (a frame
grab at each Marker's timestamp), **waveform envelope** (amplitude-over-time sampled at
~1000 points across the duration — powers the scrub-bar visualisation in the Music Plugin
player and the **Ambient Display** audio-reactive modes), and **beat grid** (BPM +
beat-timestamp sequence derived from audio analysis — powers tempo-synced visualisations;
generation tooling TBD, may land in the Enrichment Plugin scope). Generated Assets have no
candidates, no user upload, and no fallback chain — they are rebuilt from the file when
missing or stale (decided 2026-06-11; audio types added 2026-06-12). *Artwork is chosen;
Generated Assets are derived.*
_Avoid_: treating these as Artwork Slots, "preview" unqualified (say preview clip or
scrub thumbnails), treating waveform/beat-grid as plugin-private fields (they are core Generated
Assets, keyable by content fingerprint).

**Entity**:
A generic, plugin-declared domain object in the library — a Performer, Studio, Show, Season,
Director, Genre. Every Entity has a kind, a name, artwork, typed fields, and relations (to
Media and to other Entities). Entities are rows of a single generic system, **not** bespoke
schema types — the GraphQL contract exposes them through fixed generic queries regardless of
which Plugin declared them.
_Avoid_: adding a plugin domain object as a new GraphQL type (that's the rejected
compiled-schema model).

**Entity Kind**:
A Plugin's declaration of an Entity category: its name, archetype, field schema, relation
kinds, and child ordering (e.g. "Show: container of Seasons, ordered"). Declared as data, not
code — adding a kind changes no schema and no client.
_Avoid_: "type" (collides with GraphQL/Go types), "class".

**Archetype**:
The fixed rendering/behavior contract an Entity Kind maps onto, shipped in every client:
**person** (portrait-first, filmography detail), **container** (poster-first, ordered
children, play-next semantics, nestable), **category** (a faceting label, browse dimension;
a category Kind may declare itself **hierarchical** — parent/child entities with
depth-aware counts and descendant-inclusive filters, decided 2026-06-11).
Plugins add Kinds freely; archetypes are added rarely, in client releases. A concept that fits
no archetype picks the nearest one — it does not get a custom renderer.
_Avoid_: treating cast/credit as an archetype — a credit is a **relation with attributes**
(person ↔ Media with role/character/billing), never an Entity.

# Podcast Plugin vocabulary


**Podcast Plugin**:
The first-party podcatcher (decided 2026-06-11, ADR-0030; sequenced behind Music): a
**Podcast Domain Plugin** plus an **RSS Subscription Integration Plugin** (feed poller +
enclosure downloader through the Incoming Folder pipeline). Positioned as **the podcast
archive**, but delivered **stream-first, archive-always** (ADR-0052, amending ADR-0030 §2):
tapping an episode plays it immediately while the server tees the bytes to disk, so the copy
fetched at listen time is preserved against ad re-stuffing and dead feeds. The archive is
delivered *through* the streaming path, not before it.
_Avoid_: "stream-only"/virtual Media that never persists (still rejected — the ADR-0052
proxy-tee always keeps a copy), treating episodes as "audio movies" (the Plex failure),
enclosure URLs as identity (episode `<guid>` + `podcast:guid` ride External IDs).

**Podcast / Episode** *(Podcast Kinds & labels)*:
**Podcast**: the one Entity Kind (container archetype) — the show. An **Episode is Media**
(display label only). No Season Kind (season/episode numbers are sort metadata); episodic
vs serial (`itunes:type`) is a per-entity sort hint Sources respect. Episodes declare
**resume-eligible**.
_Avoid_: Season or Person Kinds (v1), "Episode" as a schema identifier.

**Subscription vs Follow**:
A **Subscription** is the Library-level object (feed URL, credentials, polling state,
download + retention rules) — one download per household; private-feed URLs are secrets
(invariant 5), never sent to directories. A **Follow** is per-Profile engagement-class
state (the show appears in *my* surfaces); adult Profiles may Follow from any viewer app —
the scoped ADR-0022/0023 amendment; first Follow creates the Subscription. Manager-class:
disk caps, retention, credentials, OPML.
_Avoid_: per-Profile downloads, "subscribe" for the per-Profile relation (that's Follow),
viewer surfaces that accept feed credentials.

**Stream-first, archive-always** *(ADR-0052, amends ADR-0030 §2)*:
The Podcast Plugin's playback model. Tapping an episode plays it **now** via a server-side
**proxy-tee** — the server ranges the origin enclosure to the client under a **Stream Token**
while writing the same bytes to disk — so you get the tap-to-play feel *and* keep the file.
Reconciles ADR-0030 §2's rejection of stream-only Media: bytes flow through the server, so the
Stream-Token + Reveal contract holds; the enclosure URL is bounded by the subscribed feed, so
it stays inside the SSRF-hardening regime; the episode `<guid>` is identity before the content
fingerprint settles. The deliberate reversal of "episodes exist only once downloaded."
_Avoid_: the relay/AirPlay sense of "stream" (this is server-proxied bytes under a Stream
Token, not a dumb receiver), "stream-only" as a synonym (the proxy always persists — true
never-persist streaming stays rejected).

**Provisional vs Archived Episode** *(ADR-0052)*:
The two availability states of a podcast Episode — a filterable **Source** predicate
(`archived / streamed-provisional / not-downloaded`). A **Provisional Episode** is one
materialized by a **stream-first** play or queue action: a **Media** keyed by its GUID with a
file still being tee'd (fingerprint pending), playable and queueable by `MediaID` like any
episode, and **reapable** — a streamed-on-a-whim copy a retention rule may trash (ADR-0051)
unless favorited or `keep`-flagged. On tee completion it is **promoted in place to an Archived
Episode** — the same `MediaID`, now a keep-all archive item. A subscription *download* is
Archived from the start.
_Avoid_: re-keying the `MediaID` on promotion (queue/resume refs must survive), modelling a
provisional episode as a non-Media catalog ref (it is a Media — one uniform episode model),
giving casual streamed copies the permanent keep-all guarantee.

**Discover Editions** *(ADR-0190)*:
The Podcast Plugin's premium Discover layer — Beamfall-curated editorial content (a daily
featured pick, monthly themed collections, weekly New & Notable, automated per-category top
lists) delivered as a signed bundle through **Content Sync** and rendered as extra Discover
Sections. Subscription tiers only (lifetime buys the software, not ongoing services);
sourced from Podcast Index + open RSS with in-house rankings, artwork hotlinked; every
section carries a visible last-updated stamp and hand-curated sections auto-collapse to an
evergreen fallback when stale. Without entitlement or cloud reachability, Discover is the
free directory experience (ADR-0052 §2.5) — never blank, never degraded below today.
_Avoid_: "Discover Plus" or any tier-sounding name (Premium is the entitlement term),
treating Edition URLs as trusted (every URL still passes the ADR-0012 SSRF regime at fetch),
"editorial charts" labels beyond what the ingest delivers (provenance labels per ADR-0154
§B6).

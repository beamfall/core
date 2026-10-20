# Music Plugin vocabulary


**Music Plugin**:
The first-party **Domain Plugin** for music libraries (decided 2026-06-11, ADR-0029 —
expanded in scope by ADR-0062). A **Track is Media** with a plugin display label — title,
duration, track and disc numbers are Media fields. Music Libraries are music-only; no
`class` field.
_Avoid_: "Track" as a schema identifier, Track as an Entity Kind.

**Beamfall Music** *(2026-06-18)*:
The first-class local-audio product experience built on the **Music Plugin** and Core audio
primitives: library/player UX plus Plexamp-class discovery and listening polish over
Beamfall-owned audio **Media** only. Third-party music services may integrate only as
**Integration Plugins**; their licensed catalog items are not Beamfall Media and their audio
bytes are not proxied or cached behind **Stream Tokens**.
_Avoid_: "Spotify clone" when it implies provider catalog playback, provider-specific
concepts in Core, or licensed remote audio as Beamfall-owned Media.

**Artist / Album / Genre** *(Music Kinds)*:
**Artist**: person-archetype Kind. **Album**: container Kind, ordered Media children
(sorted disc, then track; no Disc entity — disc number is a sort field). **Genre**:
category Kind, **flat**. Two distinct artist relations: **Album → Artist** from the
`ALBUMARTIST` tag (compilations stay one album; "Various Artists" is an ordinary Artist),
and **Media → Artist** from track artist tags (multi-valued tags taken literally). Artist
browse splits "Albums" (album-artist relation) from "Appears on" (track relation only).
_Avoid_: deriving Album→Artist from track artists (the phantom-compilation bug), "feat."
string-splitting in the parser, hierarchical genres, a Disc Kind.

**Playlist / Play Queue** *(amended 2026-06-11, ADR-0030)*:
A **Playlist** is a display label over a core **ordered Collection** — durable, named,
per-Profile, reveal-filtered; not a separate concept. The **Play Queue** is the
what-plays-next list, in two layers: a **core per-Profile persistent queue** (server-stored,
reorderable, reveal-filtered, rendered by the `queue` Source — ADR-0030 §5, the cross-device
Up Next podcasts forced) and the **session-declared view** (ADR-0029 §6) the client reports
into the playback session, which references the persistent queue when playing from it. Only
users mutate the queue — the server persists, never injects. Resume on audio Media is
**plugin-declared** (music declares no-resume; podcast episodes resume).
_Avoid_: "playlist" for the queue (or vice versa), server-*initiated* queue mutation
(Plex-style autoplay injection), "audio never resumes" as a blanket rule.

**Station** *(Beamfall Music, 2026-06-18)*:
An endless generated listening queue for Beamfall-owned audio **Media**, derived from a seed
and the staged-hybrid recommendation signals defined by ADR-0062. A Station produces a
mutable **Play Queue** only after an explicit user action; it is not a durable ordered
**Collection**. A saved Station stores its seed and tuning, not the generated track order.
_Avoid_: calling a Station a Playlist, promising provider/editorial catalog behavior, "Mix"
for the endless queue concept.

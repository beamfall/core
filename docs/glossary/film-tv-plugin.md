# Film & TV Plugin vocabulary


**Film & TV Plugin**:
The single first-party **Domain Plugin** covering both movies and television (decided
2026-06-11, amending ADR-0003's separate movies/TV plugins). It stays one plugin — not two —
because Films and TV share one entity space (a Person's filmography, Studios, Genres, Film
Series all span both) plus one TMDB scraper, parser/scorer hooks, and migration importers;
splitting would fork all of that and break cross-family entities. A Library bound to the
plugin declares a **family scope** — **Films**, **TV Shows**, or **Mixed** — at creation:
Films / TV Shows set the `class` field (movie / episode) deterministically so identification
never has to guess the family, while **Mixed** holds a combined folder tree where the parse
stage classifies movie vs episode per file. Declares the Kinds below; a Movie and an Episode
are **Media** (display labels via the Navigation Manifest), never Entities.
_Avoid_: separate "Movies plugin"/"TV plugin", "Movie"/"Episode" as schema identifiers.

**Show / Season** *(Film & TV Kinds)*:
**Show**: container Entity Kind, ordered children = Seasons, poster-first. **Season**:
container Kind, ordered children = Episode Media, play-next semantics (binge auto-advance).
A multi-episode file (`S01E01-E02`) is **one Media spanning a range** — the Season relation
carries `episodeStart`/`episodeEnd`; never two Media sharing one file. TMDB Season 0 is a
real Season named "Specials", sorted last, excluded from auto-advance.
_Avoid_: "Series" (collides with **Film Series**), sub-file timeline mapping.

**Film Series** *(Film & TV Kind)*:
The container Kind for scraped movie groupings (James Bond, MCU) — TMDB's "Collection"
concept, deliberately renamed: scraped groupings are Entities, while a core **Collection**
is user-curated. Ordered by release; sort fields may offer chronological.
_Avoid_: "Collection" for this Kind (taken by core), scrapers writing core Collections.

**Certification / Score**:
Two distinct rating concepts, never to collide. A **Certification** is the regulatory age
label (PG-13, TV-MA, FSK 16) — per-country, per-media-family, supplied by scrapers for the
server's configured **certification region**. A plugin declares each rating system as an
**ordered scale** (data); a managed Profile's enforced filter may set a **ceiling** against
a scale ("index ≤ N"). Unrated/unknown items are **blocked by default** under a ceiling
(fail-closed), with a per-Profile override. The ordered-scale + ceiling mechanism is
generic core machinery (a curriculum plugin could declare belt ranks on it). A **Score**
is a critic/audience number (TMDB vote average) — display, sort, and filter material only,
never policy.
_Avoid_: "rating" unqualified (say which), hardcoded US-only scales in core, unrated
content visible under a ceiling by default.

**User Rating / Favorite / Organized** *(core curation & preference fields, 2026-06-11)*:
Three core fields on Media and Entities alike. **User Rating**: the person's own star
rating, **per-Profile** — the third rating concept beside **Certification** (policy) and
**Score** (scraped critic number); never collides with either. **Favorite**: a per-Profile
flag feeding personal rails and favorite-relation filters. **Organized**: a **global**
curation-completion flag — about the catalog, not a person; a Manager-surface concern.
_Avoid_: "rating" unqualified, global favorites, plugins inventing parallel rating/favorite
fields.

**Person / Studio / Genre** *(Film & TV Kinds)*:
**Person**: one person-archetype Kind for all humans — actor/director/writer are
distinguished by **credit relation attributes** (role, character, billing, department),
never by separate Kinds. **Studio**: one category Kind covering production companies and TV
networks (a field carries the distinction). **Genre**: category Kind, browse facet.
_Avoid_: Actor/Director Kinds, a separate Network Kind, credit as an Entity.

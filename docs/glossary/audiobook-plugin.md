# Audiobook Plugin vocabulary


**Audiobook Plugin** *(ADR-0055, 2026-06-14)*:
The first-party **Domain Plugin** for audiobook libraries — declaration-heavy but carrying a
**first-party parser Hook** (ADR-0044) that groups multi-file books, riding the audio machinery the
**Music** and **Podcast** plugins forced into Core (resume-eligible audio-class Media, persistent
per-Profile **Play Queue**, chapters **Sidecar Track**, background-audio + CarPlay/Android Auto
posture). Chosen **first-party over an unofficial data-only build because Apple is neutral** (a
server plugin is never inside an App Store binary — ADR-0050/0001) and the parser Hook unlocks the
dominant real-world layout (`.mp3`-per-chapter folders) in v1. Ebooks/comics are **out of scope** —
a *reader* product (page rendering), not playable Media.
_Avoid_: "Book" as an Entity Kind, ebooks/comics as Media, "audio movie" modelling (the Plex
failure ADR-0030 warns against), an Audible scraper or Audnexus as a first-party metadata source.

**Audiobook (Media shape)** *(2026-06-14)*:
One audiobook = **one Media** with a plugin display label, **never** a container of chapter-Media.
A multi-file book (one file per chapter) is that Media's ordered **Parts** — one logical timeline,
**one** resume position — never multiple Media; the first-party **parser Hook** (ADR-0044) emits the
grouping ordinals that fold a leaf folder's ordered audio files into those Parts (the hook-free scan
path is `titleFromPath`, no grouping — which is why this plugin is first-party). Chapters are
**Markers** (embedded m4b chapters import via the chapters **Sidecar Track**): navigation *inside*
the Media, not library items.
Resume-eligible (plugin-declared, the podcast-episode precedent). The **Movie** model (one Media,
chapter markers), not the **Season** model (container of Episode-Media).
_Avoid_: chapter-as-Media, per-chapter resume, treating Parts as duplicates, a "Book" container Kind.

**Person / Series / Genre** *(Audiobook Kinds, 2026-06-14)*:
**Person**: one person-archetype Kind for all humans — **Author** and **Narrator** (and optionally
Translator) are distinguished by **credit-relation attributes**, never by separate Kinds (the
Film & TV pattern; a self-narrating author is *one* Person with two credit relations, not a
duplicate). Person browse splits **"Wrote"** (author relation) from **"Narrated"** (narrator
relation) — the Music Artist "Albums"/"Appears on" precedent. **Series**: container Kind, ordered
Book Media; sequence is a relation attribute and **may be fractional** ("Book 4.5" novellas).
**Genre**: category Kind, **flat**. **Publisher** is **deferred** (a typed field or Tag, not a Kind v1).
_Avoid_: separate Author/Narrator Kinds (self-narration duplicate), credit-as-Entity, hierarchical
genre, integer-only series sequence.

# Spoiler guard for unplayed children on the web

Ticket: CORE-3057 (`queue:core:main`), the core and web slice of CORE-1707.

## Why

A container's child rows show every child's still and summary. For a series the viewer has not
caught up on, those give away what happens next. The owner asked for stills and descriptions of
unwatched items to be hidden by default, with a setting to turn that off and a way to reveal them
one at a time or for a whole container.

## What changes

- **Guard.** An unplayed video Media item that a container holds (`SourceItem.container` is set)
  draws a neutral placeholder instead of its still: the container's poster when the card carries
  one, else the empty art tile, with an eye-off mark. Its summary is replaced by a "Hidden until
  you watch" line. The title, ordinal, runtime and actions stay. A child with any progress
  (`engagement.completed` or a position above zero) shows normally.
- **Where.** The container Media rows on an Entity page, the container children grid, and Media
  cards (Home rows such as Next up, Library, search) whose art would be a guarded child's still.
- **Setting.** Profile Settings, under Hidden items: "Hide details of unwatched items", on by
  default. It is the client pref `profile.show-unplayed-details`; `"on"` turns the guard off,
  following the existing "on"/absent boolean convention, so an unset Profile is guarded.
- **Per-container reveal.** A "Show spoilers" toggle above a container's rows writes
  `profile.show-unplayed-details.container.<URI-encoded container id>` = `"on"` (clearing it
  re-hides). It is keyed on the outermost container the card reports, so revealing a show also
  reveals its children on Home. It persists server-side with the Profile.
- **Per-item reveal.** A labelled reveal button beside a guarded row's Play, a "Reveal details"
  item in its More menu, and a click on the placeholder still reveal that one item for the
  browser session only (in memory; a reload hides it again). A one-off peek should not quietly
  become a permanent setting.
- Reveal transitions are an opacity change, removed under `prefers-reduced-motion`. No blur or
  other visual effect code is added.

## Decisions

- No GraphQL, store or migration change. The keys ride the existing `clientPref` /
  `setClientPref` seam (`profile_client_prefs`), which the server stores without interpreting,
  like `playback.skip-interval.show.<id>`. Native clients can read the same keys (CORE-1707).
- Core stays domain-agnostic: the rule reads only the core media class, container and
  engagement fields. UI copy says "unwatched" because the guard only applies to video Media, the
  same wording the existing Watched marks use.

## Non-goals

- The item's own detail page is unchanged: opening it is an explicit choice.
- Audio and photo children are never guarded.
- Native clients (CORE-1707).

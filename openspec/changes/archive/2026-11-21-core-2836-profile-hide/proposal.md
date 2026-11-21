# Hide items and hide watched, per Profile

Ticket: CORE-2836

## Why

A viewer wants to hide a show they are not interested in and have it stay hidden, so each person in
a household sees the catalog their own way. A viewer also wants to hide what they have already
watched. Both must be quick to turn on and off.

The per-Profile Mute (ADR-0054) already removes muted targets from shelves and library browse for
one Profile, but it reaches only an Entity's directly related Media, cannot target a single Media
item, keeps a muted Entity in an unfiltered Entity browse, and has no web UI.

## What Changes

- Mute gains a `media` kind: one Media item, muted directly.
- A direct Entity Mute also reaches the Entity's descendants along the parent relations the active
  plugins declare, so hiding a show removes its seasons and episodes from Continue Watching, Up
  Next, Recently Added and every library browse.
- Entity browse drops an Entity the Profile has muted directly, filtered or not.
- Changing a Mute tells the Profile's other sessions to refetch.
- A server-read client pref `profile.hide-watched` (`"on"` on, absent or anything else off) removes
  Media the Profile has finished from catalog shelves and from library browse when the browse names
  no watch filter. The Profile's own lists (Continue Watching, Up Next, Recently/Most Played,
  Watchlist, Favorites, Rated, Queue) and an explicit watch filter ignore it.
- Web: Hide / Unhide in the card More menu and on the detail page, with an inline Undo; a Hide
  watched switch on the library toolbar and in Profile settings; a Hidden items count with Unhide
  all in Profile settings.

No wire shape changes: `setMute` and `clearMute` already take `kind` as a string, and the web reads
hidden state from the existing `mutes` list.

## Non-goals

- Apple and Android UI (filed as follow-up tickets).
- Hiding from search or direct reads: search and a direct link still reach a hidden item, which is
  how a viewer finds it to unhide it.
- A titled Hidden list in settings.

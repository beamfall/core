# Sidebar "Your stuff" group and phone tab bar

Ticket: CORE-2827 (`queue:core:main`).

## Why

A UX time-to-play audit found that the web Viewer sidebar holds only Home and libraries. Playlists,
Favorites and History sit in the account menu (avatar, then item), so reaching a playlist takes two
or more clicks and is hard to discover. At phone width there is no bottom navigation: every
library is menu button, then library.

## What Changes

- The sidebar gains a "Your stuff" group with Playlists, Favorites and History, linking to the
  existing `/playlists`, `/favorites` and `/history` pages. The account menu keeps its links.
- The per-Profile sidebar editor (CORE-2435) edits the group too: each entry can be unpinned
  (hidden from the sidebar) and the entries reordered. The choice is stored in the existing
  `web.sidebar-libraries` client preference under a new optional `personal` member; stored values
  without it still parse, and a choice that leaves the group alone serializes as before.
- At phone width (the shell's existing 760 px breakpoint) a bottom tab bar offers Home, Search,
  Libraries and Your stuff. Home and Search are links; Libraries and Your stuff open the existing
  navigation drawer with focus on that group. The page content is padded so the bar never covers
  it, the bar honours safe-area insets, and the docked mini player sits above it.
- New strings ship in every catalog locale.

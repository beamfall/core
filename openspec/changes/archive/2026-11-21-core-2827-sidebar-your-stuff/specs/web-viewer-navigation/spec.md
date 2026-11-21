## ADDED Requirements

### Requirement: The sidebar lists the Profile's own lists

For a signed-in Profile the web Viewer sidebar SHALL show a "Your stuff" group linking to the
existing Playlists, Favorites and History pages, on the expanded sidebar, the collapsed icon rail
and the phone drawer, so each is one activation from any Viewer page. The account menu SHALL keep
its links to the same pages.

#### Scenario: Playlists from any page

- **WHEN** a signed-in Profile on a desktop-width Viewer page activates "Playlists" in the sidebar
- **THEN** the Playlists page opens and its sidebar entry carries `aria-current="page"`

### Requirement: Profiles pin, unpin and reorder the Your stuff group

The sidebar editor SHALL let the Profile unpin and reorder the "Your stuff" entries, by keyboard
as well as by drag. Unpinned entries SHALL leave the sidebar. The choice SHALL be stored in
`web.sidebar-libraries` under an optional `personal` member; values stored without it SHALL still
parse, and a choice that leaves the group untouched SHALL serialize without it.

#### Scenario: Profile hides and reorders the group

- **WHEN** a Profile moves "History" first, unpins "Favorites" and presses Done
- **THEN** the group lists "History" then "Playlists", and Favorites stays in the account menu

#### Scenario: Preference stored before the group existed

- **WHEN** the stored `web.sidebar-libraries` value holds only `order` and `hidden`
- **THEN** the library choice applies unchanged and the group shows all three entries in default order

### Requirement: Phone width offers a bottom tab bar

At the Viewer's phone width the shell SHALL show a bottom navigation landmark with Home, Search,
Libraries and Your stuff. Home and Search SHALL be links that carry `aria-current="page"` on their
own page; Libraries and Your stuff SHALL open the navigation drawer with focus in that group. The
bar SHALL NOT cover page content and SHALL respect safe-area insets. Wider layouts SHALL NOT show it.

#### Scenario: One tap at 375 px

- **WHEN** a Profile on a 375 px wide Viewer page taps Home, Search or Libraries
- **THEN** Home or Search opens directly, or the drawer opens with focus on the first library

## ADDED Requirements

### Requirement: Each Profile can save its Home layout on the server

The server SHALL store, per Profile, an ordered list of Home section ids and a
set of hidden Home section ids, keyed by the generic manifest section ids.
`setHomeLayout(sectionOrder, hiddenSectionIDs)` SHALL save it for the session's
own Profile and `resetHomeLayout` SHALL clear it. Both SHALL refuse a session
without a Profile.

#### Scenario: Profile reorders and hides sections

- **WHEN** a Profile saves an order placing Recently Added first and hides Watchlist
- **THEN** its Navigation Manifest Home layout lists Recently Added first and omits Watchlist

#### Scenario: Reset to default

- **WHEN** a Profile with a saved layout calls `resetHomeLayout`
- **THEN** `homeLayout` reports no saved preference and the manifest Home layout returns to the default order

#### Scenario: Preference is per Profile

- **WHEN** one Profile saves a layout
- **THEN** another Profile's Home layout is unchanged

### Requirement: Home layout writes are validated

`setHomeLayout` SHALL drop duplicate ids and ids the Profile does not currently
receive, and SHALL refuse a list over 500 entries. `homeLayout` SHALL return
every Home section the Profile receives, in effective order, each with its
title and hidden flag, and whether a preference is saved.

#### Scenario: Oversized list

- **WHEN** a client sends 501 section ids
- **THEN** the call is refused and the saved layout is unchanged

#### Scenario: Read includes hidden sections

- **WHEN** a Profile hid Watchlist
- **THEN** `homeLayout` lists Watchlist with `hidden: true`

### Requirement: Home layout tolerates added and removed sections

Sections absent from the saved order SHALL follow the ordered sections in their
default order. Saved ids naming a section that no longer exists SHALL be
ignored without error.

#### Scenario: Library added after customisation

- **WHEN** a Smart Collection shelf appears after the Profile saved its order
- **THEN** that shelf appears at the end of the Profile's Home layout

#### Scenario: Removed section

- **WHEN** a saved id names a section that is gone
- **THEN** the manifest and `homeLayout` ignore it

### Requirement: Customising Home grants no access

The preference SHALL only order and hide sections the Profile already receives
after Library access and the owner's server-level layout override are applied.
A section hidden server-wide SHALL stay hidden, and a managed or child Profile
SHALL see no section it would not see without a preference.

#### Scenario: Profile names a section it cannot see

- **WHEN** a managed Profile saves an order naming a section outside its Libraries or one the owner hid
- **THEN** the id is dropped and the section stays absent from its Home

### Requirement: Default Home leads with Continue Watching

With no saved preference, the manifest Home layout SHALL place the Continue
Watching section first, whatever order the server-level override gives it.

#### Scenario: Owner override places Continue Watching later

- **WHEN** the server override orders Recently Added before Continue Watching and the Profile saved nothing
- **THEN** the Profile's manifest Home layout lists Continue Watching first

#### Scenario: Partial order under an owner override

- **WHEN** the Profile's saved order names only some sections
- **THEN** the unlisted sections follow in the default order, Continue Watching first

### Requirement: The preference changes Home only

A Library page that opens the plugin-less default layout SHALL NOT change when
its Profile customises Home: once a preference exists, such Library nodes open
`layout-library`, the default Home layout before the preference. A save SHALL
keep the saved setting of sections the Profile cannot see at that moment (a
restricted Library while relocked).

#### Scenario: Customised Home, plugin-less Library

- **WHEN** a Profile hides Continue Watching on Home and opens a plugin-less Library
- **THEN** that Library page still lists every default section, Continue Watching first

#### Scenario: Save while relocked

- **WHEN** a Profile saves Home while relocked after hiding a restricted Library's shelf while revealed
- **THEN** that shelf stays hidden when the Profile reveals again

## ADDED Requirements

### Requirement: Profiles pin, unpin and reorder sidebar libraries

The web Viewer sidebar SHALL let the signed-in Profile pin, unpin and reorder its libraries, by
keyboard as well as by drag, and SHALL store that choice per Profile as the client preference
`web.sidebar-libraries`. Unpinned libraries SHALL remain reachable from a "More" disclosure.

#### Scenario: Profile reorders and unpins

- **WHEN** a Profile moves "Music" above "Films", unpins "Photos" and presses Done
- **THEN** the sidebar lists "Music" then "Films", "Photos" appears only under More, and the
  choice is saved as `web.sidebar-libraries` for that Profile only

### Requirement: Profile sidebar choice layers over the server navigation override

The Profile's sidebar choice SHALL apply after the server-wide navigation override: it SHALL only
reorder or unpin libraries present in the server's navigation, SHALL ignore library IDs the server
no longer lists, and SHALL place libraries it does not name after the ordered ones, in server order.

#### Scenario: Server override hides a library

- **WHEN** the server-wide navigation override hides a library the Profile had pinned
- **THEN** the sidebar does not list it, pinned or under More

#### Scenario: New library

- **WHEN** a library the Profile's saved choice does not name is added
- **THEN** it appears pinned after the libraries the choice orders

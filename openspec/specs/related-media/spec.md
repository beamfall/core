# related-media Specification

## Purpose
How the graph's `relatedMedia` read picks Media for the web Media detail page's Related row: Media sharing a category-archetype credit with the item, ranked by shared category and leading person credits, filtered by discovery suppression.

## Requirements

### Requirement: Related Media read

The graph SHALL answer `relatedMedia(mediaID, limit)` with Media that share at least one
category-archetype credit with the named Media, ordered by how many of its first four
category-archetype credits and first three person-archetype credits (in the server's credit
order) they share, most first, ties in first-read order. The named Media SHALL NOT be listed. The
limit SHALL default to 20 and be capped at 50.

#### Scenario: Ranked by shared credits

- **WHEN** one Media shares two of the item's category credits and another shares one
- **THEN** `relatedMedia` lists the first before the second, without the item itself

#### Scenario: Second billed person counts

- **WHEN** the item's person credits are, in order, A, B, C and D and a Media shares a category
  and person B
- **THEN** that Media ranks above one that shares only the category

#### Scenario: No category credits

- **WHEN** the item has no category credits
- **THEN** `relatedMedia` returns an empty list

### Requirement: Related Media follow discovery suppression

`relatedMedia` SHALL read each credit's Media through the `entity-media` Source with the acting
profile, bounded per credit, so Media the profile muted are left out, Media outside the session's
Libraries or reveal level are omitted, and locked Media are marked, as on browse and Home rails.
An unknown or hidden `mediaID` SHALL answer with an error that does not tell the two apart.

#### Scenario: Muted Media left out

- **WHEN** the profile muted a Media that shares the item's category
- **THEN** `relatedMedia` does not list it

#### Scenario: Hidden item

- **WHEN** `mediaID` names a Media outside the session's Libraries
- **THEN** `relatedMedia` answers "unknown media"

### Requirement: Related rails exclude the requested edition group

`relatedMedia` SHALL omit the requested Media and every Media in its edition group,
including its parent and sibling editions. It SHALL use the edition-folded `entity-media`
Source by passing the requested Media ID as `excludeEditionGroupOf`, folding and excluding
the requested group before each credit's bounded read. Plain `entity-media` reads without
this argument remain unchanged; folding browse and Entity pages is a separate decision.

#### Scenario: Parent detail

- **WHEN** an edition points to the requested parent with an `edition-of` relation
- **THEN** neither the parent nor its editions appear in Related

#### Scenario: Edition detail

- **WHEN** the requested Media is an edition of a parent with another edition
- **THEN** the parent, the requested edition and its sibling edition are absent from Related

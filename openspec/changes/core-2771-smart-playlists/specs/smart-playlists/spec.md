## ADDED Requirements

### Requirement: Smart Collections accept rule sources

A Smart Collection `sourceExpr` SHALL accept `kind: "rules"` with `match` (`all` or `any`), up to
20 rules of `field`, `op` and `value`, and an optional `limit` from 1 to 5000. Fields SHALL be the
built-ins `title`, `duration`, `year`, `playCount`, `lastPlayed` and `rating`, a plugin Media field
`field:<name>`, or a plugin entity relation `entity:<kind>`. Create and update SHALL reject an
unknown field or operator, a value of the wrong type, or an out-of-range limit as a validation error.

#### Scenario: Valid rule source

- **WHEN** a manager creates a Smart Collection whose source matches any of two genre rules with a
  limit of 25
- **THEN** the collection is stored and reading it returns at most 25 matching Media

#### Scenario: Malformed rule source

- **WHEN** a manager saves a rule with operator `gt` on a text field
- **THEN** the mutation fails with a validation error and nothing is stored

### Requirement: Rule sources evaluate live for the reading Profile

Reading a rule Smart Collection SHALL evaluate its rules on the server at read time against the
collection's Library, joined with `all` as AND and `any` as OR. Engagement fields SHALL use the
reading Profile's play count, last-played time and rating.

#### Scenario: Play history refreshes membership

- **WHEN** a Profile plays a track and then reads a collection with the rule `playCount gte 1`
- **THEN** the track is in the list, and it is absent for a Profile that has not played it

### Requirement: Rule results respect access filters

A rule Smart Collection result SHALL pass the same Library visibility, certification, extras,
file-presence, trash, mute and hide-watched filters as other sources.

#### Scenario: Hidden Library

- **WHEN** a session that cannot see the collection's Library reads it
- **THEN** the list is empty

### Requirement: Rule results are sorted and capped

`sortExpr` `{"by","dir"}` SHALL order a rule result, with title ascending as the default, and the
rule limit SHALL cap the whole list across pages.

#### Scenario: Limit across pages

- **WHEN** a collection with limit 3 is read in pages of 2
- **THEN** the first page has two items, the second has one, and the third is empty

### Requirement: Web builds and plays smart playlists

The web Playlists page SHALL list a Library's rule Smart Collections as smart playlists. A manager
SHALL be able to create, edit and delete one with a rule builder offering match all/any, rule rows,
sort, direction and limit. A smart playlist page SHALL show the current items and start playback of
the list in order.

#### Scenario: Play a smart playlist

- **WHEN** a viewer opens a smart playlist and presses Play all
- **THEN** playback starts at the first item with the rest of the list queued

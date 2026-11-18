# web-media-addresses Specification

## Purpose
Give watch and detail pages in the web viewer readable addresses built from generic display fields, that still resolve when names change and never leak a restricted title.

## Requirements

### Requirement: Watch and detail addresses are readable

The web viewer SHALL address a content-addressed Media item on the watch and detail routes as
`/<route>/<library-words>/<item-words>-<token>`, where the token is the digest of the item's Media ID
and the words come only from generic display fields: the Library's name, the item's title, and its
first person credit (audio) or container credit (anything else).

#### Scenario: Audio item in a named Library

- **WHEN** a viewer opens the track "38 Planes (Reprise)" by "Come From Away Company" in the
  Library "Music"
- **THEN** the address reads `/web/watch/music/come-from-away-company-38-planes-reprise-<token>`

### Requirement: Address words are folded and capped

Address words SHALL be lowercase ASCII, with accents folded, apostrophes and other punctuation
dropped and runs of other characters collapsed to one hyphen, cut at a word boundary within 60
characters. Text with no Latin letters or digits SHALL yield no words, and the address SHALL then
use the Media ID alone.

#### Scenario: Accented title

- **WHEN** an item is titled "Amélie — Le Fabuleux Destin"
- **THEN** its words read `amelie-le-fabuleux-destin`

#### Scenario: Title with no Latin letters

- **WHEN** an item's words fold to nothing
- **THEN** the address carries the Media ID alone

### Requirement: Old and stale addresses resolve to the canonical one

The web viewer SHALL resolve a watch or detail address by its token alone, and SHALL accept a
bare Media ID. Once the item is loaded it SHALL replace the address with the canonical one in
place, without adding a history entry and without restarting playback.

#### Scenario: Legacy bare-ID link

- **WHEN** a viewer opens `/web/watch/media-sha256-<digest>`
- **THEN** the same item plays and the address becomes its readable address

#### Scenario: Renamed item

- **WHEN** a viewer opens an address whose words no longer match the item
- **THEN** the item opens and the address is corrected

### Requirement: Restricted titles stay out of addresses

The web viewer SHALL keep the bare-ID address for any Media ID that is not content-addressed and
for every item shown in a Revealed session.

#### Scenario: Revealed session

- **WHEN** a Revealed session opens an item by its readable address
- **THEN** the address is replaced by the bare-ID address

### Requirement: Navigating cards are links

Every card that navigates SHALL be an anchor with an href to the page it opens, so modifier and
middle clicks open a new tab. Its More menu SHALL stay a separate control.

#### Scenario: A card has an href

- **WHEN** a related container card renders
- **THEN** it is a link whose href is that Entity's page

### Requirement: Entity pages have readable addresses

An Entity page address SHALL read `/entity/<words>-<token>`, where the token is the hex digest of a
content-derived Entity ID (`entity-` plus 32 hex digits) and the words are the folded title. A
middle container's words SHALL lead with its ancestor's title, in the same single path segment.

#### Scenario: Middle container address

- **WHEN** a container titled "Season 1" under "3 Body Problem" is shown
- **THEN** the address is `/entity/3-body-problem-season-1-<hex>`

### Requirement: Entity addresses resolve without a lookup

The client SHALL resolve the Entity from the token alone, SHALL keep resolving a bare
`/entity/<id>`, and SHALL rewrite the address in place once the Entity is known. A Revealed
session SHALL keep the bare-ID address, and an ID of any other shape SHALL stay bare.

#### Scenario: Bare address is rewritten

- **WHEN** `/entity/entity-<hex>` opens a container titled "3 Body Problem"
- **THEN** the address becomes `/entity/3-body-problem-<hex>` with no new history entry

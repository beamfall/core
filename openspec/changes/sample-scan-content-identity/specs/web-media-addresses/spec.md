## MODIFIED Requirements

### Requirement: Watch and detail addresses are readable

The web viewer SHALL address a content-addressed Media item on the watch and detail routes as
`/<route>/<library-words>/<item-words>-<token>`. The words SHALL come from the Library's name,
the item's title, and its first person credit (audio) or container credit (anything else).
The token for a SHA-256 Media ID SHALL be its digest digits. The token for a sampled Media ID
SHALL be `sample1.` followed by its digest digits.

#### Scenario: Audio item in a named Library

- **WHEN** a viewer opens the track "38 Planes (Reprise)" by "Come From Away Company" in the
  Library "Music"
- **THEN** the address reads `/web/watch/music/come-from-away-company-38-planes-reprise-<token>`

### Requirement: Address words are folded and capped

Address words SHALL be lowercase ASCII, with accents folded, apostrophes and other punctuation
dropped and runs of other characters collapsed to one hyphen, cut at a word boundary within 60
characters. If item words fold to nothing, the viewer SHALL use the Media ID as the last path
segment, except that a sampled address qualified by a Library SHALL use the sampled token.

#### Scenario: Accented title

- **WHEN** an item is titled "Amélie — Le Fabuleux Destin"
- **THEN** its words read `amelie-le-fabuleux-destin`

#### Scenario: Title with no Latin letters

- **WHEN** an item's words fold to nothing
- **THEN** the address carries the Media ID, or the sampled token after the Library words

### Requirement: Old and stale addresses resolve to the canonical one

The web viewer SHALL resolve a watch or detail address by its token and SHALL accept a bare Media
ID. An unqualified address spelling a sampled Media ID SHALL try that ID first and fall back to the
SHA-256 Media ID with the same digits only after a confirmed miss. Once the item is loaded, the
viewer SHALL replace the address in place without adding a history entry or restarting playback.

#### Scenario: Legacy bare-ID link

- **WHEN** a viewer opens `/web/watch/media-sha256-<digest>`
- **THEN** the same item plays and the address becomes its readable address

#### Scenario: Renamed item

- **WHEN** a viewer opens an address whose words no longer match the item
- **THEN** the item opens and the address is corrected

#### Scenario: Identical digest prefix across schemes

- **WHEN** a sampled Media ID and a legacy Media ID have the same 17 digest digits
- **THEN** the viewer resolves their tagged and untagged addresses to their respective IDs

#### Scenario: Generating addresses for a legacy title with the sampled ID slug

- **WHEN** a legacy title has the slug `media-sample1`
- **THEN** the viewer qualifies its readable SHA-256 address with the Library slug, or generates
  the bare SHA-256 ID when the Library slug is unavailable

#### Scenario: Previously issued unqualified legacy bookmark

- **WHEN** a viewer opens `/watch/media-sample1-a371bff46385f7ded` and the sampled ID is absent
- **THEN** the viewer resolves the bookmark to `media-sha256-a371bff46385f7ded`

#### Scenario: Temporary lookup failure

- **WHEN** the sampled lookup for an ambiguous bookmark fails temporarily
- **THEN** the viewer retries while the address and viewer identity remain current and selects
  the legacy ID only after a confirmed sampled miss

#### Scenario: Cached sampled detail while offline

- **WHEN** the current viewer opens a bare sampled ID with cached detail, a viewer-bound card
  preview, or a matching item in a viewer-cached section
- **THEN** the viewer can render the sampled item immediately while the address revalidates
- **AND** the viewer keeps the bookmark until the server confirms which item it identifies

#### Scenario: Permanent lookup failure

- **WHEN** the sampled lookup for an ambiguous bookmark fails permanently, including an
  authorization rejection
- **THEN** the viewer shows its existing unavailable message without retrying or selecting the
  legacy ID

#### Scenario: Expand the docked player

- **WHEN** the listener expands the docked player for a sampled ID
- **THEN** the active playback session survives address resolution and attaches to the full player

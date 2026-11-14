# metadata-provider-attribution Specification

## Purpose
How Beamfall credits the providers its metadata came from: identification records which source supplied each field, Media reads carry that source's declared attribution, credits list each active attribution once, and the web viewer renders it as a safe external link.

## Requirements

### Requirement: Identification records metadata provenance

When identification applies a candidate's metadata to a Media item, the server SHALL record the
plugin and scraper that supplied it for that item, replacing any earlier record. Deleting the Media
item SHALL delete its record.

#### Scenario: First identification

- **WHEN** a Media item is identified through a plugin's scraper
- **THEN** the item's provenance names that plugin and scraper

#### Scenario: Re-identification with another scraper

- **WHEN** an identified Media item is identified again through a different scraper
- **THEN** the item's provenance names only the newer scraper

### Requirement: Media reads carry the declared attribution of their source

The `media(id)` read SHALL include `metadataAttribution` with the attribution text, link and scraper
display name declared by the active bundle for the item's recorded plugin and scraper. It SHALL omit
`metadataAttribution` when the item has no provenance, the recorded plugin is not active, the
recorded scraper no longer exists in the bundle, or the scraper declares no attribution. The text
and link SHALL come only from plugin data.

#### Scenario: Item identified through a scraper with an attribution

- **WHEN** a viewer reads a Media item identified through a scraper that declares an attribution
- **THEN** the read carries that attribution's text and link

#### Scenario: Recorded plugin is not active

- **WHEN** a viewer reads a Media item whose recorded plugin is not active
- **THEN** the read carries no `metadataAttribution`

#### Scenario: Scraper declares no attribution

- **WHEN** a viewer reads a Media item identified through a scraper without an attribution
- **THEN** the read carries no `metadataAttribution`

### Requirement: Credits list every active attribution once

The server SHALL offer a session-scoped `metadataCredits` read listing the attribution of every
active plugin's scrapers that declare one, in plugin activation order then scraper declaration
order, with entries of identical text and link listed once. Console scraper details SHALL carry
each scraper's attribution.

#### Scenario: Two scrapers share one attribution

- **WHEN** two active scrapers declare the same attribution text and link
- **THEN** `metadataCredits` lists that attribution once

#### Scenario: No scraper declares an attribution

- **WHEN** no active scraper declares an attribution
- **THEN** `metadataCredits` is an empty list

### Requirement: The web viewer renders attribution as a safe external link

The web viewer SHALL show a Media item's `metadataAttribution` in the player's Media info panel and
SHALL list `metadataCredits` in a Credits section of Profile settings. An attribution whose link is
an `https:` URL SHALL render as a link that opens in a new browsing context with
`rel="noopener noreferrer"`; any other link SHALL render as plain text without a link.

#### Scenario: Attribution with an https link

- **WHEN** the viewer opens the info panel for an item whose attribution links to an https site
- **THEN** the panel shows the attribution text as a link to that site with `rel="noopener noreferrer"`

#### Scenario: Attribution with a non-https link

- **WHEN** an attribution's link is not an https URL
- **THEN** the viewer shows the text without a link

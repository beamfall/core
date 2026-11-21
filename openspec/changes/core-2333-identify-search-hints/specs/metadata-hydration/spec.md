## ADDED Requirements

### Requirement: Identification searches carry the item's search hints

When the server searches a scraper to identify an item, it SHALL substitute each `{hint.<key>}`
placeholder in the scraper's SearchTemplate URL query with the item's hint value for that key,
escaped as a URL query value. A missing or empty hint SHALL substitute the empty string. A scraper
whose required search hints the item does not all supply SHALL be skipped for that search.

#### Scenario: Hint values reach the query escaped

- **WHEN** an item with hint `creator` = `A & B` is searched with a template
  `/search?q={query}&by={hint.creator}`
- **THEN** the request query is `q=<title>&by=A+%26+B`

#### Scenario: Missing optional hint

- **WHEN** the item has no value for a hint the template names and the scraper does not require it
- **THEN** the placeholder renders as the empty string and the search runs

#### Scenario: Missing required hint

- **WHEN** the item has no value for a hint the scraper lists as required
- **THEN** the scraper is not searched for that item, and automatic identification uses the next
  searchable scraper for the item's class

### Requirement: An item's search hints come from its parser and embedded tags

The server SHALL take an item's search hints from its plugin parser, and SHALL fill each hint the
parser leaves empty from the file's non-empty embedded tag that the plugin bundle's embedded-tag
hint map names for it.

#### Scenario: Embedded tags fill parser gaps

- **WHEN** the parser gives no value for a hint and the file has a non-empty embedded tag the
  bundle maps to that hint
- **THEN** the tag value is the hint; a parser-supplied value is kept over the tag

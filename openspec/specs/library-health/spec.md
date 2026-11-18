# library-health Specification

## Purpose
Library health reports only the dimensions a library's plugin can actually provide, so operators see real gaps instead of counts for metadata the plugin never supplies.

## Requirements

### Requirement: Health dimensions follow what a library's plugin can do

Library health SHALL omit from a library's counts every dimension that does not apply to its
plugin: `unmatched` without a searchable scraper, `scrape-failure` without auto-identification,
`missing-artwork` when no scraper supplies artwork, and `no-scraper` when a scraper can search.
`broken` and `duplicate` SHALL always apply. A dimension that does not apply SHALL return no
drill-down items.

#### Scenario: Library with no plugin

- **WHEN** an owner reads library health for a library that no plugin owns
- **THEN** its counts carry `no-scraper`, `broken` and `duplicate` and no `unmatched`,
  `scrape-failure` or `missing-artwork` key

#### Scenario: Plugin with a scraper but no auto-identification

- **WHEN** the library's plugin has a searchable scraper and registers no scorer
- **THEN** its counts carry `unmatched` and no `scrape-failure` key

### Requirement: Local metadata identifies a Media

A Media SHALL NOT count as unmatched, scrape failure or no-scraper when it carries an external ID
or, for a plugin that does not auto-identify, when the plugin's local hydration marked it complete.

#### Scenario: Tagged audio

- **WHEN** a library's items were hydrated from local tags and the plugin does not auto-identify
- **THEN** those items are not counted as unmatched

### Requirement: Scrape failures count attempted scrapes

`scrape-failure` SHALL count only unidentified Media that an automatic scrape attempted and queued
as unmatched. A Media queued for manual review, or never scraped, SHALL NOT count.

#### Scenario: Never scraped

- **WHEN** an unidentified Media has no unmatched-queue row from a scraper
- **THEN** it is not counted as a scrape failure

### Requirement: Missing artwork checks the archetype's slots

`missing-artwork` SHALL count a Media only when none of the artwork slots its archetype's scrapers
supply, nor the poster those slots fall back to, holds applied or stored non-generated artwork.

#### Scenario: Episode with a thumb

- **WHEN** an episode-like Media has a thumb and no poster, and its plugin's episode scraper
  supplies a thumb
- **THEN** it is not counted as missing artwork

### Requirement: Remediation is offered only where it can help

The console SHALL show a dimension that does not apply as not applicable, with no drill-down and
no remediation buttons, and SHALL say that no plugin identifies the library when `no-scraper` is
non-zero. Whole-library remediation for a dimension that does not apply SHALL be refused.

#### Scenario: Plugin-less library in the console

- **WHEN** the owner opens library health for a library with no plugin
- **THEN** unmatched, scrape-failure and missing-artwork show "Not applicable" and no Fix match,
  Rescan or Fetch button is offered

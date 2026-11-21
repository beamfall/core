## ADDED Requirements

### Requirement: The Home hero names what it features

The Home hero SHALL be headed the way a card for its item is headed, so an item held by a container
names the container with the item's own line beneath it. It SHALL show the item's byline, year,
content rating, runtime and audience score where known, and SHALL NOT repeat any of them in its
summary. It SHALL take at most 340px of height at desktop width.

#### Scenario: An episode is featured

- **WHEN** Home features episode 9 of season 34 of "The Graham Norton Show", rated TV-14, 49 minutes, 2026, score 7.4
- **THEN** the hero is headed "The Graham Norton Show", reads "S34 · E9 · Episode 9" beneath it, and lists 2026, TV-14, 49 min and ★ 7.4

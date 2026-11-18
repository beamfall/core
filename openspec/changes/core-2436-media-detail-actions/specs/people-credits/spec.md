## MODIFIED Requirements

### Requirement: Web cast row on the Media detail page

The web Media detail page SHALL show the Media item's person-archetype credits as a row of cards
in credit order. Each card SHALL show the headshot (or the name's initial when there is none), the
name, and the credit's character or role attribute when present, and SHALL link to the person
page. Credits with a `department` attribute SHALL be grouped under a heading naming that
department, even when every credit shares one department. Audio Media names its people under the
title instead and SHALL NOT show the cast row, so a name is never listed twice.

#### Scenario: Cast with characters

- **WHEN** a video Media item has person credits with character attributes
- **THEN** the page shows a cast row listing each person with the character beneath the name

#### Scenario: One department

- **WHEN** every person credit carries the same `department` attribute
- **THEN** the cast row shows that department as a heading above its cards

#### Scenario: No person credits

- **WHEN** a Media item has no person-archetype credits
- **THEN** no cast row is shown

#### Scenario: Audio item with credited people

- **WHEN** an audio Media item has person credits, with or without headshots
- **THEN** each person is named once, linked under the title, and no cast row is shown

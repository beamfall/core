## ADDED Requirements

### Requirement: Poster cards offer a one-click Play

A playable, unlocked Media card drawn as a poster or thumbnail SHALL offer a Play button that is a
sibling of the card link and its More trigger. It SHALL show on hover and keyboard focus, stay
visible without hover, be named Play or Resume (from the card's progress) with the item's title, and
open the player through the card's play path with the card's queue.

#### Scenario: Play from a poster

- **WHEN** a viewer activates the Play button on a Media poster card
- **THEN** `/watch/<id>` opens with the card's queue, and the card link still opens `/media/<id>`

#### Scenario: Keyboard

- **WHEN** a keyboard user tabs from the card link
- **THEN** focus reaches the card's Play button, which shows and is named "Play <title>" or
  "Resume <title>"

#### Scenario: Rows and locked items

- **WHEN** a card is drawn as a list or table row, or its item is locked
- **THEN** it shows no extra Play button

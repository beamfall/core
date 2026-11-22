## ADDED Requirements

### Requirement: Remove from Continue Watching

On a shelf bound to the `continue-watching` Source, a Media card's More menu SHALL offer "Remove
from Continue Watching", and cards elsewhere SHALL NOT. Choosing it SHALL take the card off the row
at once, call `setContinueWatchingHidden` with `hidden: true`, and show the Undo notice naming the
item.

#### Scenario: Remove

- **WHEN** a viewer chooses "Remove from Continue Watching" on a card
- **THEN** the card leaves the row at once and the notice names the item and offers Undo

#### Scenario: Other shelves

- **WHEN** a viewer opens the More menu of a card on a Recently Added shelf
- **THEN** the menu does not offer "Remove from Continue Watching"

#### Scenario: Last card

- **WHEN** a viewer removes the only card on the row
- **THEN** the row disappears and Home treats it as an empty Continue Watching shelf

### Requirement: Continue Watching removal Undo and failure

Undo SHALL call `setContinueWatchingHidden` with `hidden: false` and return the card to the row. A
removed card SHALL stay off each row until that row holds a read started after the write settled,
and that read's answer SHALL stand. A failed write SHALL return the card and report the failure in
the notice, with no Undo. A removal or Undo started under one Profile SHALL NOT notify or write
under another.

#### Scenario: Undo

- **WHEN** a viewer chooses Undo on the removal notice
- **THEN** the card returns to the row

#### Scenario: Write fails

- **WHEN** the removal cannot be saved
- **THEN** the card returns to the row and the notice says the change did not save, with no Undo

#### Scenario: Played again

- **WHEN** a read of the row started after the removal settled lists the card again
- **THEN** the card shows on that row

#### Scenario: Profile switch

- **WHEN** the Profile changes while a removal is pending, or before its Undo
- **THEN** no notice shows for the new Profile and the Undo writes nothing

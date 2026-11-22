## ADDED Requirements

### Requirement: Change poster control

For a viewer who may edit metadata (a manager device on an adult Profile), the page SHALL offer a
labelled "Change poster" control over the poster. It SHALL be reachable by keyboard, shown on focus
and on hover, and stay visible where the device cannot hover. Other viewers SHALL NOT see it, nor
SHALL it show when the frame holds a wide still instead of a poster.

#### Scenario: Viewer without edit rights

- **WHEN** a child Profile or a non-manager device opens the page
- **THEN** no Change poster control is rendered

### Requirement: Poster picker dialog

The Change poster control SHALL open a dialog listing the item's poster candidates from the
existing artwork candidates query, with the applied one marked. Choosing a candidate SHALL pin it
through the existing pin mutation and reload the item, and focus SHALL return to the control when
the dialog closes.

#### Scenario: Admin picks a new poster

- **WHEN** a manager on an adult Profile opens Change poster and chooses a candidate
- **THEN** the candidate is pinned to the poster slot, the dialog closes and the item is reloaded

#### Scenario: No candidates

- **WHEN** the candidates query returns none
- **THEN** the dialog says no candidates are available

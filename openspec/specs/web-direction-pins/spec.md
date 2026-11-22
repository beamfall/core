# web-direction-pins Specification

## Purpose
Defines how web setup lets people choose a four-direction PIN for TV remotes and warns them when a numeric PIN cannot be entered that way.

## Requirements

### Requirement: Web PIN setup offers directions

Manage Restrictions and Console Profiles SHALL offer Digits and Directions when
setting the Restrictions PIN or an adult Profile's entry PIN, respectively. Direction
entry SHALL show the sequence as arrows and accept both arrow buttons and keyboard
arrow keys. Setup SHALL explain that swipes are easier to shoulder-surf than keypad taps.

#### Scenario: Set and confirm a direction PIN

- **WHEN** the person enters up, left, right, down, up, left in both direction fields
- **THEN** both fields display those six arrows
- **AND** the existing PIN mutation receives `246824`, the same string used for digit entry

#### Scenario: A direction sequence is incomplete or mismatched

- **WHEN** the direction PIN has fewer than six steps or its confirmation differs
- **THEN** setup prevents the PIN from being saved

### Requirement: Web setup warns when a digit PIN cannot be entered on a TV remote

In Digits mode, setup SHALL show a non-blocking warning for a PIN containing a digit
outside 2, 4, 6 and 8.

#### Scenario: Digits outside the TV keypad mapping

- **WHEN** the person enters `1234` in the new PIN field
- **THEN** setup warns that the PIN cannot be entered with a TV remote and permits saving
  after confirmation

#### Scenario: Digits within the TV keypad mapping

- **WHEN** the person enters `246824` in the new PIN field
- **THEN** setup shows no TV remote compatibility warning

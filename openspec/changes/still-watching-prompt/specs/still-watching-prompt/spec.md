## ADDED Requirements

### Requirement: Shared per-Profile setting

The number of autoplayed items before the prompt SHALL be stored per Profile as the client preference
`stillwatching.after`, with the value `1` to `10` or `off`. Every client SHALL read the same key and
SHALL treat an absent or malformed value as 3.

#### Scenario: Setting absent

- **WHEN** a Profile has no `stillwatching.after` preference
- **THEN** the player asks after 3 autoplayed items

#### Scenario: Setting off

- **WHEN** a Profile's `stillwatching.after` is `off`
- **THEN** the player never shows the prompt

#### Scenario: Malformed value

- **WHEN** a Profile's `stillwatching.after` is `0`, `11`, `02`, ` 2` or not a number
- **THEN** the player treats it as 3

### Requirement: Autoplay streak

A player SHALL count the items in a row that the Up Next countdown started on its own. Viewer input
in the player, Play now, picking an item and Continue on the prompt SHALL reset the count to zero.
The count SHALL live in memory only.

#### Scenario: Countdown starts items

- **WHEN** the countdown starts three items in a row with no input
- **THEN** the streak is 3

#### Scenario: Viewer input

- **WHEN** the viewer taps, clicks or presses a key in the player
- **THEN** the streak resets to 0

### Requirement: Prompt replaces autoplay

When an item ends with a playable next item and the streak has reached the setting, the player SHALL
stop and show the prompt instead of starting the next item. While the prompt is armed the Up Next
countdown SHALL NOT show. Continue SHALL start the next item and reset the streak; Stop SHALL close
the prompt without starting it.

#### Scenario: Setting reached

- **WHEN** the setting is 3 and the third autoplayed item ends with a next item queued
- **THEN** playback stays stopped behind "Are you still watching?" until the viewer chooses

#### Scenario: Nothing queued

- **WHEN** an item ends with no playable next item
- **THEN** no prompt is shown

### Requirement: Paths without a countdown are exempt

A player path that advances to the next item with no Up Next countdown SHALL neither count towards
the streak nor show the prompt.

#### Scenario: Web audio queue

- **WHEN** the web player advances an audio queue on end
- **THEN** the streak does not change and no prompt is shown

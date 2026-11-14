# web-advanced-settings Specification

## Purpose
Console, Manage and profile settings share one view with a per-browser Normal/Advanced switch, so everyday settings stay uncluttered while every setting remains reachable.

## Requirements

### Requirement: One per-browser Normal/Advanced preference

The web app SHALL keep one settings view preference per browser, Normal or Advanced, defaulting
to Normal. The preference SHALL persist across reloads when browser storage is available and SHALL
fall back to Normal, without an error, when storage is blocked or empty. Every switch bound to the
preference SHALL reflect a change made by any other switch, including in another tab.

#### Scenario: First visit

- **WHEN** a person opens the Console in a browser that has never set the preference
- **THEN** the Normal view is shown and the "Show advanced settings" switch is off

#### Scenario: Choice persists

- **WHEN** a person turns on "Show advanced settings" and reloads the page
- **THEN** the Advanced view is shown

#### Scenario: Storage unavailable

- **WHEN** reading or writing browser storage throws
- **THEN** the page renders in the Normal view and the switch still toggles the view for the
  session

### Requirement: The switch is discoverable at every width

The "Show advanced settings" switch SHALL appear in the footer of the Console rail and of the
Manage menu, visible without opening the folded phone menu, and in the header of the profile
settings page.

#### Scenario: Phone width

- **WHEN** the Console is shown at a phone width with the section menu folded
- **THEN** the switch is visible and operable

### Requirement: Normal hides expert destinations without breaking links

In the Normal view the Console and Manage menus SHALL omit destinations classified Advanced, and
SHALL omit a group left empty. The current destination SHALL always appear in the menu. Every
route SHALL remain reachable by URL in both views. The Advanced view SHALL show every destination.
No destination needed for first run or recovery SHALL be classified Advanced.

#### Scenario: Normal menu

- **WHEN** the Console is shown in the Normal view
- **THEN** the rail omits Activity, Identification, Layout & Navigation, API tokens and Retention
  rules, and shows Libraries, Profiles & access, Remote access, Backups, Migration and Updates

#### Scenario: Deep link to an Advanced page

- **WHEN** a person in the Normal view opens `/console/retention`
- **THEN** the page renders and "Retention rules" appears in the rail as the current destination

### Requirement: Advanced controls never act out of sight

In the Normal view a control classified Advanced SHALL be hidden only while its value is the
default. A control whose value differs from its default SHALL stay visible, and SHALL stay visible
while it is being edited even if the edit returns it to the default.

#### Scenario: Non-default event retention

- **WHEN** the server's event retention is 30 days and the Operations page is shown in the Normal
  view
- **THEN** the event retention field is visible

#### Scenario: Default event retention

- **WHEN** the server's event retention is 180 days and the Operations page is shown in the Normal
  view
- **THEN** the event retention field is hidden, and it appears after turning on "Show advanced
  settings"

### Requirement: One mechanism hides expert settings

The Console, Manage and profile settings SHALL NOT use any other collapsed group or disclosure
labelled "Advanced" or "Maintain" to hide settings; the Normal/Advanced preference SHALL be the
only mechanism.

#### Scenario: Server configuration

- **WHEN** the Server configuration page is shown in the Advanced view
- **THEN** the hardware transcoding codec, quality and device fields are shown without opening a
  disclosure

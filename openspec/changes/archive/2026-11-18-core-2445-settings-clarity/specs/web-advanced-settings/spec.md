## MODIFIED Requirements

### Requirement: Normal hides expert destinations without breaking links

In the Normal view the Console and Manage menus SHALL omit destinations classified Advanced, and
SHALL omit a group left empty. The current destination SHALL always appear in the menu. Every
route SHALL remain reachable by URL in both views. The Advanced view SHALL show every destination.
No destination needed for first run or recovery SHALL be classified Advanced.

#### Scenario: Normal menu

- **WHEN** the Console is shown in the Normal view
- **THEN** the rail omits Activity, Identification, API tokens and Retention rules, and shows
  Libraries, Profiles & access, Parental controls, Remote access, Backups, Migration and Updates

#### Scenario: Deep link to an Advanced page

- **WHEN** a person in the Normal view opens `/console/retention`
- **THEN** the page renders and "Retention rules" appears in the rail as the current destination

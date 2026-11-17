## MODIFIED Requirements

### Requirement: The web Library page has a Folders view

The web Library page SHALL offer a Folders view as one entry of the Library's view switcher. It
SHALL list the Library's roots, then the open folder's subfolders and Media cards, with a
breadcrumb trail whose last step is marked as the current page. The heading of an open folder SHALL
count every Media at or below it. Each folder and breadcrumb SHALL be a link that keeps the open
folder in the URL, and the view SHALL be usable by keyboard and at 375px wide.

#### Scenario: Walking down and back up

- **WHEN** a viewer opens the Folders view, follows a root and then a nested folder
- **THEN** each step shows that folder's subfolders and Media, the breadcrumbs name every step,
  and following an earlier breadcrumb opens that folder again

#### Scenario: A folder of folders

- **WHEN** a viewer opens a folder that holds subfolders but no Media of its own
- **THEN** the heading counts the Media in its subfolders, never 0 items

#### Scenario: Leaving the Folders view

- **WHEN** a viewer in the Folders view chooses the Media view in the switcher
- **THEN** the Library page shows the Media browse and the folder leaves the URL

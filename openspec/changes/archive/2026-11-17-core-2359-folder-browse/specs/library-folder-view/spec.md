## ADDED Requirements

### Requirement: A Library can be read one folder at a time

The Graph read `libraryFolder` SHALL return one folder of one Library: its subfolders, each with
the number of visible Media at or below it, one page of the visible Media directly in it, their
`total` across pages, and the `crumbs` from the Library root down to it. Folders SHALL be derived
from the catalog paths of Media the session can see, with the visibility of `libraryItems`.

#### Scenario: Roots of a Library with several roots

- **WHEN** a session reads `libraryFolder` with no `path` for a Library whose two roots both hold
  visible Media
- **THEN** `path` is empty, `rootCount` is 2, `folders` lists both roots in configured order and
  `items` is empty

#### Scenario: A Library with one root opens it

- **WHEN** a session reads `libraryFolder` with no `path` for a Library with one root holding
  visible Media
- **THEN** `path` is that root and the response lists its subfolders and Media

#### Scenario: Nested folders open one level at a time

- **WHEN** a session reads a folder holding a subfolder that itself holds a subfolder
- **THEN** `folders` lists only the direct subfolder, with a `mediaCount` that includes the Media
  below it, and `items` holds only the Media directly in the folder

#### Scenario: Hidden Media do not make or count folders

- **WHEN** the only Media under a subfolder is in the trash
- **THEN** that subfolder is not listed and no parent folder counts it

#### Scenario: Paging

- **WHEN** a session reads a folder of N Media with `limit: 1, offset: 1`
- **THEN** `items` holds the second Media in file-name order and `total` is N

### Requirement: The folder read stays inside the Library

`libraryFolder` SHALL refuse, as a client validation error, a `path` that is relative, outside
every configured root of the Library, or escapes a root with `..`. A Library outside the session's
scope SHALL be refused as an unknown library.

#### Scenario: Path outside the roots

- **WHEN** a session reads `libraryFolder` with a `path` outside the Library's roots
- **THEN** the read fails with a client error and returns no folders or Media

#### Scenario: Restricted Library not revealed

- **WHEN** a session that has not revealed a restricted Library reads its `libraryFolder`
- **THEN** the read fails as an unknown library

### Requirement: The web Library page has a Folders view

The web Library page SHALL offer a Folders view beside Grid and List. It SHALL list the Library's
roots, then the open folder's subfolders and Media cards, with a breadcrumb trail whose last step
is marked as the current page. Each folder and breadcrumb SHALL be a link that keeps the open folder
in the URL, and the view SHALL be usable by keyboard and at 375px wide.

#### Scenario: Walking down and back up

- **WHEN** a viewer opens the Folders view, follows a root and then a nested folder
- **THEN** each step shows that folder's subfolders and Media, the breadcrumbs name every step,
  and following an earlier breadcrumb opens that folder again

#### Scenario: Leaving the Folders view

- **WHEN** a viewer in the Folders view chooses Grid
- **THEN** the Library page shows the grid browse and the folder leaves the URL

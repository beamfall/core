## ADDED Requirements

### Requirement: Web poster size is one app-wide preference offered on every pivot

The web viewer SHALL keep one Small, Medium or Large poster size preference for the whole app.
Every Library pivot (Recommended, Library, Collections, Categories) SHALL offer the control for it
whenever it shows its pivots, and a change on any page SHALL reach every page.

#### Scenario: One choice across pivots

- **WHEN** a viewer picks Medium on the Recommended pivot and opens the Library pivot
- **THEN** the Library pivot's control shows Medium and its grid draws at Medium

#### Scenario: A choice on any pivot reaches Home

- **WHEN** a viewer picks Small on a Library's Categories pivot and opens Home
- **THEN** Home's shelf cards draw at the Small width

### Requirement: Every web thumbnail surface follows the poster size

Every viewer thumbnail surface SHALL draw at the size the poster size preference sets: poster
grids, shelf cards on Home, Recommended and detail rows, the Collections poster grid, the
Categories cards, the pivot tiles and the cast row. Large SHALL keep each surface's prior size.

#### Scenario: Smaller shelf posters on Recommended

- **WHEN** a viewer picks Small on a Library's Recommended pivot
- **THEN** that page's shelf cards draw narrower than at Large

#### Scenario: Large keeps today's render

- **WHEN** a viewer has never picked a size
- **THEN** every thumbnail surface draws at the size it had before this change

## MODIFIED Requirements

### Requirement: Web library page offers a full browse view

The web viewer SHALL offer, for each library, a browse view that shows the total item count, renders
the set in a virtualized display, and keeps the sort and every active filter in the URL. Active
filters SHALL be shown as removable controls. The display SHALL be one of a poster grid, a
landscape thumbnail grid and a compact list whose rows show the ordinal, title, subtitle, duration
and progress. The display choice SHALL be remembered per library, SHALL apply to the all-titles
view and an open folder, and SHALL fit a 375px viewport without horizontal scrolling. Every control SHALL be keyboard operable and have an accessible name.

#### Scenario: Shared URL restores the view

- **WHEN** a user opens a browse URL carrying a sort and a filter
- **THEN** the view loads with that sort and filter applied and shown as active

#### Scenario: Removing a filter

- **WHEN** a user activates an active filter's remove control
- **THEN** the filter leaves the URL and the count and items update to the wider set

#### Scenario: Display choice is remembered per library

- **WHEN** a user picks the list display in one library and reopens that library later
- **THEN** the list display is selected there and other libraries keep their own choice

#### Scenario: Earlier grid choice

- **WHEN** a library's remembered choice is the earlier "grid" value
- **THEN** the poster grid is selected

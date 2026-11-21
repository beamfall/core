## ADDED Requirements

### Requirement: Web Recommended pivot offers the poster size for its shelves

A Library's Recommended pivot SHALL offer the same Small, Medium and Large poster size control as
the Library toolbar whenever it shows its pivots. Viewer shelf cards SHALL draw at a width set by
that same per-browser choice, Large keeping the width shelves had before, so a smaller size fits
more cards across a shelf.

#### Scenario: Smaller shelf posters on Recommended

- **WHEN** a viewer picks Small on a Library's Recommended pivot
- **THEN** that page's shelf cards draw narrower than at Large

#### Scenario: One choice across pivots

- **WHEN** a viewer picks Medium on the Recommended pivot and opens the Library pivot
- **THEN** the Library pivot's control shows Medium and its grid draws at Medium

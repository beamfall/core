## ADDED Requirements

### Requirement: Web viewer grids offer a poster size

The web Library toolbar SHALL offer Small, Medium and Large poster sizes, Large being the default
and the size the grids had before. The choice SHALL be kept per browser and SHALL set the column
floor of every viewer poster grid, so a smaller size fits more columns. The letter rail, keyboard
focus, card titles and lazy image loading SHALL keep working at every size.

#### Scenario: Smaller posters fit more per row

- **WHEN** a viewer picks Small in a Library toolbar
- **THEN** that grid and every other viewer poster grid show more columns than at Large

#### Scenario: The size is remembered

- **WHEN** a viewer picks Medium and reloads the page
- **THEN** the grids still draw at Medium

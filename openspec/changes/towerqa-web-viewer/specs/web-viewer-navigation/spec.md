## ADDED Requirements

### Requirement: Viewer pages name the browser tab

Each web viewer page SHALL set the document title to its main heading followed by the product
name, and the player SHALL use the playing item's title. A Revealed session SHALL show the product
name alone.

#### Scenario: Library page

- **WHEN** a viewer opens the Library "Music"
- **THEN** the tab title is "Music · Beamfall"

#### Scenario: Revealed session

- **WHEN** a Revealed session opens any viewer page
- **THEN** the tab title is "Beamfall"

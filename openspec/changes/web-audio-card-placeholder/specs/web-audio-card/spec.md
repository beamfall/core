## ADDED Requirements

### Requirement: Graph source items report their Media class

The graph `SourceItem` SHALL carry `mediaClass` for Media rows and SHALL leave it empty for entity
rows.

#### Scenario: Mixed shelf

- **WHEN** a client reads a Home shelf holding an audio Media, a video Media and an entity
- **THEN** the rows report `audio`, `video` and no class

### Requirement: Artwork-less audio cards show an audio placeholder

The web viewer SHALL draw an audio placeholder glyph for an artwork-less item whose class is
`audio`, and the film glyph for any other artwork-less item.

#### Scenario: Audio item without artwork

- **WHEN** a shelf shows an audio Media with no artwork
- **THEN** its card shows the audio glyph, not the film glyph

#### Scenario: Audio item with artwork

- **WHEN** an audio Media has artwork
- **THEN** its card shows the artwork and no glyph

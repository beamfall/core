# placeholder-icon Specification

## Purpose
Media without artwork shows the icon its plugin declares for its Libraries. Cards and the media read carry that icon, and clients draw it in place of missing art.

## Requirements

### Requirement: A plugin's declared icon is its Libraries' default icon

When a Library's plugin declares `LibraryDefaults.Icon` with a name from the Library icon
vocabulary, that name SHALL be the Library's default icon. A user-chosen Library icon SHALL still
take precedence, and a plugin that declares none SHALL keep the media-class default.

#### Scenario: Declaring plugin

- **WHEN** a Library's plugin declares `LibraryDefaults.Icon` and the user chose no icon
- **THEN** the Library's icon and default icon are the declared name

#### Scenario: User choice

- **WHEN** the user chose an icon for a Library whose plugin declares one
- **THEN** the Library's icon is the user's choice and its default icon is the declared name

### Requirement: Cards and the media read carry the placeholder icon

Every Graph read that returns `SourceItem` cards, and the `media(id)` read, SHALL set
`placeholderIcon` on a Media item whose Library plugin declares `LibraryDefaults.Icon`. Entities,
Media in a Library whose plugin declares none, and Media in a plugin-less Library SHALL omit the
field. Core SHALL NOT derive the icon from a plugin identity.

#### Scenario: Declaring plugin

- **WHEN** a Media item lives in a Library whose plugin declares `LibraryDefaults.Icon`
- **THEN** its card, its search card and its `media(id)` read carry that name as `placeholderIcon`

#### Scenario: Non-declaring plugin

- **WHEN** a Media item lives in a Library whose plugin declares no icon
- **THEN** its card and its `media(id)` read carry no `placeholderIcon` key

### Requirement: Clients draw the placeholder icon for artless Media

A client that draws a glyph in place of missing artwork SHALL draw the item's `placeholderIcon`
when it names an icon the client knows, and SHALL fall back to its media-class glyph otherwise.

#### Scenario: Artless karaoke song

- **WHEN** a card without artwork carries `placeholderIcon: "mic"`
- **THEN** the web client draws the microphone glyph

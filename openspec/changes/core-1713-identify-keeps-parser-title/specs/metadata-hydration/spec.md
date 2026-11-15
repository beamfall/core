## ADDED Requirements

### Requirement: Identification keeps a parser-owned display title

When an identification is applied and the library plugin's filename parser supplies a non-blank
per-file display title for the Media's primary file, the server SHALL write that title instead of
the scraper's title. It SHALL write the scraper's title when the parser supplies none or the title
cannot be read. Every other identified value SHALL be applied as before.

#### Scenario: A per-file title survives identification

- **WHEN** a file titled `2023-03-01` by its plugin's parser is identified through a lookup whose
  title is the name of the group the file belongs to
- **THEN** the Media keeps the title `2023-03-01` and takes the lookup's summary and year

#### Scenario: No parser title

- **WHEN** the plugin's parser supplies no per-file title for the Media
- **THEN** the Media takes the lookup's title

#### Scenario: Unreadable file

- **WHEN** the Media's primary file cannot be read
- **THEN** the identification still applies and the Media takes the lookup's title

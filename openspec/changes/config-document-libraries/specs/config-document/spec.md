## ADDED Requirements

### Requirement: Config document declares libraries additively

The config document SHALL accept an optional `libraries` list whose entries carry `title`,
`plugin` and `roots`. Applying it SHALL create each library not matched by exact title and
plugin, SHALL add each listed root missing from a matched library, and SHALL NOT remove any
library or root. An empty `plugin` SHALL denote a pluginless library. Applying the same
document twice SHALL change nothing the second time.

#### Scenario: Create then re-apply

- **WHEN** an operator applies a document listing library `Music` (plugin `music`, one root) to a
  server without it, then applies the same document again
- **THEN** the first apply creates one library with that root and the second reports the server
  already up to date with still one library

#### Scenario: Add a root without removing

- **WHEN** a matched library has root `/a` and the document lists only `/b`
- **THEN** the library ends with roots `/a` and `/b`

### Requirement: Library entries are validated before any write

Apply SHALL reject the whole document, writing nothing, when a library names an unknown plugin,
a root that is not absolute or not clean, a duplicate root, a duplicate title-plus-plugin entry,
or a blank title.

#### Scenario: Unknown plugin

- **WHEN** a document sets workers to 7 and lists a library with plugin `ghost`
- **THEN** apply fails naming `ghost` and the worker count is unchanged

### Requirement: Export round-trips stably

`export-config` SHALL emit non-restricted libraries sorted by title then plugin with sorted
roots, such that applying an export is a no-op and re-exporting yields an identical document.

#### Scenario: Round trip

- **WHEN** an operator exports, applies the export, and exports again
- **THEN** the apply changes nothing and both exports are identical

### Requirement: Offline config mode for stopped servers

`export-config` and `apply-config` SHALL accept `--data-dir` to work directly against a data
directory, and SHALL refuse when that directory holds a live server lock. Libraries created
offline SHALL be granted to the owner profile when one exists, and before first-run setup SHALL
be created ungranted so first-run setup grants them to the new owner.

#### Scenario: Seed before first start

- **WHEN** an install script applies a document with libraries to a fresh, stopped data directory
- **THEN** the libraries exist and the owner created at first-run setup can see them

#### Scenario: Running server

- **WHEN** `server.lock` is present in the data directory
- **THEN** offline apply refuses without writing

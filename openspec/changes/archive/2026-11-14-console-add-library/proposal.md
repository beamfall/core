# Add a library in one step, from one list of media types

Ticket: CORE-1156 (`queue:core:main`).

## Why

On a fresh server, Console > Libraries "Add a library" has no folder field (the owner creates an
empty library and then adds folders on its card), defaults to "No media type", and offers the
plugin list ("Film & TV", ...). The first-run setup wizard offers a different, hard-coded list
(Films, TV Shows, Mixed, Music, Podcasts) that leaves out installed plugins such as Photos,
Audiobooks, Family Archive and Karaoke. The two surfaces disagree about what a library can be.

## What the server does today (verified, unchanged by this change)

- `createLibrary(title, pluginID, familyScope, roots)` creates the library and its folders in one
  store call (`CreateLibraryWithRoots`); a bad root fails the whole call and nothing is created.
- `plugins` lists the installed plugins with `id` and a localized `displayName`. It does not publish
  a plugin's family scopes (the Film & TV plugin declares `films`, `tvshows` and `mixed`), so the
  web app keeps a small preset table for those scopes until the server publishes them.

## What Changes

- One web module (`src/lib/libraryTypes.ts`) turns the server's plugin list into the media types a
  library can be: one entry per plugin, labelled with the plugin's server-provided display name,
  and the Film & TV plugin's family scopes as separate presets (Films, TV Shows, Mixed). If a
  plugin publishes `familyScopes`, those are used instead of the preset table.
- The setup wizard's Library step and Console's add-library form both use that module, so both
  offer every installed media type.
- Console's add-library form asks for the media type first (a required choice, no "No media type"
  option), then a name (defaulting to the type's label) and at least one folder, using the existing
  `PathPickerField` browse control. It creates the library with its folders in one `createLibrary`
  call. Missing type or folder is reported inline next to the field and focus moves to it; a server
  refusal is shown in the form and nothing is created.

## Non-goals

- Server or schema changes (publishing family scopes on `plugins`); written up as a follow-up.
- Restyling the setup wizard (CORE-1114 owns it); only its list source changes.
- Translations beyond English for the new copy.

## Capabilities

### New Capabilities

- `library-creation`: the media types offered when creating a library, and creating a library with
  its folders from the Console.

### Modified Capabilities

None.

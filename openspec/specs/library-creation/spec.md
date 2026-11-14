# library-creation Specification

## Purpose
Lets an operator add a library from the web console, choosing a name, plugin bundle and root path, through the audited server mutation.

## Requirements

### Requirement: One media-type list for every surface that creates a library

The web app SHALL derive the media types offered when creating a library from the server's plugin
list, in one shared module used by both the first-run setup wizard and Console > Libraries. Every
installed plugin SHALL appear, labelled with its server-provided display name. A plugin that
declares family scopes SHALL appear once per scope instead.

#### Scenario: Every installed plugin is offered

- **WHEN** the server reports the Film & TV, Music, Photos and Karaoke plugins
- **THEN** both setup and Console offer Films, TV Shows, Mixed, Music, Photos and Karaoke

### Requirement: Console creates a library with a type and a folder in one action

Console's add-library form SHALL require a media type and at least one folder and SHALL NOT offer a
"no media type" choice. The name SHALL default to the chosen type's label when left blank. Submitting
SHALL send one `createLibrary` call carrying the plugin, the family scope when the type has one,
and the folders. A missing type or folder SHALL be reported next to its field, with focus moved to
the first problem, and nothing SHALL be sent.

#### Scenario: Library and folder are created together

- **WHEN** an admin picks "Music", enters the folder "/media/music" and creates the library
- **THEN** one `createLibrary` call carries `pluginID` music and `roots` ["/media/music"], and the
  new library is listed with that folder

#### Scenario: Folder missing

- **WHEN** an admin picks a type but leaves the folder empty and creates the library
- **THEN** no request is sent, the folder field says a folder is needed and has focus

### Requirement: A refused create leaves the form as it was

When the server refuses `createLibrary`, the form SHALL stay open with what the admin entered and
SHALL show the server's reason. Nothing SHALL be created, because the server creates the library
and its folders together.

#### Scenario: Server refuses the folder

- **WHEN** the server answers `createLibrary` with an error
- **THEN** the form still shows the entered type, name and folder, and shows the error

### Requirement: A Library folder is checked before anything is created

The server SHALL refuse to create a Library with, or point a Library at, a folder that does not
exist, is not a directory or cannot be read, and SHALL do so before creating or changing anything.
The refusal SHALL name the folder and the reason in plain words. Only folders an update adds are
checked: a folder already saved on the Library MAY be kept even when it can no longer be read, so a
Library whose folders have gone missing can be repaired by removing them.

#### Scenario: Folder does not exist

- **WHEN** a Library is created with a folder that does not exist on the server
- **THEN** the request is refused with a message that the folder does not exist and no Library is
  created

#### Scenario: Folder is a file

- **WHEN** a Library's folder is updated to a path that is a regular file
- **THEN** the request is refused, saying it is a file and not a folder, and the Library keeps its
  previous folder

#### Scenario: Folder cannot be read

- **WHEN** the folder exists but the server lacks permission to read it
- **THEN** the request is refused with a permission message and nothing is created

#### Scenario: Removing a missing folder

- **WHEN** a Library has two saved folders that no longer exist and an update keeps one of them
- **THEN** the update succeeds and the Library keeps only that folder

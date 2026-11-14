## ADDED Requirements

### Requirement: A Library folder is checked before anything is created

The server SHALL refuse to create a Library with, or point a Library at, a folder that does not
exist, is not a directory or cannot be read, and SHALL do so before creating or changing anything.
The refusal SHALL name the folder and the reason in plain words.

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

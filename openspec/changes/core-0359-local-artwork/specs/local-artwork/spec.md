## ADDED Requirements

### Requirement: Local sidecar artwork is discovered from one vocabulary table

The scan SHALL discover sidecar images for each non-image Media from a single local artwork
vocabulary table, in the jpg, png and webp formats only, and SHALL import matches into the
existing artwork slots without adding a slot kind. Folder-level names SHALL apply only when the
folder holds exactly one non-image media file.

#### Scenario: Folder poster and fanart

- **WHEN** a folder holds one video file beside `poster.jpg` and `fanart.jpg`
- **THEN** the Media's `poster` and `backdrop` slots hold those bytes and no frame grab runs

#### Scenario: Folder art in a shared folder

- **WHEN** a folder holds two video files beside `poster.jpg`
- **THEN** neither Media takes `poster.jpg` as its own poster

### Requirement: File-level sidecar names outrank folder-level names

A file-level name (`<stem>-poster`, `<stem>-fanart`, `<stem>-thumb`, `<stem>-landscape`) SHALL win
over a folder-level name (`poster`, `folder`, `cover`, `fanart`, `backdrop`, `thumb`, `landscape`,
`extrafanart/`) for the same slot of the Media whose file stem it carries.

#### Scenario: File-level art wins over folder art

- **WHEN** a folder holds one video file `<stem>.mkv` beside `<stem>-thumb.jpg` and `thumb.jpg`
- **THEN** the Media's `thumb` slot holds the bytes of `<stem>-thumb.jpg`

### Requirement: Local artwork is locked against lower-precedence writers

A locally imported slot SHALL carry a lock marker recording source `local` and the sidecar's
path, size and modification time. A scrape import SHALL refuse a locked slot with a typed error
and SHALL NOT mark its candidate applied. A manual pick SHALL be able to bypass the lock.

#### Scenario: A later scrape

- **WHEN** a scrape imports artwork into a slot that holds local artwork
- **THEN** the import fails with `SlotLockedError` and the slot keeps the local bytes

#### Scenario: A manual pick

- **WHEN** the operator picks artwork for a locally locked slot through the manual entry point
- **THEN** the picked bytes replace the slot

### Requirement: Local artwork follows the sidecar file

Each scan SHALL compare every matched sidecar's size and modification time with the slot's lock
marker and SHALL re-import a changed sidecar, even when the media file itself is unchanged. When
no sidecar matches a locally locked slot any more, the scan SHALL remove the lock and keep the
art.

#### Scenario: An edited sidecar

- **WHEN** `poster.jpg` is replaced with new bytes and a new modification time and the folder is
  rescanned
- **THEN** the Media's `poster` slot holds the new bytes

#### Scenario: A removed sidecar

- **WHEN** `poster.jpg` is deleted and the folder is rescanned
- **THEN** the `poster` slot keeps its bytes and a scrape may replace them again

### Requirement: Container art reaches the ordered container Entity

The scan SHALL map `season<NN>-poster` and `season-specials-poster`, found in the Media's folder
or its parent inside the library root, to the poster of the ordered container Entity with that
ordinal, when the identifier resolves the Media's container Entity.

#### Scenario: A container poster

- **WHEN** `Show/season01-poster.jpg` exists and a Media under `Show/` resolves to the container
  Entity with ordinal 1
- **THEN** that Entity's poster holds the bytes of `season01-poster.jpg`

### Requirement: Sealed and Encrypted Libraries import no local artwork

The artwork service SHALL write no local artwork and no lock marker for a Media or Entity whose
Library is Sealed or Encrypted, and SHALL fail closed when it cannot resolve the Library (ADR-0045).

#### Scenario: A Sealed Library

- **WHEN** a Sealed Library's folder holds a video file beside `poster.jpg`
- **THEN** no artwork file and no lock marker is written for that Media

### Requirement: Sidecar reads are confined to regular image files

The artwork service SHALL read a sidecar only when it is a regular file reached without following
a symlink, within the size cap, and whose bytes are a JPEG, PNG or WebP image.

#### Scenario: A symlinked sidecar

- **WHEN** `poster.jpg` is a symlink to a file elsewhere
- **THEN** nothing is imported

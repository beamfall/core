## ADDED Requirements

### Requirement: Recognised sidecar artwork is never a catalogue item

The scan SHALL NOT catalogue an image the local artwork vocabulary recognises as an item, in any
Library. Folder-level and container names SHALL match by name alone, a `<stem>-<name>` file name
SHALL match only beside a non-image media file with that stem, and any image in an `extrafanart`
directory SHALL match. A rescan SHALL mark a previously catalogued sidecar item missing through the
existing missing-file path rather than deleting it.

#### Scenario: A folder holding every vocabulary name

- **WHEN** a Library folder holds media files beside an image for every vocabulary name,
  including `logo`, `clearlogo`, `banner`, `artist` and `season01-poster`
- **THEN** the scan catalogues only the media files

#### Scenario: A sidecar catalogued by an earlier scan

- **WHEN** an earlier scan catalogued `clearlogo.png` as an item and the Library is rescanned
- **THEN** the item's file is marked missing and the Media row is kept

#### Scenario: A photo that only resembles a file-level name

- **WHEN** a folder holds `hills-landscape.jpg` and no media file with the stem `hills`
- **THEN** the image is catalogued as an item

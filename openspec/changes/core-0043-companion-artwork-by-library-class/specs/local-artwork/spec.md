## MODIFIED Requirements

### Requirement: Recognised sidecar artwork is never a catalogue item

The scan SHALL NOT catalogue an image the local artwork vocabulary recognises as an item in a
Library whose bound plugin does not declare the `image` Media class. In a Library whose bound plugin
declares the `image` class, the scan SHALL catalogue every image by extension as before.
Folder-level and container names SHALL match by name alone, a `<stem>-<name>` file name (including
`<stem>-thumb`, `<stem>-poster`, `<stem>-fanart` and `<stem>-banner`) SHALL match only beside a
non-image media file with that stem, and any image in an `extrafanart` directory SHALL match. A
rescan SHALL mark a previously catalogued sidecar item missing through the existing missing-file
path rather than deleting it.

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

#### Scenario: Companion artwork in a video Library

- **WHEN** a Library bound to a plugin that declares only the `video` class holds `<stem>.mkv`
  beside `<stem>-thumb.jpg`, `<stem>-banner.jpg`, `poster.jpg`, `fanart.jpg` and `folder.jpg`
- **THEN** the scan catalogues one video item and no image item, and the Library's all-items feed
  holds no image Media

#### Scenario: The same files in an image Library

- **WHEN** the same files sit in a Library bound to a plugin that declares the `image` class
- **THEN** the scan catalogues the video and every image as items

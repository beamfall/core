## MODIFIED Requirements

### Requirement: What the local artwork vocabulary recognises

Folder-level and container names SHALL match by name alone. Container names SHALL include the
numbered (`season<NN>-`), `season-specials-` and `season-all-` forms of `poster`, `banner`,
`fanart`, `landscape` and `thumb`. A `<stem>-<name>` file name (including `<stem>-thumb`,
`<stem>-poster`, `<stem>-fanart` and `<stem>-banner`) SHALL match only beside a non-image media
file with that stem. Any image in an `extrafanart` directory SHALL match.

#### Scenario: A folder holding every vocabulary name

- **WHEN** a Library folder holds media files beside an image for every vocabulary name,
  including `logo`, `clearlogo`, `banner`, `artist` and `season01-poster`
- **THEN** the scan catalogues only the media files

#### Scenario: A photo that only resembles a file-level name

- **WHEN** a folder holds `hills-landscape.jpg` and no media file with the stem `hills`
- **THEN** the image is catalogued as an item

#### Scenario: Container banners and fanart

- **WHEN** a video Library folder holds `season01-banner.jpg`, `season02-fanart.png` and
  `season-all-poster.jpg`
- **THEN** none of them is catalogued as an item

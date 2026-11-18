# local-artwork Specification

## Purpose
How the library scan finds curated artwork files that sit beside media (Kodi, Plex and Jellyfin style sidecar images such as `poster.jpg`, `fanart.jpg` and `<stem>-thumb.jpg`), imports them into the existing artwork slots as local art, and locks those slots so scrapes and generated posters never overwrite them until the sidecar changes or disappears. Sealed and Encrypted Libraries import nothing.

## Requirements

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
or its parent inside the library root, to the poster of the innermost ordered container Entity
with that ordinal, when the identifier resolves the Media's container Entities. The identifier
SHALL resolve the container Entities innermost first, inside the Media's Library, from the kinds
the Library's plugin declares as containers.

#### Scenario: A container poster

- **WHEN** `Show/season01-poster.jpg` exists and a Media under `Show/` resolves to the container
  Entity with ordinal 1
- **THEN** that Entity's poster holds the bytes of `season01-poster.jpg`

### Requirement: Folder art reaches the outer container Entity

The scan SHALL map a folder `poster`, `folder` or `cover` image in the library's top-level folder
that holds the Media to the poster of the outermost container Entity.

#### Scenario: An outer container poster

- **WHEN** `Show/poster.jpg` exists and a Media in `Show/Season 01/` resolves to an ordered
  container Entity inside an outer container Entity
- **THEN** the outer Entity's poster holds the bytes of `poster.jpg`

#### Scenario: A folder outside the library root

- **WHEN** the library root is `Show/Season 01` and `Show/poster.jpg` exists above it
- **THEN** no container Entity receives that image

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

### Requirement: Recognised sidecar artwork is never a catalogue item

The scan SHALL NOT catalogue an image the local artwork vocabulary recognises as an item in a
Library with no bound plugin, or whose bound plugin's declared classes are unknown. In a Library
whose bound plugin declares Media classes without the `image` class, the scan SHALL NOT catalogue
any image as an item: an image there is a sidecar artwork candidate or is ignored. In a Library
whose bound plugin declares the `image` class, the scan SHALL catalogue every image by extension.

#### Scenario: Companion artwork in a video Library

- **WHEN** a Library bound to a plugin that declares only the `video` class holds `<stem>.mkv`
  beside `<stem>-thumb.jpg`, `<stem>-banner.jpg`, `poster.jpg`, `fanart.jpg` and `folder.jpg`
- **THEN** the scan catalogues one video item and no image item, and the Library's all-items feed
  holds no image Media

#### Scenario: An unrecognised image in a video Library

- **WHEN** a Library bound to a plugin that declares only the `video` class holds `unknown.jpg`
- **THEN** the scan does not catalogue it as an item

#### Scenario: The same files in an image Library

- **WHEN** the same files sit in a Library bound to a plugin that declares the `image` class
- **THEN** the scan catalogues the video and every image as items

#### Scenario: An unrecognised image in a Library with no plugin

- **WHEN** a Library with no bound plugin holds `unknown.jpg` beside a video
- **THEN** the scan catalogues both as items

### Requirement: A rescan retires sidecars an earlier scan catalogued

In a Library with no bound plugin, a rescan SHALL mark a previously catalogued sidecar item
missing through the existing missing-file path rather than deleting it.

#### Scenario: A sidecar catalogued by an earlier scan

- **WHEN** an earlier scan catalogued `clearlogo.png` as an item in a Library with no bound plugin
  and the Library is rescanned
- **THEN** the item's file is marked missing and the Media row is kept

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

### Requirement: Scan preview and estimate apply the Library's sidecar rule

The rescan preview and the first-scan estimate SHALL apply the scan's image rule for the target
plugin: they SHALL count every image when the target plugin declares the `image` Media class,
SHALL skip every image when it declares classes without `image`, and SHALL otherwise skip only
recognised sidecars. The target plugin is the Library's bound plugin, or the `pluginID` an
estimate of a raw root is given; with none, or an unregistered one, both SHALL skip recognised
sidecars only.

#### Scenario: A thumb image beside a video in an image Library

- **WHEN** a root holds `<stem>.mov` beside `<stem>-thumb.jpg` and the target plugin declares the
  `image` class
- **THEN** the preview lists both files as adds and the estimate counts both files

#### Scenario: The same files for a video-only plugin

- **WHEN** the same files and `unknown.jpg` are previewed or estimated for a plugin that declares
  only the `video` class
- **THEN** the preview lists only the video as an add and the estimate counts only the video

#### Scenario: The same files with no plugin

- **WHEN** the same files and `unknown.jpg` are previewed or estimated with no target plugin
- **THEN** the video and `unknown.jpg` count and the thumb does not

### Requirement: A rescan retires images the Library no longer imports

A rescan of a Library whose bound plugin declares Media classes without the `image` class SHALL
move to the trash every live image-class Media whose live files all lie under the scanned root.
It SHALL use the recoverable trash path, so the Media can be restored until the trash retention
window ends, and SHALL record a `media.trash` activity entry for each retired Media. It SHALL NOT
retire anything on a walk that found no media file, and SHALL NOT retire while trash is disabled.

#### Scenario: Images an earlier scan catalogued in a video Library

- **WHEN** an earlier scan catalogued `season01-banner.jpg` and `unknown.jpg` as image items in a
  Library bound to a plugin that declares only the `video` class, and the Library is rescanned
- **THEN** both Media are in the trash with a `media.trash` activity entry, and the grid no longer
  lists them

#### Scenario: An image outside the scanned root

- **WHEN** the same Library holds an image-class Media whose file lies outside the scanned root
- **THEN** the rescan leaves it live

#### Scenario: A root that walks empty

- **WHEN** the scanned root walks no media file, for example an unmounted share
- **THEN** the rescan retires nothing

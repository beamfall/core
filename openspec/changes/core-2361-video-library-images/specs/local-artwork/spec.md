## MODIFIED Requirements

### Requirement: Recognised sidecar artwork is never a catalogue item

The scan SHALL NOT catalogue an image the local artwork vocabulary recognises as an item in a
Library with no bound plugin, or whose bound plugin's declared classes are unknown. In a Library
whose bound plugin declares Media classes without the `image` class, the scan SHALL NOT catalogue
any image as an item: an image there is a sidecar artwork candidate or is ignored. In a Library
whose bound plugin declares the `image` class, the scan SHALL catalogue every image by extension
as before. In a Library with no bound plugin, a rescan SHALL mark a previously catalogued sidecar
item missing through the existing missing-file path rather than deleting it.

#### Scenario: A sidecar catalogued by an earlier scan

- **WHEN** an earlier scan catalogued `clearlogo.png` as an item in a Library with no bound plugin
  and the Library is rescanned
- **THEN** the item's file is marked missing and the Media row is kept

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

## ADDED Requirements

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

## ADDED Requirements

### Requirement: Scan preview and estimate apply the Library's sidecar rule

The rescan preview and the first-scan estimate SHALL count a recognised sidecar-named image when
the target plugin declares the `image` Media class and SHALL skip it otherwise, as the scan does.
The target plugin is the Library's bound plugin, or the `pluginID` an estimate of a raw root is
given; with none, or an unregistered one, both SHALL skip recognised sidecars.

#### Scenario: A thumb image beside a video in an image Library

- **WHEN** a root holds `<stem>.mov` beside `<stem>-thumb.jpg` and the target plugin declares the
  `image` class
- **THEN** the preview lists both files as adds and the estimate counts both files

#### Scenario: The same files for a video-only plugin

- **WHEN** the same files are previewed or estimated for a plugin that declares only the `video`
  class
- **THEN** the preview lists only the video as an add and the estimate counts only the video

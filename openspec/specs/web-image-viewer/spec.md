# web-image-viewer Specification

## Purpose
Open image Media in the web viewer with a stage for the original image and navigation through the viewer queue.

## Requirements

### Requirement: Image Media opens in the viewer

The web client SHALL open an image-class Media in the image viewer at `/view/<address>`, never in
the video player. A card for an image-class Media SHALL open the viewer on activation and from its
quick Play. The Media detail page's Play SHALL open the viewer for an image. When the watch route
learns that the Media it was opened for is image-class, it SHALL replace its history entry with
the viewer's address and carry the opening state.

#### Scenario: Image card opens the viewer

- **WHEN** a viewer activates a card whose Media has class `image`
- **THEN** the address becomes `/view/<that Media's address>` and the video player is not mounted

#### Scenario: Watch link for an image is redirected

- **WHEN** the watch route opens a Media and the playback decision reports class `image`
- **THEN** the history entry is replaced with `/view/<that Media's address>`

### Requirement: Viewer stage and original image

The viewer SHALL paint the Media's poster derivative on a full-bleed dark stage as soon as the
item is known. On the first zoom it SHALL request the playback decision and load the direct
stream's original. It SHALL show the original only after the browser has decoded it. When the
original cannot be decoded, the viewer SHALL keep the derivative and state that the full-size
image is not viewable in this browser.

#### Scenario: Original replaces the derivative after decoding

- **WHEN** the viewer zooms and the original image decodes
- **THEN** the stage shows the original in place of the derivative

#### Scenario: Undecodable original keeps the derivative

- **WHEN** the original image fails to decode
- **THEN** the stage keeps the derivative and shows a notice that the full-size image cannot be
  shown in this browser

### Requirement: Viewer queue navigation

The viewer SHALL step to the previous and next image items of the queue it was opened with, from
the Left and Right arrow keys, from labelled buttons and from a horizontal swipe, replacing its
history entry on each step. Escape SHALL close the viewer.

#### Scenario: Arrow keys step through image items

- **WHEN** the viewer was opened from a queue of an image, a video and another image, on the first
  image, and the viewer presses the Right arrow
- **THEN** the viewer shows the second image and skips the video

### Requirement: Viewer image controls

Z, a double click and the wheel SHALL zoom; dragging SHALL pan a zoomed image; 0 SHALL reset zoom
and rotation; R SHALL rotate the view by a quarter turn without saving it; F SHALL toggle the
Profile's favourite for the Media; I SHALL toggle an info pane with the title, year and
plugin-declared fields. Every control SHALL be a keyboard-focusable button with an accessible name.

#### Scenario: Favourite toggles

- **WHEN** the viewer presses F on an item that is not a favourite
- **THEN** the client sends `setFavorite` with `favorite: true` and the favourite control shows
  the item as a favourite

#### Scenario: Unknown item

- **WHEN** the server answers `media(id)` with "unknown media"
- **THEN** the viewer shows "Not available" and no image

### Requirement: Viewer filmstrip

The viewer SHALL show a strip of thumbnails of the items around the current one in its queue.
Activating a thumbnail SHALL open that item in the viewer. The strip SHALL keep the current item
in view and mark it as current. The strip SHALL hide together with the viewer's other controls
when the pointer has been idle and SHALL show again on pointer movement or a key press.

#### Scenario: Thumbnail jumps to its item

- **WHEN** the viewer is open on the second of five queue items and the viewer activates the
  fourth thumbnail
- **THEN** the viewer shows the fourth item and its thumbnail is marked current

#### Scenario: Strip hides with the chrome

- **WHEN** the pointer has not moved for the idle period and no panel is open
- **THEN** the control bar, the step buttons and the filmstrip are hidden

### Requirement: Viewer trash with undo

On a device with the manager role, the viewer SHALL offer a trash control and the Delete key.
Trashing SHALL call the audited `trashMedia` mutation, then step to the next item, or the
previous one when the trashed item was last, or close when no item is left. The viewer SHALL show
a toast with Undo; Undo SHALL call `restoreMedia` for that item and return to it. The viewer
SHALL NOT show the trash control on a device without the manager role.

#### Scenario: Trash steps on and undo restores

- **WHEN** a manager trashes the second of three items and then chooses Undo
- **THEN** `trashMedia` is called for the second item, the viewer shows the third, and after Undo
  `restoreMedia` is called and the viewer shows the second item again

#### Scenario: No trash control without the manager role

- **WHEN** the viewer is open on a device whose role is not manager
- **THEN** no trash control is shown and the Delete key does nothing

### Requirement: Inline clips in the viewer

The viewer SHALL treat a video-class item in its queue as part of stepping. It SHALL play the
clip muted and inline on the stage from the direct stream `/api/playback-info` issues, with a
play/pause control that Space also toggles, and SHALL end that playback session when it leaves
the item.

#### Scenario: Stepping onto a clip plays it inline

- **WHEN** the viewer steps from an image onto a video-class item in its queue
- **THEN** the stage shows a muted inline video of that item and a pause control

### Requirement: Clips the viewer cannot play

When a clip has no direct stream or the browser cannot decode it, the viewer SHALL keep the
poster, state that the clip cannot play here and offer the full player. A video-class Media
opened at `/view` outside the viewer's queue SHALL be replaced by the watch route.

#### Scenario: Undecodable clip offers the player

- **WHEN** the browser fails to decode a clip's direct stream
- **THEN** the viewer shows a notice and an Open in player control that opens `/watch` for it

### Requirement: Clip cards in image grids

A video-class card shown with a queue that holds image items SHALL draw its duration as a badge
on its picture when the duration is known, SHALL draw a clip glyph rather than the film glyph
when it has no artwork, and SHALL open the viewer on activation.

#### Scenario: Clip card shows its duration

- **WHEN** a grid of images contains a video-class item of 42 seconds
- **THEN** its card shows a `0:42` badge on the picture

### Requirement: Favourite toggles do not overlap

The viewer SHALL NOT send a second favourite change for an item while one for that item is in
flight; the favourite control SHALL be disabled until the first completes.

#### Scenario: Rapid toggles

- **WHEN** the viewer presses F twice before the first change returns
- **THEN** only one `setFavorite` request is sent for that item

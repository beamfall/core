## ADDED Requirements

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

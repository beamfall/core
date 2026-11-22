## ADDED Requirements

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

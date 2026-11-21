# web-audio-visualizer Specification

## Purpose
Draw a pinned Oscillux visual behind the Web Viewer audio stage that reacts to the playing track, takes the track's artwork as its image input, and never interrupts playback.

## Requirements

### Requirement: The visual's image input is the playing track's artwork

The Web Viewer visualizer SHALL supply the playing audio item's artwork as the Oscillux visual's
image input when the artwork is served from the page's origin and decodes. It SHALL supply a
neutral stand-in image when the item has no artwork, the artwork is cross-origin, or loading or
decoding fails, and the visual SHALL keep drawing in every case.

#### Scenario: Track with artwork

- **WHEN** an audio item with same-origin artwork plays with an image-input visual
- **THEN** the visual is drawn with that artwork as its image input

#### Scenario: Next track

- **WHEN** playback advances to an item with different artwork
- **THEN** the image input changes to the new item's artwork

#### Scenario: No usable artwork

- **WHEN** the item has no artwork, or its artwork is cross-origin or fails to load
- **THEN** the visual draws with the neutral stand-in and playback is unaffected

### Requirement: The visualizer blends between visuals with the Oscillux switch pass

When the Web Viewer visualizer changes from one Oscillux visual to another, it SHALL blend the
outgoing visual into the incoming one through the switch pass vendored unchanged from the pinned
Oscillux release, with the progress timed by the Oscillux transition scheduler, instead of
replacing the visual in one frame. Core SHALL NOT supply its own transition shader. Under
`prefers-reduced-motion` the visual SHALL change at once.

#### Scenario: Album change

- **WHEN** playback advances to an item whose artwork selects a different visual
- **THEN** both visuals draw while the switch pass blends from the outgoing one to the incoming
  one, and the outgoing visual is dropped once the switch ends

#### Scenario: Same artwork

- **WHEN** playback advances to an item that selects the same visual
- **THEN** the visual keeps drawing with no switch

#### Scenario: Reduced motion

- **WHEN** the visual changes while the user prefers reduced motion
- **THEN** the incoming visual replaces the outgoing one at once

#### Scenario: Change during a switch

- **WHEN** another visual is requested while a switch is running
- **THEN** it starts once the running switch ends

### Requirement: The visualizer scales render size and detail with the Oscillux quality governor

The Web Viewer visualizer SHALL drive its visual render size and detail reduction from the
Oscillux quality governor in the pinned wasm core, using the reference web host's quality ladder,
so that frame pacing holds on displays the GPU cannot fill at full size. Core SHALL NOT implement
its own quality heuristics. The present pass and the frame flash limiter SHALL keep running at
the full canvas size.

#### Scenario: Slow frames

- **WHEN** frame intervals stay above the governor's target
- **THEN** the governor demotes the level and the visual renders at the smaller scale with more
  detail reduction, while the canvas keeps its size

#### Scenario: Headroom returns

- **WHEN** frame intervals stay well under the target
- **THEN** the governor promotes the level back toward full size and full detail

#### Scenario: Switch running

- **WHEN** a switch between visuals is running
- **THEN** its frames are not fed to the governor and the level does not change until it ends

### Requirement: The viewer can switch the visualizer off

The Web Viewer visualizer choice SHALL include Off. With Off the audio stage SHALL mount no
visualizer, so no WebGL context is created and the playing audio is neither decoded nor analysed for
it, and the stage SHALL show the artwork backdrop and card as it does where the browser cannot
visualize. The choice SHALL be kept per browser like the other visualizer choices.

#### Scenario: Off chosen

- **WHEN** the viewer chooses Off while an audio item plays
- **THEN** the visualizer is removed and the artwork backdrop and card show without it

#### Scenario: Off kept

- **WHEN** the viewer reloads the page after choosing Off
- **THEN** the audio stage still shows no visualizer

### Requirement: The viewer can step to the next or previous visual

The Web Viewer audio player SHALL offer next and previous visual. Each SHALL pin the visual after
or before the visual on screen in the Oscillux catalog order, wrapping at the ends; when the viewer
prefers reduced motion it SHALL step only to calm visuals. From Off, next or previous SHALL turn the
visualizer on with the visual shown last, or the first visual when none was shown.

#### Scenario: Next visual

- **WHEN** a visual shows and the viewer steps to the next visual
- **THEN** the next visual in catalog order is pinned and shown, and after the last visual comes the first

#### Scenario: Previous visual

- **WHEN** a visual shows and the viewer steps to the previous visual
- **THEN** the previous visual in catalog order is pinned and shown, and before the first visual comes the last

#### Scenario: Reduced motion

- **WHEN** the viewer prefers reduced motion and steps to the next visual
- **THEN** visuals that are not calm are skipped

#### Scenario: Turning the visualizer back on

- **WHEN** the visualizer is Off and the viewer steps to the next or previous visual
- **THEN** the visualizer turns on with the visual shown last, or the first visual

### Requirement: The audio transport carries a visualizer control and shortcuts

The audio transport SHALL show a visualizer control with previous visual, an on/off toggle naming
the visual on screen, and next visual, only for audio items where the browser can visualize, and it
SHALL NOT cover the artwork, title or waveform. `V` SHALL step to the next visual and `Shift+V` to
the previous one, except while focus is in a field or a control, and both SHALL be listed in the
player's shortcut sheet.

#### Scenario: Control on an audio item

- **WHEN** an audio item plays in a browser that can visualize
- **THEN** the transport shows previous visual, the toggle with the visual's name, and next visual

#### Scenario: Shortcut

- **WHEN** a visual shows and the viewer presses `V`
- **THEN** the next visual is shown

#### Scenario: Typing

- **WHEN** focus is in a text field and the viewer types `v`
- **THEN** the visual does not change

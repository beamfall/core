## ADDED Requirements

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

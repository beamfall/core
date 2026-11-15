## ADDED Requirements

### Requirement: Subtitle offset control

The web player SHALL offer a subtitle offset control in its subtitle panel that steps by 0.1 s and
0.5 s in either direction within ±60 s, shows the current value with its direction, resets to zero,
and shows a non-zero value on the settings row. A positive offset SHALL show cues later.

#### Scenario: Nudging the offset

- **WHEN** the viewer presses +0.5 and then +0.1 with a native subtitle track showing
- **THEN** every cue shows 0.6 s after its authored time and the panel reads "+0.6 s" and "later"

#### Scenario: Range limit

- **WHEN** the offset is at +60 s
- **THEN** the later step buttons are disabled and the value does not grow

### Requirement: Subtitle offset on the hls.js route

The web player SHALL apply the subtitle offset when hls.js renders cues into native TextTracks, and
SHALL re-apply it to cues that arrive after the offset was set. It SHALL disable the control with
an "unavailable on this route" note only when cues are not native TextTracks.

#### Scenario: Cues from a later fragment

- **WHEN** an offset is set and hls.js appends cues from a subtitle fragment that arrives later
- **THEN** the new cues show shifted by the offset and earlier cues stay where they were

### Requirement: Audio delay control

The web player SHALL offer an audio sync control for video items that delays the element's sound
from 0 to 5 s in 0.1 s and 0.5 s steps, shows the value on its settings row, resets to zero, and
states that the browser cannot play audio early. Where the sound cannot be routed through Web Audio
without going silent, the control SHALL be disabled with a note saying so.

#### Scenario: Delaying audio

- **WHEN** the viewer sets the audio delay to +0.6 s on a same-origin or hls.js source
- **THEN** the sound plays 0.6 s behind the picture and the settings row reads "+0.6 s"

#### Scenario: Route that cannot carry the delay

- **WHEN** the browser plays HLS through its own engine, even for a direct-play item
- **THEN** the audio sync control is disabled and says the delay is unavailable on this route

#### Scenario: Recovering after an interruption at zero delay

- **WHEN** the viewer set a delay, reset it to zero, and the browser then suspends audio
- **THEN** the next play or interaction resumes the sound

### Requirement: Sync values persist per Profile and Media

The web player SHALL remember the subtitle offset and audio delay on the device for the active
Profile and the playing Media, restore them when that Media plays again, and forget an entry whose
values are both zero.

#### Scenario: Playing the item again

- **WHEN** the viewer sets a subtitle offset, leaves, and later plays the same Media on the same
  Profile
- **THEN** the player opens with that offset applied and shown

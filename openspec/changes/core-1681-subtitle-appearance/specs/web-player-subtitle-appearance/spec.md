## ADDED Requirements

### Requirement: Subtitle appearance controls

The web player's subtitle panel SHALL offer text size, background, font, text colour, text opacity,
edge style and vertical position, and SHALL apply each to the showing subtitles on the direct-play
route and on the hls.js route when hls.js renders through native TextTracks.

#### Scenario: Changing the colour and edge

- **WHEN** the viewer picks yellow text with an outline edge while a subtitle track is showing
- **THEN** the showing cues render yellow with a dark outline

#### Scenario: Moving subtitles up

- **WHEN** the viewer picks the middle position
- **THEN** each cue's bottom sits at the vertical middle of the picture, including cues that arrive
  after the choice, and picking the authored position puts every cue back where its file placed it

### Requirement: Subtitle appearance persists per Profile

The web player SHALL store each subtitle appearance value in the active Profile's client
preferences under its `subtitle.*` key, restore the values the next time any item plays on that
Profile, and clear a key when its value returns to the default. Reset SHALL clear every key.

#### Scenario: Look follows the Profile

- **WHEN** the viewer picks large serif text, leaves, and later plays another item on the same
  Profile
- **THEN** the player opens with large serif subtitles and the panel shows those choices

#### Scenario: Back to default

- **WHEN** the viewer sets the colour back to white
- **THEN** the `subtitle.color` key is cleared rather than stored as `#ffffff`

### Requirement: Subtitle text keys

The `subtitle.size`, `subtitle.background` and `subtitle.font` keys SHALL hold only the values
below, and any other value SHALL read as the default.

| Key | Values | Default |
| --- | --- | --- |
| `subtitle.size` | `0.85`, `1`, `1.25`, `1.5` (scale of the base cue size) | `1` |
| `subtitle.background` | `none`, `dark` (about 55% black), `solid` (about 92% black) | `dark` |
| `subtitle.font` | `sans`, `serif`, `mono`, `casual` | `sans` |

#### Scenario: Unknown font

- **WHEN** a Profile stores `subtitle.font` as `fantasy`
- **THEN** the player renders the `sans` font

### Requirement: Subtitle colour key

The `subtitle.color` key SHALL hold one of the lowercase values `#ffffff` (the default), `#ffff00`,
`#00ff00`, `#00ffff`, `#0000ff`, `#ff00ff`, `#ff0000` and `#000000`, and any other value SHALL
read as `#ffffff`.

#### Scenario: Uppercase colour

- **WHEN** a Profile stores `subtitle.color` as `#FFFF00`
- **THEN** the player renders white text

### Requirement: Subtitle opacity and edge keys

The `subtitle.opacity` key SHALL hold `1` (the default), `0.75`, `0.5` or `0.25`, applied to the
text and its edge but not the background. The `subtitle.edge` key SHALL hold `none` (the default),
`outline`, `drop-shadow`, `raised` or `depressed`. Any other value SHALL read as the default.

#### Scenario: Half-opaque outlined text

- **WHEN** a Profile stores `subtitle.opacity` as `0.5` and `subtitle.edge` as `outline`
- **THEN** the text and its outline render at half opacity over an unchanged background

### Requirement: Subtitle position key

The `subtitle.position` key SHALL hold `auto` (each cue where its file placed it) or one of `10`,
`25`, `50` and `80`: the cue's bottom edge as a percentage of the picture height above its bottom.
Any other value SHALL read as `auto`, the default.

#### Scenario: Unknown position

- **WHEN** a Profile stores `subtitle.position` as `33`
- **THEN** the player leaves every cue where its file placed it

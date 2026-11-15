## ADDED Requirements

### Requirement: The web player offers key and tempo controls for karaoke songs

The web player SHALL show key controls (a semitone down, a semitone up, reset) and tempo controls
(5% slower, 5% faster, reset) only when GraphQL `serverInfo.playbackAdjustments` reports the
accepted ranges and the playing Media is served as a composed karaoke picture. Each control SHALL
clamp to the advertised range. Every button SHALL carry an accessible label, the controls SHALL be
keyboard operable, and the current key and tempo SHALL be announced in a live region.

#### Scenario: Karaoke song on a server with the capability

- **WHEN** the server advertises pitch -6..6 and tempo 0.75..1.25 and the song is served as a
  composed picture
- **THEN** the player shows the key and tempo controls with "Key 0" and "Tempo 100%"

#### Scenario: No capability or not karaoke

- **WHEN** `serverInfo` has no `playbackAdjustments`, or the Media is not served as a composed
  picture
- **THEN** no key or tempo control is shown

#### Scenario: Clamped range

- **WHEN** the key is already at the advertised maximum
- **THEN** the raise-key control is disabled and no request goes beyond the range

### Requirement: A key or tempo change restarts at the same song position

A change SHALL restart playback with `adjustment{pitchShiftSemitones, tempo}` on the playback-info
request, starting at the song position the listener was at. The player SHALL keep positions in
song seconds: a stream position is the song position divided by the applied tempo, for the
restart, the resume point, progress reports and the transport's time, duration and seeks.

#### Scenario: Slower tempo mid-song

- **WHEN** a listener at song second 30 picks tempo 0.8
- **THEN** playback-info is asked for `tempo` 0.8 and the adjusted stream starts at stream second
  37.5

### Requirement: The player does not claim an adjustment the server did not apply

When a non-default adjustment is requested and the playback-info response does not echo
`adjustment`, the player SHALL show the requested key and tempo as not applied.

#### Scenario: Server did not apply it

- **WHEN** the listener raises the key and the response carries no `adjustment`
- **THEN** the bar shows "Key +1" with "Not applied"

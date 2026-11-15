## ADDED Requirements

### Requirement: Cast button only where the Cast SDK works

The web player SHALL load the Cast Web Sender SDK only in a secure context on a Chromium browser
that supports Cast, and SHALL make no request for it anywhere else. It SHALL show a Cast button
for video only after the SDK loaded and reports at least one receiver on the network.

#### Scenario: Unsupported browser

- **WHEN** a video plays in a browser without Cast support
- **THEN** no Cast SDK request is made and no Cast button is shown

#### Scenario: Receiver available

- **WHEN** the SDK loads and reports a receiver on the network
- **THEN** the player shows a Cast button

### Requirement: Server-chosen receiver route

Starting a cast SHALL request playback info for the Media with a Default Media Receiver
capability profile whose client starts with `chromecast-`, and SHALL load the server's first
stream on the receiver. When the server offers no stream the player SHALL say the video cannot be
cast.

#### Scenario: Cast with resume and subtitles

- **WHEN** a viewer casts a video with a resume point and a selected WebVTT subtitle
- **THEN** the receiver loads the server's route at the resume point with that subtitle active

#### Scenario: No route

- **WHEN** the server returns no stream for the receiver profile
- **THEN** the player shows that the video cannot be cast

### Requirement: Local controls drive the receiver

While a cast session has media loaded, the player SHALL show play/pause, position and stop
controls that act on the receiver through the Cast RemotePlayerController. Stopping SHALL end the
session and return the receiver's last position to the local player.

#### Scenario: Pause and seek on the receiver

- **WHEN** the viewer presses pause or moves the cast position while casting
- **THEN** the receiver pauses or seeks to that position

#### Scenario: Stop casting

- **WHEN** the viewer stops casting
- **THEN** the session ends and local playback resumes from the receiver's position

### Requirement: Minimal script policy for the sender

The web shell's Content Security Policy SHALL allow scripts only from `'self'` and the
`https://www.gstatic.com/` path prefixes the Cast sender loads from, never the whole host.

#### Scenario: Shell headers

- **WHEN** the web shell is served
- **THEN** its `script-src` lists `'self'` and only `www.gstatic.com` path prefixes

## ADDED Requirements

### Requirement: Graphics tracks are drawn by the Oscillux CD+G presenter

A composed picture from a graphics track SHALL be drawn by the pinned Oscillux
`oscillux-cdg-render` module, run in-process in a WebAssembly runtime with no file, network or
clock access, and fed to the encode as raw frames at the composed canvas size. A seek SHALL start
the renderer at the source position the output plays, and a tempo change SHALL scale the source
time each output frame shows. The renderer SHALL stop when the encode exits or its context ends.

#### Scenario: Graphics picture is rendered, not decoded

- **WHEN** a song with a graphics sidecar is composed live or precomputed
- **THEN** FFmpeg reads the picture as rawvideo from a pipe at the canvas size, and the renderer
  reads the graphics file

#### Scenario: Seek under a tempo

- **WHEN** a live composed stream starts at output second 30 with tempo 1.25
- **THEN** the renderer starts at source second 37.5 with tempo 1.25

#### Scenario: Encode ends first

- **WHEN** FFmpeg exits while the renderer is writing frames
- **THEN** the renderer stops and the process's Wait returns

#### Scenario: Embedded module matches its pin

- **WHEN** the server is built
- **THEN** the embedded module's SHA-256 equals the release asset's digest in `pin.json`

#### Scenario: Recipe change re-renders

- **WHEN** a Ready composed rendition was drawn with the xBR recipe
- **THEN** its digest no longer matches the current picture and the rendition is made again

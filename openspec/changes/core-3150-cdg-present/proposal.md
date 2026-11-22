# Modern CD+G presentation in composed video

Ticket: CORE-3150 (`queue:core:main`). Follows CORE-3149, which replaced nearest-neighbour scaling
with xBR.

## Why

xBR made graphics tracks smooth, but they still look like 1990s karaoke: a flat blue field, square
letters and no depth. The owner chose to modernise them in the server's composed video, so every
client gets the new look without a client or wire change.

## What Changes

- **Renderer**: a graphics track is drawn by `oscillux-cdg-render` from Oscillux v0.2.14, a WASI
  command that reads the CD+G stream on stdin and writes RGBA frames. It finds the background
  colour from the screen edge, draws smooth letters with round corners, casts a soft shadow under
  the ink and replaces the flat background with a drifting glow. Effects stay in Oscillux (ADR-0228).
- **Runtime**: core runs the module in-process with wazero (pure Go, Apache-2.0, no cgo). The
  module has no file, network or clock access: it sees only its arguments, stdin and stdout. It is
  compiled once per process.
- **Encode**: the composed encode reads the graphics picture from a rawvideo pipe at the canvas
  size (`-f rawvideo -pix_fmt rgba -s WxH -r 30 -i pipe:0`) instead of decoding the `.cdg` file
  with FFmpeg's decoder and upscaling it with `xbr`. A seek or tempo maps to the renderer's
  `--start` (source seconds) and `--tempo`. FFmpeg no longer needs the `xbr` filter for graphics.
- **Recipe digest**: `composedGraphicsRecipe` becomes `oscillux-cdg-v0.2.14`, so renditions drawn
  with xBR are made again.
- **Vendoring**: the module is embedded from `internal/ffmpeg/cdgrender/`, pinned to the release
  asset's SHA-256 in `pin.json`, and a test checks the embedded bytes against the pin.

## Out of scope

- Client-side CD+G rendering and serving the `.cdg` stream to clients (superseded by the owner's
  server-video decision).
- Timed-text (ASS) pictures, which keep their libass path.

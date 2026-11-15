# Console transcoder settings: simultaneous transcodes, temp directory, HDR tone mapping

Tickets: CORE-1970 (`queue:core:main`)

## Why

The concurrent-transcode cap and the transcoder scratch directory could only be set with
`BEAMFALL_TRANSCODE_CAP` / `BEAMFALL_TRANSCODE_TMP` (or their flags), and HDR-to-SDR tone mapping
could not be turned off at all. An owner should manage all three from Console Config, and see
when an environment variable decides the value instead.

## What changes

- `transcodeConfig` / `setTranscodeConfig` gain the cap (`maxConcurrentTranscodes`, with
  `clearMaxConcurrentTranscodes` to return to automatic), the temp directory (`tempDir`, empty
  for the server default) and `toneMapEnabled`. Each setting reports the value in effect, whether
  an environment variable or flag overrides it, and whether a restart is needed to apply it.
- Validation: a cap below 1 or a non-whole number is rejected; a temp directory must be an
  absolute path to an existing, writable directory. Nothing is stored on a rejected write.
- Settings persist in the existing server-config key/value store (no migration).
- Precedence: environment variable or flag, then the saved value, then the automatic value (the
  hardware probe's cap, `<data dir>/transcode`).
- Live where possible: the cap applies to the running transcoder at once and tone mapping
  switches at once. The temp directory is fixed when the transcoder starts, so a change reports
  `tempDirRestartRequired` until the server restarts. A saved directory that is no longer usable
  at startup is logged and the default is used.
- With tone mapping off, a video that needs HDR-to-SDR conversion is refused with the new reason
  `hdr-tone-map-disabled` rather than encoded without conversion. ffmpeg's rendition gate and
  playback's decision report the same reason; the web player maps it to its own message.
- The write is audited as a `config.update` activity on `transcode`, like the existing policy write.
- Console Config edits the cap and the tone-map switch in Normal, the temp directory in Advanced
  (kept visible when it holds a value or needs attention), and shows server rejections.

## Wire contract

The new `TranscodeConfigInfo` fields and mutation arguments are additive (ADR-0122). The wire
goldens are regenerated here; a new client-core-wire minor must be published
(`make publish-wire-contract`) and pinned before merge, then consumers re-vendor.

## Non-goals

- Moving the temp directory of a running transcoder without a restart.
- Native-client Console screens or native mapping of `hdr-tone-map-disabled`.

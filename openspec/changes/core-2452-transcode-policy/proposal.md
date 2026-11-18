# Console transcode policy reaches the encoder

Ticket: CORE-2452 (`queue:core:main`). Found in the CORE-2445 settings audit.

## Why

Console > Playback & sign-in saves a hardware-acceleration switch, a codec and a quality, and the
API reads them back, but no transcoder code reads them. The encoder is chosen from the startup
probe whatever the switch says, and every session encodes with the same arguments. The help text
has to admit that the settings do nothing.

## What Changes

- The hardware switch applies to new sessions, live. Off makes every new session encode in
  software (`libx264`), with no hardware upload, device decode or hardware tone-map, and stops HEVC
  output, which has no software encoder on the realtime path. On restores the encoder the startup
  probe verified.
- The switch moves to a new key, `transcode.hardware`, which defaults to on. The old
  `transcode.hardware_enabled` key never had an effect, and the Console sent `false` with every
  save, so many servers hold an off value nobody chose. Honouring it would turn hardware off for
  them on upgrade. The old key is ignored.
- With hardware off, the automatic concurrent-transcode cap is the software cap for the host, so
  turning hardware off does not leave a hardware-sized cap on a CPU encoder.
- Codec: Automatic (the default) keeps today's choice, HEVC when the device and the verified
  hardware encoder support it and H.264 otherwise. `h264` makes new sessions H.264 only. The
  Console offers Automatic and H.264 only. The API still accepts `hevc` and stores it as
  Automatic, which is what it always meant.
- Quality applies to new sessions. Balanced (the default) keeps today's arguments. Software encodes
  use CRF 28 for low and 20 for high. Hardware encodes aim at three quarters of the rung bitrate
  for low, and use a slower preset for high where the encoder has one: NVENC `p6`, QSV `medium`,
  AMF `-quality balanced`. VA-API and VideoToolbox high is the same as balanced.
- The Console help for the three settings describes this behaviour.

## Not in this change

- Sessions already running keep their encoder until they end.
- The device hint still applies at startup only.

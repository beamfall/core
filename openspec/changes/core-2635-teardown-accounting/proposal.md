# Separate encoder admission from directory cleanup

## Why

CORE-2635 splits playback teardown into stopping session work and removing its
directory. A slow directory removal can outlast the encoder and its readers.

## What Changes

- Give each encoder run ownership of its transcode slot and governor lease.
  Release both on encoder exit, after reader revocation if the session is
  detached. A replacement within an attached session inherits the admission.
- Drain in-progress work before directory removal. Keep the directory owned
  by teardown or its cleanup retry until removal succeeds.

## Impact

The change is internal to playback teardown and admission. The existing
replacement and shutdown requirements remain in the playback self-healing spec.

# Separate encoder admission from directory cleanup

## Why

CORE-2635 splits playback teardown into stopping session work and removing its
directory. A slow directory removal can outlast the encoder and its readers.

## What Changes

- Release the transcode slot and governor lease once the encoder has stopped
  and the session's readers have been revoked.
- Drain in-progress work before directory removal. Keep the directory owned
  by teardown or its cleanup retry until removal succeeds.

## Impact

The change is internal to playback teardown and admission. The existing
replacement and shutdown requirements remain in the playback self-healing spec.

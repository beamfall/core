# Separate encoder admission from directory cleanup

## Why

CORE-2635 splits playback teardown into stopping session work and removing its
directory. A slow directory removal can outlast the encoder and its readers.

## What Changes

- Release the transcode slot once the encoder has stopped and the session's
  readers and in-progress joins have drained.
- Hold the detached session's lease until its directory has been removed.
  A failed removal carries the lease into the existing cleanup retry.

## Impact

The change is internal to playback teardown and admission. The existing
replacement and shutdown requirements remain in the playback self-healing spec.

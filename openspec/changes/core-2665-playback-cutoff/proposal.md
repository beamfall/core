## Why

CORE-2665 binds live HLS requests to an exact playback instance and makes process capacity track encoder ownership through reap.

## What Changes

- Bind live requests before verification and keep the binding through source preparation and artifact open.
- Fence access and spawn on termination, and release encoder capacity at reap.
- Move filesystem and process I/O outside the session manager lock.
- Qualify playback observations by source generation and make shared FFmpeg waits cancellable.

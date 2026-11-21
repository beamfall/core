## Why

CORE-2666 gives the library lifecycle a fence that confirms playback resources have been released before the library can reopen.

## What Changes

- Track library admission across playback-info and live HLS requests.
- Commit playback-info grants together under the original admission stamp.
- Join library-owned playback work and scratch cleanup under a retryable drain fence.

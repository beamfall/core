## Why

CORE-1630 makes the server Play Queue the shared playback state for a Profile. Media identity currently doubles as queue identity, so clients cannot queue the same Media twice, insert a batch in one call, or resume the queue from another device.

## What Changes

- Give each queue entry a durable ID and migrate existing entries in their original order.
- Persist the current entry, position in milliseconds, shuffle order and repeat mode.
- Add Graph operations for batch insertion and entry-addressed playback and editing. Keep existing Graph fields and their argument and result shapes.
- Apply the same Profile precondition and Media visibility checks to the new writes.

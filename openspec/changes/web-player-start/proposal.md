# Start web playback without waiting on markers, trickplay or a resume seek

Ticket: CORE-0965 (`queue:core:main`).

## Why

The web player awaits the markers request and the trickplay manifest before it attaches the stream,
so a slow or hung optional request delays the first frame. A resumed hls.js session loads from
position zero and then seeks. The player route's code is fetched only after the viewer presses play.

## What Changes

- The stream is attached as soon as playback info resolves. Markers are requested alongside playback
  info, trickplay as soon as info names it, and both are applied when they arrive.
- A resumed hls.js session starts loading at the resume position (`startPosition`). The player's own
  metadata seek remains only on the native `<video src>` path.
- Pointing at or focusing a media card prefetches the player route chunk.

## Non-goals

- Reducing per-`timeupdate` renders in the transport controls.
- Changing playback decisions, error recovery, track selection or progress reporting.
- Measuring wall-clock time to first frame; that needs a real browser and network.

## Failure modes

- A failed markers or trickplay request stays silent and leaves playback untouched.
- Markers or trickplay that arrive after playback has ended or failed are discarded, so a terminal
  state never picks them up.
- A stream-token refresh reloads the source at the current position, not the original resume point.
- A failed chunk prefetch is forgotten; the route's own load reports any real failure.

## Acceptance evidence

- `usePlayer.test.tsx`: a markers request that never resolves does not delay `video.src` or
  `hls.loadSource`; a resumed hls.js session receives the resume position and performs no seek on
  metadata; a token refresh updates the start position.
- `MediaItemCard.test.tsx`: pointer entry and focus prefetch the player chunk for media cards only.

## Rollback

Revert the change. It holds no stored state and changes no wire contract.

## Capabilities

### New Capabilities

- `web-playback-start`: what the web player waits for before it starts a stream.

### Modified Capabilities

None.

# The web player says converting is off when a direct-play file will not decode and HLS was withheld

Ticket: CORE-2181 (`queue:core:main`).

## Why

When direct play fails in the browser with `MediaError` 3 or 4 and playbackInfo offered no HLS
stream, the web player shows the generic unsupported-codec message. If HLS was withheld because the
Profile's `transcodeAllowed` is false, the viewer is never told that turning converting on would
fix it: the server returns `transcode-disallowed` only on an `unavailable` decision, when it knew up
front that a transcode was needed. A `direct-play` decision carries no reason for the missing HLS
stream, and neither ProfileSummary nor SessionInfo carries `transcodeAllowed`.

## What Changes

- A `direct-play` decision that withholds the HLS stream because the Profile's `transcodeAllowed`
  is false carries reason `transcode-disallowed`. A Dolby Vision P7 base-layer decision keeps its
  own `dolby-vision-p7-base-layer` reason. Other refusals (stream limit, capacity, source or policy
  refusals) leave the reason empty as today.
- On a direct-play decode failure with no HLS stream to fall over to, the web player shows the
  existing `playbackTranscodeDisallowed` message when the decision's reason is
  `transcode-disallowed`, and the unsupported-codec message otherwise.
- The playback details panel labels that reason in plain words instead of the raw token.

## Impact

- `internal/playback` (decision reason) and `internal/web/app` (player and details copy).
- No wire contract change: `reason` is an existing optional string on playbackInfo. Native
  clients that render an unknown reason verbatim on a direct-play decision will show the token.
- No schema, migration or golden change.

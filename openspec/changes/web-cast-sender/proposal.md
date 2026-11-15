# Web Cast sender for video

Ticket: CORE-1679

## Why

The web viewer can play video only on the screen it runs on. Viewers with a Google Cast receiver
on their network expect a Cast button in the player that hands the video to the TV and keeps the
browser as the remote.

## What Changes

- The web player shows a Cast button for video only where the Cast Web Sender SDK can work: a
  secure context in a desktop or Android Chromium browser. The SDK is loaded lazily on that
  browser only; every other browser makes no request and shows no button. The button stays
  hidden until the SDK reports a receiver on the network.
- Starting a cast opens the receiver picker inside the click, then asks `POST /api/playback-info`
  for the Media with a Default Media Receiver capability profile (client
  `chromecast-default-receiver`: MP4 / H.264 up to 1080p level 4.1, stereo AAC, WebVTT). The
  server's first stream is the route; the sender does not pick one.
- The receiver starts at the local position once playback has moved, otherwise at the server's
  resume point, with the server's WebVTT caption tracks side-loaded and the server-selected
  subtitle active.
- While casting, the player shows cast controls (play/pause, position, stop) that drive the
  receiver through `RemotePlayerController`. Stopping hands the receiver's position back to the
  local player.
- The web shell's Content Security Policy gains a `script-src` naming `'self'` and the three
  `www.gstatic.com` path prefixes the sender script and its framework load from. No other
  directive changes.
- New code lives under `internal/web/app/src/areas/viewer/player/cast/`; the player gets one
  additive mount. Strings live in `strings/cast.ts`.

No wire-contract change: the profile travels in the existing `capabilityProfile` argument.

## Non-goals

- Native-client Cast senders and a custom Beamfall receiver application.
- Driving the receiver from the player's main transport and keyboard shortcuts; only the cast
  control group drives it in this change.
- Switching subtitles or audio on the receiver mid-cast.
- Quirks-DB entries for Cast receivers.

## Failure modes

- Receiver-bound grants (ADR-0194 section 6): a `chromecast-` capability profile marks the Stream
  Token receiver-bound, and media bytes for it fail closed until a quirks-DB selector resolves the
  receiver profile. `internal/playback/quirks/baseline.json` is empty today, so on a real
  receiver the load fails and the player shows "The cast device could not play this video."
  rather than bypassing that boundary. Adding the selector is a security-reviewed follow-up with
  ADR-0089's receiver-trusted TLS path.
- The receiver fetches media from the page's origin. A page opened on `localhost` gives the
  receiver an unreachable URL; casting needs the server's LAN or public address.
- No route for the profile: the player says "This video cannot be cast from this server."
- Closing the receiver picker returns to the idle state with no error.
- The SDK failing to load or timing out leaves the player without a Cast button.

## Acceptance evidence

- vitest with a mocked Cast framework (no physical receiver was available): SDK gating and
  single lazy load, route and start selection, subtitle selection, the load request the receiver
  gets, remote play/pause, seek and stop, failure and cancel states.
- Go test pinning the CSP `script-src` to `'self'` plus `https://www.gstatic.com/` path prefixes.

## Rollback

Revert the change: remove the `cast/` folder, the mount in `PlayerTransport.tsx`, the strings and
the `script-src` directive. No stored state or migration is involved.

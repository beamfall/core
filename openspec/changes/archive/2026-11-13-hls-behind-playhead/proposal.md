# Keep waiting for a healthy encoder, and never report unseen progress

Ticket: CORE-0044 (`queue:core:main`, PLAYBACK-HLS-TIMELINE-RUNAWAY-1).

## Why

The far-seek runaway this ticket was filed for no longer reproduces on main: a 30:00 seek into a
cold 1080p transcode presents its first frame in about 1.6 s and plays at 1:1. Two parts of the
failure remain. A segment request whose encoder is running but behind the playhead still fails
after one latency budget, and the byte layer turns that into a retryable 503. And the web player
reports `video.currentTime` as progress even while a seek is pending, so a stalled seek can
persist a resume point the viewer never saw.

## What Changes

- A segment request keeps waiting while the session's encoder for that rendition is the same
  run and wrote at least one new segment during the last budget-long window. A window with no
  new segment, or an encoder another request replaced, fails as before.
- The web player reports the last position whose frame was presented. While a seek is pending
  it reports the position before the seek, and before any frame is presented in a load it
  reports nothing.
- A 503 keeps the existing recovery: hls.js reloads from where it stopped, and the playhead
  stays where it is. A test now pins that.

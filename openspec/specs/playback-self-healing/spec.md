# playback-self-healing Specification

## Purpose
How live playback recovers on its own and explains itself when it cannot: the live HLS session restarts an exited encoder within the waiting request, a truncated source never yields playable empty segments, the web player retries transient faults and ends only on revocation, shows progress and a reason with an action, and seeks once per scrub gesture.

## Requirements

### Requirement: A waiting segment request heals an exited encoder

The live HLS session manager SHALL detect that a session's encoder has exited while a segment
request waits for a segment the encoder has not produced. It SHALL restart the encoder at the
requested segment within that request, up to a bounded number of restarts. Once the bound is
used up, it SHALL fail the request without waiting out the latency budget. It SHALL log each
unexpected exit once, with its exit status and a bounded tail of the encoder's diagnostics.

#### Scenario: Encoder killed mid-play

- **WHEN** the encoder exits before writing the segment a request is waiting for
- **THEN** the request restarts the encoder at that segment and returns the segment without
  waiting out the latency budget

#### Scenario: Encoder keeps failing

- **WHEN** the encoder exits again after the restart bound is used up
- **THEN** the request fails promptly, and the exit status is logged

### Requirement: Truncated sources never yield playable empty segments

The server SHALL NOT serve a segment as playable when the segment contains no media or lies past
the point where the source ended. It SHALL fail such a request promptly, and SHALL log the
position at which the source ended.

#### Scenario: Source shorter than its header duration

- **WHEN** a client requests a segment past the point where a truncated source ended
- **THEN** the request fails promptly, and no empty segment is returned with a success status

### Requirement: The web player retries transient faults and ends only on revocation

The web player SHALL enter its session-ended state only on an explicit revocation signal. It
SHALL treat these as transient faults: a failed token refresh, a reaped or preempted session,
and a network error on any route. For a transient fault it SHALL recover, or offer a retry that
resumes at the last presented position. While the browser is offline, it SHALL wait without
spending its retry budget, and SHALL resume when the browser comes back online.

#### Scenario: Token refresh fails on the network

- **WHEN** the stream-token refresh fails with a network or server error
- **THEN** the player shows a retryable message instead of ending the session

#### Scenario: Connectivity drops for 20 seconds

- **WHEN** the browser goes offline during playback and comes back online 20 seconds later
- **THEN** the player resumes playback without reaching a failed state

### Requirement: The web player shows progress and reasons

The web player SHALL show a progress indicator from attach until the first frame, and whenever
playback waits for data for longer than a short delay while not paused. When the server reports
that playback is unavailable, the player SHALL show copy and an action specific to the server's
reason, falling back to generic copy for an unknown reason.

#### Scenario: Cold transcode start

- **WHEN** the stream is attached and no frame has been presented yet
- **THEN** the player shows a progress indicator

#### Scenario: Capacity reached

- **WHEN** playback info reports that playback is unavailable with the reason
  `transcode-capacity-reached`
- **THEN** the player explains that the server is busy and offers to try again

### Requirement: Scrubbing seeks once per gesture

The web player SHALL preview the position locally while the scrubber is being dragged. It SHALL
seek the media once, when the drag is released. Keyboard steps SHALL seek immediately.

#### Scenario: Drag across the timeline

- **WHEN** the viewer drags the scrubber across many positions and releases
- **THEN** the media element receives exactly one seek, to the release position

### Requirement: A segment request waits for a healthy encoder behind the playhead

The live HLS session manager SHALL keep a segment request waiting while the encoder it waits on
is still the session's encoder for that rendition, started at or before the requested segment,
and wrote a segment during the last latency-budget window. Otherwise it SHALL fail the request
after that window. It SHALL NOT restart an encoder that is making progress.

#### Scenario: Encoder slower than real time

- **WHEN** a request waits for a segment past the encoder's frontier and the encoder keeps
  writing at least one segment per latency budget, but more slowly than real time
- **THEN** the request returns the segment once it is written, without a restart and without
  failing

#### Scenario: Segments cached past the encoder

- **WHEN** segments from an earlier encoder already exist past the one the encoder is writing
- **THEN** they do not count as progress, and the encoder's own writes do

#### Scenario: Encoder replaced past the segment

- **WHEN** a request waits for a segment and another request replaces the encoder with one
  started past that segment
- **THEN** the request fails after one window, however many segments the new encoder writes

#### Scenario: Encoder stalls

- **WHEN** a request waits for a segment and the encoder writes nothing for a whole window
- **THEN** the request fails after that window

### Requirement: The web player reports only presented progress

The web player SHALL report as playback progress only a position whose frame was presented.
While a seek is pending, or before the element has decoded the current frame, it SHALL report
the last presented position. Before the load has presented any frame it SHALL report nothing.

#### Scenario: A far seek stalls

- **WHEN** the viewer seeks to a position the stream has not produced yet and pauses or leaves
  before that frame is presented
- **THEN** the progress report carries the position presented before the seek

### Requirement: A waiting request restarts a stalled encoder

The live HLS session manager SHALL track each live encoder's output progress. While a request
waits on an encoder, it SHALL kill the encoder if the encoder's output position has not advanced
for a bounded interval. It SHALL treat the kill as an unexpected exit: the waiting request
restarts the encoder within the existing restart bound and session budget, and fails promptly
once they are used up. It SHALL NOT kill an encoder that no request is waiting on.

#### Scenario: Encoder hangs without exiting

- **WHEN** a request waits on an encoder whose output position stops advancing
- **THEN** the encoder is killed after the stall interval and restarted at the requested segment

#### Scenario: Encoder keeps hanging

- **WHEN** every restarted encoder also stops advancing
- **THEN** the request fails with a classified encoder failure once the restart bound is used up,
  and the stall is logged

### Requirement: A resumed transcode starts its encoder once

When playback info resumes a viewer at a non-zero position, the live HLS session SHALL start
its first encoder at the segment that contains that position. A request for the init segment
SHALL reuse any encoder already running for its rendition.

#### Scenario: Resume at an offset

- **WHEN** a client resumes a transcode at a non-zero position and requests the init segment,
  then the segment at that position
- **THEN** the encoder is spawned once, at that segment

### Requirement: One session's encoder churn does not stall other sessions

The live HLS session manager SHALL NOT hold its global lock while it stops an encoder, waits for
an encoder to exit, spawns an encoder or removes a session's directory.

#### Scenario: Slow encoder teardown

- **WHEN** one session's encoder is slow to exit while that session is torn down
- **THEN** segment requests for other sessions are served without waiting for it, and the torn
  down session's directory is removed once its encoder has exited

#### Scenario: Slow encoder replacement

- **WHEN** a far seek replaces a session's encoder and the old encoder is slow to exit
- **THEN** other sessions' requests are served meanwhile, and the replacing session still counts
  against the transcode cap

### Requirement: Encoder work done outside the lock still counts

A session whose encoder is being replaced SHALL keep its transcode slot and lease until the
replacement starts or fails, and SHALL NOT be preempted meanwhile. A torn-down session SHALL keep
its slot and lease until its encoder has stopped. Shutdown SHALL NOT return while an encoder start
or teardown is still in progress.

#### Scenario: Eviction during a replacement

- **WHEN** a session is torn down while its encoder is being replaced
- **THEN** the teardown does not wait for the replacement, the replacing request fails with the
  session-evicted error, and the session's lease and directory are released

#### Scenario: Slow teardown at the transcode cap

- **WHEN** the transcode cap is full and a torn-down session's encoder is slow to exit
- **THEN** a new session is refused until that encoder has stopped, and the governor keeps
  counting it as a realtime transcode until then

#### Scenario: Shutdown during a replacement

- **WHEN** the server shuts down while a session's encoder is being replaced, or while another
  teardown is still stopping an encoder
- **THEN** shutdown returns only after those encoders have stopped and their leases and
  directories are released

#### Scenario: Preemption during a replacement

- **WHEN** the transcode cap is full and a higher-priority session asks for a slot while the only
  session holding one is replacing its encoder
- **THEN** the higher-priority session is refused rather than launched over the cap

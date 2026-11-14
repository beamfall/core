## ADDED Requirements

### Requirement: Live composed picture when no precomputed rendition is ready

For an audio Media whose plugin describes a composed picture, playback negotiation SHALL answer a
video-capable client with a live composed HLS stream (verdict `transcode`, reason
`composed-picture`) when no Ready composed rendition matches the current picture and the transcode
policy and capacity admit a stream.

#### Scenario: Graphics track, nothing precomputed

- **WHEN** a client declaring H.264 video requests a song with a graphics sidecar and no Ready
  composed rendition
- **THEN** the decision is `transcode` with reason `composed-picture` and the stream grant carries
  `composedPicture`

#### Scenario: Timed text, nothing precomputed

- **WHEN** the same client requests a song whose plugin supplies a timed-text document
- **THEN** the decision is `transcode` with reason `composed-picture`

#### Scenario: Ready rendition preferred

- **WHEN** a Ready composed rendition matches the song's current picture
- **THEN** the decision is `precomputed-rendition` with reason `composed-picture`

#### Scenario: Client without H.264 video

- **WHEN** a client that declares no H.264 video requests the song
- **THEN** the decision does not compose a picture and the audio plays as before

### Requirement: A live composed picture honours client and server limits

A live composed stream SHALL be encoded at the largest composed shape (720p, 480p or 360p) whose
height and peak bandwidth fit the client's declared H.264 limits and any matched quirk caps, with
the picture scaled to it. When a quirk denies H.264 or no shape fits, the decision SHALL NOT
compose a picture and the audio SHALL play as before.

#### Scenario: Client height ceiling

- **WHEN** a client declaring H.264 video with a maximum height of 360 requests a song with no
  Ready composed rendition
- **THEN** the decision is a live composed stream whose single rendition is 360 pixels high

#### Scenario: Server quirk height cap

- **WHEN** a matched quirk caps the client at 360 pixels
- **THEN** the live composed stream is 360 pixels high

#### Scenario: No composed shape fits

- **WHEN** the client's or quirk's limits exclude every composed shape, or a quirk denies H.264
- **THEN** the decision does not compose a picture and the audio plays as before

### Requirement: A live composed picture needs a capable FFmpeg

Before offering a live composed stream, the server SHALL check that the resolved FFmpeg build
provides every filter and encoder the picture's composition recipe needs (for timed text, the
libass `ass` filter), caching the answer per FFmpeg binary. When one is missing, the decision SHALL
NOT compose a picture and the audio SHALL play as before; the server SHALL NOT substitute a picture
without the timed text.

#### Scenario: FFmpeg without libass

- **WHEN** a song whose plugin supplies a timed-text document has no Ready composed rendition and
  the FFmpeg build has no `ass` filter
- **THEN** the decision does not compose a picture and the audio plays as before

#### Scenario: Ready rendition needs no live encode

- **WHEN** a Ready composed rendition matches the song's current picture on the same FFmpeg build
- **THEN** the decision is still `precomputed-rendition` with reason `composed-picture`

### Requirement: Live composed streams share the transcode session lifecycle

A live composed stream SHALL carry the picture over the Media's audio and SHALL be subject to the
same session limits, preemption, idle reap and cleanup as every live transcode.

#### Scenario: Session limits apply

- **WHEN** the transcode capacity is exhausted
- **THEN** no live composed stream is offered and the existing refusal or fallback applies

### Requirement: A live composed play queues the precomputed rendition

After answering with a live composed stream, the server SHALL queue the Media's composed rendition
at raised priority, unless a rendition for the current picture already exists, so a later play is
served precomputed.

#### Scenario: First play queues the render

- **WHEN** a song is played live composed
- **THEN** a precompute job for its composed rendition is queued with a priority above the
  Schedule sweep's

## ADDED Requirements

### Requirement: Audio Media direct-plays within the declared audio envelope

The playback negotiation SHALL NOT require a video codec match for a Media whose class is audio
and which has no video stream. Such an item SHALL direct-play when its container and audio codec
are within the client's declared envelope.

#### Scenario: mp3 for a client that declares video codecs

- **WHEN** a client declaring `videoCodecs`, `audioCodecs` including `mp3` and the `mp3` container
  requests playback of an audio-class mp3
- **THEN** the decision is `direct-play`

#### Scenario: Undeclared audio codec still transcodes

- **WHEN** the same client requests an audio-class item whose codec it does not declare
- **THEN** the decision is not `direct-play`

### Requirement: Media read reports artwork and plugin-declared credits

The `media(id)` read SHALL report the Media's artwork slots and the Entities its plugin relates to
it, each with id, title, kind, the kind's display label and archetype, in the plugin's declaration
order, filtered by the caller's scope like every read.

#### Scenario: Music track

- **WHEN** a client reads a track related to an artist and an album
- **THEN** `credits` lists the artist and the album with their kind labels, and `artworkSlots`
  carries the track's stored artwork

### Requirement: Web player presents audio Media

When playback-info reports `mediaClass` `audio`, the web player SHALL show the Media's artwork (or
the accent fallback), its title and its credits, and SHALL NOT show an empty video frame.

#### Scenario: Track from an album

- **WHEN** a viewer plays a track
- **THEN** the player shows the artwork, the title and the credits line (for example the artist and
  the album)

### Requirement: Web player queue

A Media played from a list SHALL carry the list's playable siblings as a queue. The player SHALL
offer previous and next controls when there is a previous or next item, SHALL declare the next
item to the server, and SHALL keep the queue when it advances.

#### Scenario: Next track

- **WHEN** a viewer plays the second of five tracks and presses Next
- **THEN** the third track plays and Previous returns to the second

### Requirement: Honest route and unavailable labels

The player SHALL label a route "Server transcode" only when the server's decision is `transcode`,
and SHALL name the server's reason when playback is unavailable.

#### Scenario: Unavailable item

- **WHEN** the server answers `unavailable` with a reason
- **THEN** the message states that reason in plain words

### Requirement: Per-container skip interval

The player SHALL show the per-container skip-interval override only when the Media belongs to a
container Entity, label it with the container's kind, and store it against the container.

#### Scenario: Film without a container

- **WHEN** a viewer opens settings while playing a Media with no container Entity
- **THEN** no per-container skip-interval row is shown

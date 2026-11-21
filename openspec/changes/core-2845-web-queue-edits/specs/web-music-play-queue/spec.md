## ADDED Requirements

### Requirement: Audio track menus edit the server Play Queue

The viewer SHALL offer Play next and Add to queue on audio Media with known unlocked state. The
currently playing Media SHALL be ineligible for either action. Play next SHALL place the selected
Media after the current Media, or at the head when playback has no queue cursor. Add to queue
SHALL place the selected Media at the tail. An existing row SHALL move to the requested position.

#### Scenario: The selected track already appears in the queue

- **WHEN** a listener chooses Add to queue for a track that appears before the tail
- **THEN** the track moves to the tail and appears exactly once
- **AND** the viewer acknowledges the edit after reading the resulting server order

### Requirement: A catalog run converts on the first queue edit

Before the first write, the viewer SHALL obtain the complete source listing. Conversion SHALL put
the current Media first, preserve the relative order of existing queued rows, then append missing
playable catalog tracks in source order before applying the requested placement.

#### Scenario: The source extends past the player's navigation window

- **WHEN** a listener edits the queue while playing an album or Play all run with additional pages
- **THEN** conversion includes playable tracks beyond the first page and beyond the navigation window

### Requirement: Queue edits are serialized with playback advancement

The viewer SHALL serialize queue edits and playback advancement. Every write SHALL carry the expected
Profile ID. A profile mismatch SHALL drop the edit. Once a conversion write has begun, the viewer
SHALL use only a server reread to recover and SHALL issue no automatic repeat writes.

#### Scenario: A conversion write loses its response

- **WHEN** a write commits and its response is lost during conversion
- **THEN** the viewer rereads the queue and uses the resulting server cursor and order for advancement

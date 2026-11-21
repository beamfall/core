## ADDED Requirements

### Requirement: Audio track menus edit the server Play Queue

The viewer SHALL offer Play next and Add to queue on audio Media with known unlocked state while
idle or during a server-queue run for the active Profile. The `?playQueue=<profileID>` route
identifies a server-queue run. The viewer SHALL hide these actions during catalog playback and for
rows whose lock state is unknown. A search row omitted by a refresh SHALL lose its eligibility,
including rows retained from later pages. Both actions SHALL be disabled for the current track.

#### Scenario: Catalog playback starts on an already-queued track

- **WHEN** a listener plays a catalog track whose Media ID is also in the server queue
- **THEN** playback follows the catalog and its track menus omit the server queue edits

### Requirement: Queue edits place tracks relative to the server cursor

Play next SHALL place the selected Media immediately after the current queue cursor or at the head
while idle. Add to queue SHALL place the selected Media at the tail. An existing row SHALL move to
the requested position. Before writing, the viewer SHALL confirm the identity, playback context,
and presence of any active cursor. It SHALL skip the write for a known locked target.

#### Scenario: The selected track already appears in the queue

- **WHEN** an idle listener chooses Add to queue for a track that appears before the tail
- **THEN** the track moves to the tail and appears exactly once
- **AND** the viewer acknowledges the edit after reading the resulting server order

### Requirement: Queue edits are serialized with server advancement

The viewer SHALL serialize each edit and its confirming reread with server-queue advancement.
An edit SHALL issue one queueAdd with the expected Profile ID and Reveal state. A Profile mismatch
or relock SHALL drop the edit and invalidate the reader. The viewer SHALL confirm placement by
rereading, even after a lost response, and SHALL never replay the write. If the reread fails,
audio SHALL stay attached and advancement SHALL require a fresh read.

#### Scenario: A delayed append overlaps Next

- **WHEN** the listener requests Next while Add to queue is awaiting confirmation
- **THEN** advancement waits for the edit and uses a fresh server read to resolve the next track

#### Scenario: Next begins before a menu edit

- **WHEN** the listener makes an edit while Next is reading the queue
- **THEN** the edit waits and uses the cursor reached by Next for the same server-queue run

#### Scenario: The queue write commits and its response is lost

- **WHEN** a write commits and the response is lost
- **THEN** the viewer rereads the queue and acknowledges the edit only if the placement is confirmed

### Requirement: Queue addition can require an unchanged Reveal state

The queueAdd mutation SHALL accept an optional boolean `expectedRevealed`. When supplied, the
server SHALL compare it with the request scope and keep the session's Reveal state and Profile
binding stable from validation through the queue write. A Reveal mismatch SHALL return
`CONFLICT` with reason `REVEAL_STATE_MISMATCH` and leave the stored queue unchanged. Omission
SHALL preserve the existing queueAdd behavior.

#### Scenario: Another tab relocks after preflight

- **WHEN** a listener reads the queue while Revealed and another tab relocks the same session
  before their queueAdd applies
- **THEN** a queueAdd carrying `expectedRevealed: true` is refused even for a Media still visible
  after relock
- **AND** the viewer drops the edit and invalidates the scoped reader

### Requirement: Overlapping search reads preserve the latest row lock state

The viewer SHALL resolve overlapping search responses for a row by request order. A later
refresh page that observes a lock SHALL supersede an older Load more response for the same row.

#### Scenario: A row moves between pages during a refresh

- **WHEN** a refresh page returns a locked row that an earlier Load more request saw unlocked
- **THEN** the row stays locked regardless of which response settles first

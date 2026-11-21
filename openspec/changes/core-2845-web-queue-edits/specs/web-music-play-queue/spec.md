## ADDED Requirements

### Requirement: Audio track menus edit the server Play Queue

The viewer SHALL offer Play next and Add to queue for known unlocked audio Media while idle or
on the active Profile's `?playQueue=<profileID>` route. Both actions SHALL be disabled for the
current track. The viewer SHALL hide them during catalog playback and on search results.
CORE-2865 owns catalog conversion. CORE-2867 owns search queue actions.

#### Scenario: Catalog playback starts on an already-queued track

- **WHEN** a listener plays a catalog track whose Media ID is also in the server queue
- **THEN** playback follows the catalog and its track menus omit the server queue edits

### Requirement: Queue edits place tracks relative to the server cursor

Play next SHALL place the track after the cursor or at the head while idle. Add to queue SHALL
place it at the tail. Existing rows SHALL move. Before writing, the viewer SHALL verify identity,
playback context and cursor presence, and skip known locked targets. CORE-2866 owns server-side
enforcement after the best-effort client lock check.

#### Scenario: The selected track already appears in the queue

- **WHEN** an idle listener chooses Add to queue for a track that appears before the tail
- **THEN** the track moves to the tail and appears exactly once
- **AND** the viewer acknowledges the edit after reading the resulting server order

### Requirement: Queue edits are serialized with server advancement

The viewer SHALL serialize each edit and its confirming reread with server-queue advancement.
An edit SHALL issue one queueAdd with the expected Profile ID. A Profile mismatch or an observed
relock SHALL drop the edit and invalidate the reader. The viewer SHALL confirm placement by
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

#### Scenario: The confirming queue read observes a relock

- **WHEN** the confirming read reports that the session has relocked
- **THEN** the viewer drops the edit without acknowledging it and invalidates the scoped reader

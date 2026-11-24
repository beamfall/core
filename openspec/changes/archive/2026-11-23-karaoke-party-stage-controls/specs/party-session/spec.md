## ADDED Requirements

### Requirement: The stage names each up-next singer, song and expected time

The stage ticker and the stage's idle up-next list SHALL show each waiting entry's singer, song
title and artist as distinct parts, and the entry's expected start from the server's ETA: "next"
for the first entry when it starts within a minute, otherwise the whole minutes until it starts,
at least one.

#### Scenario: The ticker shows when each singer is up

- **WHEN** the first waiting entry's ETA is 30 seconds and the second's is 420 seconds
- **THEN** the ticker shows the first singer as next and the second as in about 7 minutes

### Requirement: The host sets a screen's join QR corner and mute

Each stage screen SHALL carry a join-QR corner, one of off, top-left, top-right, bottom-left or
bottom-right, defaulting to off, and a mute flag, defaulting to off. Both are stored on the server
for the screen's life. Only the session's host SHALL be able to change them, and a change SHALL
affect only that screen. While a song plays, a screen with a corner set SHALL show the join QR
and code in that corner. A muted screen SHALL play without sound and SHALL NOT ask for a sound
gesture.

#### Scenario: A muted screen plays silently

- **WHEN** the host mutes a screen and the next song starts
- **THEN** that screen plays the song without sound and other screens keep their sound

#### Scenario: The join QR follows the chosen corner

- **WHEN** the host sets a screen's join QR to bottom-right and that screen reloads
- **THEN** the screen reads its corner as bottom-right and draws the join QR there during songs

### Requirement: A skipped singer can be brought back

When the host skips the current entry, the server SHALL record it as skipped rather than done.
The host SHALL be able to bring back a skipped entry of the live session whose singer is still in
the session. A brought-back entry SHALL wait as next up without interrupting the current entry.
An entry that finished playing SHALL stay done and SHALL NOT be offered to bring back.

#### Scenario: A late singer is brought back next

- **WHEN** the host skips a singer who did not answer and then brings them back while another
  singer is on stage
- **THEN** the current singer keeps singing and the skipped singer is next up

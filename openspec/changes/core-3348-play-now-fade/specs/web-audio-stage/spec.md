## MODIFIED Requirements

### Requirement: Audio warns before playing next

During the final 12 seconds of an audio item that has a next item, the audio stage SHALL show a
Playing next card with the next title and the seconds left, and the next item's picture and artist
when its read has them. A picture that fails to load SHALL be left out. Waiting on the card SHALL
NOT change when or whether playback advances. Seeking out of the final 12 seconds SHALL hide it,
and Hide SHALL dismiss it for the rest of that item. Play now SHALL hide the card at once and move
to the next item, fading the playing item out and the next item in instead of cutting, and SHALL
keep any volume the listener sets during the fade without saving the faded levels.

#### Scenario: Track nears its end

- **WHEN** an audio item with a next item reaches 10 seconds from its end
- **THEN** the Playing next card shows the next title and "10"

#### Scenario: Next item has a picture and an artist

- **WHEN** the card shows and the next item's read has a poster and a person credit "Cee"
- **THEN** the card shows that picture and "Cee", and its region is named "Playing next: C by Cee"

#### Scenario: Play now

- **WHEN** the listener presses Play now on the card while the item plays
- **THEN** the card hides at once, the playing item fades out, the next item plays and fades in,
  and the audio stage and its visual stay mounted

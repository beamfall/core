## ADDED Requirements

### Requirement: Persisted listening speed with a container override

The web audio stage SHALL offer a playback speed from 0.5× to 3.0× with pitch preserved, SHALL
persist it through Profile client prefs as a global value and as an optional override keyed by the
item's container Entity, and SHALL apply the override before the global value.

#### Scenario: Speed for one collection

- **WHEN** the listener sets 1.5× with "Use for this collection" on
- **THEN** every item of that container starts at 1.5× on any device of the Profile, while other
  items use the global speed

#### Scenario: Override cleared

- **WHEN** the listener turns the container toggle off
- **THEN** the override is removed and the item plays at the global speed

### Requirement: Remove long pauses from the silence map

When the trim-silence preference is on and playback-info advertises a silence map, the web audio
stage SHALL skip every silent span of at least 0.8 seconds, SHALL keep reported positions in file
time, and SHALL show the time saved.

#### Scenario: Silent span

- **WHEN** playback reaches the start of a 2-second silent span
- **THEN** playback continues at the span's end and the saved time grows by about 2 seconds

#### Scenario: No silence map

- **WHEN** playback-info carries no silence map
- **THEN** nothing is skipped

### Requirement: Sleep timer with fade

The web audio stage SHALL offer a sleep timer of 15, 30, 45 or 60 minutes, end of item and end of
chapter, SHALL fade the volume out over the last 10 seconds, then pause and restore the volume.

#### Scenario: End of item

- **WHEN** the timer is set to end of item
- **THEN** the volume fades over the item's last 10 seconds and playback pauses before the item
  ends, without advancing to the next item

### Requirement: OS controls use the configured skip interval

The Media Session seekbackward and seekforward actions SHALL step by the configured skip interval
when the OS supplies no offset.

#### Scenario: 30-second interval

- **WHEN** the skip interval is 30 seconds and the OS sends seekforward without an offset
- **THEN** playback moves 30 seconds forward

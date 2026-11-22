## ADDED Requirements

### Requirement: Sonic analysis opt-in

Each Library SHALL have a sonic-analysis toggle, separate from AI indexing, that is OFF unless an
Admin turns it on. Reading and changing it SHALL need an Admin Session and a visible Library.
Turning it on SHALL be refused while the server has no analyser, and SHALL arm the analysis job.
Turning it off SHALL always succeed and SHALL keep stored vectors.

#### Scenario: Default off

- **WHEN** a Library has never been configured
- **THEN** its toggle reads OFF and none of its Media are analysed

#### Scenario: No analyser

- **WHEN** an Admin turns the toggle on and no analyser is wired
- **THEN** the mutation fails and the toggle stays OFF

### Requirement: Idle-only excerpt analysis

The analysis job SHALL consider only audio-class Media in Libraries whose toggle is on and that
lack a vector of the analyser's kind, and SHALL skip Sealed and Encrypted Libraries. It SHALL
decode one excerpt of at most 60 seconds from the middle of the file, keep samples in memory, and
store the analyser's vector by content fingerprint. Nothing SHALL leave the server.

#### Scenario: Excerpt window

- **WHEN** a 200-second file is analysed
- **THEN** the excerpt starts at 70 seconds

#### Scenario: Protected Library

- **WHEN** a candidate is in a Sealed or Encrypted Library
- **THEN** no excerpt is decoded and no vector is stored

### Requirement: Analysis yields and validates

The job SHALL run only while the resource governor admits background work and SHALL yield with a
checkpoint when preempted or when more candidates remain. A decode or analyser failure, or a vector
that is empty, wider than 256 values, non-finite or all zero, SHALL be skipped without failing the
job, and the item SHALL stay a candidate for the next sweep.

#### Scenario: Resume

- **WHEN** more candidates remain than one slice handles
- **THEN** the job yields and the next run continues after the last processed Media

#### Scenario: Bad vector

- **WHEN** the analyser returns a non-finite value
- **THEN** nothing is stored and the item is retried by a later sweep

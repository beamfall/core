## MODIFIED Requirements

### Requirement: Scans yield on time as well as on files

A scan job SHALL yield after two minutes of work even when it has not finished its file batch. The
two minutes SHALL count from the run's first checkpoint, so the walk a resumed run makes back to its
resume point does not spend them.

#### Scenario: Slow files

- **WHEN** a scan's time budget is spent before its file batch is done
- **THEN** the job yields and a later claim resumes where it stopped

#### Scenario: Slow walk back to the resume point

- **WHEN** a resumed scan takes longer than its time budget to walk back to its checkpoint
- **THEN** the run still checkpoints new files for its whole budget before it yields

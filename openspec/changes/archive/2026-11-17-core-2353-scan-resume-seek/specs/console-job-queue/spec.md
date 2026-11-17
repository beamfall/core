## ADDED Requirements

### Requirement: A resumed scan seeks to its checkpoint

A resumed scan SHALL NOT list or stat a folder whose whole subtree sorts before its checkpoint in
walk order, and SHALL visit the same files after the checkpoint as a full walk of the same tree.

#### Scenario: Folders before the checkpoint are not read

- **WHEN** a scan resumes from a checkpoint inside a Library folder
- **THEN** it reads only the checkpoint's ancestor folders and the folders after them, and imports
  the files after the checkpoint in the same order as a full walk

#### Scenario: Files deleted after the checkpoint

- **WHEN** a resumed scan finds no media files after its checkpoint because they were deleted
- **THEN** it still marks them missing rather than treating the root as empty

### Requirement: Folder preflight probes share the worker budget

When a Library's parser reads a folder's files together, the scan SHALL probe that folder's files
within the scan's worker budget, in parallel up to that budget, and SHALL decide the folder as if
they were probed one at a time.

#### Scenario: Several workers

- **WHEN** a scan with several workers reaches a folder of files its parser reads together
- **THEN** it probes up to that many files at once and never more probes than workers in total

## ADDED Requirements

### Requirement: Scan folds verified legacy and sampled copies into Versions

After the scan walk, the scanner SHALL find pairs of live `sample1:` and `sha256:` files
with equal nonempty OSHash and equal size on distinct untrashed Media in the same Library.
The scanner SHALL verify the sampled file's full digest against the legacy fingerprint
before attempting an undoable merge. Encrypted Libraries, Libraries scanned with trash
disabled, Extras, Media in a relation, multipart Media, and Media restored by a version
merge undo SHALL be excluded.

#### Scenario: Verified copies in different directories

- **WHEN** the sampled file's full digest matches the legacy fingerprint in the same Library
- **THEN** the scan keeps the sampled Media and its first Version's probe facts
- **AND** both paths and fingerprints are retained as independently playable Versions
- **AND** attaching a later labelled Version keeps a sampled anchor in the default slot
- **AND** the survivor's play count sums both Media's plays and its last-played time is the later of the two
- **AND** each Profile keeps the survivor's position and watched state if it has a progress row, or takes them from the legacy row otherwise

#### Scenario: Verification does not match

- **WHEN** files share an OSHash and size but the sampled file's full digest differs from the legacy fingerprint
- **THEN** the scan does not fold them as copies
- **AND** the files are distinct content, so the default same-title Version rule applies to them as to any other file

#### Scenario: Verification is interrupted by a file read error

- **WHEN** the scanner cannot read a possible copy's full digest
- **THEN** both Media sit out title grouping in that scan, so a later scan can retry verification
- **AND** title grouping still merges the title's other Media, taking at most one store call fewer than their count

#### Scenario: The catalog commits before a playback write fails

- **WHEN** a verified merge's events transaction does not commit
- **THEN** recovery retries the journaled playback contributions individually by Profile and Media pair
- **AND** an engagement erase cancels contributions for the erased Profile and Media in the same transaction

#### Scenario: Undo restores the split

- **WHEN** a user undoes the version merge
- **THEN** both Media and their file assignments are restored
- **AND** later scans keep the restored Media separate

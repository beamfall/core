# media-trash-merges Specification

## Purpose
Defines how Trash and the activity log explain Media folded by a verified-copy merge, so merge batches read as merges with an actor rather than as unexplained deletions, and how operators find survivors that hold identical copies.

## Requirements

### Requirement: Trash entries say which Media a merge folded them into

The `trashList` query SHALL return, for each trashed Media that an automatic Version merge folded
into another Media and whose merge batch has not been undone, a `mergedInto` object holding the
merge batch id, the surviving Media's id and title, and the time of the merge. A Media trashed by
any other route SHALL have no `mergedInto`.

#### Scenario: A verified-copy merge's folded Media names its survivor

- **WHEN** a scan merges two byte-identical copies, keeping Media "A" and trashing Media "B"
- **THEN** the `trashList` entry for "B" has `mergedInto` with the batch id, survivor id "A" and
  survivor title "A"

#### Scenario: A deleted Media has no merge information

- **WHEN** an operator trashes a Media
- **THEN** its `trashList` entry has no `mergedInto`

#### Scenario: An undone merge no longer explains a Trash row

- **WHEN** a merge batch is undone
- **THEN** none of its Media appear in `trashList` with `mergedInto`

### Requirement: Manage Trash groups merges apart from deletions

Manage › Trash SHALL list merge-folded entries separately from deleted entries, one group per merge
batch. Each group SHALL say the Media was merged into its survivor as a Version automatically by the
scan, link to the survivor, and offer an Undo merge action that reverses the whole batch through
`undoBatch`. Merge-folded entries SHALL NOT offer the per-row Restore that deletions offer.

#### Scenario: Undo merge reverses one batch

- **WHEN** the operator chooses Undo merge on a group
- **THEN** `undoBatch` runs for that batch id and the group leaves the merged list

### Requirement: Purging a merge-folded Media keeps the moved files

Purging trashed Media SHALL NOT remove, reassign or mark missing any file a merge moved onto the
surviving Media. The survivor SHALL keep every Version it gained.

#### Scenario: Purge after a merge

- **WHEN** a merge folds Media "B" into Media "A" and Trash is purged past the merge time
- **THEN** "B" is gone and "A" still has both files as live Versions

### Requirement: Duplicate on-disk copies are reported, never deleted

The server SHALL offer the manager query `duplicateCopies`, reveal-filtered like other Library
reads. It SHALL list each Media holding two or more live part-0 Versions that share a non-empty
OSHash and the same size, with the Media id, title, Library id, and each copy's path, size and
Version label. The server SHALL NOT delete or move any file for this report.

#### Scenario: A survivor with two identical copies is listed

- **WHEN** a merge leaves Media "A" with two Versions of the same content at different paths
- **THEN** `duplicateCopies` lists "A" with both paths

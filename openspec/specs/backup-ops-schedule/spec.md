# backup-ops-schedule Specification

## Purpose
Enforce the ADR-0192 §6 scheduled database backup retention policy, destination prechecks, post-backup integrity verification, and failure classification.

## Requirements

### Requirement: Scheduled backup retention follows the ADR-0192 policy

After a verified scheduled backup, the server SHALL keep the newest 7 sets, the newest set in each
further 7-day age bucket up to 90 days, and at most 12 sets in total. It SHALL NOT prune after a failed backup.

#### Scenario: Old sets pruned

- **WHEN** a verified backup completes and sets older than 90 days exist beyond the newest 7
- **THEN** those older sets are removed

#### Scenario: Failure keeps history

- **WHEN** the destination is full
- **THEN** the Job fails with class destination-full and every prior set is unchanged

### Requirement: Scheduled backup failures are classified

A failed scheduled backup SHALL be logged with one class: destination-full, unwritable,
snapshot-error or integrity-failure.

#### Scenario: New set fails verification

- **WHEN** the new set fails integrity verification
- **THEN** the Job fails with class integrity-failure and no pruning occurs

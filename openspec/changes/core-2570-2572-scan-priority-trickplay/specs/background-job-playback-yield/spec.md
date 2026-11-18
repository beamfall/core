## ADDED Requirements

### Requirement: Library scans take priority over generation sweeps

Library scans SHALL preempt artwork, generated-assets, decode-verification and skip-detection
sweeps, including post-scan Generated Assets catch-up. A running or runnable queued/yielded scan SHALL make sweeps yield with their latest
checkpoint and a bounded retry. Sweeps SHALL NOT start while a scan lease is held and SHALL
resume eligibility after all scans finish. Scan preemption SHALL NOT cancel convert, playback
precompute or ordinary generation work.

#### Scenario: A scan starts during a sweep

- **WHEN** a sweep is running and a library scan starts
- **THEN** the sweep yields with its checkpoint and a bounded retry delay
- **AND** sweep admission resumes after the scan finishes

#### Scenario: A queued scan needs the worker

- **WHEN** a sweep holds a job worker and a queued or yielded scan is available to run
- **THEN** the job-runner ticker preempts the sweep so the scan can claim the worker
- **AND** the server logs the preemption at Info without repeating it every tick for that yield

#### Scenario: Convert and precompute continue

- **WHEN** a library scan starts while convert or playback precompute work is running
- **THEN** that work is not cancelled by the scan

### Requirement: Full Generated Assets sweeps run off-peak

The server SHALL register the full Generated Assets sweep as an enabled daily Schedule at 03:00
using the existing scheduler. Scheduled and manual run-now jobs SHALL use the same versioned
sweep payload and dedupe key. The sweep SHALL generate one Media item at a time and SHALL NOT
be enqueued by scan completion or server startup. Startup SHALL still resume interrupted jobs.
A failed manual enqueue SHALL be logged; the nightly Schedule SHALL re-arm the sweep.

#### Scenario: Nightly sweep

- **WHEN** the enabled Generated Assets Schedule is due at 03:00
- **THEN** the scheduler enqueues the full asset-sweep job with version 1 payload
- **AND** an already queued, running or yielded sweep deduplicates the enqueue

#### Scenario: Startup and scan completion

- **WHEN** the server starts or a library scan completes
- **THEN** no new full asset-sweep job is enqueued
- **AND** artwork and skip-detection enqueues retain their existing behavior

#### Scenario: Manual run now

- **WHEN** an operator requests Generated Assets run now
- **THEN** the server enqueues a full sweep, preserving deduplication with nightly work

### Requirement: New Media receive a resumable post-scan catch-up

Before invoking a scan handler, the server SHALL read the maximum Media rowid. After a successful
scan, it SHALL enqueue a background `asset-catch-up` job only if that maximum advanced. The
payload SHALL contain version 1 and the exclusive `after_rowid` floor and SHALL deduplicate by
job kind. Watermark failures SHALL log a warning and skip catch-up. Generation SHALL NOT block
the scan handler.

#### Scenario: Scan adds Media

- **WHEN** a successful scan increases the maximum Media rowid
- **THEN** it enqueues catch-up with the pre-scan rowid floor and does not generate assets inline
- **AND** catch-up visits only rows above that floor

#### Scenario: Scan adds nothing or fails

- **WHEN** a scan fails or completes without increasing the maximum Media rowid
- **THEN** no catch-up job is enqueued

#### Scenario: Watermark unavailable

- **WHEN** either the pre-scan or post-scan watermark read fails
- **THEN** the failure is logged and no catch-up job is enqueued

### Requirement: Catch-up shares resumable sweep generation

Catch-up SHALL read rows in ascending rowid order in bounded batches, use the full sweep's
per-item generator recipe, and checkpoint the last completed rowid. It SHALL yield to scans
and realtime transcodes through the existing sweep governor. Interrupted items SHALL be
retried without advancing past unfinished work.

#### Scenario: Catch-up resumes after preemption

- **WHEN** a catch-up yields to a scan or live transcode
- **THEN** it preserves its last completed rowid and resumes above that checkpoint
- **AND** an interrupted item remains eligible on the next attempt

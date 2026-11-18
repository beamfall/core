## ADDED Requirements

### Requirement: Library scans take priority over generation sweeps

Library scans SHALL preempt artwork, generated-assets, decode-verification and skip-detection
sweeps. A running or runnable queued/yielded scan SHALL make sweeps yield with their latest
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

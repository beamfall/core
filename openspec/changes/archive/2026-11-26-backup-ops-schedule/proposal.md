## Why

CORE-0778 (BKUP-OPS-CHAIN-1, ADR-0192 §6): the daily database backup Job keeps a flat newest-14 and
reports every failure the same way. It needs the ADR retention policy, failure classes and an
integrity check of each new set.

## What Changes

- Retention: newest 7 sets, plus the newest set per further 7-day bucket to 90 days, hard cap 12.
- The Job prechecks destination free space, verifies the new set, and logs a failure class.

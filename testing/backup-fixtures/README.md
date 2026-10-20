# Backup restore fixture corpus

Each release-candidate directory contains an encrypted v2 Backup Set, deterministic row-count/checksum expectations, and a descriptor. The descriptor's BackupKey is intentionally public synthetic test data and is never valid for a production library.

Generate the candidate fixture before cutting its tag:

```sh
make backup-fixture-generate VERSION=1.2.3 RELEASE_ORDINAL=12
```

The committed corpus retains every release from the prior 12 months and at least the three most recent releases. `seed-schema.json` records the seed/schema synchronization point; CI permits one release of drift and fails on the second.

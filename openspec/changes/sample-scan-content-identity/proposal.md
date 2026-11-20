# Sample scan content identity (CORE-1162)

## Why

Initial scans read every byte of large media files to establish identity, exhausting the scan
job's time budget after only a few files. A bounded fingerprint lets the scan proceed to probing
without waiting for a whole-file read.

## What changes

- Use one sampled identity for new scan, managed import and managed convert rows.
- Keep legacy identities at unchanged paths. For a legacy row with incomplete stats, compare
  the full digest once at its existing path and record the stats on a match.
- Keep managed staging's full SHA-256 for duplicate checks against legacy rows.

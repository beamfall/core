# Sample scan content identity (CORE-1162)

## Why

Initial scans exhaust their time budget reading large files for identity. Bounded fingerprinting
lets the scan proceed to probing sooner.

## What changes

- Use one sampled identity for new scan, managed import and managed convert rows.
- Keep legacy identities at unchanged paths. For a legacy row with incomplete stats, compare
  the full digest once at its existing path and record the stats on a match.
- Keep managed staging's full SHA-256 for duplicate checks against legacy rows.
- Treat either digest as a duplicate before proxy promotion or qBittorrent relocation.
- Compute photo export manifest digests from the exported plaintext.

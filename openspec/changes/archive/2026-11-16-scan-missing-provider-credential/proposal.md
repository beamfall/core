# Report a missing provider credential once per scan

Ticket: CORE-1101 (`queue:core:main`).

## Why

A first scan of a Library whose metadata provider needs a credential that is not configured logs,
for every file, `WARN scan: auto-identify failed ... resolving credential "<ref>": unavailable:
lstat /data/secrets/...: no such file or directory`. The files import but stay unidentified, and
the operator gets a raw filesystem error per file instead of one hint saying what to add.

## What Changes

- `internal/scrape` reports a credential that is absent (no credential source, or the source
  reports it does not exist) as `CredentialMissingError`, which names only the credential ref,
  never the secret or its path. Other resolution failures stay ordinary errors.
- A scan notes a missing credential once: one WARN naming the Library, plugin and credential and
  where to add it. The ref is kept in the scan result and the job checkpoint, so a scan job that
  yields and resumes does not report it again. Other identification errors are still logged per
  file.
- The completed scan job's progress message names the missing credential instead of a bare
  "Scan complete".

## Non-goals

- A console prompt for the key. The graph already exposes `setScraperCredential`; the web UI
  piece is a follow-up for the web lane.

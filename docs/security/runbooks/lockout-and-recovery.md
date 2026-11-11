# Runbook: Lockout and recovery

What to do when an operator or a device is locked out, the admin password is lost, first-run setup
stalled, the database is damaged, or the server runs out of file descriptors.

## Read the refusal first

Every refused credential leaves one `auth refused` record ([logs](logs.md)) and increments
`beamfall_auth_refusals_total{reason}` ([metrics](metrics.md)). Match the `requestID` from the
client's `X-Beamfall-Request-Id` header to the record, then act on its `reason`:

| `reason` | Meaning | Action |
|---|---|---|
| `admin_locked` | Admin re-auth is in backoff | Wait it out (below) |
| `admin_password_rejected` | Wrong admin password | Retry; if lost, reset it (below) |
| `integration_locked` / `integration_rejected` | Integration sign-in is in backoff / was refused | Wait it out; check the integration's configured credential |
| `pairing_throttled` / `pairing_code_invalid` | Enrollment is in backoff / the code is wrong or expired | Wait, then mint a fresh pairing code |
| `setup_token_invalid` | Wrong or already-used first-run setup token | See *First-run setup* |
| `device_secret_invalid`, `device_revoked`, `device_mismatch` | The device credential no longer matches | Re-pair the device |
| `session_expired`, `session_invalid` | The session ended | Sign in again |
| `profile_unavailable`, `outside_schedule` | The Profile is disabled or outside its access schedule | Adjust the Profile in the console |

The record never carries a password, secret, token, key or code, so it is safe to paste into an
incident ticket.

## Lockout curves

Admin re-auth (keyed per session), integration sign-in and pairing enrollment (keyed per code and per
source) share one curve: 5 free failures inside a 10-minute window, then a delay that starts at 2 s
and doubles per further failure, capped at 10 minutes. The delay counts from the last failure, a
success resets the count, and failures older than the window stop counting. Attempts are stored in
the database, so restarting the server does not clear a lockout. There is no unlock command: stop
the failing client, wait out the delay (at most 10 minutes after the last failure) and retry once
with the right credential. A sustained `admin_password_rejected` or `pairing_code_invalid` rate from
one source is a guessing attempt: handle it as an [incident](incident.md).

## Reset the admin password

The admin password is reset from the host filesystem (ADR-0012 §6), never over the network:

1. Stop the server. The command refuses to run while the server holds its data-directory lock.
2. Run, as the service user:

   ```sh
   beamfall reset-admin-password --data-dir /var/lib/beamfall
   ```

   With no `--password` it reads the new password from stdin, which keeps it out of the process list
   and shell history; prefer that over `--password`. `BEAMFALL_DATA_DIR` is honored in place of
   `--data-dir`.
3. Confirm it prints `admin password updated`, start the server and sign in.

## First-run setup

Until first-run setup has completed (an adult Profile and an enrolled Device both exist), every start
generates a new one-time setup token and prints `Beamfall first-run setup: <url>` to stdout. The
structured log carries the same URL with the token as `REDACTED`, so read the token from the console
or supervisor stdout, not from shipped logs. To recover a lost or already-used token
(`setup_token_invalid`), restart the server and use the newly printed URL; the previous token stops
working. Once setup has completed no token is generated again. There is no command that re-arms
first-run on a set-up server: reset the admin password instead, or start over with a fresh data
directory, or restore a backup taken before setup (see [backup-restore](backup-restore.md)).

## Database corruption

Signs: the server refuses to start with a SQLite error, migrations fail their checksum check, or
queries fail with `database disk image is malformed`.

1. Stop the server.
2. Copy the whole data directory aside, unchanged, before trying anything. It is the evidence and
   the fallback.
3. Restore the latest good backup with `beamfall restore` while the server is stopped, following
   [backup-restore](backup-restore.md), including its post-restore verification query.
4. Start the server and confirm `/health` reports `ready` and `beamfall_up 1`. Anything written after
   the backup is lost; record the gap in the incident timeline.

Do not edit the database by hand to get past a migration checksum error: applied migrations are
append-only and their checksums are verified at every start.

## File descriptor limit (`LimitNOFILE`)

Each client connection, open media file, FFmpeg pipe and SQLite handle uses a descriptor. A low limit
shows up as `too many open files` in the logs and a rising `beamfall_http_requests_total{status_class="5xx"}`.
Beamfall does not ship a systemd unit. Under systemd, raise the limit in the unit or a drop-in:

```ini
[Service]
LimitNOFILE=65536
```

Then `systemctl daemon-reload` and restart. Check the running value with
`cat /proc/$(pidof beamfall)/limits`. In a container, set the equivalent `ulimit` (`--ulimit nofile=65536:65536`).

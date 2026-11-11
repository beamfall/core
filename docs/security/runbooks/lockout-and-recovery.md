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

Admin re-auth (keyed per Profile, so a new session does not reset it), integration sign-in and pairing enrollment (keyed per code and per
source) share one curve: 5 free failures inside a 10-minute window, then a delay that starts at 2 s
and doubles per further failure, capped at 10 minutes. The delay counts from the last failure, a
success resets the count, and failures older than the window stop counting. Attempts are stored in
the database, so restarting the server does not clear a lockout. There is no unlock command: stop
the failing client, wait out the delay (at most 10 minutes after the last failure) and retry once
with the right credential. Admin-password sign-in from the enroll page is keyed per source and
once for the whole server, so a guessing flood from anywhere can delay the owner's own sign-in;
use a pairing code from the host (below) while it lasts. The server checks at most two admin passwords at
once and counts each attempt before checking it; a request that waits more than 2 s for a turn is
refused as a wrong password without being counted. A sustained `admin_password_rejected` or `pairing_code_invalid` rate from
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
working. A browser on the server's local network does not need the token: open
`http://<server-ip>:8800/web/` (the server also prints `Beamfall first-run setup from this network (no
token needed): <url>` for each LAN address when bound to all interfaces) and finish setup there.
Requests that reach the server from outside the local network, through the relay, or through a
proxy that is not declared as trusted still need the token, and so does a LAN browser that opens
the server by a DNS name other than `localhost` or a `.local` name: use the server's IP address.
Once setup has completed no token is
generated again and the local-network path closes too. No command re-arms first-run on a set-up
server. To get a browser back in, use *Sign in again after setup* below. To start over, use a
fresh data directory or restore a backup taken before setup (see [backup-restore](backup-restore.md)).

## Sign in again after setup

There are three ways back in after setup. None of them needs a setup token.

1. **Session renewal.** A browser session lasts 30 days. The device cookies last 400 days. When the
   session ends, the web app calls `POST /api/web/session/renew` once with the device cookies and
   gets a new session. This fails if the device was revoked, its Profile is disabled, or the
   cookies are gone. Failed renewals are throttled per source address.
2. **Admin password.** The enroll page has a second form, *Sign in with the admin password*. It
   enrolls the browser as a Manager device on the owner Profile. It works over HTTPS from
   anywhere, and over plain HTTP only from the local network, opened by IP address, `localhost` or
   a `.local` name. Every refusal says only "sign-in failed". Failures are throttled per source
   and across the server on the admin re-auth curve. If the password is lost, reset it first
   (above).
3. **Pairing code from the host.** Stop the server and run, as the service user:

   ```sh
   beamfall pairing-code --data-dir /var/lib/beamfall
   ```

   It prints a one-time code. Start the server, open `http://<server>:8800/web/` and enter the
   code. The new device is a Manager; pass `--role viewer` for a Viewer. `BEAMFALL_DATA_DIR` is
   honored in place of `--data-dir`. Like `reset-admin-password`, it refuses to run while the
   server holds its data-directory lock.

**Strict mode.** At setup the owner picks Home (the default) or Strict sign-in security. Strict turns
off admin-password sign-in, so the enroll page hides that form and the endpoint refuses with the
same "sign-in failed" as a wrong password. Session renewal and pairing codes from a Manager still
work. When no signed-in Manager is left, recovery in Strict mode is `beamfall pairing-code` on the
host (above). A Manager can switch modes in the Console under Access with the admin password; each
switch is audited as `signin_security.mode`.

## Database corruption

Signs: the server refuses to start with a SQLite error, migrations fail their checksum check, or
queries fail with `database disk image is malformed`.

1. Stop the server.
2. Copy the whole data directory aside, unchanged, before trying anything. It is the evidence and
   the fallback.
3. Restore the latest good backup with `beamfall restore` while the server is stopped, following
   [backup-restore](backup-restore.md), including its post-restore verification query.
4. Start the server, confirm `/health` reports `"ready": true`, then make a separate authenticated
   `/metrics` request (see [metrics](metrics.md)) and confirm it reports `beamfall_up 1`. Anything
   written after the backup is lost; record the gap in the incident timeline.

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

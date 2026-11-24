# Install on Linux

**Pre-release:** no release binary, image digest, or deploy bundle is published yet. Build from
source or use a locally built container. See the [install index](README.md) for release status.

## Prerequisites

For a native build, install Git, the Go version in [go.mod](../../../go.mod) (currently 1.27.4),
Node.js and npm, and FFmpeg with ffprobe. Both media tools must be on the server's `PATH`.
Use local disk for server state. For containers, install Docker Engine and Docker Compose;
the image supplies FFmpeg and the build toolchains.

Linux hardware probing supports Intel QSV, VA-API (including compatible AMD hardware), and
NVIDIA NVENC. The startup probe must report a working encoder; driver installation alone is
not evidence that transcoding works. See the [probe implementation](../../../internal/ffmpeg/hwaccel.go).

## Install

From a checkout's repository root, build the web UI before the binary:

```sh
npm --prefix internal/web/app ci
npm --prefix internal/web/app run build
go build -o beamfall ./cmd/beamfall
```

Without the UI build, a clean checkout embeds only a placeholder. See
[Native binary](../../../README.md#native-binary) for the build contract.

Untested: the following native Linux installation and systemd example have not been exercised
on a Linux host. The paths are suggested installation locations. Create a dedicated account
(skip account creation if it already exists):

```sh
sudo useradd --system --user-group --home-dir /var/lib/beamfall --shell /usr/sbin/nologin beamfall
sudo install -m 0755 beamfall /usr/local/bin/beamfall
sudo install -d -o beamfall -g beamfall -m 0700 /var/lib/beamfall
```

Alternatively, from the repository root, build and start the CPU container. Replace `/srv/media`
with your existing media directory:

```sh
docker build -t beamfall-core:local .
export BEAMFALL_IMAGE=beamfall-core:local
export BEAMFALL_MEDIA_DIR=/srv/media
docker compose -f packaging/compose/compose.cpu.yml up -d
```

Untested: the container build and Compose deployment have not been run here. Keep those variables
set for subsequent Compose commands. The host port defaults to loopback; for intentional LAN
access, set `BEAMFALL_HTTP_BIND=0.0.0.0` before starting. Follow the
[Compose reference](../../../packaging/compose/README.md) for other configurations: choose one
base file (`compose.cpu.yml`, `compose.intel-vaapi.yml`, or `compose.nvidia.yml`). Intel needs
`/dev/dri` access and `BEAMFALL_RENDER_GID` set to the host render node's group ID; native users
need membership in that group too. NVIDIA containers need the NVIDIA Container Toolkit and host
driver. Add `compose.public.yml` only as described in the reference for Caddy HTTPS.

Keep the image's default entrypoint so `tini` runs as PID 1 and reaps orphaned encoder
descendants. Running Beamfall itself as PID 1 leaves descendant zombies after an encoder
group is stopped. Beamfall joins the encoder it started. On native macOS, Stop kills the
process group immediately and `launchd` reaps orphaned descendants.

## Run as a service

For the native installation above, create `/etc/beamfall.env`, owned by root with mode `0600`:

```ini
BEAMFALL_DATA_DIR=/var/lib/beamfall
BEAMFALL_ADDR=127.0.0.1:2326
PATH=/usr/local/bin:/usr/bin:/bin
```

Create `/etc/systemd/system/beamfall.service`:

```ini
[Unit]
Description=Beamfall Core
After=network.target

[Service]
User=beamfall
Group=beamfall
WorkingDirectory=/var/lib/beamfall
EnvironmentFile=/etc/beamfall.env
ExecStart=/usr/local/bin/beamfall serve
Restart=on-failure
RestartSec=5
UMask=0077

[Install]
WantedBy=multi-user.target
```

Then enable it and read the first-run log:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now beamfall.service
sudo journalctl -u beamfall.service -n 100
```

This example is covered by the untested notice above. Packaged deb/rpm installation with
a hardened unit is tracked as CORE-1979. For Compose, `restart: unless-stopped` is already in
the base files; arrange for Docker Engine itself to start at boot.

## Data and media paths

The native service uses `/var/lib/beamfall`. The CLI otherwise defaults to `.beamfall` relative
to its working directory; always set an absolute service data directory. Keep SQLite state on
local disk. `-allow-network-data-dir` exists to override the network-filesystem refusal; it is
not a recommended installation setting. Give the service account read/traverse access to media.

Compose stores state in the `beamfall-data` named volume at `/data` and mounts media read-only
at `/media`. The image runs as UID/GID `1000:1000`; bind-mounted writable directories must permit
that user. Library roots are paths as seen by the server, not paths on another machine.

Scratch defaults to `<data-dir>/transcode`. `BEAMFALL_TRANSCODE_TMP` can select an existing,
writable scratch directory. The base Compose files mount `/transcode` but do not set that
variable: set it to `/transcode` in the service environment if using that separate scratch volume.

## First sign-in and pairing

Open `http://127.0.0.1:2326/` on the host. For LAN access, change the service's `BEAMFALL_ADDR`
to `0.0.0.0:2326` and restart it; the equivalent foreground flag is `-addr 0.0.0.0:2326`.
Only expose an unclaimed instance on a trusted LAN. After listening, the server prints setup
URLs as its last startup lines, including a one-time token. A LAN browser using the host's IP
needs no token; while setup remains unfinished, every restart produces a new token. For a
container, read `docker logs beamfall` and use the host's address, not the printed bridge IP.
For public access, use the [deploy procedure](../../security/runbooks/deploy.md).

To sign a browser back in with an owner pairing code, **stop the server first**, then run as
the data-directory owner:

```sh
sudo systemctl stop beamfall.service
sudo -u beamfall /usr/local/bin/beamfall pairing-code -data-dir /var/lib/beamfall -addr 127.0.0.1:2326
sudo systemctl start beamfall.service
```

Use the code before its printed expiry; choose the reachable LAN address for `-addr` if needed.
The default role is `manager`; `-role viewer` is also supported. The command refuses a live
server lock. Password recovery also requires a stopped server:
`sudo -u beamfall /usr/local/bin/beamfall reset-admin-password -data-dir /var/lib/beamfall`.
For containers, stop the service and run the offline command in a temporary container with the
same data volume and user, then restart. See [client pairing](../../api/lan-pairing.md).

## Upgrade

Build the candidate UI and binary, or a new local image tag, and retain the previous build.
Follow [upgrade testing](../../security/runbooks/upgrade-test.md) before stopping the service
and replacing its binary or Compose image. Use the [deploy](../../security/runbooks/deploy.md)
checks after restart. Follow [rollback](../../security/runbooks/rollback.md) and
[offline recovery](../restore.md) rather than starting an older binary against migrated state.
Future releases use the deploy bundle linked in the [index](README.md).

## Back up and restore

Use the Console or `beamfall backup -server URL -token TOKEN -dest DIR` against the running
server, substituting the server URL, API token, and allowed server-side destination. Create a
CLI token while stopped with `beamfall token create -data-dir DIR -name backups`; run it as
the data owner, protect its output, then restart before backing up. See `beamfall token --help`.

Restore while stopped. An encrypted Backup Set (`set.bbk`) needs its Backup Key; a legacy
`manifest.json` takes no key:

```sh
sudo systemctl stop beamfall.service
sudo /usr/local/bin/beamfall restore -data-dir /var/lib/beamfall -manifest /path/to/set.bbk --key 'BACKUP-KEY'
sudo chown -R beamfall:beamfall /var/lib/beamfall
```

Run it as root: restore first renames the data directory to a sibling
`/var/lib/beamfall.pre-restore-*` rollback copy, which needs write access to `/var/lib`. The
`chown` hands the restored directory back to the service account. The
[backup runbook](../../security/runbooks/backup-restore.md) and [offline recovery guide](../restore.md) govern verification, keys, paths, and recovery; for
containers, preserve the data volume and provide offline tooling access to its contents.

## Uninstall

For native installation, run `sudo systemctl disable --now beamfall.service`, then remove the
unit, environment file, and installed binary and run `sudo systemctl daemon-reload`.
For Compose, use `docker compose -f packaging/compose/compose.cpu.yml down` with the same
environment and selected files as installation. Keep the named data volume.
Delete `/var/lib/beamfall` or the data volume only if you intend to lose the library state.
Keep backups and media unless separately choosing to remove them.

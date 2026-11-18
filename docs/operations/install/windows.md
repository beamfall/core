# Install on Windows through WSL2

**Pre-release:** no release binary, image digest, or deploy bundle is published yet. Native
Windows is not supported: the server build fails on Unix-only filesystem calls in
`internal/store`. There is no Windows service mode; the Windows installer/service is tracked
as CORE-1980. Use Docker Desktop with WSL2 or build the Linux binary inside a WSL2 distro.

## Prerequisites

Install a WSL2 Linux distro. For containers, enable Docker Desktop's
[WSL2 backend and distro integration](https://docs.docker.com/desktop/features/wsl/).
For a Linux source build, install Git, Go at the [go.mod](../../../go.mod) version, Node.js, npm,
FFmpeg, and ffprobe inside the distro. Run the commands below in its Linux shell, not PowerShell.

## Install

Untested: these WSL2 and Docker Desktop installation paths have not been exercised on Windows.
Keep the checkout in the Linux filesystem. From its repository root, with an existing media
directory substituted for `/srv/media`:

```sh
docker build -t beamfall-core:local .
export BEAMFALL_IMAGE=beamfall-core:local
export BEAMFALL_MEDIA_DIR=/srv/media
docker compose -f packaging/compose/compose.cpu.yml up -d
```

Keep those variables for later Compose commands. See the
[Compose reference](../../../packaging/compose/README.md) for configuration. Future releases
use its digest-pinned deploy bundle; there is no release image to pull today.

For the alternative without Docker, follow the [Linux source build](linux.md#install) inside
WSL2, then start the built Linux binary from the repository root:

```sh
./beamfall serve -data-dir "$HOME/beamfall-data" -addr 0.0.0.0:8800
```

Untested: Beamfall NVIDIA transcoding through Docker Desktop's WSL2 GPU support is unverified.
[Docker documents NVIDIA GPU-PV support](https://docs.docker.com/desktop/features/gpu/), not a
Beamfall transcode result. Intel `/dev/dri` passthrough is not available through that Docker
Desktop GPU route; do not select the Intel overlay. Start with CPU transcoding.

## Run as a service

Nothing here configures Windows boot startup. For containers, explicitly enable Docker Desktop's
start-on-login setting if desired; the Compose files already use `restart: unless-stopped`.
That policy cannot start Docker Desktop itself and does not restart a deliberately stopped service.

Untested: the [Linux systemd example](linux.md#run-as-a-service) may be used in a WSL2 distro
with systemd enabled. You must separately arrange for that distro to start; enabling a Linux
unit alone does not establish a Windows boot service.

## Data and media paths

Keep native server state inside the distro's Linux filesystem (ext4), for example
`$HOME/beamfall-data`, expanded to an absolute path. Keep Compose state in its `beamfall-data`
named volume. Do not relocate SQLite state to `/mnt/c` or opt into `-allow-network-data-dir`.

Untested: SQLite locking reliability on the Windows-mounted 9p/DrvFs path is a caveat, not a
qualified storage configuration. Windows media under `/mnt/c` can be read, but access is slower.
Core's [watcher](../../../internal/watcher/watcher.go) uses fsnotify (inotify on Linux);
Windows-backed mounts do not supply the Linux file-change events needed for automatic watching
in containers. Use explicit or scheduled scans for those media roots. See
[Docker's filesystem guidance](https://docs.docker.com/desktop/features/wsl/best-practices/).
For volume ownership and scratch, see [container paths](linux.md#data-and-media-paths).

## First sign-in and pairing

Try `http://localhost:8800/` from Windows. Read the Linux process output or
`docker logs beamfall`: setup URLs and the one-time token are the last startup lines once
listening. While setup is unfinished, restarting prints a new token. A browser the server
classifies as on the local network needs no token; WSL2 and Docker Desktop relay requests
through a virtual network, so if setup asks for a token, open the printed token URL.

Untested: WSL2 NAT prevents direct LAN access to the distro without port forwarding or mirrored
networking and appropriate firewall rules. mDNS discovery does not cross that NAT; enter the
server address manually. Follow [Microsoft's networking guidance](https://learn.microsoft.com/en-us/windows/wsl/networking).
For Compose LAN access, also deliberately change `BEAMFALL_HTTP_BIND` from loopback before
starting; a published port does not by itself establish reachability through every VM/firewall.
Claim the server only on a trusted network.

To recover browser sign-in, stop the Linux server, run
`./beamfall pairing-code -data-dir "$HOME/beamfall-data" -addr 0.0.0.0:8800`, then restart before
the code expires. The default role is `manager`; `-role viewer` is available. Password reset
also runs stopped: `./beamfall reset-admin-password -data-dir "$HOME/beamfall-data"`.
For containers, use an offline container with the same data volume and user. See
[client pairing](../../api/lan-pairing.md).

## Upgrade

Rebuild inside Linux, or build a new local image tag and update `BEAMFALL_IMAGE`, retaining the
previous build. Follow [upgrade testing](../../security/runbooks/upgrade-test.md) and
[deploy](../../security/runbooks/deploy.md); stop the running instance before replacement.
Use [rollback](../../security/runbooks/rollback.md) and [offline recovery](../restore.md), not
an in-place binary downgrade against migrated state. Release bundles are described in the
[install index](README.md).

## Back up and restore

Use the Console or the [Linux CLI backup entry points](linux.md#back-up-and-restore) inside
WSL2. API token creation and restore require a stopped server; backup contacts the running
server. Keep paths in the server's Linux/container namespace. The canonical
[backup runbook](../../security/runbooks/backup-restore.md) and [offline recovery guide](../restore.md)
cover verification, keys, and restore paths. Keep verified backups outside the distro as well.

## Uninstall

Stop the Linux process or disable its configured unit. For Compose, run
`docker compose -f packaging/compose/compose.cpu.yml down` with the same environment and files
as installation; retain the data volume. Remove the built binary and any service configuration.
Delete the data directory or volume only if you intend to lose the library state. Removing a
WSL distro or Docker Desktop storage can also remove that state; preserve backups first.

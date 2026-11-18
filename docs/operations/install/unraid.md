# Install on Unraid

**Pre-release:** no release binary, image digest, or deploy bundle is published yet. Beamfall
is not in Community Applications. Build the image locally and add the template manually.
See the [install index](README.md) for the future release route.

## Prerequisites

Enable Unraid's Docker service and choose a local pool for server state and transcode scratch.
Have a source checkout available to the Docker host for the build. The image builds the UI and
Go binary and bundles pinned FFmpeg; the Unraid host does not need Go, Node.js, or FFmpeg.

## Install

Untested: the following deployment has been checked against the packaging files but has not
been installed on an Unraid host. From the repository root on that host:

```sh
docker build -t beamfall-core:local .
```

Copy [beamfall-core.xml](../../../packaging/unraid/beamfall-core.xml) to
`/boot/config/plugins/dockerMan/templates-user/my-Beamfall-Core.xml`. In **Docker → Add Container**,
select the template and change **Repository** to `beamfall-core:local` before **Apply**.
The committed `ghcr.io/beamfall/core@sha256:RELEASE_DIGEST_REQUIRED` value is a placeholder,
not a downloadable image. Set paths and permissions below before applying.

The [Unraid packaging notes](../../../packaging/unraid/README.md) are canonical for template
installation, transferring a checkout, and building versioned local tags. When releases exist,
use the rendered template from `beamfall-deploy-manifests.tar.gz` with its pinned digest.

## Run as a service

Manage the container from Unraid's Docker tab. Enable its autostart setting if you want it to
start with the array; stop it there for offline maintenance. This uses the Docker service,
not a Beamfall-specific service command. Untested: Unraid autostart and reboot behavior have
not been exercised here.

## Data and media paths

Follow the [template's path and storage notes](../../../packaging/unraid/README.md#paths-and-storage):

- `/data`: `/mnt/cache/appdata/beamfall/data`, or the corresponding path on your named pool.
  Prefer the pool path over the FUSE `/mnt/user` view for SQLite. Keep state on local disk.
- `/media`: the default maps `/mnt/user/media` read-only. For multiple shares, add one read-only
  mount per share, preferably at the same path inside and outside the container so library roots
  stay consistent. Do not grant the container write access to source media.
- `/transcode`: `/mnt/cache/appdata/beamfall/transcode`, writable scratch on a fast pool.
  The template sets `BEAMFALL_TRANSCODE_TMP=/transcode`.

The template overrides the image's UID/GID `1000:1000` with `--user 99:100` (`nobody:users`).
Create data and scratch directories owned by `99:100`; grant that identity read/traverse access
to media. Do not enable `-allow-network-data-dir` as a normal installation setting.

Remove the optional `/dev/dri` device entry for CPU-only operation. For Intel QSV/VA-API,
follow the [hardware instructions](../../../packaging/unraid/README.md#hardware-transcoding),
including the render device's actual group ID rather than assuming a fixed one.
Untested: GPU passthrough and transcoding have not been checked on Unraid hardware.
If a reverse proxy fronts the container, set `BEAMFALL_KNOWN_PROXIES` to its address/CIDR as
described in the [deploy runbook](../../security/runbooks/deploy.md).

## First sign-in and pairing

Open `http://<unraid-ip>:8800/web/setup` on a trusted LAN, substituting the host's IP. Read the
container log in Unraid: the setup URLs and one-time setup token are the last startup lines
once listening. A browser on the local network needs no token; every restart while setup is
pending prints a new token. Printed bridge addresses belong to the container, so use the
Unraid host's address instead. The template listens on `0.0.0.0:8800` inside the container.

For an owner pairing code, stop the container and run the offline command with the same data
mount and user (adjust the host path if changed):

```sh
docker run --rm --user 99:100 -v /mnt/cache/appdata/beamfall/data:/data beamfall-core:local \
  pairing-code -data-dir /data -addr 0.0.0.0:8800
```

Restart the normal container, open its Web UI using the Unraid host IP, and enter the code
before its printed expiry. The role defaults to `manager`; `-role viewer` is also available.
Password recovery uses `reset-admin-password -data-dir /data` with the same offline mount/user;
it reads the new password from stdin, so provide an interactive stdin when using Docker.
See [client pairing](../../api/lan-pairing.md).

## Upgrade

Build a new local tag and change **Repository**, keeping the previous tag for rollback.
Follow [upgrade testing](../../security/runbooks/upgrade-test.md) and
[deploy](../../security/runbooks/deploy.md), stopping the old container before replacement.
Keep the same data and media mappings. Use [rollback](../../security/runbooks/rollback.md)
and [offline recovery](../restore.md) if needed; selecting an older image alone does not undo
a database migration. Future releases use the rendered deploy bundle, not a mutable image tag.

## Back up and restore

Use the Console backup action while running. The canonical
[backup runbook](../../security/runbooks/backup-restore.md) and [offline recovery guide](../restore.md)
cover verification, keys, and restore paths. Stop the container before running offline restore
tooling against the host data directory as its owner. Preserve the whole verified backup set
and its recorded paths; do not assume copying a live SQLite directory is a consistent backup.

## Uninstall

Stop and remove the container in Unraid, then remove its saved user template if no longer needed.
Keep the appdata directory and backups. Delete `/mnt/cache/appdata/beamfall/data` (or your chosen
pool path) only if you intend to lose the library state. Scratch may be removed while stopped;
media shares are separate and should remain unless you explicitly choose to delete them.

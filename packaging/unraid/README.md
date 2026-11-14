# Beamfall Core on Unraid

[`beamfall-core.xml`](./beamfall-core.xml) is the Unraid Docker template for Beamfall Core. It runs
the published container image `ghcr.io/beamfall/core`. The committed template fails closed: its
`Repository` is a placeholder until a release renders the digest-pinned copy, and mutable channel
tags are rejected by release policy.

## Install from a release

1. Copy the digest-pinned `beamfall-core.xml` from the release's `beamfall-deploy-manifests.tar.gz`
   bundle to `/boot/config/plugins/dockerMan/templates-user/my-Beamfall-Core.xml`.
2. **Docker → Add Container**, pick the template, set the paths below, then **Apply**.
3. Open `http://<unraid-ip>:8800/web/setup`. The one-time setup token is in the container log
   (`docker logs Beamfall-Core 2>&1 | grep 'first-run setup'`). The tokened URL names the listener
   address; a browser on the same network can instead open one of the token-free LAN URLs the
   server prints beside it. In a bridge-networked container those are container-internal, so use
   the Unraid host's own address with port 8800.

## Install from a local build (before a release digest exists)

Build the image on the Unraid host from a source tree, then point the template at the local tag.
The build context has no `.git`, so pass the version and commit explicitly; an unstamped build
reports version `development`.

```sh
# From a machine with a checkout:
COMMIT=$(git rev-parse origin/main)
SHA=${COMMIT:0:12}
ssh root@<unraid-ip> "mkdir -p /root/bf-build/$SHA"
git archive origin/main | ssh root@<unraid-ip> "tar -x -C /root/bf-build/$SHA"
ssh root@<unraid-ip> docker build -t "beamfall-core:local-$SHA" \
  --build-arg BEAMFALL_VERSION="local-$SHA" \
  --build-arg BEAMFALL_COMMIT="$COMMIT" \
  "/root/bf-build/$SHA"
```

`/root` is RAM on Unraid, so the source tree is gone after a reboot; the image is kept. Then
install the template as above, but set **Repository** to `beamfall-core:local-<sha>` (no registry).
Updating means building a new tag and changing **Repository** again; keep the previous tag for
rollback. The image is not signed or digest-pinned, so treat this as a test deployment.

## Paths and storage

| Setting | Container | Recommended host path |
|---|---|---|
| Data Directory | `/data` | `/mnt/cache/appdata/beamfall/data` |
| Transcode Scratch | `/transcode` (`BEAMFALL_TRANSCODE_TMP`) | `/mnt/cache/appdata/beamfall/transcode` |
| Media | one read-only Path per share | same path as on the host |

- **SQLite on a pool, not `/mnt/user`.** The data directory holds SQLite databases. Use a pool path
  (`/mnt/cache/...`, or your pool's name) rather than the FUSE `/mnt/user/...` view: locking and
  fsync through the user-share layer are slower and less reliable. Create the directory owned by
  `99:100` (`nobody:users`), the user the template runs as.
- **Media read-only, at identical paths.** Library roots are container paths. Mounting each share
  at the same path it has on the host (host `/mnt/user/movies` → container `/mnt/user/movies`,
  mode `ro`) keeps roots identical to the Unraid share paths and to other media servers, so paths
  in logs and the Console match what you see in the Unraid UI. Add one Path per share rather than
  all of `/mnt/user`, and keep every media mount read-only.
- **Transcode scratch** is disposable and write-heavy; keep it on a fast pool and off the array.

## Hardware transcoding

Pass `/dev/dri` (Intel QSV/VAAPI). Running as `99:100`, the container also needs the host's `video`
group to open the render node: add `--group-add 18` (check with `stat -c %g /dev/dri/renderD128`)
to **Extra Parameters**. The startup log reports `hardware acceleration probe: verified working
accelerators=...` when it works.

## Provisioning without the Console

To create libraries before anyone signs in, seed them while the container is stopped (the command
refuses to run while the server holds `server.lock`). Use container paths for roots:

```sh
cat > /mnt/cache/appdata/beamfall/data/libraries.json <<'JSON'
{"version":1,"backgroundWorkers":2,"plugins":[],
 "libraries":[{"title":"Films","plugin":"filmtv","roots":["/mnt/user/movies"]},
              {"title":"TV","plugin":"filmtv","roots":["/mnt/user/tv"]}]}
JSON
docker run --rm --user 99:100 -v /mnt/cache/appdata/beamfall/data:/data <image> \
  apply-config --data-dir /data --file /data/libraries.json
```

`<image>` is the template's **Repository**. Libraries match by title plus plugin; missing ones are
created, missing roots are added, and nothing is removed, so re-running is safe. Libraries seeded
before first-run setup are granted to the admin that setup creates, so first-run setup in the
browser is still required; `BEAMFALL_ADMIN_PASSWORD` only presets the admin password and does not
complete it. See `beamfall apply-config -h` for the full document format.

# Beamfall Core on Unraid

[`beamfall-core.xml`](./beamfall-core.xml) is the Unraid Docker template for Beamfall Core. It runs
the published container image `ghcr.io/beamfall/core`. The committed template fails closed: its
`Repository` is a placeholder until a release renders the digest-pinned copy, and mutable channel
tags are rejected by release policy.

## Install from a release

1. Copy the digest-pinned `beamfall-core.xml` from the release's `beamfall-deploy-manifests.tar.gz`
   bundle to `/boot/config/plugins/dockerMan/templates-user/my-Beamfall-Core.xml`.
2. **Docker → Add Container**, pick the template, set the paths below, then **Apply**.
3. Open `http://<unraid-ip>:2326/web/setup`. The one-time setup token is in the container log
   (`docker logs Beamfall-Core 2>&1 | grep 'first-run setup'`). The tokened URL names the listener
   address; a browser on the same network can instead open one of the token-free LAN URLs the
   server prints beside it. In a bridge-networked container those are container-internal, so use
   the Unraid host's own address with port 2326.

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

To have a test build carry metadata provider keys (so TMDB works before you enter one), keep them
on the server in a `0600` file of `ref=value,...` pairs and add
`--secret id=beamfall_bundled_credentials,src=<file>` to the build. Never commit the file. The
keys are linked into the server binary, so treat that image as private: keep it on the server
and do not push it to a registry.

`/root` is RAM on Unraid, so the source tree is gone after a reboot; the image is kept. Then
install the template as above, but set **Repository** to `beamfall-core:local-<sha>` (no registry).
Updating means building a new tag and changing **Repository** again; keep the previous tag for
rollback. The image is not signed or digest-pinned, so treat this as a test deployment.

## Admitting a local debug client build

A Core admits only the native client builds its release linked in (see
[the first-party build catalog](../../docs/release/first-party-builds.md)), so a debug build from
your own checkout is refused with "Update required" (`unknown-build`). To admit one without
rebuilding the image, list its record in the server's **dev build file**:

1. Set **Dev Builds File** (`BEAMFALL_DEV_BUILDS_FILE`) to `/data/dev-builds.json` and apply the
   template once. Unset, the server admits no dev builds.
2. Print the build's record from the checkout you built it from, then copy it to the server:

   ```sh
   # beamfall-android, after building the debug APK (app-tv or app-mobile):
   python3 script/client_build_identity.py --app tv --dev --manifest dev-builds.json
   scp dev-builds.json root@<unraid-ip>:/mnt/cache/appdata/beamfall/data/dev-builds.json
   ```

   The record lists the `viewer` role. To pair the build with a Manager code, add `"manager"` to
   its `roles`. Apple clients take the build ID and artifact hash from the build's Info.plist
   (`BeamfallClientBuildID`, `BeamfallClientArtifactHash`); write the record by hand in the same
   `beamfall.first-party-builds/v1` form.
3. No restart: the server re-reads the file within a few seconds of a change and logs
   `dev build catalog loaded` with each admitted build ID, surface and hash prefix. Re-pair or
   reopen the client.

Only records whose `buildID` ends in `-dev`, whose `distributions` is exactly `["direct"]` and
whose `roles` are `viewer` and/or `manager` load, and the file may not set `minimumSequence`. A
file that breaks any rule, does not parse or is missing admits no dev builds at all, and the log
says why. A dev record never replaces a build the release linked in. A device admits a dev build the first
time only with the exact artifact hash in the record; after that, rebuilding the same build ID on
that device needs no change to the file. A new device, or a new build ID (a version bump), needs
its record listed once. Removing a record pauses the devices that used it: listing the same build
ID again lets them back in. To shut a device out for good, unpair it. Delete the file, or empty
its `builds`, to stop admitting every dev build.

Each refused handshake logs `compatibility handshake refused` with the device, build ID,
artifact hash and the server-side finding (for example `unknown-build` or
`artifact-hash-mismatch`). Check it first when a client says "Update required".

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

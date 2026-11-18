# Install Beamfall Core

**Pre-release:** no Beamfall release binary, container image digest, or deploy bundle has been
published yet. Today, build from source or build the container image locally. These guides are
for operator installations; the repository's development runner is not an install path.

| Platform | Choose this path |
|---|---|
| [Linux](linux.md) | Native source build with a systemd example, or a locally built image with Compose. |
| [macOS](macos.md) | Native source build for VideoToolbox hardware transcoding; Docker Desktop for CPU-only use. |
| [Windows](windows.md) | Docker Desktop with WSL2, or a Linux source build inside WSL2. Native Windows is not supported yet (CORE-1980). |
| [Unraid](unraid.md) | Locally built image and the manually installed Docker template; no Community Applications listing yet. |
| TrueNAS SCALE | Follow the [custom-app packaging notes](../../../packaging/truenas/README.md), supplying your locally built image as `BEAMFALL_IMAGE`. |

For a container build, run this from the repository root on the Docker host that will run it:

```sh
docker build -t beamfall-core:local .
```

The [Dockerfile](../../../Dockerfile) builds the web UI and server and bundles pinned FFmpeg.
Pass `beamfall-core:local` as `BEAMFALL_IMAGE` to the source Compose templates, or override the
Unraid template's **Repository**. Untested: these container installations have not been run on
the target hosts as part of this guide's verification.

Once releases exist, the supported release route is the digest-pinned
`beamfall-deploy-manifests.tar.gz` bundle described in the
[Compose reference](../../../packaging/compose/README.md). Its release verification and binding
requirements do not turn a local test image into a release.

Use the canonical [deploy](../../security/runbooks/deploy.md),
[upgrade test](../../security/runbooks/upgrade-test.md),
[rollback](../../security/runbooks/rollback.md),
[backup and restore](../../security/runbooks/backup-restore.md), and
[offline recovery](../restore.md) procedures for ongoing operation.

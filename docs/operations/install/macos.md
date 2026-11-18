# Install on macOS

**Pre-release:** no release binary, image digest, or deploy bundle is published yet. Build from
source; native macOS builds work. See the [install index](README.md) for release status.

## Prerequisites

Install Git, the Go version in [go.mod](../../../go.mod) (currently 1.27.4), and Node.js with npm.
FFmpeg and ffprobe must be on the server's `PATH`. With Homebrew, install the
[FFmpeg formula](https://formulae.brew.sh/formula/ffmpeg):

```sh
brew install ffmpeg
```

Core probes Apple's VideoToolbox encoder on macOS; prefer native installation for hardware
transcoding. A working startup probe is required; support in the
[code](../../../internal/ffmpeg/hwaccel.go) does not guarantee every codec on every Mac.

## Install

From the repository root:

```sh
npm --prefix internal/web/app ci
npm --prefix internal/web/app run build
go build -o beamfall ./cmd/beamfall
mkdir -p "$HOME/.local/bin"
cp beamfall "$HOME/.local/bin/beamfall"
```

Build the UI first or a clean checkout embeds only a placeholder. The binary path above is a
suggestion. For a foreground run, using a suggested local data location:

```sh
"$HOME/.local/bin/beamfall" serve -data-dir "$HOME/Library/Application Support/Beamfall"
```

Alternatively, build `docker build -t beamfall-core:local .` from the repository root and follow
the [Linux guide's Compose commands](linux.md#install), using an existing Mac media directory
for `BEAMFALL_MEDIA_DIR`. See the [Compose reference](../../../packaging/compose/README.md).
Untested: this Docker Desktop installation has not been exercised on macOS. Use the CPU overlay:
Docker Desktop does not pass the Mac GPU through to this Linux container, so Beamfall container
transcoding is CPU-only ([Docker GPU support](https://docs.docker.com/desktop/features/gpu/)).

## Run as a service

Untested: this launchd LaunchAgent example has not been loaded on macOS. It runs at your login,
not as a system service before login. Stop any foreground server before loading it.

```sh
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Application Support/Beamfall"
chmod 700 "$HOME/Library/Application Support/Beamfall"
```

Save the following as `~/Library/LaunchAgents/org.beamfall.core.plist`. Replace every
`/Users/YOU` with your actual absolute home directory; launchd does not expand `~` or `$HOME`
inside these values. The `PATH` includes the usual Apple Silicon and Intel Homebrew locations;
adjust it if FFmpeg is elsewhere.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>org.beamfall.core</string>
  <key>ProgramArguments</key><array>
    <string>/Users/YOU/.local/bin/beamfall</string>
    <string>serve</string>
    <string>-data-dir</string><string>/Users/YOU/Library/Application Support/Beamfall</string>
    <string>-addr</string><string>127.0.0.1:8800</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>Umask</key><integer>63</integer>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>/Users/YOU/Library/Application Support/Beamfall/stdout.log</string>
  <key>StandardErrorPath</key><string>/Users/YOU/Library/Application Support/Beamfall/stderr.log</string>
</dict></plist>
```

Load it and read the startup output:

```sh
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/org.beamfall.core.plist"
tail -n 100 "$HOME/Library/Application Support/Beamfall/stdout.log"
```

For offline maintenance, unload it with
`launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/org.beamfall.core.plist"`, then use
the bootstrap command to start it again. For containers, configure Docker Desktop to start on
login if desired; Compose's `unless-stopped` policy requires Docker to be running.

## Data and media paths

Suggested state location: `~/Library/Application Support/Beamfall`, expanded to an absolute
path in service configuration. Otherwise, `serve` defaults to `.beamfall` relative to its
working directory. Keep it on local disk; do not enable `-allow-network-data-dir` as a normal
install setting. Grant the login user read access to media and any macOS privacy permissions
needed for the selected folders or volumes.

Scratch defaults to the data directory's `transcode` subdirectory; `BEAMFALL_TRANSCODE_TMP`
can select an existing writable directory. With Compose, use the `beamfall-data` volume and
read-only media mounts; see [container paths and permissions](linux.md#data-and-media-paths).

## First sign-in and pairing

Open `http://127.0.0.1:8800/`. For LAN access, change `-addr` to `0.0.0.0:8800` in the foreground
command or plist and restart. Use a trusted LAN for first claim. Setup URLs, including the
one-time token, are the last startup lines once listening; a LAN browser using the Mac's IP
needs no token. Each restart while setup is pending prints a new token. For Docker, read
`docker logs beamfall` and use the host's published port rather than the container IP.

For an owner pairing code, unload the LaunchAgent or stop the foreground server, then run:

```sh
"$HOME/.local/bin/beamfall" pairing-code -data-dir "$HOME/Library/Application Support/Beamfall" -addr 127.0.0.1:8800
```

Restart before entering the code; it expires at the printed time. `-role viewer` is optional
(the default is `manager`). To reset the password, stop the server and run
`"$HOME/.local/bin/beamfall" reset-admin-password -data-dir "$HOME/Library/Application Support/Beamfall"`.
See [client pairing](../../api/lan-pairing.md).

Untested: mDNS discovery from inside Docker Desktop's VM is not expected to reach LAN clients;
use the Mac's address explicitly. Native discovery still depends on the local network.

## Upgrade

Rebuild the UI and binary and retain the previous build. Follow
[upgrade testing](../../security/runbooks/upgrade-test.md), unload the agent, replace the binary,
then reload and perform the [deploy checks](../../security/runbooks/deploy.md). Containers use a
new local tag now and a release deploy bundle once available. Follow
[rollback](../../security/runbooks/rollback.md) and [offline recovery](../restore.md) for a
failed upgrade; do not run an older binary directly against migrated state.

## Back up and restore

Use the Console or `beamfall backup -server URL -token TOKEN -dest DIR` against the running
server. Here `beamfall` means the installed binary, and `DIR` is an allowed server-side
destination. Mint a token while stopped with `beamfall token create -data-dir DIR -name backups`
(using the actual data directory), protect its output, and restart before backing up.
See `beamfall token --help`.

Unload the agent before `beamfall restore -data-dir DIR -manifest PATH`. Follow the canonical
[backup runbook](../../security/runbooks/backup-restore.md) and [offline recovery](../restore.md)
for verification, keys, and restore paths before reloading it.

## Uninstall

Unload the LaunchAgent with the bootout command above, then remove its plist and
`~/.local/bin/beamfall`. For Compose, use `docker compose -f packaging/compose/compose.cpu.yml down`
with the same image/media environment and files as installation; retain its data volume.
Remove the suggested data directory or the container data volume only if you intend to lose
the library state. Keep media and backups unless separately choosing to delete them.

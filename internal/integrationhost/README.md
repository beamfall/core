# integrationhost — local inference confinement

The v1 local inference profile (ADR-0127 §1) is CPU-only, has no network, and reaches exactly three
declared roots: read-only staged input, a read-only model-artifact root, and a read-write work root.
`BuildLocalInferencePolicy` is the single inventory both the parent launcher (`LocalInferenceRuntime`)
and the hardening shim (`cmd/beamfall`) compute their grants from:

- every declared root is symlink-resolved, and refused when it resolves into a denied host tree
  (`/etc`, `/proc`, `/sys`, `/dev`, `/boot`, `/root`, `/var/run`, or the filesystem root), when one
  root nests inside another, or when the exec target is not an executable regular file outside the
  writable work root;
- the only grants outside the three roots are the exec inode itself and the host loader directories.
  Host configuration, TLS, font, and device trees are absent — `LocalInferencePolicy.Reach` is the
  policy's own account of that blast radius;
- enforcement fails closed. darwin confines through `sandbox-exec`, pinned to the absolute
  `LocalInferenceSandboxExecPath` rather than resolved through `$PATH` — otherwise a `$PATH` entry
  ahead of `/usr/bin` would decide what confines the child; linux re-execs the shim, which
  refuses when the kernel reports no landlock support, refuses when the kernel's landlock ABI is
  below `LocalInferenceMinLandlockABI` (ABI 1–2 carry no `LANDLOCK_ACCESS_FS_TRUNCATE` bit, so the
  read-only roots would stay emptiable through `truncate(2)`), and refuses when it has no seccomp
  architecture token for the build target; every other platform is refused outright.
  `LocalInferenceRuntime.Run` supervises the child in its own process group and retires that group
  on every exit path, so a descendant that outlives the direct child is never left behind.

On darwin the generated SBPL profile denies the configuration and credential trees explicitly,
after the `(import "system.sb")` line and before the declared-root grants. `(deny default)` alone
is not sufficient: `system.sb` is load-bearing (without it the child cannot exec at all) but it
re-grants host reads on its own account, and `/etc/passwd` was observed readable through it while
`Reach` reported `ReachNone` for the same path. `/dev` is deliberately left out of that deny set —
the only device nodes `system.sb` leaves reachable are `/dev/null` and `/dev/urandom` (block
devices are already refused), and denying the tree also revokes the child's own stdout when the
caller redirects it there.

The `applyConfinement` path used by the ffmpeg and worker shims deliberately keeps its own
best-effort posture (ADR-0012 §10); local inference does not inherit it.

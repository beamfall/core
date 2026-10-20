# Filesystem / mount capability probe

A per-Library-root probe that **detects and records** what a filesystem/mount can actually do, so every
managed file operation consults evidence instead of assuming POSIX. This is the design for **GCR-009**
(P0, core-parity, NAS/SMB/file-operation capability probe). ADR-0064 already declares path safety a
**release gate**; this spec is how the gate knows what is safe.

## Design principles

- **Probe, don't assume (rule).** SMB/NFS/FUSE do **not** behave like local ext4/APFS. Every capability
  below is established by an actual probe operation on the root, not inferred from the mount name alone.
- **Fingerprint is identity, not inode (ADR-0009/0123).** Media File identity is `(content fingerprint,
  home node)`. Inode/file-id and mtime are *accelerators*; when unstable, fall back to fingerprint.
- **Network Library roots are an anti-pattern (ADR-0123).** Remote/network-mounted Library roots are
  **rejected** by the Origin-Node design (they break same-filesystem rename and node-local fingerprint).
  A probe that finds one **warns loudly**; organize/relocate is meant to be a same-filesystem rename
  local to the node.
- **Degrade safely, never corrupt (rule).** An unsupported capability downgrades to a copy→verify→delete
  or scan-fallback path that **cannot lose or duplicate data**; it never silently proceeds as if the
  capability existed.
- **Fail closed on the unknown (ADR-0032/0064).** Unknown fstype, unavailable mount, or permission-denied
  → the operation is blocked with a Console-visible diagnostic, not attempted hopefully.

## Probe lifecycle

1. **When:** on Library-root registration, on mount-state change, and re-validated before any destructive
   batch (mounts disappear — ADR-0064 "unavailable mounts").
2. **What is stored (per root):** fstype, mount id + `st_dev`, network-mount flag, each capability result
   (supported / unsupported / unknown), probe timestamp, probe-version, and the failure reason on any
   negative probe. Same evidence shape as the hardware-probe matrix (ADR-0075 style): honest, timestamped,
   reason-carrying.
3. **Invalidation:** a root whose mount is unavailable at operation time invalidates its cached result and
   **fails closed** until re-probed.
4. **Scratch discipline:** probes create/rename/unlink files only in a dedicated `.beamfall-probe/`
   scratch dir under the root, cleaned on completion — never against user media.

## Capability matrix

| Capability | Probe method | Required before which operation | Safe behavior if unsupported | Operator diagnostic | Source ADRs | Linked GCR |
|---|---|---|---|---|---|---|
| Filesystem type | `statfs`/`getmntinfo` f_type/fstypename (Win: volume info); record ext4/xfs/apfs/ntfs/**cifs/nfs/fuse** | all ops (baseline) | unknown fstype → treat as **least-capable** (no hardlink/atomic-rename assumed); copy-verify mode | "Root `<p>`: filesystem `<t>` (`<known/unknown>`). Running in `<full/conservative>` mode." | 0009, 0064, 0123 | GCR-009 |
| Mount boundary | walk to mountpoint; compare `st_dev` of root vs target; read mount table | move, organize, qBittorrent ingest | crossing a boundary → treat as **cross-device** (copy+delete, not rename) | "Target crosses a mount boundary; using copy+verify+delete, not rename." | 0064, 0123 | GCR-009 |
| Network mount indicator | fstype ∈ {cifs/smb, nfs, afp, webdav, fuse.sshfs} or remote mount option | organize, move, hardlink, watcher, tag write-back | **disable atomic-rename + hardlink assumptions**; copy-verify; watcher→scheduled scan; **warn (0123 anti-pattern)** | "Root `<p>` is a network mount (`<smb>`). Renames/hardlinks not assumed; scheduled scan used. Local roots recommended." | 0123, 0020, 0032 | GCR-009 |
| Hardlink support | **create a real hardlink** in the target dir (`link()` probe→probe2, stat same inode + nlink=2, unlink) | qBittorrent hardlink ingest | **copy ingest (2× space, disclosed)** or refuse hardlink mode; **never silently break seeding** | "Hardlinks unsupported on `<root>`; ingest will copy (uses extra space) or you can move (stops seeding)." | 0020, 0009 | GCR-009 |
| Atomic rename | `rename()` a scratch temp within the same dir + verify; on network fs mark **not-assumed-atomic** | organize, rename, tag write-back, batch rename | write-to-temp → fsync → verify → best-effort rename **with rollback**; if unsafe, copy-verify-swap under lock | "Atomic rename not guaranteed on `<root>`; using verified temp-swap." | 0064, 0173 | GCR-009 |
| Case-only rename | create `probeX`, rename→`probex`; collision ⇒ case-insensitive, success ⇒ case-sensitive | rename, organize, batch rename | case-insensitive → **two-step rename** (X→tmp→x) to dodge source==dest collision; warn | "Case-insensitive filesystem; case-only renames use a two-step to avoid collision." | 0064, 0173 | GCR-009 |
| Copy+delete risk | derived: cross-device ∨ non-atomic ∨ network | move, organize | **copy → verify (size + fingerprint) → delete source only after verify**; failure leaves source intact | "Move on `<root>` is copy+delete (non-atomic); source kept until the copy is verified." | 0064, 0009 | GCR-009, GCR-024 |
| Stable inode / file-id | stat a scratch file twice (and across a remount if observable); compare inode/file-id | scan move/missing detection | unstable → **use content fingerprint, never inode**, for identity + move detection | "Inode identity unstable on `<root>`; move/missing detection uses content fingerprint." | 0009, 0123, 0032 | GCR-009 |
| Permission model | stat dir mode/owner; **create+delete a scratch file** (write probe); detect POSIX vs ACL/SMB-mapped | any write op | permission-denied → **fail closed**, disable writes on that root, report required permission | "Write denied on `<root>` (`<uid/ACL>`); managed operations disabled here." | 0064 | GCR-009 |
| Path normalization | write a file with combining Unicode + mixed case; read the name back; detect NFC/NFD + case-fold | rename, organize, collision detection | normalizing fs → **normalize before collision checks**; store raw + normalized; never assume byte-equality | "Root normalizes Unicode (`<NFD>`); collision checks are normalization-aware." | 0064 | GCR-009 |
| Symlink behavior | create a scratch symlink; `lstat`/`stat`; detect support + follow (Win needs privilege) | organize, move, folder browse | unsupported/policy-denied → **refuse to follow; block symlink escapes** (security fail-closed, ADR-0064) | "Symlinks `<unsupported/blocked>` on `<root>`; escapes refused." | 0064 | GCR-009 |
| Cross-device rename | attempt `rename()` across two roots/mounts → expect/detect `EXDEV` | move, organize, qBittorrent ingest | EXDEV → **copy+delete path** (see Copy+delete risk); never assume rename crosses devices | "Cross-device move (`EXDEV`); using copy+verify+delete." | 0064, 0123 | GCR-009 |
| Watcher support | register fsnotify watch, touch a scratch file, await the event within a timeout | Watcher activation | no event (typical on SMB/NFS) → **scheduled scan fallback** (Watcher is accelerator, never correctness) | "Live watch unavailable on `<root>`; using scheduled scans (correctness unaffected)." | 0032, 0020 | GCR-009 |
| mtime / size stability | stat a scratch file, re-stat after delay/remount; detect mtime granularity (FAT 2s) + at-rest stability | scan move/missing detection, incremental scan | coarse/unstable → **fingerprint-based change detection**, wider re-scan window | "Coarse/unstable mtime on `<root>`; incremental scan falls back to fingerprint checks." | 0009, 0032 | GCR-009 |
| Trash location compatibility | can the Trash staging dir sit on the **same `st_dev`** as the root (so delete-to-Trash is a same-fs rename)? | delete-to-Trash, Trash restore, destructive ops | different fs → **copy-to-Trash + verify + delete**, or per-root same-fs Trash; if impossible, **require explicit confirm — never lose recoverability** (invariant 6) | "Trash for `<root>` is cross-device; delete-to-Trash copies then removes (slower). Recoverability preserved." | 0064, 0078, 0032 | GCR-009, GCR-024 |
| Same-filesystem check | `st_dev` equality of source, destination, and Trash | move, organize, Trash, hardlink ingest, dedupe-merge | different fs → cross-device path; **hardlink + atomic rename refused** | "Source and destination are on different filesystems; rename/hardlink unavailable." | 0123, 0009, 0064 | GCR-009 |
| Origin Node / home-node | run the whole probe **per node, per Library root**; results are node-local; flag any network-mounted root | any node-local file op | network Library root → **warn it breaks same-fs rename + node-local fingerprint (0123 rejected config)** | "Node `<n>` root `<p>` is network-mounted — unsupported for organize/relocate. Use a local root." | 0123, 0009 | GCR-009, GCR-026 |

## Operation → required capabilities

Every launch file operation names the capabilities it must consult **first**. If any required capability
is `unknown`/`unsupported`, the operation takes its safe-degrade path (above) or is blocked.

| Operation | Must consult | If a required capability is unsupported |
|---|---|---|
| **organize** | mount-boundary, same-fs, atomic-rename, case-only, cross-device, path-normalization, symlink, permission | copy+verify+delete; case-safe two-step; block on symlink escape / permission-denied |
| **manual rename** | atomic-rename, case-only, path-normalization, permission | verified temp-swap; two-step case rename; block if write-denied |
| **move files/folders** | same-fs, cross-device, copy+delete-risk, atomic-rename, mount-boundary, permission | copy→verify→delete, source kept until verified |
| **qBittorrent hardlink/copy ingest** | hardlink-support, same-fs, cross-device, network-mount, permission | copy ingest (disclosed) or refuse hardlink; never silently break seeding |
| **batch rename** | atomic-rename, case-only, path-normalization, permission (+ job handoff for large batches) | per-item verified temp-swap; **partial-failure report**, continue others |
| **Trash restore** | trash-location-compat, same-fs, path-normalization, permission | name-collision-aware restore; cross-device copy-back; block if write-denied |
| **destructive file operations** | trash-location-compat, permission, same-fs (fail-closed) | route to Trash; if Trash degraded, **explicit confirm**, never silent permanent delete |
| **file-tag write-back** | atomic-rename, permission, mtime-stability, network-mount | verified temp-swap write; on coarse mtime avoid re-scan storm; skip on network fs if unsafe |
| **Watcher activation** | watcher-support, network-mount, mtime-stability | **scheduled scan fallback** (ADR-0032) — correctness preserved |
| **scan move/missing detection** | stable-inode, mtime/size-stability, **content-fingerprint fallback**, same-fs | fingerprint-based move/missing (ADR-0009), never inode-only |

## Rules honored

- **SMB ≠ local POSIX** — the network-mount indicator disables atomic-rename/hardlink assumptions and
  every capability is separately probed on the mount, not inferred.
- **Rename not atomic across mounts** — mount-boundary + cross-device + atomic-rename probes gate every
  move/organize onto the copy→verify→delete path.
- **Hardlinks may not survive network shares** — hardlink support is established by an actual `link()`
  probe; failure → copy ingest, never a silently-broken hardlink.
- **Degrade safely, never corrupt/duplicate** — copy paths verify (size + fingerprint) before deleting
  the source; Trash never loses recoverability; scan falls back to fingerprint.

## GCR updates needed
*(Recommendations — GCR is human-authored; not applied here.)*
- **GCR-009 (P0, NAS/SMB capability probe):** adopt **this spec** as its deliverable; record that the
  probe + per-root capability store + operation-gating are **REQUIRED_MISSING** (no probe exists today).
  ADR-0064 already makes path safety a release gate — this makes the gate evidence-based.
- **Any unprobed launch file operation is a GCR-009 gap.** The ten operations above each name their
  required capabilities; any operation shipping **without** consulting them at launch must be added to
  GCR-009 (rule: unprobed launch file op → GCR row). Today, **all ten are unprobed.**
- **GCR-024 (destructive recoverability):** cross-link — Trash-location-compatibility + copy+delete-risk
  are shared with the trash-invariant proof; delete-to-Trash must stay recoverable even on cross-device
  Trash.
- **GCR-026 (Origin Nodes wire contract):** cross-link — the home-node probe scope + the
  network-Library-root rejection (ADR-0123) belong to the node↔main contract; a network-mounted root is
  an unsupported config, not a silent degrade.

---
**Method:** file plane + fingerprint identity + missing detection from ADR-0009; managed-op safety
(mount boundaries, case-only, symlink, unavailable mounts, path normalization, Trash) from `0064`;
same-filesystem-rename + network-root rejection + home-node scoping from `0123`; Watcher-as-accelerator
+ fail-closed scheduler from `0032`; qBittorrent hardlink/copy ingest from `0020`; rename engine +
file-tag write-back from `0173`. Probe evidence shape mirrors the ADR-0075 hardware-probe discipline.

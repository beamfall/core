# Path configuration


**Path Picker** *(ADR-0116, config surface)*:
The server-driven UI for selecting a server-side filesystem path when configuring Libraries,
watch/incoming folders, download dirs, and backup destinations. The server enumerates and lists;
clients render. A bounded browser over **Candidate Roots** with content previews and a typed escape
hatch — never a full `/` explorer.
_Avoid_: "file browser"/"directory browser" (implies a full-host explorer), client-side path logic.

**Candidate Root / Configured Root** *(ADR-0116)*:
A **Candidate Root** is a starting point the **Path Picker** offers — auto-detected (mount points,
container volume mounts, common media dirs, the data dir) or operator-configured. A **Configured
Root** is one in the GitOps-exportable `allowed_roots` allowlist, the authoritative selection
boundary: nothing outside it persists, "Add as Root" is the audited, restart-free act that widens
it, and a system-path blocklist (`/etc`, `/proc`, …) is rejected regardless of role.
_Avoid_: treating auto-detection as the security boundary (it is ergonomic only).

**PathRole** *(ADR-0116, config/UI-layer term — NOT a domain entity; never in Core schema, invariant 1)*:
The closed vocabulary parameterizing the **Path Picker**'s per-entry eligibility: `libraryRoot`,
`incomingFolder` (must lie inside a Library root), `downloadDestination`, `backupDestination`.
Eligibility is server-computed via a PickerEligibility adapter over `normalizeRoots`/`ValidateRouting`;
clients render `selectable` + a closed `reasonCode`, never compute rules. Distinct from the domain
terms **Folder** and **Incoming Folder** — PathRole is configuration intent, not a library-tree concept.
_Avoid_: "siteKind", one shared "destination" role, folding PathRole into the domain model.

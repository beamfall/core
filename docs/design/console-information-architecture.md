# Beamfall Console — Information Architecture

Status: accepted v1.0
Date: 2026-06-20

This document is the binding information-architecture (IA) contract for the **Beamfall Console**
— the server-shaped administration area of the Core Web UI (ADR-0060, ADR-0061). It does not add
routes, capabilities, or mutations; it defines how the surfaces those ADRs already settle are
**grouped, prioritised, and progressively disclosed** so the Console reads as a navigable system
rather than one undifferentiated admin wall.

It resolves an expert-panel finding: the
~30 ADR-0060/0061 child surfaces each pass their per-ADR parity test, but no surface owned the
Console's cognitive-load model, progressive disclosure, or primary-vs-advanced split.

Governed mockup (design-change gate, AGENTS.md; the `NEXTGEN-DESIGN-2026-07-12` remediation is
explicitly operator-approved and may drive downstream pixels subject to normal implementation gates):
`../../../beamfall-design-system/mockups/web-app/console-ia.html`
(registered in the design-system mockup catalog).

Related documents:

- ADR-0060 — product/architecture boundary for the
  Core Web UI. **This spec owns the capability map, route grouping, child-surface placement, and
  binding authorization matrix.**
- ADR-0061 — the Console↔Manage surface split (§2)
  and the launch-blocking parity checklist (§3). This spec reconciles the per-area groupings to it.
- ADR-0101 system state families /
  [state-families spec](state-families.md) — the empty/loading/error/no-access board the Overview
  triage queue and every destination consume.
- [Onboarding funnel](onboarding-funnel.md) — owns the first-run spine and the post-setup checklist;
  this spec only places the checklist's resting home on the Console Overview.
- [Design system](../design-system.md) — tokens, register (Console = workbench-dark), components.
- `core-web-ui-*` plans — reconciled in §7.

---

## 1. Principles

The Console IA rests on three moves. They are binding; a Console surface that violates one is a
defect against this spec.

1. **Top-level grouping.** The Console left rail is a small set of named, intent-based groups, never
   a flat list of every route. Groups are ordered by operator frequency, primary → advanced, top to
   bottom. The vocabulary is fixed (§3) — destinations move between groups only by amending this
   spec.

2. **Primary-vs-advanced split.** Daily observe-and-respond work (the **Monitor** group) rides at
   the top and is always expanded. Rare, destructive, or recovery work (the **Maintain** group) is a
   collapsed advanced group. The split repeats *inside* each destination: primary controls are
   visible; advanced/rarely-touched controls live behind an in-panel `Advanced` disclosure.

3. **Progressive disclosure.** **Overview** is the keystone and the default landing. It shows
   server status plus a **Needs-attention** triage queue whose rows deep-link into the owning group.
   The operator only travels deeper when a surface asks for them; nothing in the deep tree demands a
   routine visit. Empty/loading/error/no-access in the queue and every destination follow the
   [state-families](state-families.md) board (no-access stays byte-indistinguishable from empty).

These are IA rules, not visual ones. Density, chrome, and component choices remain the design
system's; route strings, capability grouping, and authority are owned below as the binding design
contract for ADR-0060's Core Web UI decision.

---

## 2. The Console within the Core Web UI

The top bar switches the three Core Web UI areas — **Viewer · Manage · Console** — plus the
Admin-Session chrome (ADR-0085). Ownership is by object shape, per ADR-0061 §2:

- **Console** (`/web/console/*`) — server/admin/system management. This spec's scope.
- **Manage** (`/web/manage/*`) — content/library curation (Tags, Markers, Collections, Unmatched/
  Fix-match, Field locks, Media/Entities, Folders, Bulk, content-Trash). IA owned by the Manage
  register; placed here only so every child ADR has a home (§6).
- **Viewer** (`/web/`, `/web/search`, …) — browse/search/playback/My-Data (ADR-0082/0083/0084).

Cross-boundary workflows follow ADR-0061 §2: system-shaped configuration starts in Console;
content-shaped correction starts in Manage; preview/playback deep-links into Viewer. The
**Admin Session** (ADR-0085) is Console chrome — it gates the whole area and is shown, with its
expiry, in the top bar; MANAGER Device Role never substitutes for it (§8).

---

## 3. The Console groups (canonical rail)

Five groups, ordered primary → advanced. Group names are the fixed vocabulary; the right column is
the canonical destination label and its ADR-0060 route family. Counts/badges are illustrative.

### Overview — `/web/console`
The keystone landing (§4). Not a group; always first.

### Monitor — daily, observe-and-respond (always expanded)
| Destination | Route (ADR-0060) | Owning child ADR(s) |
|---|---|---|
| Operations | `/web/console/operations` | 0076 (jobs/governor), 0075 (HW transcode probe) |
| Live Sessions | `/web/console/operations` (sessions view) | 0074 |
| Library Health | `/web/console/health` | 0042 via 0061; remediation links to Manage |
| Activity & Logs | `/web/console/activity` | 0073 |

### Library — set up & shape content (occasional configuration)
| Destination | Route (ADR-0060) | Owning child ADR(s) |
|---|---|---|
| Libraries | `/web/console/libraries` | 0064 (folder browser/file ops), scan/Watcher/schedules |
| Plugins | `/web/console/plugins` | 0070 |
| Identification | `/web/console/identification` | 0069 (scraper credentials/endpoints) |
| Layout & Navigation | `/web/console/layouts` | 0071 |

### Access — who connects (occasional)
| Destination | Route (ADR-0060) | Owning child ADR(s) |
|---|---|---|
| Profiles & Devices | `/web/console/access` | 0086 (three lanes), 0081 (pairing/enrollment) |
| Schedules & Policy | `/web/console/access` (policy view) | 0087 |
| Remote Access | `/web/console/remote` | ADR-0012/0018/0025 via 0060 |
| API Keys | `/web/console/api` | ADR-0013/0040 via 0060 |

### Maintain — rare / recovery (collapsed advanced group)
| Destination | Route (ADR-0060) | Owning child ADR(s) |
|---|---|---|
| Backup & Export | `/web/console/backups` | 0077 |
| Trash & Retention | `/web/console/trash` (+ retention policy) | 0078, ADR-0051 retention (post-parity) |
| Migration | `/web/console/migrate` | 0079 migration warnings; ADR-0037 wizard |
| Updates | `/web/console/operations` (update awareness) | 0079 |
| Server Config | `/web/console/config` | ADR-0013 config; resource-governor base; server-health (WEB-5K) |

**Notes.**
- Where a destination is a *view* of an existing route (Live Sessions under `operations`, Schedules &
  Policy under `access`), the route string stays as ADR-0060 defines it; the grouping is presentation.
  Any new sub-route is implementation detail, settled when the destination is built — this spec fixes
  the **placement**, not new path strings.
- `Updates` appears under Maintain but its surface is the ADR-0079 update-awareness view on
  `operations`; it is grouped by *frequency* (rare) not by route.
- The collapsed Maintain group is the visual primary-vs-advanced split. First-run setup
  (`/web/setup`, ADR-0080) is a pre-Console wizard, not a rail destination; its residual checklist
  lives on the Overview (§4) and is owned by the [onboarding funnel](onboarding-funnel.md).

---

## 4. The Overview keystone (progressive-disclosure entry)

`/web/console` is the default Console landing and the progressive-disclosure entry point. It carries
exactly three regions, in order:

1. **Status tiles** — Server health, Libraries, Jobs, Sessions. Glanceable; each tile's state uses
   the [state-families](state-families.md) treatment. Tiles are read-only summaries, not controls.

2. **Needs-attention queue** — the triage list. Each row is a real, actionable condition (failed
   matches, retrying jobs, available update, expiring credential, paused backup, health warnings)
   and **deep-links into the owning group's destination**. Severity uses the state-warning/danger/
   restricted roles. When nothing needs attention the queue shows the Empty family (calm, not an
   error). Restricted conditions obey Reveal: a no-access row stays indistinguishable from absence
   (ADR-0101). This region is what lets the deep tree stay quiet — operators are pulled to a
   destination by a queue row, not expected to patrol the rail.

3. **First-run checklist** — the resting home of the onboarding spine's post-setup checklist (owned
   by [onboarding-funnel.md](onboarding-funnel.md)); deep-links to Plugins, Profiles & Devices, etc.
   It clears off the Overview once the spine completes (first successful browse).

The Overview adds no mutations of its own; every action is a deep-link into a destination that owns
the audited server mutation.

---

## 5. In-panel disclosure rule

Every Console destination repeats the primary-vs-advanced split internally:

- **Primary controls** — the common-case configuration for that surface — are visible on entry.
- **Advanced controls** — rarely-touched tuning, probes, raw config, and destructive/recovery
  actions — live behind an in-panel `Advanced` disclosure (collapsed by default), or a clearly
  separated advanced section. Examples: resource-governor CPU budget and HW-transcode probe under a
  Library's scan settings; raw config keys under Server Config; permanent-purge under Trash.
- Destructive confirmations still state blast radius and recoverability (Trash / Activity undo /
  named restore / irreversible-purge label) per ADR-0060 §7 — disclosure never hides the safety copy.
- Disclosure state is presentation; it must remain keyboard-operable with deterministic focus order
  and announced expanded/collapsed state (ADR-0060 §7, ADR-0100).

---

## 6. Management capability map

The binding rules are:

- Core Web UI owns local management; Premium native clients may improve ergonomics but never make
  local Core administration unavailable from `/web/`.
- Destructive changes route through audited server mutations and remain recoverable through Trash,
  Activity undo, or a named restore flow unless explicitly labeled as irreversible purge.
- Plugin surfaces may render trusted server-declared descriptors, but never downloaded plugin JS,
  HTML, view code, or plugin-declared auth scopes.
- Console grouping and child-surface placement live in this IA spec.

This spec owns the capability map and authorization matrix, not the release-status inventory.

### 6.1. Child-surface placement map (authoritative)

Every ADR-0060/0061 child surface maps to exactly one home. This table is the single source of truth
for placement; ADR-0060/0061 reference it (parent-points-spec-maps). Manage/Viewer rows are listed
for completeness — their internal IA is owned by those registers, not by this Console spec.

| ADR | Surface | Area | Group → destination |
|---|---|---|---|
| 0080 | First-run setup & bootstrap | (pre-Console wizard) | Overview → first-run checklist (residual) |
| 0064 | File operations & folder browser | Console + Manage | Library → Libraries (browser); Manage for file ops |
| 0069 | Scraper credentials & outbound endpoints | Console | Library → Identification |
| 0070 | Plugin activation/config/lifecycle | Console | Library → Plugins |
| 0071 | Layout & navigation override editor | Console | Library → Layout & Navigation |
| 0074 | Live sessions dashboard & controls | Console | Monitor → Live Sessions |
| 0075 | Hardware transcoding config & probe | Console | Monitor → Operations (advanced: probe) |
| 0076 | Jobs dashboard & resource governor | Console | Monitor → Operations |
| 0073 | Activity log, object history & undo | Console | Monitor → Activity & Logs |
| 0042/0061 | Library Health | Console | Monitor → Library Health (remediation → Manage) |
| 0086 | Profile, device & access administration | Console | Access → Profiles & Devices |
| 0081 | Browser enrollment, pairing & promotion | Console | Access → Profiles & Devices (pairing) |
| 0087 | Access schedules, filters & managed policy | Console | Access → Schedules & Policy |
| 0012/0018/0025 | Remote access | Console | Access → Remote Access |
| 0013/0040 | API credentials | Console | Access → API Keys |
| 0077 | Backup, restore guidance & metadata export | Console | Maintain → Backup & Export |
| 0078 | Trash policy, browse, restore & purge | Console + Manage | Maintain → Trash & Retention; Manage for content-Trash |
| 0051 | Retention rules (post-parity) | Console | Maintain → Trash & Retention |
| 0037/0079 | Migration wizard & update/migration warnings | Console | Maintain → Migration; Updates |
| 0013 | Server config | Console | Maintain → Server Config |
| 0085 | Admin session, owner reauth & console authority | Console | Top-bar chrome (gates the whole area) |
| 0065 | Tag manager & assignment | Manage | Manage register |
| 0066 | Marker & segment editor | Manage / Viewer player | Manage register |
| 0067 | Collections & smart-collection editor | Manage | Manage register |
| 0068 | Unmatched queue & fix-match | Manage | Manage register (Health/Migration link in) |
| 0072 | Field locks & overwrite protection | Manage | Manage register (Identification cross-link) |
| 0082 | Profile switcher, settings & viewer prefs | Viewer | Viewer register |
| 0083 | Web viewer browse, search & navigation | Viewer | Viewer register |
| 0084 | Web playback surface & media session | Viewer | Viewer register |

---

## 7. Reconciliation

This spec adds **no** capabilities; it organises the existing Console surfaces into the groups
above. Disclosure controls, the triage queue, and group toggles inherit the accessibility floor,
and the rail, tiles, and disclosures consume semantic tokens; no new primitives.

---

## 8. Binding authorization matrix

This matrix is the binding authorization summary for Core Web UI surfaces. It preserves the
existing small, orthogonal axes and does not create enterprise RBAC.

- **Surface / action** lists the root-relative React route first, with the canonical external
  `/web/...` route in parentheses where the distinction matters.
- **Enrolled browser/device** means an enrolled browser/device token or equivalent client device
  key. First-run setup and pairing-code redemption are the intentional exceptions.
- **Adult Profile** means the selected Profile is adult. Managed Profiles remain Viewer-only and
  do not self-serve management or My Data deletion.
- **Owner Profile** means the selected adult Profile has the owner flag from ADR-0004.
- **Admin Session** means the short-lived owner-adult + server-admin-password tier from ADR-0007.
- **MANAGER role** means the enrolled browser/device has the MANAGER Device Role.
- **Reveal** means the session-scoped, adult Profile-only, PIN-gated Reveal state from ADR-0004.
  It is required only when the read or mutation would name, serve, count, restore, purge, or
  otherwise act on restricted Library content.

| Surface / action | Enrolled browser/device required | Adult Profile required | Owner Profile required | Admin Session required | MANAGER role required | Reveal required | Notes |
|---|---|---|---|---|---|---|---|
| `/setup` (`/web/setup`) | No for first-run claim; yes for post-setup checklist | No for first-run claim | No for first-run claim | No for first-run claim | No | No | First-run setup is gated by the one-time setup token from server logs and creates the admin password, first Library, and owner Profile. After setup, retry/edit flows move to the Console and follow the relevant Console row. |
| `/console` (`/web/console/*`) | Yes | Yes | Yes | Yes | No | Conditional | Console is the server-shaped administration surface. Admin Session is orthogonal to Device Role; MANAGER status never substitutes for owner/admin re-auth. |
| `/console/libraries` (`/web/console/libraries`) | Yes | Yes | Yes | Yes | No | Conditional | Library creation, roots, scan policy, Restrictions/encryption/custodial flags, schedules, and retention policy are Console administration. Reveal is required to list or operate on restricted Libraries or their content; unrestricted Library setup does not require Reveal. |
| `/console/plugins` (`/web/console/plugins`) | Yes | Yes | Yes | Yes | No | No | Plugin activation/configuration, scraper credentials, outbound endpoint review, permissions, update sources, status/dead-letter handling, and server-performed lifecycle actions are operator/admin policy. Manager may request descriptor actions only where Plugin Control Policy allows, but the server remains the runtime authority. |
| `/console/migrate` (`/web/console/migrate`) | Yes | Yes | Yes | Yes | No | Conditional | Migration Wizard orchestration is Console administration, not Manager curation. Listing plugin-declared importers does not require Reveal; selecting restricted target Libraries/Profiles, reviewing restricted match results, or applying imported state to restricted content requires Reveal. Source credentials and export paths are operator-supplied secrets/host paths and stay out of scoped `/v1/` developer APIs. |
| `/console/api` (`/web/console/api`) | Yes | Yes | Yes | Yes | No | No | Creating, rotating, revoking, and scoping local API credentials is Console administration. Scoped API key use is evaluated later by token scope, optional Profile binding, and Reveal filtering; admin API tokens remain a separate operator-only class. |
| `/console/access` (`/web/console/access`) | Yes | Yes | Yes | Yes | No | Conditional | Profile CRUD, other Profiles' layouts, Library access policy, device revoke/promote, pairing-code issuance, and access schedules are owner/admin actions. Reveal is required when the action names or changes access to restricted Libraries. |
| `/console/remote` (`/web/console/remote`) | Yes | Yes | Yes | Yes | No | No | Remote URLs, TLS/direct exposure, overlay/self-hosted access, local credential rotation, and remote-enrollment toggles are server administration. Hosted account/device approval stays outside Core. |
| `/manage` (`/web/manage/*`) | Yes | Yes | No | No | Yes | Conditional | Browser Manager curation requires adult Profile on a MANAGER browser/device. Owner flag and Admin Session are not required for curation. |
| `/manage/entities` (`/web/manage/entities`) | Yes | Yes | No | No | Yes | Conditional | Entity and Media curation, relation/order editing, dedupe, fix-match, artwork, field locks, and restore flows follow the Manager gate plus Profile/Reveal filtering. |
| `/manage/bulk` (`/web/manage/bulk`) | Yes | Yes | No | No | Yes | Conditional | Bulk edit/delete/restore is Manager curation. The UI must preview blast radius and route destructive batches through audited server mutations. |
| Destructive file operations | Yes | Yes | No | No | Yes | Conditional | File/folder create, rename, move, delete-to-Trash, and managed relocation are destructive/file mutations gated by MANAGER Device Role, not Admin Session. If Trash is disabled, irreversible destruction needs explicit human confirmation under invariant #6. |
| Trash restore/delete | Yes | Yes | No | No | Yes | Conditional | Restore is an audited MANAGER mutation with path-collision handling. Permanent purge is irreversible, must be labeled as such, must require confirmation, and is audited. Console Trash policy configuration follows the Console administration rows. |
| Viewer playback | Yes | No | No | No | No | Conditional | Any selected Profile may play content allowed by its Library access and policy. Restricted Library playback additionally requires an adult Profile with Reveal; media-serving endpoints and Stream Tokens remain server-filtered. |
| My Data (`/web/mydata`) | Yes | Yes for self-service | No | No | No | Conditional | Adult Profiles can view/export/delete their own Profile-exclusive data from Viewer/Profile Settings without Manager or Admin rights. Managed Profiles have no self-service My Data path; guardian/operator deletion uses the Console Admin Session path. Reveal is required only to include restricted Media-linked rows. |
| Pairing/enrollment (`/web/enroll`) | No to redeem; yes to issue from Console/app | No to redeem; yes to issue from Console | No to redeem; yes to issue/promote from Console | No to redeem; yes to issue/promote from Console | No to redeem; no for ordinary VIEWER enrollment | No | Pairing codes are the only enrollment credential. The admin password is never accepted from an unenrolled client. A new device redeems a short-lived pairing code; issuing codes, remote-enrollment toggles, revocation, and MANAGER promotion follow `/console/access`. |

### 8.1. Conflict resolutions

- ADR-0007's original text allowed browser enrollment by pairing code or server password. ADR-0012
  supersedes that: pairing codes are the only enrollment credential, and the admin password is
  never accepted from an unenrolled client. This matrix follows ADR-0012.
- ADR-0007 uses both "Admin" and "Console" for the server-shaped area. ADR-0060 standardizes the
  product name as **Beamfall Console** and the architecture name as **Core Web UI**; this matrix
  uses `/console` for that area.
- No conflict was found between the Profile/Reveal, MANAGER Device Role, owner/Admin Session, My
  Data, scoped API key, destructive file operation, or Trash semantics. The matrix keeps them
  separate: Viewer/Profile/Reveal permissions do not grant MANAGER/Admin authority, and
  MANAGER/Admin authority does not bypass Reveal filtering.

## 9. Verification implications

The implementation of this IA (a follow-up WEB ticket against `internal/web/`) should be pinned by:

- A rail-structure test: the Console rail renders the five fixed groups in order, with Maintain
  collapsed by default, and every ADR-0060 `/web/console/*` destination resolves under exactly one
  group per §3/§6.
- An Overview-contract test: `/web/console` renders status tiles + Needs-attention queue +
  first-run checklist; queue rows deep-link to the owning destination; the empty queue uses the
  Empty state family and never an error; restricted rows stay indistinguishable from absence.
- A disclosure-a11y test: group toggles and in-panel `Advanced` disclosures are keyboard-operable
  with deterministic focus order and announced expanded/collapsed state; safety copy is never hidden
  by collapse.
- A placement-coverage check: every 0064–0087 child surface (and the §6.1 table) maps to exactly one
  area/destination; an unmapped or doubly-mapped surface is a defect.

Until that ticket lands this is a binding design contract; implementation in `internal/web/` is the
named follow-up.

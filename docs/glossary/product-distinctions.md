# Product distinctions


**Restrictions** *(App Store label)* / **Reveal contract** *(internal term)*:
The **server-enforced**, Library-scoped content hide. A restricted Library is hidden by
default under an enumerated completeness contract — absent from navigation, search, every
Source, continue-watching, stats, notifications/webhooks, and cross-device presence. The
**secrets toggle** is the session-scoped reveal: adult-profile-only, PIN-gated, auto-relocking
on background/idle/profile-switch; managed profiles have no reveal path at all. Granularity is
**Library-level only** — a secret item is an item in a restricted Library; there is no
per-item/per-tag secret flag. Server-enforced because sessions carry a Profile (an unrevealed
session is simply never served restricted content) — a strict upgrade over the predecessor's
client-side hiding. App Store builds use only the **Restrictions** (parental-control)
vocabulary.
_Avoid_: "private vault", "hidden content reveal" in App Store copy, per-tag hide lists *as a
Restrictions mechanism* (Reveal stays Library-level; soft per-Entity/Tag personal hiding is
**Mute**, a separate non-security preference), client-side-only enforcement.

**Mute** *(internal term; user-facing "Not interested" — per-Profile soft hide, 2026-06-14)*:
A per-Profile, **self-authored** soft suppression of an **Entity** (any Kind — Show, Genre,
Studio, Artist…) or a **Tag**: muted targets vanish from that Profile's **passive discovery
surfaces** (every browse rail, recommendation, recently-added, continue-watching, related
rails) while staying **searchable on purpose, reachable by direct navigation, and fully
playable**. The soft, person-set sibling of the operator-set **enforced filters** and the
security-grade **Restrictions / Reveal contract**: a Mute is a preference the person sets and
clears *for themselves*, never a policy imposed on them and never a confidentiality boundary.
Mechanically an implicit **negative Source predicate** — exclude Media related to any muted
Entity or carrying any muted Tag — riding the existing entity/tag filter vocabulary
(**include-descendants** honored for hierarchical Genre/Tag targets). **Specificity wins**: a
direct entity Mute suppresses everywhere (including continue-watching), but a broad category
Mute (Genre/Tag) yields to an explicit positive signal on a specific entity — **Favorite**,
active continue-watching, or **Collection** membership — so muting a genre never eats a show
you favorited. A **discovery suppressor only**: it never rewrites engagement history, never
gates playback or **Scrobbling**, never affects another Profile, and never touches
operator/server-global surfaces (**Webhook Emitter**, Console, catalog stats) — though it does
suppress the Profile's own new-content push notifications. Distinct from **continue-watching
dismiss** (one item, one rail, transient).
_Avoid_: "Hide"/"Hidden" (collides with the Snapshot **hidden-state** rule and Reveal-hidden
content), "Block" (collides with enforced **blocked tags**), "Dismiss" (the continue-watching
one-item action), treating a Mute as a security/Reveal boundary, making muted content
unfindable by explicit search, per-Media mute (v1 targets Entities + Tags).

**Push** *(server→device wake signal, ADR-0039 §2.5)*:
The server's **wake-up signal** to a Profile's enrolled devices for **time-sensitive
cross-Profile events** (parental **ExtensionRequest**, **AccessRequest**, Watch Party
invitation) — delivered via APNs / FCM / browser Push, operator-credentialed, no third-party
relay. The payload carries **only a notification type + a server-issued opaque event ID**; the
client then fetches details over the authenticated, Reveal-filtered channel. Push is a signal,
**not** a data or messaging channel — platform payloads never carry Profile names, Media titles,
Library names, or decision content. The scope line: *if a human is waiting on a blocked screen,
use Push*; everything else uses the **Notification Inbox**.
_Avoid_: "communicate/send/message via push" (it wakes, it does not carry content), push for
non-urgent events, a proprietary push relay, content in the payload.

**Notification Inbox** *(polled in-app surface, ADR-0039 §2.5)*:
The **per-Profile, polled** in-app surface for **non-urgent** notifications (new content
available, import/job complete) and the **fallback** when **Push** is disabled on a device —
refreshed on app foreground, not real-time. The non-urgent sibling of **Push**: the two split
the same event space along the waiting-human line. A Profile's own new-content entries are
suppressed by its **Mute** preferences.
_Avoid_: routing inbox-class events through **Push**, treating the inbox as real-time, conflating
it with the domain-blind **Webhook Emitter** (ADR-0040, operator/server-global, not per-Profile).

**Diagnostic Bundle**:
A local-first, user-inspectable package of redacted app or server diagnostic evidence that can be
exported or explicitly submitted for bug and crash investigation. It is not telemetry: Beamfall
may also receive platform opt-in crash reports, but Beamfall-owned diagnostic collection is never
silent phone-home.
_Avoid_: "bug report" or "crash report" for the bundle artifact, "support bundle" in new docs,
default-on telemetry, automatic server submission.

**Locked** *(managed-Profile enforced-filter visibility state, 2026-06-21)*:
A third outcome for **enforced filters**, between *shown* and the **Reveal contract**'s *gone*:
enforced-filter-blocked **Media** rendered to a **managed Profile** as **visible-but-blocked** —
present in browse/search with normal catalog metadata, a lock affordance, and an **"Ask a parent"**
action, but unplayable. **Opt-in per enforced filter, default hide**: a filter left in hide mode
keeps today's behavior (the Media is absent — a negative Source predicate). Never applies to the
**Reveal contract** — restricted (secret) Libraries are *gone*, never *Locked*, so the completeness
contract is unbroken. The visible sibling of the operator-set hide, and the precondition for an
**AccessRequest**.
_Avoid_: applying Locked to restricted/Reveal Libraries, framing it as acquisition-adjacent (it is
standard parental gating, App-Store-safe), treating Locked as a confidentiality boundary (it is a
managed-Profile policy presentation, not security-grade hiding).

**AccessRequest** *(managed-Profile per-title unlock request, 2026-06-21)*:
A **managed Profile**'s request to unlock one **Locked** **Media** item, answered by an adult
Profile. **Durable/queued**: it persists as *pending* — not the 5-minute ephemeral model of the time
**ExtensionRequest** — until an adult resolves it or a long server TTL expires; the child sees
"Requested — waiting for a parent" and moves on. Reuses the ADR-0039 push fan-out to every adult
Profile, **first-adult-wins** atomicity, and the type-plus-opaque-event-id payload-privacy contract;
adds a parent **pending-request inbox**. Deduplicated one-pending-per-(Profile, Media). The *content*
sibling of the *time*-scoped **ExtensionRequest** (ADR-0039), and never an entitlement — it stays in
Core.
_Avoid_: conflating with **ExtensionRequest** (that is time, session-scoped, 5-min expiry; this is
content, durable, per-title), routing it to the proprietary cloud (invariant 7 — Core only),
any self-approval path for the managed Profile.

**AccessGrant** *(resolved per-title access decision, 2026-06-21)*:
The persisted resolution of an **AccessRequest**, scoped per-(managed Profile, **Media**), with
status `granted` (the Media is unlocked **permanently until revoked**), `denied` (soft — the child
may ask again later), or `suppressed` ("don't ask again" — the request affordance is withdrawn for
that title). Revocable and auditable: every grant/denial/suppression carries actor + timestamp and
appears in the managed-Profile policy editor (ADR-0087) as a list of exceptions a supervisor can
review and undo. A narrow per-title exception that pokes a hole in an **enforced filter**, never a
filter edit — approving one title never lifts a tag or rating-ceiling bucket.
_Avoid_: per-category grants (approving a title must not unblock a whole tag/rating bucket),
session-scoped or one-time semantics (the grant is durable until revoked), treating it as a cloud
entitlement (Core policy only).

**Web UI**:
The single server-embedded web app (`go:embed`, AGPL), in three areas: **Viewer** (a full
client at the root — Layouts, Profiles, the Reveal contract), **Manage** (the Manager
surfaces, for a promoted MANAGER browser + adult Profile), **Admin** (the **Console**, behind
an **Admin Session**). Also owns first-run onboarding (a fresh server's root URL is the setup
wizard). Installable PWA (shell caching only).
_Avoid_: "the PWA" as a separate product (it's one app), treating the web Viewer as a lesser
client (it honors every contract the native clients do).

**Console**:
The web-based, **server-shaped** operations surface — the Web UI's Admin area: plugin
activation, Library and Profile management, the Layout editor, Scraper config, device
enrollment/promotion, jobs, live sessions, backups, stats.
Owns structural and administrative editing (ADR-0004: the console authors, apps pick).
_Avoid_: putting content-curation flows here (that's the **Manager**), "admin panel".

**Manager**:
The **content-shaped** curation surface: dedupe/merge, the Unmatched queue and fix-match,
bulk edit, file operations, Trash review. It is also the rich control surface for active
server-side **Integration Plugins**: account-link flows, mapping UIs, status/dead-letter
review, and service-specific tools, using the plugin capabilities the connected server
declares. Exists wherever a MANAGER-role device runs: the native **macOS Manager app** (the
premium one — Companion Plugins, native player, Spotlight) and the web UI's manager surfaces
on a promoted browser. Destructive/file curation is gated by **Device Role**, never by
platform. Server-shaped administration belongs to the **Console**, not here.
_Avoid_: "manager" meaning server admin, giving viewers destructive curation, platform-based
(rather than Device-Role-based) gating of destruction, running Integration Plugin runtime code
inside the Manager.

**Standalone Mode** *(macOS, ADR-0099)*:
The macOS app operating with **no remote server configured**, against a bundled **Embedded
Server** instead. The "one-stop media tool" posture — encode, rename, scrape, organize, manage
locally without setting up a server. Amends ADR-0006 §1 ("useless without a server"); §2
direct-distribution-only and §5 the plugin firewall are preserved unchanged.
_Avoid_: "offline mode" (it is a full running server, not a degraded cache), "server-less"
(there *is* a server — it is local and auto-managed).

**Embedded Server / Local Server** *(macOS, ADR-0099)*:
The **real AGPL server**, bundled in the macOS app and run on `localhost` as a supervised
process with its own local data dir. Standalone work executes here, inheriting the server's
ffmpeg encode seam, scan/fingerprint file plane, jobs, Trash/audit/undo, and Custodial uploads
— no parallel in-app engine. Because it is a real server, **Promote-in-place** graduation is
relocating its data dir + root folders to always-on hardware.
_Avoid_: "embedded database" (it is the whole server), implying a forked or cut-down build.

**Origin Node** *(multi-machine serving, 2026-06-22 — DRAFT, pending ADR-0123)*:
An enrolled, always-on machine running the **real AGPL server binary** that holds media bytes
**locally** and serves them under the **main server's** authority, while the main server remains
the **single catalog authority** (one catalog, one Reveal contract, one Device-Role enforcer, one
**Stream Token** issuer — invariants intact). The Origin Node **scans, probes, fingerprints, and
transcodes its own bytes locally** (it has the bytes; the main server never reads them to catalog
them) and **reports its index up** (reusing the existing `Media`/`Media File` wire shape) to the main
server, which authors and owns the catalog rows. A **Library's home node is a Library-level binding**
(not a per-file column), and Media File identity is fingerprint-keyed **per home node**. It runs the
**same binary in an attached mode** along a two-face boundary: **authority** (authoring rows, issuing
Stream Tokens, authoring engagement) is **main-only**, while **byte-local work** (Identification +
fingerprint trust, transcode, file mutation) is **node-only**. It enrolls as a **distinct node class** via the existing cert-pinning
machinery (ADR-0012/0046), **not** as a VIEWER/MANAGER **Device Role** — a Device Role governs a
*person's* device making catalog mutations; an Origin Node is *infrastructure* that serves bytes.
**v1 byte delivery is proxy-only**: the client talks only to the main server, which issues the
Stream Token and ranges the bytes from the node (the ADR-0052 proxy-tee shape) — so the main server
stays the single Reveal/token enforcement point. The faster **direct-LAN path** (and the second
Reveal enforcement point + offline token validation it needs) is **deferred** to a later ADR.
A Library lives on **exactly one node**; the model is **1 main : N nodes**. The
**attach-instead-of-promote** counterpart to **Embedded Server** Promote-in-place: a Standalone Mac
keeps its bytes in place and joins an existing main server, rather than relocating its data dir to
become one. (In this glossary the bare word "node" means an Origin Node — never a **Folder**, whose
own entry reserves "Node" against directory-tree usage.)
_Avoid_: "federation"/"peer server"/two-catalog framing (the catalog stays single-authority),
calling it a **Device Role** or a MANAGER device, "remote mount"/"network share" (it is a serving
server binary, not a filesystem seam), conflating with **Standalone Mode** (no main server) or with
a **Relay** (byte-blind pipe, holds no catalog and serves no bytes of its own).

**Origin-Node Unavailable** *(reachability state, 2026-06-22 — DRAFT)*:
The transient per-node state when an **Origin Node** is powered off, asleep, or off-network — a
**non-durable** signal derived from node heartbeat, **never persisted as catalog truth**. Its items
stay **fully visible** with complete metadata (the catalog rows live on the main server) but are
**unplayable** and shown as "on \<node\>, currently offline"; resume, Collections, deep links, and
continue-watching all survive (engagement is main-authored). **Strictly distinct from missing**:
`missing` means a *vanished content fingerprint* — a data-loss health alarm, "never silently
deleted" (ADR-0009) — whereas Unavailable is mere unreachability. The node's "fingerprint gone →
missing" check is **scoped to the node's own roots**; an offline node **never** raises the missing
alarm; only when the node is **online and reports a fingerprint gone** does the item become
`missing`. A **grace/debounce window** (mirroring ADR-0009's grace) keeps sleep/wake cycles from
churning availability. Distinct again from **permanent node removal** (an operator action with a
chosen tombstone-or-retain end-state).
_Avoid_: hiding offline items (churns Collections/resume/links, breaks the single-catalog promise),
reusing `missing` for unreachability (cries data-loss wolf every time the Mac sleeps), persisting
availability into the catalog/migrations (it is a heartbeat projection).

**Local-default activation** *(ADR-0099)*:
On first `--local-default` boot, the Embedded Server auto-activates the **existing first-party
domain plugins** (`internal/plugin/firstparty/{filmtv,music,photos}` — declarative Entity Kinds +
enrichment scrapers) so Standalone Mode gets mainstream metadata through the normal Plugin/Scraper
path. **No new "generic" plugin is created** — the firstparty plugins already live under the one
vocab-exempt prefix and already pass the firewall gate, so the Mac base binary stays domain-clean
and the §5 invariant (domain vocabulary only ever via a Plugin) is untouched.
_Avoid_: "Generic Media Domain Plugin" (no such new plugin — say the firstparty filmtv/music/photos
plugins), compiling providers into the base binary, treating "generic" as license to carry
domain vocabulary.

**Quick Actions** *(macOS, ADR-0099)*:
The **file-first, non-cataloged** surface — drag a loose file, encode/rename/tag it, done, with
no required Library membership — backed by the Embedded Server's engines via a new non-cataloged
job path. The Handbrake/FileBot-style counterpart to the catalog-first **Manager** tables.
_Avoid_: leaving catalog residue from a one-shot action, treating Quick Actions as a second
execution engine (it is the same server jobs, just uncataloged).

**Commercial Deployment**:
An organization-run Beamfall server used by **one organization** to serve that organization's
own members, customers, or staff through domain Plugins, Libraries, Profiles, and the
**Reveal contract**. An expansion constraint, not the launch business model: Beamfall may
support these deployments without hosting their libraries, owning their viewer identities,
adding multi-tenant organization isolation, or putting entitlement/billing logic in the AGPL
server. Viewer access still uses enrolled devices and Profiles; it is controlled member
access, not anonymous/public link playback. Commercial Deployments use the standard Beamfall
clients with Plugin/Library branding through labels, Layouts, theme tokens, icons, and
wordmarks; they do not imply white-label binaries or per-organization client forks.
_Avoid_: Beamfall-hosted SaaS, built-in membership billing, server-side licensing, making a
**Beamfall Account** know viewing or library data, multiple unrelated organizations on one
server, public share links, anonymous playback, white-label apps.

**External Access System**:
The non-Beamfall system of record for a **Commercial Deployment**'s customers, members,
students, patrons, or staff. It may drive Beamfall through the documented API, scoped keys,
webhooks/SSE, and Profile/Library access changes; Beamfall remains the media server and access
enforcer, not the membership, billing, or CRM authority.
_Avoid_: Beamfall-native customer accounts, Beamfall-native subscriptions, treating Profiles as
billable customer records, making the **Beamfall Account** an organization identity provider.

**Curation vs Engagement**:
**Curation** is deliberate metadata the user authors (tag, rename, covers, markers, organize).
**Engagement** is the passive signal watching emits. Viewers may write engagement;
destructive/file curation belongs to the manager surface only.

**Engagement Event**:
The canonical engagement record: a timestamped, **Profile-scoped** event (play with
start/end, resume position) in a Core event log, with counts **derived, never stored**.
Plugins may declare custom engagement event kinds (e.g. a plugin-defined counter) that
ride the same log — one mechanism, no parallel counters.
_Avoid_: stored aggregate counters as source of truth, plugin-private engagement tables.

**Scrobble** *(2026-06-14, ADR-0049)*:
A per-Profile, opt-in transmission of a Profile's *own* play to an external watch/listen-tracking
service (Trakt, Last.fm, ListenBrainz), emitted by the **scrobble Integration Plugin** — the
**first per-Profile Integration Plugin** (every prior one is server-global): the operator
*activates* it, each **Profile** links its *own* account. **Push-only** v1 (one-time history
backfill is a Migration Wizard Importer, ADR-0037; two-way reconciliation is a fast-follow).
**Reveal-filtered at the emission point** and per-Library-eligibility-gated — a restricted-Library
play **never scrobbles**, even from a revealed session — and **External-ID-gated** (Trakt →
TMDB/IMDB/TVDB, Last.fm/ListenBrainz → MusicBrainz): an item without the needed identity is
**skipped with a logged reason**, never fuzzy-matched.
_Avoid_: operator-wide or default-on scrobbling, scrobbling restricted content, fuzzy title
matching, treating it as telemetry (it is the Profile's own explicit choice — invariant #5),
conflating with the domain-blind **Webhook Emitter** (ADR-0040).

**Device Role**:
The transport-level capability of an enrolled device key: **VIEWER** or **MANAGER**.
Destructive and file-touching mutations are accepted only from MANAGER devices —
server-enforced — and this governs the **catalog**. A Profile's **own-data operations** —
backing its camera roll up to its private **Custodial Library**, deleting its own backups, and
contributing or removing-its-own in the shared household Library — are a *separate orthogonal
axis* gated by a per-Profile **backup capability** (enabled by an **Admin Session**), not by
Device Role: a VIEWER device performs them and stays VIEWER (2026-06-13, ADR-0045). Removing
*another* Profile's photos is catalog curation → MANAGER. Orthogonal to **Profile**: the Device
Role says what the *machine* may do;
the Profile says what the *person* sees. Manager surfaces render only for an adult Profile on
a MANAGER device. Apps enroll with their role; browsers enroll as VIEWER and may be
**promoted** to MANAGER by an **Admin Session** — deliberate, per-device, revocable, listed
in the Console.
_Avoid_: conflating device rights with person identity, client-side-only gating of
destructive ops, treating own-data backup writes as catalog file-touching (they need no
MANAGER).

**Admin Session**:
The third authorization tier (after VIEWER and MANAGER **Device Roles**): an owner-flagged
adult **Profile** plus the **server admin password** (one per server, set at first run),
re-authenticated short-lived. Gates all server-shaped (**Console**) operations — its own
mutation class, orthogonal to Device Role: any browser can host an admin session; no PIN ever
grants one. Browsers enroll as VIEWER devices (pairing code or server password) and pick
Profiles like any client.
_Avoid_: per-person admin credentials (ADR-0004: profiles have no credentials), PIN-gated
admin, conflating Console rights with MANAGER device rights.

**Session**:
The first-class server object every authorization decision reads: (device key, **Profile**,
reveal state, expiry). Long-lived transports — subscriptions, tombstone feeds, deviceBus —
re-evaluate it per event, so relock and revocation take effect immediately everywhere.
_Avoid_: treating reveal as client state, transports that check auth only at connect.

**Stream Token**:
The short-lived signed credential carried in every media-byte URL (direct-play ranges, HLS,
scrub thumbnails, artwork, sidecars), scoped to device + Profile + reveal state + media + expiry —
because AVPlayer, `<video>`, and AirPlay cannot send auth headers. The mechanism that puts
byte-serving endpoints inside the Reveal contract.
_Avoid_: long-lived or device-key-only media URLs.

**Claim Code**:
The short code that pairs a server with a future **Hosted Relay** account: minted by the
relay service at web checkout or beta invitation, typed once into the Console. Distinct
from a **pairing code** (which enrolls a *device* with a *server*, ADR-0012) — a Claim Code
connects a *server* to a *paid service*. Entitlement lives entirely in the relay's token
mint; the AGPL server only presents tokens it was given. Not used for **Self-hosted Relay**.
_Avoid_: "pairing code" for relay setup (collides with device enrollment), any
entitlement/licensing logic in the server itself.

**Parity Ledger**:
The maintained capability table (ADR-0017 §8) recording, for every user-facing Viewer
capability, the Apple mechanism, the Android mechanism, and the parity status (parity /
exceeds / deferred-with-rule). The contract artifact behind "parity by contract, Android by
idiom": a gap persisting past one release cycle is a bug; deliberate asymmetries (e.g. no
Play Family Sharing for IAP) are recorded there, never discovered by users.
_Avoid_: treating parity as feature-list identity (it's capability parity via each
platform's best idiom), shipping either ecosystem a capability without a ledger update.

**Capability Profile**:
A device's structured declaration of what it can play, per engine (containers, codecs with
limits, HDR formats, subtitle formats, downstream audio hint). Declared at enrollment
(apps) or probed at session start (browsers); the input to `playbackInfo` negotiation.
_Avoid_: one "web" profile (each browser family is its own), server-side guessing.

**Profile**:
A person using the server — Netflix/Plex-style, on top of device auth (the device holds a
per-device key; the Profile is picked at app launch). Name, avatar, optional PIN, and a kind:
**adult** (PIN-gated entry, may hold Restrictions reveal rights) or **managed** (kid — opens
free, sees only its allowed Libraries, no path to restricted content). Per Profile: a Library
access list, Layout overrides, default filters, **enforced filters** and **access schedules**
(server-applied policy — blocked tags, plugin-provided rating ceilings, time windows; the
managed-profile hardening of default filters), and all Engagement. (Deliberately reverses
the predecessor's single-user vault, 2026-06-10.)
_Avoid_: "account"/"login" (no per-person credentials — devices authenticate, people pick;
a **Beamfall Account** is a cloud purchase identity, unrelated to Profiles), "user" in
schema names (the wire term is Profile).

**Guest**:
An out-of-household person served by the existing machinery — an ordinary **Profile**
(scoped Library list, enforced filters, certification ceilings) plus their remotely-paired
device. **Not a distinct concept** (decided 2026-06-11): no invite-by-email identity, no
guest accounts, no tiered/expiring memberships (Wizarr-class membership service explicitly
rejected — that's running a service for strangers). Abuse containment is the per-Profile
stream/transcode caps.
_Avoid_: "shared user"/"friend account", any email roster on the server, a Guest schema
kind.

**Snapshot**:
A client's persisted, per-**Profile** copy of that Profile's world — Libraries, Entities,
engagement state, plus the Navigation Manifest and Layouts — used for instant launch
(render first frame from disk, sync after). The at-rest rule: a Snapshot only ever contains
the **hidden-state** world. Restricted content is never written to disk, even by a revealed
session — reveal is fetch-only and in-memory; re-lock leaves no residue. A PIN-gated profile
is never instant-rendered before its PIN.
_Avoid_: shared/global snapshots, persisting revealed content, treating the snapshot as a
sync source of truth (the server is).

**Offline Pack**:
A client-side encrypted cache of selected Media for offline playback, with bidirectional
progress sync on reconnect. The **one sanctioned exception** to the hidden-state-only
at-rest rule (ADR-0023; Snapshots remain absolutely hidden-state-only): a pack may hold
restricted Media under a named regime — per-pack key wrapped by device biometry, key
dropped on backgrounding/re-lock exactly as Reveal drops, pack *metadata* (names, artwork,
counts) still hidden-state-only, pack destroyed on enrollment revocation. At launch on
iPhone/iPad (iOS exceeds — recorded); Android follows post-launch (2026-06-11, amending
the 2026-06-10 post-launch posture).
_Avoid_: "downloads" implying exportable files, packs that survive enrollment revocation,
restricted content at rest *outside* a pack's key regime.

**Beam**:
The handoff gesture the product is named for: moving an *active playback session* between
two enrolled devices (e.g. iPhone → Apple TV, and back) at the exact canonical-timeline
position, as a server-side session transfer (ADR-0023). Not AirPlay — both ends are
Beamfall clients with their own Sessions and Stream Tokens; the server moves the session,
the bytes never relay through the first device.
_Avoid_: "cast"/"AirPlay" for this (those send bytes to a dumb receiver; Beam transfers
the session), beaming to unenrolled devices.

**Ambient Remote**:
A mobile companion UI for an **Ambient Display** session running on another device (typically
a TV). The mobile attaches to the TV's active ambient + audio sessions as a **remote
controller** — it enumerates the Profile's active sessions, subscribes to their event streams
for Now Playing state, and sends transport mutations (skip media, skip track, pause/resume)
without transferring the session to the mobile. Also surfaces **Dark Room** mode preference
controls (brightness, hold duration) and the display-mode switcher, so the user never needs
to touch the TV's dark screen. Subject to the **Reveal contract**: an unrevealed mobile
cannot see or control a Revealed session (decided 2026-06-12).
_Avoid_: confusing with **Beam** (Beam transfers the session; Ambient Remote leaves it on the
TV), treating this as a general remote-control product (it is Ambient Display-first; broader
session remote control is a future decision).

**Ambient Display**:
A full-screen presentation mode — a **playback posture**, not a **Layout** or **Section** — that
advances a muted media sequence (the **Ambient Queue**) while an independent audio session
runs via a compatible plugin's background audio posture (**Music Plugin** or **Podcast
Plugin**). Triggered by action on a **Source** or **Collection** from any Library (any
domain — video clips today, photos when core image-Media lands). The **Navigation Manifest**
gains one top-level "Ambient" entry pointing at last-used or default configuration.
**Engagement-silent**: no **Engagement Events** are emitted; ambient media does not affect
continue-watching, recently-played, or play counts. The two independent sessions (ambient +
audio) share no composite session object; Ambient Display is indifferent to which plugin
supplies the audio. Three composable display modes, switchable at runtime: **Visualiser**
(full-screen audio visualiser, no media sequence — real-time PCM-driven and/or precomputed
**waveform envelope** / **beat grid** Generated Assets), **Overlay** (that visualiser atop
the advancing media sequence), and **Dark Room** (black background; when a new audio item
begins, its metadata fades in to a user-configured maximum brightness then fades to black —
OLED-friendly, no Generated Assets required; hold duration also configurable). All media
bytes are served under **Stream Tokens** subject to the **Reveal contract** exactly as
standard playback (decided 2026-06-12).
_Avoid_: "slideshow" as a code/schema identifier, emitting play events for ambient media,
treating Ambient Display as a Layout type or Section, a composite session object spanning
both axes, hardcoding the audio side to Music Plugin only.

**Ambient Queue**:
The ordered sequence of muted media items advancing in the **Ambient Display** — distinct
from the Music Plugin's **Play Queue** (the audio side). Fed by either a **Collection** or
a **Source** filter expression. Not a separately persisted server object: assembled from the
selected Source/Collection at session start, re-assembled on reconnect. Advances
independently of the audio session. The user control is **skip media** (advance the Ambient
Queue), paired with **skip track** (advance the active audio session's queue — track or
episode, depending on the plugin) — two distinct transport axes on the Ambient Display's
controls overlay (decided 2026-06-12).
_Avoid_: "media playlist" (the Playlist label is a display name for a core ordered
Collection — these are different), conflating with the Play Queue (music side), treating
the queue as a durable server-persisted object.

**Ambient Group**:
A set of **Ambient Display** sessions on different devices (typically TVs) sharing a
beat-accurate audio timeline, coordinated by the server's **Group Conductor**. **LAN-only**
in v1 — internet jitter makes WAN beat-accurate sync impractical. Video is not synchronised
across members; each device independently advances its own **Ambient Queue** (the visual is
ambient decoration; only the ear demands sync). Group membership is explicitly formed by the
user from the **Ambient Remote** — automatic grouping is rejected. A device may join or
leave a group while it is running without stopping playback (decided 2026-06-12, ADR-0035 §8).
_Avoid_: treating an Ambient Group as a broadcast session or a leader-follower device
protocol; confusing with **SyncPlay** (a deferred co-watching feature with different
semantics); implying WAN support.

**Group Conductor**:
The server-side capability that manages synchronized multi-device playback groups, first for
**Ambient Group** and later for **Watch Party** and **Synchronized Display Group**. Maintains:
the membership list (each entry holds its measured render latency and clock offset); the shared
queue or selected item and canonical position; and the NTP anchor (`canonical position P was at
server NTP time T`).
On each audio transition the Group Conductor broadcasts `{ item, position, present_at }` to
all members via the existing session event channel; each member releases audio at
`T_present − own_render_latency` so all speakers present the same sample within the LAN NTP
accuracy window (~1–5 ms). Drift correction issues rate-adjustment signals (±0.1 % playback
rate) for members drifting < 500 ms; members beyond 500 ms re-sync via a full transition.
The plugin remains the queue/item source; the Group Conductor owns queue-position state for
the group. A controller device is stateless — the Group Conductor (server) keeps the group
alive if the controller sleeps (decided 2026-06-12, ADR-0035 §8; generalized 2026-06-17).
_Avoid_: treating the mobile as conductor; leader-device protocols; confusing Group
Conductor with the plugin that supplies the audio queue, treating a **Relay** as timeline
authority.

**Synchronized Display Group** *(commercial expansion concept, 2026-06-17)*:
A **Group Conductor** mode for a **Commercial Deployment** where multiple enrolled clients
present the same server-selected media timeline at the same time — signage, classroom,
studio, event, or facility display. Each client fetches bytes through its own **Stream Token**
and Session; the shared signal is timeline metadata (`item`, `position`, `present_at`), not
relayed media bytes. **LAN-first**: same-site groups are the initial target; WAN/Relay support,
if added later, is a weaker synchronized-start mode rather than beat/frame-accurate sync.
Distinct from **Watch Party**: there is no social host/participant model, no per-viewer
transport requests, and no public invitation flow. Control belongs to **Manager**/**Console**
surfaces or a scoped automation key; display clients are enrolled Viewer devices. The server-side
**Group Conductor** primitives stay generic/open; a commercial entitlement may unlock
proprietary client features, **Companion Plugins**, dashboards, display-mode polish, and
automation UX for this mode, but the AGPL server does not gain entitlement checks.
_Avoid_: calling this Relay, public broadcast, anonymous screen links, peer-to-peer screen
mirroring, white-label signage app, promising tight WAN sync, letting any watching Profile
control a facility display group, entitlement-gated server Plugins.

**Visual** *(catalog item — Ambient Display + Music player, 2026-06-13)*:
One entry in the client's audio-reactive renderer catalog, named by **rendering shape**
(Spectrum, Tunnel, Aurora…), never by domain or mood. One catalog feeds two surfaces: the
**Ambient Display** **Visualiser** and **Overlay** display modes, and the background of the
**Music Plugin** player's Now Playing screen. A Visual is a **GPU shader authored once and
compiled into each client** — one portable source transpiled to Metal / Vulkan / WebGL at
*build* time — a fixed compiled vocabulary like **Sections**, grown only in client releases
(invariant #3); plugins never contribute a Visual, and nothing is downloaded at runtime. Each
Visual declares a manifest: minimum **Visual Tier**, which **Visual Runtime** inputs it
consumes, **Overlay**-capable or Visualiser-only, **reduced-motion** behaviour, flash/HDR risk,
text-safe zones, current-image dependency, and default-enabled surfaces. Reactivity comes from
the real-time PCM path, the precomputed **waveform envelope**, optional **beat grid** / sync-lane
Generated Assets, transport/time, and **Visual Director** cues derived from them (ADR-0035 §5,
ADR-0047). The first catalog is curated for quality, not breadth: six to eight excellent Visuals
beat a broad preset wall. A Visual that declares the **current image** input is *image-capable*:
in Ambient Display it renders the current **Ambient Queue** photo as audio-reactive material
rather than a raw crossfade — the deliberate step beyond ADR-0043's "slideshow only". Image
Media is gated on core image-Media (ADR-0027 §3) + the **Photos Plugin**, so photo Visuals are a
later increment than the music-only catalog. (A third-party *downloadable* gallery — which would
need a sandboxed data-driven runtime — is deliberately deferred, not committed.)
_Avoid_: "Visualiser" for a single Visual (that names the display *mode*), plugin-contributed
or downloaded-at-runtime Visuals (invariant #3 — compiled in, like Sections), domain- or
mood-named Visuals, treating the catalog as a wallpaper pack, "Scene"/"Section"/"Theme" as the
term.

**Visual Director** *(2026-06-15)*:
The deterministic client-side show-control layer above individual **Visuals**. It does not add
downloaded code or plugin scripting; it maps the fixed **Visual Runtime** inputs into authored
camera cues, palette shifts, intensity envelopes, beat/phrase transitions, inter-Visual morph
points, and finale states. Its purpose is to make Beamfall Visualiser mode feel composed rather
than preset-shaped while preserving the same compiled shader catalog and fixed host vocabulary.
The Director may use real-time PCM-derived lanes and optional Generated Assets such as beat grid,
downbeat/phrase hints, confidence, and onset strength; every cue has graceful fallback when those
assets are absent. _Avoid_: server-authored shows, plugin-provided timelines, downloading cue code,
or making Overlay mode obey cinematic cues that would harm media/control legibility.

**Visual Runtime** *(2026-06-13)*:
The per-platform host layer (Apple / Android / web) that runs the compiled **Visuals** and
feeds them a **fixed input vocabulary** — and nothing else. Raw host inputs are real-time PCM
spectrum, the **waveform envelope** and optional **beat grid** / sync-lane Generated Assets, the
**Theme Token** accent color, the **current image** — the now-playing **artwork**, or the current
**Ambient Queue** photo when one is present (both Reveal-gated; artwork is already in hand for
Now Playing) — transport/time, previous-frame feedback texture, and per-Visual post settings.
The Runtime derives a stable `VisualUniforms` contract from those inputs: level, band energies,
bass impact, flux/onset, spectral centroid, beat phase/count/confidence when available, frame
timing, quality scalar, overlay-safe limits, and safety clamps. The "small fixed vocabulary" half
of the unbounded-data discipline; one input contract across all three platforms, so a Visual
authored once renders everywhere it is offered, subject to the device's **Visual Tier**. v1 hosts
only the compiled first-party catalog — it interprets no downloaded data (the deferred third-party
gallery would add that, with the sandbox it requires).
_Avoid_: treating the runtime as a plugin or scripting surface, exposing open-ended host/OS APIs
to a Visual (the fixed input vocabulary only), Metal-only source-of-truth shaders, assuming a
runtime data-interpreter exists in v1 (Visuals are compiled in, like **Sections**).

**Visual Tier** *(2026-06-13)*:
The fixed ordinal capability level that gates which **Visuals** a device may run: **Lite** <
**Standard** < **Flagship**. A device **self-classifies** from form factor *and* GPU (phone →
Lite, tablet / mid-range → Standard, TV + desktop + high-end → Flagship), so capability — not
device class alone — decides: a weak TV box stays Standard, a strong phone may reach Standard.
Each Visual declares its **minimum tier**; a device offers only Visuals at or below its own,
so "huge 3D" simply never appears on a phone. Light self-scaling (particle count, resolution)
within a tier is a client-side UX detail; the *gate* is the contract. Grown rarely, in client
releases (cf. **Archetype**).
_Avoid_: equating tier with screen size alone (it is capability), downloading per-Visual tier
assignments as data (the gate is client-evaluated), conflating with the **Capability Profile**
(that negotiates codecs/playback — a different axis entirely).

**Hosted Relay**:
The future first-party paid service for zero-config remote access: a Beamfall-run
byte-blind relay kept on the roadmap until product traction, demand, and cost evidence prove
the unit economics (ADR-0025). A Nabu-Casa-shaped subscription sold on the web, never
in-app, if it later graduates. Sells infrastructure only: the AGPL server gains no
entitlement checks, and LAN, direct HTTPS, overlay-network, and **Self-hosted Relay** paths
remain first-class.
_Avoid_: in-app purchase of the relay (Apple cut + review entanglement), relay-exclusive
server features, treating the relay as the only remote path, fronting open-ended relay
costs before viability is proven.

**Content Sync**:
The generic core service (ADR-0190) through which a home server pulls signed,
entitlement-gated curated content bundles from Beamfall's cloud: claim-code pairing,
purpose-scoped short-lived tokens (mirroring the Hosted Relay token mint), daily fetch,
signature verification, and an opaque domain-tagged cache core never parses for meaning.
One-way by construction — nothing about the household ever flows up. Domain plugins render
Sections from the cached payload; the Podcast Plugin's **Discover Editions** is the first
consumer. Always degrades gracefully: no entitlement or no cloud ⇒ the plugin's free
Discover, never a blank surface.
_Avoid_: "feed" for the mechanism (overloaded — RSS feeds are the podcast domain's word),
"subscription" (taken: the Library-level RSS object), per-query lookups through the cloud
(behavioral data; bulk pull only), entitlement code in the AGPL server (the mint enforces,
core never refuses).

**Self-hosted Relay**:
The advanced remote-access path where the operator runs the Beamfall relay endpoint on their
own VPS or public server and pays that provider directly. Same byte-blind shape as Hosted
Relay: TLS terminates at the home server, the relay forwards bytes, and media content stays
opaque to the relay. Launch-supported for power users; not a Beamfall Account purchase.
_Avoid_: calling it zero-config, tying it to Premium, or making it the only remote-access
story for consumer users.

**Beamfall Account**:
The optional passwordless cloud identity (email magic-link + passkeys) on the proprietary
service side, carrying **Premium** and any future Hosted Relay purchase across stores and
devices. Lives entirely outside the AGPL server — which never stores, proxies, or validates
accounts — and knows *what you bought, never what you watch*: no library, playback, or
engagement data ever reaches it. Never required: per-store purchases work accountless; the
account's pitch is "unlock your other devices." One account per household is tolerated by
design.
_Avoid_: confusing with **Profile** (a person on a server; no credentials), any account
awareness in the AGPL server, account-mandatory flows.

**Beta Access**:
The proprietary account-side state that lets an invited tester use beta client builds while
Beamfall is pre-GA. It is not a portable **License Key**, not a paid trial clock, and not
an AGPL server or Plugin entitlement; beta build expiry is a release-safety mechanism,
separate from tester access.
_Avoid_: hidden trial countdowns, automatic GA conversion, entitlement checks in the AGPL
server, beta keys that unlock stable paid releases.

**Client Trial**:
The device-local native-client grace period before **Premium** is required. It is not a
Beamfall Account state, not a **License Key**, and not a trial of the AGPL server, Web UI,
or server Plugins.
_Avoid_: server trial, cloud-issued trial key, treating reinstall-to-re-trial leakage as a
server licensing problem.

**Premium**:
The single paid entitlement (subscription or lifetime) across the ecosystem: playback on
native Viewer clients after the **Client Trial**, the **Manager**'s advanced tier, and **ongoing Photo
Backup** (auto-upload to a **Custodial Library**) — plus proprietary **Companion Plugin**
surfaces/client extensions where they exist, but **viewing, retrieving, and exporting
already-stored photos stays free forever** (ADR-0045: monetize the convenience, never the data;
a lapsed subscription pauses new backups and touches no stored photo). One entitlement,
three checkouts (web, Apple IAP, Play Billing) at identical prices, all granting the same
ecosystem-wide unlock via the **Beamfall Account**; a purchase's local store receipt always
suffices on its own store without an account. Everything required to stand up a working
household library stays free. The "lifetime stays lifetime" pledge binds to the account,
not a store receipt — and survives the project itself: lifetime **Entitlement Tokens**
are perpetual, and the **Continuity Pledge** backstops the rest.
_Avoid_: per-store-only entitlements (superseded 2026-06-11), a second paid SKU, gating
core curation/setup — or photo viewing/retrieval/export — behind Premium, entitlement checks
in the AGPL server, license-gating AGPL server Plugins.

**Commercial Entitlement** *(commercial expansion concept, 2026-06-17)*:
A proprietary, ongoing organization/deployment-scoped license that may unlock commercial
client features, **Companion Plugins**, dashboards, display-mode polish, and automation UX for
a **Commercial Deployment**. Distinct from household **Premium**: it maps to an organization,
site, or deployment rather than a personal media household, and may be metered per active
commercial display client — an ordinary enrolled **VIEWER** device/session whose proprietary
client is permitted to enter commercial display mode, not a new **Device Role** and not the
physical HDMI panel or projector it drives. Enforcement lives in proprietary clients/control
surfaces, never as licensing or entitlement logic in the AGPL server.
_Avoid_: treating household Premium as a facility license, per-server AGPL license checks,
personal Beamfall Accounts as organization identity, entitlement-gated server Plugins,
lifetime commercial facility licenses by default, adding a commercial server-side Device Role.

**Entitlement Token**:
The signed, offline-valid credential a client caches to prove **Premium** between contacts
with the entitlement service — verified against a compiled-in public key, refreshed
opportunistically, never revoked *in place* (no remote kill, no playback-time
phone-home; abuse is handled by refusing *new* issuance, plus a compiled-in blocklist of
known-leaked keys in **future builds only** — an installed client never changes its
verdict). Lifetime tokens are
**perpetual** — no expiry — and exportable as a user-held **License Key** (signed
file/QR embedding the buyer's email) that any client imports and verifies offline, so
even a brand-new device never depends on the entitlement service being alive. Distinct
from a **Stream Token**
(media-byte authorization, server-side, short-lived).
_Avoid_: launch-time live entitlement checks, remote or runtime revocation of cached
tokens (the compiled-in leaked-key blocklist in new builds is the one sanctioned
exception), conflating with Stream Tokens.

**Continuity Pledge**:
The public commitment paired with "lifetime stays lifetime": if Beamfall can no longer be
continued (development and distribution cease with no successor upholding both pledges),
**all proprietary applications are released as open source** under an OSI-approved
license, with a final gate-removed build of each app, and the cloud services simply shut
down. Combined positioning: *your purchase outlives our prices, our servers, and us.*
_Avoid_: framing it as contingent on store availability, scoping it to anything less than
every proprietary app, or letting any user-facing capability depend on the cloud staying
alive.

**Sidecar Track**:
A typed companion file detected at scan by filename pattern (`{basename}.srt`,
`{basename}.en.vtt`, plugin-declared extensions) and attached to its Media item, served over
authenticated endpoints. **Captions are a Core sidecar type**; plugins register additional
types (for example interactive tracks). The mechanic is generic; the App Store
binary knows only "typed sidecar tracks".
_Avoid_: domain sidecar names in Core code or App Store-facing strings.

**Precomputed Playback Rendition** *(2026-06-17)*:
A reusable local playback derivative generated ahead of demand for Media expected to fan out
or run on a schedule — commercial display loops, classroom sessions, event playlists, or known
popular items. Served through normal **Stream Tokens** and selected by `playbackInfo` like any
other playable Version/rung, so a crowd or display group does not require N identical live
transcodes. Triggered explicitly by an operator, **Schedule**, Source/Collection precompute
rule, or event/display setup; popularity signals may suggest candidates later but do not
silently generate renditions. Disk-backed by default; active fan-out may use a bounded **hot
segment cache** in RAM for the next/current HLS segments or byte ranges many clients are
reading at once. A server-derived cache object, not a **Version**: Versions are user-owned
files/encodes; Precomputed Playback Renditions are disposable, rebuildable, and budget-governed.
_Avoid_: generic CDN cache, cloud edge cache, automatically caching anything merely popular,
bypassing Stream Tokens or the Reveal contract, whole-media RAM residency by default, exposing
renditions as Library Versions.

**Trash**:
The server-side recoverable-delete store, **ON by default**: deleted files and merge-losers
move to a trash folder with a retention window and a restore path. Autonomous destruction
(e.g. one-click merge-all-duplicates) is permitted only while Trash is enabled; with Trash
off, every destructive operation requires human confirmation. (Reverses the predecessor's
trash-off reality and softens its "never autonomous" invariant into a gate.)
_Avoid_: "recycle bin" (OS concept), claiming deletes are recoverable when Trash is disabled.

**Retention Rule** *(2026-06-14, ADR-0051)*:
A Core, operator-authored automation that deletes catalog Media matching a declarative condition
after a grace window — the Maintainerr-class "leaving soon → Trash" capability, built entirely on
existing primitives, **not** a plugin. The match is a **Source** expression (one filter
vocabulary, no second query language) extended with one new **catalog-aggregate** predicate —
*watched by any / no Profile* (a household boolean only; never *which* Profile, preserving
Profile-scoped **Engagement**). Lifecycle: the match set is a **Smart Collection** ("leaving
soon" — user-visible, Reveal-scoped) → a configurable grace window → a **Schedule**-fired
delete-to-**Trash** Job (two safety layers: grace window then Trash window). Authored in the
**Console** (Admin Session); **requires Trash ON** (invariant #6). **Restricted Libraries are
exempt by default** (a non-revealed rule cannot see them — Reveal contract). A household member
rescues an item with **keep** — a **Favorite-class**, viewer-writable per-item flag; ≥1 keep or an
operator hold exempts the item, and an operator MANAGER hard-delete overrides.
_Avoid_: a standalone rule engine / second query language, exposing per-Profile detail in the
aggregate predicate, autonomous retention with Trash off, treating "keep" as MANAGER curation (it
is a viewer preference flag), conflating "leaving soon" (pre-delete warning) with **Trash**
(post-delete recovery).

**Deploy vs Release**:
A **Deploy** pushes the current build onto its own running destination (in-place, personal). A
**Release** cuts a versioned, published artifact for others to install (App Store, tagged
server release).

**Job**:
A durable unit of background or interactive work on the server's job substrate: persisted,
checkpointed, resumable, cancelable, deduped, and governed (interactive transcode preempts
background generation). Jobs are the **only** way work runs; everything that triggers work —
boot, scan completion, a Console mutation, a **Schedule**, the **Watcher** — does so by
enqueueing a Job.
_Avoid_: "task" as a schema or code identifier (informal synonym only), running work outside
the substrate.

**Schedule**:
A persisted rule that enqueues a Job when due. Expressed in a small closed trigger
vocabulary — `every N minutes/hours`, `daily at HH:MM`, `weekly on <day> at HH:MM` — stored
in the server's configured timezone, never as raw cron expressions. The **Scheduler** is the
server component that evaluates Schedules and enqueues; it never executes work itself.
_Avoid_: cron syntax in schema/UI, confusing with **access schedules** (per-Profile
parental-control time windows, ADR-0007 — unrelated).

**Watcher**:
The server component that observes Library root folders for filesystem changes and reacts by
enqueueing a debounced, deduped scan Job for the affected Library. An event source, not a
scan pipeline: there is exactly one scan path, and the Watcher merely triggers it earlier
than a Schedule would.
_Avoid_: per-path partial scans as a separate pipeline, treating watch events as reliable on
network filesystems (Schedules remain the correctness backstop).

**Backup Set** *(2026-07-07, ADR-0192)*:
The complete, encrypted, restorable unit of server disaster recovery: every database plus the
non-regenerable operational state a bare-metal restore needs (secrets, stream-token key, pinned
TLS identity, config, integration state), sealed under the operator's **Backup Key** and
described by a hashed manifest. Regenerable caches are excluded and rebuilt after restore;
media is referenced, never copied.
_Avoid_: calling a plaintext pre-migration copy a "backup set" (that is a **Rollback
Snapshot**), treating a Backup Set as a metadata export (portability is a separate flow,
ADR-0077).

**Rollback Snapshot** *(2026-07-07, ADR-0192)*:
The plaintext, databases-only, all-at-once copy taken on the server before any schema
migration begins. Its sole purpose is undoing a failed migration with zero keys and one
command; it lives in the data directory's trust domain and is pruned by schema-version soak,
not operator policy.
_Avoid_: encrypting it (rollback must never require the Backup Key), per-database copies
(cross-database skew makes partial rollback unsafe), using it for disaster recovery.

**Backup Key** *(2026-07-07; extends ADR-0115's BackupKey to local sets)*:
The user-held, shown-once, Recovery-Key-shaped credential that decrypts every Backup Set,
local or off-site. The server keeps only a public wrap key, so unattended backups encrypt
without the secret ever resting on the box; losing the Backup Key loses every set sealed
under it.
_Avoid_: server-side escrow beyond the bounded first-boot save-gate, conflating with the
Encrypted-Library **Recovery Key** (same shape, different secret).

**Restore Drill** *(2026-07-07, ADR-0192)*:
A rehearsal that proves a backup restores: restore into scratch, boot, migrate forward, verify
data fidelity and that a real client can authenticate. Runs as CI fixture drills per release
and as the operator-initiated, key-in-hand "Verify my backup now" action.
_Avoid_: claiming "restore verified" from anything less than a drill.

**Integrity Check** *(2026-07-07, ADR-0192)*:
The unattended verification the server can do without the Backup Key: the stored bytes match
the manifest hashes and the set is complete and well-formed. Catches bit-rot and truncation.
_Avoid_: presenting an Integrity Check as restore verification — only a **Restore Drill**
proves restorability.

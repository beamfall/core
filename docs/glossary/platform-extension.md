# Platform extension


**Compatibility Handshake** *(2026-06-19)*:
The server-authoritative compatibility check a client performs before normal API use. The
server decides whether the client may proceed, may proceed with degraded capabilities, or must
stop at an update-required screen; clients do not infer compatibility by probing failures.
_Avoid_: client-side best-effort probing, raw product-version matching, treating every server
bugfix as an app-breaking change.

**Client Contract** *(2026-06-19)*:
The compatibility declaration a client presents to the server: its app surface, app build,
rendering conformance stamp when applicable, supported contract revision ranges, supported
fixed vocabularies, distribution class, and requested capabilities. The server evaluates this
declaration during the **Compatibility Handshake**, deriving trust-sensitive facts such as
App Store binary treatment from known surface/build/enrollment state rather than accepting a
client claim at face value.
_Avoid_: User-Agent parsing, assuming app version alone describes compatibility.

**Compatibility Decision** *(2026-06-19)*:
The server's current ruling for a **Client Contract**: proceed, proceed with degraded
capabilities, server too old, client too old, or server installation inconsistent. Degraded
operation is allowed only for features the server can prove remain safe under the submitted
Client Contract; if safety is unclear, the server stops the client instead.
_Avoid_: one-time startup-only compatibility, letting upgraded servers fail old clients through
ordinary GraphQL or playback errors.

**Plugin**:
A platform extension for optional **domain**, external-service, or companion-client behavior.
The umbrella for three species — **Domain Plugin**, **Integration Plugin**, and
**Companion Plugin**. The Core Platform has no knowledge of any domain or external service;
shared primitives that enforce global contracts or fixed vocabularies remain Core even when
they are optional. First-party plugins ship with the server binary, activated by config; the
bundle format is data-only so third-party Domain Plugins can later be installed at runtime
without a rebuild. No plugins active = Core Platform only.
_Avoid_: "module", bare "extension", calling every optional Core feature a Plugin.

**Plugin Use-Case Parity** *(2026-07-04)*:
Beamfall's plugin-compatibility target: recurring Emby, Plex, and Jellyfin plugin jobs are
supported through Beamfall-native extension points, not by running upstream plugin APIs or
runtimes unchanged. Linear TV/DVR/tuner/EPG/channel-provider plugins are reserved future
product scope, not parity-required. Authentication-provider plugins are likewise not
parity-required: Beamfall Account auth stays non-pluggable, while local IdP integration is
reserved future enterprise/admin scope. Plugins expose controls through **Plugin Surfaces**,
**Plugin Action Descriptors**, the **Integration Host API**, and deliberately promoted `/v1/`
Developer API endpoints, not plugin-defined public routes or controllers. Scheduled,
background-task, and local-processing use cases route through Beamfall-governed jobs,
supervised workers, Workflow seams, and the resource governor, not arbitrary in-process
plugin services.
Broad Channels/external streaming-source catalogs are not parity-required; Plex/Emby/Jellyfin
migration/import compatibility is parity, while Live TV/tuner/EPG and narrow provider or
foreign-server connectors are reserved future scope that must preserve Beamfall's owned-catalog
boundary.
_Avoid_: drop-in Plex `.bundle` compatibility, Emby/Jellyfin `.NET` plugin compatibility,
arbitrary plugin HTML/JS/admin controllers.

**Domain Plugin**:
A Plugin for a content domain (movies, TV, music, home movies, …): bound to Libraries,
**declarations** — Entity Kinds, Navigation Manifest entries, Layouts, and Scrapers. Bundled
first-party builds may compose audited Hooks; installable executable behavior is always a paired
**Domain Hook Provider**, never code inside the Domain Plugin bundle.
_Avoid_: domain vocabulary outside one of these.

**Integration Plugin**:
A Plugin for an external service rather than a content domain (e.g. qBittorrent ingest): not
Library-bound, declares no Entity Kinds; runs on the server, where secrets, outbound
endpoints, job lifecycle, status, and audited mutations are governed. The **Manager** and
**Console** consume the server plugin's declared capabilities and status; an Integration
Plugin may also have a Mac **Companion Plugin** for richer Manager UI, but the integration
runtime itself is server-side. First-party Integration Plugins may ship in-process with the
server; third-party Integration Plugins run as supervised **out-of-process server workers**
behind a narrow host API so permissions, crashes, resources, logs, secrets, and versioning
remain bounded. File relocation only through audited server mutations.
_Avoid_: baking service integrations into the Core Platform or a client base binary,
running the integration runtime inside the Manager.

**Domain Hook Provider** *(2026-08-23)*:
A supervised **Integration Plugin** paired with a data-only **Domain Plugin** to implement narrow
Core-shaped filename parsing, match scoring, local metadata extraction, import transformation, or
scraper-authentication hooks. It returns provenance-bearing proposals; Core owns identity,
conflicts, idempotency, persistence, and acceptance.
_Avoid_: executable Domain Plugin, arbitrary hook API, plugin-owned identity or import commits.

**Provider Group** *(2026-08-23)*:
An ordered set of enabled, health-tracked **Plugins** implementing the same Core-owned capability
contract. Membership never makes unrelated Plugins substitutable, and each capability retains its
own eligibility, error, commitment, retry, and failover semantics.
_Avoid_: global Plugin order, universal failover state machine, cross-capability substitution.

**Transcode Provider** *(2026-08-23)*:
An **Integration Plugin** that executes Core-shaped media transforms for playback. Core remains
the authority for playback policy, admission, sessions, timelines, authorization, serving, provider
selection, and acceptance of provider output.
_Avoid_: Transcoder Plugin, plugin-owned playback sessions, treating a provider as playback policy.

**External Library Provider** *(2026-08-23)*:
A read-only **Integration Plugin** that presents operator-owned remote or virtual media bytes for
Core indexing and bounded reads. Core owns Media/File identity, reconciliation, authorization, and
publication; provider locators are never identity, and provider-backed bytes gain custody or offline
durability only by import into Core-controlled storage.
_Avoid_: Media Source Provider (collides with **Source**), remote-catalog Plugin, writable Library
backend, treating provider availability as custodial durability.

**Intelligence Provider** *(2026-08-23)*:
An **Integration Plugin** implementing one or more independent Core-shaped model capabilities:
embedding, transcription, classification/tagging, or recommendation proposals. Core owns retrieval,
Reveal filtering, provenance, consent, acceptance, and publication; provider output is always a
proposal until Core validates it.
_Avoid_: universal AI Provider, plugin-owned search index, autonomous recommendation authority,
default-on cloud inference.

**Secret Custody Provider** *(2026-08-23)*:
An operator-selected **Integration Plugin** that stores only explicitly delegated, Core-namespaced
Plugin/service credentials, provider API keys, or transport tokens. Core owns references, access,
redaction, migration, rotation, and lifecycle; identity/session, Stream Token, TLS, Library-recovery,
and release/update roots are never delegated to an installable Plugin.
_Avoid_: Auth Provider, plugin-owned secret namespace, silent plaintext fallback, treating custody as
authorization authority.

**Export Destination Provider** *(2026-08-23)*:
An **Integration Plugin** that transfers a Core-authorized, filtered, redacted, immutable export
artifact through a short-lived read-only capability. Core owns export scope, format, staging,
digests, expiry, consent, retry authority, and the durable receipt; the provider cannot query a
Library, construct or widen the export, or revoke a delivered external copy.
_Avoid_: Export Provider, plugin-defined export schema, destination-driven redaction, treating a
delivery receipt as revocation authority.

**Observability Sink Provider** *(2026-08-23)*:
An operator-selected **Integration Plugin** that transmits only versioned, Core-shaped, bounded
metric batches after explicit opt-in. Core owns instrumentation, semantics, redaction, cardinality,
buffering, batching, endpoint policy, and egress; a sink cannot enable collection, add attributes,
define public metrics, or read the local diagnostic ring.
_Avoid_: Telemetry Plugin, plugin-defined traces or labels, ambient exporter, treating OTLP as a
Beamfall-hosted collector.

**Integration Plugin Permission** *(2026-06-17)*:
One of the closed server permissions an out-of-process third-party **Integration Plugin**
may request: `outbound-network` (declared host allowlist), `read-catalog` (Reveal-filtered
metadata reads), `read-events` (filtered event subscription), `write-status` (plugin
health/status/dead-letter rows), `manage-secrets` (plugin-scoped secrets only),
`enqueue-jobs` (plugin-owned job kinds only), `request-ingest` (Incoming Folder / scan
handoff only), `request-file-mutation` (audited Core file mutations only; high-risk), and
`profile-link` (per-Profile OAuth/account linking). The vocabulary is Core-owned and closed;
plugins never declare new permissions or auth scopes. No permission is product-disabled by
default: if an operator chooses to grant it on their server, the server allows it. High-risk
permissions (`read-catalog`, `profile-link`, `request-ingest`, `request-file-mutation`) require
explicit approval and audit, and any update that expands permissions requires re-approval.
_Avoid_: raw DB access, arbitrary filesystem access, arbitrary GraphQL/admin calls,
plugin-declared auth scopes, hiding permission expansion inside routine updates.

**Plugin Lifecycle Action** *(2026-06-17)*:
A server-owned operation on an installed **Plugin** that a **Manager** or **Console** may
request but never performs directly: check for update, stage update, apply update, restart
worker, disable, run a plugin-declared action, retry a dead-lettered item, or test a
connection. The server verifies the request against policy, signatures or hashes, and
declared permissions; performs any filesystem or worker change itself; then writes status and
audit records. Manual/local install remains the first third-party posture, but Manager may
trigger updates from a server-staged bundle or trusted configured source.
_Avoid_: Manager mutating plugin files directly, unaudited worker restarts, plugin
self-update without server verification.

**Plugin Update Source** *(2026-06-17)*:
The explicit per-Plugin source from which the server may check, stage, and apply updates.
First-party Plugins use the Beamfall release feed and compiled-in signing trust; third-party
Plugins use an operator-configured URL or local folder plus either a pinned signing key or a
pinned artifact hash. Remote checks are default-off/operator-initiated and send no server,
Library, Profile, Plugin-state, or **Client Contract** data. The server refuses an update when
the Plugin id changes, requested permissions expand without explicit approval, signature or
hash verification fails, or the worker binary is incompatible with this server/platform. This
is not a marketplace or global catalog.
_Avoid_: automatic remote install, unpinned third-party update feeds, permission expansion
hidden inside a routine update.

**Plugin Trust State** *(2026-07-10; ADR-0196 §1)*:
The server-assessed verification standing of an installed **Plugin** artifact, computed at load
from its signatures against release-bundled trust roots and the opportunistically fetched
revocation list. It is never a gate on loading: a **Plugin** always loads (sideloaded and
user-modified builds included); Trust State only withholds trusted standing, drives advisory
surfacing, and governs support eligibility. Values are mutually exclusive, resolved by the
precedence **Tampered > Revoked > Unrecognized > Unsigned > Trusted** — integrity is assessed
before key attribution, since an invalid signature makes the artifact's own claimed author key
untrustworthy:
- **Trusted** — author signature and registry countersignature both verify against a bundled
  trust root (or first-party compiled-in trust) and the author key is not revoked.
- **Tampered** — a signature is present but does not cryptographically verify (the artifact was
  modified after signing, or a countersignature is forged).
- **Revoked** — signatures verify, but the author key is on the revocation list.
- **Unrecognized** — the countersignature verifies structurally but no bundled trust root
  recognizes the signing registry key or checks-version (typically a server too old to know a
  newer registry key; resolved by updating the server).
- **Unsigned** — the artifact carries no signatures (a sideloaded, self-compiled, or development
  build).
Only **Trusted** carries Beamfall support; every other state voids it. Trust State measures
**integrity, not authorship**: a Beamfall-authored plugin and a third-party plugin that merely
passed registry conformance are both **Trusted** — the authorship distinction is **Plugin
Provenance**, an orthogonal axis. Distinct too from **Plugin Update Source** (where/whether an
update is fetched and applied) — Trust State is the standing of whatever artifact is already loaded.
_Avoid_: treating Trust State as a load gate or kill switch; collapsing **Tampered** into
**Unsigned** (evidence of foul play vs. an expected self-build); reusing "Untrusted" as a value
(it names only the class of the four non-Trusted states); folding authorship (**Plugin
Provenance**) into Trust State.

**Plugin Provenance** *(2026-07-10; ADR-0196 §3)*:
The vouching authority behind an installed **Plugin**'s code, established by the server from which
key and trust root authenticated the artifact — orthogonal to **Plugin Trust State** (which
measures integrity, not authorship) and to **Plugin Update Source** (the distribution channel).
Values:
- **First-Party** — authored and reviewed by Beamfall, authenticated by a Beamfall key/root.
  Provenance stays First-Party even when the plugin is later distributed through the registry
  (ADR-0164), because Beamfall still vouches for the code.
- **Third-Party** — authenticated only by the registry conformance countersignature. The registry
  attests mechanical conformance (`sdktest`, boundary manifests, malware/legality bar), never that
  the code's behavior was semantically reviewed.
- **Sideloaded** — no registry authority vouches (self-signed or unsigned).
Provenance is surfaced beside Trust State wherever permissions or outbound access are granted, so a
conformance-passing **Third-Party** plugin is never mistaken for Beamfall-reviewed code. It is a
derived signal, not a stored plugin class, and is never coupled to capability — outbound
eligibility, uninstallability, and update channel derive independently.
_Avoid_: treating First-Party as a persisted class or a capability switch; coupling outbound or
uninstall rights to Provenance; implying **Third-Party** or **Sideloaded** means unsafe (they are
lower-review, not malware).

**Plugin Surface** *(2026-06-17)*:
The server-declared control/status contract a **Plugin** exposes to **Manager** and
**Console** clients: config schema, status fields, **Plugin Action Descriptors**, permission
prompts, update state, and optional **Companion Plugin** handles. Clients render the surface
with native controls, subject to **Plugin Control Policy**, Device Role, Profile, and the
**App Store binary firewall**. Applies to both plugin species: a **Domain Plugin** Surface
covers Library-binding defaults, scraper config, identification status, imported-metadata
health, and Companion Plugin handles; an **Integration Plugin** Surface covers connection
config, permissions, worker status, actions, update state, dead-letter queues, and outbound
endpoint visibility. A Plugin Surface is not downloaded UI code.
_Avoid_: using "surface" ambiguously for UI code, App Store binary visibility, and plugin
control/status contracts.

**Plugin Action Descriptor** *(2026-06-17)*:
A server-declared description of one **Plugin Lifecycle Action** or plugin-specific command
that the **Manager**/**Console** renders with native controls: stable action id, localized
label, icon, risk level, required **Integration Plugin Permission**, input schema,
confirmation copy, and result/status schema. It lets the Manager dogfood server-side
Integration Plugins without hosting arbitrary plugin UI. A **Companion Plugin** may
replace or augment the descriptor-rendered surface for first-party or otherwise trusted
native-rich cases. Official Manager clients may access and operate these surfaces by default,
subject to server role checks and plugin permissions; the server can switch Manager plugin
control off entirely or require Console-only approval for high-risk permission changes.
An action is automation-safe only when its versioned descriptor also declares idempotency,
retry/terminal-failure, time/resource, commitment, and disable/uninstall semantics that Core can
enforce. Otherwise it remains manual-only. Plugins never add automation events or rule operators.
_Avoid_: downloaded JavaScript/HTML/views in the Manager, open-ended UI APIs, plugin actions
without risk/permission/confirmation metadata.

**Plugin Control Policy** *(2026-06-17)*:
The server setting that controls how much **Manager** clients may operate installed
**Plugins**. Modes: `manager-enabled` (default; official Manager clients may operate plugin
surfaces subject to Device Role, Profile, and plugin permissions), `console-approval-required`
(Manager may view status and run low-risk actions, but high-risk permission grants and
updates require Console approval), and `console-only` (plugin lifecycle and actions are
operated only from the Console). App Store binaries still obey the **App Store binary
firewall** regardless of this policy.
_Avoid_: treating Manager access as unconditional, using this policy to bypass ADR-0050.

**Integration Worker Artifact** *(2026-06-17)*:
The native executable shipped by a third-party **Integration Plugin** for one declared server
target triple (OS/architecture). The server runs only an artifact whose target matches the
host, whose binary hash or signature verifies, whose host-API version range is compatible,
and whose requested **Integration Plugin Permissions** have been approved. Native workers are
the first runtime because real integrations need ordinary network/OAuth libraries and
occasionally local CLIs or SDKs; a portable sandbox such as WASM remains a future option, not
the first contract.
_Avoid_: Go `plugin` ABI loading, running an artifact for the wrong target, treating WASM as
required before third-party Integration Plugins can exist.

**Integration Host API** *(2026-06-17)*:
The dedicated local API the server exposes to a supervised **Integration Worker Artifact**.
The server launches the worker with a short-lived local credential and binds the API to a
local-only transport (Unix socket, named pipe, or loopback-only RPC). The API exposes only
approved capability methods implied by the worker's **Integration Plugin Permissions**:
Reveal-filtered catalog reads, filtered event subscription, plugin-scoped secret access,
status/dead-letter writes, plugin job/checkpoint operations, and audited Core mutation
requests. Workers never receive public admin tokens, direct GraphQL access, or database
handles.
_Avoid_: using the public GraphQL API as the plugin host API, sharing admin tokens with
workers, bypassing method-level permission checks.

**Enrichment Plugin**:
The first-party **Integration Plugin** implementing Beamfall's default **Intelligence Provider**
capabilities: transcription, embeddings for semantic search, classification/tagging, and
recommendation proposals. Core owns indexes, retrieval, Reveal filtering, engagement seeds,
provenance, and acceptance. Local-first by default; any cloud inference path exists only where an
ADR explicitly allows it and must be opt-in, scoped, disclosed, and reveal-safe. Optional like any
Plugin — weak hardware leaves it off. The headline post-launch differentiator (decided 2026-06-10).
_Avoid_: an "AI service" outside the plugin system, default-on cloud inference, any AI surface that
bypasses the Reveal contract.

**Companion Plugin**:
A loadable client-side plugin bundle that contributes domain-specific UI to the **Manager**
(macOS) against fixed **Extension Points**. Keyed to a server **Plugin** id — it activates only
when the connected server has that plugin active. v1 is first-party only (same-Team-ID
signature required to load). The platform-wide firewall: domain code and vocabulary
exist *only* in plugins — server Plugin or Companion Plugin — never in any base binary.
_Avoid_: "client plugin" (ambiguous with rendering server plugins), compiled-in domain modules.

**Extension Point**:
The fixed, rarely-grown vocabulary of Manager surfaces a Companion Plugin may fill — e.g.
sidecar-track editor, detail-screen panel, player overlay, identification-review enhancer.
Added rarely, in app releases; plugins fill them freely — the client-side mirror of the
"unbounded data against small fixed vocabularies" discipline. An Extension Point is filled by
the **base Manager for Core types** and *augmented* by Companion Plugins for **domain types**:
the `sidecar-track editor` is the base Manager's caption (Core sidecar) editor, which a
**Companion Plugin** may augment with an editor for its own sidecar type — the same slot, different fillers. (Curation of **Markers** is *not* an Extension
Point — Markers are not Sidecar Tracks; the marker/skip-segment editor is a plain base-Manager
core-curation surface.)
_Avoid_: open-ended "here's the app object" plugin APIs, treating an Extension Point as
plugin-only (the base Manager fills it for Core types), routing Marker editing through the
sidecar-track editor (Markers are DB timestamps, not companion files).

**Scraper**:
A Plugin's declarative fetch-and-map definition against an external metadata source: URL
templates plus mappings from response paths to Entity fields, relations, and artwork slots.
Data, not code — the platform executes it. Outbound access is plugin-declared and visible to
the operator; it is no longer constrained by the predecessor's no-disclosure privacy posture
(deliberately dropped 2026-06-10).
Search templates may interpolate **core fingerprint variables** (`{phash}`, `{oshash}`,
`{duration}`) alongside parsed-filename variables (2026-06-11) — fingerprint-driven
identification is a declarative capability, not a hook side-channel.
_Avoid_: "agent", "downloader" (artwork download is part of scraping).

**External ID**:
A core `{source, id}` identifier set on Media and Entities (a TMDB id is the typical
shape). Written by the apply stage of
**Identification**; powers pinning, re-identification, entity dedupe, and multi-source
linking. Core schema, never a plugin field (2026-06-11).
_Avoid_: source-specific id fields in core code, plugin-private identifier fields.

**Navigation Manifest**:
The server-declared **allowlist** tree of navigation entries and browse dimensions that clients
render, composed from the Core Platform plus active Plugins. Entries are typed by **View Type**;
clients silently ignore unknown types. Authored by the server; consumed read-only by clients on
connect.
_Avoid_: "app config", "feature flags".

**Section**:
A compiled rendering primitive the client binary ships with: **hero**, **shelf**, **grid**,
**list**, **info**, **filter-bar**, **motion-preview**. Named by rendering shape, never semantic domain;
parameterized by card style, Artwork Slot, label, and data source. The set is fixed at compile
time — clients never download rendering logic, and silently ignore unknown section types.
Plugins compose freely; sections are added rarely.
_Avoid_: "component" (implies runtime code), "View Type" (the earlier ADR-0001 term — absorbed
by Sections: a screen is now a Layout of Sections, not a monolithic typed view).

**Layout**:
A declarative, ordered composition of **Sections** with parameters, attached to a Navigation
Manifest node by a Plugin. The unit of plugin-controlled look and feel: same Section
primitives, different compositions per domain. Data, not code.
_Avoid_: "template" (implies whole-screen rigidity), "theme" (a Layout is structure, not
styling).

**Motion Preview Surface** *(2026-06-16)*:
A specialized client browse surface that may show a bounded set of muted, looping **preview
clips** at once. It is not full **Media** playback, not a general Section replacement, and not
plugin-supplied rendering code.
_Avoid_: "autoplay wall" (sounds unbounded), "multi-player" (implies playback sessions), full
Media streams in browse.

**Source**:
The bounded, declarative vocabulary a Section uses to fetch its data: `children`,
`entities(kind, sort, filter)`, `media(filter, sort)`, `markers(filter)` (2026-06-11),
`recently-added`, `continue-watching`, `related(relation)`, `pinned(ids)`. Filters are declarative structures (field/relation/sort),
never query strings; tag/entity predicates accept an **include-descendants** modifier;
predicates may reference plugin-declared Entity fields, engagement projections (play/o
counts), and relation counts, and may impose **one level** of related-entity conditions
("has related artist where favorite") — never arbitrary nesting (2026-06-11). The
server validates every Source at plugin load. The same vocabulary
backs user-built smart collections.
_Avoid_: raw query languages in Layouts (SQL/GraphQL documents as section parameters).

**Theme Token**:
The tiny styling vocabulary a Plugin/Library may set: **accent color** and **Library
icon/wordmark**. Everything else — typography, spacing, focus effects, motion — is owned by
the client, uniform across plugins. Structure is the plugin's; style is the client's.
_Avoid_: plugin-controlled fonts/spacing/focus behavior.

**Core Platform**:
The domain-agnostic base the server exposes with no Plugin active: Folder navigation, Media
playback, Tags, Markers, Collections, filter/search. What App Store reviewers see.
_Avoid_: "base app", "default mode".

**Identification**:
The pipeline that turns scanned files into organized, enriched library content: **parse**
(filename/path → structured guess) → **identify** (Scraper search → candidate match) →
**apply** (create Entities/relations, fill Artwork Slots). Above a per-plugin confidence
threshold, apply is automatic on scan; below it, items land in the **Unmatched queue** for
search-and-pick. Application is additive and never touches files. A user correction ("fix
match") **pins** the match against rescans. Movies/TV default to auto; home movies
default to the queue.
_Avoid_: "scraping" for the whole pipeline (a Scraper is one stage's tool).

**Incoming Folder**:
The per-Library staging folder where newly-acquired files land before Identification —
placed there by an Integration Plugin (qBittorrent ingest) or a manual drop. Inside the
Library root, so the standard scan covers it; organize relocates files out on a confident
match (a same-filesystem rename, preserving hardlinks). Download directories, by contrast,
must lie **outside** Library roots (validated at config).
_Avoid_: "landing zone"/"staging" as code identifiers, download dirs inside Library roots.

**Parse Hint**:
External metadata attached to a file entering **Identification** — e.g. a torrent release
name, an ingest category — carrying a provenance. Consumed by the *parse* stage to improve
the structured guess; a hint never overrides a **pinned** match and is never stored as
authored metadata.
_Avoid_: treating hints as curation (they're input, not truth), hints without provenance.

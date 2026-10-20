// Catalog entry definitions for the embedded Viewer/Manage web UI (surface
// `web`, internal/web/app). Intents are ground truth derived from the
// governing ADRs (0004 Profiles+Reveal, 0007 Web UI areas+Console, 0014 client
// rendering contract) and RENDERING-CONFORMANCE expectations — NOT from what a
// render happens to show. The capture harness (capture.mjs) renders each
// `captured` entry by injecting the seeded session credential and driving the
// real app to the target state. The old static-app selector drivers are disabled
// in capture.mjs until they are replaced with React route/role drivers; `listed`
// entries enumerate states without trusted React screenshots; `device-only`
// entries need a real decoder.
//
// Seeded credentials (conformance seed == development world, see
// internal/store/sqlite.go developmentSeed): device-tv secret "dev-tv-secret",
// device-mac (MANAGER) secret "dev-manager-secret", device-revoked (revoked
// viewer); sessions session-default (adult locked), session-revealed (adult
// revealed), session-manager (manager), session-managed (managed profile),
// session-revoked. Adult reveal PIN is "1234".

// Named sessions → cookie credential triples (auth.go ScopeFromRequest cookie path).
export const SESSIONS = {
  "adult-locked": { session: "session-default", device: "device-tv", secret: "dev-tv-secret" },
  "adult-revealed": { session: "session-revealed", device: "device-tv", secret: "dev-tv-secret" },
  manager: { session: "session-manager", device: "device-mac", secret: "dev-manager-secret" },
  managed: { session: "session-managed", device: "device-tv", secret: "dev-tv-secret" },
  revoked: { session: "session-revoked", device: "device-revoked", secret: "device-revoked-secret" },
  none: null, // signed-out: no cookies injected
};

// Viewports. `desktop` is the primary; `mobile` is the responsive small width.
export const VIEWPORTS = {
  desktop: { name: "desktop", w: 1280, h: 900 },
  mobile: { name: "mobile", w: 390, h: 844 },
};

// Each entry: { id, screen, state, features, viewport, session, intent, drive,
// status, note, a11yVariants }. `drive` is the name of a driver step in capture.mjs
// (the real UI interaction that reaches the state). `status` defaults to "captured".
// `a11yVariants` declares which accessibility variants are required for release per
// ADR-0023 §9. An empty array (or absent) means default capture only (no a11y variant
// baselines required for this entry). The capture harness copies this field into
// manifest.json under `a11y_variants` so the release-gate tooling can enumerate gaps.
export const ENTRIES = [
  // ── enroll / auth (ADR-0007 §2, ADR-0012 §5) ──────────────────────────────
  {
    id: "web.enroll.signed-out.desktop",
    screen: "enroll",
    state: "signed-out",
    features: ["pairing-enroll", "session-lifecycle"],
    viewport: "desktop",
    session: "none",
    adr: ["0007", "0012"],
    fixture: "conformance:unenrolled",
    drive: "enroll",
    intent:
      "An unenrolled browser sees only the enrollment view: a pairing-code field and device-name field with a submit affordance. No catalog, Profile, Manage, or Reveal affordance is present — nothing hints at restricted content. The device secret is never placed in JS-readable space (it rides HttpOnly cookies set by the server).",
  },
  {
    id: "web.enroll.signed-out.mobile",
    screen: "enroll",
    state: "signed-out",
    features: ["pairing-enroll", "session-lifecycle"],
    viewport: "mobile",
    session: "none",
    adr: ["0007", "0012"],
    fixture: "conformance:unenrolled",
    drive: "enroll",
    intent:
      "The enrollment view reflows to a narrow viewport without truncation or horizontal scroll: pairing-code and device-name fields stack, the submit affordance stays reachable. Still no catalog/Manage/Reveal affordance.",
  },
  {
    id: "web.enroll.invalid-code.desktop",
    screen: "enroll",
    state: "error",
    features: ["pairing-enroll"],
    viewport: "desktop",
    session: "none",
    adr: ["0007", "0012"],
    fixture: "conformance:bad-pairing-code",
    drive: "enrollInvalid",
    intent:
      "Submitting an invalid pairing code surfaces an inline error message in the enroll view and keeps the form available for retry. The error text reveals no server internals and the failure does not enroll the device.",
  },

  // ── browse (ADR-0007 §1, ADR-0014, ADR-0004) ──────────────────────────────
  {
    id: "web.browse.populated.desktop",
    screen: "browse",
    state: "populated",
    features: ["navigation-manifest", "layout-render", "section-sources", "artwork-slots"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0007", "0014"],
    fixture: "conformance:adult-locked",
    drive: "browse",
    // ADR-0023 §9 release a11y variants — large-text, reduce-motion, high-contrast
    a11yVariants: ["large-text", "reduce-motion", "high-contrast"],
    intent:
      "An enrolled adult session (locked) renders the Navigation Manifest as a nav plus the selected Library's Layout Sections, each hydrated from its Source. Sections render in declared order; empty/unknown-kind Sections are silently omitted (never a broken placeholder). Because the session is locked, no restricted Library content, titles, artwork, or counts appear anywhere in the DOM, and a single Reveal affordance is present in the header.",
  },
  {
    id: "web.browse.populated.mobile",
    screen: "browse",
    state: "populated",
    features: ["navigation-manifest", "layout-render", "section-sources"],
    viewport: "mobile",
    session: "adult-locked",
    adr: ["0007", "0014"],
    fixture: "conformance:adult-locked",
    drive: "browse",
    // ADR-0023 §9 release a11y variants
    a11yVariants: ["large-text", "reduce-motion", "high-contrast"],
    intent:
      "The browse Layout reflows to a narrow viewport: the nav and Sections remain usable without horizontal scroll or overlap. The same Reveal/leak invariants as the desktop populated state hold.",
  },
  {
    id: "web.browse.reveal-locked.desktop",
    screen: "browse",
    state: "reveal-locked",
    features: ["reveal-unlock", "defensive-render", "navigation-manifest"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0004", "0007", "0014"],
    fixture: "conformance:adult-locked",
    drive: "browse",
    leakProof: true,
    intent:
      "LEAK-PROOF CONTRACT: with an adult Profile session locked, the restricted Library ('Archive', IsRestricted) and every restricted Media must be entirely absent from the DOM — no restricted title, summary, artwork URL, item count, or even a hidden-state placeholder that names them. Scope is re-derived server-side, so restricted content is simply not in the manifest/Sources. A single Reveal affordance (and only that) signals a locked adult session exists.",
  },
  {
    id: "web.browse.revealed.desktop",
    screen: "browse",
    state: "revealed",
    features: ["reveal-unlock", "navigation-manifest", "layout-render"],
    viewport: "desktop",
    session: "adult-revealed",
    adr: ["0004", "0007"],
    fixture: "conformance:adult-revealed",
    drive: "browse",
    intent:
      "After a successful PIN Reveal the session is visually explicit: the iris/Revealed chip and a Relock control appear in the header, the body carries the revealed keyline, and the Reveal affordance is gone. Restricted Libraries and Media now appear in the nav and Sections. Relock is always one click away.",
  },
  {
    id: "web.browse.managed.desktop",
    screen: "browse",
    state: "profile-managed",
    features: ["profile-switch", "defensive-render"],
    viewport: "desktop",
    session: "managed",
    adr: ["0004", "0007"],
    fixture: "conformance:managed",
    drive: "browse",
    leakProof: true,
    intent:
      "A managed Profile session has NO reveal path: no Reveal affordance, chip, or any hint that restricted content exists is present in the DOM (ADR-0004). Only the managed Profile's permitted Libraries render. The header offers profile switching but never a Restrictions control.",
  },
  {
    id: "web.browse.connection-lost.desktop",
    screen: "browse",
    state: "error",
    features: ["defensive-render", "navigation-manifest"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0007", "0014"],
    fixture: "conformance:adult-locked",
    drive: "browseConnectionLost",
    intent:
      "When the manifest/Source fetch fails after a prior good render, a non-destructive connection-lost banner appears while the last good content remains; the app degrades honestly (no fabricated content) and recovers on reconnect. No restricted content leaks via the error path.",
  },

  // ── profile switcher (ADR-0004) ───────────────────────────────────────────
  {
    id: "web.profile.switcher-open.desktop",
    screen: "profile",
    state: "profile-switcher-open",
    features: ["profile-switch"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0004"],
    fixture: "conformance:adult-locked",
    drive: "profileSwitcher",
    intent:
      "The Profile switcher lists the session's selectable Profiles as cards. It names Profiles only — no restricted Library content or counts — and a cancel affordance returns to browse. Switching re-derives all scope server-side.",
  },

  // ── reveal PIN entry (ADR-0004) ───────────────────────────────────────────
  {
    id: "web.reveal.pin-entry.desktop",
    screen: "restrictions",
    state: "reveal-pin-entry",
    features: ["reveal-unlock"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0004"],
    fixture: "conformance:adult-locked",
    drive: "revealPinEntry",
    leakProof: true,
    intent:
      "The Reveal PIN-entry view shows a masked numeric PIN field and submit/cancel affordances only. It names no restricted content and leaks nothing about what Reveal would expose; the PIN is server-verified (the client never holds the verifier).",
  },
  {
    id: "web.reveal.pin-denied.desktop",
    screen: "restrictions",
    state: "reveal-throttled",
    features: ["reveal-unlock"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0004"],
    fixture: "conformance:bad-pin",
    drive: "revealPinDenied",
    leakProof: true,
    intent:
      "A wrong PIN surfaces an inline error in the PIN-entry view and keeps the session locked; no restricted content is revealed and the error text discloses nothing about the correct PIN or restricted catalog.",
  },

  // ── entity detail (ADR-0014, features entity-detail) ──────────────────────
  {
    id: "web.entity-detail.populated.desktop",
    screen: "entity-detail",
    state: "populated",
    features: ["entity-detail", "section-sources", "artwork-slots"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0007", "0014"],
    fixture: "conformance:adult-locked",
    drive: "entityDetail",
    status: "listed",
    note: "The conformance/development seed plants flat Media with no Entity rows (Kinds), so no entity-detail nav target renders. Capturable once the seed includes a plugin Entity.",
    intent:
      "An entity-detail view renders the Entity's display fields, its artwork, and its child Sections (e.g. the Media belonging to it) per the defensive Layout rules. Restricted children obey the same reveal filtering as browse.",
  },

  // ── see-all paging (features see-all-paging) ──────────────────────────────
  {
    id: "web.see-all.populated.desktop",
    screen: "see-all",
    state: "populated",
    features: ["see-all-paging", "section-sources"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0007", "0014"],
    fixture: "conformance:adult-locked",
    drive: "seeAll",
    status: "listed",
    note: "See-All requires a Section with more items than its inline cap plus a 'see all' affordance; the conformance seed's single non-restricted Media does not overflow any Section. Capturable with a larger seeded Library.",
    intent:
      "The See-All view renders the full paged item grid for one Section with a load-more affordance; it carries the same reveal filtering as the originating Section and never exposes restricted items the session may not see.",
  },

  // ── search (features search-keyword) ──────────────────────────────────────
  {
    id: "web.search.results.desktop",
    screen: "search",
    state: "populated",
    features: ["search-keyword"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0007"],
    fixture: "conformance:adult-locked",
    drive: null,
    status: "listed",
    note: "Search lives in the React Viewer app, but the catalog does not yet drive that route against a searchable conformance fixture.",
    intent:
      "Keyword search returns reveal-filtered Media matching the query; restricted results never appear for a locked or managed session. An empty query state and a no-results state are distinct and both non-leaking.",
  },
  {
    id: "web.search.empty.desktop",
    screen: "search",
    state: "search-empty",
    features: ["search-keyword", "defensive-render"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0007"],
    fixture: "conformance:adult-locked",
    drive: null,
    status: "listed",
    note: "Search is not yet captured by this catalog driver (see web.search.results.desktop).",
    intent:
      "A search yielding no results shows an explicit empty-results state (not a broken/blank page) and reveals nothing about restricted content that might have matched.",
  },

  // ── manage area (ADR-0007 §1 Manage, §3 promotion) ────────────────────────
  {
    id: "web.manage.libraries.desktop",
    screen: "manage.libraries",
    state: "populated",
    features: ["entity-manager", "job-management", "trash-review", "api-keys"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "conformance:manager",
    drive: "manage",
    // ADR-0023 §9 release a11y variants — large-text for Manager (primary power-user surface)
    a11yVariants: ["large-text", "reduce-motion", "high-contrast"],
    intent:
      "A MANAGER-role browser opens the Manage register (the Viewer nav gives way to a Manage crumb plus the visible Device-Role chip — role is visible state, never a hidden mode). It exposes the Manage sections that exist in the embedded app: Libraries (create + per-Library controls), Jobs, Schedules, Trash, Subscriptions, Pairing codes, and API tokens. This is the Manager surface, distinct from the operator Console. Per the manage-folder-tree mockup (beamfall-design-system/mockups/web-app/manage-folder-tree.html), the per-Library folder picker renders the bounded `folderTree` as flat `.node.depth-N` treeitem rows — a leading glyph (▸ folder / · file), the name, and a right-aligned `.meta` (child count for folders, byte size for files); files read muted. The selected target is a labeled `.selected` readout row carrying the path plus a Folder/File kind chip, not bare text.",
  },
  {
    id: "web.manage.libraries.mobile",
    screen: "manage.libraries",
    state: "populated",
    features: ["entity-manager", "job-management"],
    viewport: "mobile",
    session: "manager",
    adr: ["0007"],
    fixture: "conformance:manager",
    drive: "manage",
    intent:
      "The Manage register reflows to a narrow viewport: the Manage crumb + Device-Role chip stay visible and the Manage sections stack without horizontal scroll or control overlap.",
  },
  {
    id: "web.manage.tags.desktop",
    screen: "manage.tags",
    state: "populated",
    features: ["tag-manager", "tag-assignment", "trash-review"],
    viewport: "desktop",
    session: "manager",
    adr: ["0061", "0005"],
    fixture: "conformance:manager",
    drive: null,
    status: "listed",
    note: "The Tag manager lives in the React Manage app (internal/web/app/src/areas/manage/pages/TagsPage.tsx), but the catalog does not yet drive that route; this entry binds the screen's intent to its governing mockup until the React surface is captured. Mockup: beamfall-design-system/mockups/web-app/manage-tags.html.",
    intent:
      "The Tag manager (mockup mockups/web-app/manage-tags.html) renders four panels for first-class, domain-agnostic Core Tags. The hierarchy panel shows tags as a depth-indented tree, each node trailing an inline assignment-count badge, with a Rename / Move / Delete row-action cluster and a panel-head total-count chip; the panel body carries the create-tag control row (name + parent). The single-assign panel attaches/detaches Tags on one Media/Entity/Marker target, assigned tags shown as removable ×-pills. The bulk panel applies Add/Remove/Replace (a segmented mode selector in the panel head) across many targets, the selected Tags shown as ×-pills with an add-picker. The restore panel lists soft-deleted Tags with per-row Restore (HARD invariant #6 — delete is recoverable via trash). Every label is domain-agnostic (HARD invariant #1) and every mutation routes through the manager-gated GraphQL; the server is the authority.",
  },
  {
    id: "web.promote.code-entry.desktop",
    screen: "promote",
    state: "admin-session-prompt",
    features: ["device-promotion", "admin-session"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0007"],
    fixture: "conformance:adult-locked",
    drive: "promote",
    intent:
      "The browser→MANAGER promotion view shows a manager pairing-code field plus submit/cancel. Promotion is deliberate and per-device; redeeming a manager code changes the role server-side (cookies untouched) so a re-init picks up the new role everywhere.",
  },

  // ── console / admin area (ADR-0007 §4, ADR-0040 api-keys, ADR-0012 activity) ─
  {
    id: "web.console.locked.desktop",
    screen: "console.locked",
    state: "admin-locked",
    features: ["admin-session"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "conformance:manager",
    drive: "manage",
    intent:
      "A MANAGER browser sees the Console section inside Manage, but every Console operation stays gated behind the Admin Session: only the admin-password unlock form is present. The API-keys and Activity screens are absent from the DOM until the admin password is verified — admin is a re-auth tier orthogonal to the manager Device Role (ADR-0007 §4). A managed or non-manager session has no Console section at all.",
  },
  {
    id: "web.console.unlocked.desktop",
    screen: "console.unlocked",
    state: "admin-active",
    features: ["admin-session", "api-keys", "activity-log"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007", "0040", "0012"],
    fixture: "conformance:manager",
    drive: "console",
    intent:
      "After the admin password verifies, the Admin Session is active (visible state, never a hidden mode): the unlock form yields to a Lock-Console affordance and the admin-gated Console screens appear — scoped API keys (create with the five-scope vocabulary, list metadata only with no secret material, revoke) and the append-only, reveal-filtered Activity log. The full key secret is shown exactly once at creation and never folds back into the list.",
  },

  // ── player chrome (ADR-0007 transport spec, ADR-0014). The transport chrome
  //    is capturable without a decoded frame; a live decoded frame is device-
  //    only (no real video bytes in the conformance corpus). ────────────────
  {
    id: "web.player.transport-overlay.desktop",
    screen: "player",
    state: "transport-overlay",
    features: ["transport-ui", "playback-info", "canonical-timeline"],
    viewport: "desktop",
    session: "adult-revealed",
    adr: ["0007", "0010", "0014"],
    fixture: "conformance:revealed-adult",
    drive: "player",
    // ADR-0023 §9 release a11y variants — reduce-motion matters for animation, high-contrast for controls
    a11yVariants: ["reduce-motion", "high-contrast"],
    intent:
      "The player view presents the unified custom transport overlay: a scrubber with elapsed/remaining times, play/pause + seek controls, and the audio/subtitle/quality/info menu buttons. The transport is keyboard-operable and chrome is capturable even without a decoded frame.",
  },
  {
    id: "web.player.audio-menu.desktop",
    screen: "player",
    state: "audio-menu",
    features: ["audio-track-select", "transport-ui"],
    viewport: "desktop",
    session: "adult-revealed",
    adr: ["0007"],
    fixture: "conformance:revealed-adult",
    drive: "playerAudioMenu",
    intent:
      "Opening the audio-track menu shows a listbox of available audio tracks with the active one indicated; the menu has correct ARIA (role=listbox, aria-expanded on its button) and is keyboard-navigable.",
  },
  {
    id: "web.player.subtitle-menu.desktop",
    screen: "player",
    state: "subtitle-menu",
    features: ["subtitle-tracks", "transport-ui"],
    viewport: "desktop",
    session: "adult-revealed",
    adr: ["0007"],
    fixture: "conformance:revealed-adult",
    drive: "playerSubtitleMenu",
    intent:
      "The subtitle menu lists available subtitle tracks plus an off option, active track indicated, correct listbox ARIA; a subtitle-style affordance is present where supported.",
  },
  {
    id: "web.player.quality-menu.desktop",
    screen: "player",
    state: "quality-menu",
    features: ["transport-ui", "hls-transcode"],
    viewport: "desktop",
    session: "adult-revealed",
    adr: ["0007", "0010"],
    fixture: "conformance:revealed-adult",
    drive: "playerQualityMenu",
    intent:
      "The quality menu lists the honest set of available renditions (Auto plus the ladder the server offers), active one indicated; selecting a manual rendition is reflected without misrepresenting an unavailable quality.",
  },
  {
    id: "web.player.info-overlay.desktop",
    screen: "player",
    state: "info-overlay",
    features: ["transport-ui", "playback-info"],
    viewport: "desktop",
    session: "adult-revealed",
    adr: ["0007", "0010"],
    fixture: "conformance:revealed-adult",
    drive: "playerInfoOverlay",
    intent:
      "The info overlay (role=dialog) shows the playback route summary — direct-play vs transcode decision, container/codec, rendition — honestly reflecting the server's playbackInfo decision, not an aspirational claim.",
  },
  {
    id: "web.player.terminal-hidden.desktop",
    screen: "player",
    state: "terminal-error",
    features: ["playback-info", "reveal-unlock", "stream-token"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0004", "0007", "0010"],
    fixture: "conformance:adult-locked-restricted-media",
    drive: "playerTerminalHidden",
    leakProof: true,
    intent:
      "Requesting playback of restricted Media from a locked session yields a reveal-safe terminal state: a generic message with a Back affordance, and NO restricted title, artwork, or detail anywhere — the terminal state cannot be used to confirm restricted content exists. Stream Tokens join the Reveal contract (bytes are gated identically).",
  },
  {
    id: "web.player.decoded-frame.desktop",
    screen: "player",
    state: "playing",
    features: ["direct-play", "transport-ui"],
    viewport: "desktop",
    session: "adult-revealed",
    adr: ["0010"],
    fixture: "conformance:revealed-adult",
    drive: null,
    status: "device-only",
    note: "The conformance corpus serves single-byte placeholder media files (0x00/0x01), not decodable video; a live decoded frame requires a real GPU/decoder. The transport chrome over the (empty) video element is captured separately by web.player.transport-overlay.desktop.",
    intent:
      "A playing decoded video frame fills the player shell behind the auto-hiding transport chrome; direct-play is used when the client's Capability Profile matches the source.",
  },

  // ── Admin / Console area (ADR-0007 §4). Built React sections remain `listed`
  //    until the catalog has route drivers and conformance fixtures for them.
  {
    id: "web.console.onboarding-wizard.desktop",
    screen: "console.onboarding",
    state: "listed",
    features: ["first-run-wizard", "admin-session"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console onboarding route is not yet captured by this catalog driver.",
    intent:
      "The first-run onboarding wizard (ADR-0007 §4) walks the operator: set admin password → create first Library → owner Profile → plugin activation. Gated by the Admin Session (server admin password), never accepted from an unenrolled client.",
  },
  {
    id: "web.console.admin-login.desktop",
    screen: "console.admin-login",
    state: "admin-login",
    features: ["admin-session"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console Admin Session prompt is not yet captured by this catalog driver.",
    intent:
      "Console operations require a short-lived Admin Session: the operator re-authenticates with the single server admin password (owner-flagged adult Profile). PINs never grant admin; the Admin tier is orthogonal to Device Role.",
  },
  {
    id: "web.console.libraries.desktop",
    screen: "console.libraries",
    state: "listed",
    features: ["console-config", "scan-pipeline"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console Libraries screen is not yet captured by this catalog driver.",
    intent:
      "Console Libraries (ADR-0007 §4): manage roots, plugin binding, the Restrictions flag, and scan triggers/schedules per Library.",
  },
  {
    id: "web.console.plugins.desktop",
    screen: "console.plugins",
    state: "populated",
    features: ["console-config"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007", "0003", "0013"],
    fixture: "conformance:manager",
    drive: null,
    status: "listed",
    note: "Built in the React Console Plugins section (internal/web/app/src/areas/console/sections/Plugins.tsx), but this catalog does not yet drive that route; this entry binds the screen's intent to its mockup until the React surface is captured. Mockup: beamfall-design-system/mockups/web-app/console-plugins.html.",
    intent:
      "Console Plugins (ADR-0007 §4 / ADR-0003 / ADR-0013, mockup web-app/console-plugins.html): activate/configure plugins and review each plugin's Core-owned permissions, runtime/declaration status, and server-declared Action Descriptors. Per the mockup, the activation list is a flat `.panel` of `.row` entries (plugin name + Active/Inactive state chip + activation switch), not a data table; each active plugin's detail panel carries its typed non-secret config (`setPluginConfig`), permission rows (high-risk tinted), status rows, and risk-tinted action controls the client never executes itself (high-risk confirm before calling Core back). Secrets stay behind the scraper-credential seam, never in the typed-config surface.",
  },
  {
    id: "web.console.identification.desktop",
    screen: "console.identification",
    state: "listed",
    features: ["identify-scrape", "console-config"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console Identification screen is not yet captured by this catalog driver.",
    intent:
      "Console Identification (ADR-0007 §4): scraper config/credentials, match thresholds, and identification-run monitoring.",
  },
  {
    id: "web.console.profiles-access.desktop",
    screen: "console.profiles-access",
    state: "listed",
    features: ["console-config", "field-locks", "access-schedules", "enforced-filters", "api-keys"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007", "0004"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console Profiles & access screen is not yet captured by this catalog driver.",
    intent:
      "Console Profiles & access (ADR-0007 §4): Profile CRUD, per-Profile Library access and policies (enforced filters, access schedules), device enroll/revoke/promote, pairing codes, and API keys.",
  },
  {
    id: "web.console.layout-editor.desktop",
    screen: "console.layout-editor",
    state: "listed",
    features: ["layout-render", "console-config"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007", "0014"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console Layout editor is not yet captured by this catalog driver.",
    intent:
      "Console Layout editor (ADR-0007 §4): per-node Section composition, per-Profile overrides, and theme-token editing.",
  },
  {
    id: "web.console.restrictions.desktop",
    screen: "console.restrictions",
    state: "listed",
    features: ["console-config", "reveal-unlock"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007", "0004"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console Restrictions screen is not yet captured by this catalog driver.",
    intent:
      "Console Restrictions (ADR-0007 §4): Library Restrictions designation and the re-lock policy that governs the Reveal contract.",
  },
  {
    id: "web.console.operations-jobs.desktop",
    screen: "console.operations.jobs",
    state: "populated",
    features: ["job-management", "console-config", "live-sessions"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007", "0013", "0040"],
    fixture: "conformance:manager",
    drive: null,
    status: "listed",
    note: "Built in the React Console Operations section (internal/web/app/src/areas/console/sections/Operations.tsx — JobsQueue + UpdateAwareness), but this catalog does not yet drive that route; this entry binds the screen's intent to its mockup until the React surface is captured. Mockup: beamfall-design-system/mockups/web-app/console-jobs-live.html.",
    intent:
      "Console Operations — Jobs (ADR-0007 §4 / ADR-0013 / ADR-0040, mockup web-app/console-jobs-live.html): the live job panel renders a connection indicator driven by the /api/events SSE lifecycle — a pulsing green dot when live, an amber blink while reconnecting (last-good rows retained, fail-soft), and a manual-refresh fallback only once retries are exhausted (the live path shows no refresh button, honest and never-silent). Jobs render as flat rows (name + progress bar + numeric + status badge), the yielded bar warn-tinted. Update awareness owns the default-off opt-in switch, a Stable/Beta segmented channel toggle, current/latest version rows, the plain-GET disclosure, and the pre-migration backup warning; enabling sends only a public-channel manifest GET (no library/account/identity).",
  },
  {
    id: "web.console.operations-live-sessions.desktop",
    screen: "console.operations.live-sessions",
    state: "listed",
    features: ["live-sessions"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console live-sessions dashboard is not yet captured by this catalog driver.",
    intent:
      "Console Operations — live sessions dashboard (ADR-0007 §4/§5): what is now playing per device/Profile, direct-play vs transcode, with kill/message-session controls and live log streaming.",
  },
  {
    id: "web.console.operations-transcode.desktop",
    screen: "console.operations.transcode",
    state: "listed",
    features: ["transcode-config", "hls-transcode"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console hardware-transcode config is not yet captured by this catalog driver.",
    intent:
      "Console Operations — hardware transcoding config (ADR-0007 §4/§5): select and tune the hardware acceleration the on-the-fly HLS transcode uses.",
  },
  {
    id: "web.console.operations-trash.desktop",
    screen: "console.operations.trash",
    state: "listed",
    features: ["trash-review"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007", "0005"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console Trash policy + restore browser is not yet captured by this catalog driver (the Manager Manage register has a simpler Trash list).",
    intent:
      "Console Operations — Trash policy + restore browser (ADR-0007 §4): retention-window policy and a browser to restore trashed items (destruction is recoverable by default).",
  },
  {
    id: "web.console.activity.desktop",
    screen: "console.activity",
    state: "listed",
    features: ["activity-log"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007", "0012"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "The React Console Activity log is not yet captured by this catalog driver.",
    intent:
      "Console Activity (ADR-0007 §4): the server-backed, reveal-filtered append-only activity log with undo for reversible operations.",
  },

  // ── My Data (ADR-0036) — built server+web, no catalog state yet ────────────
  // These entries are `listed` because capture requires a MANAGER session with
  // the My Data surface reachable in the current conformance seed; the drive step
  // is unimplemented. Tracked as TEST-GATE-1B residual.
  {
    id: "web.mydata.export.desktop",
    screen: "mydata",
    state: "export",
    features: ["my-data-export"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0036"],
    fixture: "conformance:adult-locked",
    drive: "mydata",
    intent:
      "My Data export (ADR-0036): the user-facing export trigger that kicks off a server-side export job and reveals a download link when complete. No server, library, or watch-history data is included (HARD invariant #7).",
  },
  {
    id: "web.mydata.delete-confirm.desktop",
    screen: "mydata",
    state: "delete-confirm",
    features: ["my-data-delete"],
    viewport: "desktop",
    session: "adult-locked",
    adr: ["0036"],
    fixture: "conformance:adult-locked",
    drive: "mydataDeleteConfirm",
    intent:
      "The My Data hard-DELETE confirm view presents a disclosure that the operation permanently deletes the user's engagement history (watch progress, bookmarks) and is NOT routed through trash — it is not recoverable. A deliberate separate confirmation action is required before the server executes the hard DELETE mutation.",
  },

  // ── Migration Wizard (ADR-0013) — built server+web, no catalog state yet ──
  // Capture requires an importer-capable conformance seed and a drive step that
  // navigates to the migration wizard. Listed as TEST-GATE-1B residual.
  {
    id: "web.migration-wizard.empty.desktop",
    screen: "migration-wizard",
    state: "importer-list",
    features: ["migration-wizard"],
    viewport: "desktop",
    session: "manager",
    adr: ["0013"],
    fixture: "conformance:manager",
    drive: "migrationWizardEmpty",
    intent:
      "Migration Wizard importer list (ADR-0013): the operator selects from available importers (plugins with ImporterSpec). With no importers, an empty state is shown rather than a broken list.",
  },
  {
    id: "web.migration-wizard.run-confirm.desktop",
    screen: "migration-wizard",
    state: "run-confirm",
    features: ["migration-wizard"],
    viewport: "desktop",
    session: "manager",
    adr: ["0013"],
    fixture: "n-a",
    drive: null,
    status: "listed",
    note: "Migration Wizard run-confirm has no separate UI state: runMigrationImport() fires the mutation directly on button click with no intermediate confirm view. Capturable only if a confirm dialog is added to the implementation.",
    intent:
      "Migration Wizard run-confirm (ADR-0013): before launching a migration run the operator sees a summary of the importer scope, the estimated operation, and a confirmation action.",
  },

  // ── Pairing-mint (ADR-0007 §3) — built web, no catalog state yet ─────────
  // Capture requires a MANAGER session with the pairing-code mint flow navigated
  // via the Manage register's promotion/pairing section.
  {
    id: "web.pairing-mint.viewer-code.desktop",
    screen: "pairing-mint",
    state: "viewer-code",
    features: ["device-promotion", "admin-session"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "conformance:manager",
    drive: "pairingMintViewer",
    intent:
      "Minting a viewer pairing code (ADR-0007 §3) shows a short-lived, single-use viewer enrollment code under the manager gate. The code is displayed once; no permanent storage or re-display of the secret is possible.",
  },
  {
    id: "web.pairing-mint.manager-code.desktop",
    screen: "pairing-mint",
    state: "manager-code",
    features: ["device-promotion", "admin-session"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "conformance:manager",
    drive: "pairingMintManager",
    intent:
      "Minting a manager pairing code (ADR-0007 §3) shows a short-lived, single-use manager promotion code under the manager gate. Same single-display-no-relist invariant as the viewer code.",
  },
  {
    id: "web.pairing-mint.error.desktop",
    screen: "pairing-mint",
    state: "mint-error",
    features: ["device-promotion"],
    viewport: "desktop",
    session: "manager",
    adr: ["0007"],
    fixture: "n-a",
    drive: "pairingMintError",
    intent:
      "A pairing-code mint failure shows an inline error state (server rejected the request) and keeps the mint form available for retry; no partial or stale code is displayed.",
  },
];

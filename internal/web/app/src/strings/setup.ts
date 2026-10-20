export const setupStrings = {
  en: {
    // First-run setup wizard (WEB-1, ADR-0007 §2).
    setupBrandSuffix: "· first-run setup",
    setupFreshServer: "fresh server · no admin yet",
    setupUnlockedBanner:
      "Wizard unlocked — one-time setup token verified from the server logs. The admin password is never accepted from an unenrolled browser without it.",
    setupRailToken: "Unlock with setup token",
    setupRailAdminPassword: "Admin password",
    setupRailLibrary: "First Library",
    setupRailProfile: "Owner profile",
    setupRailPlugins: "Activate plugins",
    setupRailScan: "First scan",
    setupStepCount: "Step {n} of 6",
    setupTokenTitle: "Unlock with the setup token",
    setupTokenLead:
      "Use the one-time setup token printed to the server logs. It proves this browser was opened by someone with access to the server process before an admin password exists.",
    setupToken: "Setup token",
    setupTokenInvalid: "Setup token invalid or already used.",
    setupAdminPasswordTitle: "Set the admin password",
    setupAdminPasswordLead:
      "This password gates the Console and admin-only operations. Store it somewhere safe.",
    setupAdminPassword: "Admin password",
    setupAdminPasswordConfirm: "Confirm password",
    setupAdminPasswordMismatch: "The passwords do not match.",
    setupAdminPasswordRequired: "Enter an admin password.",
    setupLibraryTitle: "Create your first Library",
    setupLibraryLead:
      "Pick what's in this folder. Films, TV Shows and Mixed all use the one Film & TV plugin — the choice tells identification the family so it never has to guess (Mixed detects per file).",
    setupLibraryType: "Library type",
    setupLibraryName: "Library name",
    setupLibraryRoot: "Root folder",
    setupLibraryRootRequired: "Enter a root folder path.",
    setupLibraryBrowse: "Browse…",
    setupLibraryPickerUse: "Use this folder",
    setupLibraryPickerLoading: "Loading folders…",
    setupLibraryPickerEmpty: "No subfolders here.",
    setupLibraryPickerError: "Couldn't read that folder.",
    setupLibraryPickerClose: "Close folder picker",
    setupTypeFilms: "Films",
    setupTypeTVShows: "TV Shows",
    setupTypeMixed: "Mixed",
    setupTypeMusic: "Music",
    setupTypePodcasts: "Podcasts",
    setupPluginFilmTV: "Film & TV",
    setupPluginMusic: "Music",
    setupPluginPodcast: "Podcast",
    setupProfileTitle: "Name the owner profile",
    setupProfileLead:
      "The owner Profile is the first Profile on this server. You can add more later.",
    setupProfileName: "Owner profile name",
    setupProfileDefault: "Owner",
    setupPluginsTitle: "Activate plugins",
    setupPluginsLead:
      "Turn on the plugins this server should run. The plugin behind your first Library is selected for you.",
    setupPluginsEmpty: "No plugins are installed.",
    setupUpdateTitle: "Update awareness",
    setupUpdateLead:
      "Beamfall can check the public release manifest for updates. This is off by default.",
    setupUpdateToggle: "Check for updates",
    setupUpdateDisclosure:
      "When enabled, the request is a plain GET with no library data, account data, cookies, body, query string, or server identity.",
    setupBack: "Back",
    setupContinue: "Continue",
    setupFinish: "Finish setup",
    setupBrowseReady: "Browse what's ready",
    setupWorking: "Working…",
    setupFailed: "Something went wrong. Please try again.",
    setupScanTitle: "First scan is running",
    setupScanLead:
      "Beamfall has started scanning your first Library. You can browse as soon as items appear; the scan keeps running in the background.",
    setupScanStatus: "Scan status: {status}",
    setupScanWaiting: "Waiting for scan progress…",
    // Seam A — inline migration offer, the final setup step (ADR-0080 /
    // onboarding-funnel §3). Source labels come from the active plugins, never
    // hardcoded here.
    setupMigrateTitle: "Bring your library with you",
    setupMigrateLead:
      "Switching from another server? Import your watch history, ratings, and collections so you pick up where you left off.",
    setupMigrateTrustAdditive:
      "Additive only — it fills gaps and never overwrites or downgrades your Beamfall progress.",
    setupMigrateTrustScope:
      "Imports watch state, ratings, and collections only — never files, metadata, or settings.",
    setupMigrateTrustNoConnection:
      "No ongoing connection is kept, and your old server is never modified.",
    setupMigrateTrustRerunnable:
      "Re-runnable — keep using the old server and re-import the difference on switch day.",
    setupMigrateTrustReportFirst:
      "Report first — you preview exactly what will import before anything is written.",
    setupMigrateSourcesLabel: "Available sources",
    setupMigrateSourcesUnavailable:
      "Could not load the available sources. You can open the importer later from the Console.",
    setupMigrateNoSources:
      "No import sources are available from your active plugins yet. You can add them later from the Console.",
    setupMigrateSkip: "Skip for now",
    setupMigrateContinue: "Choose a source",
  },
  fr: {},
  de: {},
  es: {},
  pt: {},
} as const;

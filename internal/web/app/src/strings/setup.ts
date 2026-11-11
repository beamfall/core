export const setupStrings = {
  en: {
    // First-run setup wizard (WEB-1, ADR-0007 §2).
    setupBrandSuffix: "Server setup",
    setupFreshServer: "New server",
    setupUnlockedBanner: "Server claimed — you're the owner now.",
    setupRailToken: "Unlock",
    setupRailAdminPassword: "Admin password",
    setupRailSecurity: "Sign-in security",
    setupRailLibrary: "First library",
    setupRailProfile: "Owner profile",
    setupRailPlugins: "Plugins",
    setupRailScan: "First scan",
    setupStepCount: "Step {n} of {total}",
    setupTokenTitle: "Unlock with the setup token",
    setupTokenLead:
      "Paste the one-time code from the server log. It proves you can reach the server itself before anyone sets an admin password.",
    setupToken: "Setup token",
    setupTokenInvalid: "Setup token invalid or already used.",
    setupAdminPasswordTitle: "Set the admin password",
    setupAdminPasswordLead:
      "It unlocks the Console and owner-only tools. Pick something strong and keep it in your password manager.",
    setupAdminPassword: "Admin password",
    setupAdminPasswordConfirm: "Confirm password",
    setupAdminPasswordMismatch: "The passwords do not match.",
    setupAdminPasswordRequired: "Enter an admin password.",
    setupSecurityTitle: "Choose how the owner signs in",
    setupSecurityLead: "You can change this later in the Console with the admin password.",
    setupSecurityHome: "Home",
    setupSecurityHomeDetail:
      "You can sign in as the owner from any browser with the admin password.",
    setupSecurityStrict: "Strict",
    setupSecurityStrictDetail:
      "Only paired devices can sign in; to recover, run beamfall pairing-code on the server.",
    setupSecurityRecommended: "Recommended",
    setupLibraryTitle: "Create your first Library",
    setupLibraryLead:
      "Point Beamfall at a folder and say what's inside. Films, TV Shows and Mixed all use the Film & TV plugin; Mixed works it out file by file.",
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
    setupProfileLead: "This is you. Everyone else in the house can get their own Profile later.",
    setupProfileName: "Owner profile name",
    setupProfileDefault: "Owner",
    setupPluginsTitle: "Choose what Beamfall understands",
    setupPluginsLead:
      "Each plugin teaches Beamfall a kind of media. Turn on what you have; you can change this any time.",
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
    setupScanTitle: "Your library is filling up",
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

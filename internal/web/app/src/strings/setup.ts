export const setupStrings = {
  en: {
    // First-run setup wizard (WEB-1, ADR-0007 §2).
    setupBrandSuffix: "Server setup",
    setupUnlockedBanner: "Server claimed. You're the owner now.",
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
      "Paste the one-time code from the server log. It proves you can reach the server before anyone sets an admin password.",
    setupToken: "Setup token",
    setupTokenRequired: "Paste the setup token from the server log to continue.",
    setupTokenInvalid:
      "That setup token didn't work. It may be mistyped or already used; restarting the server prints a new one in its log.",
    setupTokenAgain:
      "The setup token is cleared after each attempt. Paste it again to finish claiming the server.",
    setupNeedsSecure:
      "Setup needs HTTPS or this server's local network. Open the setup link from a device on the same network.",
    setupAdminPasswordTitle: "Set the admin password",
    setupAdminPasswordLead:
      "It unlocks the Console and owner-only tools. Use something strong and keep it in a password manager.",
    setupAdminPassword: "Admin password",
    setupAdminPasswordConfirm: "Confirm password",
    setupAdminPasswordMismatch: "The passwords don't match. Type the same password in both fields.",
    setupAdminPasswordRequired: "Enter an admin password.",
    setupSecurityTitle: "Choose how the owner signs in",
    setupSecurityLead: "You can change this later in the Console.",
    setupSecurityHome: "Home",
    setupSecurityHomeDetail: "Sign in as the owner from any browser with the admin password.",
    setupSecurityStrict: "Strict",
    setupSecurityStrictDetail:
      "Only paired devices can sign in. To recover, run beamfall pairing-code on the server.",
    setupSecurityRecommended: "Recommended",
    setupLibraryTitle: "Create your first Library",
    setupLibraryLead:
      "Pick a folder and say what's in it. Films, TV Shows and Mixed use the Film & TV plugin; Mixed sorts each file.",
    setupLibraryType: "Library type",
    setupLibraryName: "Library name",
    setupLibraryRoot: "Root folder",
    setupLibraryRootRequired: "Type the folder's path on the server, or choose it with Browse.",
    setupLibraryCreateFailed:
      "The server couldn't set up that folder: {reason}. Check the path exists and the server can read it.",
    setupLibraryScanFailed:
      "The Library is created, but its first scan didn't start: {reason}. Continue to try the scan again.",
    setupLibraryBrowse: "Browse…",
    setupLibraryPickerUse: "Use this folder",
    setupLibraryPickerLoading: "Loading folders…",
    setupLibraryPickerEmpty:
      "No subfolders here. Use this folder if your media files sit directly inside it.",
    setupLibraryPickerError:
      "Couldn't read that folder. The server may not have permission to open it; pick another folder.",
    setupLibraryPickerSeedError:
      "Couldn't open {path} on the server, so the picker starts at the top of its filesystem.",
    setupLibraryPickerClose: "Close folder picker",
    setupTypeFilms: "Films",
    setupTypeTVShows: "TV Shows",
    setupTypeMixed: "Mixed",
    setupTypeMusic: "Music",
    setupTypePodcasts: "Podcasts",
    setupProfileTitle: "Name the owner profile",
    setupProfileLead: "This is you. Everyone else can get their own Profile later.",
    setupProfileName: "Owner profile name",
    setupProfileDefault: "Owner",
    setupPluginsTitle: "Choose what Beamfall understands",
    setupPluginsLead:
      "Each plugin teaches Beamfall a kind of media. Turn on what you have; change it any time.",
    setupPluginsEmpty: "No plugins are installed.",
    setupKeysTitle: "Metadata provider keys",
    setupKeysLead:
      "Titles, details and artwork come from providers that ask for a free API key. Paste one now or add it later in Console › Plugins.",
    setupKeyOptional: "Optional: paste your API key",
    setupKeySaveFailed:
      "The {provider} key couldn't be saved. Check it, or clear the field to add it later in Console › Plugins.",
    setupUpdateTitle: "Update awareness",
    setupUpdateLead: "Check the public release manifest for new versions. Off by default.",
    setupUpdateToggle: "Check for updates",
    setupUpdateDisclosure:
      "The check is a plain GET with no library data, account data, cookies, body, query string or server identity.",
    setupBack: "Back",
    setupContinue: "Continue",
    setupFinish: "Finish setup",
    setupBrowseReady: "Browse what's ready",
    setupWorking: "Working…",
    setupFailed:
      "The server couldn't finish this step. Check the server log for the cause, then try again.",
    setupOffline:
      "Can't reach the server. Check that it's running and you're on its network, then try again.",
    setupScanTitle: "Your library is filling up",
    setupScanLead:
      "Beamfall is scanning your first Library. Browse as soon as items appear; the scan keeps going in the background.",
    setupScanStatus: "Scan status: {status}",
    setupScanWaiting: "Waiting for progress…",
    setupScanConnectionLost:
      "Lost contact with the server. Retrying; the scan keeps running on the server.",
    setupScanStopped:
      "The scan stopped before it finished. Browse what's ready, then scan this Library again from Console › Libraries.",
    // Seam A — inline migration offer, the final setup step (ADR-0080 /
    // onboarding-funnel §3). Source labels come from the active plugins, never
    // hardcoded here.
    setupMigrateTitle: "Bring your library with you",
    setupMigrateLead:
      "Switching from another server? Import your watch history, ratings, and collections so you pick up where you left off.",
    setupMigrateTrustAdditive:
      "Additive only. It fills gaps and never overwrites or downgrades your Beamfall progress.",
    setupMigrateTrustScope:
      "Imports watch state, ratings, and collections only, never files, metadata, or settings.",
    setupMigrateTrustNoConnection:
      "No ongoing connection is kept, and your old server is never modified.",
    setupMigrateTrustRerunnable:
      "Re-runnable. Keep using the old server and re-import the difference on switch day.",
    setupMigrateTrustReportFirst:
      "Report first. You preview exactly what will import before anything is written.",
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

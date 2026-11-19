import type { StringKey } from "@/strings";

// The settings search index (CORE-2445). Each entry names a control on a
// Console or Manage page by its `data-setting` anchor, so a search result can
// open the page and scroll to the control. `advanced` marks a control that only
// shows with Advanced settings on, so opening it turns Advanced on first, and
// `opener` names the control to show instead when the setting only appears
// after an action. settingsIndex.test.ts checks that every anchor here exists
// in the source.

export type SettingsArea = "console" | "manage";

export type SettingEntry = {
  id: string;
  label: StringKey;
  help?: StringKey;
  advanced?: boolean;
  /** The anchor of an always-shown control that opens or holds this setting,
   * for a setting that only appears after an action on the page: the Add
   * button of a form, a profile picker, or the list of rows that carry it. */
  opener?: string;
};

export type SettingsPage = {
  area: SettingsArea;
  /** The route under the area: "config" is /console/config. */
  path: string;
  page: StringKey;
  settings: SettingEntry[];
};

export const SETTINGS_INDEX: SettingsPage[] = [
  {
    area: "console",
    path: "operations",
    page: "consoleOperationsDestination",
    settings: [
      {
        id: "console.operations.lowPower",
        label: "governorLowPower",
        help: "governorLowPowerTip",
      },
      {
        id: "console.operations.workers",
        label: "governorBackgroundWorkers",
        help: "governorBackgroundWorkersTip",
        advanced: true,
      },
      {
        id: "console.operations.eventsRetention",
        label: "eventsRetentionDays",
        help: "eventsRetentionDaysTip",
        advanced: true,
      },
    ],
  },
  {
    area: "console",
    path: "libraries",
    page: "libraries",
    settings: [
      {
        id: "console.libraries.previewClips",
        label: "previewClips",
        help: "previewClipsTip",
        opener: "console.libraries.list",
      },
      {
        id: "console.libraries.showOnHome",
        label: "showOnHome",
        help: "showOnHomeHelp",
        opener: "console.libraries.list",
      },
      {
        id: "console.libraries.skipDetection",
        label: "skipDetectionLabel",
        help: "skipDetectionTip",
        opener: "console.libraries.list",
      },
      {
        id: "console.libraries.restricted",
        label: "libraryRestrictedLabel",
        help: "libraryRestrictedTip",
        opener: "console.libraries.list",
      },
    ],
  },
  {
    area: "console",
    path: "access",
    page: "consoleAccessGroup",
    settings: [
      {
        id: "console.access.profileType",
        label: "accessHelpProfileTypeLabel",
        help: "accessHelpProfileTypeTip",
      },
      {
        id: "console.access.pairingCodes",
        label: "accessHelpPairingCodesLabel",
        help: "accessHelpPairingCodesTip",
      },
      {
        id: "console.access.remoteEnrollment",
        label: "remoteEnrollToggle",
        help: "accessHelpRemoteEnrollTip",
        advanced: true,
      },
      {
        id: "console.access.signinSecurity",
        label: "signinSecurityHeading",
        help: "accessHelpStrictTip",
      },
      {
        id: "console.access.adminPassword",
        label: "adminPasswordLabel",
        help: "accessHelpAdminPasswordHint",
      },
    ],
  },
  {
    area: "console",
    path: "schedules",
    page: "navParentalControls",
    settings: [
      {
        id: "console.schedules.libraryAccess",
        label: "libraryAccessHeading",
        help: "accessHelpLibraryAccessHint",
        opener: "console.schedules.profile",
      },
      {
        id: "console.schedules.ratingLimit",
        label: "enforcedFiltersHeading",
        help: "accessHelpRatingLimitTip",
        opener: "console.schedules.profile",
      },
      {
        id: "console.schedules.accessSchedule",
        label: "accessHelpScheduleLabel",
        help: "accessHelpScheduleTip",
        opener: "console.schedules.profile",
      },
      {
        id: "console.schedules.streamCap",
        label: "accessHelpStreamCapLabel",
        help: "accessHelpStreamCapTip",
        advanced: true,
        opener: "console.schedules.profile",
      },
      {
        id: "console.schedules.remoteBitrateLimit",
        label: "accessHelpRemoteCapLabel",
        help: "accessHelpProfileRemoteCapTip",
        advanced: true,
        opener: "console.schedules.profile",
      },
      {
        id: "console.schedules.extraTimeAutoApprove",
        label: "accessHelpExtraTimeLabel",
        help: "accessHelpExtraTimeTip",
        advanced: true,
        opener: "console.schedules.profile",
      },
    ],
  },
  {
    area: "console",
    path: "remote",
    page: "remoteAccessHeading",
    settings: [
      {
        id: "console.remote.reachability",
        label: "reachabilityHeading",
        help: "accessHelpReachability",
      },
      {
        id: "console.remote.portMapping",
        label: "portMappingToggle",
        help: "accessHelpPortMappingTip",
      },
      {
        id: "console.remote.bitrateLimit",
        label: "accessHelpRemoteCapLabel",
        help: "accessHelpServerRemoteCapTip",
      },
      {
        id: "console.remote.relay",
        label: "watchAwayAdvancedRelay",
        help: "accessHelpRelayTip",
        advanced: true,
      },
    ],
  },
  {
    area: "console",
    path: "api",
    page: "navApiKeys",
    settings: [
      { id: "console.api.tokens", label: "apiTokens", help: "accessHelpApiTokensTip" },
      { id: "console.api.keys", label: "apiKeysHeading", help: "accessHelpApiKeysTip" },
      { id: "console.api.scopes", label: "apiKeyScopesLegend", help: "accessHelpApiScopesTip" },
    ],
  },
  {
    area: "console",
    path: "webhooks",
    page: "webhooksHeading",
    settings: [
      {
        id: "console.webhooks.publicServerUrl",
        label: "webhookPublicBaseURL",
        help: "accessHelpWebhookPublicUrlTip",
      },
      { id: "console.webhooks.events", label: "webhookEvents", opener: "console.webhooks.add" },
      {
        id: "console.webhooks.secret",
        label: "webhookSecret",
        help: "accessHelpWebhookSecretTip",
        opener: "console.webhooks.add",
      },
      {
        id: "console.webhooks.allowPrivateNetwork",
        label: "webhookAllowPrivate",
        help: "accessHelpWebhookPrivateTip",
        opener: "console.webhooks.add",
      },
    ],
  },
  {
    area: "console",
    path: "retention",
    page: "navLeavingSoonRules",
    settings: [
      {
        id: "console.retention.name",
        label: "retentionRuleNameLabel",
        opener: "console.retention.add",
      },
      {
        id: "console.retention.library",
        label: "retentionRuleLibraryLabel",
        help: "retentionRuleLibraryTip",
        opener: "console.retention.add",
      },
      {
        id: "console.retention.source",
        label: "retentionRuleSourceLabel",
        help: "retentionRuleSourceTip",
        opener: "console.retention.add",
      },
      {
        id: "console.retention.grace",
        label: "retentionRuleGraceLabel",
        help: "retentionRuleGraceTip",
        opener: "console.retention.add",
      },
    ],
  },
  {
    area: "console",
    path: "identification",
    page: "navOptionalFeatures",
    settings: [
      {
        id: "console.identification.conversion",
        label: "capabilityConversion",
        help: "capabilityConversionTip",
      },
      {
        id: "console.identification.rename",
        label: "capabilityRename",
        help: "capabilityRenameTip",
      },
      {
        id: "console.identification.share-mount",
        label: "capabilityShareMount",
        help: "capabilityShareMountTip",
      },
    ],
  },
  {
    area: "console",
    path: "migrate",
    page: "jobKindMigration",
    settings: [
      {
        id: "console.migrate.sourcePath",
        label: "accessHelpMigrateSourceLabel",
        help: "accessHelpMigrateSourceTip",
        opener: "console.migrate.importers",
      },
      {
        id: "console.migrate.mapping",
        label: "migrationWizardMapping",
        help: "accessHelpMigrateMappingDesc",
        opener: "console.migrate.importers",
      },
    ],
  },
  {
    area: "console",
    path: "config",
    page: "navPlaybackSignIn",
    settings: [
      {
        id: "console.playback.hardware",
        label: "transcodeHardwareEnabled",
        help: "transcodeHardwareEnabledTip",
      },
      {
        id: "console.playback.maxTranscodes",
        label: "transcodeMaxConcurrent",
        help: "transcodeMaxConcurrentTip",
      },
      {
        id: "console.playback.toneMap",
        label: "transcodeToneMapEnabled",
        help: "transcodeToneMapHelp",
      },
      {
        id: "console.playback.codec",
        label: "transcodeCodec",
        help: "transcodeCodecTip",
        advanced: true,
      },
      {
        id: "console.playback.quality",
        label: "transcodeQuality",
        help: "transcodeQualityTip",
        advanced: true,
      },
      {
        id: "console.playback.device",
        label: "transcodeDevice",
        help: "transcodeDeviceTip",
        advanced: true,
      },
      {
        id: "console.playback.tempDir",
        label: "transcodeTempDir",
        help: "transcodeTempDirTip",
        advanced: true,
      },
      {
        id: "console.config.pairingCodes",
        label: "viewerPairingCodesDisabled",
        help: "viewerPairingCodesDisabledHelp",
      },
      {
        id: "console.config.sessionLifetime",
        label: "sessionLifetimeLabel",
        help: "sessionLifetimeHelp",
      },
      {
        id: "console.config.adminSession",
        label: "adminSessionLengthLabel",
        help: "adminSessionLengthHelp",
      },
    ],
  },
  {
    area: "manage",
    path: "libraries",
    page: "libraries",
    settings: [
      {
        id: "manage.libraries.previewClips",
        label: "previewClips",
        help: "manageLibPreviewClipsHelp",
        opener: "manage.libraries.list",
      },
    ],
  },
  {
    area: "manage",
    path: "operations",
    page: "jobs",
    settings: [
      {
        id: "manage.operations.repeat",
        label: "manageScheduleRepeat",
        help: "manageScheduleRepeatHelp",
        opener: "manage.operations.schedules",
      },
      {
        id: "manage.operations.time",
        label: "manageScheduleTime",
        opener: "manage.operations.schedules",
      },
    ],
  },
  {
    area: "manage",
    path: "convert",
    page: "convert",
    settings: [
      {
        id: "manage.convert.preset",
        label: "convertPresetLabel",
        help: "manageConvertPresetHelp",
      },
    ],
  },
  {
    area: "manage",
    path: "rename",
    page: "rename",
    settings: [
      { id: "manage.rename.find", label: "renameFindLabel", help: "manageRenameFindHelp" },
      { id: "manage.rename.prefix", label: "renamePrefixLabel", help: "manageRenamePrefixHelp" },
      { id: "manage.rename.digits", label: "manageRenameDigits", help: "manageRenameDigitsHelp" },
    ],
  },
  {
    area: "manage",
    path: "trash",
    page: "navTrash",
    settings: [
      {
        id: "manage.trash.purgeBefore",
        label: "purgeBeforeLabel",
        help: "manageTrashPurgeBeforeHelp",
      },
    ],
  },
  {
    area: "manage",
    path: "subscriptions",
    page: "subscriptions",
    settings: [
      {
        id: "manage.subscriptions.keepDownloads",
        label: "manageSubKeepDownloads",
        help: "manageSubKeepDownloadsHelp",
        opener: "manage.subscriptions.list",
      },
      {
        id: "manage.subscriptions.downloadsToKeep",
        label: "manageSubDownloadsToKeep",
        help: "manageSubKeepDownloadsHelp",
        opener: "manage.subscriptions.list",
      },
      {
        id: "manage.subscriptions.downloadFolder",
        label: "downloadFolder",
        help: "manageSubDownloadFolderHelp",
      },
      {
        id: "manage.subscriptions.authorizationHeader",
        label: "authorizationHeader",
        help: "manageSubAuthHeaderHelp",
      },
    ],
  },
  {
    area: "manage",
    path: "bulk",
    page: "bulkOpsHeading",
    settings: [
      { id: "manage.bulk.mediaIds", label: "bulkEditMediaIDs", help: "manageBulkMediaIdsHelp" },
      { id: "manage.bulk.fieldName", label: "bulkEditFieldName", help: "manageBulkFieldNameHelp" },
      {
        id: "manage.bulk.namingTemplate",
        label: "organizeTemplate",
        help: "manageBulkTemplateHelp",
      },
    ],
  },
  {
    area: "manage",
    path: "navigation",
    page: "navOverrideHeading",
    settings: [
      {
        id: "manage.navigation.accent",
        label: "navOverrideAccentLabel",
        help: "manageNavAccentHelp",
      },
      { id: "manage.navigation.sections", label: "navOverrideSectionsHeading" },
    ],
  },
  {
    area: "manage",
    path: "restrictions",
    page: "navUnlockRestricted",
    settings: [
      // Set PIN with none set, the unlock field otherwise, Change PIN once unlocked (CORE-2463).
      {
        id: "manage.restrictions.pin",
        label: "restrictionsSetPin",
        help: "restrictionsSetupStepPin",
      },
    ],
  },
];

/** Console pages an App Store build leaves out (see console/index.tsx). */
export const APP_STORE_FIREWALL_PATHS = new Set([
  "identification",
  "layout",
  "capabilities",
  "migrate",
]);

export type SearchablePage = {
  area: SettingsArea;
  path: string;
  label: StringKey;
  lead?: StringKey;
};

// Every page in the Console and Manage menus, so a search from either menu
// finds a page in the other by name (CORE-2445). Each area's tests check this
// list against its menu, so a page added to a menu fails until it is added
// here. Console pages behind the App Store firewall are left out on that host.
export const MENU_PAGES: SearchablePage[] = [
  ...(
    [
      ["", "consoleOverviewHeading"],
      ["operations", "consoleOperationsDestination"],
      ["sessions", "liveSessionsHeading"],
      ["history", "playHistoryHeading"],
      ["health", "libraryHealthHeading"],
      ["activity", "activityLogHeading"],
      ["logs", "serverLogsHeading"],
      ["libraries", "libraries"],
      ["plugins", "consoleConfigPlugins"],
      ["access", "consoleAccessGroup"],
      ["schedules", "navParentalControls"],
      ["remote", "remoteAccessHeading"],
      ["api", "navApiKeys"],
      ["webhooks", "webhooksHeading"],
      ["backups", "backupHeading"],
      ["retention", "navLeavingSoonRules"],
      ["identification", "navOptionalFeatures"],
      ["migrate", "jobKindMigration"],
      ["updates", "navUpdateNotifications"],
      ["config", "navPlaybackSignIn"],
    ] as const
  ).map(([path, label]) => ({ area: "console" as const, path, label })),
  ...(
    [
      ["libraries", "libraries", "manageLibrariesLead"],
      ["operations", "jobs", "manageJobsLead"],
      ["convert", "convert", "convertLead"],
      ["rename", "rename", "renameLead"],
      ["trash", "navTrash", "manageTrashLead"],
      ["subscriptions", "subscriptions", "manageSubscriptionsLead"],
      ["unmatched", "unmatchedHeading", "unmatchedLead"],
      ["collections", "collections", "manageCollectionsLead"],
      ["bulk", "bulkOpsHeading", "bulkOpsLead"],
      ["tags", "tagsHeading", "tagsLead"],
      ["curation", "curationHeading", "curationLead"],
      ["entities", "manageEntitiesTitle", "manageEntitiesLead"],
      ["items", "itemMetadataHeading", "itemMetadataLead"],
      ["artwork", "artworkCandidates", "manageArtworkLead"],
      ["markers", "markersHeading", "markersLead"],
      ["field-locks", "fieldLocksHeading", "fieldLocksLead"],
      ["navigation", "navOverrideHeading", "navOverrideLead"],
      ["restrictions", "navUnlockRestricted", "restrictionsLead"],
      ["profile", "navSwitchProfile", "profilesLead"],
    ] as const
  ).map(([path, label, lead]) => ({ area: "manage" as const, path, label, lead })),
];

export type SettingsSearchResult = {
  key: string;
  area: SettingsArea;
  path: string;
  label: string;
  /** The page that holds a setting; absent for a page result. */
  page?: string;
  setting?: string;
  advanced?: boolean;
  opener?: string;
};

// Case and accent-insensitive, so "reglages" finds "réglages".
export function normalizeSearchText(text: string): string {
  return text
    .normalize("NFD")
    .replace(/\p{Diacritic}/gu, "")
    .toLowerCase();
}

// Every word of the query must appear in the label or its help. A label match
// ranks above a help-only match, and a match at the start of the label first.
export function searchSettings(
  query: string,
  t: (key: StringKey) => string,
  {
    pages = MENU_PAGES,
    appStoreHost = false,
    limit = 8,
    visiblePage = () => true,
  }: {
    pages?: SearchablePage[];
    appStoreHost?: boolean;
    limit?: number;
    visiblePage?: (area: SettingsArea, path: string) => boolean;
  } = {},
): SettingsSearchResult[] {
  const words = normalizeSearchText(query).split(/\s+/).filter(Boolean);
  if (words.length === 0) return [];

  const candidates: { result: SettingsSearchResult; label: string; help: string }[] = [];
  const hidden = (area: SettingsArea, path: string) =>
    (area === "console" && appStoreHost && APP_STORE_FIREWALL_PATHS.has(path)) ||
    !visiblePage(area, path);
  for (const page of pages) {
    if (hidden(page.area, page.path)) continue;
    candidates.push({
      result: {
        key: `${page.area}:${page.path}`,
        area: page.area,
        path: page.path,
        label: t(page.label),
      },
      label: t(page.label),
      help: page.lead ? t(page.lead) : "",
    });
  }
  for (const page of SETTINGS_INDEX) {
    if (hidden(page.area, page.path)) continue;
    for (const setting of page.settings) {
      candidates.push({
        result: {
          key: setting.id,
          area: page.area,
          path: page.path,
          label: t(setting.label),
          page: t(page.page),
          setting: setting.id,
          advanced: setting.advanced,
          opener: setting.opener,
        },
        label: t(setting.label),
        help: setting.help ? t(setting.help) : "",
      });
    }
  }

  const scored: { result: SettingsSearchResult; score: number }[] = [];
  for (const { result, label, help } of candidates) {
    const l = normalizeSearchText(label);
    const h = normalizeSearchText(help);
    if (!words.every((w) => l.includes(w) || h.includes(w))) continue;
    const score = l.startsWith(words[0]) ? 0 : words.every((w) => l.includes(w)) ? 1 : 2;
    scored.push({ result, score });
  }
  return scored
    .sort((a, b) => a.score - b.score)
    .slice(0, limit)
    .map((s) => s.result);
}

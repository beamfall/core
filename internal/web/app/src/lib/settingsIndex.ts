import type { StringKey } from "@/strings";

// The settings search index (CORE-2445). Each entry names a control on a
// Console or Manage page by its `data-setting` anchor, so a search result can
// open the page and scroll to the control. `advanced` marks a control that only
// shows with Advanced settings on, so opening it turns Advanced on first.
// settingsIndex.test.ts checks that every anchor here exists in the source.

export type SettingsArea = "console" | "manage";

export type SettingEntry = {
  id: string;
  label: StringKey;
  help?: StringKey;
  advanced?: boolean;
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
      { id: "console.libraries.previewClips", label: "previewClips", help: "previewClipsTip" },
      { id: "console.libraries.showOnHome", label: "showOnHome", help: "showOnHomeHelp" },
      {
        id: "console.libraries.skipDetection",
        label: "skipDetectionLabel",
        help: "skipDetectionTip",
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
      },
      {
        id: "console.schedules.ratingLimit",
        label: "enforcedFiltersHeading",
        help: "accessHelpRatingLimitTip",
      },
      {
        id: "console.schedules.accessSchedule",
        label: "accessHelpScheduleLabel",
        help: "accessHelpScheduleTip",
      },
      {
        id: "console.schedules.streamCap",
        label: "accessHelpStreamCapLabel",
        help: "accessHelpStreamCapTip",
        advanced: true,
      },
      {
        id: "console.schedules.remoteBitrateLimit",
        label: "accessHelpRemoteCapLabel",
        help: "accessHelpProfileRemoteCapTip",
        advanced: true,
      },
      {
        id: "console.schedules.extraTimeAutoApprove",
        label: "accessHelpExtraTimeLabel",
        help: "accessHelpExtraTimeTip",
        advanced: true,
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
      { id: "console.webhooks.events", label: "webhookEvents" },
      {
        id: "console.webhooks.secret",
        label: "webhookSecret",
        help: "accessHelpWebhookSecretTip",
      },
      {
        id: "console.webhooks.allowPrivateNetwork",
        label: "webhookAllowPrivate",
        help: "accessHelpWebhookPrivateTip",
      },
    ],
  },
  {
    area: "console",
    path: "retention",
    page: "navLeavingSoonRules",
    settings: [
      { id: "console.retention.name", label: "retentionRuleNameLabel" },
      {
        id: "console.retention.library",
        label: "retentionRuleLibraryLabel",
        help: "retentionRuleLibraryTip",
      },
      {
        id: "console.retention.source",
        label: "retentionRuleSourceLabel",
        help: "retentionRuleSourceTip",
      },
      {
        id: "console.retention.grace",
        label: "retentionRuleGraceLabel",
        help: "retentionRuleGraceTip",
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
      },
      {
        id: "console.migrate.mapping",
        label: "migrationWizardMapping",
        help: "accessHelpMigrateMappingDesc",
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
      },
      { id: "manage.operations.time", label: "manageScheduleTime" },
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
      },
      {
        id: "manage.subscriptions.downloadsToKeep",
        label: "manageSubDownloadsToKeep",
        help: "manageSubKeepDownloadsHelp",
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

export type SettingsSearchResult = {
  key: string;
  area: SettingsArea;
  path: string;
  label: string;
  /** The page that holds a setting; absent for a page result. */
  page?: string;
  setting?: string;
  advanced?: boolean;
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
  pages: SearchablePage[],
  t: (key: StringKey) => string,
  { appStoreHost = false, limit = 8 }: { appStoreHost?: boolean; limit?: number } = {},
): SettingsSearchResult[] {
  const words = normalizeSearchText(query).split(/\s+/).filter(Boolean);
  if (words.length === 0) return [];

  const candidates: { result: SettingsSearchResult; label: string; help: string }[] = [];
  for (const page of pages) {
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
    if (page.area === "console" && appStoreHost && APP_STORE_FIREWALL_PATHS.has(page.path))
      continue;
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

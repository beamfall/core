export const stagedRestoreStrings = {
  en: {
    // Console staged restore (CORE-1720).
    stagedRestoreHeading: "Restore from the Console",
    stagedRestoreLead:
      "Open a backup set with its backup key to see what it holds, then stage it. The server restores it the next time it starts, before the database opens, and keeps the current data as a rollback copy.",
    stagedRestorePickSet: "Select a backup set in the history above to restore it.",
    stagedRestoreSetLabel: "Backup set",
    stagedRestoreKeyLabel: "Backup key",
    stagedRestoreKeyHint: "The key only opens the set. It is never stored or shown again.",
    stagedRestoreInspect: "Show contents",
    stagedRestoreInspecting: "Opening…",
    stagedRestoreCreatedLabel: "Captured",
    stagedRestoreReasonLabel: "Reason",
    stagedRestoreFormatLabel: "Restore format",
    stagedRestoreFormatValue: "Version {version}, this server reads up to {supported}",
    stagedRestoreLibraryRootsLabel: "Library folders",
    stagedRestoreFilesLabel: "Server files",
    stagedRestoreFilesNone: "None",
    stagedRestoreColDatabase: "Database",
    stagedRestoreColSchema: "Schema version",
    stagedRestoreColSupported: "This server supports",
    stagedRestoreColSize: "Size",
    stagedRestoreCompatible: "This server can restore this backup.",
    stagedRestoreIncompatible: "This server cannot restore this backup.",
    stagedRestoreReplaceIdentity:
      "Replace this server's secrets and certificates with the ones in the backup",
    stagedRestoreStage: "Stage restore for next start",
    stagedRestoreStaging: "Staging…",
    stagedRestoreStagedTitle: "A restore is staged",
    stagedRestoreStagedBody:
      "{set}, staged {time}. Restart the server to apply it. The current data is kept as a rollback copy.",
    stagedRestoreStagedReplaces: "It also replaces this server's secrets and certificates.",
    stagedRestoreCancel: "Cancel staged restore",
    stagedRestoreCanceling: "Canceling…",
    stagedRestoreLastOK:
      "The last start restored the backup captured {time}. The previous data is kept at {path}.",
    stagedRestoreLastFailed:
      "The last start could not restore the staged backup and kept the original data: {error}",
    stagedRestoreUnavailable: "The staged restore could not be loaded.",
    stagedRestoreFailed: "The restore could not be staged.",
    stagedRestoreOfflineHeading: "Restore offline instead",
  },
  fr: {},
  de: {},
  es: {},
  pt: {},
} as const;

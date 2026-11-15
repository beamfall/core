const updateSafetyStrings = {
  updateMigrationCautionLabel: "Migration caution",
  updateMigrationCautionAria: "Migration caution for available Core update",
  updateMigrationImpactUnknown: "Beamfall can't tell whether this release changes the database.",
  updateMigrationPossible:
    "This release may contain forward-only migrations. Backup freshness is unknown.",
  updateBackupRequired: "Take and verify a backup before installing.",
  updateExportNotBackup:
    "Metadata export is optional portability evidence, not a backup substitute.",
  updateRollbackSnapshotNotice:
    "Beamfall writes a plaintext Rollback Snapshot when migrations run; it is a local rollback net, not a backup.",
  updateDowngradeOffline: "Downgrade requires an offline restore and never uses in-place rollback.",
  updateScopeCore: "Core",
  updateScopePlugins: "Plugins",
  updateScopeCompatibility: "Compatibility",
  updatePluginCompatibilityUnknown: "Compatibility not checked",
  updateCompatibilityReview: "Review before install",
  updateRecoveryArtifactsHeading: "Three separate artifacts. They are not interchangeable.",
  updateArtifactV2Title: "Backup Set (v2, encrypted)",
  updateArtifactV2Body:
    "Databases plus operational state: secrets, the stream-token key, the pinned TLS identity, config, and integration state. Encrypted as one set; only your BackupKey opens it. The BackupKey and Library Recovery Keys are held separately and are never inside the set.",
  updateArtifactV1Title: "Backup (v1, database-only)",
  updateArtifactV1Body:
    "Databases only. It carries no operational state, so secrets, the TLS identity, and config must be re-provisioned by hand after restore.",
  updateArtifactRollbackTitle: "Rollback Snapshot (pre-migration, plaintext)",
  updateArtifactRollbackBody:
    "Written to this server's own disk before migrations start. Databases only, unencrypted, restored with one command and no key, back to the version this upgrade started from. Not a backup: it never leaves this disk and holds no secrets, TLS identity, or config.",
  updateCreateVerifyBackup: "Create and verify backup",
  updateExportMetadata: "Export metadata",
  updateOfflineRestoreGuidance: "Offline restore guidance",
  updateRollbackSelectVersion:
    "Select the earlier Beamfall version you want to run. Check that the versions listed inside the pre-migration manifest match that exact upgrade. Don't go by the file name.",
  updateRollbackStop:
    "Stop Beamfall before restore. The command won't run while Beamfall is running.",
  updateRollbackRun:
    "Run the rollback command. It checks the pre-migration manifest before it replaces the database folder:",
  updateRollbackCommand:
    "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
  updateRollbackRestart:
    "Restart Beamfall, check that it is healthy and that you can sign in, then remove the old database folder it kept.",
  updateCanonicalRestoreProcedure: "Canonical restore procedure",
  updateCanonicalRestoreHref:
    "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
} as const;

export const consoleOperationsStrings = {
  en: {
    ...updateSafetyStrings,
    playbackCacheHeading: "Playback rendition cache",
    playbackCacheLead:
      "Copies of your videos prepared ahead of time so they start faster. Check how much space they use, prepare one title now, or free up space.",
    playbackCacheRefresh: "Refresh",
    playbackCacheUnavailable: "The playback rendition cache is unavailable right now.",
    playbackCacheUnconfigured: "The playback rendition cache is not configured.",
    playbackCacheUnconfiguredHelp:
      "Set a cache folder and a disk budget on the server to prepare playback copies ahead of time.",
    playbackCacheConfigured: "Configured",
    playbackCacheOverBudget: "Over budget",
    playbackCacheStatusReady: "Ready",
    playbackCacheStatusGenerating: "Generating",
    playbackCacheStatusFailed: "Failed",
    playbackCacheStatusEvicted: "Evicted",
    playbackCacheDiskUsed: "Disk used / budget",
    playbackCachePrecompute: "Precompute renditions",
    playbackCachePrecomputeHelp:
      "Prepares every playback quality for one title now. Beamfall never does this on its own.",
    playbackCacheMediaPlaceholder: "Media ID",
    playbackCachePrecomputeAction: "Precompute",
    playbackCacheEvict: "Evict over budget",
    playbackCacheEvictHelp:
      "Removes prepared copies until the cache fits its disk budget. Beamfall can prepare them again later.",
    playbackCacheEvictAction: "Evict over budget",
    playbackCacheEvictConfirm: "Evict cache objects over the disk budget?",
    capabilitiesHeading: "Capabilities",
    capabilitiesLead:
      "Turn workflow features on or off here. Detected features depend on this computer, so they can only be viewed.",
    capabilitiesRefresh: "Refresh",
    capabilitiesUnavailable: "Capabilities are unavailable right now.",
    capabilitiesEmpty: "No capabilities are compiled into this server.",
    capabilitiesWorkflowGroup: "Workflow capabilities",
    capabilitiesProbeGroup: "Detected capabilities",
    capabilitiesEnabled: "Enabled",
    capabilitiesProbeAbsent: "Not detected on this host",
    capabilitiesEnableAction: "Enable",
    capabilitiesDisableAction: "Disable",
    operationsControlsHeading: "Operations controls",
    operationsControlsLead:
      "How much background work the server does, low-power mode, and the job list. Cancel, retry, or read the log for any job.",
    governorHeading: "Resource governor",
    governorRefresh: "Refresh",
    governorUnavailable: "The resource governor is unavailable right now.",
    governorActive: "Active",
    governorInactive: "Unavailable",
    governorUnwiredLine: "The resource governor is not active on this server.",
    governorUnwiredHelp:
      "This shows the background worker count from the server settings. Live load figures appear once the resource governor is running.",
    governorWorkers: "Generation workers",
    governorCPUBudget: "CPU budget",
    governorActiveTranscodes: "Active transcodes",
    governorActiveGeneration: "Active generation",
    governorCPUBudgetUnit: "cores",
    governorGenerationAllowed: "Generation allowed",
    governorGenerationPaused: "Generation paused",
    governorPreempting: "Background work paused for playback",
    governorLowPower: "Low-power mode",
    governorSettingsHeading: "Background work",
    governorBackgroundWorkers: "Background workers",
    governorBackgroundWorkersSave: "Save background workers",
    governorBackgroundWorkersHelp:
      "How many background jobs can run at once within the CPU budget. Playback always comes first.",
    eventsRetentionDays: "Events retention days",
    eventsRetentionDaysSave: "Save events retention days",
    eventsRetentionDaysHelp:
      "How many days to keep routine server events. Watch history and activity are never deleted here.",
    eventsCompactionRun: "Compact events now",
    eventsCompactionQueued: "Queued",
    eventsCompactionFailed: "Could not queue events compaction.",
    governorLowPowerHelp:
      "Pin background work to a single worker so the server stays quiet and cool.",
    governorSettingsUnavailable: "Governor settings are unavailable right now.",
    jobQueueHeading: "Job queue",
    jobQueueRefresh: "Refresh",
    jobQueueUnavailable: "The job queue is unavailable right now.",
    jobCancelAction: "Cancel job",
    jobRetryAction: "Retry",
    jobLogsShow: "Logs",
    jobLogsHide: "Hide logs",
    jobCancelConfirm: "Cancel this job? It will stop at its next checkpoint.",
    jobLogsEmpty: "No events recorded for this job yet.",
    jobLogsUnavailable: "Could not load this job's history.",
    jobLogPayloadRedacted: "(redacted)",
    jobKindEventsCompaction: "Events compaction",
    created: "Created",
    lastUsed: "Last used",
    never: "Never",
    active: "Active",
    revoked: "Revoked",
    deleteLibraryFilesKept:
      "Your media files stay on disk. Nothing in the library's folders is deleted or changed.",
    libraryDeleted: "Deleted “{library}”. Its media files are still on disk.",
    libraryControlName: "{control}: {library}",
    setupChecklistOptional: "Optional",
  },
  fr: {
    ...updateSafetyStrings,
    created: "Créé",
    lastUsed: "Dernière utilisation",
    never: "Jamais",
    active: "Actif",
    revoked: "Révoqué",
    deleteLibraryFilesKept:
      "Vos fichiers multimédias restent sur le disque. Rien n’est supprimé ni modifié dans les dossiers de la bibliothèque.",
    libraryDeleted:
      "« {library} » a été supprimée. Ses fichiers multimédias sont toujours sur le disque.",
    libraryControlName: "{control} : {library}",
    setupChecklistOptional: "Facultatif",
  },
  de: {
    ...updateSafetyStrings,
    created: "Erstellt",
    lastUsed: "Zuletzt verwendet",
    never: "Nie",
    active: "Aktiv",
    revoked: "Widerrufen",
    deleteLibraryFilesKept:
      "Die Mediendateien bleiben auf dem Datenträger. In den Ordnern der Bibliothek wird nichts gelöscht oder geändert.",
    libraryDeleted:
      "„{library}“ wurde gelöscht. Die Mediendateien liegen weiterhin auf dem Datenträger.",
    libraryControlName: "{control}: {library}",
    setupChecklistOptional: "Optional",
  },
  es: {
    ...updateSafetyStrings,
    created: "Creado",
    lastUsed: "Último uso",
    never: "Nunca",
    active: "Activo",
    revoked: "Revocado",
    deleteLibraryFilesKept:
      "Tus archivos multimedia permanecen en el disco. No se elimina ni se modifica nada en las carpetas de la biblioteca.",
    libraryDeleted: "Se eliminó «{library}». Sus archivos multimedia siguen en el disco.",
    libraryControlName: "{control}: {library}",
    setupChecklistOptional: "Opcional",
  },
  pt: {
    ...updateSafetyStrings,
    created: "Criado",
    lastUsed: "Último uso",
    never: "Nunca",
    active: "Ativo",
    revoked: "Revogado",
    deleteLibraryFilesKept:
      "Seus arquivos de mídia permanecem no disco. Nada é excluído nem alterado nas pastas da biblioteca.",
    libraryDeleted: "“{library}” foi excluída. Os arquivos de mídia continuam no disco.",
    libraryControlName: "{control}: {library}",
    setupChecklistOptional: "Opcional",
  },
} as const;

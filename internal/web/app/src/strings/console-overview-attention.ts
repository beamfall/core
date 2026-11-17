// Overview "Needs attention" and first-value step (CORE-2310, CORE-2309).
export const consoleOverviewAttentionStrings = {
  en: {
    consoleAttentionHealthCount: "{label}: {count, plural, one {# item} other {# items}}",
    consoleAttentionJobFailed: "{job} failed",
    consoleAttentionBackupKey: "The backup key is not saved yet",
    consoleOverviewJobsFailed: "{count, plural, one {# failed} other {# failed}}",
    setupChecklistScanRunning: "A scan is running. Items appear as it brings them in.",
    setupChecklistContentPending:
      "Items are ready to browse, but no recent scan has finished. Rescan to finish this step.",
    consoleOverviewStatus: "Status",
  },
  fr: {
    consoleAttentionHealthCount: "{label} : {count, plural, one {# élément} other {# éléments}}",
    consoleAttentionJobFailed: "Échec : {job}",
    consoleAttentionBackupKey: "La clé de sauvegarde n’est pas encore enregistrée",
    consoleOverviewJobsFailed: "{count, plural, one {# en échec} other {# en échec}}",
    setupChecklistScanRunning:
      "Une analyse est en cours. Les éléments apparaissent au fur et à mesure.",
    setupChecklistContentPending:
      "Des éléments sont consultables, mais aucune analyse récente n’a abouti. Relancez une analyse pour finir cette étape.",
    consoleOverviewStatus: "État",
  },
  de: {
    consoleAttentionHealthCount: "{label}: {count, plural, one {# Eintrag} other {# Einträge}}",
    consoleAttentionJobFailed: "{job} fehlgeschlagen",
    consoleAttentionBackupKey: "Der Sicherungsschlüssel ist noch nicht gesichert",
    consoleOverviewJobsFailed: "{count, plural, one {# fehlgeschlagen} other {# fehlgeschlagen}}",
    setupChecklistScanRunning: "Ein Scan läuft. Einträge erscheinen, sobald er sie aufnimmt.",
    setupChecklistContentPending:
      "Einträge sind schon abrufbar, aber kein aktueller Scan wurde abgeschlossen. Scanne erneut, um diesen Schritt abzuschließen.",
    consoleOverviewStatus: "Status",
  },
  es: {
    consoleAttentionHealthCount: "{label}: {count, plural, one {# elemento} other {# elementos}}",
    consoleAttentionJobFailed: "Error: {job}",
    consoleAttentionBackupKey: "La clave de copia de seguridad aún no está guardada",
    consoleOverviewJobsFailed: "{count, plural, one {# con error} other {# con error}}",
    setupChecklistScanRunning:
      "Hay un análisis en curso. Los elementos aparecen a medida que los incorpora.",
    setupChecklistContentPending:
      "Ya hay elementos para explorar, pero ningún análisis reciente ha terminado. Vuelve a analizar para completar este paso.",
    consoleOverviewStatus: "Estado",
  },
  pt: {
    consoleAttentionHealthCount: "{label}: {count, plural, one {# item} other {# itens}}",
    consoleAttentionJobFailed: "Falha: {job}",
    consoleAttentionBackupKey: "A chave de backup ainda não foi guardada",
    consoleOverviewJobsFailed: "{count, plural, one {# com falha} other {# com falha}}",
    setupChecklistScanRunning:
      "Há uma análise em curso. Os itens aparecem à medida que são adicionados.",
    setupChecklistContentPending:
      "Já há itens para explorar, mas nenhuma análise recente terminou. Volte a analisar para concluir este passo.",
    consoleOverviewStatus: "Estado",
  },
} as const;

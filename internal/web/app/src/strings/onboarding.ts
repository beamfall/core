export const onboardingStrings = {
  en: {
    setupChecklistHeading: "Get to first value",
    setupChecklistLead:
      "Your path to a first successful browse. Each step opens where you finish it. Reading this leaks no secret, only whether each step is done.",
    setupChecklistScan: "Start your first scan",
    setupChecklistMigrate: "Migrate from another server",
    setupChecklistEnroll: "Add a device or browser",
    setupChecklistProfiles: "Create profiles",
    setupIncomplete: "Finish the steps above to reach your first browse.",
    setupChecklistFirstValue: "You're all set. Your library is ready to browse.",
    setupChecklistEmptyNextStep:
      "Setup is done, but there's nothing to browse yet. Start a scan or migrate to bring in content.",
    setupChecklistDone: "Done",
    setupChecklistNext: "Next step",
    setupChecklistUnavailable: "Could not load setup state.",
    setupChecklistMigrationResume: "Migration paused",
    setupChecklistMigrationPartial:
      "A previous migration stopped before it finished. Resume it, re-run safely, or finish later.",
    setupChecklistResume: "Resume",
    setupChecklistRerun: "Re-run from start",
    setupChecklistDiscard: "Discard & finish later",
    // Pairing-code entry on the enroll and promote pages (CORE-1117).
    pairingCodeRequired: "Enter the pairing code to continue.",
    pairingCodeRejectedHint:
      "Each code works once and expires 10 minutes after it's made. Ask for a new one if this one is old.",
    pairingCodeOffline:
      "Can't reach the server. Check your connection and that the server is running, then try again.",
    enrollRetry:
      "The server couldn't connect this browser. Try again; if it keeps failing, check the server log.",
    promoteRetry:
      "The server couldn't promote this browser. Try again; if it keeps failing, check the server log.",
    promoteCodeHint:
      "Manager codes come from a manager device: Console › {page} › {action} › {kind}.",
    promoteNeedsManagerCode:
      "That's a viewer code, so it can't make this browser a manager. Ask for a manager code instead.",
  },
  fr: {
    setupChecklistHeading: "Configuration initiale",
    setupChecklistLead:
      "Les étapes d'intégration dont ce serveur a encore besoin. Lire ceci ne divulgue aucun secret, seulement si la configuration est terminée.",
    setupIncomplete: "Configuration incomplète.",
    setupChecklistDone: "Fait",
    setupChecklistUnavailable: "Impossible de charger l'état de configuration.",
  },
  de: {
    setupChecklistHeading: "Ersteinrichtung",
    setupChecklistLead:
      "Die Einrichtungsschritte, die dieser Server noch benötigt. Dies preiszugeben verrät kein Geheimnis, nur, ob die Einrichtung abgeschlossen ist.",
    setupIncomplete: "Einrichtung unvollständig.",
    setupChecklistDone: "Erledigt",
    setupChecklistUnavailable: "Einrichtungsstatus konnte nicht geladen werden.",
  },
  es: {
    setupChecklistHeading: "Configuración inicial",
    setupChecklistLead:
      "Los pasos de incorporación que este servidor aún necesita. Leer esto no revela ningún secreto, solo si la configuración terminó.",
    setupIncomplete: "Configuración incompleta.",
    setupChecklistDone: "Hecho",
    setupChecklistUnavailable: "No se pudo cargar el estado de configuración.",
  },
  pt: {
    setupChecklistHeading: "Configuração inicial",
    setupChecklistLead:
      "As etapas de integração que este servidor ainda precisa. Ler isso não revela nenhum segredo, apenas se a configuração foi concluída.",
    setupIncomplete: "Configuração incompleta.",
    setupChecklistDone: "Concluído",
    setupChecklistUnavailable: "Não foi possível carregar o estado de configuração.",
  },
} as const;

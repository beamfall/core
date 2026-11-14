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
    pairingCodeRequired: "Saisissez le code de jumelage pour continuer.",
    pairingCodeRejectedHint:
      "Chaque code ne sert qu’une fois et expire 10 minutes après sa création. Demandez-en un nouveau si celui-ci est ancien.",
    pairingCodeOffline:
      "Impossible de joindre le serveur. Vérifiez votre connexion et que le serveur fonctionne, puis réessayez.",
    enrollRetry:
      "Le serveur n’a pas pu connecter ce navigateur. Réessayez ; si l’échec persiste, consultez le journal du serveur.",
    promoteRetry:
      "Le serveur n’a pas pu promouvoir ce navigateur. Réessayez ; si l’échec persiste, consultez le journal du serveur.",
    promoteCodeHint:
      "Les codes de gestionnaire proviennent d’un appareil gestionnaire : Console › {page} › {action} › {kind}.",
    promoteNeedsManagerCode:
      "C’est un code de spectateur : il ne peut pas faire de ce navigateur un gestionnaire. Demandez plutôt un code de gestionnaire.",
  },
  de: {
    setupChecklistHeading: "Ersteinrichtung",
    setupChecklistLead:
      "Die Einrichtungsschritte, die dieser Server noch benötigt. Dies preiszugeben verrät kein Geheimnis, nur, ob die Einrichtung abgeschlossen ist.",
    setupIncomplete: "Einrichtung unvollständig.",
    setupChecklistDone: "Erledigt",
    setupChecklistUnavailable: "Einrichtungsstatus konnte nicht geladen werden.",
    pairingCodeRequired: "Gib den Kopplungscode ein, um fortzufahren.",
    pairingCodeRejectedHint:
      "Jeder Code funktioniert einmal und läuft 10 Minuten nach der Erstellung ab. Fordere einen neuen an, wenn dieser alt ist.",
    pairingCodeOffline:
      "Server nicht erreichbar. Prüfe deine Verbindung und ob der Server läuft, und versuche es dann erneut.",
    enrollRetry:
      "Der Server konnte diesen Browser nicht verbinden. Versuche es erneut; wenn es weiter fehlschlägt, prüfe das Serverprotokoll.",
    promoteRetry:
      "Der Server konnte diesen Browser nicht hochstufen. Versuche es erneut; wenn es weiter fehlschlägt, prüfe das Serverprotokoll.",
    promoteCodeHint:
      "Manager-Codes kommen von einem Manager-Gerät: Console › {page} › {action} › {kind}.",
    promoteNeedsManagerCode:
      "Das ist ein Zuschauer-Code und kann diesen Browser nicht zum Manager machen. Fordere stattdessen einen Manager-Code an.",
  },
  es: {
    setupChecklistHeading: "Configuración inicial",
    setupChecklistLead:
      "Los pasos de incorporación que este servidor aún necesita. Leer esto no revela ningún secreto, solo si la configuración terminó.",
    setupIncomplete: "Configuración incompleta.",
    setupChecklistDone: "Hecho",
    setupChecklistUnavailable: "No se pudo cargar el estado de configuración.",
    pairingCodeRequired: "Introduce el código de emparejamiento para continuar.",
    pairingCodeRejectedHint:
      "Cada código funciona una sola vez y caduca 10 minutos después de crearse. Pide uno nuevo si este es antiguo.",
    pairingCodeOffline:
      "No se puede conectar con el servidor. Comprueba tu conexión y que el servidor esté en marcha, y vuelve a intentarlo.",
    enrollRetry:
      "El servidor no pudo conectar este navegador. Inténtalo de nuevo; si sigue fallando, revisa el registro del servidor.",
    promoteRetry:
      "El servidor no pudo promover este navegador. Inténtalo de nuevo; si sigue fallando, revisa el registro del servidor.",
    promoteCodeHint:
      "Los códigos de administrador se obtienen en un dispositivo administrador: Console › {page} › {action} › {kind}.",
    promoteNeedsManagerCode:
      "Es un código de espectador, así que no puede convertir este navegador en administrador. Pide un código de administrador.",
  },
  pt: {
    setupChecklistHeading: "Configuração inicial",
    setupChecklistLead:
      "As etapas de integração que este servidor ainda precisa. Ler isso não revela nenhum segredo, apenas se a configuração foi concluída.",
    setupIncomplete: "Configuração incompleta.",
    setupChecklistDone: "Concluído",
    setupChecklistUnavailable: "Não foi possível carregar o estado de configuração.",
    pairingCodeRequired: "Digite o código de pareamento para continuar.",
    pairingCodeRejectedHint:
      "Cada código funciona uma vez e expira 10 minutos após ser criado. Peça um novo se este for antigo.",
    pairingCodeOffline:
      "Não foi possível acessar o servidor. Verifique sua conexão e se o servidor está em execução e tente novamente.",
    enrollRetry:
      "O servidor não conseguiu conectar este navegador. Tente novamente; se continuar falhando, verifique o log do servidor.",
    promoteRetry:
      "O servidor não conseguiu promover este navegador. Tente novamente; se continuar falhando, verifique o log do servidor.",
    promoteCodeHint:
      "Códigos de gerente vêm de um dispositivo gerente: Console › {page} › {action} › {kind}.",
    promoteNeedsManagerCode:
      "Esse é um código de espectador, então não pode tornar este navegador gerente. Peça um código de gerente.",
  },
} as const;

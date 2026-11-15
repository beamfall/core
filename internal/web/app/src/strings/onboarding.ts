export const onboardingStrings = {
  en: {
    setupChecklistHeading: "Get to first value",
    setupChecklistLead:
      "Your path to a first successful browse. Each step opens where you finish it.",
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
    // Library folder health (CORE-1606).
    libraryRootMissing: "Folder not found",
    libraryRootUnreadable: "Can't read folder",
    libraryRootAttention: "{library}: {path}",
    libraryRootFixHint: "Remove this folder and add the right one below.",
    firstScanBlocked: "Can't scan {path}: {reason}.",
    firstScanFixFolder: "Fix the folder",
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
    setupChecklistScan: "Lancer votre premier scan",
    setupChecklistMigrate: "Migrer depuis un autre serveur",
    setupChecklistEnroll: "Ajouter un appareil ou un navigateur",
    setupChecklistProfiles: "Créer des profils",
    setupIncomplete: "Configuration incomplète.",
    setupChecklistFirstValue: "Tout est prêt. Vous pouvez parcourir votre bibliothèque.",
    setupChecklistEmptyNextStep:
      "La configuration est terminée, mais il n’y a encore rien à parcourir. Lancez un scan ou migrez pour importer du contenu.",
    setupChecklistDone: "Fait",
    setupChecklistNext: "Étape suivante",
    setupChecklistUnavailable: "Impossible de charger l'état de configuration.",
    setupChecklistMigrationResume: "Migration en pause",
    setupChecklistMigrationPartial:
      "Une migration précédente s’est arrêtée avant la fin. Reprenez-la, relancez-la sans risque, ou terminez plus tard.",
    setupChecklistResume: "Reprendre",
    setupChecklistRerun: "Relancer depuis le début",
    setupChecklistDiscard: "Abandonner et finir plus tard",
    libraryRootMissing: "Dossier introuvable",
    libraryRootUnreadable: "Dossier illisible",
    libraryRootAttention: "{library} : {path}",
    libraryRootFixHint: "Retirez ce dossier et ajoutez le bon ci-dessous.",
    firstScanBlocked: "Impossible de scanner {path} : {reason}.",
    firstScanFixFolder: "Corriger le dossier",
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
    setupChecklistScan: "Ersten Scan starten",
    setupChecklistMigrate: "Von einem anderen Server umziehen",
    setupChecklistEnroll: "Gerät oder Browser hinzufügen",
    setupChecklistProfiles: "Profile erstellen",
    setupIncomplete: "Einrichtung unvollständig.",
    setupChecklistFirstValue: "Alles bereit. Die Bibliothek kann durchsucht werden.",
    setupChecklistEmptyNextStep:
      "Die Einrichtung ist fertig, aber es gibt noch nichts zu durchsuchen. Starte einen Scan oder zieh um, um Inhalte zu holen.",
    setupChecklistDone: "Erledigt",
    setupChecklistNext: "Nächster Schritt",
    setupChecklistUnavailable: "Einrichtungsstatus konnte nicht geladen werden.",
    setupChecklistMigrationResume: "Umzug pausiert",
    setupChecklistMigrationPartial:
      "Ein früherer Umzug wurde vor dem Ende gestoppt. Setze ihn fort, starte ihn sicher neu oder mach später weiter.",
    setupChecklistResume: "Fortsetzen",
    setupChecklistRerun: "Von vorn starten",
    setupChecklistDiscard: "Verwerfen und später beenden",
    libraryRootMissing: "Ordner nicht gefunden",
    libraryRootUnreadable: "Ordner nicht lesbar",
    libraryRootAttention: "{library}: {path}",
    libraryRootFixHint: "Entferne diesen Ordner und füge unten den richtigen hinzu.",
    firstScanBlocked: "{path} kann nicht gescannt werden: {reason}.",
    firstScanFixFolder: "Ordner korrigieren",
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
    setupChecklistScan: "Inicia tu primer análisis",
    setupChecklistMigrate: "Migrar desde otro servidor",
    setupChecklistEnroll: "Añadir un dispositivo o navegador",
    setupChecklistProfiles: "Crear perfiles",
    setupIncomplete: "Configuración incompleta.",
    setupChecklistFirstValue: "Todo listo. Ya puedes explorar tu biblioteca.",
    setupChecklistEmptyNextStep:
      "La configuración está lista, pero todavía no hay nada que explorar. Inicia un análisis o migra para traer contenido.",
    setupChecklistDone: "Hecho",
    setupChecklistNext: "Siguiente paso",
    setupChecklistUnavailable: "No se pudo cargar el estado de configuración.",
    setupChecklistMigrationResume: "Migración en pausa",
    setupChecklistMigrationPartial:
      "Una migración anterior se detuvo antes de terminar. Reanúdala, vuelve a ejecutarla sin riesgo o termina más tarde.",
    setupChecklistResume: "Reanudar",
    setupChecklistRerun: "Repetir desde el principio",
    setupChecklistDiscard: "Descartar y terminar más tarde",
    libraryRootMissing: "Carpeta no encontrada",
    libraryRootUnreadable: "No se puede leer la carpeta",
    libraryRootAttention: "{library}: {path}",
    libraryRootFixHint: "Quita esta carpeta y añade la correcta abajo.",
    firstScanBlocked: "No se puede analizar {path}: {reason}.",
    firstScanFixFolder: "Corregir la carpeta",
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
    setupChecklistScan: "Inicie sua primeira verificação",
    setupChecklistMigrate: "Migrar de outro servidor",
    setupChecklistEnroll: "Adicionar um dispositivo ou navegador",
    setupChecklistProfiles: "Criar perfis",
    setupIncomplete: "Configuração incompleta.",
    setupChecklistFirstValue: "Tudo pronto. Sua biblioteca já pode ser explorada.",
    setupChecklistEmptyNextStep:
      "A configuração está concluída, mas ainda não há nada para explorar. Inicie uma verificação ou migre para trazer conteúdo.",
    setupChecklistDone: "Concluído",
    setupChecklistNext: "Próxima etapa",
    setupChecklistUnavailable: "Não foi possível carregar o estado de configuração.",
    setupChecklistMigrationResume: "Migração pausada",
    setupChecklistMigrationPartial:
      "Uma migração anterior parou antes de terminar. Retome-a, execute de novo com segurança ou termine depois.",
    setupChecklistResume: "Retomar",
    setupChecklistRerun: "Recomeçar do início",
    setupChecklistDiscard: "Descartar e terminar depois",
    libraryRootMissing: "Pasta não encontrada",
    libraryRootUnreadable: "Não é possível ler a pasta",
    libraryRootAttention: "{library}: {path}",
    libraryRootFixHint: "Remova esta pasta e adicione a correta abaixo.",
    firstScanBlocked: "Não é possível verificar {path}: {reason}.",
    firstScanFixFolder: "Corrigir a pasta",
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

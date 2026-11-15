export const setupFinishStrings = {
  en: {
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
  fr: {
    setupBack: "Retour",
    setupContinue: "Continuer",
    setupFinish: "Terminer la configuration",
    setupBrowseReady: "Parcourir ce qui est prêt",
    setupWorking: "En cours…",
    setupFailed:
      "Le serveur n’a pas pu terminer cette étape. Consultez le journal du serveur pour en connaître la cause, puis réessayez.",
    setupOffline:
      "Impossible de joindre le serveur. Vérifiez qu’il fonctionne et que vous êtes sur son réseau, puis réessayez.",
    setupScanTitle: "Votre bibliothèque se remplit",
    setupScanLead:
      "Beamfall scanne votre première bibliothèque. Parcourez-la dès que des éléments apparaissent ; le scan continue en arrière-plan.",
    setupScanStatus: "État du scan : {status}",
    setupScanWaiting: "En attente de progression…",
    setupScanConnectionLost:
      "Contact perdu avec le serveur. Nouvelle tentative ; le scan continue sur le serveur.",
    setupScanStopped:
      "Le scan s’est arrêté avant la fin. Parcourez ce qui est prêt, puis relancez le scan de cette bibliothèque depuis Console › Bibliothèques.",
    setupMigrateTitle: "Emportez votre bibliothèque",
    setupMigrateLead:
      "Vous venez d’un autre serveur ? Importez votre historique de visionnage, vos notes et vos collections pour reprendre où vous en étiez.",
    setupMigrateTrustAdditive:
      "Ajout uniquement. Cela comble les vides et n’écrase ni ne réduit jamais votre progression Beamfall.",
    setupMigrateTrustScope:
      "Importe uniquement l’état de visionnage, les notes et les collections, jamais les fichiers, métadonnées ni réglages.",
    setupMigrateTrustNoConnection:
      "Aucune connexion durable n’est conservée et votre ancien serveur n’est jamais modifié.",
    setupMigrateTrustRerunnable:
      "Relançable. Continuez d’utiliser l’ancien serveur et réimportez la différence le jour du changement.",
    setupMigrateTrustReportFirst:
      "Rapport d’abord. Vous voyez exactement ce qui sera importé avant que quoi que ce soit soit écrit.",
    setupMigrateSourcesLabel: "Sources disponibles",
    setupMigrateSourcesUnavailable:
      "Impossible de charger les sources disponibles. Vous pourrez ouvrir l’importeur plus tard depuis la Console.",
    setupMigrateNoSources:
      "Aucune source d’import n’est encore disponible via vos plugins actifs. Vous pourrez en ajouter plus tard depuis la Console.",
    setupMigrateSkip: "Passer pour l’instant",
    setupMigrateContinue: "Choisir une source",
  },
  de: {
    setupBack: "Zurück",
    setupContinue: "Weiter",
    setupFinish: "Einrichtung abschließen",
    setupBrowseReady: "Fertige Inhalte durchsuchen",
    setupWorking: "Wird bearbeitet…",
    setupFailed:
      "Der Server konnte diesen Schritt nicht abschließen. Die Ursache steht im Serverprotokoll. Versuche es danach erneut.",
    setupOffline:
      "Server nicht erreichbar. Prüfe, ob er läuft und du in seinem Netzwerk bist, und versuche es dann erneut.",
    setupScanTitle: "Deine Bibliothek füllt sich",
    setupScanLead:
      "Beamfall scannt deine erste Bibliothek. Stöbere, sobald Einträge erscheinen; der Scan läuft im Hintergrund weiter.",
    setupScanStatus: "Scan-Status: {status}",
    setupScanWaiting: "Warten auf Fortschritt…",
    setupScanConnectionLost:
      "Verbindung zum Server verloren. Neuer Versuch läuft; der Scan läuft auf dem Server weiter.",
    setupScanStopped:
      "Der Scan wurde vor dem Ende gestoppt. Stöbere in dem, was fertig ist, und scanne diese Bibliothek dann in Console › Bibliotheken erneut.",
    setupMigrateTitle: "Nimm deine Bibliothek mit",
    setupMigrateLead:
      "Wechselst du von einem anderen Server? Importiere deinen Wiedergabeverlauf, deine Bewertungen und Sammlungen, um dort weiterzumachen, wo du aufgehört hast.",
    setupMigrateTrustAdditive:
      "Nur Ergänzungen. Es füllt Lücken und überschreibt oder verschlechtert nie deinen Beamfall-Fortschritt.",
    setupMigrateTrustScope:
      "Importiert nur Wiedergabestatus, Bewertungen und Sammlungen, niemals Dateien, Metadaten oder Einstellungen.",
    setupMigrateTrustNoConnection:
      "Es bleibt keine dauerhafte Verbindung bestehen, und dein alter Server wird nie verändert.",
    setupMigrateTrustRerunnable:
      "Wiederholbar. Nutze den alten Server weiter und importiere am Umstiegstag nur den Unterschied nach.",
    setupMigrateTrustReportFirst:
      "Erst der Bericht. Du siehst genau, was importiert wird, bevor etwas geschrieben wird.",
    setupMigrateSourcesLabel: "Verfügbare Quellen",
    setupMigrateSourcesUnavailable:
      "Die verfügbaren Quellen konnten nicht geladen werden. Du kannst den Import später über die Console öffnen.",
    setupMigrateNoSources:
      "Deine aktiven Plugins bieten noch keine Importquellen. Du kannst sie später über die Console ergänzen.",
    setupMigrateSkip: "Vorerst überspringen",
    setupMigrateContinue: "Quelle wählen",
  },
  es: {
    setupBack: "Atrás",
    setupContinue: "Continuar",
    setupFinish: "Terminar la configuración",
    setupBrowseReady: "Explorar lo que ya está listo",
    setupWorking: "Trabajando…",
    setupFailed:
      "El servidor no pudo terminar este paso. Mira el registro del servidor para saber la causa y vuelve a intentarlo.",
    setupOffline:
      "No se puede conectar con el servidor. Comprueba que esté en marcha y que estés en su red, y vuelve a intentarlo.",
    setupScanTitle: "Tu biblioteca se está llenando",
    setupScanLead:
      "Beamfall está analizando tu primera biblioteca. Explórala en cuanto aparezcan elementos; el análisis sigue en segundo plano.",
    setupScanStatus: "Estado del análisis: {status}",
    setupScanWaiting: "Esperando el progreso…",
    setupScanConnectionLost:
      "Se perdió el contacto con el servidor. Reintentando; el análisis sigue en el servidor.",
    setupScanStopped:
      "El análisis se detuvo antes de terminar. Explora lo que ya está listo y vuelve a analizar esta biblioteca desde Console › Bibliotecas.",
    setupMigrateTitle: "Trae tu biblioteca contigo",
    setupMigrateLead:
      "¿Vienes de otro servidor? Importa tu historial de reproducción, tus valoraciones y tus colecciones para seguir donde lo dejaste.",
    setupMigrateTrustAdditive:
      "Solo añade. Rellena huecos y nunca sobrescribe ni reduce tu progreso en Beamfall.",
    setupMigrateTrustScope:
      "Solo importa el estado de reproducción, las valoraciones y las colecciones, nunca archivos, metadatos ni ajustes.",
    setupMigrateTrustNoConnection:
      "No se mantiene ninguna conexión y tu servidor anterior nunca se modifica.",
    setupMigrateTrustRerunnable:
      "Repetible. Sigue usando el servidor anterior y vuelve a importar la diferencia el día del cambio.",
    setupMigrateTrustReportFirst:
      "Primero el informe. Ves exactamente qué se importará antes de que se escriba nada.",
    setupMigrateSourcesLabel: "Fuentes disponibles",
    setupMigrateSourcesUnavailable:
      "No se pudieron cargar las fuentes disponibles. Puedes abrir el importador más tarde desde la Console.",
    setupMigrateNoSources:
      "Tus plugins activos aún no ofrecen fuentes de importación. Puedes añadirlas más tarde desde la Console.",
    setupMigrateSkip: "Omitir por ahora",
    setupMigrateContinue: "Elegir una fuente",
  },
  pt: {
    setupBack: "Voltar",
    setupContinue: "Continuar",
    setupFinish: "Concluir a configuração",
    setupBrowseReady: "Explorar o que já está pronto",
    setupWorking: "Trabalhando…",
    setupFailed:
      "O servidor não conseguiu concluir esta etapa. Veja o log do servidor para saber a causa e tente novamente.",
    setupOffline:
      "Não foi possível acessar o servidor. Verifique se ele está em execução e se você está na rede dele, e tente novamente.",
    setupScanTitle: "Sua biblioteca está se enchendo",
    setupScanLead:
      "O Beamfall está verificando sua primeira biblioteca. Explore assim que os itens aparecerem; a verificação continua em segundo plano.",
    setupScanStatus: "Status da verificação: {status}",
    setupScanWaiting: "Aguardando o progresso…",
    setupScanConnectionLost:
      "Contato com o servidor perdido. Tentando de novo; a verificação continua no servidor.",
    setupScanStopped:
      "A verificação parou antes de terminar. Explore o que já está pronto e verifique esta biblioteca de novo em Console › Bibliotecas.",
    setupMigrateTitle: "Traga sua biblioteca com você",
    setupMigrateLead:
      "Está vindo de outro servidor? Importe seu histórico de reprodução, suas avaliações e coleções para continuar de onde parou.",
    setupMigrateTrustAdditive:
      "Apenas adiciona. Preenche lacunas e nunca sobrescreve nem reduz seu progresso no Beamfall.",
    setupMigrateTrustScope:
      "Importa só o estado de reprodução, as avaliações e as coleções, nunca arquivos, metadados ou configurações.",
    setupMigrateTrustNoConnection:
      "Nenhuma conexão contínua é mantida e seu servidor antigo nunca é modificado.",
    setupMigrateTrustRerunnable:
      "Repetível. Continue usando o servidor antigo e importe a diferença de novo no dia da troca.",
    setupMigrateTrustReportFirst:
      "Relatório primeiro. Você vê exatamente o que será importado antes de qualquer coisa ser gravada.",
    setupMigrateSourcesLabel: "Fontes disponíveis",
    setupMigrateSourcesUnavailable:
      "Não foi possível carregar as fontes disponíveis. Você pode abrir o importador depois pelo Console.",
    setupMigrateNoSources:
      "Seus plugins ativos ainda não oferecem fontes de importação. Você pode adicioná-las depois pelo Console.",
    setupMigrateSkip: "Pular por enquanto",
    setupMigrateContinue: "Escolher uma fonte",
  },
} as const;

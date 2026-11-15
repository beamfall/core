export const backupRetentionStrings = {
  en: {
    // WEB-5L — Backup / metadata export / Trash policy / Activity undo.
    backupHeading: "Backups",
    backupLead:
      "Run a database backup to a server-side destination. The mutation returns the written manifest path, shown below as the run record.",
    backupDestLabel: "Destination",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Run backup",
    backupRunning: "Running…",
    backupManifestLabel: "Latest manifest",
    backupNoneYet: "No backup has been run from here yet.",
    backupFailed: "The backup could not be run.",
    backupNeedDest: "Enter a destination path.",
    backupRestoreNote:
      "Restore is operator-run from the destination with the server stopped; use the guided restore flow below, documented in ",
    backupRestoreNoteDocLabel: "docs/operations/restore.md",
    backupRestoreNoteDocHref: "docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ". The Console never writes over a live database.",
    backupRestoreHeading: "Guided restore",
    backupRestoreLead:
      "Use this when startup refuses a corrupt database or you need to roll back to a known-good backup.",
    backupRestoreStepStop: "Stop Beamfall and leave the corrupt data directory in place.",
    backupRestoreStepChoose:
      "Choose the newest trusted manifest, usually under <data-dir>/backups/pre-migration or the backup destination shown above.",
    backupRestoreStepRun:
      "Run the offline restore command on the server host; it refuses a live server lock and verifies manifest hashes before touching the data directory.",
    backupRestoreStepStart:
      "Start Beamfall again and keep the pre-restore snapshot until the restored server is verified.",
    backupRestoreSafety:
      "The Console never overwrites a live database: a staged restore applies on the next start, before the database opens.",
  },
  fr: {
    backupHeading: "Sauvegardes",
    backupLead:
      "Lancez une sauvegarde de la base de données vers une destination côté serveur. L’opération renvoie le chemin du manifeste écrit, affiché ci-dessous comme trace d’exécution.",
    backupDestLabel: "Destination",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Lancer la sauvegarde",
    backupRunning: "En cours…",
    backupManifestLabel: "Dernier manifeste",
    backupNoneYet: "Aucune sauvegarde n’a encore été lancée depuis ici.",
    backupFailed: "La sauvegarde n’a pas pu être lancée.",
    backupNeedDest: "Saisissez un chemin de destination.",
    backupRestoreNote:
      "La restauration est effectuée par l’opérateur depuis la destination, serveur arrêté ; utilisez le parcours de restauration guidée ci-dessous, documenté dans ",
    backupRestoreNoteDocLabel: "docs/operations/restore.md",
    backupRestoreNoteDocHref: "docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ". La Console n’écrase jamais une base de données active.",
    backupRestoreHeading: "Restauration guidée",
    backupRestoreLead:
      "Utilisez-la lorsque le démarrage refuse une base corrompue ou que vous devez revenir à une sauvegarde saine connue.",
    backupRestoreStepStop:
      "Arrêtez Beamfall et laissez le répertoire de données corrompu en place.",
    backupRestoreStepChoose:
      "Choisissez le manifeste de confiance le plus récent, généralement sous <data-dir>/backups/pre-migration ou dans la destination de sauvegarde indiquée ci-dessus.",
    backupRestoreStepRun:
      "Exécutez la commande de restauration hors ligne sur l’hôte du serveur ; elle refuse un verrou de serveur actif et vérifie les empreintes du manifeste avant de toucher au répertoire de données.",
    backupRestoreStepStart:
      "Redémarrez Beamfall et conservez l’instantané préalable à la restauration jusqu’à ce que le serveur restauré soit vérifié.",
    backupRestoreSafety:
      "La Console n’écrase jamais une base de données active : une restauration planifiée s’applique au prochain démarrage, avant l’ouverture de la base.",
  },
  de: {
    backupHeading: "Backups",
    backupLead:
      "Starte ein Datenbank-Backup in ein Ziel auf dem Server. Der Vorgang gibt den Pfad des geschriebenen Manifests zurück, der unten als Ausführungsprotokoll angezeigt wird.",
    backupDestLabel: "Ziel",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Backup starten",
    backupRunning: "Läuft…",
    backupManifestLabel: "Letztes Manifest",
    backupNoneYet: "Von hier aus wurde noch kein Backup gestartet.",
    backupFailed: "Das Backup konnte nicht gestartet werden.",
    backupNeedDest: "Gib einen Zielpfad ein.",
    backupRestoreNote:
      "Die Wiederherstellung führt der Betreiber vom Ziel aus durch, während der Server gestoppt ist; nutze den geführten Ablauf unten, dokumentiert in ",
    backupRestoreNoteDocLabel: "docs/operations/restore.md",
    backupRestoreNoteDocHref: "docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ". Die Konsole überschreibt nie eine laufende Datenbank.",
    backupRestoreHeading: "Geführte Wiederherstellung",
    backupRestoreLead:
      "Nutze das, wenn der Start eine beschädigte Datenbank ablehnt oder du auf ein bekannt gutes Backup zurückgehen musst.",
    backupRestoreStepStop: "Stoppe Beamfall und lass das beschädigte Datenverzeichnis unverändert.",
    backupRestoreStepChoose:
      "Wähle das neueste vertrauenswürdige Manifest, meist unter <data-dir>/backups/pre-migration oder im oben angezeigten Backup-Ziel.",
    backupRestoreStepRun:
      "Führe den Offline-Wiederherstellungsbefehl auf dem Server-Host aus; er lehnt eine aktive Server-Sperre ab und prüft die Manifest-Hashes, bevor er das Datenverzeichnis anfasst.",
    backupRestoreStepStart:
      "Starte Beamfall wieder und behalte den Snapshot vor der Wiederherstellung, bis der wiederhergestellte Server überprüft ist.",
    backupRestoreSafety:
      "Die Konsole überschreibt nie eine laufende Datenbank: Eine vorbereitete Wiederherstellung wird beim nächsten Start angewendet, bevor die Datenbank geöffnet wird.",
  },
  es: {
    backupHeading: "Copias de seguridad",
    backupLead:
      "Ejecuta una copia de seguridad de la base de datos en un destino del servidor. La operación devuelve la ruta del manifiesto escrito, que se muestra abajo como registro de la ejecución.",
    backupDestLabel: "Destino",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Ejecutar copia de seguridad",
    backupRunning: "Ejecutando…",
    backupManifestLabel: "Último manifiesto",
    backupNoneYet: "Aún no se ha ejecutado ninguna copia de seguridad desde aquí.",
    backupFailed: "No se pudo ejecutar la copia de seguridad.",
    backupNeedDest: "Introduce una ruta de destino.",
    backupRestoreNote:
      "La restauración la realiza el operador desde el destino con el servidor detenido; usa el flujo de restauración guiada de abajo, documentado en ",
    backupRestoreNoteDocLabel: "docs/operations/restore.md",
    backupRestoreNoteDocHref: "docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ". La Consola nunca sobrescribe una base de datos activa.",
    backupRestoreHeading: "Restauración guiada",
    backupRestoreLead:
      "Úsala cuando el inicio rechace una base de datos dañada o necesites volver a una copia de seguridad en buen estado.",
    backupRestoreStepStop: "Detén Beamfall y deja en su sitio el directorio de datos dañado.",
    backupRestoreStepChoose:
      "Elige el manifiesto de confianza más reciente, normalmente en <data-dir>/backups/pre-migration o en el destino de copia de seguridad indicado arriba.",
    backupRestoreStepRun:
      "Ejecuta el comando de restauración sin conexión en el host del servidor; rechaza un bloqueo de servidor activo y verifica los hashes del manifiesto antes de tocar el directorio de datos.",
    backupRestoreStepStart:
      "Vuelve a iniciar Beamfall y conserva la instantánea previa a la restauración hasta verificar el servidor restaurado.",
    backupRestoreSafety:
      "La Consola nunca sobrescribe una base de datos activa: una restauración preparada se aplica en el próximo inicio, antes de abrir la base de datos.",
  },
  pt: {
    backupHeading: "Backups",
    backupLead:
      "Execute um backup do banco de dados para um destino no servidor. A operação retorna o caminho do manifesto gravado, exibido abaixo como registro da execução.",
    backupDestLabel: "Destino",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Executar backup",
    backupRunning: "Executando…",
    backupManifestLabel: "Último manifesto",
    backupNoneYet: "Nenhum backup foi executado a partir daqui ainda.",
    backupFailed: "Não foi possível executar o backup.",
    backupNeedDest: "Informe um caminho de destino.",
    backupRestoreNote:
      "A restauração é feita pelo operador a partir do destino, com o servidor parado; use o fluxo de restauração guiada abaixo, documentado em ",
    backupRestoreNoteDocLabel: "docs/operations/restore.md",
    backupRestoreNoteDocHref: "docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ". O Console nunca sobrescreve um banco de dados em uso.",
    backupRestoreHeading: "Restauração guiada",
    backupRestoreLead:
      "Use quando a inicialização recusar um banco de dados corrompido ou você precisar voltar a um backup em bom estado.",
    backupRestoreStepStop: "Pare o Beamfall e deixe o diretório de dados corrompido no lugar.",
    backupRestoreStepChoose:
      "Escolha o manifesto confiável mais recente, normalmente em <data-dir>/backups/pre-migration ou no destino de backup mostrado acima.",
    backupRestoreStepRun:
      "Execute o comando de restauração offline no host do servidor; ele recusa um bloqueio de servidor ativo e verifica os hashes do manifesto antes de tocar no diretório de dados.",
    backupRestoreStepStart:
      "Inicie o Beamfall novamente e mantenha o snapshot anterior à restauração até que o servidor restaurado seja verificado.",
    backupRestoreSafety:
      "O Console nunca sobrescreve um banco de dados em uso: uma restauração preparada é aplicada na próxima inicialização, antes de o banco de dados ser aberto.",
  },
} as const;

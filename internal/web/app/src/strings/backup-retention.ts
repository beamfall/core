export const backupRetentionStrings = {
  en: {
    // WEB-5L — Backup / metadata export / Trash policy / Activity undo.
    backupHeading: "Backups",
    backupLead:
      "Back up the database to a folder on the server. When the backup finishes, its location appears below.",
    backupDestLabel: "Destination",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Run backup",
    backupRunning: "Running…",
    backupManifestLabel: "Latest manifest",
    backupNoneYet: "No backup has been run from here yet.",
    backupFailed: "The backup could not be run.",
    backupNeedDest: "Enter a destination path.",
    backupRestoreNote: "For every step in detail, read the ",
    backupRestoreNoteDocLabel: "restore guide",
    backupRestoreNoteDocHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ".",
    backupRestoreHeading: "Guided restore",
    backupRestoreLead:
      "Use this when startup refuses a corrupt database or you need to roll back to a known-good backup.",
    backupRestoreStepStop: "Stop Beamfall and leave the corrupt data directory in place.",
    backupRestoreStepChoose:
      "Choose the newest backup you trust, usually under <data-dir>/backups/pre-migration or the destination shown above.",
    backupRestoreStepRun:
      "Run the offline restore command on the server. It won't run while Beamfall is running, and it checks the backup before it touches the data folder.",
    backupRestoreStepStart:
      "Start Beamfall again and keep the pre-restore snapshot until the restored server is verified.",
    backupRestoreSafety:
      "The Console never overwrites a live database: a staged restore applies on the next start, before the database opens.",
  },
  fr: {
    backupHeading: "Sauvegardes",
    backupLead:
      "Sauvegardez la base de données dans un dossier du serveur. Une fois la sauvegarde terminée, son emplacement s’affiche ci-dessous.",
    backupDestLabel: "Destination",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Lancer la sauvegarde",
    backupRunning: "En cours…",
    backupManifestLabel: "Dernier manifeste",
    backupNoneYet: "Aucune sauvegarde n’a encore été lancée depuis ici.",
    backupFailed: "La sauvegarde n’a pas pu être lancée.",
    backupNeedDest: "Saisissez un chemin de destination.",
    backupRestoreNote: "Pour le détail de chaque étape, consultez le ",
    backupRestoreNoteDocLabel: "guide de restauration",
    backupRestoreNoteDocHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ".",
    backupRestoreHeading: "Restauration guidée",
    backupRestoreLead:
      "Utilisez-la lorsque le démarrage refuse une base corrompue ou que vous devez revenir à une sauvegarde saine connue.",
    backupRestoreStepStop:
      "Arrêtez Beamfall et laissez le répertoire de données corrompu en place.",
    backupRestoreStepChoose:
      "Choisissez la sauvegarde la plus récente en laquelle vous avez confiance, généralement sous <data-dir>/backups/pre-migration ou dans la destination indiquée ci-dessus.",
    backupRestoreStepRun:
      "Exécutez la commande de restauration hors ligne sur le serveur. Elle ne s’exécute pas tant que Beamfall est en cours d’exécution, et elle vérifie la sauvegarde avant de toucher au dossier de données.",
    backupRestoreStepStart:
      "Redémarrez Beamfall et conservez l’instantané préalable à la restauration jusqu’à ce que le serveur restauré soit vérifié.",
    backupRestoreSafety:
      "La Console n’écrase jamais une base de données active : une restauration planifiée s’applique au prochain démarrage, avant l’ouverture de la base.",
  },
  de: {
    backupHeading: "Backups",
    backupLead:
      "Sichere die Datenbank in einen Ordner auf dem Server. Sobald das Backup fertig ist, erscheint sein Speicherort unten.",
    backupDestLabel: "Ziel",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Backup starten",
    backupRunning: "Läuft…",
    backupManifestLabel: "Letztes Manifest",
    backupNoneYet: "Von hier aus wurde noch kein Backup gestartet.",
    backupFailed: "Das Backup konnte nicht gestartet werden.",
    backupNeedDest: "Gib einen Zielpfad ein.",
    backupRestoreNote: "Alle Schritte im Detail findest du in der ",
    backupRestoreNoteDocLabel: "Anleitung zur Wiederherstellung",
    backupRestoreNoteDocHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ".",
    backupRestoreHeading: "Geführte Wiederherstellung",
    backupRestoreLead:
      "Nutze das, wenn der Start eine beschädigte Datenbank ablehnt oder du auf ein bekannt gutes Backup zurückgehen musst.",
    backupRestoreStepStop: "Stoppe Beamfall und lass das beschädigte Datenverzeichnis unverändert.",
    backupRestoreStepChoose:
      "Wähle das neueste Backup, dem du vertraust, meist unter <data-dir>/backups/pre-migration oder im oben angezeigten Ziel.",
    backupRestoreStepRun:
      "Führe den Offline-Wiederherstellungsbefehl auf dem Server aus. Er läuft nicht, solange Beamfall ausgeführt wird, und prüft das Backup, bevor er den Datenordner anfasst.",
    backupRestoreStepStart:
      "Starte Beamfall wieder und behalte den Snapshot vor der Wiederherstellung, bis der wiederhergestellte Server überprüft ist.",
    backupRestoreSafety:
      "Die Konsole überschreibt nie eine laufende Datenbank: Eine vorbereitete Wiederherstellung wird beim nächsten Start angewendet, bevor die Datenbank geöffnet wird.",
  },
  es: {
    backupHeading: "Copias de seguridad",
    backupLead:
      "Haz una copia de seguridad de la base de datos en una carpeta del servidor. Cuando termine, su ubicación aparecerá abajo.",
    backupDestLabel: "Destino",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Ejecutar copia de seguridad",
    backupRunning: "Ejecutando…",
    backupManifestLabel: "Último manifiesto",
    backupNoneYet: "Aún no se ha ejecutado ninguna copia de seguridad desde aquí.",
    backupFailed: "No se pudo ejecutar la copia de seguridad.",
    backupNeedDest: "Introduce una ruta de destino.",
    backupRestoreNote: "Para ver cada paso en detalle, lee la ",
    backupRestoreNoteDocLabel: "guía de restauración",
    backupRestoreNoteDocHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ".",
    backupRestoreHeading: "Restauración guiada",
    backupRestoreLead:
      "Úsala cuando el inicio rechace una base de datos dañada o necesites volver a una copia de seguridad en buen estado.",
    backupRestoreStepStop: "Detén Beamfall y deja en su sitio el directorio de datos dañado.",
    backupRestoreStepChoose:
      "Elige la copia de seguridad más reciente en la que confíes, normalmente en <data-dir>/backups/pre-migration o en el destino indicado arriba.",
    backupRestoreStepRun:
      "Ejecuta el comando de restauración sin conexión en el servidor. No se ejecuta mientras Beamfall está en marcha, y comprueba la copia de seguridad antes de tocar la carpeta de datos.",
    backupRestoreStepStart:
      "Vuelve a iniciar Beamfall y conserva la instantánea previa a la restauración hasta verificar el servidor restaurado.",
    backupRestoreSafety:
      "La Consola nunca sobrescribe una base de datos activa: una restauración preparada se aplica en el próximo inicio, antes de abrir la base de datos.",
  },
  pt: {
    backupHeading: "Backups",
    backupLead:
      "Faça backup do banco de dados em uma pasta no servidor. Quando o backup terminar, o local dele aparece abaixo.",
    backupDestLabel: "Destino",
    backupDestPlaceholder: "/var/lib/beamfall/backups/2026-06-20",
    backupRun: "Executar backup",
    backupRunning: "Executando…",
    backupManifestLabel: "Último manifesto",
    backupNoneYet: "Nenhum backup foi executado a partir daqui ainda.",
    backupFailed: "Não foi possível executar o backup.",
    backupNeedDest: "Informe um caminho de destino.",
    backupRestoreNote: "Para ver cada etapa em detalhes, leia o ",
    backupRestoreNoteDocLabel: "guia de restauração",
    backupRestoreNoteDocHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
    backupRestoreNoteAfterDoc: ".",
    backupRestoreHeading: "Restauração guiada",
    backupRestoreLead:
      "Use quando a inicialização recusar um banco de dados corrompido ou você precisar voltar a um backup em bom estado.",
    backupRestoreStepStop: "Pare o Beamfall e deixe o diretório de dados corrompido no lugar.",
    backupRestoreStepChoose:
      "Escolha o backup mais recente em que você confia, normalmente em <data-dir>/backups/pre-migration ou no destino mostrado acima.",
    backupRestoreStepRun:
      "Execute o comando de restauração offline no servidor. Ele não é executado enquanto o Beamfall está em execução e verifica o backup antes de tocar na pasta de dados.",
    backupRestoreStepStart:
      "Inicie o Beamfall novamente e mantenha o snapshot anterior à restauração até que o servidor restaurado seja verificado.",
    backupRestoreSafety:
      "O Console nunca sobrescreve um banco de dados em uso: uma restauração preparada é aplicada na próxima inicialização, antes de o banco de dados ser aberto.",
  },
} as const;

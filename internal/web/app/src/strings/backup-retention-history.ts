export const backupRetentionHistoryStrings = {
  en: {
    // MIGREV-3 — Backup panel: history + verify + manifest + restore assistant.
    backupHistoryHeading: "Backup history",
    backupHistoryLead:
      "The run record of the backup sets written to this destination: when each ran, its size, and its last integrity verify status. Enter a destination and load its history, or run a backup to publish a new set.",
    backupHistoryLoad: "Load history",
    backupHistoryLoading: "Loading…",
    backupHistoryEmpty: "No backup sets under this destination yet.",
    backupHistoryNeedDest: "Enter a backup destination to load its history.",
    backupHistoryUnavailable: "Could not read the backup history for this destination.",
    backupHistoryCount: "{count} sets",
    backupColSet: "Set",
    backupColCaptured: "Captured",
    backupColSize: "Size",
    backupColVerify: "Verify",
    backupVerifyAction: "Verify",
    backupVerifyRunning: "Verifying…",
    backupVerified: "Verified",
    backupVerifyFailed: "Failed",
    backupVerifyNotRun: "Not verified",
    backupVerifyPassBanner:
      "Integrity check passed. All files present and every content hash matches the manifest. This set is safe to restore.",
    backupVerifyFailBanner:
      "Integrity check failed. This set is incomplete or was modified after capture and cannot be trusted for restore.",
    backupManifestHeading: "Manifest detail",
    backupManifestLead:
      "The contents of the selected set: its published manifest path and the recovery-key exclusion that restore depends on. Select a set from the history above.",
    backupManifestNone: "Select a backup set to see its manifest detail.",
    backupManifestPathLabel: "Manifest path",
    backupKeysCalloutTitle: "Recovery Keys are NOT in this backup",
    backupKeysCalloutBody:
      "This set contains no key material by design. If you lose your Recovery Keys, restoring this backup will not recover encrypted content. Save your Recovery Keys separately and store them somewhere this backup is not.",
  },
  fr: {
    backupHistoryHeading: "Historique des sauvegardes",
    backupHistoryLead:
      "La trace d’exécution des jeux de sauvegarde écrits dans cette destination : date de chaque exécution, taille et dernier état de vérification d’intégrité. Saisissez une destination et chargez son historique, ou lancez une sauvegarde pour publier un nouveau jeu.",
    backupHistoryLoad: "Charger l’historique",
    backupHistoryLoading: "Chargement…",
    backupHistoryEmpty: "Aucun jeu de sauvegarde dans cette destination pour l’instant.",
    backupHistoryNeedDest: "Saisissez une destination de sauvegarde pour charger son historique.",
    backupHistoryUnavailable:
      "Impossible de lire l’historique des sauvegardes de cette destination.",
    backupHistoryCount: "{count} jeux",
    backupColSet: "Jeu",
    backupColCaptured: "Capturé",
    backupColSize: "Taille",
    backupColVerify: "Vérification",
    backupVerifyAction: "Vérifier",
    backupVerifyRunning: "Vérification…",
    backupVerified: "Vérifié",
    backupVerifyFailed: "Échec",
    backupVerifyNotRun: "Non vérifié",
    backupVerifyPassBanner:
      "Vérification d’intégrité réussie. Tous les fichiers sont présents et chaque empreinte de contenu correspond au manifeste. Ce jeu peut être restauré en toute sécurité.",
    backupVerifyFailBanner:
      "Échec de la vérification d’intégrité. Ce jeu est incomplet ou a été modifié après sa capture ; on ne peut pas s’y fier pour une restauration.",
    backupManifestHeading: "Détail du manifeste",
    backupManifestLead:
      "Le contenu du jeu sélectionné : le chemin de son manifeste publié et l’exclusion des clés de récupération dont dépend la restauration. Sélectionnez un jeu dans l’historique ci-dessus.",
    backupManifestNone: "Sélectionnez un jeu de sauvegarde pour voir le détail de son manifeste.",
    backupManifestPathLabel: "Chemin du manifeste",
    backupKeysCalloutTitle: "Les clés de récupération ne sont PAS dans cette sauvegarde",
    backupKeysCalloutBody:
      "Ce jeu ne contient aucun matériel de clé, par conception. Si vous perdez vos clés de récupération, restaurer cette sauvegarde ne permettra pas de récupérer le contenu chiffré. Enregistrez vos clés de récupération séparément, dans un endroit qui n’est pas celui de cette sauvegarde.",
  },
  de: {
    backupHistoryHeading: "Backup-Verlauf",
    backupHistoryLead:
      "Das Ausführungsprotokoll der in dieses Ziel geschriebenen Backup-Sets: wann jedes lief, seine Größe und sein letzter Integritätsprüfstatus. Gib ein Ziel ein und lade den Verlauf, oder starte ein Backup, um ein neues Set zu veröffentlichen.",
    backupHistoryLoad: "Verlauf laden",
    backupHistoryLoading: "Wird geladen…",
    backupHistoryEmpty: "In diesem Ziel gibt es noch keine Backup-Sets.",
    backupHistoryNeedDest: "Gib ein Backup-Ziel ein, um den Verlauf zu laden.",
    backupHistoryUnavailable: "Der Backup-Verlauf dieses Ziels konnte nicht gelesen werden.",
    backupHistoryCount: "{count} Sets",
    backupColSet: "Set",
    backupColCaptured: "Erstellt",
    backupColSize: "Größe",
    backupColVerify: "Prüfung",
    backupVerifyAction: "Prüfen",
    backupVerifyRunning: "Wird geprüft…",
    backupVerified: "Geprüft",
    backupVerifyFailed: "Fehlgeschlagen",
    backupVerifyNotRun: "Nicht geprüft",
    backupVerifyPassBanner:
      "Integritätsprüfung bestanden. Alle Dateien sind vorhanden und jeder Inhalts-Hash stimmt mit dem Manifest überein. Dieses Set lässt sich sicher wiederherstellen.",
    backupVerifyFailBanner:
      "Integritätsprüfung fehlgeschlagen. Dieses Set ist unvollständig oder wurde nach der Erstellung verändert und kann für die Wiederherstellung nicht als vertrauenswürdig gelten.",
    backupManifestHeading: "Manifest-Details",
    backupManifestLead:
      "Der Inhalt des ausgewählten Sets: sein veröffentlichter Manifest-Pfad und der Ausschluss der Wiederherstellungsschlüssel, auf dem die Wiederherstellung beruht. Wähle oben im Verlauf ein Set aus.",
    backupManifestNone: "Wähle ein Backup-Set aus, um die Manifest-Details zu sehen.",
    backupManifestPathLabel: "Manifest-Pfad",
    backupKeysCalloutTitle: "Wiederherstellungsschlüssel sind NICHT in diesem Backup enthalten",
    backupKeysCalloutBody:
      "Dieses Set enthält konstruktionsbedingt kein Schlüsselmaterial. Wenn du deine Wiederherstellungsschlüssel verlierst, lassen sich verschlüsselte Inhalte durch das Zurückspielen dieses Backups nicht wiederherstellen. Speichere deine Wiederherstellungsschlüssel getrennt und an einem Ort, an dem dieses Backup nicht liegt.",
  },
  es: {
    backupHistoryHeading: "Historial de copias de seguridad",
    backupHistoryLead:
      "El registro de ejecución de los conjuntos de copia escritos en este destino: cuándo se ejecutó cada uno, su tamaño y el estado de su última verificación de integridad. Introduce un destino y carga su historial, o ejecuta una copia para publicar un conjunto nuevo.",
    backupHistoryLoad: "Cargar historial",
    backupHistoryLoading: "Cargando…",
    backupHistoryEmpty: "Aún no hay conjuntos de copia de seguridad en este destino.",
    backupHistoryNeedDest: "Introduce un destino de copia de seguridad para cargar su historial.",
    backupHistoryUnavailable:
      "No se pudo leer el historial de copias de seguridad de este destino.",
    backupHistoryCount: "{count} conjuntos",
    backupColSet: "Conjunto",
    backupColCaptured: "Capturado",
    backupColSize: "Tamaño",
    backupColVerify: "Verificación",
    backupVerifyAction: "Verificar",
    backupVerifyRunning: "Verificando…",
    backupVerified: "Verificado",
    backupVerifyFailed: "Fallido",
    backupVerifyNotRun: "No verificado",
    backupVerifyPassBanner:
      "Verificación de integridad superada. Todos los archivos están presentes y cada hash de contenido coincide con el manifiesto. Este conjunto se puede restaurar con seguridad.",
    backupVerifyFailBanner:
      "La verificación de integridad ha fallado. Este conjunto está incompleto o se modificó tras la captura, y no es fiable para una restauración.",
    backupManifestHeading: "Detalle del manifiesto",
    backupManifestLead:
      "El contenido del conjunto seleccionado: la ruta de su manifiesto publicado y la exclusión de las claves de recuperación de la que depende la restauración. Selecciona un conjunto en el historial de arriba.",
    backupManifestNone:
      "Selecciona un conjunto de copia de seguridad para ver el detalle de su manifiesto.",
    backupManifestPathLabel: "Ruta del manifiesto",
    backupKeysCalloutTitle: "Las claves de recuperación NO están en esta copia de seguridad",
    backupKeysCalloutBody:
      "Este conjunto no contiene material de claves, por diseño. Si pierdes tus claves de recuperación, restaurar esta copia no recuperará el contenido cifrado. Guarda tus claves de recuperación por separado, en un lugar donde no esté esta copia de seguridad.",
  },
  pt: {
    backupHistoryHeading: "Histórico de backups",
    backupHistoryLead:
      "O registro de execução dos conjuntos de backup gravados neste destino: quando cada um foi executado, seu tamanho e o status da última verificação de integridade. Informe um destino e carregue o histórico, ou execute um backup para publicar um novo conjunto.",
    backupHistoryLoad: "Carregar histórico",
    backupHistoryLoading: "Carregando…",
    backupHistoryEmpty: "Ainda não há conjuntos de backup neste destino.",
    backupHistoryNeedDest: "Informe um destino de backup para carregar o histórico.",
    backupHistoryUnavailable: "Não foi possível ler o histórico de backups deste destino.",
    backupHistoryCount: "{count} conjuntos",
    backupColSet: "Conjunto",
    backupColCaptured: "Capturado",
    backupColSize: "Tamanho",
    backupColVerify: "Verificação",
    backupVerifyAction: "Verificar",
    backupVerifyRunning: "Verificando…",
    backupVerified: "Verificado",
    backupVerifyFailed: "Falhou",
    backupVerifyNotRun: "Não verificado",
    backupVerifyPassBanner:
      "Verificação de integridade aprovada. Todos os arquivos estão presentes e cada hash de conteúdo corresponde ao manifesto. Este conjunto pode ser restaurado com segurança.",
    backupVerifyFailBanner:
      "A verificação de integridade falhou. Este conjunto está incompleto ou foi modificado após a captura e não é confiável para restauração.",
    backupManifestHeading: "Detalhes do manifesto",
    backupManifestLead:
      "O conteúdo do conjunto selecionado: o caminho do manifesto publicado e a exclusão das chaves de recuperação da qual a restauração depende. Selecione um conjunto no histórico acima.",
    backupManifestNone: "Selecione um conjunto de backup para ver os detalhes do manifesto.",
    backupManifestPathLabel: "Caminho do manifesto",
    backupKeysCalloutTitle: "As chaves de recuperação NÃO estão neste backup",
    backupKeysCalloutBody:
      "Este conjunto não contém material de chaves, por design. Se você perder suas chaves de recuperação, restaurar este backup não recuperará o conteúdo criptografado. Guarde suas chaves de recuperação separadamente, em um local onde este backup não esteja.",
  },
} as const;

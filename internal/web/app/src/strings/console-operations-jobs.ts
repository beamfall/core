import { consoleOperationsJobKindsStrings } from "./console-operations-job-kinds";

// CORE-2109 — Console job queue: what each job does, where it runs, and Scan now.
export const consoleOperationsJobsStrings = {
  en: {
    ...consoleOperationsJobKindsStrings.en,
    jobKindDescScan: "Walks a library folder and imports new or changed media files.",
    jobKindDescScanBackfill:
      "Walks a library folder and refreshes metadata for media already imported.",
    jobKindDescSubscriptionPoll: "Checks every Subscription for new items to fetch.",
    jobKindDescQBittorrentIngestPoll: "Checks qBittorrent for finished downloads and imports them.",
    jobKindDescMigration: "Imports data from another media server through the Migration Wizard.",
    jobKindDescRetentionSweep:
      "Permanently removes trash past its retention window and the records and artwork orphaned by it.",
    jobKindDescEventsCompaction:
      "Prunes disposable events.db rows older than the retention cutoff.",
    jobKindDescDatabaseBackup: "Writes a backup of the server databases.",
    jobKindDescArtworkSweep: "Fills in missing posters and artwork across your libraries.",
    jobKindDescAssetSweep:
      "Creates missing thumbnails, previews and other images when the server has spare capacity.",
    jobKindDescDecodeVerify: "Samples media files and checks that they still decode cleanly.",
    jobKindDescSkipDetectSweep:
      "Finds skippable segments such as intros and credits in newly scanned media.",
    jobKindDescPlaybackPrecompute:
      "Prepares playback renditions ahead of time so playback starts without transcoding.",
    jobKindDescPlaybackPrecomputeEvict:
      "Evicts precomputed playback renditions to keep the cache within its disk budget.",
    jobKindDescPlaybackComposedSweep:
      "Queues missing composed renditions for libraries whose plugin composes pictures.",
    jobKindDescOfflinePackBuild: "Packages media into an Offline Pack for download.",
    jobKindDescOfflinePackExpire: "Removes expired Offline Packs.",
    jobKindDescPhotoExport: "Exports photos in bulk.",
    jobKindDescConvertManagedFile: "Converts a media file into a managed file.",
    jobKindDescSubtitleOffsetPrune:
      "Removes stored subtitle timing corrections for subtitle tracks that no longer exist.",
    jobTargetLibrary: "Library",
    jobTargetFolder: "Folder",
    jobTargetBackfill: "Metadata refresh",
    jobTargetAllLibraries: "All libraries",
    jobQueueScanHelp:
      "Rescan all libraries queues a scan of every library folder. A folder that already has a scan waiting is not queued twice.",
    jobWhenQueued: "Queued {when}",
    jobWhenStarted: "Started {when}",
    jobWhenFinished: "Finished {when}",
    jobSinceFirstStart: "{duration} since first start",
    jobTook: "took {duration}",
    jobTookOverAttempts: "took {duration} over {attempts} attempts",
    jobAttempt: "attempt {attempts}",
    jobDurationSeconds: "{seconds} s",
    jobDurationMinutes: "{minutes} min {seconds} s",
    jobDurationHours: "{hours} h {minutes} min",
    jobRunCount: "{count, plural, one {# run} other {# runs}}",
    jobFailureCount: "{count, plural, one {# failure} other {# failures}}",
    jobHistorySummary:
      "{count, plural, one {Finished work (# kind of job)} other {Finished work (# kinds of job)}}",
    jobNothingActive: "Nothing is running or waiting, and nothing has failed.",
  },
  fr: {
    ...consoleOperationsJobKindsStrings.fr,
    jobKindDescScan:
      "Parcourt un dossier de bibliothèque et importe les fichiers multimédias nouveaux ou modifiés.",
    jobKindDescScanBackfill:
      "Parcourt un dossier de bibliothèque et actualise les métadonnées des médias déjà importés.",
    jobKindDescSubscriptionPoll:
      "Vérifie chaque abonnement pour trouver de nouveaux éléments à récupérer.",
    jobKindDescQBittorrentIngestPoll:
      "Vérifie les téléchargements terminés dans qBittorrent et les importe.",
    jobKindDescMigration:
      "Importe les données d’un autre serveur multimédia via l’assistant de migration.",
    jobKindDescRetentionSweep:
      "Supprime définitivement le contenu de la corbeille dont la durée de conservation est dépassée, ainsi que les enregistrements et illustrations devenus orphelins.",
    jobKindDescEventsCompaction:
      "Purge les lignes jetables d’events.db antérieures à la limite de conservation.",
    jobKindDescDatabaseBackup: "Écrit une sauvegarde des bases de données du serveur.",
    jobKindDescArtworkSweep:
      "Complète les affiches et illustrations manquantes dans vos bibliothèques.",
    jobKindDescAssetSweep:
      "Crée les miniatures, aperçus et autres images manquants quand le serveur a de la capacité libre.",
    jobKindDescDecodeVerify:
      "Échantillonne des fichiers multimédias et vérifie qu’ils se décodent toujours correctement.",
    jobKindDescSkipDetectSweep:
      "Repère les segments à ignorer, comme les génériques de début et de fin, dans les médias récemment analysés.",
    jobKindDescPlaybackPrecompute:
      "Prépare les versions de lecture à l’avance pour que la lecture démarre sans transcodage.",
    jobKindDescPlaybackPrecomputeEvict:
      "Évince des versions de lecture précalculées pour maintenir le cache dans son budget disque.",
    jobKindDescPlaybackComposedSweep:
      "Met en file les versions composées manquantes pour les bibliothèques dont le plugin compose des images.",
    jobKindDescOfflinePackBuild: "Regroupe des médias dans un pack hors ligne à télécharger.",
    jobKindDescOfflinePackExpire: "Supprime les packs hors ligne expirés.",
    jobKindDescPhotoExport: "Exporte des photos par lot.",
    jobKindDescConvertManagedFile: "Convertit un fichier multimédia en fichier géré.",
    jobKindDescSubtitleOffsetPrune:
      "Supprime les corrections de synchronisation enregistrées pour les pistes de sous-titres qui n’existent plus.",
    jobTargetLibrary: "Bibliothèque",
    jobTargetFolder: "Dossier",
    jobTargetBackfill: "Actualisation des métadonnées",
    jobTargetAllLibraries: "Toutes les bibliothèques",
    jobQueueScanHelp:
      "« Réanalyser toutes les bibliothèques » met en file une analyse de chaque dossier de bibliothèque. Un dossier qui a déjà une analyse en attente n’est pas mis en file deux fois.",
    jobWhenQueued: "En file {when}",
    jobWhenStarted: "Démarré {when}",
    jobWhenFinished: "Terminé {when}",
    jobSinceFirstStart: "{duration} depuis le premier démarrage",
    jobTook: "durée {duration}",
    jobTookOverAttempts: "durée {duration} sur {attempts} tentatives",
    jobAttempt: "tentative {attempts}",
    jobDurationSeconds: "{seconds} s",
    jobDurationMinutes: "{minutes} min {seconds} s",
    jobDurationHours: "{hours} h {minutes} min",
    jobRunCount: "{count, plural, one {# exécution} other {# exécutions}}",
    jobFailureCount: "{count, plural, one {# échec} other {# échecs}}",
    jobHistorySummary:
      "{count, plural, one {Travail terminé (# type de tâche)} other {Travail terminé (# types de tâche)}}",
    jobNothingActive: "Rien n’est en cours ni en attente, et rien n’a échoué.",
  },
  de: {
    ...consoleOperationsJobKindsStrings.de,
    jobKindDescScan:
      "Durchläuft einen Bibliotheksordner und importiert neue oder geänderte Mediendateien.",
    jobKindDescScanBackfill:
      "Durchläuft einen Bibliotheksordner und aktualisiert die Metadaten bereits importierter Medien.",
    jobKindDescSubscriptionPoll: "Prüft jedes Abonnement auf neue Elemente zum Abrufen.",
    jobKindDescQBittorrentIngestPoll:
      "Prüft qBittorrent auf abgeschlossene Downloads und importiert sie.",
    jobKindDescMigration:
      "Importiert Daten von einem anderen Medienserver über den Migrationsassistenten.",
    jobKindDescRetentionSweep:
      "Entfernt endgültig Papierkorbinhalte, deren Aufbewahrungsfrist abgelaufen ist, sowie die dadurch verwaisten Einträge und Artworks.",
    jobKindDescEventsCompaction:
      "Entfernt verzichtbare Zeilen aus events.db, die älter als die Aufbewahrungsgrenze sind.",
    jobKindDescDatabaseBackup: "Schreibt eine Sicherung der Server-Datenbanken.",
    jobKindDescArtworkSweep: "Ergänzt fehlende Poster und Artworks in deinen Bibliotheken.",
    jobKindDescAssetSweep:
      "Erstellt fehlende Vorschaubilder, Vorschauen und andere Bilder, wenn der Server freie Kapazität hat.",
    jobKindDescDecodeVerify:
      "Prüft stichprobenartig Mediendateien darauf, ob sie sich noch fehlerfrei decodieren lassen.",
    jobKindDescSkipDetectSweep:
      "Findet überspringbare Segmente wie Intros und Abspänne in neu gescannten Medien.",
    jobKindDescPlaybackPrecompute:
      "Bereitet Wiedergabeversionen im Voraus vor, damit die Wiedergabe ohne Transcodierung startet.",
    jobKindDescPlaybackPrecomputeEvict:
      "Entfernt vorberechnete Wiedergabeversionen, damit der Cache in seinem Speicherbudget bleibt.",
    jobKindDescPlaybackComposedSweep:
      "Reiht fehlende zusammengesetzte Versionen für Bibliotheken ein, deren Plugin Bilder zusammensetzt.",
    jobKindDescOfflinePackBuild: "Packt Medien in ein Offline-Paket zum Herunterladen.",
    jobKindDescOfflinePackExpire: "Entfernt abgelaufene Offline-Pakete.",
    jobKindDescPhotoExport: "Exportiert Fotos gesammelt.",
    jobKindDescConvertManagedFile: "Konvertiert eine Mediendatei in eine verwaltete Datei.",
    jobKindDescSubtitleOffsetPrune:
      "Entfernt gespeicherte Untertitel-Zeitkorrekturen für Untertitelspuren, die es nicht mehr gibt.",
    jobTargetLibrary: "Bibliothek",
    jobTargetFolder: "Ordner",
    jobTargetBackfill: "Metadaten-Aktualisierung",
    jobTargetAllLibraries: "Alle Bibliotheken",
    jobQueueScanHelp:
      "„Alle Bibliotheken erneut scannen“ reiht einen Scan für jeden Bibliotheksordner ein. Ein Ordner, für den bereits ein Scan wartet, wird nicht doppelt eingereiht.",
    jobWhenQueued: "Eingereiht {when}",
    jobWhenStarted: "Gestartet {when}",
    jobWhenFinished: "Beendet {when}",
    jobSinceFirstStart: "{duration} seit dem ersten Start",
    jobTook: "Dauer {duration}",
    jobTookOverAttempts: "Dauer {duration} über {attempts} Versuche",
    jobAttempt: "Versuch {attempts}",
    jobDurationSeconds: "{seconds} s",
    jobDurationMinutes: "{minutes} Min. {seconds} s",
    jobDurationHours: "{hours} Std. {minutes} Min.",
    jobRunCount: "{count, plural, one {# Lauf} other {# Läufe}}",
    jobFailureCount: "{count, plural, one {# Fehlschlag} other {# Fehlschläge}}",
    jobHistorySummary:
      "{count, plural, one {Erledigte Arbeit (# Auftragsart)} other {Erledigte Arbeit (# Auftragsarten)}}",
    jobNothingActive: "Nichts läuft oder wartet, und nichts ist fehlgeschlagen.",
  },
  es: {
    ...consoleOperationsJobKindsStrings.es,
    jobKindDescScan:
      "Recorre una carpeta de la biblioteca e importa los archivos multimedia nuevos o modificados.",
    jobKindDescScanBackfill:
      "Recorre una carpeta de la biblioteca y actualiza los metadatos del contenido ya importado.",
    jobKindDescSubscriptionPoll:
      "Comprueba cada suscripción en busca de elementos nuevos que obtener.",
    jobKindDescQBittorrentIngestPoll:
      "Comprueba si qBittorrent tiene descargas terminadas y las importa.",
    jobKindDescMigration:
      "Importa datos de otro servidor multimedia mediante el asistente de migración.",
    jobKindDescRetentionSweep:
      "Elimina definitivamente el contenido de la papelera que superó su periodo de retención, junto con los registros e ilustraciones que quedan huérfanos.",
    jobKindDescEventsCompaction:
      "Purga las filas prescindibles de events.db anteriores al límite de retención.",
    jobKindDescDatabaseBackup: "Escribe una copia de seguridad de las bases de datos del servidor.",
    jobKindDescArtworkSweep: "Completa los pósteres e ilustraciones que faltan en tus bibliotecas.",
    jobKindDescAssetSweep:
      "Crea las miniaturas, vistas previas y otras imágenes que faltan cuando el servidor tiene capacidad libre.",
    jobKindDescDecodeVerify:
      "Toma muestras de archivos multimedia y comprueba que aún se decodifican correctamente.",
    jobKindDescSkipDetectSweep:
      "Busca segmentos omitibles, como intros y créditos, en el contenido analizado recientemente.",
    jobKindDescPlaybackPrecompute:
      "Prepara versiones de reproducción por adelantado para que la reproducción empiece sin transcodificar.",
    jobKindDescPlaybackPrecomputeEvict:
      "Expulsa versiones de reproducción precalculadas para mantener la caché dentro de su presupuesto de disco.",
    jobKindDescPlaybackComposedSweep:
      "Pone en cola las versiones compuestas que faltan en las bibliotecas cuyo plugin compone imágenes.",
    jobKindDescOfflinePackBuild:
      "Empaqueta contenido multimedia en un paquete sin conexión para descargar.",
    jobKindDescOfflinePackExpire: "Elimina los paquetes sin conexión caducados.",
    jobKindDescPhotoExport: "Exporta fotos en bloque.",
    jobKindDescConvertManagedFile: "Convierte un archivo multimedia en un archivo gestionado.",
    jobKindDescSubtitleOffsetPrune:
      "Elimina las correcciones de sincronización guardadas de pistas de subtítulos que ya no existen.",
    jobTargetLibrary: "Biblioteca",
    jobTargetFolder: "Carpeta",
    jobTargetBackfill: "Actualización de metadatos",
    jobTargetAllLibraries: "Todas las bibliotecas",
    jobQueueScanHelp:
      "«Volver a analizar todas las bibliotecas» pone en cola un análisis de cada carpeta de biblioteca. Una carpeta que ya tiene un análisis en espera no se pone en cola dos veces.",
    jobWhenQueued: "En cola {when}",
    jobWhenStarted: "Iniciado {when}",
    jobWhenFinished: "Terminado {when}",
    jobSinceFirstStart: "{duration} desde el primer inicio",
    jobTook: "duró {duration}",
    jobTookOverAttempts: "duró {duration} en {attempts} intentos",
    jobAttempt: "intento {attempts}",
    jobDurationSeconds: "{seconds} s",
    jobDurationMinutes: "{minutes} min {seconds} s",
    jobDurationHours: "{hours} h {minutes} min",
    jobRunCount: "{count, plural, one {# ejecución} other {# ejecuciones}}",
    jobFailureCount: "{count, plural, one {# fallo} other {# fallos}}",
    jobHistorySummary:
      "{count, plural, one {Trabajo terminado (# tipo de tarea)} other {Trabajo terminado (# tipos de tarea)}}",
    jobNothingActive: "Nada está en curso ni en espera, y nada ha fallado.",
  },
  pt: {
    ...consoleOperationsJobKindsStrings.pt,
    jobKindDescScan:
      "Percorre uma pasta da biblioteca e importa arquivos de mídia novos ou alterados.",
    jobKindDescScanBackfill:
      "Percorre uma pasta da biblioteca e atualiza os metadados das mídias já importadas.",
    jobKindDescSubscriptionPoll: "Verifica cada assinatura em busca de novos itens para obter.",
    jobKindDescQBittorrentIngestPoll:
      "Verifica se há downloads concluídos no qBittorrent e os importa.",
    jobKindDescMigration: "Importa dados de outro servidor de mídia pelo assistente de migração.",
    jobKindDescRetentionSweep:
      "Remove definitivamente os itens da lixeira que passaram do período de retenção, além dos registros e artes que ficaram órfãos.",
    jobKindDescEventsCompaction:
      "Remove as linhas descartáveis de events.db anteriores ao limite de retenção.",
    jobKindDescDatabaseBackup: "Grava um backup dos bancos de dados do servidor.",
    jobKindDescArtworkSweep: "Preenche pôsteres e artes que faltam nas suas bibliotecas.",
    jobKindDescAssetSweep:
      "Cria as miniaturas, prévias e outras imagens que faltam quando o servidor tem capacidade livre.",
    jobKindDescDecodeVerify:
      "Analisa amostras de arquivos de mídia e confere se ainda são decodificados sem erros.",
    jobKindDescSkipDetectSweep:
      "Encontra segmentos puláveis, como aberturas e créditos, nas mídias verificadas recentemente.",
    jobKindDescPlaybackPrecompute:
      "Prepara versões de reprodução com antecedência para que a reprodução comece sem transcodificação.",
    jobKindDescPlaybackPrecomputeEvict:
      "Remove versões de reprodução pré-calculadas para manter o cache dentro do orçamento de disco.",
    jobKindDescPlaybackComposedSweep:
      "Coloca na fila as versões compostas que faltam nas bibliotecas cujo plugin compõe imagens.",
    jobKindDescOfflinePackBuild: "Empacota mídias em um pacote offline para download.",
    jobKindDescOfflinePackExpire: "Remove pacotes offline expirados.",
    jobKindDescPhotoExport: "Exporta fotos em lote.",
    jobKindDescConvertManagedFile: "Converte um arquivo de mídia em um arquivo gerenciado.",
    jobKindDescSubtitleOffsetPrune:
      "Remove correções de sincronia salvas de faixas de legenda que não existem mais.",
    jobTargetLibrary: "Biblioteca",
    jobTargetFolder: "Pasta",
    jobTargetBackfill: "Atualização de metadados",
    jobTargetAllLibraries: "Todas as bibliotecas",
    jobQueueScanHelp:
      "“Verificar todas as bibliotecas novamente” coloca na fila uma verificação de cada pasta de biblioteca. Uma pasta que já tem uma verificação aguardando não entra na fila duas vezes.",
    jobWhenQueued: "Na fila {when}",
    jobWhenStarted: "Iniciado {when}",
    jobWhenFinished: "Concluído {when}",
    jobSinceFirstStart: "{duration} desde o primeiro início",
    jobTook: "levou {duration}",
    jobTookOverAttempts: "levou {duration} ao longo de {attempts} tentativas",
    jobAttempt: "tentativa {attempts}",
    jobDurationSeconds: "{seconds} s",
    jobDurationMinutes: "{minutes} min {seconds} s",
    jobDurationHours: "{hours} h {minutes} min",
    jobRunCount: "{count, plural, one {# execução} other {# execuções}}",
    jobFailureCount: "{count, plural, one {# falha} other {# falhas}}",
    jobHistorySummary:
      "{count, plural, one {Trabalho concluído (# tipo de tarefa)} other {Trabalho concluído (# tipos de tarefa)}}",
    jobNothingActive: "Nada está em execução ou aguardando, e nada falhou.",
  },
} as const;

// CORE-2109 — Console job queue: what each job does, where it runs, and Scan now.
export const consoleOperationsJobsStrings = {
  en: {
    jobKindPlaybackPrecompute: "Playback precompute",
    jobKindPlaybackPrecomputeEvict: "Playback cache eviction",
    jobKindPlaybackComposedSweep: "Composed rendition sweep",
    jobKindOfflinePackBuild: "Offline Pack build",
    jobKindOfflinePackExpire: "Offline Pack expiry",
    jobKindPhotoExport: "Photo export",
    jobKindConvertManagedFile: "Managed file conversion",
    jobKindSubtitleOffsetPrune: "Subtitle offset cleanup",
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
      "Generates missing Generated Assets such as thumbnails and previews, when the governor allows.",
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
      "Scan now queues a scan of every library folder. A folder that already has a scan waiting is not queued twice.",
  },
  fr: {
    jobKindPlaybackPrecompute: "Précalcul de lecture",
    jobKindPlaybackPrecomputeEvict: "Éviction du cache de lecture",
    jobKindPlaybackComposedSweep: "Recherche de versions composées",
    jobKindOfflinePackBuild: "Création de pack hors ligne",
    jobKindOfflinePackExpire: "Expiration des packs hors ligne",
    jobKindPhotoExport: "Export de photos",
    jobKindConvertManagedFile: "Conversion de fichier géré",
    jobKindSubtitleOffsetPrune: "Nettoyage des décalages de sous-titres",
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
      "Génère les actifs générés manquants, comme les miniatures et les aperçus, lorsque le gouverneur l’autorise.",
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
      "« Analyser maintenant » met en file une analyse de chaque dossier de bibliothèque. Un dossier qui a déjà une analyse en attente n’est pas mis en file deux fois.",
  },
  de: {
    jobKindPlaybackPrecompute: "Wiedergabe-Vorberechnung",
    jobKindPlaybackPrecomputeEvict: "Wiedergabe-Cache bereinigen",
    jobKindPlaybackComposedSweep: "Zusammengesetzte Versionen prüfen",
    jobKindOfflinePackBuild: "Offline-Paket erstellen",
    jobKindOfflinePackExpire: "Offline-Pakete ablaufen lassen",
    jobKindPhotoExport: "Fotoexport",
    jobKindConvertManagedFile: "Verwaltete Datei konvertieren",
    jobKindSubtitleOffsetPrune: "Untertitelversätze bereinigen",
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
      "Erzeugt fehlende generierte Assets wie Vorschaubilder und Vorschauen, wenn der Governor es zulässt.",
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
      "„Jetzt scannen“ reiht einen Scan für jeden Bibliotheksordner ein. Ein Ordner, für den bereits ein Scan wartet, wird nicht doppelt eingereiht.",
  },
  es: {
    jobKindPlaybackPrecompute: "Precálculo de reproducción",
    jobKindPlaybackPrecomputeEvict: "Expulsión de la caché de reproducción",
    jobKindPlaybackComposedSweep: "Barrido de versiones compuestas",
    jobKindOfflinePackBuild: "Creación de paquete sin conexión",
    jobKindOfflinePackExpire: "Caducidad de paquetes sin conexión",
    jobKindPhotoExport: "Exportación de fotos",
    jobKindConvertManagedFile: "Conversión de archivo gestionado",
    jobKindSubtitleOffsetPrune: "Limpieza de desfases de subtítulos",
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
      "Genera los recursos generados que faltan, como miniaturas y vistas previas, cuando el gobernador lo permite.",
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
      "«Analizar ahora» pone en cola un análisis de cada carpeta de biblioteca. Una carpeta que ya tiene un análisis en espera no se pone en cola dos veces.",
  },
  pt: {
    jobKindPlaybackPrecompute: "Pré-cálculo de reprodução",
    jobKindPlaybackPrecomputeEvict: "Remoção do cache de reprodução",
    jobKindPlaybackComposedSweep: "Varredura de versões compostas",
    jobKindOfflinePackBuild: "Criação de pacote offline",
    jobKindOfflinePackExpire: "Expiração de pacotes offline",
    jobKindPhotoExport: "Exportação de fotos",
    jobKindConvertManagedFile: "Conversão de arquivo gerenciado",
    jobKindSubtitleOffsetPrune: "Limpeza de deslocamentos de legendas",
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
      "Gera os ativos gerados que faltam, como miniaturas e prévias, quando o governador permite.",
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
      "“Verificar agora” coloca na fila uma verificação de cada pasta de biblioteca. Uma pasta que já tem uma verificação aguardando não entra na fila duas vezes.",
  },
} as const;

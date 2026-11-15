export const consoleOperationsStrings = {
  en: {
    updateMigrationCautionLabel: "Migration caution",
    updateMigrationCautionAria: "Migration caution for available Core update",
    updateMigrationImpactUnknown:
      "Migration impact is unknown. The signed release manifest does not represent migrations.",
    updateMigrationPossible:
      "This release may contain forward-only migrations. Backup freshness is unknown.",
    updateBackupRequired: "Take and verify a backup before installing.",
    updateExportNotBackup:
      "Metadata export is optional portability evidence, not a backup substitute.",
    updateRollbackSnapshotNotice:
      "Beamfall writes a plaintext Rollback Snapshot when migrations run; it is a local rollback net, not a backup.",
    updateDowngradeOffline:
      "Downgrade requires an offline restore and never uses in-place rollback.",
    updateScopeCore: "Core",
    updateScopePlugins: "Plugins",
    updateScopeCompatibility: "Compatibility",
    updatePluginCompatibilityUnknown: "Compatibility not checked",
    updateCompatibilityReview: "Review before install",
    updateRecoveryArtifactsHeading: "Three separate artifacts. They are not interchangeable.",
    updateArtifactV2Title: "Backup Set (v2, encrypted)",
    updateArtifactV2Body:
      "Databases plus operational state: secrets, the stream-token key, the pinned TLS identity, config, and integration state. Encrypted as one set; only your BackupKey opens it. The BackupKey and Library Recovery Keys are held separately and are never inside the set.",
    updateArtifactV1Title: "Backup (v1, database-only)",
    updateArtifactV1Body:
      "Databases only. It carries no operational state, so secrets, the TLS identity, and config must be re-provisioned by hand after restore.",
    updateArtifactRollbackTitle: "Rollback Snapshot (pre-migration, plaintext)",
    updateArtifactRollbackBody:
      "Written to this server's own disk before migrations start. Databases only, unencrypted, restored with one command and no key, back to the version this upgrade started from. Not a backup: it never leaves this disk and holds no secrets, TLS identity, or config.",
    updateCreateVerifyBackup: "Create and verify backup",
    updateExportMetadata: "Export metadata",
    updateOfflineRestoreGuidance: "Offline restore guidance",
    updateRollbackSelectVersion:
      "Select the prior Beamfall version you intend to run. Confirm the trusted pre-migration manifest's fromVersions/toVersions tuple belongs to that exact upgrade session; do not guess from its filename.",
    updateRollbackStop: "Stop Beamfall before restore. The command refuses a live server lock.",
    updateRollbackRun:
      "Run the guarded rollback command. It verifies the pre-migration manifest before replacing the database tree:",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Restart Beamfall and verify health and an authenticated read before removing the retained pre-rollback database tree.",
    updateCanonicalRestoreProcedure: "Canonical restore procedure",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
  fr: {
    updateMigrationCautionLabel: "Précaution de migration",
    updateMigrationCautionAria: "Précaution de migration pour la mise à jour de Core disponible",
    updateMigrationImpactUnknown:
      "L’impact de la migration est inconnu. Le manifeste de version signé ne décrit pas les migrations.",
    updateMigrationPossible:
      "Cette version peut contenir des migrations irréversibles. La fraîcheur de la sauvegarde est inconnue.",
    updateBackupRequired: "Créez et vérifiez une sauvegarde avant l’installation.",
    updateExportNotBackup:
      "L’export des métadonnées est une preuve de portabilité facultative, pas un substitut de sauvegarde.",
    updateRollbackSnapshotNotice:
      "Beamfall écrit un Instantané de restauration en clair lors des migrations ; c’est un filet de sécurité local, pas une sauvegarde.",
    updateDowngradeOffline:
      "Le retour à une version antérieure exige une restauration hors ligne et n’utilise jamais de restauration sur place.",
    updateScopeCore: "Core",
    updateScopePlugins: "Plugins",
    updateScopeCompatibility: "Compatibilité",
    updatePluginCompatibilityUnknown: "Compatibilité non vérifiée",
    updateCompatibilityReview: "À examiner avant l’installation",
    updateRecoveryArtifactsHeading: "Trois artefacts distincts. Ils ne sont pas interchangeables.",
    updateArtifactV2Title: "Jeu de sauvegarde (v2, chiffré)",
    updateArtifactV2Body:
      "Bases de données plus état opérationnel : secrets, clé des jetons de flux, identité TLS épinglée, configuration et état des intégrations. Chiffré en un seul jeu ; seule votre BackupKey l’ouvre. La BackupKey et les clés de récupération de bibliothèque sont conservées séparément et ne sont jamais dans le jeu.",
    updateArtifactV1Title: "Sauvegarde (v1, bases de données uniquement)",
    updateArtifactV1Body:
      "Bases de données uniquement. Elle ne contient aucun état opérationnel : les secrets, l’identité TLS et la configuration doivent être reprovisionnés à la main après la restauration.",
    updateArtifactRollbackTitle: "Instantané de restauration (avant migration, en clair)",
    updateArtifactRollbackBody:
      "Écrit sur le disque de ce serveur avant le début des migrations. Bases de données uniquement, non chiffré, restauré en une commande sans clé, vers la version dont cette mise à niveau est partie. Ce n’est pas une sauvegarde : il ne quitte jamais ce disque et ne contient ni secrets, ni identité TLS, ni configuration.",
    updateCreateVerifyBackup: "Créer et vérifier la sauvegarde",
    updateExportMetadata: "Exporter les métadonnées",
    updateOfflineRestoreGuidance: "Guide de restauration hors ligne",
    updateRollbackSelectVersion:
      "Sélectionnez la version antérieure de Beamfall que vous comptez exécuter. Vérifiez que le couple fromVersions/toVersions du manifeste de pré-migration approuvé appartient bien à cette session de mise à niveau ; ne le devinez pas d’après son nom de fichier.",
    updateRollbackStop:
      "Arrêtez Beamfall avant la restauration. La commande refuse un verrou de serveur actif.",
    updateRollbackRun:
      "Exécutez la commande de restauration protégée. Elle vérifie le manifeste de pré-migration avant de remplacer l’arborescence de la base de données :",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Redémarrez Beamfall et vérifiez l’état de santé ainsi qu’une lecture authentifiée avant de supprimer l’arborescence de base de données conservée d’avant la restauration.",
    updateCanonicalRestoreProcedure: "Procédure de restauration canonique",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
  de: {
    updateMigrationCautionLabel: "Migrationshinweis",
    updateMigrationCautionAria: "Migrationshinweis zum verfügbaren Core-Update",
    updateMigrationImpactUnknown:
      "Die Auswirkungen der Migration sind unbekannt. Das signierte Release-Manifest beschreibt keine Migrationen.",
    updateMigrationPossible:
      "Dieses Release kann nur vorwärts gerichtete Migrationen enthalten. Die Aktualität des Backups ist unbekannt.",
    updateBackupRequired: "Erstelle und prüfe vor der Installation ein Backup.",
    updateExportNotBackup:
      "Der Metadaten-Export ist ein optionaler Portabilitätsnachweis und kein Ersatz für ein Backup.",
    updateRollbackSnapshotNotice:
      "Beamfall schreibt beim Ausführen von Migrationen einen unverschlüsselten Rollback-Snapshot; er ist ein lokales Sicherheitsnetz, kein Backup.",
    updateDowngradeOffline:
      "Ein Downgrade erfordert eine Offline-Wiederherstellung und nutzt nie ein direktes Rollback.",
    updateScopeCore: "Core",
    updateScopePlugins: "Plugins",
    updateScopeCompatibility: "Kompatibilität",
    updatePluginCompatibilityUnknown: "Kompatibilität nicht geprüft",
    updateCompatibilityReview: "Vor der Installation prüfen",
    updateRecoveryArtifactsHeading: "Drei getrennte Artefakte. Sie sind nicht austauschbar.",
    updateArtifactV2Title: "Backup-Set (v2, verschlüsselt)",
    updateArtifactV2Body:
      "Datenbanken plus Betriebszustand: Geheimnisse, Stream-Token-Schlüssel, angeheftete TLS-Identität, Konfiguration und Integrationszustand. Als ein Set verschlüsselt; nur dein BackupKey öffnet es. BackupKey und Bibliotheks-Wiederherstellungsschlüssel werden getrennt aufbewahrt und sind nie im Set enthalten.",
    updateArtifactV1Title: "Backup (v1, nur Datenbank)",
    updateArtifactV1Body:
      "Nur Datenbanken. Es enthält keinen Betriebszustand, daher müssen Geheimnisse, TLS-Identität und Konfiguration nach der Wiederherstellung von Hand neu eingerichtet werden.",
    updateArtifactRollbackTitle: "Rollback-Snapshot (vor der Migration, unverschlüsselt)",
    updateArtifactRollbackBody:
      "Wird vor Beginn der Migrationen auf die eigene Festplatte dieses Servers geschrieben. Nur Datenbanken, unverschlüsselt, mit einem Befehl und ohne Schlüssel auf die Version zurückspielbar, von der dieses Upgrade ausging. Kein Backup: Er verlässt diese Festplatte nie und enthält weder Geheimnisse noch TLS-Identität oder Konfiguration.",
    updateCreateVerifyBackup: "Backup erstellen und prüfen",
    updateExportMetadata: "Metadaten exportieren",
    updateOfflineRestoreGuidance: "Hinweise zur Offline-Wiederherstellung",
    updateRollbackSelectVersion:
      "Wähle die frühere Beamfall-Version aus, die du ausführen willst. Bestätige, dass das fromVersions/toVersions-Tupel des vertrauenswürdigen Vor-Migrations-Manifests zu genau dieser Upgrade-Sitzung gehört; rate nicht anhand des Dateinamens.",
    updateRollbackStop:
      "Beende Beamfall vor der Wiederherstellung. Der Befehl verweigert die Ausführung bei aktiver Server-Sperre.",
    updateRollbackRun:
      "Führe den geschützten Rollback-Befehl aus. Er prüft das Vor-Migrations-Manifest, bevor er den Datenbankbaum ersetzt:",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Starte Beamfall neu und prüfe den Zustand sowie einen authentifizierten Lesezugriff, bevor du den aufbewahrten Datenbankbaum von vor dem Rollback entfernst.",
    updateCanonicalRestoreProcedure: "Kanonisches Wiederherstellungsverfahren",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
  es: {
    updateMigrationCautionLabel: "Precaución de migración",
    updateMigrationCautionAria: "Precaución de migración para la actualización de Core disponible",
    updateMigrationImpactUnknown:
      "Se desconoce el impacto de la migración. El manifiesto de versión firmado no describe migraciones.",
    updateMigrationPossible:
      "Esta versión puede contener migraciones que solo avanzan. Se desconoce la antigüedad de la copia de seguridad.",
    updateBackupRequired: "Crea y verifica una copia de seguridad antes de instalar.",
    updateExportNotBackup:
      "La exportación de metadatos es una prueba de portabilidad opcional, no sustituye a una copia de seguridad.",
    updateRollbackSnapshotNotice:
      "Beamfall escribe una Instantánea de reversión sin cifrar cuando se ejecutan migraciones; es una red de seguridad local, no una copia de seguridad.",
    updateDowngradeOffline:
      "Volver a una versión anterior requiere una restauración sin conexión y nunca usa la reversión en el mismo lugar.",
    updateScopeCore: "Core",
    updateScopePlugins: "Plugins",
    updateScopeCompatibility: "Compatibilidad",
    updatePluginCompatibilityUnknown: "Compatibilidad sin comprobar",
    updateCompatibilityReview: "Revisar antes de instalar",
    updateRecoveryArtifactsHeading: "Tres artefactos distintos. No son intercambiables.",
    updateArtifactV2Title: "Conjunto de copia de seguridad (v2, cifrado)",
    updateArtifactV2Body:
      "Bases de datos más estado operativo: secretos, la clave de tokens de transmisión, la identidad TLS fijada, la configuración y el estado de las integraciones. Se cifra como un solo conjunto; solo tu BackupKey lo abre. La BackupKey y las claves de recuperación de biblioteca se guardan por separado y nunca están dentro del conjunto.",
    updateArtifactV1Title: "Copia de seguridad (v1, solo base de datos)",
    updateArtifactV1Body:
      "Solo bases de datos. No incluye estado operativo, así que los secretos, la identidad TLS y la configuración deben reconfigurarse a mano tras restaurar.",
    updateArtifactRollbackTitle: "Instantánea de reversión (previa a la migración, sin cifrar)",
    updateArtifactRollbackBody:
      "Se escribe en el propio disco de este servidor antes de que empiecen las migraciones. Solo bases de datos, sin cifrar, se restaura con un comando y sin clave, a la versión desde la que empezó esta actualización. No es una copia de seguridad: nunca sale de este disco y no contiene secretos, identidad TLS ni configuración.",
    updateCreateVerifyBackup: "Crear y verificar copia de seguridad",
    updateExportMetadata: "Exportar metadatos",
    updateOfflineRestoreGuidance: "Guía de restauración sin conexión",
    updateRollbackSelectVersion:
      "Selecciona la versión anterior de Beamfall que quieres ejecutar. Confirma que la tupla fromVersions/toVersions del manifiesto previo a la migración de confianza pertenece a esa sesión de actualización exacta; no la deduzcas por el nombre del archivo.",
    updateRollbackStop:
      "Detén Beamfall antes de restaurar. El comando se niega a continuar si hay un bloqueo de servidor activo.",
    updateRollbackRun:
      "Ejecuta el comando de reversión protegida. Verifica el manifiesto previo a la migración antes de reemplazar el árbol de la base de datos:",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Reinicia Beamfall y verifica el estado de salud y una lectura autenticada antes de eliminar el árbol de la base de datos conservado de antes de la reversión.",
    updateCanonicalRestoreProcedure: "Procedimiento de restauración canónico",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
  pt: {
    updateMigrationCautionLabel: "Cuidado com a migração",
    updateMigrationCautionAria: "Cuidado com a migração para a atualização do Core disponível",
    updateMigrationImpactUnknown:
      "O impacto da migração é desconhecido. O manifesto de versão assinado não descreve migrações.",
    updateMigrationPossible:
      "Esta versão pode conter migrações sem volta. A atualidade do backup é desconhecida.",
    updateBackupRequired: "Crie e verifique um backup antes de instalar.",
    updateExportNotBackup:
      "A exportação de metadados é uma prova de portabilidade opcional, não substitui um backup.",
    updateRollbackSnapshotNotice:
      "O Beamfall grava um Snapshot de reversão sem criptografia quando as migrações são executadas; ele é uma rede de segurança local, não um backup.",
    updateDowngradeOffline:
      "Voltar para uma versão anterior exige uma restauração offline e nunca usa reversão no local.",
    updateScopeCore: "Core",
    updateScopePlugins: "Plugins",
    updateScopeCompatibility: "Compatibilidade",
    updatePluginCompatibilityUnknown: "Compatibilidade não verificada",
    updateCompatibilityReview: "Revisar antes de instalar",
    updateRecoveryArtifactsHeading: "Três artefatos separados. Eles não são intercambiáveis.",
    updateArtifactV2Title: "Conjunto de backup (v2, criptografado)",
    updateArtifactV2Body:
      "Bancos de dados mais estado operacional: segredos, a chave de tokens de streaming, a identidade TLS fixada, a configuração e o estado das integrações. Criptografado como um único conjunto; só a sua BackupKey o abre. A BackupKey e as chaves de recuperação da biblioteca ficam guardadas separadamente e nunca estão dentro do conjunto.",
    updateArtifactV1Title: "Backup (v1, somente banco de dados)",
    updateArtifactV1Body:
      "Somente bancos de dados. Não inclui estado operacional, então segredos, identidade TLS e configuração precisam ser reconfigurados manualmente após a restauração.",
    updateArtifactRollbackTitle: "Snapshot de reversão (pré-migração, sem criptografia)",
    updateArtifactRollbackBody:
      "Gravado no próprio disco deste servidor antes do início das migrações. Somente bancos de dados, sem criptografia, restaurado com um comando e sem chave, para a versão de onde este upgrade partiu. Não é um backup: ele nunca sai deste disco e não contém segredos, identidade TLS nem configuração.",
    updateCreateVerifyBackup: "Criar e verificar backup",
    updateExportMetadata: "Exportar metadados",
    updateOfflineRestoreGuidance: "Orientações de restauração offline",
    updateRollbackSelectVersion:
      "Selecione a versão anterior do Beamfall que você pretende executar. Confirme que a tupla fromVersions/toVersions do manifesto de pré-migração confiável pertence a essa sessão de upgrade exata; não deduza pelo nome do arquivo.",
    updateRollbackStop:
      "Pare o Beamfall antes de restaurar. O comando recusa continuar se houver um bloqueio de servidor ativo.",
    updateRollbackRun:
      "Execute o comando de reversão protegida. Ele verifica o manifesto de pré-migração antes de substituir a árvore do banco de dados:",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Reinicie o Beamfall e verifique a integridade e uma leitura autenticada antes de remover a árvore do banco de dados retida de antes da reversão.",
    updateCanonicalRestoreProcedure: "Procedimento de restauração canônico",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
} as const;

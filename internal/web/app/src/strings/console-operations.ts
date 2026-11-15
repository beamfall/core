export const consoleOperationsStrings = {
  en: {
    updateMigrationCautionLabel: "Migration caution",
    updateMigrationCautionAria: "Migration caution for available Core update",
    updateMigrationImpactUnknown: "Beamfall can't tell whether this release changes the database.",
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
      "Select the earlier Beamfall version you want to run. Check that the versions listed inside the pre-migration manifest match that exact upgrade. Don't go by the file name.",
    updateRollbackStop:
      "Stop Beamfall before restore. The command won't run while Beamfall is running.",
    updateRollbackRun:
      "Run the rollback command. It checks the pre-migration manifest before it replaces the database folder:",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Restart Beamfall, check that it is healthy and that you can sign in, then remove the old database folder it kept.",
    updateCanonicalRestoreProcedure: "Canonical restore procedure",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
  fr: {
    updateMigrationCautionLabel: "Précaution de migration",
    updateMigrationCautionAria: "Précaution de migration pour la mise à jour de Core disponible",
    updateMigrationImpactUnknown:
      "Beamfall ne peut pas déterminer si cette version modifie la base de données.",
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
      "Sélectionnez la version antérieure de Beamfall que vous voulez exécuter. Vérifiez que les versions indiquées dans le manifeste de pré-migration correspondent exactement à cette mise à niveau. Ne vous fiez pas au nom du fichier.",
    updateRollbackStop:
      "Arrêtez Beamfall avant la restauration. La commande ne s’exécute pas tant que Beamfall est en cours d’exécution.",
    updateRollbackRun:
      "Exécutez la commande de restauration. Elle vérifie le manifeste de pré-migration avant de remplacer le dossier de la base de données :",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Redémarrez Beamfall, vérifiez qu’il fonctionne correctement et que vous pouvez vous connecter, puis supprimez l’ancien dossier de base de données qu’il a conservé.",
    updateCanonicalRestoreProcedure: "Procédure de restauration canonique",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
  de: {
    updateMigrationCautionLabel: "Migrationshinweis",
    updateMigrationCautionAria: "Migrationshinweis zum verfügbaren Core-Update",
    updateMigrationImpactUnknown:
      "Beamfall kann nicht feststellen, ob dieses Release die Datenbank ändert.",
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
      "Wähle die frühere Beamfall-Version aus, die du ausführen willst. Prüfe, ob die im Vor-Migrations-Manifest aufgeführten Versionen zu genau diesem Upgrade passen. Verlass dich nicht auf den Dateinamen.",
    updateRollbackStop:
      "Beende Beamfall vor der Wiederherstellung. Der Befehl läuft nicht, solange Beamfall ausgeführt wird.",
    updateRollbackRun:
      "Führe den Rollback-Befehl aus. Er prüft das Vor-Migrations-Manifest, bevor er den Datenbankordner ersetzt:",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Starte Beamfall neu, prüfe, ob es fehlerfrei läuft und du dich anmelden kannst, und entferne dann den alten Datenbankordner, den es aufbewahrt hat.",
    updateCanonicalRestoreProcedure: "Kanonisches Wiederherstellungsverfahren",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
  es: {
    updateMigrationCautionLabel: "Precaución de migración",
    updateMigrationCautionAria: "Precaución de migración para la actualización de Core disponible",
    updateMigrationImpactUnknown:
      "Beamfall no puede saber si esta versión modifica la base de datos.",
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
      "Selecciona la versión anterior de Beamfall que quieres ejecutar. Comprueba que las versiones indicadas en el manifiesto previo a la migración corresponden exactamente a esa actualización. No te guíes por el nombre del archivo.",
    updateRollbackStop:
      "Detén Beamfall antes de restaurar. El comando no se ejecuta mientras Beamfall está en marcha.",
    updateRollbackRun:
      "Ejecuta el comando de reversión. Comprueba el manifiesto previo a la migración antes de reemplazar la carpeta de la base de datos:",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Reinicia Beamfall, comprueba que funciona correctamente y que puedes iniciar sesión, y luego elimina la carpeta antigua de la base de datos que conservó.",
    updateCanonicalRestoreProcedure: "Procedimiento de restauración canónico",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
  pt: {
    updateMigrationCautionLabel: "Cuidado com a migração",
    updateMigrationCautionAria: "Cuidado com a migração para a atualização do Core disponível",
    updateMigrationImpactUnknown:
      "O Beamfall não consegue saber se esta versão altera o banco de dados.",
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
      "Selecione a versão anterior do Beamfall que você quer executar. Confira se as versões listadas no manifesto de pré-migração correspondem exatamente a esse upgrade. Não se guie pelo nome do arquivo.",
    updateRollbackStop:
      "Pare o Beamfall antes de restaurar. O comando não é executado enquanto o Beamfall está em execução.",
    updateRollbackRun:
      "Execute o comando de reversão. Ele verifica o manifesto de pré-migração antes de substituir a pasta do banco de dados:",
    updateRollbackCommand:
      "beamfall restore --rollback-manifest /path/to/pre-migration/manifest.json --data-dir /var/lib/beamfall",
    updateRollbackRestart:
      "Reinicie o Beamfall, confira se ele está íntegro e se você consegue entrar, e depois remova a pasta antiga do banco de dados que ele manteve.",
    updateCanonicalRestoreProcedure: "Procedimento de restauração canônico",
    updateCanonicalRestoreHref:
      "https://github.com/beamfall/core/blob/main/docs/operations/restore.md",
  },
} as const;

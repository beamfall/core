export const backupOpsStrings = {
  en: {
    // Backup health, restore drill and update gate (CORE-0778, ADR-0192 §5 L3 and §6).
    backupOpsHeading: "Backup health",
    backupOpsLead:
      "Scheduled backups run once a day between 02:00 and 05:00. A failure raises a banner here and sends a webhook.",
    backupOpsUnavailable: "Backup health could not be loaded.",
    backupOpsIncidentsLabel: "Backup incidents",
    backupOpsJobFailedTitle: "Job failed",
    backupOpsJobFailedBody:
      "The scheduled backup on {time} did not finish ({reason}). Earlier Backup Sets are kept.",
    backupOpsIntegrityFailedTitle: "Integrity failed",
    backupOpsIntegrityFailedBody:
      "The Backup Set written on {time} failed its integrity check. Do not rely on it until a new backup verifies.",
    backupOpsAcknowledge: "Acknowledge",
    backupOpsAcknowledging: "Acknowledging…",
    backupOpsAcknowledgeFailed: "The banner could not be acknowledged.",
    backupOpsNoIncidents: "No open backup incidents.",
    backupOpsEvidenceHeading: "Recovery evidence",
    backupOpsLastDrill: "Last restore drill",
    backupOpsDrillPass: "Pass",
    backupOpsDrillFail: "Fail",
    backupOpsDrillNever: "Never run",
    backupOpsTrustedPoint: "Trusted recovery point",
    backupOpsTrustedNone: "None yet",
    backupOpsVerifyHeading: "Verify my backup now",
    backupOpsVerifyLead:
      "Restores the selected Backup Set into scratch space, opens it and checks that it reads back. Your live data is never touched.",
    backupOpsVerifyPickSet: "Select a Backup Set in the history to verify it.",
    backupOpsVerifyKeyLabel: "Backup key for the drill",
    backupOpsVerifyRun: "Verify my backup now",
    backupOpsVerifyRunning: "Verifying…",
    backupOpsVerifyPassed:
      "Restore verified: {databases} databases and {libraries} Libraries read back from {set}.",
    backupOpsVerifyFailedResult: "The restore drill for {set} failed ({reason}).",
    backupOpsVerifyFailed: "The restore drill could not run.",
    backupOpsGateHeading: "Update gate",
    backupOpsGateHardLabel: "Hard block",
    backupOpsGateHardHelp:
      "Prevent upgrades until a valid Backup Set exists. Optional: by default Beamfall warns and lets you continue.",
    backupOpsGateFailed: "The update gate could not be changed.",
    updateBackupGateHeading: "Backup before update",
    updateBackupGateStepBoundary: "Boundary detected",
    updateBackupGateStepBackup: "Create encrypted Backup Set",
    updateBackupGateStepVerify: "Verify manifest and backup key",
    updateBackupGateStepInstall: "Install update",
    updateBackupGateSoft: "Gate policy: warn, then permit (soft).",
    updateBackupGateHard: "Gate policy: require a backup and integrity pass (hard block).",
    updateBackupGateBlocked:
      "Updates are blocked until a valid Backup Set exists and passes its integrity check.",
    updateBackupGateWarn:
      "No verified Backup Set is on record. Take and verify a backup before installing.",
    updateBackupGateClear: "A verified Backup Set from {time} is on record.",
  },
  fr: {
    backupOpsHeading: "État des sauvegardes",
    backupOpsLead:
      "Les sauvegardes planifiées s’exécutent une fois par jour entre 02:00 et 05:00. Un échec affiche une bannière ici et envoie un webhook.",
    backupOpsUnavailable: "L’état des sauvegardes n’a pas pu être chargé.",
    backupOpsIncidentsLabel: "Incidents de sauvegarde",
    backupOpsJobFailedTitle: "Tâche échouée",
    backupOpsJobFailedBody:
      "La sauvegarde planifiée du {time} ne s’est pas terminée ({reason}). Les jeux de sauvegarde précédents sont conservés.",
    backupOpsIntegrityFailedTitle: "Intégrité en échec",
    backupOpsIntegrityFailedBody:
      "Le jeu de sauvegarde écrit le {time} a échoué à sa vérification d’intégrité. Ne vous y fiez pas avant qu’une nouvelle sauvegarde soit vérifiée.",
    backupOpsAcknowledge: "Prendre acte",
    backupOpsAcknowledging: "Enregistrement…",
    backupOpsAcknowledgeFailed: "La bannière n’a pas pu être acquittée.",
    backupOpsNoIncidents: "Aucun incident de sauvegarde ouvert.",
    backupOpsEvidenceHeading: "Preuves de récupération",
    backupOpsLastDrill: "Dernier exercice de restauration",
    backupOpsDrillPass: "Réussi",
    backupOpsDrillFail: "Échoué",
    backupOpsDrillNever: "Jamais exécuté",
    backupOpsTrustedPoint: "Point de récupération fiable",
    backupOpsTrustedNone: "Aucun pour l’instant",
    backupOpsVerifyHeading: "Vérifier ma sauvegarde maintenant",
    backupOpsVerifyLead:
      "Restaure le jeu de sauvegarde choisi dans un espace temporaire, l’ouvre et vérifie qu’il se relit. Vos données en service ne sont jamais modifiées.",
    backupOpsVerifyPickSet: "Choisissez un jeu de sauvegarde dans l’historique pour le vérifier.",
    backupOpsVerifyKeyLabel: "Clé de sauvegarde pour l’exercice",
    backupOpsVerifyRun: "Vérifier ma sauvegarde maintenant",
    backupOpsVerifyRunning: "Vérification…",
    backupOpsVerifyPassed:
      "Restauration vérifiée : {databases} bases de données et {libraries} bibliothèques relues depuis {set}.",
    backupOpsVerifyFailedResult: "L’exercice de restauration de {set} a échoué ({reason}).",
    backupOpsVerifyFailed: "L’exercice de restauration n’a pas pu s’exécuter.",
    backupOpsGateHeading: "Contrôle des mises à jour",
    backupOpsGateHardLabel: "Blocage strict",
    backupOpsGateHardHelp:
      "Empêche les mises à jour tant qu’aucun jeu de sauvegarde valide n’existe. Facultatif : par défaut, Beamfall avertit et vous laisse continuer.",
    backupOpsGateFailed: "Le contrôle des mises à jour n’a pas pu être modifié.",
    updateBackupGateHeading: "Sauvegarde avant la mise à jour",
    updateBackupGateStepBoundary: "Limite détectée",
    updateBackupGateStepBackup: "Créer un jeu de sauvegarde chiffré",
    updateBackupGateStepVerify: "Vérifier le manifeste et la clé de sauvegarde",
    updateBackupGateStepInstall: "Installer la mise à jour",
    updateBackupGateSoft: "Politique : avertir, puis autoriser (souple).",
    updateBackupGateHard:
      "Politique : exiger une sauvegarde et une vérification d’intégrité réussie (blocage strict).",
    updateBackupGateBlocked:
      "Les mises à jour sont bloquées jusqu’à ce qu’un jeu de sauvegarde valide existe et réussisse sa vérification d’intégrité.",
    updateBackupGateWarn:
      "Aucun jeu de sauvegarde vérifié n’est enregistré. Faites et vérifiez une sauvegarde avant d’installer.",
    updateBackupGateClear: "Un jeu de sauvegarde vérifié du {time} est enregistré.",
  },
  de: {
    backupOpsHeading: "Backup-Zustand",
    backupOpsLead:
      "Geplante Backups laufen einmal täglich zwischen 02:00 und 05:00. Ein Fehler erzeugt hier ein Banner und sendet einen Webhook.",
    backupOpsUnavailable: "Der Backup-Zustand konnte nicht geladen werden.",
    backupOpsIncidentsLabel: "Backup-Vorfälle",
    backupOpsJobFailedTitle: "Auftrag fehlgeschlagen",
    backupOpsJobFailedBody:
      "Das geplante Backup am {time} wurde nicht abgeschlossen ({reason}). Frühere Backup-Sätze bleiben erhalten.",
    backupOpsIntegrityFailedTitle: "Integrität fehlgeschlagen",
    backupOpsIntegrityFailedBody:
      "Der am {time} geschriebene Backup-Satz hat seine Integritätsprüfung nicht bestanden. Verlassen Sie sich nicht darauf, bis ein neues Backup geprüft ist.",
    backupOpsAcknowledge: "Bestätigen",
    backupOpsAcknowledging: "Wird bestätigt…",
    backupOpsAcknowledgeFailed: "Das Banner konnte nicht bestätigt werden.",
    backupOpsNoIncidents: "Keine offenen Backup-Vorfälle.",
    backupOpsEvidenceHeading: "Wiederherstellungsnachweise",
    backupOpsLastDrill: "Letzte Wiederherstellungsprobe",
    backupOpsDrillPass: "Bestanden",
    backupOpsDrillFail: "Fehlgeschlagen",
    backupOpsDrillNever: "Noch nie ausgeführt",
    backupOpsTrustedPoint: "Vertrauenswürdiger Wiederherstellungspunkt",
    backupOpsTrustedNone: "Noch keiner",
    backupOpsVerifyHeading: "Mein Backup jetzt prüfen",
    backupOpsVerifyLead:
      "Stellt den gewählten Backup-Satz in einem temporären Bereich wieder her, öffnet ihn und prüft, ob er sich lesen lässt. Ihre laufenden Daten werden nie verändert.",
    backupOpsVerifyPickSet: "Wählen Sie im Verlauf einen Backup-Satz zum Prüfen.",
    backupOpsVerifyKeyLabel: "Backup-Schlüssel für die Probe",
    backupOpsVerifyRun: "Mein Backup jetzt prüfen",
    backupOpsVerifyRunning: "Wird geprüft…",
    backupOpsVerifyPassed:
      "Wiederherstellung geprüft: {databases} Datenbanken und {libraries} Bibliotheken aus {set} gelesen.",
    backupOpsVerifyFailedResult:
      "Die Wiederherstellungsprobe für {set} ist fehlgeschlagen ({reason}).",
    backupOpsVerifyFailed: "Die Wiederherstellungsprobe konnte nicht ausgeführt werden.",
    backupOpsGateHeading: "Update-Sperre",
    backupOpsGateHardLabel: "Harte Sperre",
    backupOpsGateHardHelp:
      "Verhindert Upgrades, bis ein gültiger Backup-Satz existiert. Optional: Standardmäßig warnt Beamfall und lässt Sie fortfahren.",
    backupOpsGateFailed: "Die Update-Sperre konnte nicht geändert werden.",
    updateBackupGateHeading: "Backup vor dem Update",
    updateBackupGateStepBoundary: "Grenze erkannt",
    updateBackupGateStepBackup: "Verschlüsselten Backup-Satz erstellen",
    updateBackupGateStepVerify: "Manifest und Backup-Schlüssel prüfen",
    updateBackupGateStepInstall: "Update installieren",
    updateBackupGateSoft: "Richtlinie: warnen, dann erlauben (weich).",
    updateBackupGateHard:
      "Richtlinie: Backup und bestandene Integritätsprüfung verlangen (harte Sperre).",
    updateBackupGateBlocked:
      "Updates sind gesperrt, bis ein gültiger Backup-Satz existiert und seine Integritätsprüfung besteht.",
    updateBackupGateWarn:
      "Es ist kein geprüfter Backup-Satz verzeichnet. Erstellen und prüfen Sie vor der Installation ein Backup.",
    updateBackupGateClear: "Ein geprüfter Backup-Satz vom {time} ist verzeichnet.",
  },
  es: {
    backupOpsHeading: "Estado de las copias de seguridad",
    backupOpsLead:
      "Las copias programadas se ejecutan una vez al día entre las 02:00 y las 05:00. Un fallo muestra un aviso aquí y envía un webhook.",
    backupOpsUnavailable: "No se pudo cargar el estado de las copias de seguridad.",
    backupOpsIncidentsLabel: "Incidencias de copia de seguridad",
    backupOpsJobFailedTitle: "Tarea fallida",
    backupOpsJobFailedBody:
      "La copia programada del {time} no terminó ({reason}). Se conservan los conjuntos de copias anteriores.",
    backupOpsIntegrityFailedTitle: "Integridad fallida",
    backupOpsIntegrityFailedBody:
      "El conjunto de copias escrito el {time} no superó su comprobación de integridad. No confíes en él hasta que se verifique una copia nueva.",
    backupOpsAcknowledge: "Marcar como visto",
    backupOpsAcknowledging: "Guardando…",
    backupOpsAcknowledgeFailed: "No se pudo marcar el aviso como visto.",
    backupOpsNoIncidents: "No hay incidencias de copia abiertas.",
    backupOpsEvidenceHeading: "Pruebas de recuperación",
    backupOpsLastDrill: "Último simulacro de restauración",
    backupOpsDrillPass: "Correcto",
    backupOpsDrillFail: "Fallido",
    backupOpsDrillNever: "Nunca ejecutado",
    backupOpsTrustedPoint: "Punto de recuperación fiable",
    backupOpsTrustedNone: "Ninguno todavía",
    backupOpsVerifyHeading: "Verificar mi copia ahora",
    backupOpsVerifyLead:
      "Restaura el conjunto de copias elegido en un espacio temporal, lo abre y comprueba que se lee. Tus datos en uso nunca se modifican.",
    backupOpsVerifyPickSet: "Elige un conjunto de copias en el historial para verificarlo.",
    backupOpsVerifyKeyLabel: "Clave de copia para el simulacro",
    backupOpsVerifyRun: "Verificar mi copia ahora",
    backupOpsVerifyRunning: "Verificando…",
    backupOpsVerifyPassed:
      "Restauración verificada: {databases} bases de datos y {libraries} bibliotecas leídas de {set}.",
    backupOpsVerifyFailedResult: "El simulacro de restauración de {set} falló ({reason}).",
    backupOpsVerifyFailed: "No se pudo ejecutar el simulacro de restauración.",
    backupOpsGateHeading: "Control de actualizaciones",
    backupOpsGateHardLabel: "Bloqueo estricto",
    backupOpsGateHardHelp:
      "Impide las actualizaciones hasta que exista un conjunto de copias válido. Opcional: por defecto, Beamfall avisa y te deja continuar.",
    backupOpsGateFailed: "No se pudo cambiar el control de actualizaciones.",
    updateBackupGateHeading: "Copia antes de actualizar",
    updateBackupGateStepBoundary: "Límite detectado",
    updateBackupGateStepBackup: "Crear un conjunto de copias cifrado",
    updateBackupGateStepVerify: "Verificar el manifiesto y la clave de copia",
    updateBackupGateStepInstall: "Instalar la actualización",
    updateBackupGateSoft: "Política: avisar y luego permitir (suave).",
    updateBackupGateHard:
      "Política: exigir una copia y una comprobación de integridad superada (bloqueo estricto).",
    updateBackupGateBlocked:
      "Las actualizaciones están bloqueadas hasta que exista un conjunto de copias válido que supere su comprobación de integridad.",
    updateBackupGateWarn:
      "No hay ningún conjunto de copias verificado registrado. Haz y verifica una copia antes de instalar.",
    updateBackupGateClear: "Hay registrado un conjunto de copias verificado del {time}.",
  },
  pt: {
    backupOpsHeading: "Saúde dos backups",
    backupOpsLead:
      "Os backups agendados rodam uma vez por dia entre 02:00 e 05:00. Uma falha exibe um aviso aqui e envia um webhook.",
    backupOpsUnavailable: "Não foi possível carregar a saúde dos backups.",
    backupOpsIncidentsLabel: "Incidentes de backup",
    backupOpsJobFailedTitle: "Tarefa falhou",
    backupOpsJobFailedBody:
      "O backup agendado de {time} não terminou ({reason}). Os conjuntos de backup anteriores são mantidos.",
    backupOpsIntegrityFailedTitle: "Integridade falhou",
    backupOpsIntegrityFailedBody:
      "O conjunto de backup gravado em {time} falhou na verificação de integridade. Não confie nele até que um novo backup seja verificado.",
    backupOpsAcknowledge: "Marcar como visto",
    backupOpsAcknowledging: "Salvando…",
    backupOpsAcknowledgeFailed: "Não foi possível marcar o aviso como visto.",
    backupOpsNoIncidents: "Nenhum incidente de backup aberto.",
    backupOpsEvidenceHeading: "Evidências de recuperação",
    backupOpsLastDrill: "Último teste de restauração",
    backupOpsDrillPass: "Aprovado",
    backupOpsDrillFail: "Falhou",
    backupOpsDrillNever: "Nunca executado",
    backupOpsTrustedPoint: "Ponto de recuperação confiável",
    backupOpsTrustedNone: "Nenhum ainda",
    backupOpsVerifyHeading: "Verificar meu backup agora",
    backupOpsVerifyLead:
      "Restaura o conjunto de backup escolhido em um espaço temporário, abre-o e confere se ele é lido. Seus dados em uso nunca são alterados.",
    backupOpsVerifyPickSet: "Escolha um conjunto de backup no histórico para verificá-lo.",
    backupOpsVerifyKeyLabel: "Chave de backup para o teste",
    backupOpsVerifyRun: "Verificar meu backup agora",
    backupOpsVerifyRunning: "Verificando…",
    backupOpsVerifyPassed:
      "Restauração verificada: {databases} bancos de dados e {libraries} bibliotecas lidos de {set}.",
    backupOpsVerifyFailedResult: "O teste de restauração de {set} falhou ({reason}).",
    backupOpsVerifyFailed: "Não foi possível executar o teste de restauração.",
    backupOpsGateHeading: "Controle de atualizações",
    backupOpsGateHardLabel: "Bloqueio rígido",
    backupOpsGateHardHelp:
      "Impede atualizações até que exista um conjunto de backup válido. Opcional: por padrão, o Beamfall avisa e deixa você continuar.",
    backupOpsGateFailed: "Não foi possível alterar o controle de atualizações.",
    updateBackupGateHeading: "Backup antes de atualizar",
    updateBackupGateStepBoundary: "Limite detectado",
    updateBackupGateStepBackup: "Criar um conjunto de backup criptografado",
    updateBackupGateStepVerify: "Verificar o manifesto e a chave de backup",
    updateBackupGateStepInstall: "Instalar a atualização",
    updateBackupGateSoft: "Política: avisar e depois permitir (flexível).",
    updateBackupGateHard:
      "Política: exigir um backup e uma verificação de integridade aprovada (bloqueio rígido).",
    updateBackupGateBlocked:
      "As atualizações estão bloqueadas até que exista um conjunto de backup válido que passe na verificação de integridade.",
    updateBackupGateWarn:
      "Nenhum conjunto de backup verificado está registrado. Faça e verifique um backup antes de instalar.",
    updateBackupGateClear: "Há um conjunto de backup verificado de {time} registrado.",
  },
};

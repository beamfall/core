// Help text (CORE-2445) for the manage settings: the (?) popovers and the preset
// dropdown choices. Every key ships in every locale.
export const settingsHelpManageStrings = {
  en: {
    manageSubKeepDownloads: "Keep downloads",
    manageSubKeepDownloadsHelp:
      "What happens to old downloads from this feed. Keep all (the default) never removes anything. Keep only the newest moves older downloads to Trash once more than your chosen number arrive. Delete after played moves a download to Trash once it has been played.",
    manageSubKeepNewest: "Keep only the newest",
    manageSubDownloadsToKeep: "Downloads to keep",
    manageSubDownloadFolderHelp:
      "Where this feed's downloads are saved. Leave it empty to use the library's first folder. New files there are added to the library like any other.",
    manageSubAuthHeaderHelp:
      'Only for private feeds that need a sign-in. Paste the full header value your provider gives you, for example "Bearer" followed by a token. Leave it empty for public feeds.',
    manageScheduleRepeat: "Repeat",
    manageScheduleRepeatHelp:
      "How often this job runs. Every runs it again after the interval you set. Daily and Weekly run it at a set time, in the server's time zone; a quiet hour such as 03:00 keeps it out of the way.",
    manageScheduleTime: "Time",
    manageScheduleOrphanedHelp:
      "The feature or plugin that added this schedule is no longer loaded, so the schedule does not run. Its settings are kept, and it runs again if that feature comes back.",
    manageConvertPresetH264: "H.264 MP4 (plays almost everywhere)",
    manageConvertPresetHevc: "HEVC MP4 (smaller files)",
    manageConvertPresetAac: "AAC M4A (audio only)",
    manageConvertPresetHelp:
      "The format of the new file. H.264 (the default) plays on nearly every device. HEVC makes smaller files, but some older devices cannot play it. AAC keeps only the sound. The original file moves to Trash.",
    manageRenameDigits: "Number digits",
    manageRenameDigitsHelp:
      "How many digits each number gets. Leading zeros keep files sorted in order: 01, 02 … 10. The default is 2; choose None for plain numbers.",
    manageRenameFindHelp:
      "The exact text to look for in each file name. Every match is changed to the Replace text. This is plain text, not a pattern, and the file extension is never changed.",
  },
  fr: {
    manageSubKeepDownloads: "Conserver les téléchargements",
    manageSubKeepDownloadsHelp:
      "Ce qu'il advient des anciens téléchargements de ce flux. Tout conserver (par défaut) ne supprime jamais rien. Garder uniquement les plus récents déplace les anciens téléchargements vers la corbeille dès que leur nombre dépasse celui choisi. Supprimer après lecture déplace un téléchargement vers la corbeille une fois qu'il a été lu.",
    manageSubKeepNewest: "Garder uniquement les plus récents",
    manageSubDownloadsToKeep: "Téléchargements à garder",
    manageSubDownloadFolderHelp:
      "Où les téléchargements de ce flux sont enregistrés. Laissez vide pour utiliser le premier dossier de la bibliothèque. Les nouveaux fichiers y sont ajoutés à la bibliothèque comme les autres.",
    manageSubAuthHeaderHelp:
      "Uniquement pour les flux privés qui demandent une connexion. Collez la valeur complète de l'en-tête fournie par votre service, par exemple « Bearer » suivi d'un jeton. Laissez vide pour les flux publics.",
    manageScheduleRepeat: "Répétition",
    manageScheduleRepeatHelp:
      "La fréquence d'exécution de cette tâche. « Tous les » relance la tâche après l'intervalle choisi. Quotidien et Hebdomadaire la lancent à une heure fixe, dans le fuseau horaire du serveur ; une heure creuse comme 03:00 évite de gêner.",
    manageScheduleTime: "Heure",
    manageScheduleOrphanedHelp:
      "La fonction ou le plugin qui a créé cette planification n'est plus chargé, elle ne s'exécute donc pas. Ses réglages sont conservés et elle reprend si cette fonction revient.",
    manageConvertPresetH264: "H.264 MP4 (lisible presque partout)",
    manageConvertPresetHevc: "HEVC MP4 (fichiers plus petits)",
    manageConvertPresetAac: "AAC M4A (audio uniquement)",
    manageConvertPresetHelp:
      "Le format du nouveau fichier. H.264 (par défaut) se lit sur presque tous les appareils. HEVC produit des fichiers plus petits, mais certains appareils anciens ne peuvent pas le lire. AAC ne garde que le son. Le fichier d'origine part dans la corbeille.",
    manageRenameDigits: "Chiffres du numéro",
    manageRenameDigitsHelp:
      "Le nombre de chiffres de chaque numéro. Les zéros en tête gardent les fichiers dans l'ordre : 01, 02 … 10. La valeur par défaut est 2 ; choisissez Aucun pour des numéros simples.",
    manageRenameFindHelp:
      "Le texte exact à chercher dans chaque nom de fichier. Chaque occurrence est remplacée par le texte de Remplacer par. C'est du texte simple, pas un motif, et l'extension du fichier n'est jamais modifiée.",
  },
  de: {
    manageSubKeepDownloads: "Downloads behalten",
    manageSubKeepDownloadsHelp:
      "Was mit alten Downloads dieses Feeds passiert. Alle behalten (Standard) entfernt nie etwas. Nur die neuesten behalten verschiebt ältere Downloads in den Papierkorb, sobald mehr als die gewählte Anzahl vorhanden sind. Nach Wiedergabe löschen verschiebt einen Download in den Papierkorb, sobald er abgespielt wurde.",
    manageSubKeepNewest: "Nur die neuesten behalten",
    manageSubDownloadsToKeep: "Zu behaltende Downloads",
    manageSubDownloadFolderHelp:
      "Wo die Downloads dieses Feeds gespeichert werden. Leer lassen, um den ersten Ordner der Bibliothek zu verwenden. Neue Dateien dort werden wie alle anderen zur Bibliothek hinzugefügt.",
    manageSubAuthHeaderHelp:
      "Nur für private Feeds, die eine Anmeldung brauchen. Fügen Sie den vollständigen Header-Wert Ihres Anbieters ein, zum Beispiel „Bearer“ gefolgt von einem Token. Für öffentliche Feeds leer lassen.",
    manageScheduleRepeat: "Wiederholung",
    manageScheduleRepeatHelp:
      "Wie oft dieser Auftrag läuft. „Alle“ startet ihn nach dem gewählten Abstand erneut. Täglich und Wöchentlich starten ihn zu einer festen Uhrzeit in der Zeitzone des Servers; eine ruhige Stunde wie 03:00 stört am wenigsten.",
    manageScheduleTime: "Uhrzeit",
    manageScheduleOrphanedHelp:
      "Die Funktion oder das Plugin, das diesen Zeitplan angelegt hat, ist nicht mehr geladen, daher läuft der Zeitplan nicht. Seine Einstellungen bleiben erhalten, und er läuft wieder, wenn die Funktion zurückkommt.",
    manageConvertPresetH264: "H.264 MP4 (läuft fast überall)",
    manageConvertPresetHevc: "HEVC MP4 (kleinere Dateien)",
    manageConvertPresetAac: "AAC M4A (nur Audio)",
    manageConvertPresetHelp:
      "Das Format der neuen Datei. H.264 (Standard) läuft auf fast jedem Gerät. HEVC erzeugt kleinere Dateien, manche älteren Geräte können sie aber nicht abspielen. AAC behält nur den Ton. Die Originaldatei kommt in den Papierkorb.",
    manageRenameDigits: "Stellen der Nummer",
    manageRenameDigitsHelp:
      "Wie viele Stellen jede Nummer bekommt. Führende Nullen halten die Dateien in der richtigen Reihenfolge: 01, 02 … 10. Standard ist 2; wählen Sie Keine für einfache Nummern.",
    manageRenameFindHelp:
      "Der genaue Text, der in jedem Dateinamen gesucht wird. Jeder Treffer wird durch den Ersetzen-Text ersetzt. Das ist einfacher Text, kein Muster, und die Dateiendung wird nie geändert.",
  },
  es: {
    manageSubKeepDownloads: "Conservar descargas",
    manageSubKeepDownloadsHelp:
      "Qué pasa con las descargas antiguas de este feed. Conservar todo (predeterminado) nunca elimina nada. Conservar solo las más recientes mueve las descargas antiguas a la papelera cuando llegan más de las que elijas. Eliminar después de reproducir mueve una descarga a la papelera cuando se ha reproducido.",
    manageSubKeepNewest: "Conservar solo las más recientes",
    manageSubDownloadsToKeep: "Descargas que conservar",
    manageSubDownloadFolderHelp:
      "Dónde se guardan las descargas de este feed. Déjalo vacío para usar la primera carpeta de la biblioteca. Los archivos nuevos se añaden a la biblioteca como cualquier otro.",
    manageSubAuthHeaderHelp:
      "Solo para feeds privados que piden iniciar sesión. Pega el valor completo del encabezado que te da tu proveedor, por ejemplo «Bearer» seguido de un token. Déjalo vacío para feeds públicos.",
    manageScheduleRepeat: "Repetición",
    manageScheduleRepeatHelp:
      "Con qué frecuencia se ejecuta esta tarea. «Cada» la vuelve a ejecutar tras el intervalo que elijas. Diaria y Semanal la ejecutan a una hora fija, en la zona horaria del servidor; una hora tranquila como las 03:00 molesta menos.",
    manageScheduleTime: "Hora",
    manageScheduleOrphanedHelp:
      "La función o el plugin que creó esta programación ya no está cargado, así que no se ejecuta. Su configuración se conserva y vuelve a ejecutarse si esa función regresa.",
    manageConvertPresetH264: "H.264 MP4 (se reproduce casi en todas partes)",
    manageConvertPresetHevc: "HEVC MP4 (archivos más pequeños)",
    manageConvertPresetAac: "AAC M4A (solo audio)",
    manageConvertPresetHelp:
      "El formato del nuevo archivo. H.264 (predeterminado) se reproduce en casi cualquier dispositivo. HEVC genera archivos más pequeños, pero algunos dispositivos antiguos no pueden reproducirlo. AAC conserva solo el sonido. El archivo original pasa a la papelera.",
    manageRenameDigits: "Dígitos del número",
    manageRenameDigitsHelp:
      "Cuántos dígitos tiene cada número. Los ceros a la izquierda mantienen los archivos en orden: 01, 02 … 10. El valor predeterminado es 2; elige Ninguno para números simples.",
    manageRenameFindHelp:
      "El texto exacto que se busca en cada nombre de archivo. Cada coincidencia se cambia por el texto de Reemplazar. Es texto simple, no un patrón, y la extensión del archivo nunca cambia.",
  },
  pt: {
    manageSubKeepDownloads: "Manter transferências",
    manageSubKeepDownloadsHelp:
      "O que acontece às transferências antigas deste feed. Manter tudo (padrão) nunca remove nada. Manter só as mais recentes move as transferências antigas para a lixeira quando chegam mais do que o número escolhido. Excluir depois de reproduzir move uma transferência para a lixeira depois de ser reproduzida.",
    manageSubKeepNewest: "Manter só as mais recentes",
    manageSubDownloadsToKeep: "Transferências a manter",
    manageSubDownloadFolderHelp:
      "Onde as transferências deste feed são guardadas. Deixe vazio para usar a primeira pasta da biblioteca. Os ficheiros novos são adicionados à biblioteca como quaisquer outros.",
    manageSubAuthHeaderHelp:
      "Apenas para feeds privados que pedem início de sessão. Cole o valor completo do cabeçalho fornecido pelo seu serviço, por exemplo «Bearer» seguido de um token. Deixe vazio para feeds públicos.",
    manageScheduleRepeat: "Repetição",
    manageScheduleRepeatHelp:
      "Com que frequência esta tarefa é executada. «A cada» volta a executá-la após o intervalo escolhido. Diário e Semanal executam-na a uma hora fixa, no fuso horário do servidor; uma hora calma como as 03:00 incomoda menos.",
    manageScheduleTime: "Hora",
    manageScheduleOrphanedHelp:
      "A funcionalidade ou o plugin que criou este agendamento já não está carregado, por isso o agendamento não é executado. As definições ficam guardadas e volta a ser executado se essa funcionalidade regressar.",
    manageConvertPresetH264: "H.264 MP4 (reproduz quase em todo o lado)",
    manageConvertPresetHevc: "HEVC MP4 (ficheiros mais pequenos)",
    manageConvertPresetAac: "AAC M4A (só áudio)",
    manageConvertPresetHelp:
      "O formato do novo ficheiro. H.264 (padrão) reproduz em quase todos os dispositivos. HEVC cria ficheiros mais pequenos, mas alguns dispositivos antigos não o conseguem reproduzir. AAC mantém só o som. O ficheiro original vai para a lixeira.",
    manageRenameDigits: "Dígitos do número",
    manageRenameDigitsHelp:
      "Quantos dígitos tem cada número. Os zeros à esquerda mantêm os ficheiros por ordem: 01, 02 … 10. O padrão é 2; escolha Nenhum para números simples.",
    manageRenameFindHelp:
      "O texto exato a procurar em cada nome de ficheiro. Cada ocorrência é trocada pelo texto de Substituir. É texto simples, não um padrão, e a extensão do ficheiro nunca muda.",
  },
};

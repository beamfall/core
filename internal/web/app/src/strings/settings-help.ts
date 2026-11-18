// Settings clarity (CORE-2445): the (?) help button, the settings search, the
// one-line purpose of the Console and Manage menus, and the plain-language
// names and help text for Console settings.
export const settingsHelpStrings = {
  en: {
    settingsHelpFor: "Help: {setting}",
    // Settings search
    settingsSearchLabel: "Search settings",
    settingsSearchPlaceholder: "Search settings…",
    settingsSearchEmpty: "No matching settings",
    settingsSearchConsole: "Console",
    settingsSearchManage: "Manage",
    // Menu purpose
    consoleMenuPurpose:
      "Server settings: how Beamfall runs, who can use it, and keeping it healthy. Needs the admin password.",
    consoleMenuCrossLink: "Organizing media? Go to Manage",
    manageMenuPurpose:
      "Day-to-day library work: fix matches, edit details, organize and tidy files.",
    manageMenuCrossLink: "Changing server settings? Go to the Console",
    // Menu names
    navOptionalFeatures: "Optional features",
    navParentalControls: "Parental controls",
    navApiKeys: "API keys & tokens",
    navLeavingSoonRules: "Leaving soon rules",
    navUpdateNotifications: "Update notifications",
    navPlaybackSignIn: "Playback & sign-in",
    navTrash: "Trash",
    navUnlockRestricted: "Show restricted content",
    navSwitchProfile: "Switch profile",
    // Playback & sign-in
    configPageLead:
      "How video is converted for devices that cannot play the original file, and how long people stay signed in.",
    transcodeAutomatic: "Automatic",
    transcodeHardwareEnabledTip:
      "Uses the video engine in your graphics card or processor to convert video. It is much faster and lighter on the CPU. If playback fails or looks wrong, turn this off.",
    transcodeMaxConcurrentTip:
      "How many videos the server converts at the same time. Automatic picks a limit from your hardware. Lower it if playback stutters when several people watch at once.",
    transcodeCodecTip:
      "The video format used when a file has to be converted. H.264 plays on almost every device. HEVC makes smaller files, but some older devices cannot play it. Server default lets Beamfall choose.",
    transcodeCodecH264: "H.264 (most compatible)",
    transcodeCodecHevc: "HEVC (smaller files)",
    transcodeQualityTip:
      "Picture quality against bandwidth and CPU when converting. Balanced suits most homes. Low helps slow connections. High needs a fast network and more CPU.",
    transcodeQualityLow: "Low (saves bandwidth)",
    transcodeQualityBalanced: "Balanced",
    transcodeQualityHigh: "High (best picture)",
    transcodeDeviceTip:
      "Which hardware encoder to use: vaapi, qsv, nvenc, videotoolbox, amf, or a device path such as /dev/dri/renderD128. Leave empty to let the server choose.",
    transcodeTempDirTip:
      "Folder where converted video is written while it plays. Use a fast disk with free space. Leave empty for the server default. Takes effect after a restart.",
    // Operations
    governorLowPowerTip:
      "On by default. Background jobs such as scanning, artwork and analysis run one at a time so playback always comes first. Turn it off on a powerful server to finish background work sooner.",
    governorBackgroundWorkersTip:
      "How many background jobs may run at once when low-power mode is off. More finishes work sooner but uses more CPU and disk.",
    eventsRetentionDaysTip:
      "How long the server keeps its event log of activity and job history. Older entries are trimmed. This never deletes media; to remove media automatically, use Leaving soon rules.",
  },
  fr: {
    settingsHelpFor: "Aide : {setting}",
    settingsSearchLabel: "Rechercher dans les réglages",
    settingsSearchPlaceholder: "Rechercher un réglage…",
    settingsSearchEmpty: "Aucun réglage correspondant",
    settingsSearchConsole: "Console",
    settingsSearchManage: "Gérer",
    consoleMenuPurpose:
      "Réglages du serveur : son fonctionnement, qui peut l’utiliser et son bon état. Nécessite le mot de passe administrateur.",
    consoleMenuCrossLink: "Vous organisez vos médias ? Allez dans Gérer",
    manageMenuPurpose:
      "Travail quotidien sur les bibliothèques : corriger les correspondances, modifier les détails, organiser et ranger les fichiers.",
    manageMenuCrossLink: "Vous changez les réglages du serveur ? Allez dans la Console",
    navOptionalFeatures: "Fonctions facultatives",
    navParentalControls: "Contrôle parental",
    navApiKeys: "Clés et jetons d’API",
    navLeavingSoonRules: "Règles « Bientôt retiré »",
    navUpdateNotifications: "Avis de mise à jour",
    navPlaybackSignIn: "Lecture et connexion",
    navTrash: "Corbeille",
    navUnlockRestricted: "Afficher le contenu restreint",
    navSwitchProfile: "Changer de profil",
    configPageLead:
      "Comment la vidéo est convertie pour les appareils qui ne lisent pas le fichier d’origine, et combien de temps les personnes restent connectées.",
    transcodeAutomatic: "Automatique",
    transcodeHardwareEnabledTip:
      "Utilise le moteur vidéo de votre carte graphique ou de votre processeur pour convertir la vidéo. C’est bien plus rapide et moins lourd pour le processeur. Si la lecture échoue ou s’affiche mal, désactivez-le.",
    transcodeMaxConcurrentTip:
      "Nombre de vidéos que le serveur convertit en même temps. Automatique choisit une limite selon votre matériel. Baissez-la si la lecture saccade quand plusieurs personnes regardent.",
    transcodeCodecTip:
      "Le format vidéo utilisé quand un fichier doit être converti. H.264 se lit sur presque tous les appareils. HEVC produit des fichiers plus petits, mais certains anciens appareils ne le lisent pas. Valeur du serveur laisse Beamfall choisir.",
    transcodeCodecH264: "H.264 (le plus compatible)",
    transcodeCodecHevc: "HEVC (fichiers plus petits)",
    transcodeQualityTip:
      "Qualité d’image contre bande passante et processeur lors de la conversion. Équilibrée convient à la plupart des foyers. Basse aide les connexions lentes. Haute demande un réseau rapide et plus de processeur.",
    transcodeQualityLow: "Basse (économise la bande passante)",
    transcodeQualityBalanced: "Équilibrée",
    transcodeQualityHigh: "Haute (meilleure image)",
    transcodeDeviceTip:
      "L’encodeur matériel à utiliser : vaapi, qsv, nvenc, videotoolbox, amf, ou un chemin de périphérique comme /dev/dri/renderD128. Laissez vide pour laisser le serveur choisir.",
    transcodeTempDirTip:
      "Dossier où la vidéo convertie est écrite pendant la lecture. Choisissez un disque rapide avec de l’espace libre. Laissez vide pour la valeur du serveur. Prend effet après un redémarrage.",
    governorLowPowerTip:
      "Activé par défaut. Les tâches de fond comme l’analyse, les illustrations et le balayage s’exécutent une par une pour que la lecture passe toujours en premier. Désactivez-le sur un serveur puissant pour finir plus vite.",
    governorBackgroundWorkersTip:
      "Nombre de tâches de fond simultanées quand le mode économie d’énergie est désactivé. Plus de tâches finissent plus vite mais utilisent plus de processeur et de disque.",
    eventsRetentionDaysTip:
      "Durée pendant laquelle le serveur garde son journal d’activité et d’historique des tâches. Les entrées plus anciennes sont supprimées. Cela ne supprime jamais de médias ; pour retirer des médias automatiquement, utilisez les règles « Bientôt retiré ».",
  },
  de: {
    settingsHelpFor: "Hilfe: {setting}",
    settingsSearchLabel: "Einstellungen durchsuchen",
    settingsSearchPlaceholder: "Einstellungen durchsuchen…",
    settingsSearchEmpty: "Keine passenden Einstellungen",
    settingsSearchConsole: "Konsole",
    settingsSearchManage: "Verwalten",
    consoleMenuPurpose:
      "Servereinstellungen: wie Beamfall läuft, wer es nutzen darf und wie es gesund bleibt. Erfordert das Admin-Passwort.",
    consoleMenuCrossLink: "Medien ordnen? Zu Verwalten",
    manageMenuPurpose:
      "Tägliche Bibliotheksarbeit: Zuordnungen korrigieren, Details bearbeiten, Dateien ordnen und aufräumen.",
    manageMenuCrossLink: "Servereinstellungen ändern? Zur Konsole",
    navOptionalFeatures: "Optionale Funktionen",
    navParentalControls: "Jugendschutz",
    navApiKeys: "API-Schlüssel und -Token",
    navLeavingSoonRules: "Regeln für „Bald entfernt“",
    navUpdateNotifications: "Update-Hinweise",
    navPlaybackSignIn: "Wiedergabe und Anmeldung",
    navTrash: "Papierkorb",
    navUnlockRestricted: "Eingeschränkte Inhalte zeigen",
    navSwitchProfile: "Profil wechseln",
    configPageLead:
      "Wie Videos für Geräte umgewandelt werden, die die Originaldatei nicht abspielen können, und wie lange Personen angemeldet bleiben.",
    transcodeAutomatic: "Automatisch",
    transcodeHardwareEnabledTip:
      "Nutzt die Video-Engine Ihrer Grafikkarte oder Ihres Prozessors zum Umwandeln. Das ist viel schneller und schont die CPU. Wenn die Wiedergabe fehlschlägt oder falsch aussieht, schalten Sie es aus.",
    transcodeMaxConcurrentTip:
      "Wie viele Videos der Server gleichzeitig umwandelt. Automatisch wählt ein Limit passend zu Ihrer Hardware. Senken Sie es, wenn die Wiedergabe ruckelt, sobald mehrere Personen schauen.",
    transcodeCodecTip:
      "Das Videoformat, wenn eine Datei umgewandelt werden muss. H.264 läuft auf fast jedem Gerät. HEVC erzeugt kleinere Dateien, manche ältere Geräte können es aber nicht abspielen. Serverstandard lässt Beamfall wählen.",
    transcodeCodecH264: "H.264 (am kompatibelsten)",
    transcodeCodecHevc: "HEVC (kleinere Dateien)",
    transcodeQualityTip:
      "Bildqualität gegen Bandbreite und CPU beim Umwandeln. Ausgewogen passt für die meisten Haushalte. Niedrig hilft bei langsamen Verbindungen. Hoch braucht ein schnelles Netz und mehr CPU.",
    transcodeQualityLow: "Niedrig (spart Bandbreite)",
    transcodeQualityBalanced: "Ausgewogen",
    transcodeQualityHigh: "Hoch (bestes Bild)",
    transcodeDeviceTip:
      "Welcher Hardware-Encoder genutzt wird: vaapi, qsv, nvenc, videotoolbox, amf oder ein Gerätepfad wie /dev/dri/renderD128. Leer lassen, damit der Server wählt.",
    transcodeTempDirTip:
      "Ordner, in den umgewandelte Videos während der Wiedergabe geschrieben werden. Nutzen Sie eine schnelle Platte mit freiem Platz. Leer lassen für den Serverstandard. Wirkt nach einem Neustart.",
    governorLowPowerTip:
      "Standardmäßig an. Hintergrundaufgaben wie Scans, Artwork und Analyse laufen nacheinander, damit die Wiedergabe immer Vorrang hat. Auf einem starken Server ausschalten, um Hintergrundarbeit schneller abzuschließen.",
    governorBackgroundWorkersTip:
      "Wie viele Hintergrundaufgaben gleichzeitig laufen dürfen, wenn der Energiesparmodus aus ist. Mehr ist schneller fertig, nutzt aber mehr CPU und Festplatte.",
    eventsRetentionDaysTip:
      "Wie lange der Server sein Ereignisprotokoll mit Aktivität und Aufgabenverlauf aufbewahrt. Ältere Einträge werden gekürzt. Dabei werden nie Medien gelöscht; um Medien automatisch zu entfernen, nutzen Sie die Regeln für „Bald entfernt“.",
  },
  es: {
    settingsHelpFor: "Ayuda: {setting}",
    settingsSearchLabel: "Buscar en los ajustes",
    settingsSearchPlaceholder: "Buscar ajustes…",
    settingsSearchEmpty: "Ningún ajuste coincide",
    settingsSearchConsole: "Consola",
    settingsSearchManage: "Gestionar",
    consoleMenuPurpose:
      "Ajustes del servidor: cómo funciona Beamfall, quién puede usarlo y cómo mantenerlo en buen estado. Requiere la contraseña de administrador.",
    consoleMenuCrossLink: "¿Organizando contenido? Ve a Gestionar",
    manageMenuPurpose:
      "Trabajo diario en las bibliotecas: corregir coincidencias, editar detalles, organizar y ordenar archivos.",
    manageMenuCrossLink: "¿Cambiando ajustes del servidor? Ve a la Consola",
    navOptionalFeatures: "Funciones opcionales",
    navParentalControls: "Control parental",
    navApiKeys: "Claves y tokens de API",
    navLeavingSoonRules: "Reglas de «Se va pronto»",
    navUpdateNotifications: "Avisos de actualización",
    navPlaybackSignIn: "Reproducción e inicio de sesión",
    navTrash: "Papelera",
    navUnlockRestricted: "Mostrar contenido restringido",
    navSwitchProfile: "Cambiar de perfil",
    configPageLead:
      "Cómo se convierte el vídeo para los dispositivos que no pueden reproducir el archivo original, y cuánto tiempo siguen conectadas las personas.",
    transcodeAutomatic: "Automático",
    transcodeHardwareEnabledTip:
      "Usa el motor de vídeo de tu tarjeta gráfica o procesador para convertir vídeo. Es mucho más rápido y carga menos la CPU. Si la reproducción falla o se ve mal, desactívalo.",
    transcodeMaxConcurrentTip:
      "Cuántos vídeos convierte el servidor a la vez. Automático elige un límite según tu hardware. Bájalo si la reproducción da tirones cuando varias personas ven algo a la vez.",
    transcodeCodecTip:
      "El formato de vídeo que se usa cuando hay que convertir un archivo. H.264 funciona en casi todos los dispositivos. HEVC genera archivos más pequeños, pero algunos dispositivos antiguos no lo reproducen. Predeterminado del servidor deja elegir a Beamfall.",
    transcodeCodecH264: "H.264 (el más compatible)",
    transcodeCodecHevc: "HEVC (archivos más pequeños)",
    transcodeQualityTip:
      "Calidad de imagen frente a ancho de banda y CPU al convertir. Equilibrada sirve para la mayoría de hogares. Baja ayuda con conexiones lentas. Alta necesita una red rápida y más CPU.",
    transcodeQualityLow: "Baja (ahorra ancho de banda)",
    transcodeQualityBalanced: "Equilibrada",
    transcodeQualityHigh: "Alta (mejor imagen)",
    transcodeDeviceTip:
      "Qué codificador por hardware usar: vaapi, qsv, nvenc, videotoolbox, amf o una ruta de dispositivo como /dev/dri/renderD128. Déjalo vacío para que elija el servidor.",
    transcodeTempDirTip:
      "Carpeta donde se escribe el vídeo convertido mientras se reproduce. Usa un disco rápido con espacio libre. Déjala vacía para el valor del servidor. Se aplica tras reiniciar.",
    governorLowPowerTip:
      "Activado de forma predeterminada. Las tareas en segundo plano, como el escaneo, las ilustraciones y el análisis, se ejecutan de una en una para que la reproducción siempre tenga prioridad. Desactívalo en un servidor potente para terminar antes.",
    governorBackgroundWorkersTip:
      "Cuántas tareas en segundo plano pueden ejecutarse a la vez con el modo de bajo consumo desactivado. Más terminan antes pero usan más CPU y disco.",
    eventsRetentionDaysTip:
      "Cuánto tiempo guarda el servidor su registro de actividad e historial de tareas. Las entradas antiguas se recortan. Nunca borra contenido; para quitar contenido automáticamente, usa las reglas de «Se va pronto».",
  },
  pt: {
    settingsHelpFor: "Ajuda: {setting}",
    settingsSearchLabel: "Pesquisar nas configurações",
    settingsSearchPlaceholder: "Pesquisar configurações…",
    settingsSearchEmpty: "Nenhuma configuração encontrada",
    settingsSearchConsole: "Console",
    settingsSearchManage: "Gerenciar",
    consoleMenuPurpose:
      "Configurações do servidor: como o Beamfall funciona, quem pode usá-lo e como mantê-lo saudável. Exige a senha de administrador.",
    consoleMenuCrossLink: "Organizando mídia? Vá para Gerenciar",
    manageMenuPurpose:
      "Trabalho diário nas bibliotecas: corrigir correspondências, editar detalhes, organizar e arrumar arquivos.",
    manageMenuCrossLink: "Mudando configurações do servidor? Vá para o Console",
    navOptionalFeatures: "Recursos opcionais",
    navParentalControls: "Controle parental",
    navApiKeys: "Chaves e tokens de API",
    navLeavingSoonRules: "Regras de “Saindo em breve”",
    navUpdateNotifications: "Avisos de atualização",
    navPlaybackSignIn: "Reprodução e login",
    navTrash: "Lixeira",
    navUnlockRestricted: "Mostrar conteúdo restrito",
    navSwitchProfile: "Trocar de perfil",
    configPageLead:
      "Como o vídeo é convertido para dispositivos que não reproduzem o arquivo original, e por quanto tempo as pessoas ficam conectadas.",
    transcodeAutomatic: "Automático",
    transcodeHardwareEnabledTip:
      "Usa o mecanismo de vídeo da sua placa de vídeo ou processador para converter vídeo. É muito mais rápido e pesa menos na CPU. Se a reprodução falhar ou ficar estranha, desative.",
    transcodeMaxConcurrentTip:
      "Quantos vídeos o servidor converte ao mesmo tempo. Automático escolhe um limite pelo seu hardware. Diminua se a reprodução travar quando várias pessoas assistem ao mesmo tempo.",
    transcodeCodecTip:
      "O formato de vídeo usado quando um arquivo precisa ser convertido. H.264 funciona em quase todos os dispositivos. HEVC gera arquivos menores, mas alguns dispositivos antigos não o reproduzem. Padrão do servidor deixa o Beamfall escolher.",
    transcodeCodecH264: "H.264 (mais compatível)",
    transcodeCodecHevc: "HEVC (arquivos menores)",
    transcodeQualityTip:
      "Qualidade de imagem contra banda e CPU ao converter. Equilibrada serve para a maioria das casas. Baixa ajuda conexões lentas. Alta precisa de uma rede rápida e mais CPU.",
    transcodeQualityLow: "Baixa (economiza banda)",
    transcodeQualityBalanced: "Equilibrada",
    transcodeQualityHigh: "Alta (melhor imagem)",
    transcodeDeviceTip:
      "Qual codificador de hardware usar: vaapi, qsv, nvenc, videotoolbox, amf ou um caminho de dispositivo como /dev/dri/renderD128. Deixe vazio para o servidor escolher.",
    transcodeTempDirTip:
      "Pasta onde o vídeo convertido é gravado durante a reprodução. Use um disco rápido com espaço livre. Deixe vazio para o padrão do servidor. Vale após reiniciar.",
    governorLowPowerTip:
      "Ativado por padrão. Tarefas em segundo plano, como varredura, artes e análise, rodam uma de cada vez para que a reprodução sempre venha primeiro. Desative em um servidor potente para terminar mais cedo.",
    governorBackgroundWorkersTip:
      "Quantas tarefas em segundo plano podem rodar ao mesmo tempo quando o modo de economia de energia está desativado. Mais terminam antes, mas usam mais CPU e disco.",
    eventsRetentionDaysTip:
      "Por quanto tempo o servidor guarda seu registro de atividade e histórico de tarefas. Entradas antigas são removidas. Isso nunca exclui mídia; para remover mídia automaticamente, use as regras de “Saindo em breve”.",
  },
};

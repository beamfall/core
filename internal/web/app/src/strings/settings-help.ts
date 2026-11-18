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
    settingsSearchCount: "{count, plural, one {# result} other {# results}}",
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
      "Uses the video engine in your graphics card or processor when the server found a working one at startup: faster and lighter on the CPU. Turn it off to convert in software only. Applies to conversions that start after you save.",
    transcodeMaxConcurrentTip:
      "How many videos the server converts at the same time. Automatic picks a limit from your hardware. Lower it if playback stutters when several people watch at once.",
    transcodeCodecTip:
      "Video format for converted files. Server default uses HEVC (smaller files) when the device and the hardware encoder support it, and H.264 otherwise. H.264 only plays on almost every device. Applies to new conversions.",
    transcodeCodecH264: "H.264 only (most compatible)",
    transcodeQualityTip:
      "Picture quality against bandwidth and processing. Low saves bandwidth and CPU; High gives a better picture for more CPU or graphics-card work; Server default is Balanced. Applies to new conversions.",
    transcodeQualityLow: "Low (saves bandwidth)",
    transcodeQualityBalanced: "Balanced",
    transcodeQualityHigh: "High (best picture)",
    transcodeDeviceTip:
      "Which hardware encoder to use: vaapi, qsv, nvenc, videotoolbox, amf, or a device path such as /dev/dri/renderD128. Leave empty to let the server choose. Takes effect when the server restarts.",
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
    settingsSearchCount: "{count, plural, one {# résultat} other {# résultats}}",
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
      "Utilise le moteur vidéo de votre carte graphique ou processeur si le serveur en a trouvé un qui fonctionne au démarrage : plus rapide et plus léger pour le processeur. Désactivez-le pour convertir en logiciel seulement. S’applique aux nouvelles conversions.",
    transcodeMaxConcurrentTip:
      "Nombre de vidéos que le serveur convertit en même temps. Automatique choisit une limite selon votre matériel. Baissez-la si la lecture saccade quand plusieurs personnes regardent.",
    transcodeCodecTip:
      "Format vidéo des fichiers convertis. Réglage par défaut du serveur utilise HEVC (fichiers plus petits) si l’appareil et l’encodeur matériel le gèrent, sinon H.264. H.264 uniquement se lit presque partout. S’applique aux nouvelles conversions.",
    transcodeCodecH264: "H.264 uniquement (le plus compatible)",
    transcodeQualityTip:
      "Qualité d’image contre bande passante et calcul. Basse économise bande passante et processeur ; Haute donne une meilleure image pour plus de calcul ; Réglage par défaut du serveur vaut Équilibrée. S’applique aux nouvelles conversions.",
    transcodeQualityLow: "Basse (économise la bande passante)",
    transcodeQualityBalanced: "Équilibrée",
    transcodeQualityHigh: "Haute (meilleure image)",
    transcodeDeviceTip:
      "L’encodeur matériel à utiliser : vaapi, qsv, nvenc, videotoolbox, amf, ou un chemin de périphérique comme /dev/dri/renderD128. Laissez vide pour laisser le serveur choisir. Prend effet au redémarrage du serveur.",
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
    settingsSearchCount: "{count, plural, one {# Ergebnis} other {# Ergebnisse}}",
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
      "Nutzt die Video-Engine Ihrer Grafikkarte oder Ihres Prozessors, wenn der Server beim Start eine funktionierende gefunden hat: schneller und schont die CPU. Ausgeschaltet wird nur per Software umgewandelt. Gilt für neue Umwandlungen.",
    transcodeMaxConcurrentTip:
      "Wie viele Videos der Server gleichzeitig umwandelt. Automatisch wählt ein Limit passend zu Ihrer Hardware. Senken Sie es, wenn die Wiedergabe ruckelt, sobald mehrere Personen schauen.",
    transcodeCodecTip:
      "Videoformat umgewandelter Dateien. Servervorgabe nutzt HEVC (kleinere Dateien), wenn Gerät und Hardware-Encoder es unterstützen, sonst H.264. Nur H.264 läuft auf fast jedem Gerät. Gilt für neue Umwandlungen.",
    transcodeCodecH264: "Nur H.264 (am kompatibelsten)",
    transcodeQualityTip:
      "Bildqualität gegen Bandbreite und Rechenaufwand. Niedrig spart Bandbreite und CPU; Hoch liefert ein besseres Bild bei mehr Rechenarbeit; Servervorgabe entspricht Ausgewogen. Gilt für neue Umwandlungen.",
    transcodeQualityLow: "Niedrig (spart Bandbreite)",
    transcodeQualityBalanced: "Ausgewogen",
    transcodeQualityHigh: "Hoch (bestes Bild)",
    transcodeDeviceTip:
      "Welcher Hardware-Encoder genutzt wird: vaapi, qsv, nvenc, videotoolbox, amf oder ein Gerätepfad wie /dev/dri/renderD128. Leer lassen, damit der Server wählt. Wirkt nach einem Neustart des Servers.",
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
    settingsSearchCount: "{count, plural, one {# resultado} other {# resultados}}",
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
      "Usa el motor de vídeo de tu tarjeta gráfica o procesador si el servidor encontró uno que funciona al iniciarse: más rápido y con menos carga de CPU. Desactívalo para convertir solo por software. Se aplica a las nuevas conversiones.",
    transcodeMaxConcurrentTip:
      "Cuántos vídeos convierte el servidor a la vez. Automático elige un límite según tu hardware. Bájalo si la reproducción da tirones cuando varias personas ven algo a la vez.",
    transcodeCodecTip:
      "Formato de vídeo de los archivos convertidos. Valor predeterminado del servidor usa HEVC (archivos más pequeños) si el dispositivo y el codificador por hardware lo admiten, y si no H.264. Solo H.264 funciona casi en todas partes. Se aplica a las nuevas conversiones.",
    transcodeCodecH264: "Solo H.264 (el más compatible)",
    transcodeQualityTip:
      "Calidad de imagen frente a ancho de banda y procesamiento. Baja ahorra ancho de banda y CPU; Alta da mejor imagen con más procesamiento; Valor predeterminado del servidor equivale a Equilibrada. Se aplica a las nuevas conversiones.",
    transcodeQualityLow: "Baja (ahorra ancho de banda)",
    transcodeQualityBalanced: "Equilibrada",
    transcodeQualityHigh: "Alta (mejor imagen)",
    transcodeDeviceTip:
      "Qué codificador por hardware usar: vaapi, qsv, nvenc, videotoolbox, amf o una ruta de dispositivo como /dev/dri/renderD128. Déjalo vacío para que elija el servidor. Se aplica al reiniciar el servidor.",
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
    settingsSearchCount: "{count, plural, one {# resultado} other {# resultados}}",
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
      "Usa o mecanismo de vídeo da sua placa de vídeo ou processador se o servidor encontrou um que funciona ao iniciar: mais rápido e mais leve para a CPU. Desative para converter só por software. Vale para novas conversões.",
    transcodeMaxConcurrentTip:
      "Quantos vídeos o servidor converte ao mesmo tempo. Automático escolhe um limite pelo seu hardware. Diminua se a reprodução travar quando várias pessoas assistem ao mesmo tempo.",
    transcodeCodecTip:
      "Formato de vídeo dos arquivos convertidos. Padrão do servidor usa HEVC (arquivos menores) se o dispositivo e o codificador de hardware suportam, senão H.264. Só H.264 funciona em quase tudo. Vale para novas conversões.",
    transcodeCodecH264: "Só H.264 (mais compatível)",
    transcodeQualityTip:
      "Qualidade de imagem contra banda e processamento. Baixa economiza banda e CPU; Alta dá imagem melhor com mais processamento; Padrão do servidor equivale a Equilibrada. Vale para novas conversões.",
    transcodeQualityLow: "Baixa (economiza banda)",
    transcodeQualityBalanced: "Equilibrada",
    transcodeQualityHigh: "Alta (melhor imagem)",
    transcodeDeviceTip:
      "Qual codificador de hardware usar: vaapi, qsv, nvenc, videotoolbox, amf ou um caminho de dispositivo como /dev/dri/renderD128. Deixe vazio para o servidor escolher. Vale após reiniciar o servidor.",
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

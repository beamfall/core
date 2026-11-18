// Continues settings-help-access.ts (CORE-2445), split to keep each string file under 20 KB.
export const settingsHelpAccess2Strings = {
  en: {
    accessHelpBitrate4: "4 Mbit/s · 720p",
    accessHelpBitrate8: "8 Mbit/s · 1080p",
    accessHelpBitrate20: "20 Mbit/s · high-quality 1080p",
    accessHelpBitrate40: "40 Mbit/s · 4K",
    accessHelpBitrateMbps: "{mbps} Mbit/s",
    accessHelpExtraTimeLabel: "Approve extra-time requests automatically",
    accessHelpExtraTimeTip:
      "When this child asks for more time, the request is approved straight away, without an adult having to say yes. The current setting is not shown here, so switch it to set it.",
    accessHelpRatingLimitTip:
      "Hides anything rated above the level you pick, using this plugin's ratings. Choose No limit to show everything.",
    accessHelpLibraryAccessHint:
      "Tick the libraries this profile can browse. With none ticked, it sees no libraries.",
    // Remote access
    accessHelpRemoteAccessLead:
      "How this server can be reached from outside your home network, and the settings for it. Passwords and keys are never shown here.",
    accessHelpPortMappingTip:
      "Asks your router to forward a port to this server, the same as setting up port forwarding by hand. Your router must support UPnP or NAT-PMP. Off by default. Leave it off if you use Tailscale or WireGuard.",
    accessHelpReachability:
      "To check this, the server tries to reach itself through your public address. It sends only a connection test, never your library. Some routers can't loop back like this, so it may say unreachable even when outside devices can connect.",
    accessHelpServerRemoteCapTip:
      "Caps video quality for everyone watching away from home. Set it a little below your internet upload speed. A profile's own lower limit still applies. No limit by default.",
    accessHelpRelayTip:
      "Shows whether this server is connected to its relay. The relay is a last resort for watching away from home when Tailscale, WireGuard and Direct HTTPS can't reach the server. It passes traffic along but can't read it.",
    // Migration wizard
    accessHelpMigrateSourceLabel: "File to import",
    accessHelpMigrateSourceTip:
      "The full path, on this server, of the file you exported from the other app. Copy the file somewhere the server can read first. The line under the box names the file this importer expects.",
    accessHelpMigrateSourcePlex: "Plex's library database file (com.plexapp.plugins.library.db).",
    accessHelpMigrateSourceEmby: "Emby's library database file (library.db).",
    accessHelpMigrateSourceJellyfin: "Jellyfin's library database file (library.db).",
    accessHelpMigrateSourceNfo:
      "The folder that holds your .nfo files, usually the library folder.",
    accessHelpMigrateSourceTrakt: "Your Trakt watched-history export (a .csv file).",
  },
  fr: {
    accessHelpBitrate4: "4 Mbit/s · 720p",
    accessHelpBitrate8: "8 Mbit/s · 1080p",
    accessHelpBitrate20: "20 Mbit/s · 1080p haute qualité",
    accessHelpBitrate40: "40 Mbit/s · 4K",
    accessHelpBitrateMbps: "{mbps} Mbit/s",
    accessHelpExtraTimeLabel: "Accepter automatiquement les demandes de temps en plus",
    accessHelpExtraTimeTip:
      "Quand cet enfant demande plus de temps, la demande est acceptée tout de suite, sans qu’un adulte ait à dire oui. Le réglage actuel n’est pas affiché ici : basculez l’interrupteur pour le définir.",
    accessHelpRatingLimitTip:
      "Masque tout ce qui est classé au-dessus du niveau choisi, selon les classifications de ce plugin. Choisissez Aucune limite pour tout afficher.",
    accessHelpLibraryAccessHint:
      "Cochez les bibliothèques que ce profil peut parcourir. Si aucune n’est cochée, il ne voit aucune bibliothèque.",
    accessHelpRemoteAccessLead:
      "Comment joindre ce serveur depuis l’extérieur de votre réseau domestique, et les réglages correspondants. Les mots de passe et les clés ne sont jamais affichés ici.",
    accessHelpPortMappingTip:
      "Demande à votre routeur de rediriger un port vers ce serveur, comme une redirection de port faite à la main. Votre routeur doit prendre en charge UPnP ou NAT-PMP. Désactivé par défaut. Laissez-le désactivé si vous utilisez Tailscale ou WireGuard.",
    accessHelpReachability:
      "Pour le vérifier, le serveur essaie de se joindre via votre adresse publique. Il n’envoie qu’un test de connexion, jamais votre bibliothèque. Certains routeurs ne savent pas faire ce bouclage : le résultat peut alors indiquer injoignable alors que les appareils extérieurs se connectent.",
    accessHelpServerRemoteCapTip:
      "Limite la qualité vidéo pour tous ceux qui regardent hors du domicile. Réglez-la un peu en dessous de votre débit montant. La limite plus basse d’un profil s’applique toujours. Aucune limite par défaut.",
    accessHelpRelayTip:
      "Indique si ce serveur est connecté à son relais. Le relais est un dernier recours pour regarder hors du domicile quand Tailscale, WireGuard et le HTTPS direct ne peuvent pas joindre le serveur. Il transmet le trafic sans pouvoir le lire.",
    accessHelpMigrateSourceLabel: "Fichier à importer",
    accessHelpMigrateSourceTip:
      "Le chemin complet, sur ce serveur, du fichier exporté depuis l’autre application. Copiez d’abord le fichier à un endroit que le serveur peut lire. La ligne sous le champ indique le fichier attendu par cet importateur.",
    accessHelpMigrateSourcePlex:
      "Le fichier de base de données de la bibliothèque Plex (com.plexapp.plugins.library.db).",
    accessHelpMigrateSourceEmby:
      "Le fichier de base de données de la bibliothèque Emby (library.db).",
    accessHelpMigrateSourceJellyfin:
      "Le fichier de base de données de la bibliothèque Jellyfin (library.db).",
    accessHelpMigrateSourceNfo:
      "Le dossier qui contient vos fichiers .nfo, en général le dossier de la bibliothèque.",
    accessHelpMigrateSourceTrakt: "Votre export de l’historique Trakt (un fichier .csv).",
  },
  de: {
    accessHelpBitrate4: "4 Mbit/s · 720p",
    accessHelpBitrate8: "8 Mbit/s · 1080p",
    accessHelpBitrate20: "20 Mbit/s · 1080p in hoher Qualität",
    accessHelpBitrate40: "40 Mbit/s · 4K",
    accessHelpBitrateMbps: "{mbps} Mbit/s",
    accessHelpExtraTimeLabel: "Anfragen nach mehr Zeit automatisch genehmigen",
    accessHelpExtraTimeTip:
      "Wenn dieses Kind um mehr Zeit bittet, wird die Anfrage sofort genehmigt, ohne dass ein Erwachsener zustimmen muss. Die aktuelle Einstellung wird hier nicht angezeigt. Schalten Sie den Schalter um, um sie festzulegen.",
    accessHelpRatingLimitTip:
      "Blendet alles aus, was über der gewählten Stufe eingestuft ist, nach den Einstufungen dieses Plugins. Wählen Sie Keine Begrenzung, um alles anzuzeigen.",
    accessHelpLibraryAccessHint:
      "Haken Sie die Mediatheken an, die dieses Profil durchsuchen darf. Ist keine angehakt, sieht es keine Mediathek.",
    accessHelpRemoteAccessLead:
      "Wie dieser Server von außerhalb Ihres Heimnetzes erreichbar ist, und die Einstellungen dazu. Passwörter und Schlüssel werden hier nie angezeigt.",
    accessHelpPortMappingTip:
      "Bittet Ihren Router, einen Port an diesen Server weiterzuleiten, genau wie eine von Hand eingerichtete Portweiterleitung. Ihr Router muss UPnP oder NAT-PMP unterstützen. Standardmäßig aus. Lassen Sie es aus, wenn Sie Tailscale oder WireGuard nutzen.",
    accessHelpReachability:
      "Zur Prüfung versucht der Server, sich selbst über Ihre öffentliche Adresse zu erreichen. Er sendet nur einen Verbindungstest, nie Ihre Mediathek. Manche Router können diese Schleife nicht, dann kann „nicht erreichbar“ erscheinen, obwohl Geräte von außen verbinden können.",
    accessHelpServerRemoteCapTip:
      "Begrenzt die Videoqualität für alle, die außer Haus schauen. Stellen Sie sie etwas unter Ihre Upload-Geschwindigkeit. Die niedrigere Grenze eines Profils gilt weiterhin. Standardmäßig keine Begrenzung.",
    accessHelpRelayTip:
      "Zeigt, ob dieser Server mit seinem Relay verbunden ist. Das Relay ist der letzte Ausweg, um unterwegs zu schauen, wenn Tailscale, WireGuard und Direct HTTPS den Server nicht erreichen. Es leitet den Datenverkehr weiter, kann ihn aber nicht lesen.",
    accessHelpMigrateSourceLabel: "Zu importierende Datei",
    accessHelpMigrateSourceTip:
      "Der vollständige Pfad auf diesem Server zu der Datei, die Sie aus der anderen App exportiert haben. Kopieren Sie die Datei zuerst an einen Ort, den der Server lesen kann. Die Zeile unter dem Feld nennt die Datei, die dieser Importer erwartet.",
    accessHelpMigrateSourcePlex:
      "Die Mediathek-Datenbankdatei von Plex (com.plexapp.plugins.library.db).",
    accessHelpMigrateSourceEmby: "Die Mediathek-Datenbankdatei von Emby (library.db).",
    accessHelpMigrateSourceJellyfin: "Die Mediathek-Datenbankdatei von Jellyfin (library.db).",
    accessHelpMigrateSourceNfo: "Der Ordner mit Ihren .nfo-Dateien, meist der Mediathek-Ordner.",
    accessHelpMigrateSourceTrakt: "Ihr Export des Trakt-Verlaufs (eine .csv-Datei).",
  },
  es: {
    accessHelpBitrate4: "4 Mbit/s · 720p",
    accessHelpBitrate8: "8 Mbit/s · 1080p",
    accessHelpBitrate20: "20 Mbit/s · 1080p de alta calidad",
    accessHelpBitrate40: "40 Mbit/s · 4K",
    accessHelpBitrateMbps: "{mbps} Mbit/s",
    accessHelpExtraTimeLabel: "Aprobar automáticamente las peticiones de más tiempo",
    accessHelpExtraTimeTip:
      "Cuando este niño pide más tiempo, la petición se aprueba al momento, sin que un adulto tenga que aceptarla. El ajuste actual no se muestra aquí, así que cambia el interruptor para fijarlo.",
    accessHelpRatingLimitTip:
      "Oculta todo lo clasificado por encima del nivel que elijas, según las clasificaciones de este plugin. Elige Sin límite para mostrarlo todo.",
    accessHelpLibraryAccessHint:
      "Marca las bibliotecas que este perfil puede explorar. Si no marcas ninguna, no verá ninguna biblioteca.",
    accessHelpRemoteAccessLead:
      "Cómo se puede llegar a este servidor desde fuera de tu red doméstica, y sus ajustes. Aquí nunca se muestran contraseñas ni claves.",
    accessHelpPortMappingTip:
      "Pide a tu router que redirija un puerto a este servidor, igual que configurar a mano el reenvío de puertos. Tu router debe admitir UPnP o NAT-PMP. Desactivado por defecto. Déjalo desactivado si usas Tailscale o WireGuard.",
    accessHelpReachability:
      "Para comprobarlo, el servidor intenta llegar a sí mismo a través de tu dirección pública. Solo envía una prueba de conexión, nunca tu biblioteca. Algunos routers no permiten esta vuelta, así que puede indicar que no es accesible aunque los dispositivos de fuera sí puedan conectarse.",
    accessHelpServerRemoteCapTip:
      "Limita la calidad de vídeo para todos los que ven fuera de casa. Ponlo un poco por debajo de tu velocidad de subida. El límite más bajo de un perfil se sigue aplicando. Sin límite por defecto.",
    accessHelpRelayTip:
      "Muestra si este servidor está conectado a su relé. El relé es el último recurso para ver fuera de casa cuando Tailscale, WireGuard y el HTTPS directo no llegan al servidor. Transmite el tráfico sin poder leerlo.",
    accessHelpMigrateSourceLabel: "Archivo que importar",
    accessHelpMigrateSourceTip:
      "La ruta completa, en este servidor, del archivo que exportaste de la otra aplicación. Copia primero el archivo a un lugar que el servidor pueda leer. La línea bajo el cuadro indica qué archivo espera este importador.",
    accessHelpMigrateSourcePlex:
      "El archivo de base de datos de la biblioteca de Plex (com.plexapp.plugins.library.db).",
    accessHelpMigrateSourceEmby:
      "El archivo de base de datos de la biblioteca de Emby (library.db).",
    accessHelpMigrateSourceJellyfin:
      "El archivo de base de datos de la biblioteca de Jellyfin (library.db).",
    accessHelpMigrateSourceNfo:
      "La carpeta que contiene tus archivos .nfo, normalmente la carpeta de la biblioteca.",
    accessHelpMigrateSourceTrakt: "Tu exportación del historial de Trakt (un archivo .csv).",
  },
  pt: {
    accessHelpBitrate4: "4 Mbit/s · 720p",
    accessHelpBitrate8: "8 Mbit/s · 1080p",
    accessHelpBitrate20: "20 Mbit/s · 1080p de alta qualidade",
    accessHelpBitrate40: "40 Mbit/s · 4K",
    accessHelpBitrateMbps: "{mbps} Mbit/s",
    accessHelpExtraTimeLabel: "Aprovar automaticamente pedidos de mais tempo",
    accessHelpExtraTimeTip:
      "Quando esta criança pede mais tempo, o pedido é aprovado na hora, sem que um adulto precise aceitar. A configuração atual não aparece aqui, então mude o botão para defini-la.",
    accessHelpRatingLimitTip:
      "Oculta tudo o que tiver classificação acima do nível escolhido, segundo as classificações deste plugin. Escolha Sem limite para mostrar tudo.",
    accessHelpLibraryAccessHint:
      "Marque as bibliotecas que este perfil pode navegar. Sem nenhuma marcada, ele não vê nenhuma biblioteca.",
    accessHelpRemoteAccessLead:
      "Como este servidor pode ser acessado de fora da sua rede doméstica, e as configurações para isso. Senhas e chaves nunca aparecem aqui.",
    accessHelpPortMappingTip:
      "Pede ao seu roteador que encaminhe uma porta para este servidor, como configurar o redirecionamento de porta à mão. O roteador precisa suportar UPnP ou NAT-PMP. Desativado por padrão. Deixe desativado se você usa Tailscale ou WireGuard.",
    accessHelpReachability:
      "Para verificar, o servidor tenta alcançar a si mesmo pelo seu endereço público. Ele envia só um teste de conexão, nunca a sua biblioteca. Alguns roteadores não fazem esse retorno, então pode aparecer inacessível mesmo quando dispositivos de fora conseguem se conectar.",
    accessHelpServerRemoteCapTip:
      "Limita a qualidade do vídeo para todos que assistem fora de casa. Defina um pouco abaixo da sua velocidade de upload. O limite menor de um perfil continua valendo. Sem limite por padrão.",
    accessHelpRelayTip:
      "Mostra se este servidor está conectado ao seu relay. O relay é o último recurso para assistir fora de casa quando Tailscale, WireGuard e HTTPS direto não alcançam o servidor. Ele repassa o tráfego sem conseguir lê-lo.",
    accessHelpMigrateSourceLabel: "Arquivo para importar",
    accessHelpMigrateSourceTip:
      "O caminho completo, neste servidor, do arquivo que você exportou do outro aplicativo. Primeiro copie o arquivo para um lugar que o servidor consiga ler. A linha abaixo do campo indica o arquivo que este importador espera.",
    accessHelpMigrateSourcePlex:
      "O arquivo de banco de dados da biblioteca do Plex (com.plexapp.plugins.library.db).",
    accessHelpMigrateSourceEmby: "O arquivo de banco de dados da biblioteca do Emby (library.db).",
    accessHelpMigrateSourceJellyfin:
      "O arquivo de banco de dados da biblioteca do Jellyfin (library.db).",
    accessHelpMigrateSourceNfo: "A pasta com seus arquivos .nfo, geralmente a pasta da biblioteca.",
    accessHelpMigrateSourceTrakt: "Sua exportação do histórico do Trakt (um arquivo .csv).",
  },
};

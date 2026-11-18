// Continues settings-help-access.ts (CORE-2445), split to keep each string file under 20 KB.
export const settingsHelpAccess3Strings = {
  en: {
    accessHelpMigrateSourceLastfm: "Your Last.fm scrobbles export (a .csv file).",
    accessHelpMigrateSourceListenBrainz:
      "Your ListenBrainz listens export (a .json or .jsonl file).",
    accessHelpMigrateSourceNavidrome: "A Navidrome playlists and plays export (a .json file).",
    accessHelpMigrateSourceM3u: "A playlist file (.m3u or .m3u8).",
    accessHelpMigrateSourceImmich:
      "Immich's album list saved as a .json file (the reply from its /albums API).",
    accessHelpMigrateSourceOpml: "A podcast subscription list (an .opml file).",
    accessHelpMigrateSourceOvercast: "Overcast's extended OPML export (an .opml file).",
    accessHelpMigrateSourceAntennaPod: "AntennaPod's database export file.",
    accessHelpMigrateSourceAudiobookshelf:
      "An Audiobookshelf user and progress export (a .json file).",
    accessHelpMigrateMappingDesc:
      "Choose whose watch history and ratings the import writes to, and optionally a single library to match against.",
    accessHelpMigrateEffect: "Effect",
    accessHelpQbitCategory: "Category",
    // API
    accessHelpApiTokensTip:
      "An API token has full admin power over this server. Use it only for your own scripts, and never give it to a third-party tool. For other tools, make an API key instead.",
    accessHelpApiKeysTip:
      "An API key lets a third-party tool use this server with only the permissions you tick. Use a key, not a token, for any app or service you did not write.",
    accessHelpApiScopesTip:
      "Read catalog: list your libraries and media. Read sessions: see who is watching now. Start library scans: ask the server to look for new files. Subscribe to events: get live updates as things happen. Control playback: play, pause and stop what is playing.",
    accessHelpScopeIngest: "Start library scans",
    // Webhooks
    accessHelpWebhookSecretTip:
      "What goes here depends on the format. Generic: a key used to sign each message, so your receiver can check it came from this server. Gotify: the app token. ntfy: an access token, only if the topic needs one. Email: the mail server password.",
    accessHelpWebhookPrivateTip:
      "Lets this webhook send to a name on your home network, such as nas.local. Off by default, which blocks names that point to home-network addresses. An address typed as numbers, like 192.168.1.20, works without it.",
    accessHelpWebhookPublicUrlTip:
      "The address people use to reach this server from the internet, for example https://media.example.com. Notifications use it to build links back to the server. Leave it empty if you don't have one.",
  },
  fr: {
    accessHelpMigrateSourceLastfm: "Votre export des écoutes Last.fm (un fichier .csv).",
    accessHelpMigrateSourceListenBrainz:
      "Votre export des écoutes ListenBrainz (un fichier .json ou .jsonl).",
    accessHelpMigrateSourceNavidrome:
      "Un export des playlists et écoutes Navidrome (un fichier .json).",
    accessHelpMigrateSourceM3u: "Un fichier de playlist (.m3u ou .m3u8).",
    accessHelpMigrateSourceImmich:
      "La liste des albums Immich enregistrée dans un fichier .json (la réponse de son API /albums).",
    accessHelpMigrateSourceOpml: "Une liste d’abonnements de podcasts (un fichier .opml).",
    accessHelpMigrateSourceOvercast: "L’export OPML étendu d’Overcast (un fichier .opml).",
    accessHelpMigrateSourceAntennaPod: "Le fichier d’export de la base de données AntennaPod.",
    accessHelpMigrateSourceAudiobookshelf:
      "Un export des utilisateurs et de la progression Audiobookshelf (un fichier .json).",
    accessHelpMigrateMappingDesc:
      "Choisissez dans quel historique et quelles notes l’import écrit, et éventuellement une seule bibliothèque où chercher les correspondances.",
    accessHelpMigrateEffect: "Effet",
    accessHelpQbitCategory: "Catégorie",
    accessHelpApiTokensTip:
      "Un jeton d’API a tous les pouvoirs d’administration sur ce serveur. Utilisez-le seulement pour vos propres scripts et ne le donnez jamais à un outil tiers. Pour les autres outils, créez plutôt une clé d’API.",
    accessHelpApiKeysTip:
      "Une clé d’API permet à un outil tiers d’utiliser ce serveur avec les seules autorisations que vous cochez. Utilisez une clé, pas un jeton, pour toute application ou tout service que vous n’avez pas écrit.",
    accessHelpApiScopesTip:
      "Lire le catalogue : lister vos bibliothèques et médias. Lire les sessions : voir qui regarde en ce moment. Lancer des analyses : demander au serveur de chercher de nouveaux fichiers. S’abonner aux événements : recevoir les mises à jour en direct. Contrôler la lecture : lire, mettre en pause et arrêter ce qui est en cours.",
    accessHelpScopeIngest: "Lancer des analyses de bibliothèque",
    accessHelpWebhookSecretTip:
      "Ce qu’il faut saisir dépend du format. Générique : une clé qui signe chaque message, pour que le destinataire vérifie qu’il vient de ce serveur. Gotify : le jeton de l’application. ntfy : un jeton d’accès, seulement si le sujet en exige un. E-mail : le mot de passe du serveur de messagerie.",
    accessHelpWebhookPrivateTip:
      "Permet à ce webhook d’envoyer vers un nom de votre réseau domestique, comme nas.local. Désactivé par défaut, ce qui bloque les noms qui pointent vers des adresses du réseau domestique. Une adresse saisie en chiffres, comme 192.168.1.20, fonctionne sans ce réglage.",
    accessHelpWebhookPublicUrlTip:
      "L’adresse utilisée pour joindre ce serveur depuis Internet, par exemple https://media.example.com. Les notifications s’en servent pour créer des liens vers le serveur. Laissez vide si vous n’en avez pas.",
  },
  de: {
    accessHelpMigrateSourceLastfm: "Ihr Export der Last.fm-Scrobbles (eine .csv-Datei).",
    accessHelpMigrateSourceListenBrainz:
      "Ihr Export der ListenBrainz-Listens (eine .json- oder .jsonl-Datei).",
    accessHelpMigrateSourceNavidrome:
      "Ein Export der Navidrome-Playlists und -Wiedergaben (eine .json-Datei).",
    accessHelpMigrateSourceM3u: "Eine Playlist-Datei (.m3u oder .m3u8).",
    accessHelpMigrateSourceImmich:
      "Die Albumliste von Immich als .json-Datei gespeichert (die Antwort seiner /albums-API).",
    accessHelpMigrateSourceOpml: "Eine Liste von Podcast-Abos (eine .opml-Datei).",
    accessHelpMigrateSourceOvercast: "Der erweiterte OPML-Export von Overcast (eine .opml-Datei).",
    accessHelpMigrateSourceAntennaPod: "Die Datenbank-Exportdatei von AntennaPod.",
    accessHelpMigrateSourceAudiobookshelf:
      "Ein Export der Nutzer und des Fortschritts aus Audiobookshelf (eine .json-Datei).",
    accessHelpMigrateMappingDesc:
      "Wählen Sie, in wessen Verlauf und Bewertungen der Import schreibt, und optional eine einzelne Mediathek für den Abgleich.",
    accessHelpMigrateEffect: "Wirkung",
    accessHelpQbitCategory: "Kategorie",
    accessHelpApiTokensTip:
      "Ein API-Token hat volle Administratorrechte auf diesem Server. Nutzen Sie es nur für Ihre eigenen Skripte und geben Sie es nie an ein Fremdtool weiter. Erstellen Sie für andere Tools stattdessen einen API-Schlüssel.",
    accessHelpApiKeysTip:
      "Ein API-Schlüssel erlaubt einem Fremdtool, diesen Server nur mit den Rechten zu nutzen, die Sie anhaken. Nutzen Sie für jede App und jeden Dienst, die Sie nicht selbst geschrieben haben, einen Schlüssel statt eines Tokens.",
    accessHelpApiScopesTip:
      "Katalog lesen: Ihre Mediatheken und Medien auflisten. Sitzungen lesen: sehen, wer gerade schaut. Mediathek-Scans starten: den Server nach neuen Dateien suchen lassen. Ereignisse abonnieren: Live-Meldungen erhalten, wenn etwas passiert. Wiedergabe steuern: abspielen, pausieren und stoppen.",
    accessHelpScopeIngest: "Mediathek-Scans starten",
    accessHelpWebhookSecretTip:
      "Was hier hingehört, hängt vom Format ab. Generisch: ein Schlüssel, der jede Nachricht signiert, damit Ihr Empfänger prüfen kann, dass sie von diesem Server kommt. Gotify: das App-Token. ntfy: ein Zugriffstoken, nur wenn das Thema eines braucht. E-Mail: das Passwort des Mailservers.",
    accessHelpWebhookPrivateTip:
      "Erlaubt diesem Webhook, an einen Namen in Ihrem Heimnetz zu senden, etwa nas.local. Standardmäßig aus. Dann werden Namen blockiert, die auf Heimnetz-Adressen zeigen. Eine als Zahlen eingegebene Adresse wie 192.168.1.20 funktioniert auch ohne diese Einstellung.",
    accessHelpWebhookPublicUrlTip:
      "Die Adresse, unter der dieser Server aus dem Internet erreichbar ist, zum Beispiel https://media.example.com. Benachrichtigungen bauen damit Links zum Server. Lassen Sie das Feld leer, wenn Sie keine haben.",
  },
  es: {
    accessHelpMigrateSourceLastfm: "Tu exportación de scrobbles de Last.fm (un archivo .csv).",
    accessHelpMigrateSourceListenBrainz:
      "Tu exportación de escuchas de ListenBrainz (un archivo .json o .jsonl).",
    accessHelpMigrateSourceNavidrome:
      "Una exportación de listas y reproducciones de Navidrome (un archivo .json).",
    accessHelpMigrateSourceM3u: "Un archivo de lista de reproducción (.m3u o .m3u8).",
    accessHelpMigrateSourceImmich:
      "La lista de álbumes de Immich guardada como archivo .json (la respuesta de su API /albums).",
    accessHelpMigrateSourceOpml: "Una lista de suscripciones de pódcast (un archivo .opml).",
    accessHelpMigrateSourceOvercast: "La exportación OPML ampliada de Overcast (un archivo .opml).",
    accessHelpMigrateSourceAntennaPod:
      "El archivo de exportación de la base de datos de AntennaPod.",
    accessHelpMigrateSourceAudiobookshelf:
      "Una exportación de usuarios y progreso de Audiobookshelf (un archivo .json).",
    accessHelpMigrateMappingDesc:
      "Elige en qué historial y valoraciones escribe la importación y, si quieres, una sola biblioteca donde buscar coincidencias.",
    accessHelpMigrateEffect: "Efecto",
    accessHelpQbitCategory: "Categoría",
    accessHelpApiTokensTip:
      "Un token de API tiene todo el poder de administración sobre este servidor. Úsalo solo para tus propios scripts y nunca se lo des a una herramienta de terceros. Para otras herramientas, crea una clave de API.",
    accessHelpApiKeysTip:
      "Una clave de API permite que una herramienta de terceros use este servidor solo con los permisos que marques. Usa una clave, no un token, para cualquier aplicación o servicio que no hayas escrito tú.",
    accessHelpApiScopesTip:
      "Leer catálogo: listar tus bibliotecas y contenidos. Leer sesiones: ver quién está viendo ahora. Iniciar análisis de biblioteca: pedir al servidor que busque archivos nuevos. Suscribirse a eventos: recibir avisos en directo cuando ocurre algo. Controlar la reproducción: reproducir, pausar y detener.",
    accessHelpScopeIngest: "Iniciar análisis de biblioteca",
    accessHelpWebhookSecretTip:
      "Lo que va aquí depende del formato. Genérico: una clave que firma cada mensaje para que el receptor compruebe que viene de este servidor. Gotify: el token de la aplicación. ntfy: un token de acceso, solo si el tema lo pide. Correo: la contraseña del servidor de correo.",
    accessHelpWebhookPrivateTip:
      "Permite que este webhook envíe a un nombre de tu red doméstica, como nas.local. Desactivado por defecto, lo que bloquea los nombres que apuntan a direcciones de la red doméstica. Una dirección escrita con números, como 192.168.1.20, funciona sin este ajuste.",
    accessHelpWebhookPublicUrlTip:
      "La dirección con la que se llega a este servidor desde internet, por ejemplo https://media.example.com. Las notificaciones la usan para crear enlaces al servidor. Déjala vacía si no tienes una.",
  },
  pt: {
    accessHelpMigrateSourceLastfm: "Sua exportação de scrobbles do Last.fm (um arquivo .csv).",
    accessHelpMigrateSourceListenBrainz:
      "Sua exportação de audições do ListenBrainz (um arquivo .json ou .jsonl).",
    accessHelpMigrateSourceNavidrome:
      "Uma exportação de playlists e reproduções do Navidrome (um arquivo .json).",
    accessHelpMigrateSourceM3u: "Um arquivo de playlist (.m3u ou .m3u8).",
    accessHelpMigrateSourceImmich:
      "A lista de álbuns do Immich salva como arquivo .json (a resposta da API /albums dele).",
    accessHelpMigrateSourceOpml: "Uma lista de assinaturas de podcasts (um arquivo .opml).",
    accessHelpMigrateSourceOvercast: "A exportação OPML estendida do Overcast (um arquivo .opml).",
    accessHelpMigrateSourceAntennaPod: "O arquivo de exportação do banco de dados do AntennaPod.",
    accessHelpMigrateSourceAudiobookshelf:
      "Uma exportação de usuários e progresso do Audiobookshelf (um arquivo .json).",
    accessHelpMigrateMappingDesc:
      "Escolha em qual histórico e avaliações a importação grava e, se quiser, uma única biblioteca para procurar correspondências.",
    accessHelpMigrateEffect: "Efeito",
    accessHelpQbitCategory: "Categoria",
    accessHelpApiTokensTip:
      "Um token de API tem poder total de administração sobre este servidor. Use-o só nos seus próprios scripts e nunca o entregue a uma ferramenta de terceiros. Para outras ferramentas, crie uma chave de API.",
    accessHelpApiKeysTip:
      "Uma chave de API permite que uma ferramenta de terceiros use este servidor só com as permissões que você marcar. Use uma chave, não um token, para qualquer aplicativo ou serviço que você não escreveu.",
    accessHelpApiScopesTip:
      "Ler catálogo: listar suas bibliotecas e mídias. Ler sessões: ver quem está assistindo agora. Iniciar verificações da biblioteca: pedir ao servidor que procure arquivos novos. Assinar eventos: receber avisos ao vivo quando algo acontece. Controlar a reprodução: reproduzir, pausar e parar.",
    accessHelpScopeIngest: "Iniciar verificações da biblioteca",
    accessHelpWebhookSecretTip:
      "O que vai aqui depende do formato. Genérico: uma chave que assina cada mensagem para o receptor confirmar que veio deste servidor. Gotify: o token do aplicativo. ntfy: um token de acesso, só se o tópico exigir. E-mail: a senha do servidor de e-mail.",
    accessHelpWebhookPrivateTip:
      "Permite que este webhook envie para um nome da sua rede doméstica, como nas.local. Desativado por padrão, o que bloqueia nomes que apontam para endereços da rede doméstica. Um endereço digitado em números, como 192.168.1.20, funciona sem esta opção.",
    accessHelpWebhookPublicUrlTip:
      "O endereço usado para acessar este servidor pela internet, por exemplo https://media.example.com. As notificações o usam para criar links para o servidor. Deixe vazio se você não tiver um.",
  },
};

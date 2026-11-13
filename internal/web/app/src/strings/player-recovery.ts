// Player starting, network and unavailable-reason copy (CORE-1146, CORE-1362, CORE-1363).
export const playerRecoveryStrings = {
  en: {
    playerLoading: "Loading…",
    playerWaitingForNetwork: "Waiting for network…",
    playbackServerReason: "Server said: {reason}",
    playbackTranscodeDisallowed:
      "This item needs converting to play here, and converting is turned off for this profile.",
    playbackStreamLimitReached:
      "This profile has reached its stream limit. Stop another stream, then try again.",
    playbackTranscodeCapacityReached:
      "The server is busy converting other streams. Try again in a moment.",
    playbackDolbyVisionUnavailable:
      "This Dolby Vision video can't be converted to play on this device.",
    playbackHdrToneMapUnavailable: "This server can't convert this HDR video for this device.",
    playbackSourceEndsEarly:
      "This file ends early. The rest of it is missing or damaged on the server.",
  },
  fr: {
    playerLoading: "Chargement…",
    playerWaitingForNetwork: "En attente du réseau…",
    playbackServerReason: "Réponse du serveur : {reason}",
    playbackTranscodeDisallowed:
      "Cet élément doit être converti pour être lu ici, et la conversion est désactivée pour ce profil.",
    playbackStreamLimitReached:
      "Ce profil a atteint sa limite de flux. Arrêtez un autre flux, puis réessayez.",
    playbackTranscodeCapacityReached:
      "Le serveur est occupé à convertir d’autres flux. Réessayez dans un instant.",
    playbackDolbyVisionUnavailable:
      "Cette vidéo Dolby Vision ne peut pas être convertie pour cet appareil.",
    playbackHdrToneMapUnavailable:
      "Ce serveur ne peut pas convertir cette vidéo HDR pour cet appareil.",
    playbackSourceEndsEarly:
      "Ce fichier s’arrête avant la fin. Le reste est manquant ou endommagé sur le serveur.",
  },
  de: {
    playerLoading: "Wird geladen…",
    playerWaitingForNetwork: "Warten auf Netzwerk…",
    playbackServerReason: "Antwort des Servers: {reason}",
    playbackTranscodeDisallowed:
      "Dieses Element muss für die Wiedergabe hier konvertiert werden, und die Konvertierung ist für dieses Profil deaktiviert.",
    playbackStreamLimitReached:
      "Dieses Profil hat sein Stream-Limit erreicht. Einen anderen Stream beenden und erneut versuchen.",
    playbackTranscodeCapacityReached:
      "Der Server konvertiert gerade andere Streams. Bitte gleich erneut versuchen.",
    playbackDolbyVisionUnavailable:
      "Dieses Dolby-Vision-Video kann für dieses Gerät nicht konvertiert werden.",
    playbackHdrToneMapUnavailable:
      "Dieser Server kann dieses HDR-Video für dieses Gerät nicht konvertieren.",
    playbackSourceEndsEarly:
      "Diese Datei endet zu früh. Der Rest fehlt auf dem Server oder ist beschädigt.",
  },
  es: {
    playerLoading: "Cargando…",
    playerWaitingForNetwork: "Esperando la red…",
    playbackServerReason: "Respuesta del servidor: {reason}",
    playbackTranscodeDisallowed:
      "Este elemento necesita convertirse para reproducirse aquí, y la conversión está desactivada para este perfil.",
    playbackStreamLimitReached:
      "Este perfil alcanzó su límite de transmisiones. Detén otra transmisión y vuelve a intentarlo.",
    playbackTranscodeCapacityReached:
      "El servidor está ocupado convirtiendo otras transmisiones. Inténtalo de nuevo en un momento.",
    playbackDolbyVisionUnavailable:
      "Este video Dolby Vision no se puede convertir para este dispositivo.",
    playbackHdrToneMapUnavailable:
      "Este servidor no puede convertir este video HDR para este dispositivo.",
    playbackSourceEndsEarly:
      "Este archivo termina antes de tiempo. El resto falta o está dañado en el servidor.",
  },
  pt: {
    playerLoading: "Carregando…",
    playerWaitingForNetwork: "Aguardando a rede…",
    playbackServerReason: "Resposta do servidor: {reason}",
    playbackTranscodeDisallowed:
      "Este item precisa ser convertido para ser reproduzido aqui, e a conversão está desativada para este perfil.",
    playbackStreamLimitReached:
      "Este perfil atingiu o limite de transmissões. Pare outra transmissão e tente novamente.",
    playbackTranscodeCapacityReached:
      "O servidor está ocupado convertendo outras transmissões. Tente novamente em instantes.",
    playbackDolbyVisionUnavailable:
      "Este vídeo Dolby Vision não pode ser convertido para este dispositivo.",
    playbackHdrToneMapUnavailable:
      "Este servidor não pode converter este vídeo HDR para este dispositivo.",
    playbackSourceEndsEarly:
      "Este arquivo termina antes do fim. O restante está ausente ou danificado no servidor.",
  },
} as const;

export const profileSearchStrings = {
  en: {
    // Profile Settings (ADR-0082 — viewer-resident, self-service per-Profile prefs).
    settingsTitle: "Profile settings",
    settingsLead:
      "Preferences for this profile on this device. Operator policy is managed in the Console.",
    settingsPlaybackHeading: "Playback",
    settingsAutoSkipHeading: "Auto-skip",
    settingsAutoSkipLead: "Automatically skip these segments instead of showing a skip button.",
    settingsAutoSkipIntro: "Skip intros",
    settingsAutoSkipRecap: "Skip recaps",
    settingsAutoSkipCredits: "Skip credits",
    settingsPlaybackSpeedLabel: "Default playback speed",
    settingsTrimSilence: "Remove long pauses",
    settingsTrimSilenceLead:
      "Skips long silent gaps between segments when a silence map is available. Smart speed is planned separately.",
    settingsPrePlay: "Play intros and previews first",
    settingsPrePlayLead:
      "Plays the server's intro clips, then previews where an extension offers them, before a title starts.",
    settingsPrePlayExtrasLabel: "Previews before a title",
    settingsSkipIntervalLabel: "Skip buttons",
    settingsStillWatchingLabel: "Ask if anyone is still watching",
    settingsStillWatchingLead:
      "After this many items in a row start on their own, pause and ask before the next one.",
    settingsStillWatchingNever: "Never",
    settingsStillWatchingAfter: "{count, plural, one {After # item} other {After # items}}",
    settingsDownstreamAudioLabel: "Audio output",
    settingsDownstreamAudioLead:
      "Choose stereo for TV speakers, or surround when your browser routes audio to a receiver.",
    settingsDownstreamAudioStereo: "Stereo",
    settingsDownstreamAudioSurround: "Surround",
    settingsAccessibilityHeading: "Accessibility",
    settingsReduceMotion: "Reduce motion",
    settingsReduceMotionLead: "Minimize non-essential animations across the app.",
    settingsRecommendHeading: "Recommendations",
    settingsRecommendOptIn: "Personalized recommendations",
    settingsRecommendOptInLead: "Allow recommend-mode search to use this profile's activity.",
    settingsCreditsHeading: "Credits",
    settingsCreditsLead: "Metadata on this server comes from these sources.",
    settingsManagedNote: "Some settings are managed by your operator and are not shown here.",
    settingsSaveFailed: "Could not save this preference.",
    settingsUnavailable: "Preferences are unavailable right now.",
    // Mode-aware search (ADR-0083 §30; ported from internal/web/static/strings.js).
    searchModeLabel: "Search mode",
    searchModeKeyword: "Keyword",
    searchModeSemantic: "Semantic",
    searchModeRecommend: "Recommend",
    searchModeFellBackToKeyword: "Semantic search unavailable. Showing keyword results.",
    searchRestrictedNotice: "Some results may be hidden by your access settings.",
    // Leak-safe no-access / stale deep-link terminal surface (ADR-0083 §32).
  },
  fr: {
    settingsTitle: "Paramètres du profil",
    settingsLead:
      "Préférences de ce profil sur cet appareil. La politique de l’opérateur se gère dans la Console.",
    settingsPlaybackHeading: "Lecture",
    settingsAutoSkipHeading: "Saut automatique",
    settingsAutoSkipLead: "Sauter automatiquement ces segments au lieu d’afficher un bouton.",
    settingsAutoSkipIntro: "Sauter les introductions",
    settingsAutoSkipRecap: "Sauter les résumés",
    settingsAutoSkipCredits: "Sauter le générique",
    settingsPlaybackSpeedLabel: "Vitesse de lecture par défaut",
    settingsTrimSilence: "Supprimer les longues pauses",
    settingsTrimSilenceLead:
      "Saute les longs silences entre les segments lorsqu’une carte des silences est disponible. La vitesse intelligente est prévue à part.",
    settingsPrePlay: "Lire d’abord les intros et les aperçus",
    settingsPrePlayLead:
      "Lit les clips d’intro du serveur, puis les aperçus proposés par une extension, avant le début d’un titre.",
    settingsPrePlayExtrasLabel: "Aperçus avant un titre",
    settingsSkipIntervalLabel: "Boutons de saut",
    settingsStillWatchingLabel: "Demander si quelqu’un regarde encore",
    settingsStillWatchingLead:
      "Après ce nombre d’éléments lancés automatiquement à la suite, mettre en pause et demander avant le suivant.",
    settingsStillWatchingNever: "Jamais",
    settingsStillWatchingAfter: "{count, plural, one {Après # élément} other {Après # éléments}}",
    settingsDownstreamAudioLabel: "Sortie audio",
    settingsDownstreamAudioLead:
      "Choisissez stéréo pour les haut-parleurs du téléviseur, ou ambiophonique si votre navigateur envoie le son vers un récepteur.",
    settingsDownstreamAudioStereo: "Stéréo",
    settingsDownstreamAudioSurround: "Ambiophonique",
    settingsAccessibilityHeading: "Accessibilité",
    settingsReduceMotion: "Réduire les animations",
    settingsReduceMotionLead: "Limiter les animations non essentielles dans l’application.",
    settingsRecommendHeading: "Recommandations",
    settingsRecommendOptIn: "Recommandations personnalisées",
    settingsRecommendOptInLead:
      "Permettre à la recherche en mode recommandation d’utiliser l’activité de ce profil.",
    settingsCreditsHeading: "Crédits",
    settingsCreditsLead: "Les métadonnées de ce serveur proviennent de ces sources.",
    settingsManagedNote:
      "Certains paramètres sont gérés par votre opérateur et ne sont pas affichés ici.",
    settingsSaveFailed: "Impossible d’enregistrer cette préférence.",
    settingsUnavailable: "Les préférences sont indisponibles pour le moment.",
    searchModeLabel: "Mode de recherche",
    searchModeKeyword: "Mots-clés",
    searchModeSemantic: "Sémantique",
    searchModeRecommend: "Recommandations",
    searchModeFellBackToKeyword:
      "Recherche sémantique indisponible. Résultats par mots-clés affichés.",
    searchRestrictedNotice: "Certains résultats peuvent être masqués par vos réglages d’accès.",
  },
  de: {
    settingsTitle: "Profileinstellungen",
    settingsLead:
      "Einstellungen für dieses Profil auf diesem Gerät. Betreiberrichtlinien werden in der Console verwaltet.",
    settingsPlaybackHeading: "Wiedergabe",
    settingsAutoSkipHeading: "Automatisch überspringen",
    settingsAutoSkipLead:
      "Diese Abschnitte automatisch überspringen, statt eine Überspringen-Schaltfläche zu zeigen.",
    settingsAutoSkipIntro: "Intros überspringen",
    settingsAutoSkipRecap: "Rückblicke überspringen",
    settingsAutoSkipCredits: "Abspann überspringen",
    settingsPlaybackSpeedLabel: "Standard-Wiedergabegeschwindigkeit",
    settingsTrimSilence: "Lange Pausen entfernen",
    settingsTrimSilenceLead:
      "Überspringt lange stille Lücken zwischen Abschnitten, wenn eine Stille-Karte vorliegt. Intelligente Geschwindigkeit ist separat geplant.",
    settingsPrePlay: "Intros und Vorschauen zuerst abspielen",
    settingsPrePlayLead:
      "Spielt die Intro-Clips des Servers und dann Vorschauen, die eine Erweiterung anbietet, bevor ein Titel startet.",
    settingsPrePlayExtrasLabel: "Vorschauen vor einem Titel",
    settingsSkipIntervalLabel: "Überspringen-Tasten",
    settingsStillWatchingLabel: "Nachfragen, ob noch jemand zuschaut",
    settingsStillWatchingLead:
      "Nach so vielen automatisch startenden Titeln in Folge pausieren und vor dem nächsten nachfragen.",
    settingsStillWatchingNever: "Nie",
    settingsStillWatchingAfter: "{count, plural, one {Nach # Titel} other {Nach # Titeln}}",
    settingsDownstreamAudioLabel: "Audioausgabe",
    settingsDownstreamAudioLead:
      "Wähle Stereo für Fernsehlautsprecher oder Surround, wenn dein Browser den Ton an einen Receiver leitet.",
    settingsDownstreamAudioStereo: "Stereo",
    settingsDownstreamAudioSurround: "Surround",
    settingsAccessibilityHeading: "Barrierefreiheit",
    settingsReduceMotion: "Bewegung reduzieren",
    settingsReduceMotionLead: "Nicht notwendige Animationen in der gesamten App minimieren.",
    settingsRecommendHeading: "Empfehlungen",
    settingsRecommendOptIn: "Personalisierte Empfehlungen",
    settingsRecommendOptInLead:
      "Erlaubt der Empfehlungssuche, die Aktivität dieses Profils zu nutzen.",
    settingsCreditsHeading: "Quellenangaben",
    settingsCreditsLead: "Die Metadaten auf diesem Server stammen aus diesen Quellen.",
    settingsManagedNote:
      "Einige Einstellungen werden von deinem Betreiber verwaltet und hier nicht angezeigt.",
    settingsSaveFailed: "Diese Einstellung konnte nicht gespeichert werden.",
    settingsUnavailable: "Einstellungen sind gerade nicht verfügbar.",
    searchModeLabel: "Suchmodus",
    searchModeKeyword: "Stichwort",
    searchModeSemantic: "Semantisch",
    searchModeRecommend: "Empfehlen",
    searchModeFellBackToKeyword:
      "Semantische Suche nicht verfügbar. Stichwortergebnisse werden angezeigt.",
    searchRestrictedNotice:
      "Einige Ergebnisse sind durch deine Zugriffseinstellungen möglicherweise ausgeblendet.",
  },
  es: {
    settingsTitle: "Ajustes del perfil",
    settingsLead:
      "Preferencias de este perfil en este dispositivo. La política del operador se gestiona en la Console.",
    settingsPlaybackHeading: "Reproducción",
    settingsAutoSkipHeading: "Omisión automática",
    settingsAutoSkipLead:
      "Omitir automáticamente estos segmentos en lugar de mostrar un botón para omitir.",
    settingsAutoSkipIntro: "Omitir introducciones",
    settingsAutoSkipRecap: "Omitir resúmenes",
    settingsAutoSkipCredits: "Omitir créditos",
    settingsPlaybackSpeedLabel: "Velocidad de reproducción predeterminada",
    settingsTrimSilence: "Quitar pausas largas",
    settingsTrimSilenceLead:
      "Omite los silencios largos entre segmentos cuando hay un mapa de silencios disponible. La velocidad inteligente está prevista aparte.",
    settingsPrePlay: "Reproducir antes intros y avances",
    settingsPrePlayLead:
      "Reproduce los clips de introducción del servidor y luego los avances que ofrezca una extensión, antes de que empiece un título.",
    settingsPrePlayExtrasLabel: "Avances antes de un título",
    settingsSkipIntervalLabel: "Botones de salto",
    settingsStillWatchingLabel: "Preguntar si alguien sigue viendo",
    settingsStillWatchingLead:
      "Tras esta cantidad de elementos seguidos que empiezan solos, pausa y pregunta antes del siguiente.",
    settingsStillWatchingNever: "Nunca",
    settingsStillWatchingAfter:
      "{count, plural, one {Después de # elemento} other {Después de # elementos}}",
    settingsDownstreamAudioLabel: "Salida de audio",
    settingsDownstreamAudioLead:
      "Elige estéreo para los altavoces del televisor, o envolvente si tu navegador envía el audio a un receptor.",
    settingsDownstreamAudioStereo: "Estéreo",
    settingsDownstreamAudioSurround: "Envolvente",
    settingsAccessibilityHeading: "Accesibilidad",
    settingsReduceMotion: "Reducir movimiento",
    settingsReduceMotionLead: "Minimiza las animaciones no esenciales en toda la aplicación.",
    settingsRecommendHeading: "Recomendaciones",
    settingsRecommendOptIn: "Recomendaciones personalizadas",
    settingsRecommendOptInLead:
      "Permite que la búsqueda en modo recomendación use la actividad de este perfil.",
    settingsCreditsHeading: "Créditos",
    settingsCreditsLead: "Los metadatos de este servidor provienen de estas fuentes.",
    settingsManagedNote: "Algunos ajustes los gestiona tu operador y no se muestran aquí.",
    settingsSaveFailed: "No se pudo guardar esta preferencia.",
    settingsUnavailable: "Las preferencias no están disponibles ahora mismo.",
    searchModeLabel: "Modo de búsqueda",
    searchModeKeyword: "Palabras clave",
    searchModeSemantic: "Semántica",
    searchModeRecommend: "Recomendar",
    searchModeFellBackToKeyword:
      "La búsqueda semántica no está disponible. Se muestran resultados por palabras clave.",
    searchRestrictedNotice: "Es posible que tus ajustes de acceso oculten algunos resultados.",
  },
  pt: {
    settingsTitle: "Configurações do perfil",
    settingsLead:
      "Preferências deste perfil neste dispositivo. A política do operador é gerenciada no Console.",
    settingsPlaybackHeading: "Reprodução",
    settingsAutoSkipHeading: "Pular automaticamente",
    settingsAutoSkipLead:
      "Pular esses trechos automaticamente em vez de mostrar um botão de pular.",
    settingsAutoSkipIntro: "Pular aberturas",
    settingsAutoSkipRecap: "Pular recapitulações",
    settingsAutoSkipCredits: "Pular créditos",
    settingsPlaybackSpeedLabel: "Velocidade de reprodução padrão",
    settingsTrimSilence: "Remover pausas longas",
    settingsTrimSilenceLead:
      "Pula longos intervalos de silêncio entre trechos quando há um mapa de silêncio disponível. A velocidade inteligente está planejada à parte.",
    settingsPrePlay: "Reproduzir aberturas e prévias primeiro",
    settingsPrePlayLead:
      "Reproduz os clipes de abertura do servidor e depois as prévias oferecidas por uma extensão, antes de um título começar.",
    settingsPrePlayExtrasLabel: "Prévias antes de um título",
    settingsSkipIntervalLabel: "Botões de pular",
    settingsStillWatchingLabel: "Perguntar se alguém ainda está assistindo",
    settingsStillWatchingLead:
      "Depois desta quantidade de itens seguidos que começam sozinhos, pausar e perguntar antes do próximo.",
    settingsStillWatchingNever: "Nunca",
    settingsStillWatchingAfter: "{count, plural, one {Após # item} other {Após # itens}}",
    settingsDownstreamAudioLabel: "Saída de áudio",
    settingsDownstreamAudioLead:
      "Escolha estéreo para os alto-falantes da TV, ou surround se seu navegador enviar o áudio para um receiver.",
    settingsDownstreamAudioStereo: "Estéreo",
    settingsDownstreamAudioSurround: "Surround",
    settingsAccessibilityHeading: "Acessibilidade",
    settingsReduceMotion: "Reduzir movimento",
    settingsReduceMotionLead: "Minimiza as animações não essenciais em todo o app.",
    settingsRecommendHeading: "Recomendações",
    settingsRecommendOptIn: "Recomendações personalizadas",
    settingsRecommendOptInLead:
      "Permite que a busca no modo recomendação use a atividade deste perfil.",
    settingsCreditsHeading: "Créditos",
    settingsCreditsLead: "Os metadados deste servidor vêm destas fontes.",
    settingsManagedNote:
      "Algumas configurações são gerenciadas pelo seu operador e não aparecem aqui.",
    settingsSaveFailed: "Não foi possível salvar esta preferência.",
    settingsUnavailable: "As preferências não estão disponíveis agora.",
    searchModeLabel: "Modo de busca",
    searchModeKeyword: "Palavra-chave",
    searchModeSemantic: "Semântica",
    searchModeRecommend: "Recomendar",
    searchModeFellBackToKeyword:
      "A busca semântica não está disponível. Mostrando resultados por palavra-chave.",
    searchRestrictedNotice:
      "Alguns resultados podem estar ocultos pelas suas configurações de acesso.",
  },
} as const;

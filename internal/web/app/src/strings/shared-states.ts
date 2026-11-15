export const sharedStateStrings = {
  en: {
    shellErrorTitle: "Something went wrong.",
    shellErrorLead: "This part of Beamfall could not be loaded. Reloading usually fixes it.",
    reloadPage: "Reload",
    enrollConnected: "Connected. Loading Beamfall…",
    enrollHandshakeIncomplete:
      "Connected, but the server handshake did not complete. Beamfall will retry it after reloading.",
    adminUnlockHandshakeIncomplete:
      "Unlocked, but the server handshake did not complete. Reload the page if Console requests fail.",
    remove: "Remove",
    couldNotRemoveSubscription: "Could not remove the subscription.",
    pickLibraryFeedRoot: "Pick a library and enter a feed URL.",
    couldNotAddSubscription: "Could not add the subscription.",
    couldNotUpdateRetention: "Could not update subscription retention.",
    viewerCodePrefix: "Viewer code: ",
    managerCodePrefix: "Manager code: ",
    codeExpiresPrefix: " (expires ",
    codeExpiresSuffix: ")",
    couldNotMintPairingCode: "Could not mint a pairing code.",
    viewerPairingCodesDisabled: "Disable Viewer pairing codes",
    viewerPairingCodesDisabledHelp:
      "New Viewer codes cannot be created and unused ones stop working. Manager pairing and demo enrollment stay enabled.",
    couldNotReachServer: "Could not reach the server.",
    requestFailed: "Request failed.",
    pairingCodeInvalid: "That pairing code is not valid.",
    profileUnavailable: "Profiles are unavailable right now.",
    managed: "Managed",
    tooManyAttempts: "Too many attempts. Try again in a few minutes.",
    pinIncorrect: "That PIN is not right.",
    playbackUnavailable: "This can't play right now. Try again in a moment.",
    playbackMediaFileMissing:
      "The file for this item is missing on the server. It may have been moved, or its drive may be offline. Ask the person who runs the server to check it.",
    playbackMediaNotAvailable:
      "This item is no longer available to this profile. Go back to Browse to pick something else.",
    playerTerminalUnavailable: "This can no longer play here. Go back to Browse.",
    transcodedPlaybackUnsupported:
      "This browser can't play this video. Try another browser or device.",
    playbackUnsupportedCodec:
      "This browser can't play this file's format. Try another browser, or ask the person who runs the server to turn on converting for this profile.",
    playbackFailed: "Playback failed. Please try again.",
    couldNotSwitchProfile: "Could not switch the profile.",
    couldNotCreateLibrary: "Could not create the library.",
    couldNotUpdateLibrary: "Could not update the library.",
    subscriptionsUnavailable: "Subscriptions are unavailable right now.",
    libraryEmptyTitle: "Nothing to show in this library yet.",
    libraryEmptyLead: "This library has no visible content for the current profile and filters.",
    // Empty-as-activation (onboarding-funnel §6) — owner/manager only. Non-owner
    // and no-access empties keep the inert copy above, byte-for-byte.
    libraryEmptyOwnerTitle: "Your library is ready. Let's fill it.",
    libraryEmptyOwnerLead:
      "Start a scan to bring in your media, or migrate it from another server.",
    libraryEmptyStartScan: "Start first scan",
    libraryEmptyMigrate: "Migrate a library",
    connectionLost: "Connection lost. Showing the last rendered view until the server responds.",
  },
  fr: {
    shellErrorTitle: "Un problème est survenu.",
    shellErrorLead:
      "Cette partie de Beamfall n’a pas pu être chargée. Recharger la page règle généralement le problème.",
    reloadPage: "Recharger",
    enrollConnected: "Connecté. Chargement de Beamfall…",
    enrollHandshakeIncomplete:
      "Connecté, mais la négociation avec le serveur n’a pas abouti. Beamfall réessaiera après le rechargement.",
    adminUnlockHandshakeIncomplete:
      "Déverrouillé, mais la négociation avec le serveur n’a pas abouti. Rechargez la page si les requêtes de la console échouent.",
    remove: "Supprimer",
    couldNotRemoveSubscription: "Impossible de supprimer l’abonnement.",
    pickLibraryFeedRoot: "Choisissez une bibliothèque et entrez une URL de flux.",
    couldNotAddSubscription: "Impossible d’ajouter l’abonnement.",
    couldNotUpdateRetention: "Impossible de mettre à jour la rétention de l’abonnement.",
    viewerCodePrefix: "Code de visionneuse : ",
    managerCodePrefix: "Code gestionnaire : ",
    codeExpiresPrefix: " (expire ",
    codeExpiresSuffix: ")",
    couldNotMintPairingCode: "Impossible de créer un code de jumelage.",
    viewerPairingCodesDisabled: "Désactiver les codes de jumelage visionneuse",
    viewerPairingCodesDisabledHelp:
      "Aucun nouveau code de visionneuse ne peut être créé et les codes inutilisés cessent de fonctionner. Le jumelage gestionnaire et l’inscription de démonstration restent activés.",
    couldNotReachServer: "Impossible de joindre le serveur.",
    requestFailed: "La requête a échoué.",
    pairingCodeInvalid: "Ce code de jumelage n’est pas valide.",
    profileUnavailable: "Les profils sont indisponibles pour le moment.",
    managed: "Géré",
    tooManyAttempts: "Trop de tentatives. Réessayez dans quelques minutes.",
    pinIncorrect: "Ce PIN n’est pas correct.",
    playbackUnavailable: "Impossible de lire ceci pour le moment. Réessayez dans un instant.",
    playbackMediaFileMissing:
      "Le fichier de cet élément est introuvable sur le serveur. Il a peut-être été déplacé, ou son disque est hors ligne. Demandez à la personne qui gère le serveur de vérifier.",
    playbackMediaNotAvailable:
      "Cet élément n’est plus disponible pour ce profil. Revenez en arrière pour choisir autre chose.",
    playerTerminalUnavailable: "Ceci ne peut plus être lu ici. Revenez en arrière.",
    transcodedPlaybackUnsupported:
      "Ce navigateur ne peut pas lire cette vidéo. Essayez un autre navigateur ou appareil.",
    playbackUnsupportedCodec:
      "Ce navigateur ne peut pas lire le format de ce fichier. Essayez un autre navigateur, ou demandez à la personne qui gère le serveur d’activer la conversion pour ce profil.",
    playbackFailed: "La lecture a échoué. Veuillez réessayer.",
    couldNotSwitchProfile: "Impossible de changer de profil.",
    couldNotCreateLibrary: "Impossible de créer la bibliothèque.",
    couldNotUpdateLibrary: "Impossible de mettre à jour la bibliothèque.",
    subscriptionsUnavailable: "Les abonnements sont indisponibles pour le moment.",
    libraryEmptyTitle: "Rien à afficher dans cette bibliothèque pour le moment.",
    libraryEmptyLead:
      "Cette bibliothèque n’a aucun contenu visible pour le profil et les filtres actuels.",
    libraryEmptyOwnerTitle: "Votre bibliothèque est prête. Remplissons-la.",
    libraryEmptyOwnerLead:
      "Lancez une analyse pour importer vos médias, ou migrez-les depuis un autre serveur.",
    libraryEmptyStartScan: "Lancer la première analyse",
    libraryEmptyMigrate: "Migrer une bibliothèque",
    connectionLost:
      "Connexion perdue. Affichage de la dernière vue rendue jusqu’à ce que le serveur réponde.",
  },
  de: {
    shellErrorTitle: "Etwas ist schiefgelaufen.",
    shellErrorLead:
      "Dieser Teil von Beamfall konnte nicht geladen werden. Ein Neuladen behebt das meist.",
    reloadPage: "Neu laden",
    enrollConnected: "Verbunden. Beamfall wird geladen…",
    enrollHandshakeIncomplete:
      "Verbunden, aber der Server-Handshake wurde nicht abgeschlossen. Beamfall versucht es nach dem Neuladen erneut.",
    adminUnlockHandshakeIncomplete:
      "Entsperrt, aber der Server-Handshake wurde nicht abgeschlossen. Lade die Seite neu, falls Konsolenanfragen fehlschlagen.",
    remove: "Entfernen",
    couldNotRemoveSubscription: "Das Abonnement konnte nicht entfernt werden.",
    pickLibraryFeedRoot: "Wähle eine Bibliothek aus und gib eine Feed-URL ein.",
    couldNotAddSubscription: "Das Abonnement konnte nicht hinzugefügt werden.",
    couldNotUpdateRetention: "Die Aufbewahrung des Abonnements konnte nicht aktualisiert werden.",
    viewerCodePrefix: "Viewer-Code: ",
    managerCodePrefix: "Manager-Code: ",
    codeExpiresPrefix: " (läuft ab ",
    codeExpiresSuffix: ")",
    couldNotMintPairingCode: "Es konnte kein Kopplungscode erstellt werden.",
    viewerPairingCodesDisabled: "Viewer-Kopplungscodes deaktivieren",
    viewerPairingCodesDisabledHelp:
      "Neue Viewer-Codes können nicht erstellt werden, und unbenutzte Codes funktionieren nicht mehr. Manager-Kopplung und Demo-Registrierung bleiben aktiviert.",
    couldNotReachServer: "Der Server konnte nicht erreicht werden.",
    requestFailed: "Anfrage fehlgeschlagen.",
    pairingCodeInvalid: "Dieser Kopplungscode ist nicht gültig.",
    profileUnavailable: "Profile sind derzeit nicht verfügbar.",
    managed: "Verwaltet",
    tooManyAttempts: "Zu viele Versuche. Versuche es in einigen Minuten erneut.",
    pinIncorrect: "Diese PIN ist nicht richtig.",
    playbackUnavailable:
      "Das kann gerade nicht abgespielt werden. Versuchen Sie es gleich noch einmal.",
    playbackMediaFileMissing:
      "Die Datei für dieses Element fehlt auf dem Server. Sie wurde vielleicht verschoben, oder ihr Laufwerk ist offline. Bitten Sie die Person, die den Server betreibt, das zu prüfen.",
    playbackMediaNotAvailable:
      "Dieses Element ist für dieses Profil nicht mehr verfügbar. Gehen Sie zurück zu Durchsuchen und wählen Sie etwas anderes.",
    playerTerminalUnavailable:
      "Das kann hier nicht mehr abgespielt werden. Gehen Sie zurück zu Durchsuchen.",
    transcodedPlaybackUnsupported:
      "Dieser Browser kann dieses Video nicht abspielen. Versuchen Sie einen anderen Browser oder ein anderes Gerät.",
    playbackUnsupportedCodec:
      "Dieser Browser kann das Format dieser Datei nicht abspielen. Versuchen Sie einen anderen Browser, oder bitten Sie die Person, die den Server betreibt, die Konvertierung für dieses Profil einzuschalten.",
    playbackFailed: "Wiedergabe fehlgeschlagen. Bitte erneut versuchen.",
    couldNotSwitchProfile: "Das Profil konnte nicht gewechselt werden.",
    couldNotCreateLibrary: "Die Bibliothek konnte nicht erstellt werden.",
    couldNotUpdateLibrary: "Die Bibliothek konnte nicht aktualisiert werden.",
    subscriptionsUnavailable: "Abonnements sind derzeit nicht verfügbar.",
    libraryEmptyTitle: "In dieser Bibliothek gibt es noch nichts anzuzeigen.",
    libraryEmptyLead:
      "Diese Bibliothek hat für das aktuelle Profil und die aktuellen Filter keine sichtbaren Inhalte.",
    libraryEmptyOwnerTitle: "Deine Bibliothek ist bereit. Füllen wir sie.",
    libraryEmptyOwnerLead:
      "Starte einen Scan, um deine Medien zu importieren, oder migriere sie von einem anderen Server.",
    libraryEmptyStartScan: "Ersten Scan starten",
    libraryEmptyMigrate: "Bibliothek migrieren",
    connectionLost:
      "Verbindung verloren. Die zuletzt gerenderte Ansicht wird angezeigt, bis der Server antwortet.",
  },
  es: {
    shellErrorTitle: "Algo salió mal.",
    shellErrorLead: "Esta parte de Beamfall no se pudo cargar. Recargar suele solucionarlo.",
    reloadPage: "Recargar",
    enrollConnected: "Conectado. Cargando Beamfall…",
    enrollHandshakeIncomplete:
      "Conectado, pero la negociación con el servidor no se completó. Beamfall lo reintentará al recargar.",
    adminUnlockHandshakeIncomplete:
      "Desbloqueado, pero la negociación con el servidor no se completó. Recarga la página si fallan las solicitudes de la consola.",
    remove: "Eliminar",
    couldNotRemoveSubscription: "No se pudo eliminar la suscripción.",
    pickLibraryFeedRoot: "Elige una biblioteca e introduce una URL de feed.",
    couldNotAddSubscription: "No se pudo agregar la suscripción.",
    couldNotUpdateRetention: "No se pudo actualizar la retención de la suscripción.",
    viewerCodePrefix: "Código de visor: ",
    managerCodePrefix: "Código de administrador: ",
    codeExpiresPrefix: " (vence ",
    codeExpiresSuffix: ")",
    couldNotMintPairingCode: "No se pudo crear un código de emparejamiento.",
    viewerPairingCodesDisabled: "Desactivar los códigos de emparejamiento de visor",
    viewerPairingCodesDisabledHelp:
      "No se pueden crear nuevos códigos de visor y los que no se han usado dejan de funcionar. El emparejamiento de administrador y la inscripción de demostración siguen activos.",
    couldNotReachServer: "No se pudo contactar con el servidor.",
    requestFailed: "Error en la solicitud.",
    pairingCodeInvalid: "Ese código de emparejamiento no es válido.",
    profileUnavailable: "Los perfiles no están disponibles en este momento.",
    managed: "Gestionado",
    tooManyAttempts: "Demasiados intentos. Inténtalo de nuevo en unos minutos.",
    pinIncorrect: "Ese PIN no es correcto.",
    playbackUnavailable: "No se puede reproducir ahora mismo. Inténtalo de nuevo en un momento.",
    playbackMediaFileMissing:
      "Falta el archivo de este elemento en el servidor. Puede que se haya movido o que su disco esté desconectado. Pide a la persona que administra el servidor que lo revise.",
    playbackMediaNotAvailable:
      "Este elemento ya no está disponible para este perfil. Vuelve a Explorar para elegir otra cosa.",
    playerTerminalUnavailable: "Esto ya no se puede reproducir aquí. Vuelve a Explorar.",
    transcodedPlaybackUnsupported:
      "Este navegador no puede reproducir este video. Prueba con otro navegador o dispositivo.",
    playbackUnsupportedCodec:
      "Este navegador no puede reproducir el formato de este archivo. Prueba con otro navegador, o pide a la persona que administra el servidor que active la conversión para este perfil.",
    playbackFailed: "Error de reproducción. Inténtalo de nuevo.",
    couldNotSwitchProfile: "No se pudo cambiar el perfil.",
    couldNotCreateLibrary: "No se pudo crear la biblioteca.",
    couldNotUpdateLibrary: "No se pudo actualizar la biblioteca.",
    subscriptionsUnavailable: "Las suscripciones no están disponibles en este momento.",
    libraryEmptyTitle: "Aún no hay nada que mostrar en esta biblioteca.",
    libraryEmptyLead:
      "Esta biblioteca no tiene contenido visible para el perfil y los filtros actuales.",
    libraryEmptyOwnerTitle: "Tu biblioteca está lista. Vamos a llenarla.",
    libraryEmptyOwnerLead: "Inicia un análisis para traer tus medios, o migra desde otro servidor.",
    libraryEmptyStartScan: "Iniciar el primer análisis",
    libraryEmptyMigrate: "Migrar una biblioteca",
    connectionLost:
      "Se perdió la conexión. Se muestra la última vista renderizada hasta que el servidor responda.",
  },
  pt: {
    shellErrorTitle: "Algo deu errado.",
    shellErrorLead:
      "Esta parte do Beamfall não pôde ser carregada. Recarregar normalmente resolve.",
    reloadPage: "Recarregar",
    enrollConnected: "Conectado. Carregando o Beamfall…",
    enrollHandshakeIncomplete:
      "Conectado, mas a negociação com o servidor não foi concluída. O Beamfall tentará novamente após recarregar.",
    adminUnlockHandshakeIncomplete:
      "Desbloqueado, mas a negociação com o servidor não foi concluída. Recarregue a página se as solicitações do console falharem.",
    remove: "Remover",
    couldNotRemoveSubscription: "Não foi possível remover a assinatura.",
    pickLibraryFeedRoot: "Escolha uma biblioteca e insira uma URL de feed.",
    couldNotAddSubscription: "Não foi possível adicionar a assinatura.",
    couldNotUpdateRetention: "Não foi possível atualizar a retenção da assinatura.",
    viewerCodePrefix: "Código de visualizador: ",
    managerCodePrefix: "Código de gerente: ",
    codeExpiresPrefix: " (expira ",
    codeExpiresSuffix: ")",
    couldNotMintPairingCode: "Não foi possível criar um código de pareamento.",
    viewerPairingCodesDisabled: "Desativar códigos de pareamento de visualizador",
    viewerPairingCodesDisabledHelp:
      "Novos códigos de visualizador não podem ser criados e os não usados deixam de funcionar. O pareamento de gerente e o cadastro de demonstração continuam ativos.",
    couldNotReachServer: "Não foi possível acessar o servidor.",
    requestFailed: "Falha na solicitação.",
    pairingCodeInvalid: "Esse código de pareamento não é válido.",
    profileUnavailable: "Os perfis não estão disponíveis no momento.",
    managed: "Gerenciado",
    tooManyAttempts: "Muitas tentativas. Tente novamente em alguns minutos.",
    pinIncorrect: "Esse PIN não está correto.",
    playbackUnavailable: "Não é possível reproduzir agora. Tente novamente em instantes.",
    playbackMediaFileMissing:
      "O arquivo deste item não está no servidor. Ele pode ter sido movido, ou o disco pode estar offline. Peça à pessoa que administra o servidor para verificar.",
    playbackMediaNotAvailable:
      "Este item não está mais disponível para este perfil. Volte para Navegar e escolha outra coisa.",
    playerTerminalUnavailable: "Isto não pode mais ser reproduzido aqui. Volte para Navegar.",
    transcodedPlaybackUnsupported:
      "Este navegador não consegue reproduzir este vídeo. Tente outro navegador ou dispositivo.",
    playbackUnsupportedCodec:
      "Este navegador não consegue reproduzir o formato deste arquivo. Tente outro navegador, ou peça à pessoa que administra o servidor para ativar a conversão para este perfil.",
    playbackFailed: "Falha na reprodução. Tente novamente.",
    couldNotSwitchProfile: "Não foi possível trocar o perfil.",
    couldNotCreateLibrary: "Não foi possível criar a biblioteca.",
    couldNotUpdateLibrary: "Não foi possível atualizar a biblioteca.",
    subscriptionsUnavailable: "As assinaturas não estão disponíveis no momento.",
    libraryEmptyTitle: "Ainda não há nada para mostrar nesta biblioteca.",
    libraryEmptyLead: "Esta biblioteca não tem conteúdo visível para o perfil e os filtros atuais.",
    libraryEmptyOwnerTitle: "Sua biblioteca está pronta. Vamos preenchê-la.",
    libraryEmptyOwnerLead:
      "Inicie uma varredura para trazer suas mídias ou migre-as de outro servidor.",
    libraryEmptyStartScan: "Iniciar a primeira varredura",
    libraryEmptyMigrate: "Migrar uma biblioteca",
    connectionLost:
      "Conexão perdida. Mostrando a última visualização renderizada até que o servidor responda.",
  },
} as const;

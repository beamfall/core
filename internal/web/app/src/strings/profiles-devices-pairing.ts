export const profileDevicePairingStrings = {
  en: {
    deviceActive: "Active",
    deviceRevoked: "Revoked",
    deviceSignedOut: "Signed out",
    deviceRevoke: "Revoke",
    deviceRevokeConfirm:
      "Revoke this device? It signs that browser out and invalidates its sessions and stream tokens.",
    deviceRevokeAll: "Revoke all others",
    deviceRevokeAllConfirm:
      "Revoke every other device? This signs out all browsers and devices except this one and invalidates their sessions and stream tokens. This one stays signed in.",
    deviceAddHeading: "Add a device",
    deviceAddLead:
      "Mint a one-time pairing code, then enter it on the new browser or device to enroll it.",
    deviceMintViewer: "Viewer code",
    deviceMintManager: "Manager code",
    deviceMintManagerConfirm:
      "Mint a manager pairing code? Whoever redeems it can manage this server, not just watch. Share it carefully.",
    deviceMintHint: "Each code works once and expires 10 minutes after it's made.",
    // The minted-code panel (CORE-1607).
    deviceCodeShownFor: "{role} pairing code. Copy it now, then enter it on the new device.",
    deviceCodeExpiresIn: "Expires in {time}",
    deviceCodeExpired: "This code has expired. Make a new one below.",
    deviceCodeOpenAt: "On the new device, open:",
    deviceCodeCopyLink: "Copy link",
    deviceCodeLinkField: "Link with the pairing code",
    deviceCodeLinkCopyHint:
      "This browser can't copy here. The link is selected: press Ctrl+C or ⌘C, or copy it by hand.",
    // The "Add a device" QR (CORE-1614).
    deviceQrScan: "Or scan this with the new device's camera:",
    deviceQrLabel: "QR code that opens {url}",
    deviceQrNeedsLan:
      "To show a QR code for a phone, open this Console at the server's network address instead of localhost.",
    remoteEnrollHeading: "Remote enrollment",
    remoteEnrollLead: "Control whether devices can enroll from outside your local network.",
    remoteEnrollToggle: "Allow remote enrollment",
    remoteEnrollToggleHint:
      "When off, only devices on your local network can redeem a pairing code. Remote enrollment over a relay is refused.",
    signinSecurityHeading: "Sign-in security",
    signinSecurityLead:
      "Choose whether the owner can sign a browser in with the admin password. Changing it needs the admin password.",
    signinSecuritySave: "Change sign-in security",
    signinSecuritySaved: "Sign-in security changed.",
    signinSecurityNotManager: "Only a Manager device can change sign-in security.",
    signinSecurityFailed: "Could not change sign-in security.",
    signinSecurityStrictConfirmTitle: "Switch to Strict?",
    signinSecurityStrictConfirmBody:
      "The admin password will no longer sign a browser in. If you are ever locked out, get a pairing code by running this on the server:",
    signinSecurityStrictConfirm: "Switch to Strict",
    deviceRevokedGroup: "{count, plural, one {# revoked device} other {# revoked devices}}",
    deviceIDDetail: "ID …{id}",
  },
  fr: {
    deviceActive: "Actif",
    deviceRevoked: "Révoqué",
    deviceSignedOut: "Déconnecté",
    deviceRevoke: "Révoquer",
    deviceRevokeConfirm:
      "Révoquer cet appareil ? Le navigateur est déconnecté, et ses sessions et jetons de diffusion sont invalidés.",
    deviceRevokeAll: "Révoquer tous les autres",
    deviceRevokeAllConfirm:
      "Révoquer tous les autres appareils ? Tous les navigateurs et appareils, sauf celui-ci, sont déconnectés et leurs sessions et jetons de diffusion sont invalidés. Celui-ci reste connecté.",
    deviceAddHeading: "Ajouter un appareil",
    deviceAddLead:
      "Générez un code de jumelage à usage unique, puis saisissez-le sur le nouveau navigateur ou appareil pour l’enregistrer.",
    deviceMintViewer: "Code visionneuse",
    deviceMintManager: "Code gestionnaire",
    deviceMintManagerConfirm:
      "Générer un code de jumelage gestionnaire ? La personne qui l’utilise peut gérer ce serveur, pas seulement regarder. Partagez-le avec prudence.",
    deviceMintHint: "Chaque code ne fonctionne qu’une fois et expire 10 minutes après sa création.",
    deviceCodeShownFor:
      "Code de jumelage {role}. Copiez-le maintenant, puis saisissez-le sur le nouvel appareil.",
    deviceCodeExpiresIn: "Expire dans {time}",
    deviceCodeExpired: "Ce code a expiré. Générez-en un nouveau ci-dessous.",
    deviceCodeOpenAt: "Sur le nouvel appareil, ouvrez :",
    deviceCodeCopyLink: "Copier le lien",
    deviceCodeLinkField: "Lien avec le code de jumelage",
    deviceCodeLinkCopyHint:
      "Ce navigateur ne peut pas copier ici. Le lien est sélectionné : appuyez sur Ctrl+C ou ⌘C, ou copiez-le à la main.",
    deviceQrScan: "Ou scannez ceci avec la caméra du nouvel appareil :",
    deviceQrLabel: "Code QR qui ouvre {url}",
    deviceQrNeedsLan:
      "Pour afficher un code QR pour un téléphone, ouvrez cette console à l’adresse réseau du serveur plutôt que sur localhost.",
    remoteEnrollHeading: "Enregistrement à distance",
    remoteEnrollLead:
      "Choisissez si des appareils peuvent s’enregistrer depuis l’extérieur de votre réseau local.",
    remoteEnrollToggle: "Autoriser l’enregistrement à distance",
    remoteEnrollToggleHint:
      "Désactivé, seuls les appareils de votre réseau local peuvent utiliser un code de jumelage. L’enregistrement à distance via un relais est refusé.",
    signinSecurityHeading: "Sécurité de connexion",
    signinSecurityLead:
      "Choisissez si le propriétaire peut connecter un navigateur avec le mot de passe administrateur. La modification demande le mot de passe administrateur.",
    signinSecuritySave: "Modifier la sécurité de connexion",
    signinSecuritySaved: "Sécurité de connexion modifiée.",
    signinSecurityNotManager:
      "Seul un appareil gestionnaire peut modifier la sécurité de connexion.",
    signinSecurityFailed: "Impossible de modifier la sécurité de connexion.",
    signinSecurityStrictConfirmTitle: "Passer en mode strict ?",
    signinSecurityStrictConfirmBody:
      "Le mot de passe administrateur ne connectera plus aucun navigateur. Si vous êtes un jour bloqué, obtenez un code de jumelage en exécutant ceci sur le serveur :",
    signinSecurityStrictConfirm: "Passer en mode strict",
    deviceRevokedGroup: "{count, plural, one {# appareil révoqué} other {# appareils révoqués}}",
    deviceIDDetail: "ID …{id}",
  },
  de: {
    deviceActive: "Aktiv",
    deviceRevoked: "Widerrufen",
    deviceSignedOut: "Abgemeldet",
    deviceRevoke: "Widerrufen",
    deviceRevokeConfirm:
      "Dieses Gerät widerrufen? Der Browser wird abgemeldet, und seine Sitzungen und Stream-Tokens werden ungültig.",
    deviceRevokeAll: "Alle anderen widerrufen",
    deviceRevokeAllConfirm:
      "Alle anderen Geräte widerrufen? Alle Browser und Geräte außer diesem werden abgemeldet, und ihre Sitzungen und Stream-Tokens werden ungültig. Dieses Gerät bleibt angemeldet.",
    deviceAddHeading: "Gerät hinzufügen",
    deviceAddLead:
      "Erzeuge einen einmaligen Kopplungscode und gib ihn im neuen Browser oder auf dem neuen Gerät ein, um es zu registrieren.",
    deviceMintViewer: "Viewer-Code",
    deviceMintManager: "Manager-Code",
    deviceMintManagerConfirm:
      "Einen Manager-Kopplungscode erzeugen? Wer ihn einlöst, kann diesen Server verwalten, nicht nur zuschauen. Gib ihn mit Bedacht weiter.",
    deviceMintHint:
      "Jeder Code funktioniert nur einmal und läuft 10 Minuten nach der Erstellung ab.",
    deviceCodeShownFor:
      "{role}-Kopplungscode. Kopiere ihn jetzt und gib ihn dann auf dem neuen Gerät ein.",
    deviceCodeExpiresIn: "Läuft ab in {time}",
    deviceCodeExpired: "Dieser Code ist abgelaufen. Erzeuge unten einen neuen.",
    deviceCodeOpenAt: "Öffne auf dem neuen Gerät:",
    deviceCodeCopyLink: "Link kopieren",
    deviceCodeLinkField: "Link mit dem Kopplungscode",
    deviceCodeLinkCopyHint:
      "Dieser Browser kann hier nicht kopieren. Der Link ist markiert: Drücke Strg+C oder ⌘C oder kopiere ihn von Hand.",
    deviceQrScan: "Oder scanne das mit der Kamera des neuen Geräts:",
    deviceQrLabel: "QR-Code, der {url} öffnet",
    deviceQrNeedsLan:
      "Um einen QR-Code für ein Smartphone anzuzeigen, öffne diese Console über die Netzwerkadresse des Servers statt über localhost.",
    remoteEnrollHeading: "Registrierung aus der Ferne",
    remoteEnrollLead:
      "Lege fest, ob sich Geräte von außerhalb deines lokalen Netzwerks registrieren dürfen.",
    remoteEnrollToggle: "Registrierung aus der Ferne erlauben",
    remoteEnrollToggleHint:
      "Wenn aus, können nur Geräte in deinem lokalen Netzwerk einen Kopplungscode einlösen. Die Registrierung aus der Ferne über ein Relay wird abgelehnt.",
    signinSecurityHeading: "Anmeldesicherheit",
    signinSecurityLead:
      "Lege fest, ob sich der Eigentümer mit dem Admin-Passwort in einem Browser anmelden darf. Für die Änderung brauchst du das Admin-Passwort.",
    signinSecuritySave: "Anmeldesicherheit ändern",
    signinSecuritySaved: "Anmeldesicherheit geändert.",
    signinSecurityNotManager: "Nur ein Manager-Gerät kann die Anmeldesicherheit ändern.",
    signinSecurityFailed: "Die Anmeldesicherheit konnte nicht geändert werden.",
    signinSecurityStrictConfirmTitle: "Auf Strikt umstellen?",
    signinSecurityStrictConfirmBody:
      "Das Admin-Passwort meldet dann keinen Browser mehr an. Falls du dich einmal aussperrst, hol dir einen Kopplungscode, indem du Folgendes auf dem Server ausführst:",
    signinSecurityStrictConfirm: "Auf Strikt umstellen",
    deviceRevokedGroup: "{count, plural, one {# widerrufenes Gerät} other {# widerrufene Geräte}}",
    deviceIDDetail: "ID …{id}",
  },
  es: {
    deviceActive: "Activo",
    deviceRevoked: "Revocado",
    deviceSignedOut: "Sesión cerrada",
    deviceRevoke: "Revocar",
    deviceRevokeConfirm:
      "¿Revocar este dispositivo? Se cierra la sesión de ese navegador y se invalidan sus sesiones y tokens de reproducción.",
    deviceRevokeAll: "Revocar todos los demás",
    deviceRevokeAllConfirm:
      "¿Revocar todos los demás dispositivos? Se cierra la sesión de todos los navegadores y dispositivos excepto este, y se invalidan sus sesiones y tokens de reproducción. Este sigue con la sesión iniciada.",
    deviceAddHeading: "Añadir un dispositivo",
    deviceAddLead:
      "Genera un código de emparejamiento de un solo uso y escríbelo en el nuevo navegador o dispositivo para registrarlo.",
    deviceMintViewer: "Código de visor",
    deviceMintManager: "Código de administrador",
    deviceMintManagerConfirm:
      "¿Generar un código de emparejamiento de administrador? Quien lo canjee podrá administrar este servidor, no solo ver contenido. Compártelo con cuidado.",
    deviceMintHint: "Cada código funciona una sola vez y caduca 10 minutos después de generarse.",
    deviceCodeShownFor:
      "Código de emparejamiento de {role}. Cópialo ahora y escríbelo en el nuevo dispositivo.",
    deviceCodeExpiresIn: "Caduca en {time}",
    deviceCodeExpired: "Este código ha caducado. Genera uno nuevo más abajo.",
    deviceCodeOpenAt: "En el nuevo dispositivo, abre:",
    deviceCodeCopyLink: "Copiar enlace",
    deviceCodeLinkField: "Enlace con el código de emparejamiento",
    deviceCodeLinkCopyHint:
      "Este navegador no puede copiar aquí. El enlace está seleccionado: pulsa Ctrl+C o ⌘C, o cópialo a mano.",
    deviceQrScan: "O escanea esto con la cámara del nuevo dispositivo:",
    deviceQrLabel: "Código QR que abre {url}",
    deviceQrNeedsLan:
      "Para mostrar un código QR para un teléfono, abre esta Console desde la dirección de red del servidor en lugar de localhost.",
    remoteEnrollHeading: "Registro remoto",
    remoteEnrollLead:
      "Controla si los dispositivos pueden registrarse desde fuera de tu red local.",
    remoteEnrollToggle: "Permitir el registro remoto",
    remoteEnrollToggleHint:
      "Si está desactivado, solo los dispositivos de tu red local pueden canjear un código de emparejamiento. Se rechaza el registro remoto a través de un relay.",
    signinSecurityHeading: "Seguridad del inicio de sesión",
    signinSecurityLead:
      "Elige si el propietario puede iniciar sesión en un navegador con la contraseña de administrador. Cambiarlo requiere la contraseña de administrador.",
    signinSecuritySave: "Cambiar la seguridad del inicio de sesión",
    signinSecuritySaved: "Seguridad del inicio de sesión cambiada.",
    signinSecurityNotManager:
      "Solo un dispositivo administrador puede cambiar la seguridad del inicio de sesión.",
    signinSecurityFailed: "No se pudo cambiar la seguridad del inicio de sesión.",
    signinSecurityStrictConfirmTitle: "¿Cambiar a Estricto?",
    signinSecurityStrictConfirmBody:
      "La contraseña de administrador ya no iniciará sesión en ningún navegador. Si alguna vez te quedas sin acceso, obtén un código de emparejamiento ejecutando esto en el servidor:",
    signinSecurityStrictConfirm: "Cambiar a Estricto",
    deviceRevokedGroup:
      "{count, plural, one {# dispositivo revocado} other {# dispositivos revocados}}",
    deviceIDDetail: "ID …{id}",
  },
  pt: {
    deviceActive: "Ativo",
    deviceRevoked: "Revogado",
    deviceSignedOut: "Desconectado",
    deviceRevoke: "Revogar",
    deviceRevokeConfirm:
      "Revogar este dispositivo? O navegador é desconectado e suas sessões e tokens de reprodução são invalidados.",
    deviceRevokeAll: "Revogar todos os outros",
    deviceRevokeAllConfirm:
      "Revogar todos os outros dispositivos? Todos os navegadores e dispositivos, exceto este, são desconectados e suas sessões e tokens de reprodução são invalidados. Este continua conectado.",
    deviceAddHeading: "Adicionar um dispositivo",
    deviceAddLead:
      "Gere um código de pareamento de uso único e digite-o no novo navegador ou dispositivo para cadastrá-lo.",
    deviceMintViewer: "Código de visualizador",
    deviceMintManager: "Código de gerente",
    deviceMintManagerConfirm:
      "Gerar um código de pareamento de gerente? Quem usar esse código poderá gerenciar este servidor, não só assistir. Compartilhe com cuidado.",
    deviceMintHint: "Cada código funciona uma única vez e expira 10 minutos depois de gerado.",
    deviceCodeShownFor:
      "Código de pareamento de {role}. Copie-o agora e digite-o no novo dispositivo.",
    deviceCodeExpiresIn: "Expira em {time}",
    deviceCodeExpired: "Este código expirou. Gere um novo abaixo.",
    deviceCodeOpenAt: "No novo dispositivo, abra:",
    deviceCodeCopyLink: "Copiar link",
    deviceCodeLinkField: "Link com o código de pareamento",
    deviceCodeLinkCopyHint:
      "Este navegador não consegue copiar aqui. O link está selecionado: pressione Ctrl+C ou ⌘C, ou copie manualmente.",
    deviceQrScan: "Ou escaneie isto com a câmera do novo dispositivo:",
    deviceQrLabel: "Código QR que abre {url}",
    deviceQrNeedsLan:
      "Para mostrar um código QR para um celular, abra este Console pelo endereço de rede do servidor em vez de localhost.",
    remoteEnrollHeading: "Cadastro remoto",
    remoteEnrollLead: "Controle se dispositivos podem se cadastrar de fora da sua rede local.",
    remoteEnrollToggle: "Permitir cadastro remoto",
    remoteEnrollToggleHint:
      "Quando desativado, só dispositivos da sua rede local podem usar um código de pareamento. O cadastro remoto por um relay é recusado.",
    signinSecurityHeading: "Segurança de login",
    signinSecurityLead:
      "Escolha se o proprietário pode entrar em um navegador com a senha de administrador. Alterar isso exige a senha de administrador.",
    signinSecuritySave: "Alterar segurança de login",
    signinSecuritySaved: "Segurança de login alterada.",
    signinSecurityNotManager: "Só um dispositivo gerente pode alterar a segurança de login.",
    signinSecurityFailed: "Não foi possível alterar a segurança de login.",
    signinSecurityStrictConfirmTitle: "Mudar para Estrito?",
    signinSecurityStrictConfirmBody:
      "A senha de administrador não vai mais conectar nenhum navegador. Se você ficar sem acesso, obtenha um código de pareamento executando isto no servidor:",
    signinSecurityStrictConfirm: "Mudar para Estrito",
    deviceRevokedGroup:
      "{count, plural, one {# dispositivo revogado} other {# dispositivos revogados}}",
    deviceIDDetail: "ID …{id}",
  },
} as const;

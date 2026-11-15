export const setupStrings = {
  en: {
    // First-run setup wizard (WEB-1, ADR-0007 §2).
    setupBrandSuffix: "Server setup",
    setupUnlockedBanner: "Server claimed. You're the owner now.",
    setupRailAdminPassword: "Admin password",
    setupRailSecurity: "Sign-in security",
    setupRailLibrary: "First library",
    setupRailProfile: "Owner profile",
    setupRailPlugins: "Plugins",
    setupRailScan: "First scan",
    setupStepCount: "Step {n} of {total}",
    setupTokenTitle: "Unlock with the setup token",
    setupTokenLead:
      "Paste the one-time code from the server's startup output (for containers, docker logs <container>). It proves you can reach the server before anyone sets an admin password.",
    setupToken: "Setup token",
    setupTokenRequired: "Paste the setup token from the server's startup output to continue.",
    setupTokenInvalid:
      "That setup token didn't work. It may be mistyped or already used; restarting the server prints a new one in its startup output.",
    setupTokenLanLink: "On the same network as the server? Open {url} to skip the token.",
    setupTokenAgain:
      "The setup token is cleared after each attempt. Paste it again to finish claiming the server.",
    setupNeedsSecure:
      "Setup needs HTTPS or this server's local network. Open the setup link from a device on the same network.",
    setupAdminPasswordTitle: "Set the admin password",
    setupAdminPasswordLead:
      "It unlocks the Console and owner-only tools. Use something strong and keep it in a password manager.",
    setupAdminPassword: "Admin password",
    setupAdminPasswordConfirm: "Confirm password",
    setupAdminPasswordMismatch: "The passwords don't match. Type the same password in both fields.",
    setupAdminPasswordRequired: "Enter an admin password.",
    setupAdminPasswordHint: "At least 8 characters.",
    setupAdminPasswordTooShort: "Admin password must be at least 8 characters.",
    setupPasswordShow: "Show password",
    setupPasswordHide: "Hide password",
    setupSecurityTitle: "Choose how the owner signs in",
    setupSecurityLead: "You can change this later in the Console.",
    setupSecurityHome: "Home",
    setupSecurityHomeDetail: "Sign in as the owner from any browser with the admin password.",
    setupSecurityStrict: "Strict",
    setupSecurityStrictDetail:
      "Only paired devices can sign in. To recover, run beamfall pairing-code on the server.",
    setupSecurityRecommended: "Recommended",
  },
  fr: {
    setupBrandSuffix: "Configuration du serveur",
    setupUnlockedBanner: "Serveur réclamé. Vous en êtes le propriétaire.",
    setupRailAdminPassword: "Mot de passe admin",
    setupRailSecurity: "Sécurité de connexion",
    setupRailLibrary: "Première bibliothèque",
    setupRailProfile: "Profil propriétaire",
    setupRailPlugins: "Plugins",
    setupRailScan: "Premier scan",
    setupStepCount: "Étape {n} sur {total}",
    setupTokenTitle: "Déverrouiller avec le jeton de configuration",
    setupTokenLead:
      "Collez le code à usage unique affiché au démarrage du serveur (pour les conteneurs, docker logs <container>). Il prouve que vous pouvez joindre le serveur avant que quiconque définisse un mot de passe admin.",
    setupToken: "Jeton de configuration",
    setupTokenRequired:
      "Collez le jeton de configuration affiché au démarrage du serveur pour continuer.",
    setupTokenInvalid:
      "Ce jeton de configuration n’a pas fonctionné. Il est peut-être mal saisi ou déjà utilisé ; redémarrer le serveur en affiche un nouveau au démarrage.",
    setupTokenLanLink: "Sur le même réseau que le serveur ? Ouvrez {url} pour ignorer le jeton.",
    setupTokenAgain:
      "Le jeton de configuration est effacé après chaque essai. Collez-le à nouveau pour finir de réclamer le serveur.",
    setupNeedsSecure:
      "La configuration exige le HTTPS ou le réseau local de ce serveur. Ouvrez le lien de configuration depuis un appareil du même réseau.",
    setupAdminPasswordTitle: "Définir le mot de passe admin",
    setupAdminPasswordLead:
      "Il déverrouille la Console et les outils réservés au propriétaire. Choisissez-en un robuste et gardez-le dans un gestionnaire de mots de passe.",
    setupAdminPassword: "Mot de passe admin",
    setupAdminPasswordConfirm: "Confirmer le mot de passe",
    setupAdminPasswordMismatch:
      "Les mots de passe ne correspondent pas. Saisissez le même mot de passe dans les deux champs.",
    setupAdminPasswordRequired: "Saisissez un mot de passe admin.",
    setupAdminPasswordHint: "Au moins 8 caractères.",
    setupAdminPasswordTooShort: "Le mot de passe admin doit comporter au moins 8 caractères.",
    setupPasswordShow: "Afficher le mot de passe",
    setupPasswordHide: "Masquer le mot de passe",
    setupSecurityTitle: "Choisir comment le propriétaire se connecte",
    setupSecurityLead: "Vous pourrez le modifier plus tard dans la Console.",
    setupSecurityHome: "Maison",
    setupSecurityHomeDetail:
      "Connectez-vous en tant que propriétaire depuis n’importe quel navigateur avec le mot de passe admin.",
    setupSecurityStrict: "Strict",
    setupSecurityStrictDetail:
      "Seuls les appareils jumelés peuvent se connecter. Pour récupérer l’accès, exécutez beamfall pairing-code sur le serveur.",
    setupSecurityRecommended: "Recommandé",
  },
  de: {
    setupBrandSuffix: "Server-Einrichtung",
    setupUnlockedBanner: "Server übernommen. Du bist jetzt der Eigentümer.",
    setupRailAdminPassword: "Admin-Passwort",
    setupRailSecurity: "Anmeldesicherheit",
    setupRailLibrary: "Erste Bibliothek",
    setupRailProfile: "Eigentümerprofil",
    setupRailPlugins: "Plugins",
    setupRailScan: "Erster Scan",
    setupStepCount: "Schritt {n} von {total}",
    setupTokenTitle: "Mit dem Einrichtungs-Token entsperren",
    setupTokenLead:
      "Füge den einmaligen Code aus der Startausgabe des Servers ein (bei Containern docker logs <container>). Er beweist, dass du den Server erreichen kannst, bevor jemand ein Admin-Passwort festlegt.",
    setupToken: "Einrichtungs-Token",
    setupTokenRequired:
      "Füge zum Fortfahren das Einrichtungs-Token aus der Startausgabe des Servers ein.",
    setupTokenInvalid:
      "Dieses Einrichtungs-Token hat nicht funktioniert. Vielleicht ist es falsch getippt oder schon benutzt; beim Neustart gibt der Server in seiner Startausgabe ein neues aus.",
    setupTokenLanLink:
      "Im selben Netzwerk wie der Server? Öffne {url}, um das Token zu überspringen.",
    setupTokenAgain:
      "Das Einrichtungs-Token wird nach jedem Versuch gelöscht. Füge es erneut ein, um die Übernahme des Servers abzuschließen.",
    setupNeedsSecure:
      "Die Einrichtung braucht HTTPS oder das lokale Netzwerk dieses Servers. Öffne den Einrichtungslink auf einem Gerät im selben Netzwerk.",
    setupAdminPasswordTitle: "Admin-Passwort festlegen",
    setupAdminPasswordLead:
      "Es entsperrt die Console und Werkzeuge nur für den Eigentümer. Wähle ein starkes Passwort und bewahre es in einem Passwortmanager auf.",
    setupAdminPassword: "Admin-Passwort",
    setupAdminPasswordConfirm: "Passwort bestätigen",
    setupAdminPasswordMismatch:
      "Die Passwörter stimmen nicht überein. Gib in beiden Feldern dasselbe Passwort ein.",
    setupAdminPasswordRequired: "Gib ein Admin-Passwort ein.",
    setupAdminPasswordHint: "Mindestens 8 Zeichen.",
    setupAdminPasswordTooShort: "Das Admin-Passwort muss mindestens 8 Zeichen lang sein.",
    setupPasswordShow: "Passwort anzeigen",
    setupPasswordHide: "Passwort verbergen",
    setupSecurityTitle: "Wähle, wie sich der Eigentümer anmeldet",
    setupSecurityLead: "Du kannst das später in der Console ändern.",
    setupSecurityHome: "Zuhause",
    setupSecurityHomeDetail:
      "Melde dich mit dem Admin-Passwort von jedem Browser aus als Eigentümer an.",
    setupSecurityStrict: "Streng",
    setupSecurityStrictDetail:
      "Nur gekoppelte Geräte können sich anmelden. Zur Wiederherstellung führe beamfall pairing-code auf dem Server aus.",
    setupSecurityRecommended: "Empfohlen",
  },
  es: {
    setupBrandSuffix: "Configuración del servidor",
    setupUnlockedBanner: "Servidor reclamado. Ahora eres el propietario.",
    setupRailAdminPassword: "Contraseña de admin",
    setupRailSecurity: "Seguridad de acceso",
    setupRailLibrary: "Primera biblioteca",
    setupRailProfile: "Perfil del propietario",
    setupRailPlugins: "Plugins",
    setupRailScan: "Primer análisis",
    setupStepCount: "Paso {n} de {total}",
    setupTokenTitle: "Desbloquear con el token de configuración",
    setupTokenLead:
      "Pega el código de un solo uso que aparece al arrancar el servidor (en contenedores, docker logs <container>). Demuestra que puedes llegar al servidor antes de que alguien defina una contraseña de admin.",
    setupToken: "Token de configuración",
    setupTokenRequired:
      "Pega el token de configuración que aparece al arrancar el servidor para continuar.",
    setupTokenInvalid:
      "Ese token de configuración no funcionó. Puede que esté mal escrito o ya usado; al reiniciar el servidor se muestra uno nuevo al arrancar.",
    setupTokenLanLink: "¿Estás en la misma red que el servidor? Abre {url} para omitir el token.",
    setupTokenAgain:
      "El token de configuración se borra tras cada intento. Pégalo de nuevo para terminar de reclamar el servidor.",
    setupNeedsSecure:
      "La configuración necesita HTTPS o la red local de este servidor. Abre el enlace de configuración desde un dispositivo de la misma red.",
    setupAdminPasswordTitle: "Define la contraseña de admin",
    setupAdminPasswordLead:
      "Desbloquea la Console y las herramientas exclusivas del propietario. Usa una contraseña segura y guárdala en un gestor de contraseñas.",
    setupAdminPassword: "Contraseña de admin",
    setupAdminPasswordConfirm: "Confirmar contraseña",
    setupAdminPasswordMismatch:
      "Las contraseñas no coinciden. Escribe la misma contraseña en ambos campos.",
    setupAdminPasswordRequired: "Introduce una contraseña de admin.",
    setupAdminPasswordHint: "Al menos 8 caracteres.",
    setupAdminPasswordTooShort: "La contraseña de admin debe tener al menos 8 caracteres.",
    setupPasswordShow: "Mostrar contraseña",
    setupPasswordHide: "Ocultar contraseña",
    setupSecurityTitle: "Elige cómo inicia sesión el propietario",
    setupSecurityLead: "Puedes cambiarlo más tarde en la Console.",
    setupSecurityHome: "Casa",
    setupSecurityHomeDetail:
      "Inicia sesión como propietario desde cualquier navegador con la contraseña de admin.",
    setupSecurityStrict: "Estricto",
    setupSecurityStrictDetail:
      "Solo los dispositivos emparejados pueden iniciar sesión. Para recuperar el acceso, ejecuta beamfall pairing-code en el servidor.",
    setupSecurityRecommended: "Recomendado",
  },
  pt: {
    setupBrandSuffix: "Configuração do servidor",
    setupUnlockedBanner: "Servidor reivindicado. Agora você é o proprietário.",
    setupRailAdminPassword: "Senha de admin",
    setupRailSecurity: "Segurança de login",
    setupRailLibrary: "Primeira biblioteca",
    setupRailProfile: "Perfil do proprietário",
    setupRailPlugins: "Plugins",
    setupRailScan: "Primeira verificação",
    setupStepCount: "Etapa {n} de {total}",
    setupTokenTitle: "Desbloquear com o token de configuração",
    setupTokenLead:
      "Cole o código de uso único exibido na inicialização do servidor (em contêineres, docker logs <container>). Ele prova que você consegue acessar o servidor antes de alguém definir uma senha de admin.",
    setupToken: "Token de configuração",
    setupTokenRequired:
      "Cole o token de configuração exibido na inicialização do servidor para continuar.",
    setupTokenInvalid:
      "Esse token de configuração não funcionou. Ele pode estar digitado errado ou já ter sido usado; reiniciar o servidor mostra um novo na inicialização.",
    setupTokenLanLink: "Está na mesma rede do servidor? Abra {url} para pular o token.",
    setupTokenAgain:
      "O token de configuração é apagado a cada tentativa. Cole-o novamente para terminar de reivindicar o servidor.",
    setupNeedsSecure:
      "A configuração precisa de HTTPS ou da rede local deste servidor. Abra o link de configuração em um dispositivo da mesma rede.",
    setupAdminPasswordTitle: "Defina a senha de admin",
    setupAdminPasswordLead:
      "Ela desbloqueia o Console e as ferramentas exclusivas do proprietário. Use uma senha forte e guarde-a em um gerenciador de senhas.",
    setupAdminPassword: "Senha de admin",
    setupAdminPasswordConfirm: "Confirmar senha",
    setupAdminPasswordMismatch: "As senhas não coincidem. Digite a mesma senha nos dois campos.",
    setupAdminPasswordRequired: "Digite uma senha de admin.",
    setupAdminPasswordHint: "Pelo menos 8 caracteres.",
    setupAdminPasswordTooShort: "A senha de admin deve ter pelo menos 8 caracteres.",
    setupPasswordShow: "Mostrar senha",
    setupPasswordHide: "Ocultar senha",
    setupSecurityTitle: "Escolha como o proprietário entra",
    setupSecurityLead: "Você pode mudar isso depois no Console.",
    setupSecurityHome: "Casa",
    setupSecurityHomeDetail: "Entre como proprietário de qualquer navegador com a senha de admin.",
    setupSecurityStrict: "Rígido",
    setupSecurityStrictDetail:
      "Somente dispositivos pareados podem entrar. Para recuperar o acesso, execute beamfall pairing-code no servidor.",
    setupSecurityRecommended: "Recomendado",
  },
} as const;

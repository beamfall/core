export const consoleAccessLockStrings = {
  en: {
    // First admin password (CORE-1132): an owner who claimed the server without
    // choosing one sets it here and lands in the Console.
    consoleFirstPasswordTitle: "Set an admin password",
    consoleFirstPasswordLead:
      "No admin password has been set yet. Choose one to protect the Console and owner-only tools, and keep it in your password manager.",
    consoleFirstPasswordSubmit: "Set password and unlock",
    consoleFirstPasswordMismatch: "The passwords do not match.",
    consoleFirstPasswordRejected:
      "The admin password could not be set. Check that this is the owner’s profile on a manager device.",
    // Reason-coded Console locked states (ADR-0085 §30) + the Admin/Reveal
    // distinction (§32).
    consoleLockedNotAdultTitle: "Console unavailable",
    consoleLockedNotAdultLead:
      "This profile can’t administer the server. Switch to an adult owner profile to manage the Console.",
    consoleLockedNoSessionTitle: "Console locked",
    consoleLockedNoSessionLead: "Enter the server’s admin password to open the Console.",
    consoleLockedExpiredTitle: "Admin session expired",
    consoleLockedExpiredLead:
      "For safety, admin access ends after a while. Enter the admin password again to pick up where you left off.",
    consoleLockedSwitchedTitle: "Profile switched",
    consoleLockedSwitchedLead:
      "You switched profiles, so admin access closed. Enter the admin password again to continue.",
    adminRevealDistinction:
      "Admin access is separate from Reveal: unlocking here won’t show restricted content, and Reveal never unlocks admin tools.",
    apiKeysHeading: "API keys",
    apiKeysLead: "Create scoped, named keys for third-party tools. Revoke any time.",
    apiKeyNameLabel: "Key name",
    apiKeyScopesLegend: "Scopes",
    scopeReadCatalog: "Read catalog",
    scopeReadSessions: "Read sessions",
    scopeEventsSubscribe: "Subscribe to events",
    scopePlaybackControl: "Control playback",
    createAPIKey: "Create key",
    noAPIKeys: "No API keys.",
    apiKeysUnavailable: "API keys are unavailable right now.",
    couldNotCreateAPIKey: "Could not create the API key.",
    couldNotRevokeAPIKey: "Could not revoke the API key.",
    confirmRevokeAPIKey: "Revoke this API key?",
    apiKeyNeedsNameAndScope: "A name and at least one scope are required.",
    noScopes: "No scopes",
    activityLogHeading: "Activity",
    activityLogEmpty: "No activity yet.",
    activityLogUnavailable: "The activity log is unavailable right now.",
  },
  fr: {
    consoleFirstPasswordTitle: "Définir un mot de passe administrateur",
    consoleFirstPasswordLead:
      "Aucun mot de passe administrateur n’est défini. Choisissez-en un pour protéger la console et les outils réservés au propriétaire, et conservez-le dans votre gestionnaire de mots de passe.",
    consoleFirstPasswordSubmit: "Définir le mot de passe et déverrouiller",
    consoleFirstPasswordMismatch: "Les mots de passe ne correspondent pas.",
    consoleFirstPasswordRejected:
      "Le mot de passe administrateur n’a pas pu être défini. Vérifiez qu’il s’agit du profil du propriétaire sur un appareil gestionnaire.",
    consoleLockedNotAdultTitle: "Console indisponible",
    consoleLockedNotAdultLead:
      "Ce profil ne peut pas administrer le serveur. Passez à un profil propriétaire adulte pour gérer la console.",
    consoleLockedNoSessionTitle: "Console verrouillée",
    consoleLockedNoSessionLead:
      "Saisissez le mot de passe administrateur du serveur pour ouvrir la Console.",
    consoleLockedExpiredTitle: "Session administrateur expirée",
    consoleLockedExpiredLead:
      "Par sécurité, l’accès administrateur prend fin après un certain temps. Saisissez à nouveau le mot de passe administrateur pour reprendre là où vous en étiez.",
    consoleLockedSwitchedTitle: "Profil changé",
    consoleLockedSwitchedLead:
      "Vous avez changé de profil, l’accès administrateur a donc été fermé. Saisissez à nouveau le mot de passe administrateur pour continuer.",
    adminRevealDistinction:
      "L’accès administrateur est distinct de la Révélation : déverrouiller ici n’affiche pas le contenu restreint, et la Révélation ne déverrouille jamais les outils d’administration.",
    apiKeysHeading: "Clés d’API",
    apiKeysLead:
      "Créez des clés nommées et limitées pour des outils tiers. Révocables à tout moment.",
    apiKeyNameLabel: "Nom de la clé",
    apiKeyScopesLegend: "Portées",
    scopeReadCatalog: "Lire le catalogue",
    scopeReadSessions: "Lire les sessions",
    scopeEventsSubscribe: "S’abonner aux événements",
    scopePlaybackControl: "Contrôler la lecture",
    createAPIKey: "Créer une clé",
    noAPIKeys: "Aucune clé d’API.",
    apiKeysUnavailable: "Les clés d’API sont indisponibles pour le moment.",
    couldNotCreateAPIKey: "Impossible de créer la clé d’API.",
    couldNotRevokeAPIKey: "Impossible de révoquer la clé d’API.",
    confirmRevokeAPIKey: "Révoquer cette clé d’API ?",
    apiKeyNeedsNameAndScope: "Un nom et au moins une portée sont requis.",
    noScopes: "Aucune portée",
    activityLogHeading: "Activité",
    activityLogEmpty: "Aucune activité pour l’instant.",
    activityLogUnavailable: "Le journal d’activité est indisponible pour le moment.",
  },
  de: {
    consoleFirstPasswordTitle: "Admin-Passwort festlegen",
    consoleFirstPasswordLead:
      "Es wurde noch kein Admin-Passwort festgelegt. Wähle eines, um die Console und Tools nur für den Eigentümer zu schützen, und bewahre es in deinem Passwortmanager auf.",
    consoleFirstPasswordSubmit: "Passwort festlegen und entsperren",
    consoleFirstPasswordMismatch: "Die Passwörter stimmen nicht überein.",
    consoleFirstPasswordRejected:
      "Das Admin-Passwort konnte nicht festgelegt werden. Prüfe, ob das das Profil des Eigentümers auf einem Manager-Gerät ist.",
    consoleLockedNotAdultTitle: "Console nicht verfügbar",
    consoleLockedNotAdultLead:
      "Dieses Profil kann den Server nicht verwalten. Wechsle zu einem erwachsenen Eigentümerprofil, um die Console zu verwalten.",
    consoleLockedNoSessionTitle: "Console gesperrt",
    consoleLockedNoSessionLead: "Gib das Admin-Passwort des Servers ein, um die Konsole zu öffnen.",
    consoleLockedExpiredTitle: "Admin-Sitzung abgelaufen",
    consoleLockedExpiredLead:
      "Aus Sicherheitsgründen endet der Admin-Zugriff nach einer Weile. Gib das Admin-Passwort erneut ein, um dort weiterzumachen, wo du aufgehört hast.",
    consoleLockedSwitchedTitle: "Profil gewechselt",
    consoleLockedSwitchedLead:
      "Du hast das Profil gewechselt, deshalb wurde der Admin-Zugriff geschlossen. Gib das Admin-Passwort erneut ein, um fortzufahren.",
    adminRevealDistinction:
      "Der Admin-Zugriff ist von „Anzeigen“ getrennt: Das Entsperren hier zeigt keine eingeschränkten Inhalte, und „Anzeigen“ entsperrt nie die Admin-Werkzeuge.",
    apiKeysHeading: "API-Schlüssel",
    apiKeysLead:
      "Erstelle benannte, eingeschränkte Schlüssel für Drittanbieter-Tools. Jederzeit widerrufbar.",
    apiKeyNameLabel: "Schlüsselname",
    apiKeyScopesLegend: "Bereiche",
    scopeReadCatalog: "Katalog lesen",
    scopeReadSessions: "Sitzungen lesen",
    scopeEventsSubscribe: "Ereignisse abonnieren",
    scopePlaybackControl: "Wiedergabe steuern",
    createAPIKey: "Schlüssel erstellen",
    noAPIKeys: "Keine API-Schlüssel.",
    apiKeysUnavailable: "API-Schlüssel sind derzeit nicht verfügbar.",
    couldNotCreateAPIKey: "Der API-Schlüssel konnte nicht erstellt werden.",
    couldNotRevokeAPIKey: "Der API-Schlüssel konnte nicht widerrufen werden.",
    confirmRevokeAPIKey: "Diesen API-Schlüssel widerrufen?",
    apiKeyNeedsNameAndScope: "Ein Name und mindestens ein Bereich sind erforderlich.",
    noScopes: "Keine Bereiche",
    activityLogHeading: "Aktivität",
    activityLogEmpty: "Noch keine Aktivität.",
    activityLogUnavailable: "Das Aktivitätsprotokoll ist derzeit nicht verfügbar.",
  },
  es: {
    consoleFirstPasswordTitle: "Establece una contraseña de administrador",
    consoleFirstPasswordLead:
      "Todavía no se ha establecido una contraseña de administrador. Elige una para proteger la Console y las herramientas exclusivas del propietario, y guárdala en tu gestor de contraseñas.",
    consoleFirstPasswordSubmit: "Establecer contraseña y desbloquear",
    consoleFirstPasswordMismatch: "Las contraseñas no coinciden.",
    consoleFirstPasswordRejected:
      "No se pudo establecer la contraseña de administrador. Comprueba que este sea el perfil del propietario en un dispositivo administrador.",
    consoleLockedNotAdultTitle: "Console no disponible",
    consoleLockedNotAdultLead:
      "Este perfil no puede administrar el servidor. Cambia a un perfil de propietario adulto para gestionar la Console.",
    consoleLockedNoSessionTitle: "Console bloqueada",
    consoleLockedNoSessionLead:
      "Introduce la contraseña de administrador del servidor para abrir la Consola.",
    consoleLockedExpiredTitle: "Sesión de administrador caducada",
    consoleLockedExpiredLead:
      "Por seguridad, el acceso de administrador termina al cabo de un tiempo. Vuelve a introducir la contraseña de administrador para continuar donde lo dejaste.",
    consoleLockedSwitchedTitle: "Perfil cambiado",
    consoleLockedSwitchedLead:
      "Cambiaste de perfil, así que el acceso de administrador se cerró. Vuelve a introducir la contraseña de administrador para continuar.",
    adminRevealDistinction:
      "El acceso de administrador es independiente de Revelar: desbloquear aquí no muestra contenido restringido, y Revelar nunca desbloquea las herramientas de administrador.",
    apiKeysHeading: "Claves de API",
    apiKeysLead:
      "Crea claves con nombre y alcance para herramientas de terceros. Revócalas cuando quieras.",
    apiKeyNameLabel: "Nombre de la clave",
    apiKeyScopesLegend: "Alcances",
    scopeReadCatalog: "Leer el catálogo",
    scopeReadSessions: "Leer las sesiones",
    scopeEventsSubscribe: "Suscribirse a eventos",
    scopePlaybackControl: "Controlar la reproducción",
    createAPIKey: "Crear clave",
    noAPIKeys: "No hay claves de API.",
    apiKeysUnavailable: "Las claves de API no están disponibles en este momento.",
    couldNotCreateAPIKey: "No se pudo crear la clave de API.",
    couldNotRevokeAPIKey: "No se pudo revocar la clave de API.",
    confirmRevokeAPIKey: "¿Revocar esta clave de API?",
    apiKeyNeedsNameAndScope: "Se requieren un nombre y al menos un alcance.",
    noScopes: "Sin alcances",
    activityLogHeading: "Actividad",
    activityLogEmpty: "Aún no hay actividad.",
    activityLogUnavailable: "El registro de actividad no está disponible en este momento.",
  },
  pt: {
    consoleFirstPasswordTitle: "Defina uma senha de administrador",
    consoleFirstPasswordLead:
      "Nenhuma senha de administrador foi definida ainda. Escolha uma para proteger o Console e as ferramentas exclusivas do proprietário, e guarde-a no seu gerenciador de senhas.",
    consoleFirstPasswordSubmit: "Definir senha e desbloquear",
    consoleFirstPasswordMismatch: "As senhas não coincidem.",
    consoleFirstPasswordRejected:
      "Não foi possível definir a senha de administrador. Verifique se este é o perfil do proprietário em um dispositivo gerente.",
    consoleLockedNotAdultTitle: "Console indisponível",
    consoleLockedNotAdultLead:
      "Este perfil não pode administrar o servidor. Mude para um perfil de proprietário adulto para gerenciar o Console.",
    consoleLockedNoSessionTitle: "Console bloqueado",
    consoleLockedNoSessionLead: "Digite a senha de administrador do servidor para abrir o Console.",
    consoleLockedExpiredTitle: "Sessão de administrador expirada",
    consoleLockedExpiredLead:
      "Por segurança, o acesso de administrador termina depois de um tempo. Digite a senha de administrador novamente para continuar de onde parou.",
    consoleLockedSwitchedTitle: "Perfil alterado",
    consoleLockedSwitchedLead:
      "Você trocou de perfil, então o acesso de administrador foi encerrado. Digite a senha de administrador novamente para continuar.",
    adminRevealDistinction:
      "O acesso de administrador é separado de Revelar: desbloquear aqui não mostra conteúdo restrito, e Revelar nunca desbloqueia as ferramentas de administrador.",
    apiKeysHeading: "Chaves de API",
    apiKeysLead:
      "Crie chaves nomeadas e com escopo para ferramentas de terceiros. Revogue quando quiser.",
    apiKeyNameLabel: "Nome da chave",
    apiKeyScopesLegend: "Escopos",
    scopeReadCatalog: "Ler o catálogo",
    scopeReadSessions: "Ler as sessões",
    scopeEventsSubscribe: "Assinar eventos",
    scopePlaybackControl: "Controlar a reprodução",
    createAPIKey: "Criar chave",
    noAPIKeys: "Nenhuma chave de API.",
    apiKeysUnavailable: "As chaves de API estão indisponíveis no momento.",
    couldNotCreateAPIKey: "Não foi possível criar a chave de API.",
    couldNotRevokeAPIKey: "Não foi possível revogar a chave de API.",
    confirmRevokeAPIKey: "Revogar esta chave de API?",
    apiKeyNeedsNameAndScope: "Um nome e pelo menos um escopo são obrigatórios.",
    noScopes: "Sem escopos",
    activityLogHeading: "Atividade",
    activityLogEmpty: "Nenhuma atividade ainda.",
    activityLogUnavailable: "O registro de atividades está indisponível no momento.",
  },
} as const;

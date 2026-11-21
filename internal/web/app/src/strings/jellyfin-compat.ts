// Console Jellyfin-compatible API settings (ADR-0171, CORE-2683). The page is behind the App
// Store firewall, so these strings never show on an App Store host.
export const jellyfinCompatStrings = {
  en: {
    navJellyfinCompat: "Jellyfin compatibility",
    jellyfinCompatLead:
      "A Jellyfin-compatible API for automation tools such as Seerr, Radarr and Sonarr.",
    jellyfinCompatSetup:
      "In the tool, use this server's address followed by /compat/jellyfin/ as the Jellyfin server URL. During setup, sign in as admin with the admin password; after that the tool signs in as a profile you opt in below, with that profile's PIN. The API is off by default. When on, it answers only clients on the allowed networks, never through the relay or remote access.",
    jellyfinCompatDisclaimer:
      "Beamfall is not affiliated with or endorsed by Jellyfin. The name is used only to describe compatibility.",
    jellyfinCompatUnavailable: "Couldn't load the compatibility settings.",
    jellyfinCompatEnabled: "Compatibility API",
    jellyfinCompatEnabledHelp: "Changes apply right away, with no restart.",
    jellyfinCompatNetworksHeading: "Networks",
    jellyfinCompatAllowedCIDRs: "Allowed networks",
    jellyfinCompatAllowedCIDRsHelp:
      "One network per line, such as 192.168.1.0/24. Only loopback, private, link-local and IPv6 unique-local ranges are accepted.",
    jellyfinCompatProxyCIDRs: "Trusted proxy networks",
    jellyfinCompatProxyCIDRsHelp:
      "A trusted reverse proxy must overwrite X-Forwarded-For, not append to it, or clients can spoof their address.",
    jellyfinCompatSaveNetworks: "Save networks",
    jellyfinCompatSaved: "Saved.",
    jellyfinCompatSaveFailed: "Couldn't save: {reason}",
    jellyfinCompatLibrariesHeading: "Shared libraries",
    jellyfinCompatLibrariesHelp:
      "Tools see only the libraries ticked here. Restricted libraries are never listed.",
    jellyfinCompatLibrariesEmpty: "No libraries can be shared.",
    jellyfinCompatProfilesHeading: "Profiles that can sign in",
    jellyfinCompatProfilesHelp:
      "A tool signs in as one of these profiles. Turning a profile off signs it out of every compatibility tool at once.",
    jellyfinCompatProfilesEmpty: "No adult profiles.",
    jellyfinCompatKeysHeading: "Integration keys",
    jellyfinCompatKeysHelp:
      "A key lets a tool such as Seerr or Radarr sign in as a profile until you revoke it, with no PIN and no expiry. Paste it into the tool's API key field. A key can read and refresh libraries; it can never change settings.",
    jellyfinCompatKeysNoProfiles: "Turn on a profile above before you create a key.",
    jellyfinCompatKeyName: "Key name",
    jellyfinCompatKeyProfile: "Profile",
    jellyfinCompatKeyCreate: "Create key",
    jellyfinCompatKeysUnavailable: "Couldn't load the integration keys.",
    jellyfinCompatKeysEmpty: "No integration keys yet.",
    jellyfinCompatKeyCreateFailed: "Couldn't create the key: {reason}",
    jellyfinCompatKeyRevokeFailed: "Couldn't revoke the key: {reason}",
    jellyfinCompatKeyConfirmRevoke: "Revoke this key? The tool using it stops working at once.",
  },
  fr: {
    navJellyfinCompat: "Compatibilité Jellyfin",
    jellyfinCompatLead:
      "Une API compatible Jellyfin pour les outils d’automatisation comme Seerr, Radarr et Sonarr.",
    jellyfinCompatSetup:
      "Dans l’outil, indiquez comme URL du serveur Jellyfin l’adresse de ce serveur suivie de /compat/jellyfin/. Pendant la configuration, connectez-vous en tant qu’admin avec le mot de passe administrateur ; ensuite, l’outil se connecte avec un profil autorisé ci-dessous et le code PIN de ce profil. L’API est désactivée par défaut. Une fois activée, elle ne répond qu’aux clients des réseaux autorisés, jamais via le relais ni l’accès à distance.",
    jellyfinCompatDisclaimer:
      "Beamfall n’est ni affilié à Jellyfin ni approuvé par Jellyfin. Le nom sert uniquement à décrire la compatibilité.",
    jellyfinCompatUnavailable: "Impossible de charger les réglages de compatibilité.",
    jellyfinCompatEnabled: "API de compatibilité",
    jellyfinCompatEnabledHelp: "Les modifications s’appliquent immédiatement, sans redémarrage.",
    jellyfinCompatNetworksHeading: "Réseaux",
    jellyfinCompatAllowedCIDRs: "Réseaux autorisés",
    jellyfinCompatAllowedCIDRsHelp:
      "Un réseau par ligne, par exemple 192.168.1.0/24. Seules les plages de bouclage, privées, link-local et IPv6 unique-local sont acceptées.",
    jellyfinCompatProxyCIDRs: "Réseaux de proxy de confiance",
    jellyfinCompatProxyCIDRsHelp:
      "Un proxy inverse de confiance doit remplacer X-Forwarded-For, et non y ajouter une valeur, sinon les clients peuvent usurper leur adresse.",
    jellyfinCompatSaveNetworks: "Enregistrer les réseaux",
    jellyfinCompatSaved: "Enregistré.",
    jellyfinCompatSaveFailed: "Impossible d’enregistrer : {reason}",
    jellyfinCompatLibrariesHeading: "Bibliothèques partagées",
    jellyfinCompatLibrariesHelp:
      "Les outils ne voient que les bibliothèques cochées ici. Les bibliothèques restreintes ne sont jamais listées.",
    jellyfinCompatLibrariesEmpty: "Aucune bibliothèque ne peut être partagée.",
    jellyfinCompatProfilesHeading: "Profils autorisés à se connecter",
    jellyfinCompatProfilesHelp:
      "Un outil se connecte avec l’un de ces profils. Désactiver un profil le déconnecte de tous les outils de compatibilité à la fois.",
    jellyfinCompatProfilesEmpty: "Aucun profil adulte.",
    jellyfinCompatKeysHeading: "Clés d’intégration",
    jellyfinCompatKeysHelp:
      "Une clé permet à un outil comme Seerr ou Radarr de se connecter avec un profil jusqu’à ce que vous la révoquiez, sans code PIN ni expiration. Collez-la dans le champ de clé API de l’outil. Une clé peut lire et actualiser les bibliothèques ; elle ne peut jamais modifier les réglages.",
    jellyfinCompatKeysNoProfiles: "Activez un profil ci-dessus avant de créer une clé.",
    jellyfinCompatKeyName: "Nom de la clé",
    jellyfinCompatKeyProfile: "Profil",
    jellyfinCompatKeyCreate: "Créer une clé",
    jellyfinCompatKeysUnavailable: "Impossible de charger les clés d’intégration.",
    jellyfinCompatKeysEmpty: "Aucune clé d’intégration pour l’instant.",
    jellyfinCompatKeyCreateFailed: "Impossible de créer la clé : {reason}",
    jellyfinCompatKeyRevokeFailed: "Impossible de révoquer la clé : {reason}",
    jellyfinCompatKeyConfirmRevoke:
      "Révoquer cette clé ? L’outil qui l’utilise cesse de fonctionner immédiatement.",
  },
  de: {
    navJellyfinCompat: "Jellyfin-Kompatibilität",
    jellyfinCompatLead:
      "Eine Jellyfin-kompatible API für Automatisierungswerkzeuge wie Seerr, Radarr und Sonarr.",
    jellyfinCompatSetup:
      "Trage im Werkzeug als Jellyfin-Server-URL die Adresse dieses Servers gefolgt von /compat/jellyfin/ ein. Melde dich bei der Einrichtung als admin mit dem Administratorpasswort an; danach meldet sich das Werkzeug mit einem unten freigegebenen Profil und dessen PIN an. Die API ist standardmäßig aus. Eingeschaltet antwortet sie nur Clients in den erlaubten Netzwerken, nie über das Relay oder den Fernzugriff.",
    jellyfinCompatDisclaimer:
      "Beamfall ist weder mit Jellyfin verbunden noch von Jellyfin unterstützt. Der Name dient nur zur Beschreibung der Kompatibilität.",
    jellyfinCompatUnavailable: "Die Kompatibilitätseinstellungen konnten nicht geladen werden.",
    jellyfinCompatEnabled: "Kompatibilitäts-API",
    jellyfinCompatEnabledHelp: "Änderungen gelten sofort, ohne Neustart.",
    jellyfinCompatNetworksHeading: "Netzwerke",
    jellyfinCompatAllowedCIDRs: "Erlaubte Netzwerke",
    jellyfinCompatAllowedCIDRsHelp:
      "Ein Netzwerk pro Zeile, etwa 192.168.1.0/24. Nur Loopback-, private, Link-Local- und IPv6-Unique-Local-Bereiche werden akzeptiert.",
    jellyfinCompatProxyCIDRs: "Vertrauenswürdige Proxy-Netzwerke",
    jellyfinCompatProxyCIDRsHelp:
      "Ein vertrauenswürdiger Reverse-Proxy muss X-Forwarded-For überschreiben, nicht ergänzen, sonst können Clients ihre Adresse fälschen.",
    jellyfinCompatSaveNetworks: "Netzwerke speichern",
    jellyfinCompatSaved: "Gespeichert.",
    jellyfinCompatSaveFailed: "Speichern fehlgeschlagen: {reason}",
    jellyfinCompatLibrariesHeading: "Freigegebene Mediatheken",
    jellyfinCompatLibrariesHelp:
      "Werkzeuge sehen nur die hier angehakten Mediatheken. Eingeschränkte Mediatheken werden nie aufgeführt.",
    jellyfinCompatLibrariesEmpty: "Keine Mediathek kann freigegeben werden.",
    jellyfinCompatProfilesHeading: "Profile mit Anmeldung",
    jellyfinCompatProfilesHelp:
      "Ein Werkzeug meldet sich mit einem dieser Profile an. Wird ein Profil ausgeschaltet, wird es sofort von allen Kompatibilitätswerkzeugen abgemeldet.",
    jellyfinCompatProfilesEmpty: "Keine Erwachsenenprofile.",
    jellyfinCompatKeysHeading: "Integrationsschlüssel",
    jellyfinCompatKeysHelp:
      "Mit einem Schlüssel meldet sich ein Tool wie Seerr oder Radarr als Profil an, bis du ihn widerrufst – ohne PIN und ohne Ablauf. Füge ihn in das API-Schlüssel-Feld des Tools ein. Ein Schlüssel kann Mediatheken lesen und aktualisieren, aber nie Einstellungen ändern.",
    jellyfinCompatKeysNoProfiles: "Aktiviere oben ein Profil, bevor du einen Schlüssel erstellst.",
    jellyfinCompatKeyName: "Schlüsselname",
    jellyfinCompatKeyProfile: "Profil",
    jellyfinCompatKeyCreate: "Schlüssel erstellen",
    jellyfinCompatKeysUnavailable: "Die Integrationsschlüssel konnten nicht geladen werden.",
    jellyfinCompatKeysEmpty: "Noch keine Integrationsschlüssel.",
    jellyfinCompatKeyCreateFailed: "Schlüssel konnte nicht erstellt werden: {reason}",
    jellyfinCompatKeyRevokeFailed: "Schlüssel konnte nicht widerrufen werden: {reason}",
    jellyfinCompatKeyConfirmRevoke:
      "Diesen Schlüssel widerrufen? Das Tool, das ihn verwendet, funktioniert sofort nicht mehr.",
  },
  es: {
    navJellyfinCompat: "Compatibilidad con Jellyfin",
    jellyfinCompatLead:
      "Una API compatible con Jellyfin para herramientas de automatización como Seerr, Radarr y Sonarr.",
    jellyfinCompatSetup:
      "En la herramienta, usa como URL del servidor Jellyfin la dirección de este servidor seguida de /compat/jellyfin/. Durante la configuración, inicia sesión como admin con la contraseña de administrador; después, la herramienta inicia sesión con un perfil habilitado abajo y el PIN de ese perfil. La API está desactivada de forma predeterminada. Cuando está activada, solo responde a clientes de las redes permitidas, nunca a través del relé ni del acceso remoto.",
    jellyfinCompatDisclaimer:
      "Beamfall no está afiliado a Jellyfin ni cuenta con su respaldo. El nombre solo se usa para describir la compatibilidad.",
    jellyfinCompatUnavailable: "No se pudieron cargar los ajustes de compatibilidad.",
    jellyfinCompatEnabled: "API de compatibilidad",
    jellyfinCompatEnabledHelp: "Los cambios se aplican al instante, sin reiniciar.",
    jellyfinCompatNetworksHeading: "Redes",
    jellyfinCompatAllowedCIDRs: "Redes permitidas",
    jellyfinCompatAllowedCIDRsHelp:
      "Una red por línea, como 192.168.1.0/24. Solo se aceptan rangos de loopback, privados, link-local e IPv6 unique-local.",
    jellyfinCompatProxyCIDRs: "Redes de proxy de confianza",
    jellyfinCompatProxyCIDRsHelp:
      "Un proxy inverso de confianza debe sobrescribir X-Forwarded-For, no añadirle valores, o los clientes podrán falsificar su dirección.",
    jellyfinCompatSaveNetworks: "Guardar redes",
    jellyfinCompatSaved: "Guardado.",
    jellyfinCompatSaveFailed: "No se pudo guardar: {reason}",
    jellyfinCompatLibrariesHeading: "Bibliotecas compartidas",
    jellyfinCompatLibrariesHelp:
      "Las herramientas solo ven las bibliotecas marcadas aquí. Las bibliotecas restringidas nunca aparecen.",
    jellyfinCompatLibrariesEmpty: "No se puede compartir ninguna biblioteca.",
    jellyfinCompatProfilesHeading: "Perfiles que pueden iniciar sesión",
    jellyfinCompatProfilesHelp:
      "Una herramienta inicia sesión con uno de estos perfiles. Desactivar un perfil cierra su sesión en todas las herramientas de compatibilidad a la vez.",
    jellyfinCompatProfilesEmpty: "No hay perfiles de adulto.",
    jellyfinCompatKeysHeading: "Claves de integración",
    jellyfinCompatKeysHelp:
      "Una clave permite que una herramienta como Seerr o Radarr inicie sesión con un perfil hasta que la revoques, sin PIN y sin caducidad. Pégala en el campo de clave de API de la herramienta. Una clave puede leer y actualizar bibliotecas, pero nunca cambiar ajustes.",
    jellyfinCompatKeysNoProfiles: "Activa un perfil arriba antes de crear una clave.",
    jellyfinCompatKeyName: "Nombre de la clave",
    jellyfinCompatKeyProfile: "Perfil",
    jellyfinCompatKeyCreate: "Crear clave",
    jellyfinCompatKeysUnavailable: "No se pudieron cargar las claves de integración.",
    jellyfinCompatKeysEmpty: "Aún no hay claves de integración.",
    jellyfinCompatKeyCreateFailed: "No se pudo crear la clave: {reason}",
    jellyfinCompatKeyRevokeFailed: "No se pudo revocar la clave: {reason}",
    jellyfinCompatKeyConfirmRevoke:
      "¿Revocar esta clave? La herramienta que la usa deja de funcionar al instante.",
  },
  pt: {
    navJellyfinCompat: "Compatibilidade com Jellyfin",
    jellyfinCompatLead:
      "Uma API compatível com Jellyfin para ferramentas de automação como Seerr, Radarr e Sonarr.",
    jellyfinCompatSetup:
      "Na ferramenta, use como URL do servidor Jellyfin o endereço deste servidor seguido de /compat/jellyfin/. Durante a configuração, entre como admin com a senha de administrador; depois, a ferramenta entra com um perfil habilitado abaixo e o PIN desse perfil. A API fica desativada por padrão. Quando ativada, responde apenas a clientes das redes permitidas, nunca pelo relay nem pelo acesso remoto.",
    jellyfinCompatDisclaimer:
      "O Beamfall não é afiliado ao Jellyfin nem endossado por ele. O nome é usado apenas para descrever a compatibilidade.",
    jellyfinCompatUnavailable: "Não foi possível carregar as configurações de compatibilidade.",
    jellyfinCompatEnabled: "API de compatibilidade",
    jellyfinCompatEnabledHelp: "As alterações valem na hora, sem reiniciar.",
    jellyfinCompatNetworksHeading: "Redes",
    jellyfinCompatAllowedCIDRs: "Redes permitidas",
    jellyfinCompatAllowedCIDRsHelp:
      "Uma rede por linha, como 192.168.1.0/24. Apenas faixas de loopback, privadas, link-local e IPv6 unique-local são aceitas.",
    jellyfinCompatProxyCIDRs: "Redes de proxy confiáveis",
    jellyfinCompatProxyCIDRsHelp:
      "Um proxy reverso confiável deve sobrescrever X-Forwarded-For, não acrescentar a ele, ou os clientes podem falsificar o próprio endereço.",
    jellyfinCompatSaveNetworks: "Salvar redes",
    jellyfinCompatSaved: "Salvo.",
    jellyfinCompatSaveFailed: "Não foi possível salvar: {reason}",
    jellyfinCompatLibrariesHeading: "Bibliotecas compartilhadas",
    jellyfinCompatLibrariesHelp:
      "As ferramentas veem apenas as bibliotecas marcadas aqui. Bibliotecas restritas nunca aparecem.",
    jellyfinCompatLibrariesEmpty: "Nenhuma biblioteca pode ser compartilhada.",
    jellyfinCompatProfilesHeading: "Perfis que podem entrar",
    jellyfinCompatProfilesHelp:
      "Uma ferramenta entra com um destes perfis. Desativar um perfil encerra a sessão dele em todas as ferramentas de compatibilidade de uma vez.",
    jellyfinCompatProfilesEmpty: "Nenhum perfil adulto.",
    jellyfinCompatKeysHeading: "Chaves de integração",
    jellyfinCompatKeysHelp:
      "Uma chave permite que uma ferramenta como Seerr ou Radarr entre com um perfil até você revogá-la, sem PIN e sem expiração. Cole-a no campo de chave de API da ferramenta. Uma chave pode ler e atualizar bibliotecas, mas nunca alterar configurações.",
    jellyfinCompatKeysNoProfiles: "Ative um perfil acima antes de criar uma chave.",
    jellyfinCompatKeyName: "Nome da chave",
    jellyfinCompatKeyProfile: "Perfil",
    jellyfinCompatKeyCreate: "Criar chave",
    jellyfinCompatKeysUnavailable: "Não foi possível carregar as chaves de integração.",
    jellyfinCompatKeysEmpty: "Ainda não há chaves de integração.",
    jellyfinCompatKeyCreateFailed: "Não foi possível criar a chave: {reason}",
    jellyfinCompatKeyRevokeFailed: "Não foi possível revogar a chave: {reason}",
    jellyfinCompatKeyConfirmRevoke:
      "Revogar esta chave? A ferramenta que a usa para de funcionar imediatamente.",
  },
} as const;

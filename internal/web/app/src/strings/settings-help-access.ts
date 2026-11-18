// Help text (CORE-2445) for the access settings: the (?) popovers and the preset
// dropdown choices. Every key ships in every locale.
export const settingsHelpAccessStrings = {
  en: {
    // Profiles
    accessHelpProfilesLead:
      "Add or remove the people who watch on this server. Each profile keeps its own watch history and settings. A profile is not a sign-in: devices sign in, then pick a profile.",
    accessHelpProfileTypeLabel: "Profile type",
    accessHelpProfileTypeChild: "Child (managed)",
    accessHelpProfileTypeTip:
      "Adult profiles see everything their libraries allow. Child (managed) profiles are for children: you set their limits, such as viewing times and rating limits, on the Schedules page.",
    // Devices
    accessHelpPairingCodesLabel: "Viewer and manager codes",
    accessHelpPairingCodesTip:
      "A viewer code adds a device that can only watch, such as a TV or a guest's phone. A manager code adds a device that can also change server settings, so use it only for your own devices. Each code works once and expires after 10 minutes.",
    accessHelpRemoteEnrollTip:
      "Lets a new device join with a pairing code while it is away from your home network. Off by default. Devices that are already paired keep working either way.",
    accessHelpRemoteEnrollLink: "Remote access settings",
    // Sign-in security
    accessHelpStrictTip:
      "Only paired devices can sign in. The admin password stops working in a web browser. If you lose every paired device, you have to run a command on the server to get back in.",
    accessHelpAdminPasswordHint: "Enter it to confirm the change.",
    // Schedules (parental controls)
    accessHelpManagedFraming:
      "Limits you set for this child's profile. The child can't change them, and they are separate from the profile's own settings.",
    accessHelpScheduleLabel: "Viewing times",
    accessHelpScheduleTip:
      "The times of the week this profile may watch, by the server's clock. Outside these times it can't play anything. With no times added, it can watch at any time.",
    accessHelpScheduleFrom: "From",
    accessHelpScheduleUntil: "Until",
    accessHelpStreamCapLabel: "Streams at the same time",
    accessHelpStreamCapTip:
      "How many videos this profile can play at once, across all its devices. Choose No limit to remove the cap. The current setting is not shown here, so pick a value to set it.",
    accessHelpStreamCapPlaceholder: "Choose a limit",
    accessHelpStreamCount: "{count, plural, one {# stream} other {# streams}}",
    accessHelpNoLimit: "No limit",
    accessHelpRemoteCapLabel: "Quality limit away from home",
    accessHelpProfileRemoteCapTip:
      "Caps video quality for this profile when it watches away from home. Lower limits use less of your internet upload. If the server-wide limit is lower, that one applies.",
    accessHelpBitrate2: "2 Mbit/s · phone, low quality",
  },
  fr: {
    accessHelpProfilesLead:
      "Ajoutez ou retirez les personnes qui regardent sur ce serveur. Chaque profil garde son propre historique et ses réglages. Un profil n’est pas une connexion : les appareils se connectent, puis choisissent un profil.",
    accessHelpProfileTypeLabel: "Type de profil",
    accessHelpProfileTypeChild: "Enfant (géré)",
    accessHelpProfileTypeTip:
      "Les profils adultes voient tout ce que leurs bibliothèques permettent. Les profils Enfant (géré) sont pour les enfants : vous fixez leurs limites, comme les horaires et les limites d’âge, sur la page Horaires.",
    accessHelpPairingCodesLabel: "Codes spectateur et gestionnaire",
    accessHelpPairingCodesTip:
      "Un code spectateur ajoute un appareil qui peut seulement regarder, comme une télé ou le téléphone d’un invité. Un code gestionnaire ajoute un appareil qui peut aussi modifier les réglages du serveur : réservez-le à vos propres appareils. Chaque code ne sert qu’une fois et expire après 10 minutes.",
    accessHelpRemoteEnrollTip:
      "Permet à un nouvel appareil de se jumeler avec un code alors qu’il est hors de votre réseau domestique. Désactivé par défaut. Les appareils déjà jumelés continuent de fonctionner dans tous les cas.",
    accessHelpRemoteEnrollLink: "Réglages d’accès à distance",
    accessHelpStrictTip:
      "Seuls les appareils jumelés peuvent se connecter. Le mot de passe administrateur ne fonctionne plus dans un navigateur. Si vous perdez tous vos appareils jumelés, vous devez lancer une commande sur le serveur pour reprendre la main.",
    accessHelpAdminPasswordHint: "Saisissez-le pour confirmer le changement.",
    accessHelpManagedFraming:
      "Limites que vous fixez pour le profil de cet enfant. L’enfant ne peut pas les modifier, et elles sont distinctes des réglages propres au profil.",
    accessHelpScheduleLabel: "Horaires de visionnage",
    accessHelpScheduleTip:
      "Les moments de la semaine où ce profil peut regarder, selon l’horloge du serveur. En dehors de ces horaires, il ne peut rien lire. Sans horaire ajouté, il peut regarder à tout moment.",
    accessHelpScheduleFrom: "De",
    accessHelpScheduleUntil: "Jusqu’à",
    accessHelpStreamCapLabel: "Lectures simultanées",
    accessHelpStreamCapTip:
      "Le nombre de vidéos que ce profil peut lire en même temps, sur tous ses appareils. Choisissez Aucune limite pour supprimer la limite. Le réglage actuel n’est pas affiché ici : choisissez une valeur pour le définir.",
    accessHelpStreamCapPlaceholder: "Choisir une limite",
    accessHelpStreamCount: "{count, plural, one {# lecture} other {# lectures}}",
    accessHelpNoLimit: "Aucune limite",
    accessHelpRemoteCapLabel: "Limite de qualité hors du domicile",
    accessHelpProfileRemoteCapTip:
      "Limite la qualité vidéo de ce profil quand il regarde hors du domicile. Une limite plus basse utilise moins de votre débit montant. Si la limite du serveur est plus basse, c’est elle qui s’applique.",
    accessHelpBitrate2: "2 Mbit/s · téléphone, basse qualité",
  },
  de: {
    accessHelpProfilesLead:
      "Fügen Sie die Personen hinzu, die auf diesem Server schauen, oder entfernen Sie sie. Jedes Profil hat einen eigenen Verlauf und eigene Einstellungen. Ein Profil ist keine Anmeldung: Geräte melden sich an und wählen dann ein Profil.",
    accessHelpProfileTypeLabel: "Profiltyp",
    accessHelpProfileTypeChild: "Kind (verwaltet)",
    accessHelpProfileTypeTip:
      "Erwachsenenprofile sehen alles, was ihre Mediatheken erlauben. Profile vom Typ Kind (verwaltet) sind für Kinder: Ihre Grenzen, etwa Sehzeiten und Altersgrenzen, legen Sie auf der Seite Zeitpläne fest.",
    accessHelpPairingCodesLabel: "Zuschauer- und Verwaltercodes",
    accessHelpPairingCodesTip:
      "Ein Zuschauercode fügt ein Gerät hinzu, das nur abspielen kann, etwa einen Fernseher oder das Handy eines Gastes. Ein Verwaltercode fügt ein Gerät hinzu, das auch Servereinstellungen ändern kann. Nutzen Sie ihn nur für Ihre eigenen Geräte. Jeder Code funktioniert einmal und läuft nach 10 Minuten ab.",
    accessHelpRemoteEnrollTip:
      "Erlaubt einem neuen Gerät, sich mit einem Kopplungscode anzumelden, während es nicht in Ihrem Heimnetz ist. Standardmäßig aus. Bereits gekoppelte Geräte funktionieren in jedem Fall weiter.",
    accessHelpRemoteEnrollLink: "Einstellungen für den Fernzugriff",
    accessHelpStrictTip:
      "Nur gekoppelte Geräte können sich anmelden. Das Administratorpasswort funktioniert im Webbrowser nicht mehr. Wenn Sie alle gekoppelten Geräte verlieren, müssen Sie einen Befehl auf dem Server ausführen, um wieder Zugang zu bekommen.",
    accessHelpAdminPasswordHint: "Geben Sie es ein, um die Änderung zu bestätigen.",
    accessHelpManagedFraming:
      "Grenzen, die Sie für das Profil dieses Kindes festlegen. Das Kind kann sie nicht ändern, und sie sind von den eigenen Einstellungen des Profils getrennt.",
    accessHelpScheduleLabel: "Sehzeiten",
    accessHelpScheduleTip:
      "Die Zeiten in der Woche, zu denen dieses Profil schauen darf, nach der Uhr des Servers. Außerhalb dieser Zeiten kann es nichts abspielen. Ohne eingetragene Zeiten kann es jederzeit schauen.",
    accessHelpScheduleFrom: "Von",
    accessHelpScheduleUntil: "Bis",
    accessHelpStreamCapLabel: "Gleichzeitige Wiedergaben",
    accessHelpStreamCapTip:
      "Wie viele Videos dieses Profil gleichzeitig abspielen kann, über alle seine Geräte. Wählen Sie Keine Begrenzung, um die Grenze aufzuheben. Die aktuelle Einstellung wird hier nicht angezeigt. Wählen Sie einen Wert, um sie festzulegen.",
    accessHelpStreamCapPlaceholder: "Grenze wählen",
    accessHelpStreamCount: "{count, plural, one {# Wiedergabe} other {# Wiedergaben}}",
    accessHelpNoLimit: "Keine Begrenzung",
    accessHelpRemoteCapLabel: "Qualitätsgrenze unterwegs",
    accessHelpProfileRemoteCapTip:
      "Begrenzt die Videoqualität für dieses Profil, wenn es außer Haus schaut. Niedrigere Grenzen brauchen weniger von Ihrem Internet-Upload. Ist die serverweite Grenze niedriger, gilt diese.",
    accessHelpBitrate2: "2 Mbit/s · Handy, niedrige Qualität",
  },
  es: {
    accessHelpProfilesLead:
      "Añade o quita a las personas que ven contenido en este servidor. Cada perfil guarda su propio historial y sus ajustes. Un perfil no es un inicio de sesión: los dispositivos inician sesión y luego eligen un perfil.",
    accessHelpProfileTypeLabel: "Tipo de perfil",
    accessHelpProfileTypeChild: "Infantil (gestionado)",
    accessHelpProfileTypeTip:
      "Los perfiles de adulto ven todo lo que permiten sus bibliotecas. Los perfiles Infantil (gestionado) son para niños: sus límites, como los horarios y la clasificación por edad, se fijan en la página Horarios.",
    accessHelpPairingCodesLabel: "Códigos de espectador y de gestor",
    accessHelpPairingCodesTip:
      "Un código de espectador añade un dispositivo que solo puede ver, como una tele o el móvil de un invitado. Un código de gestor añade un dispositivo que también puede cambiar los ajustes del servidor, así que úsalo solo con tus propios dispositivos. Cada código sirve una vez y caduca a los 10 minutos.",
    accessHelpRemoteEnrollTip:
      "Permite que un dispositivo nuevo se vincule con un código mientras está fuera de tu red doméstica. Desactivado por defecto. Los dispositivos ya vinculados siguen funcionando en cualquier caso.",
    accessHelpRemoteEnrollLink: "Ajustes de acceso remoto",
    accessHelpStrictTip:
      "Solo los dispositivos vinculados pueden iniciar sesión. La contraseña de administrador deja de funcionar en un navegador. Si pierdes todos los dispositivos vinculados, tendrás que ejecutar un comando en el servidor para volver a entrar.",
    accessHelpAdminPasswordHint: "Escríbela para confirmar el cambio.",
    accessHelpManagedFraming:
      "Límites que fijas para el perfil de este niño. El niño no puede cambiarlos y son independientes de los ajustes propios del perfil.",
    accessHelpScheduleLabel: "Horarios de visionado",
    accessHelpScheduleTip:
      "Los momentos de la semana en que este perfil puede ver contenido, según el reloj del servidor. Fuera de ese horario no puede reproducir nada. Sin horarios añadidos, puede ver en cualquier momento.",
    accessHelpScheduleFrom: "Desde",
    accessHelpScheduleUntil: "Hasta",
    accessHelpStreamCapLabel: "Reproducciones a la vez",
    accessHelpStreamCapTip:
      "Cuántos vídeos puede reproducir este perfil a la vez, en todos sus dispositivos. Elige Sin límite para quitar el límite. El ajuste actual no se muestra aquí, así que elige un valor para fijarlo.",
    accessHelpStreamCapPlaceholder: "Elige un límite",
    accessHelpStreamCount: "{count, plural, one {# reproducción} other {# reproducciones}}",
    accessHelpNoLimit: "Sin límite",
    accessHelpRemoteCapLabel: "Límite de calidad fuera de casa",
    accessHelpProfileRemoteCapTip:
      "Limita la calidad de vídeo de este perfil cuando ve contenido fuera de casa. Un límite más bajo usa menos de tu subida a internet. Si el límite de todo el servidor es más bajo, se aplica ese.",
    accessHelpBitrate2: "2 Mbit/s · móvil, baja calidad",
  },
  pt: {
    accessHelpProfilesLead:
      "Adicione ou remova as pessoas que assistem neste servidor. Cada perfil guarda seu próprio histórico e suas configurações. Um perfil não é um login: os dispositivos entram e depois escolhem um perfil.",
    accessHelpProfileTypeLabel: "Tipo de perfil",
    accessHelpProfileTypeChild: "Infantil (gerenciado)",
    accessHelpProfileTypeTip:
      "Perfis adultos veem tudo o que suas bibliotecas permitem. Perfis Infantil (gerenciado) são para crianças: os limites deles, como horários e classificação etária, são definidos na página Horários.",
    accessHelpPairingCodesLabel: "Códigos de espectador e de gestor",
    accessHelpPairingCodesTip:
      "Um código de espectador adiciona um dispositivo que só pode assistir, como uma TV ou o celular de um convidado. Um código de gestor adiciona um dispositivo que também pode mudar as configurações do servidor, então use-o só nos seus próprios dispositivos. Cada código funciona uma vez e expira após 10 minutos.",
    accessHelpRemoteEnrollTip:
      "Permite que um dispositivo novo seja pareado com um código enquanto está fora da sua rede doméstica. Desativado por padrão. Os dispositivos já pareados continuam funcionando de qualquer forma.",
    accessHelpRemoteEnrollLink: "Configurações de acesso remoto",
    accessHelpStrictTip:
      "Só dispositivos pareados podem entrar. A senha de administrador deixa de funcionar no navegador. Se você perder todos os dispositivos pareados, terá de executar um comando no servidor para voltar a entrar.",
    accessHelpAdminPasswordHint: "Digite-a para confirmar a mudança.",
    accessHelpManagedFraming:
      "Limites que você define para o perfil desta criança. A criança não pode mudá-los, e eles são separados das configurações do próprio perfil.",
    accessHelpScheduleLabel: "Horários para assistir",
    accessHelpScheduleTip:
      "Os horários da semana em que este perfil pode assistir, pelo relógio do servidor. Fora desses horários, não pode reproduzir nada. Sem horários adicionados, pode assistir a qualquer hora.",
    accessHelpScheduleFrom: "Das",
    accessHelpScheduleUntil: "Até",
    accessHelpStreamCapLabel: "Reproduções ao mesmo tempo",
    accessHelpStreamCapTip:
      "Quantos vídeos este perfil pode reproduzir ao mesmo tempo, em todos os seus dispositivos. Escolha Sem limite para remover o limite. A configuração atual não aparece aqui, então escolha um valor para defini-la.",
    accessHelpStreamCapPlaceholder: "Escolha um limite",
    accessHelpStreamCount: "{count, plural, one {# reprodução} other {# reproduções}}",
    accessHelpNoLimit: "Sem limite",
    accessHelpRemoteCapLabel: "Limite de qualidade fora de casa",
    accessHelpProfileRemoteCapTip:
      "Limita a qualidade do vídeo deste perfil quando ele assiste fora de casa. Limites menores usam menos do seu upload de internet. Se o limite do servidor for menor, vale o do servidor.",
    accessHelpBitrate2: "2 Mbit/s · celular, baixa qualidade",
  },
};

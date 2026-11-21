// Setting up Restrictions from the web: the Restrictions PIN in Manage ›
// Restrictions and the per-Library Restricted switch in Console › Libraries
// (CORE-2463), plus direction PIN setup for Restrictions and Profile entry (CORE-2464).
export const restrictionsSetupStrings = {
  en: {
    restrictionsSetupSubtitle: "Set a Restrictions PIN to control who can show restricted content.",
    restrictionsSetupLead:
      "No Restrictions PIN is set yet, so nothing is hidden. To hide a Library:",
    restrictionsSetupStepPin: "Set a PIN below. Anyone who knows it can show restricted content.",
    restrictionsSetupStepLibrary:
      "Unlock with the PIN, then turn on Restricted for the Library in Console › Libraries.",
    restrictionsSetPin: "Set PIN",
    restrictionsChangePin: "Change PIN",
    restrictionsNewPin: "New PIN",
    restrictionsConfirmPin: "Confirm PIN",
    restrictionsPinMismatch: "The two PINs don't match.",
    restrictionsPinSaved: "PIN saved.",
    restrictionsPinNotSaved: "The PIN was not saved. Try again.",
    restrictionsPinNoRevealPath: "This profile can't have a Restrictions PIN.",
    restrictionsLibrariesLink: "Choose restricted Libraries in Console › Libraries",
    libraryRestrictedLabel: "Restricted",
    libraryRestrictedHelp: "Hidden until someone unlocks with the Restrictions PIN.",
    libraryRestrictedTip:
      "A restricted Library disappears from every profile's browsing, search and Home until the session is unlocked with the Restrictions PIN (Manage › Restrictions).",
    libraryRestrictedNeedsUnlock:
      "Unlock with the Restrictions PIN first (Manage › Restrictions) to change this.",
    libraryRestrictedNotSaved: "The change was not saved. Try again.",
    pinSetupDigits: "Digits",
    pinSetupDirections: "Directions",
    pinSetupDirectionsHelp:
      "Use at least 6 steps. Use the arrow buttons or the keyboard arrow keys.",
    pinSetupTVWarning:
      "This PIN cannot be entered with a TV remote. Use only 2, 4, 6 and 8 for TV entry.",
    pinSetupSwipePrivacy: "Swipes are easier for someone nearby to see than keypad taps.",
    pinDirectionUp: "Up",
    pinDirectionLeft: "Left",
    pinDirectionRight: "Right",
    pinDirectionDown: "Down",
    pinSetupRemoveStep: "Remove last step",
    pinSetupProfileAction: "Entry PIN",
    pinSetupProfileTitle: "Entry PIN for {name}",
    pinSetupProfileHelp: "Use this PIN when switching to this profile.",
  },
  fr: {
    restrictionsSetupSubtitle:
      "Définissez un code PIN de restriction pour contrôler qui peut afficher le contenu restreint.",
    restrictionsSetupLead:
      "Aucun PIN de restriction n’est défini, donc rien n’est masqué. Pour masquer une bibliothèque :",
    restrictionsSetupStepPin:
      "Définissez un PIN ci-dessous. Quiconque le connaît peut afficher le contenu restreint.",
    restrictionsSetupStepLibrary:
      "Déverrouillez avec le PIN, puis activez Restreinte pour la bibliothèque dans Console › Bibliothèques.",
    restrictionsSetPin: "Définir le PIN",
    restrictionsChangePin: "Changer le PIN",
    restrictionsNewPin: "Nouveau PIN",
    restrictionsConfirmPin: "Confirmer le PIN",
    restrictionsPinMismatch: "Les deux PIN ne correspondent pas.",
    restrictionsPinSaved: "PIN enregistré.",
    restrictionsPinNotSaved: "Le PIN n’a pas été enregistré. Réessayez.",
    restrictionsPinNoRevealPath: "Ce profil ne peut pas avoir de PIN de restriction.",
    restrictionsLibrariesLink: "Choisir les bibliothèques restreintes dans Console › Bibliothèques",
    libraryRestrictedLabel: "Restreinte",
    libraryRestrictedHelp: "Masquée jusqu’au déverrouillage avec le PIN de restriction.",
    libraryRestrictedTip:
      "Une bibliothèque restreinte disparaît de la navigation, de la recherche et de l’accueil de chaque profil jusqu’à ce que la session soit déverrouillée avec le PIN de restriction (Gérer › Restrictions).",
    libraryRestrictedNeedsUnlock:
      "Déverrouillez d’abord avec le PIN de restriction (Gérer › Restrictions) pour modifier ce réglage.",
    libraryRestrictedNotSaved: "La modification n’a pas été enregistrée. Réessayez.",
    pinSetupDigits: "Chiffres",
    pinSetupDirections: "Directions",
    pinSetupDirectionsHelp:
      "Saisissez au moins 6 directions avec les boutons fléchés ou les flèches du clavier.",
    pinSetupTVWarning:
      "Ce PIN ne peut pas être saisi avec une télécommande TV. Pour la TV, utilisez uniquement 2, 4, 6 et 8.",
    pinSetupSwipePrivacy:
      "Une personne à proximité voit plus facilement les gestes de balayage que les pressions sur le pavé numérique.",
    pinDirectionUp: "Haut",
    pinDirectionLeft: "Gauche",
    pinDirectionRight: "Droite",
    pinDirectionDown: "Bas",
    pinSetupRemoveStep: "Supprimer la dernière direction",
    pinSetupProfileAction: "PIN d’accès",
    pinSetupProfileTitle: "PIN d’accès de {name}",
    pinSetupProfileHelp: "Utilisez ce PIN pour passer à ce profil.",
  },
  de: {
    restrictionsSetupSubtitle:
      "Legen Sie eine Einschränkungs-PIN fest, um zu bestimmen, wer eingeschränkte Inhalte anzeigen kann.",
    restrictionsSetupLead:
      "Es ist noch keine Einschränkungs-PIN festgelegt, daher ist nichts ausgeblendet. So blenden Sie eine Mediathek aus:",
    restrictionsSetupStepPin:
      "Legen Sie unten eine PIN fest. Wer sie kennt, kann eingeschränkte Inhalte zeigen.",
    restrictionsSetupStepLibrary:
      "Entsperren Sie mit der PIN und schalten Sie dann in Konsole › Mediatheken „Eingeschränkt“ für die Mediathek ein.",
    restrictionsSetPin: "PIN festlegen",
    restrictionsChangePin: "PIN ändern",
    restrictionsNewPin: "Neue PIN",
    restrictionsConfirmPin: "PIN bestätigen",
    restrictionsPinMismatch: "Die beiden PINs stimmen nicht überein.",
    restrictionsPinSaved: "PIN gespeichert.",
    restrictionsPinNotSaved: "Die PIN wurde nicht gespeichert. Versuchen Sie es erneut.",
    restrictionsPinNoRevealPath: "Dieses Profil kann keine Einschränkungs-PIN haben.",
    restrictionsLibrariesLink: "Eingeschränkte Mediatheken in Konsole › Mediatheken wählen",
    libraryRestrictedLabel: "Eingeschränkt",
    libraryRestrictedHelp: "Ausgeblendet, bis jemand mit der Einschränkungs-PIN entsperrt.",
    libraryRestrictedTip:
      "Eine eingeschränkte Mediathek verschwindet für jedes Profil aus Stöbern, Suche und Startseite, bis die Sitzung mit der Einschränkungs-PIN entsperrt wird (Verwalten › Einschränkungen).",
    libraryRestrictedNeedsUnlock:
      "Entsperren Sie zuerst mit der Einschränkungs-PIN (Verwalten › Einschränkungen), um dies zu ändern.",
    libraryRestrictedNotSaved: "Die Änderung wurde nicht gespeichert. Versuchen Sie es erneut.",
    pinSetupDigits: "Ziffern",
    pinSetupDirections: "Richtungen",
    pinSetupDirectionsHelp:
      "Geben Sie mindestens 6 Richtungen über die Pfeilschaltflächen oder die Pfeiltasten ein.",
    pinSetupTVWarning:
      "Diese PIN lässt sich nicht mit einer TV-Fernbedienung eingeben. Verwenden Sie dafür nur 2, 4, 6 und 8.",
    pinSetupSwipePrivacy:
      "Personen in Ihrer Nähe können Wischgesten leichter beobachten als Eingaben auf dem Ziffernfeld.",
    pinDirectionUp: "Oben",
    pinDirectionLeft: "Links",
    pinDirectionRight: "Rechts",
    pinDirectionDown: "Unten",
    pinSetupRemoveStep: "Letzte Richtung entfernen",
    pinSetupProfileAction: "Profil-PIN",
    pinSetupProfileTitle: "Profil-PIN für {name}",
    pinSetupProfileHelp: "Verwenden Sie diese PIN, um zu diesem Profil zu wechseln.",
  },
  es: {
    restrictionsSetupSubtitle:
      "Configura un PIN de restricción para controlar quién puede mostrar el contenido restringido.",
    restrictionsSetupLead:
      "Aún no hay un PIN de restricción, así que no se oculta nada. Para ocultar una biblioteca:",
    restrictionsSetupStepPin:
      "Define un PIN abajo. Quien lo conozca puede mostrar el contenido restringido.",
    restrictionsSetupStepLibrary:
      "Desbloquea con el PIN y activa Restringida para la biblioteca en Consola › Bibliotecas.",
    restrictionsSetPin: "Definir PIN",
    restrictionsChangePin: "Cambiar PIN",
    restrictionsNewPin: "PIN nuevo",
    restrictionsConfirmPin: "Confirmar PIN",
    restrictionsPinMismatch: "Los dos PIN no coinciden.",
    restrictionsPinSaved: "PIN guardado.",
    restrictionsPinNotSaved: "No se guardó el PIN. Inténtalo de nuevo.",
    restrictionsPinNoRevealPath: "Este perfil no puede tener un PIN de restricción.",
    restrictionsLibrariesLink: "Elegir bibliotecas restringidas en Consola › Bibliotecas",
    libraryRestrictedLabel: "Restringida",
    libraryRestrictedHelp: "Oculta hasta que alguien desbloquee con el PIN de restricción.",
    libraryRestrictedTip:
      "Una biblioteca restringida desaparece de la navegación, la búsqueda y el inicio de todos los perfiles hasta que la sesión se desbloquea con el PIN de restricción (Gestionar › Restricciones).",
    libraryRestrictedNeedsUnlock:
      "Desbloquea primero con el PIN de restricción (Gestionar › Restricciones) para cambiar esto.",
    libraryRestrictedNotSaved: "No se guardó el cambio. Inténtalo de nuevo.",
    pinSetupDigits: "Dígitos",
    pinSetupDirections: "Direcciones",
    pinSetupDirectionsHelp:
      "Introduce al menos 6 direcciones con los botones de flecha o las flechas del teclado.",
    pinSetupTVWarning:
      "Este PIN no se puede introducir con el mando del televisor. Para usar el mando, elige solo 2, 4, 6 y 8.",
    pinSetupSwipePrivacy:
      "Quien esté cerca puede ver más fácilmente los gestos de deslizamiento que las pulsaciones en el teclado numérico.",
    pinDirectionUp: "Arriba",
    pinDirectionLeft: "Izquierda",
    pinDirectionRight: "Derecha",
    pinDirectionDown: "Abajo",
    pinSetupRemoveStep: "Quitar la última dirección",
    pinSetupProfileAction: "PIN de acceso",
    pinSetupProfileTitle: "PIN de acceso de {name}",
    pinSetupProfileHelp: "Usa este PIN para cambiar a este perfil.",
  },
  pt: {
    restrictionsSetupSubtitle:
      "Defina um PIN de restrição para controlar quem pode mostrar conteúdo restrito.",
    restrictionsSetupLead:
      "Ainda não há um PIN de restrição, por isso nada fica oculto. Para ocultar uma biblioteca:",
    restrictionsSetupStepPin:
      "Defina um PIN abaixo. Quem o conhecer pode mostrar o conteúdo restrito.",
    restrictionsSetupStepLibrary:
      "Desbloqueie com o PIN e ative Restrita para a biblioteca em Consola › Bibliotecas.",
    restrictionsSetPin: "Definir PIN",
    restrictionsChangePin: "Alterar PIN",
    restrictionsNewPin: "Novo PIN",
    restrictionsConfirmPin: "Confirmar PIN",
    restrictionsPinMismatch: "Os dois PIN não coincidem.",
    restrictionsPinSaved: "PIN guardado.",
    restrictionsPinNotSaved: "O PIN não foi guardado. Tente novamente.",
    restrictionsPinNoRevealPath: "Este perfil não pode ter um PIN de restrição.",
    restrictionsLibrariesLink: "Escolher bibliotecas restritas em Consola › Bibliotecas",
    libraryRestrictedLabel: "Restrita",
    libraryRestrictedHelp: "Oculta até alguém desbloquear com o PIN de restrição.",
    libraryRestrictedTip:
      "Uma biblioteca restrita desaparece da navegação, da pesquisa e do início de todos os perfis até a sessão ser desbloqueada com o PIN de restrição (Gerir › Restrições).",
    libraryRestrictedNeedsUnlock:
      "Desbloqueie primeiro com o PIN de restrição (Gerir › Restrições) para alterar isto.",
    libraryRestrictedNotSaved: "A alteração não foi guardada. Tente novamente.",
    pinSetupDigits: "Dígitos",
    pinSetupDirections: "Direções",
    pinSetupDirectionsHelp:
      "Introduza pelo menos 6 direções com os botões de seta ou as teclas de seta.",
    pinSetupTVWarning:
      "Não é possível introduzir este PIN com o comando da TV. Para usar o comando, utilize apenas 2, 4, 6 e 8.",
    pinSetupSwipePrivacy:
      "Quem está por perto vê mais facilmente os gestos de deslizar do que os toques no teclado numérico.",
    pinDirectionUp: "Cima",
    pinDirectionLeft: "Esquerda",
    pinDirectionRight: "Direita",
    pinDirectionDown: "Baixo",
    pinSetupRemoveStep: "Remover a última direção",
    pinSetupProfileAction: "PIN de acesso",
    pinSetupProfileTitle: "PIN de acesso de {name}",
    pinSetupProfileHelp: "Use este PIN para mudar para este perfil.",
  },
};

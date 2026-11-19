// Continues settings-help.ts (CORE-2445), split to keep each string file under 20 KB.
export const settingsHelp2Strings = {
  en: {
    updateVersionUnknown: "Unknown",
    // Libraries
    previewClipsTip:
      "Makes short preview clips that play when you hover over or focus an item. Uses disk space and background time after each scan.",
    skipDetectionLabel: "Detect intros and credits",
    skipDetectionTip:
      "Finds intros and end credits so players can offer Skip buttons. Runs in the background after a scan.",
    // Optional features
    optionalFeaturesLead:
      "Extra tools you can switch on or off for the whole server. Features marked as detected are found automatically and cannot be switched.",
    capabilityConversion: "File conversion",
    capabilityConversionTip:
      "Lets you convert media files to other formats. Turning this off hides Manage → Convert and blocks new conversion requests.",
    capabilityRename: "Rename files",
    capabilityRenameTip:
      "Lets you rename files on disk, individually or with a naming pattern. Turning this off hides Manage → Rename and blocks new file rename requests.",
    capabilityShareMount: "Network shares",
    capabilityShareMountTip:
      "Whether this server can mount network shares (SMB or NFS) for libraries. It is detected from the operating system automatically, so there is nothing to switch.",
    // Leaving soon rules
    retentionPageLead:
      "Remove media automatically. Items that match a rule appear in the Leaving soon collection first, then move to Trash when the waiting period ends. Anyone can keep an item by holding it.",
    retentionRuleNameLabel: "Rule name",
    retentionRuleLibraryLabel: "Library",
    retentionRuleLibraryTip: "The library this rule removes items from.",
    retentionRuleLibraryPick: "Choose a library",
    retentionRuleSourceLabel: "Which items",
    retentionRuleSourceTip:
      "Which items in the library the rule applies to. Every item suits a library you clear out regularly. A custom rule is a smart collection query written as JSON.",
    retentionRuleSourceAll: "Every item in the library",
    retentionRuleSourceCustom: "Custom rule (JSON)",
    retentionRuleGraceLabel: "Days in Leaving soon",
    retentionRuleGraceTip:
      "How long a matching item stays in Leaving soon before it moves to Trash. Items in Trash can still be restored until Trash is emptied.",
    // Plugins, Health, Backups
    pluginBuiltInCannotDisable: "Built-in plugins cannot be turned off.",
    consoleScraperValidateTip: "Ask the provider whether this key works. Nothing is changed.",
    healthColumnsTip:
      "Unmatched: files not linked to a catalog entry. Broken: files missing or unreadable. Scrape failures: details could not be fetched. Missing artwork: no poster or cover. Duplicates: the same item found more than once. No scraper: no plugin can look up details for this library. Select a number to see and fix those items.",
    backupDestTip:
      "A folder on the server where backups are written, ideally on another disk or a network share. Choose the same folder later to see its history and restore.",
    stagedRestoreReplaceIdentityTip:
      "Also restore the server's identity, so paired devices and remote access keep working as before. Leave it off when moving a backup to a second, separate server.",
  },
  fr: {
    updateVersionUnknown: "Inconnue",
    previewClipsTip:
      "Crée de courts extraits qui se lisent quand vous survolez ou sélectionnez un élément. Utilise de l’espace disque et du temps de fond après chaque analyse.",
    skipDetectionLabel: "Détecter les génériques",
    skipDetectionTip:
      "Repère les génériques de début et de fin pour que les lecteurs proposent un bouton Passer. S’exécute en arrière-plan après une analyse.",
    optionalFeaturesLead:
      "Des outils supplémentaires à activer ou désactiver pour tout le serveur. Les fonctions marquées comme détectées sont trouvées automatiquement et ne peuvent pas être modifiées.",
    capabilityConversion: "Conversion de fichiers",
    capabilityConversionTip:
      "Permet de convertir les fichiers multimédias dans d’autres formats. Désactiver cette option masque Gérer → Convertir et bloque les nouvelles demandes de conversion.",
    capabilityRename: "Renommer les fichiers",
    capabilityRenameTip:
      "Permet de renommer les fichiers sur le disque, un par un ou selon un modèle. Désactiver cette option masque Gérer → Renommer et bloque les nouvelles demandes de renommage.",
    capabilityShareMount: "Partages réseau",
    capabilityShareMountTip:
      "Indique si ce serveur peut monter des partages réseau (SMB ou NFS) pour les bibliothèques. C’est détecté automatiquement depuis le système, il n’y a donc rien à modifier.",
    retentionPageLead:
      "Retirez des médias automatiquement. Les éléments qui correspondent à une règle apparaissent d’abord dans la collection « Bientôt retiré », puis passent à la corbeille à la fin du délai. Chacun peut garder un élément en le retenant.",
    retentionRuleNameLabel: "Nom de la règle",
    retentionRuleLibraryLabel: "Bibliothèque",
    retentionRuleLibraryTip: "La bibliothèque d’où cette règle retire des éléments.",
    retentionRuleLibraryPick: "Choisir une bibliothèque",
    retentionRuleSourceLabel: "Quels éléments",
    retentionRuleSourceTip:
      "Les éléments de la bibliothèque concernés par la règle. Tous les éléments convient à une bibliothèque que vous videz régulièrement. Une règle personnalisée est une requête de collection intelligente écrite en JSON.",
    retentionRuleSourceAll: "Tous les éléments de la bibliothèque",
    retentionRuleSourceCustom: "Règle personnalisée (JSON)",
    retentionRuleGraceLabel: "Jours dans « Bientôt retiré »",
    retentionRuleGraceTip:
      "Durée pendant laquelle un élément reste dans « Bientôt retiré » avant de passer à la corbeille. Les éléments de la corbeille peuvent être restaurés jusqu’à ce qu’elle soit vidée.",
    pluginBuiltInCannotDisable: "Les plugins intégrés ne peuvent pas être désactivés.",
    consoleScraperValidateTip:
      "Demande au fournisseur si cette clé fonctionne. Rien n’est modifié.",
    healthColumnsTip:
      "Non associés : fichiers liés à aucune fiche du catalogue. Défectueux : fichiers manquants ou illisibles. Échecs de récupération : les détails n’ont pas pu être obtenus. Illustrations manquantes : pas d’affiche ni de pochette. Doublons : le même élément trouvé plusieurs fois. Aucun récupérateur : aucun plugin ne peut chercher les détails de cette bibliothèque. Sélectionnez un nombre pour voir et corriger ces éléments.",
    backupDestTip:
      "Un dossier du serveur où les sauvegardes sont écrites, idéalement sur un autre disque ou un partage réseau. Choisissez le même dossier plus tard pour voir son historique et restaurer.",
    stagedRestoreReplaceIdentityTip:
      "Restaure aussi l’identité du serveur, pour que les appareils associés et l’accès à distance continuent de fonctionner. Laissez désactivé pour déplacer une sauvegarde vers un second serveur distinct.",
  },
  de: {
    updateVersionUnknown: "Unbekannt",
    previewClipsTip:
      "Erstellt kurze Vorschauclips, die abspielen, wenn Sie über einen Eintrag fahren oder ihn fokussieren. Kostet nach jedem Scan Speicherplatz und Hintergrundzeit.",
    skipDetectionLabel: "Intros und Abspann erkennen",
    skipDetectionTip:
      "Findet Intros und Abspann, damit Player eine Überspringen-Taste anbieten können. Läuft nach einem Scan im Hintergrund.",
    optionalFeaturesLead:
      "Zusätzliche Werkzeuge, die Sie für den ganzen Server ein- oder ausschalten können. Als erkannt markierte Funktionen werden automatisch gefunden und lassen sich nicht umschalten.",
    capabilityConversion: "Dateiumwandlung",
    capabilityConversionTip:
      "Wandelt Mediendateien in andere Formate um. Das Ausschalten blendet Verwalten → Umwandeln aus und blockiert neue Umwandlungsaufträge.",
    capabilityRename: "Dateien umbenennen",
    capabilityRenameTip:
      "Benennt Dateien auf dem Datenträger einzeln oder nach einem Muster um. Das Ausschalten blendet Verwalten → Umbenennen aus und blockiert neue Anfragen zum Umbenennen.",
    capabilityShareMount: "Netzwerkfreigaben",
    capabilityShareMountTip:
      "Ob dieser Server Netzwerkfreigaben (SMB oder NFS) für Bibliotheken einbinden kann. Das wird automatisch am Betriebssystem erkannt, es gibt also nichts umzuschalten.",
    retentionPageLead:
      "Medien automatisch entfernen. Passende Einträge erscheinen zuerst in der Sammlung „Bald entfernt“ und wandern nach Ablauf der Wartezeit in den Papierkorb. Jede Person kann einen Eintrag behalten, indem sie ihn zurückhält.",
    retentionRuleNameLabel: "Regelname",
    retentionRuleLibraryLabel: "Bibliothek",
    retentionRuleLibraryTip: "Die Bibliothek, aus der diese Regel Einträge entfernt.",
    retentionRuleLibraryPick: "Bibliothek wählen",
    retentionRuleSourceLabel: "Welche Einträge",
    retentionRuleSourceTip:
      "Für welche Einträge der Bibliothek die Regel gilt. Alle Einträge passt zu einer Bibliothek, die Sie regelmäßig leeren. Eine eigene Regel ist eine Abfrage für intelligente Sammlungen in JSON.",
    retentionRuleSourceAll: "Alle Einträge der Bibliothek",
    retentionRuleSourceCustom: "Eigene Regel (JSON)",
    retentionRuleGraceLabel: "Tage in „Bald entfernt“",
    retentionRuleGraceTip:
      "Wie lange ein passender Eintrag in „Bald entfernt“ bleibt, bevor er in den Papierkorb wandert. Einträge im Papierkorb lassen sich wiederherstellen, bis er geleert wird.",
    pluginBuiltInCannotDisable: "Integrierte Plugins lassen sich nicht ausschalten.",
    consoleScraperValidateTip:
      "Fragt beim Anbieter nach, ob dieser Schlüssel funktioniert. Es wird nichts geändert.",
    healthColumnsTip:
      "Nicht zugeordnet: Dateien ohne Katalogeintrag. Defekt: Dateien fehlen oder sind unlesbar. Abruffehler: Details konnten nicht geladen werden. Fehlendes Artwork: kein Poster oder Cover. Duplikate: derselbe Eintrag mehrfach gefunden. Kein Scraper: kein Plugin kann Details für diese Bibliothek nachschlagen. Wählen Sie eine Zahl, um diese Einträge zu sehen und zu beheben.",
    backupDestTip:
      "Ein Ordner auf dem Server, in den Sicherungen geschrieben werden, am besten auf einer anderen Platte oder Netzwerkfreigabe. Wählen Sie später denselben Ordner, um den Verlauf zu sehen und wiederherzustellen.",
    stagedRestoreReplaceIdentityTip:
      "Stellt auch die Identität des Servers wieder her, damit gekoppelte Geräte und der Fernzugriff weiter funktionieren. Aus lassen, wenn Sie eine Sicherung auf einen zweiten, getrennten Server übertragen.",
  },
  es: {
    updateVersionUnknown: "Desconocida",
    previewClipsTip:
      "Crea clips cortos de vista previa que se reproducen al pasar el ratón o enfocar un elemento. Usa espacio en disco y tiempo en segundo plano tras cada escaneo.",
    skipDetectionLabel: "Detectar intros y créditos",
    skipDetectionTip:
      "Encuentra las intros y los créditos finales para que los reproductores ofrezcan un botón Saltar. Se ejecuta en segundo plano tras un escaneo.",
    optionalFeaturesLead:
      "Herramientas adicionales que puedes activar o desactivar para todo el servidor. Las funciones marcadas como detectadas se encuentran automáticamente y no se pueden cambiar.",
    capabilityConversion: "Conversión de archivos",
    capabilityConversionTip:
      "Permite convertir archivos multimedia a otros formatos. Al desactivar esta opción se oculta Gestionar → Convertir y se bloquean las nuevas solicitudes de conversión.",
    capabilityRename: "Renombrar archivos",
    capabilityRenameTip:
      "Permite renombrar archivos en el disco, uno a uno o con un patrón. Al desactivar esta opción se oculta Gestionar → Renombrar y se bloquean las nuevas solicitudes de cambio de nombre.",
    capabilityShareMount: "Recursos compartidos de red",
    capabilityShareMountTip:
      "Si este servidor puede montar recursos compartidos de red (SMB o NFS) para las bibliotecas. Se detecta automáticamente desde el sistema operativo, así que no hay nada que cambiar.",
    retentionPageLead:
      "Quita contenido automáticamente. Los elementos que cumplen una regla aparecen primero en la colección «Se va pronto» y pasan a la papelera cuando termina el plazo. Cualquiera puede conservar un elemento reteniéndolo.",
    retentionRuleNameLabel: "Nombre de la regla",
    retentionRuleLibraryLabel: "Biblioteca",
    retentionRuleLibraryTip: "La biblioteca de la que esta regla quita elementos.",
    retentionRuleLibraryPick: "Elige una biblioteca",
    retentionRuleSourceLabel: "Qué elementos",
    retentionRuleSourceTip:
      "A qué elementos de la biblioteca se aplica la regla. Todos los elementos sirve para una biblioteca que vacías con regularidad. Una regla personalizada es una consulta de colección inteligente escrita en JSON.",
    retentionRuleSourceAll: "Todos los elementos de la biblioteca",
    retentionRuleSourceCustom: "Regla personalizada (JSON)",
    retentionRuleGraceLabel: "Días en «Se va pronto»",
    retentionRuleGraceTip:
      "Cuánto tiempo queda un elemento en «Se va pronto» antes de pasar a la papelera. Los elementos de la papelera se pueden restaurar hasta que se vacíe.",
    pluginBuiltInCannotDisable: "Los plugins integrados no se pueden desactivar.",
    consoleScraperValidateTip: "Pregunta al proveedor si esta clave funciona. No se cambia nada.",
    healthColumnsTip:
      "Sin coincidencia: archivos sin ficha en el catálogo. Dañados: archivos ausentes o ilegibles. Fallos de obtención: no se pudieron descargar los detalles. Sin ilustraciones: sin póster ni portada. Duplicados: el mismo elemento encontrado más de una vez. Sin buscador: ningún plugin puede buscar detalles para esta biblioteca. Selecciona un número para ver y corregir esos elementos.",
    backupDestTip:
      "Una carpeta del servidor donde se escriben las copias de seguridad, idealmente en otro disco o un recurso de red. Elige la misma carpeta más tarde para ver su historial y restaurar.",
    stagedRestoreReplaceIdentityTip:
      "Restaura también la identidad del servidor, para que los dispositivos vinculados y el acceso remoto sigan funcionando como antes. Déjalo desactivado al llevar una copia a un segundo servidor independiente.",
  },
  pt: {
    updateVersionUnknown: "Desconhecida",
    previewClipsTip:
      "Cria clipes curtos de prévia que tocam ao passar o mouse ou focar um item. Usa espaço em disco e tempo em segundo plano após cada varredura.",
    skipDetectionLabel: "Detectar aberturas e créditos",
    skipDetectionTip:
      "Encontra aberturas e créditos finais para que os players ofereçam um botão Pular. Roda em segundo plano após uma varredura.",
    optionalFeaturesLead:
      "Ferramentas extras que você pode ativar ou desativar para todo o servidor. Recursos marcados como detectados são encontrados automaticamente e não podem ser alterados.",
    capabilityConversion: "Conversão de arquivos",
    capabilityConversionTip:
      "Permite converter arquivos de mídia para outros formatos. Desativar esta opção oculta Gerenciar → Converter e bloqueia novas solicitações de conversão.",
    capabilityRename: "Renomear arquivos",
    capabilityRenameTip:
      "Permite renomear arquivos no disco, um por vez ou seguindo um padrão. Desativar esta opção oculta Gerenciar → Renomear e bloqueia novas solicitações de renomeação.",
    capabilityShareMount: "Compartilhamentos de rede",
    capabilityShareMountTip:
      "Se este servidor pode montar compartilhamentos de rede (SMB ou NFS) para bibliotecas. É detectado automaticamente pelo sistema operacional, então não há nada para alterar.",
    retentionPageLead:
      "Remova mídia automaticamente. Itens que atendem a uma regra aparecem primeiro na coleção “Saindo em breve” e vão para a lixeira quando o prazo termina. Qualquer pessoa pode manter um item segurando-o.",
    retentionRuleNameLabel: "Nome da regra",
    retentionRuleLibraryLabel: "Biblioteca",
    retentionRuleLibraryTip: "A biblioteca da qual esta regra remove itens.",
    retentionRuleLibraryPick: "Escolha uma biblioteca",
    retentionRuleSourceLabel: "Quais itens",
    retentionRuleSourceTip:
      "A quais itens da biblioteca a regra se aplica. Todos os itens serve para uma biblioteca que você esvazia com frequência. Uma regra personalizada é uma consulta de coleção inteligente escrita em JSON.",
    retentionRuleSourceAll: "Todos os itens da biblioteca",
    retentionRuleSourceCustom: "Regra personalizada (JSON)",
    retentionRuleGraceLabel: "Dias em “Saindo em breve”",
    retentionRuleGraceTip:
      "Por quanto tempo um item fica em “Saindo em breve” antes de ir para a lixeira. Itens na lixeira ainda podem ser restaurados até ela ser esvaziada.",
    pluginBuiltInCannotDisable: "Plugins integrados não podem ser desativados.",
    consoleScraperValidateTip: "Pergunta ao provedor se esta chave funciona. Nada é alterado.",
    healthColumnsTip:
      "Sem correspondência: arquivos sem entrada no catálogo. Com defeito: arquivos ausentes ou ilegíveis. Falhas de busca: não foi possível obter os detalhes. Sem arte: sem pôster nem capa. Duplicados: o mesmo item encontrado mais de uma vez. Sem buscador: nenhum plugin consegue buscar detalhes para esta biblioteca. Selecione um número para ver e corrigir esses itens.",
    backupDestTip:
      "Uma pasta no servidor onde os backups são gravados, de preferência em outro disco ou compartilhamento de rede. Escolha a mesma pasta depois para ver o histórico e restaurar.",
    stagedRestoreReplaceIdentityTip:
      "Restaura também a identidade do servidor, para que dispositivos pareados e o acesso remoto continuem funcionando. Deixe desativado ao levar um backup para um segundo servidor separado.",
  },
};
